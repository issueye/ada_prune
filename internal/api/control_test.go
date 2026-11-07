package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/learningdog1/ada_prune/internal/app"
)

// adminCall performs one control-plane request and decodes the JSON answer.
func adminCall(t *testing.T, srv *httptest.Server, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)

	var decoded map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("%s %s returned non-JSON: %s", method, path, raw)
		}
	}
	return resp.StatusCode, decoded
}

// upstreamStub answers the metadata calls the command_code adapter makes.
func upstreamStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/provider/v1/models"):
			_, _ = io.WriteString(w, `{"data":[{"id":"deepseek/deepseek-v4-flash"},{"id":"gpt-5.6-luna"}]}`)
		case strings.HasSuffix(r.URL.Path, "/alpha/whoami"):
			_, _ = io.WriteString(w, `{"userName":"dev","email":"dev@example.com","orgId":"org_1"}`)
		case strings.HasSuffix(r.URL.Path, "/alpha/billing/credits"):
			_, _ = io.WriteString(w, `{"monthly":3.5,"purchased":0,"free":1,"totalRemaining":4.5}`)
		case strings.HasSuffix(r.URL.Path, "/alpha/billing/subscriptions"):
			_, _ = io.WriteString(w, `{"subscriptions":[{"id":"individual-go","status":"active"}]}`)
		case strings.HasSuffix(r.URL.Path, "/alpha/usage/summary"):
			_, _ = io.WriteString(w, `{"totalCount":12,"totalCost":1.2}`)
		case strings.HasSuffix(r.URL.Path, "/alpha/fingerprint/record"),
			strings.HasSuffix(r.URL.Path, "/alpha/lifecycle-events"):
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// createProvider is the shared fixture.
func createProvider(t *testing.T, srv *httptest.Server, upstream string) int64 {
	t.Helper()
	status, body := adminCall(t, srv, http.MethodPost, "/admin/api/providers", map[string]any{
		"name":    "Command Code 主号",
		"kind":    "command_code",
		"baseUrl": upstream,
		"adapterConfig": map[string]any{
			"cliMode": "agent",
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("create provider: status = %d body = %v", status, body)
	}
	return int64(body["id"].(float64))
}

func TestProviderCRUD(t *testing.T) {
	upstream := upstreamStub(t)
	_, srv := newServer(t, app.Options{})

	id := createProvider(t, srv, upstream.URL)

	// The provider is not routable until it has a credential.
	status, body := adminCall(t, srv, http.MethodGet, "/admin/api/providers", nil)
	if status != http.StatusOK {
		t.Fatalf("list: status = %d", status)
	}
	rows := body["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	first := rows[0].(map[string]any)
	if first["routable"] != false {
		t.Fatalf("routable = %v, want false without credentials", first["routable"])
	}
	if first["routableReason"] == "" {
		t.Fatal("a non-routable provider must explain why")
	}

	// Add a credential; now it becomes routable.
	status, cred := adminCall(t, srv, http.MethodPost, "/admin/api/credentials", map[string]any{
		"providerId": id,
		"name":       "主号",
		"secret":     "user_0123456789abcdef0123456789abcdef",
	})
	if status != http.StatusCreated {
		t.Fatalf("create credential: status = %d body = %v", status, cred)
	}
	if cred["secret"] == "user_0123456789abcdef0123456789abcdef" {
		t.Fatal("the credential response leaked the plaintext")
	}
	if !strings.Contains(cred["secret"].(string), "…") {
		t.Fatalf("secret is not masked: %v", cred["secret"])
	}

	status, body = adminCall(t, srv, http.MethodGet, "/admin/api/providers/"+itoa(id), nil)
	if status != http.StatusOK {
		t.Fatalf("get: status = %d", status)
	}
	if body["routable"] != true {
		t.Fatalf("routable = %v, want true after adding a credential", body["routable"])
	}
	if body["adapterCapabilities"] == nil {
		t.Fatal("adapter capabilities are missing")
	}

	// Update.
	status, body = adminCall(t, srv, http.MethodPut, "/admin/api/providers/"+itoa(id), map[string]any{
		"name":   "renamed",
		"kind":   "command_code",
		"weight": 3,
		"remark": "updated",
	})
	if status != http.StatusOK {
		t.Fatalf("update: status = %d body = %v", status, body)
	}
	if body["name"] != "renamed" {
		t.Fatalf("name = %v", body["name"])
	}

	// Kind is immutable.
	status, body = adminCall(t, srv, http.MethodPut, "/admin/api/providers/"+itoa(id), map[string]any{
		"name": "renamed",
		"kind": "openai_compatible",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("kind change: status = %d body = %v", status, body)
	}
}

func TestProviderValidation(t *testing.T) {
	_, srv := newServer(t, app.Options{})

	status, body := adminCall(t, srv, http.MethodPost, "/admin/api/providers", map[string]any{
		"name": "",
		"kind": "command_code",
	})
	if status != http.StatusBadRequest || body["field"] != "name" {
		t.Fatalf("missing name: status = %d body = %v", status, body)
	}

	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/providers", map[string]any{
		"name": "x",
		"kind": "nope",
	})
	if status != http.StatusBadRequest || body["field"] != "kind" {
		t.Fatalf("unknown kind: status = %d body = %v", status, body)
	}

	// The adapter validates its own config.
	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/providers", map[string]any{
		"name":          "x",
		"kind":          "command_code",
		"adapterConfig": map[string]any{"cliMode": "turbo"},
	})
	if status != http.StatusBadRequest || body["field"] != "adapterConfig" {
		t.Fatalf("bad adapter config: status = %d body = %v", status, body)
	}
}

