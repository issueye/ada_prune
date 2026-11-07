package command_code

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/learningdog1/ada_proxy/internal/gateway"
	"github.com/learningdog1/ada_proxy/internal/gateway/ir"
	"github.com/learningdog1/ada_proxy/internal/logging"
	"github.com/learningdog1/ada_proxy/internal/provider"
)

func testAdapter(t *testing.T) *Adapter {
	t.Helper()
	built, err := New(provider.Deps{
		Log:   logging.Discard(),
		State: memState{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return built.(*Adapter)
}

// memState is an in-memory gateway.StateStore for tests.
type memState map[string]any

func (m memState) key(providerID int64, key string) string {
	return string(rune(providerID)) + "|" + key
}

func (m memState) Get(providerID int64, key string, v any) (bool, error) {
	raw, ok := m[m.key(providerID, key)]
	if !ok {
		return false, nil
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(buf, v)
}

func (m memState) Set(providerID int64, key string, v any) error {
	m[m.key(providerID, key)] = v
	return nil
}

func (m memState) Delete(providerID int64, key string) error {
	delete(m, m.key(providerID, key))
	return nil
}

func TestKindAndCapabilities(t *testing.T) {
	a := testAdapter(t)
	if a.Kind() != provider.KindCommandCode {
		t.Fatalf("kind = %q", a.Kind())
	}
	caps := a.Capabilities()
	if !caps.Stream || !caps.Tools || !caps.Fingerprint || !caps.Quota || !caps.AccountUsage {
		t.Fatalf("capabilities = %+v", caps)
	}
	if caps.ProtocolVersion == "" {
		t.Fatal("protocol version is not reported")
	}
	if a.AccessMode() != gateway.AccessReverseProxy {
		t.Fatalf("access mode = %q, want reverse_proxy", a.AccessMode())
	}
}

func TestValidateConfig(t *testing.T) {
	a := testAdapter(t)

	ok := gateway.ProviderConfig{
		Kind: provider.KindCommandCode,
		AdapterConfig: map[string]any{
			"apiBase": "https://api.example.com",
			"cliMode": "agent",
		},
	}
	if err := a.ValidateConfig(ok); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	badURL := gateway.ProviderConfig{AdapterConfig: map[string]any{"apiBase": "ftp://nope"}}
	if err := a.ValidateConfig(badURL); err == nil {
		t.Fatal("accepted an ftp apiBase")
	}

	badMode := gateway.ProviderConfig{AdapterConfig: map[string]any{"cliMode": "turbo"}}
	if err := a.ValidateConfig(badMode); err == nil {
		t.Fatal("accepted an unknown cliMode")
	}

	badCatalog := gateway.ProviderConfig{AdapterConfig: map[string]any{"modelCatalog": "magic"}}
	if err := a.ValidateConfig(badCatalog); err == nil {
		t.Fatal("accepted an unknown modelCatalog")
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg, err := parseConfig(gateway.ProviderConfig{})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.APIBase != DefaultAPIBase || cfg.CLIMode != "agent" || cfg.ProjectDir != DefaultProjectDir {
		t.Fatalf("defaults = %+v", cfg)
	}
	if !cfg.EmptySystemPlaceholder {
		t.Fatal("the empty-system placeholder should default to on")
	}
	if cfg.Catalog != CatalogHybrid {
		t.Fatalf("catalog = %q", cfg.Catalog)
	}

	// A provider base URL wins over the adapter's apiBase.
	cfg, _ = parseConfig(gateway.ProviderConfig{
		BaseURL:       "https://override.example.com/",
		AdapterConfig: map[string]any{"apiBase": "https://ignored.example.com"},
	})
	if cfg.APIBase != "https://override.example.com/" {
		t.Fatalf("base URL override = %q", cfg.APIBase)
	}
}

func TestSchemaDescribesEveryField(t *testing.T) {
	a := testAdapter(t)
	schema := a.Schema()
	if len(schema) == 0 {
		t.Fatal("schema is empty")
	}
	for _, f := range schema {
		if f.Key == "" || f.Label == "" || f.Type == "" {
			t.Fatalf("incomplete field: %+v", f)
		}
		switch f.Type {
		case "string", "int", "bool", "select", "textarea":
		default:
			t.Fatalf("field %q has unsupported type %q", f.Key, f.Type)
		}
		if f.Type == "select" && len(f.Options) == 0 {
			t.Fatalf("select field %q has no options", f.Key)
		}
	}
}

func TestDoSendsEnvelopeAndHeaders(t *testing.T) {
	var gotPath, gotAuth, gotVersion, gotSession, gotSlug, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("x-command-code-version")
		gotSession = r.Header.Get("x-session-id")
		gotSlug = r.Header.Get("x-project-slug")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"type":"text-delta","text":"ok"}`+"\n")
		_, _ = io.WriteString(w, `{"type":"finish","finishReason":"stop"}`+"\n")
	}))
	defer srv.Close()

	a := testAdapter(t)
	in := sampleInvoke()
	in.Provider.BaseURL = srv.URL

	resp, err := a.Do(context.Background(), in)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if gotPath != "/alpha/generate" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer "+fixedSecret {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if gotVersion != ProtocolVersion {
		t.Fatalf("version header = %q", gotVersion)
	}
	if gotSession == "" {
		t.Fatal("session header is missing")
	}
	if gotSlug != SlugifyProjectPath(DefaultProjectDir) {
		t.Fatalf("project slug = %q", gotSlug)
	}

	var env Request
	if err := json.Unmarshal([]byte(gotBody), &env); err != nil {
		t.Fatalf("decode envelope: %v (%s)", err, gotBody)
	}
	if env.Params.Model != in.Model.UpstreamName {
		t.Fatalf("model = %q", env.Params.Model)
	}
	if env.Mode == "" || env.PermissionMode == "" {
		t.Fatalf("envelope metadata missing: %+v", env)
	}

	// The stream decodes end to end.
	dec := a.DecodeStream(resp.Body, in)
	var text string
	for {
		chunk, err := dec.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode stream: %v", err)
		}
		if chunk.Kind == ir.ChunkText {
			text += chunk.Text
		}
	}
	if text != "ok" {
		t.Fatalf("text = %q", text)
	}
}

func TestDoReportsQuotaStatusWithoutError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"error":{"message":"no credit"}}`)
	}))
	defer srv.Close()

	a := testAdapter(t)
	in := sampleInvoke()
	in.Provider.BaseURL = srv.URL

	resp, err := a.Do(context.Background(), in)
	if err != nil {
		t.Fatalf("Do returned an error for a 402; the caller must classify it: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if verdict := a.Classify(resp.StatusCode, body); verdict.Kind != gateway.QuotaCredit {
		t.Fatalf("verdict = %+v, want credit", verdict)
	}
}

func TestDoRequiresSecret(t *testing.T) {
	a := testAdapter(t)
	in := sampleInvoke()
	in.Credential.Secret = ""
	if _, err := a.Do(context.Background(), in); err == nil {
		t.Fatal("Do accepted an empty credential")
	}
}

func TestDecodeResponseAggregates(t *testing.T) {
	a := testAdapter(t)
	body := []byte(strings.Join([]string{
		`{"type":"text-delta","text":"hey"}`,
		`{"type":"finish","finishReason":"stop","totalUsage":{"inputTokens":3,"outputTokens":1}}`,
	}, "\n"))

	resp, err := a.DecodeResponse(body, sampleInvoke())
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if resp.Text() != "hey" {
		t.Fatalf("text = %q", resp.Text())
	}
	if resp.Usage.PromptTokens != 3 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestProbeUsesModelList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/provider/v1/models" {
			t.Errorf("probe path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"deepseek/deepseek-v4-flash"}]}`)
	}))
	defer srv.Close()

	a := testAdapter(t)
	in := sampleInvoke()
	in.Provider.BaseURL = srv.URL

	result, err := a.Probe(context.Background(), gateway.ProbeRequest{
		Provider:   in.Provider,
		Credential: in.Credential,
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !result.OK || result.Models != 1 {
		t.Fatalf("probe = %+v", result)
	}
}

func TestDiscoverModelsFallsBackToBuiltin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	a := testAdapter(t)
	in := sampleInvoke()
	in.Provider.BaseURL = srv.URL
	// Hybrid mode: a failed upstream call falls back to the embedded list.
	in.Provider.AdapterConfig = map[string]any{"modelCatalog": CatalogHybrid}

	models, err := a.DiscoverModels(context.Background(), gateway.ProbeRequest{
		Provider:   in.Provider,
		Credential: in.Credential,
	})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != len(BuiltinModels) {
		t.Fatalf("models = %d, want the embedded %d", len(models), len(BuiltinModels))
	}

	// Dynamic mode surfaces the failure instead of hiding it.
	in.Provider.AdapterConfig = map[string]any{"modelCatalog": CatalogDynamic}
	if _, err := a.DiscoverModels(context.Background(), gateway.ProbeRequest{
		Provider:   in.Provider,
		Credential: in.Credential,
	}); err == nil {
		t.Fatal("dynamic mode hid an upstream failure")
	}
}

func TestStatusReportsCredentials(t *testing.T) {
	a := testAdapter(t)
	in := sampleInvoke()

	// Touch the key book through Derive so the status has something to report.
	identity := a.Derive(in.Provider, in.Credential)
	if identity.Thumbmark == "" {
		t.Fatal("derived identity has no thumbmark")
	}
	a.entry(in.Provider.ID, in.Credential, Config{ProjectDir: DefaultProjectDir, FingerprintSalt: ""})

	status := a.Status(context.Background(), in.Provider)
	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	if !strings.Contains(string(payload), "protocolVersion") {
		t.Fatalf("status = %s", payload)
	}
}
