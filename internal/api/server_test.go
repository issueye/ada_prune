package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/learningdog1/ada_prune/internal/app"
)

// newServer boots a real app on a temp data directory and wraps its handler.
func newServer(t *testing.T, opts app.Options) (*app.App, *httptest.Server) {
	t.Helper()
	if opts.DataDir == "" {
		opts.DataDir = t.TempDir()
	}
	a, err := app.New(opts)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(a.Close)

	srv := httptest.NewServer(a.Server.Handler())
	t.Cleanup(srv.Close)
	return a, srv
}

func get(t *testing.T, srv *httptest.Server, path string, header map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, body
}

func TestHealth(t *testing.T) {
	_, srv := newServer(t, app.Options{})
	resp, body := get(t, srv, "/health", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != "OK" {
		t.Fatalf("body = %q", body)
	}
}

func TestRequestIDIsEchoedAndMinted(t *testing.T) {
	_, srv := newServer(t, app.Options{})

	resp, _ := get(t, srv, "/health", map[string]string{"X-Request-ID": "abc-123"})
	if got := resp.Header.Get("X-Request-ID"); got != "abc-123" {
		t.Fatalf("X-Request-ID = %q, want the caller's value", got)
	}

	resp, _ = get(t, srv, "/health", nil)
	if got := resp.Header.Get("X-Request-ID"); len(got) != 24 {
		t.Fatalf("minted X-Request-ID = %q, want 24 hex characters", got)
	}
}

func TestUnknownPathIs404(t *testing.T) {
	_, srv := newServer(t, app.Options{})
	resp, body := get(t, srv, "/nope", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "not_found") {
		t.Fatalf("body = %s", body)
	}
}

func TestModelsRequiresAKey(t *testing.T) {
	_, srv := newServer(t, app.Options{})
	for _, path := range []string{"/v1/models", "/models"} {
		resp, body := get(t, srv, path, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401 without a key", path, resp.StatusCode)
		}
		if !strings.Contains(string(body), "authentication_error") {
			t.Fatalf("%s body = %s", path, body)
		}
	}
}

func TestDataPlaneRejectsMissingKey(t *testing.T) {
	_, srv := newServer(t, app.Options{})
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		resp, err := srv.Client().Post(srv.URL+path, "application/json", strings.NewReader(`{"model":"m"}`))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", path, resp.StatusCode)
		}
		if !strings.Contains(string(body), "error") {
			t.Fatalf("%s body = %s", path, body)
		}
	}
}

func TestDataPlaneSetsCORS(t *testing.T) {
	_, srv := newServer(t, app.Options{})
	resp, _ := get(t, srv, "/v1/models", nil)
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("CORS origin = %q, want *", got)
	}
}

func TestAdminOverview(t *testing.T) {
	_, srv := newServer(t, app.Options{})
	resp, body := get(t, srv, "/admin/api/overview", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	var ov struct {
		Version  string `json:"version"`
		DataDir  string `json:"dataDir"`
		Degraded bool   `json:"degraded"`
		Port     int    `json:"port"`
	}
	if err := json.Unmarshal(body, &ov); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if ov.Version == "" {
		t.Fatal("version is empty")
	}
	if ov.DataDir == "" {
		t.Fatal("dataDir is empty")
	}
	if ov.Degraded {
		t.Fatal("a fresh temp directory should not be degraded")
	}
	if ov.Port != 3050 {
		t.Fatalf("port = %d, want the default 3050", ov.Port)
	}
}

func TestAdminRequiresTokenWhenConfigured(t *testing.T) {
	_, srv := newServer(t, app.Options{AdminToken: "s3cret"})

	resp, _ := get(t, srv, "/admin/api/overview", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", resp.StatusCode)
	}

	resp, _ = get(t, srv, "/admin/api/overview", map[string]string{"Authorization": "Bearer wrong"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", resp.StatusCode)
	}

	resp, _ = get(t, srv, "/admin/api/overview", map[string]string{"Authorization": "Bearer s3cret"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("right token: status = %d, want 200", resp.StatusCode)
	}

	// The query parameter form exists for browsers that cannot set headers.
	resp, _ = get(t, srv, "/admin/api/overview?token=s3cret", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("query token: status = %d, want 200", resp.StatusCode)
	}
}

func TestAdminSettingsRoundTrip(t *testing.T) {
	a, srv := newServer(t, app.Options{})

	req, err := http.NewRequest(http.MethodPut, srv.URL+"/admin/api/settings",
		strings.NewReader(`{"logLevel":"debug","maxInflight":4}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("PUT settings: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}

	_, body = get(t, srv, "/admin/api/settings", nil)
	var view struct {
		LogLevel    string `json:"logLevel"`
		MaxInflight int64  `json:"maxInflight"`
	}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.LogLevel != "debug" || view.MaxInflight != 4 {
		t.Fatalf("settings = %+v", view)
	}
	if got := a.Cfg.Get().MaxInflight; got != 4 {
		t.Fatalf("effective maxInflight = %d", got)
	}
}

func TestAdminRejectsCrossOriginWrite(t *testing.T) {
	_, srv := newServer(t, app.Options{})
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/admin/api/settings",
		strings.NewReader(`{"logLevel":"warn"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Origin", "http://evil.example")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestDataFilesAreCreated(t *testing.T) {
	dir := t.TempDir()
	newServer(t, app.Options{DataDir: dir})

	for _, name := range []string{"config.json", ".secret.key", "ada_prune.db"} {
		if _, err := os.Stat(dir + string(os.PathSeparator) + name); err != nil {
			t.Fatalf("%s was not created: %v", name, err)
		}
	}
}