func TestProviderProbeAndDiscovery(t *testing.T) {
	upstream := upstreamStub(t)
	_, srv := newServer(t, app.Options{})
	id := createProvider(t, srv, upstream.URL)

	// A probe without a credential must say so rather than hitting the network.
	status, body := adminCall(t, srv, http.MethodPost, "/admin/api/providers/"+itoa(id)+"/probe", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("probe without credential: status = %d body = %v", status, body)
	}

	if status, cred := adminCall(t, srv, http.MethodPost, "/admin/api/credentials", map[string]any{
		"providerId": id,
		"name":       "c1",
		"secret":     "user_0123456789abcdef0123456789abcdef",
	}); status != http.StatusCreated {
		t.Fatalf("create credential: %d %v", status, cred)
	}

	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/providers/"+itoa(id)+"/probe", nil)
	if status != http.StatusOK {
		t.Fatalf("probe: status = %d body = %v", status, body)
	}
	if body["ok"] != true {
		t.Fatalf("probe result = %v", body)
	}

	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/providers/"+itoa(id)+"/discover-models", nil)
	if status != http.StatusOK {
		t.Fatalf("discover: status = %d body = %v", status, body)
	}
	rows := body["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("discovered = %d, want 2", len(rows))
	}

	status, body = adminCall(t, srv, http.MethodGet, "/admin/api/providers/"+itoa(id)+"/status", nil)
	if status != http.StatusOK {
		t.Fatalf("status: status = %d", status)
	}
	if body["protocolVersion"] == nil {
		t.Fatalf("status payload = %v", body)
	}
}

