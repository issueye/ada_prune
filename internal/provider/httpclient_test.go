package provider

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPClientForEmptyProxyReturnsBase(t *testing.T) {
	base := &http.Client{Timeout: time.Second}
	if got := HTTPClientFor(base, "  "); got != base {
		t.Fatalf("HTTPClientFor(base, empty) = %p, want the base client %p", got, base)
	}
}

func TestHTTPClientForRoutesThroughProxy(t *testing.T) {
	var requestURI string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURI = r.RequestURI
		_, _ = io.WriteString(w, "via-proxy")
	}))
	defer proxy.Close()

	client := HTTPClientFor(&http.Client{Timeout: 5 * time.Second}, proxy.URL)
	req, err := http.NewRequest(http.MethodGet, "http://upstream.invalid/hello", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	if string(body) != "via-proxy" {
		t.Fatalf("body = %q, want the proxy's answer", body)
	}
	if !strings.HasPrefix(requestURI, "http://upstream.invalid/hello") {
		t.Fatalf("proxy saw %q, want the absolute target URI", requestURI)
	}
}

func TestHTTPClientForCachesPerProxyAddress(t *testing.T) {
	base := &http.Client{Timeout: time.Second}
	first := HTTPClientFor(base, "http://127.0.0.1:19001")
	again := HTTPClientFor(base, "http://127.0.0.1:19001")
	if first != again {
		t.Fatal("the same proxy address must reuse one client")
	}
	other := HTTPClientFor(base, "http://127.0.0.1:19002")
	if other == first {
		t.Fatal("a different proxy address must get its own client")
	}
}
