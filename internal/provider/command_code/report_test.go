package command_code

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/learningdog1/ada_prune/internal/gateway"
	"github.com/learningdog1/ada_prune/internal/logging"
	"github.com/learningdog1/ada_prune/internal/provider"
)

// TestReportUsesFullProviderConfig pins the fix for a stripped ProviderConfig:
// the announcement must go to the provider's own base URL, carry its version,
// platform profile and ZDR flag, and report the fingerprint derived from its
// salt rather than an adapter default.
func TestReportUsesFullProviderConfig(t *testing.T) {
	const (
		salt       = "test-salt"
		projectDir = `C:\work\demo`
		version    = "9.9.9"
	)

	var (
		mu       sync.Mutex
		received = map[string]reportCapture{}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		received[r.URL.Path] = reportCapture{headers: r.Header.Clone(), body: raw}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := testAdapter(t)
	p := gateway.ProviderConfig{
		ID:      1,
		Kind:    provider.KindCommandCode,
		BaseURL: srv.URL,
		AdapterConfig: map[string]any{
			"fingerprintSalt":    salt,
			"projectDir":         projectDir,
			"protocolVersion":    version,
			"cliSessionMode":     "batch",
			"zdr":                true,
			"reportRefreshHours": 1,
		},
	}
	cred := gateway.Credential{ID: 7, Secret: fixedSecret}

	result, err := a.Report(context.Background(), gateway.FingerprintRequest{
		Provider:   p,
		Credential: cred,
	})
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if !result.OK {
		t.Fatalf("report result = %+v", result)
	}

	mu.Lock()
	fp, ok := received["/alpha/fingerprint/record"]
	lc, okLC := received["/alpha/lifecycle-events"]
	mu.Unlock()
	if !ok || !okLC {
		t.Fatalf("requests received = %v, want fingerprint and lifecycle", keysOf(received))
	}

	if got, want := fp.headers.Get("Authorization"), "Bearer "+fixedSecret; got != want {
		t.Errorf("authorization = %q, want %q", got, want)
	}
	if got := fp.headers.Get("x-command-code-version"); got != version {
		t.Errorf("version header = %q, want %q", got, version)
	}
	if got := fp.headers.Get("x-project-slug"); got != SlugifyProjectPath(projectDir) {
		t.Errorf("project slug = %q, want %q", got, SlugifyProjectPath(projectDir))
	}
	if got := fp.headers.Get("x-cmd-zdr"); got != "1" {
		t.Errorf("zdr header = %q, want 1", got)
	}

	var fingerprint Fingerprint
	if err := json.Unmarshal(fp.body, &fingerprint); err != nil {
		t.Fatalf("decode fingerprint: %v", err)
	}
	want := GenerateFingerprint(fixedSecret, salt, projectDir)
	if fingerprint.Thumbmark != want.Thumbmark {
		t.Errorf("thumbmark = %s, want the configured salt's %s", fingerprint.Thumbmark, want.Thumbmark)
	}

	var lifecycle struct {
		Metadata struct {
			Mode       string `json:"mode"`
			CLIVersion string `json:"cliVersion"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(lc.body, &lifecycle); err != nil {
		t.Fatalf("decode lifecycle: %v", err)
	}
	if lifecycle.Metadata.Mode != "batch" {
		t.Errorf("lifecycle mode = %q, want batch", lifecycle.Metadata.Mode)
	}
	if lifecycle.Metadata.CLIVersion != version {
		t.Errorf("lifecycle cli version = %q, want %q", lifecycle.Metadata.CLIVersion, version)
	}
}

// TestSaveReportKeepsConcurrentEntries pins the read-modify-write lock: two
// credentials reporting at once must both survive in the persisted book.
func TestSaveReportKeepsConcurrentEntries(t *testing.T) {
	state := newSyncState()
	built, err := New(provider.Deps{Log: logging.Discard(), State: state})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := built.(*Adapter)

	const entries = 64
	var wg sync.WaitGroup
	for i := int64(1); i <= entries; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			a.saveReport(1, id, credentialReport{NextReportAt: id})
		}(i)
	}
	wg.Wait()

	var book reportBook
	found, err := state.Get(1, gateway.StateFingerprint, &book)
	if err != nil {
		t.Fatalf("read book: %v", err)
	}
	if !found {
		t.Fatal("report book was not written")
	}
	if len(book.Credentials) != entries {
		t.Fatalf("book holds %d credentials, want %d (lost updates)", len(book.Credentials), entries)
	}
	for i := int64(1); i <= entries; i++ {
		if got := book.Credentials[i].NextReportAt; got != i {
			t.Fatalf("credential %d NextReportAt = %d, want %d", i, got, i)
		}
	}
}

type reportCapture struct {
	headers http.Header
	body    []byte
}

func keysOf(m map[string]reportCapture) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// syncState is a race-safe gateway.StateStore for the concurrency test.
type syncState struct {
	mu sync.Mutex
	m  map[string]any
}

func newSyncState() *syncState { return &syncState{m: map[string]any{}} }

func (s *syncState) key(providerID int64, key string) string {
	return strings.Join([]string{string(rune(providerID)), key}, "|")
}

func (s *syncState) Get(providerID int64, key string, v any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, ok := s.m[s.key(providerID, key)]
	if !ok {
		return false, nil
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(buf, v)
}

func (s *syncState) Set(providerID int64, key string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[s.key(providerID, key)] = v
	return nil
}

func (s *syncState) Delete(providerID int64, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, s.key(providerID, key))
	return nil
}