func TestCredentialEndpoints(t *testing.T) {
	upstream := upstreamStub(t)
	_, srv := newServer(t, app.Options{})
	id := createProvider(t, srv, upstream.URL)

	// Batch import: one named, one bare, one duplicate.
	status, body := adminCall(t, srv, http.MethodPost, "/admin/api/credentials/batch", map[string]any{
		"providerId": id,
		"text":       "acct-1,user_aaaa1111bbbb2222\nuser_cccc3333dddd4444\nacct-1,user_eeee5555ffff6666",
	})
	if status != http.StatusOK {
		t.Fatalf("batch: status = %d body = %v", status, body)
	}
	if created := len(body["created"].([]any)); created != 2 {
		t.Fatalf("created = %d, want 2", created)
	}
	if skipped := len(body["skipped"].([]any)); skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}

	// Pin one.
	creds := body["created"].([]any)
	firstID := int64(creds[0].(map[string]any)["id"].(float64))
	status, _ = adminCall(t, srv, http.MethodPost, "/admin/api/credentials/pin", map[string]any{"id": firstID})
	if status != http.StatusOK {
		t.Fatalf("pin: status = %d", status)
	}

	// The list reflects the pin.
	status, body = adminCall(t, srv, http.MethodGet, "/admin/api/credentials?providerId="+itoa(id), nil)
	if status != http.StatusOK {
		t.Fatalf("list: status = %d", status)
	}
	rows := body["rows"].([]any)
	var pinned int
	for _, raw := range rows {
		if raw.(map[string]any)["pinned"] == true {
			pinned++
		}
	}
	if pinned != 1 {
		t.Fatalf("pinned rows = %d, want 1", pinned)
	}

	// Status transitions.
	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/credentials/"+itoa(firstID)+"/status",
		map[string]any{"status": "exhausted"})
	if status != http.StatusOK {
		t.Fatalf("set status: status = %d body = %v", status, body)
	}
	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/credentials/"+itoa(firstID)+"/status",
		map[string]any{"status": "bogus"})
	if status != http.StatusInternalServerError {
		t.Fatalf("invalid status: status = %d body = %v", status, body)
	}

	// Usage comes from the adapter.
	status, body = adminCall(t, srv, http.MethodGet, "/admin/api/credentials/"+itoa(firstID)+"/usage", nil)
	if status != http.StatusOK {
		t.Fatalf("usage: status = %d body = %v", status, body)
	}
	if body["whoami"] == nil {
		t.Fatalf("usage payload = %v", body)
	}

	// Deleting the pinned credential clears the pin.
	status, _ = adminCall(t, srv, http.MethodDelete, "/admin/api/credentials/"+itoa(firstID), nil)
	if status != http.StatusOK {
		t.Fatalf("delete: status = %d", status)
	}
	status, body = adminCall(t, srv, http.MethodGet, "/admin/api/credentials?providerId="+itoa(id), nil)
	if status != http.StatusOK {
		t.Fatalf("list after delete: status = %d", status)
	}
	for _, raw := range body["rows"].([]any) {
		if raw.(map[string]any)["pinned"] == true {
			t.Fatal("pin survived the credential delete")
		}
	}
}

func TestModelCRUDAndRoutability(t *testing.T) {
	upstream := upstreamStub(t)
	_, srv := newServer(t, app.Options{})
	providerID := createProvider(t, srv, upstream.URL)

	// No credential yet: the model exists but is not routable.
	status, body := adminCall(t, srv, http.MethodPost, "/admin/api/models", map[string]any{
		"providerId":       providerID,
		"displayName":      "DeepSeek V4 Flash",
		"upstreamName":     "deepseek/deepseek-v4-flash",
		"upstreamProtocol": "vendor_native",
	})
	if status != http.StatusCreated {
		t.Fatalf("create model: status = %d body = %v", status, body)
	}
	modelID := int64(body["id"].(float64))
	if body["routable"] != false {
		t.Fatalf("routable = %v, want false", body["routable"])
	}
	if body["publicName"] != "Command Code 主号/deepseek/deepseek-v4-flash" {
		t.Fatalf("publicName = %v, want the provider-namespaced default", body["publicName"])
	}

	// Add a credential: now routable.
	if status, cred := adminCall(t, srv, http.MethodPost, "/admin/api/credentials", map[string]any{
		"providerId": providerID, "name": "c", "secret": "user_0123456789abcdef0123456789abcdef",
	}); status != http.StatusCreated {
		t.Fatalf("create credential: %d %v", status, cred)
	}
	_, body = adminCall(t, srv, http.MethodGet, "/admin/api/models/"+itoa(modelID), nil)
	if body["routable"] != true {
		t.Fatalf("routable = %v, want true", body["routable"])
	}

	// Duplicate upstream name under the same provider conflicts.
	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/models", map[string]any{
		"providerId":       providerID,
		"displayName":      "dup",
		"upstreamName":     "deepseek/deepseek-v4-flash",
		"upstreamProtocol": "vendor_native",
	})
	if status != http.StatusConflict {
		t.Fatalf("duplicate upstream: status = %d body = %v", status, body)
	}

	// Invalid protocol is rejected.
	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/models", map[string]any{
		"providerId":       providerID,
		"displayName":      "x",
		"upstreamName":     "other",
		"upstreamProtocol": "grpc",
	})
	if status != http.StatusBadRequest || body["field"] != "upstreamProtocol" {
		t.Fatalf("bad protocol: status = %d body = %v", status, body)
	}

	// Disabling makes it non-routable again.
	status, _ = adminCall(t, srv, http.MethodPut, "/admin/api/models/"+itoa(modelID)+"/enabled", map[string]any{"enabled": false})
	if status != http.StatusOK {
		t.Fatalf("disable: status = %d", status)
	}
	_, body = adminCall(t, srv, http.MethodGet, "/admin/api/models/"+itoa(modelID), nil)
	if body["routable"] != false {
		t.Fatalf("routable = %v after disabling", body["routable"])
	}

	// Deleting a model that a route model references is blocked.
	status, _ = adminCall(t, srv, http.MethodPut, "/admin/api/models/"+itoa(modelID)+"/enabled", map[string]any{"enabled": true})
	if status != http.StatusOK {
		t.Fatalf("enable: status = %d", status)
	}
	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/route-models", map[string]any{
		"name":     "fast",
		"strategy": "priority",
		"members":  []map[string]any{{"modelId": modelID, "priority": 1}},
	})
	if status != http.StatusCreated {
		t.Fatalf("create route model: status = %d body = %v", status, body)
	}

	status, body = adminCall(t, srv, http.MethodDelete, "/admin/api/models/"+itoa(modelID), nil)
	if status != http.StatusConflict {
		t.Fatalf("delete referenced model: status = %d body = %v", status, body)
	}
}

func TestModelImport(t *testing.T) {
	upstream := upstreamStub(t)
	_, srv := newServer(t, app.Options{})
	providerID := createProvider(t, srv, upstream.URL)

	status, body := adminCall(t, srv, http.MethodPost, "/admin/api/models/import", map[string]any{
		"providerId": providerID,
		"rows": []map[string]any{
			{"upstreamName": "deepseek/deepseek-v4-flash", "upstreamProtocol": "vendor_native"},
			{"upstreamName": "gpt-5.6-luna", "upstreamProtocol": "vendor_native"},
			{"upstreamName": ""},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("import: status = %d body = %v", status, body)
	}
	if created := len(body["created"].([]any)); created != 2 {
		t.Fatalf("created = %d, want 2", created)
	}
	if failed := len(body["failed"].([]any)); failed != 1 {
		t.Fatalf("failed = %d, want 1", failed)
	}

	// A second import skips what exists.
	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/models/import", map[string]any{
		"providerId": providerID,
		"rows":       []map[string]any{{"upstreamName": "gpt-5.6-luna"}},
	})
	if status != http.StatusOK {
		t.Fatalf("second import: status = %d", status)
	}
	if skipped := len(body["skipped"].([]any)); skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
}

func TestClientKeyLifecycleOverHTTP(t *testing.T) {
	_, srv := newServer(t, app.Options{})

	status, body := adminCall(t, srv, http.MethodPost, "/admin/api/client-keys", map[string]any{
		"name":        "codex-local",
		"allowedIps":  "127.0.0.1/32",
		"quotaTokens": 1000,
	})
	if status != http.StatusCreated {
		t.Fatalf("create: status = %d body = %v", status, body)
	}
	token, _ := body["token"].(string)
	if !strings.HasPrefix(token, "sk-prune-") {
		t.Fatalf("token = %q", token)
	}
	keyID := int64(body["id"].(float64))

	// The list never carries the plaintext.
	status, body = adminCall(t, srv, http.MethodGet, "/admin/api/client-keys", nil)
	if status != http.StatusOK {
		t.Fatalf("list: status = %d", status)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), token) {
		t.Fatal("the client key list leaked a plaintext token")
	}

	// Rotation returns a new token and invalidates the old one.
	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/client-keys/"+itoa(keyID)+"/rotate", nil)
	if status != http.StatusOK {
		t.Fatalf("rotate: status = %d body = %v", status, body)
	}
	rotated, _ := body["token"].(string)
	if rotated == token || rotated == "" {
		t.Fatalf("rotated token = %q", rotated)
	}

	// Disable, reset usage, delete.
	status, _ = adminCall(t, srv, http.MethodPut, "/admin/api/client-keys/"+itoa(keyID)+"/enabled", map[string]any{"enabled": false})
	if status != http.StatusOK {
		t.Fatalf("disable: status = %d", status)
	}
	status, _ = adminCall(t, srv, http.MethodPost, "/admin/api/client-keys/"+itoa(keyID)+"/reset-usage", nil)
	if status != http.StatusOK {
		t.Fatalf("reset usage: status = %d", status)
	}
	status, _ = adminCall(t, srv, http.MethodDelete, "/admin/api/client-keys/"+itoa(keyID), nil)
	if status != http.StatusOK {
		t.Fatalf("delete: status = %d", status)
	}

	// Invalid input is rejected with the field name.
	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/client-keys", map[string]any{
		"name":       "bad",
		"allowedIps": "999.1.1.1",
	})
	if status != http.StatusBadRequest || body["field"] != "allowedIps" {
		t.Fatalf("bad IP: status = %d body = %v", status, body)
	}
}

func TestEndpointCRUD(t *testing.T) {
	upstream := upstreamStub(t)
	_, srv := newServer(t, app.Options{})
	providerID := createProvider(t, srv, upstream.URL)

	status, body := adminCall(t, srv, http.MethodPost, "/admin/api/models", map[string]any{
		"providerId":       providerID,
		"displayName":      "m",
		"upstreamName":     "m",
		"upstreamProtocol": "vendor_native",
	})
	if status != http.StatusCreated {
		t.Fatalf("create model: %d %v", status, body)
	}
	modelID := int64(body["id"].(float64))

	// The list starts with the three builtin endpoints.
	status, body = adminCall(t, srv, http.MethodGet, "/admin/api/endpoints", nil)
	if status != http.StatusOK {
		t.Fatalf("list: status = %d", status)
	}
	rows := body["rows"].([]any)
	if len(rows) != 3 {
		t.Fatalf("builtin endpoints = %d, want 3", len(rows))
	}
	for _, raw := range rows {
		if raw.(map[string]any)["builtin"] != true {
			t.Fatalf("row is not marked builtin: %v", raw)
		}
	}

	// Redefining a builtin path is refused.
	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/endpoints", map[string]any{
		"name":            "dup",
		"path":            "/v1/messages",
		"inboundProtocol": "anthropic",
		"targetType":      "model",
		"targetId":        modelID,
	})
	if status != http.StatusBadRequest || body["field"] != "path" {
		t.Fatalf("builtin path: status = %d body = %v", status, body)
	}

	// A path without a leading slash is refused.
	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/endpoints", map[string]any{
		"name":            "bad",
		"path":            "cc/messages",
		"inboundProtocol": "anthropic",
		"targetType":      "model",
		"targetId":        modelID,
	})
	if status != http.StatusBadRequest || body["field"] != "path" {
		t.Fatalf("bad path: status = %d body = %v", status, body)
	}

	// A real endpoint is created and can be deleted.
	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/endpoints", map[string]any{
		"name":            "Claude 入口",
		"path":            "/cc/v1/messages",
		"inboundProtocol": "anthropic",
		"targetType":      "model",
		"targetId":        modelID,
		"authRequired":    true,
	})
	if status != http.StatusCreated {
		t.Fatalf("create endpoint: status = %d body = %v", status, body)
	}
	endpointID := int64(body["id"].(float64))

	status, _ = adminCall(t, srv, http.MethodDelete, "/admin/api/endpoints/"+itoa(endpointID), nil)
	if status != http.StatusOK {
		t.Fatalf("delete: status = %d", status)
	}
}

func TestRouteModelValidation(t *testing.T) {
	upstream := upstreamStub(t)
	_, srv := newServer(t, app.Options{})
	providerID := createProvider(t, srv, upstream.URL)

	status, body := adminCall(t, srv, http.MethodPost, "/admin/api/models", map[string]any{
		"providerId":       providerID,
		"displayName":      "m",
		"upstreamName":     "m",
		"upstreamProtocol": "vendor_native",
	})
	if status != http.StatusCreated {
		t.Fatalf("create model: %d %v", status, body)
	}
	modelID := int64(body["id"].(float64))
	publicName := body["publicName"].(string)

	// A route model may not shadow an existing model id: the data plane resolves
	// the model first, so such a route would be unreachable.
	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/route-models", map[string]any{
		"name":    publicName,
		"members": []map[string]any{{"modelId": modelID}},
	})
	if status != http.StatusConflict || body["field"] != "name" {
		t.Fatalf("name collision: status = %d body = %v", status, body)
	}

	// No members is invalid.
	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/route-models", map[string]any{
		"name": "empty",
	})
	if status != http.StatusBadRequest || body["field"] != "members" {
		t.Fatalf("empty members: status = %d body = %v", status, body)
	}

	// Duplicate members are invalid.
	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/route-models", map[string]any{
		"name":    "dupes",
		"members": []map[string]any{{"modelId": modelID}, {"modelId": modelID}},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("duplicate members: status = %d body = %v", status, body)
	}

	// Unknown strategy is invalid.
	status, body = adminCall(t, srv, http.MethodPost, "/admin/api/route-models", map[string]any{
		"name":     "bad",
		"strategy": "random",
		"members":  []map[string]any{{"modelId": modelID}},
	})
	if status != http.StatusBadRequest || body["field"] != "strategy" {
		t.Fatalf("bad strategy: status = %d body = %v", status, body)
	}
}

func TestExportNeverContainsSecrets(t *testing.T) {
	upstream := upstreamStub(t)
	_, srv := newServer(t, app.Options{})
	providerID := createProvider(t, srv, upstream.URL)

	const secret = "user_0123456789abcdef0123456789abcdef"
	var createdToken string
	if status, body := adminCall(t, srv, http.MethodPost, "/admin/api/credentials", map[string]any{
		"providerId": providerID, "name": "c", "secret": secret,
	}); status != http.StatusCreated {
		t.Fatalf("create credential: %d %v", status, body)
	}
	if status, body := adminCall(t, srv, http.MethodPost, "/admin/api/client-keys", map[string]any{
		"name": "k",
	}); status != http.StatusCreated {
		t.Fatalf("create client key: %d %v", status, body)
	} else if body["token"] == nil {
		t.Fatal("client key creation response is missing its one-time token")
	} else {
		createdToken = body["token"].(string)
	}

	status, body := adminCall(t, srv, http.MethodGet, "/admin/api/export", nil)
	if status != http.StatusOK {
		t.Fatalf("export: status = %d", status)
	}
	raw := mustJSON(t, body)
	if strings.Contains(raw, secret) {
		t.Fatal("the export contains a plaintext credential secret")
	}
	if createdToken != "" && strings.Contains(raw, createdToken) {
		t.Fatal("the export contains a plaintext client key")
	}
	if !strings.Contains(raw, "…") {
		t.Fatal("the export should carry masked values")
	}
}

func TestAdaptersEndpoint(t *testing.T) {
	_, srv := newServer(t, app.Options{})
	status, body := adminCall(t, srv, http.MethodGet, "/admin/api/adapters", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	rows := body["rows"].([]any)
	if len(rows) < 3 {
		t.Fatalf("adapters = %d, want the three built-in kinds", len(rows))
	}

	byKind := map[string]map[string]any{}
	for _, raw := range rows {
		row := raw.(map[string]any)
		byKind[row["kind"].(string)] = row
	}
	for _, kind := range []string{"anthropic", "command_code", "openai_compatible"} {
		row, ok := byKind[kind]
		if !ok {
			t.Fatalf("adapter %q is not registered", kind)
		}
		if len(row["configSchema"].([]any)) == 0 {
			t.Errorf("adapter %q has no config schema", kind)
		}
		if row["capabilities"].(map[string]any)["stream"] != true {
			t.Errorf("adapter %q does not claim streaming", kind)
		}
	}
}

// itoa converts an id for URL building.
func itoa(id int64) string {
	return jsonNumber(id)
}

func jsonNumber(id int64) string {
	raw, _ := json.Marshal(id)
	return string(raw)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}
