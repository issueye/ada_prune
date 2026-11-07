package command_code

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/learningdog1/ada_proxy/internal/gateway"
)

// defaultConnectTimeout bounds dialling. Request lifetimes are long by design
// (a model may think for minutes), so the dial timeout is the only hard one.
const defaultConnectTimeout = 30 * time.Second

// generatePath is the generation endpoint.
const generatePath = "/alpha/generate"

// apiBaseOf returns the effective upstream root for a provider.
func apiBaseOf(cfg Config, p gateway.ProviderConfig) string {
	base := strings.TrimSpace(p.BaseURL)
	if base == "" {
		base = cfg.APIBase
	}
	if base == "" {
		base = DefaultAPIBase
	}
	return strings.TrimRight(base, "/")
}

// usageHeaders are the CLI identity headers used by calls that are not
// generations: billing, whoami, model lists.
func usageHeaders(secret, projectDir, protocolVersion string, extra map[string]string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+secret)
	h.Set("User-Agent", "cli")
	h.Set("x-cli-environment", "production")
	h.Set("x-command-code-version", protocolVersion)
	h.Set("x-project-slug", SlugifyProjectPath(projectDir))
	for k, v := range extra {
		h.Set(k, v)
	}
	return h
}

// generateHeaders builds the headers for one generation call.
func generateHeaders(in *gateway.InvokeRequest, cfg Config, sessionID string) http.Header {
	h := usageHeaders(in.Credential.Secret, cfg.ProjectDir, cfg.ProtocolVersion, in.Provider.ExtraHeaders)
	h.Set("Content-Type", "application/json")
	h.Set("x-taste-learning", "false")
	h.Set("x-session-id", sessionID)
	h.Set("traceparent", traceparent())
	if cfg.ZDR {
		h.Set("x-cmd-zdr", "1")
	}
	return h
}

// traceparent makes a W3C trace context so the request looks like the CLI's.
func traceparent() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00-" + strings.Repeat("0", 32) + "-" + strings.Repeat("0", 16) + "-01"
	}
	return "00-" + hex.EncodeToString(b[0:16]) + "-" + hex.EncodeToString(b[16:24]) + "-01"
}

// httpClient returns the client for a provider, honouring its proxy setting.
// Clients are cached per proxy address so repeated calls share a connection pool.
func (a *Adapter) httpClient(cfg Config, p gateway.ProviderConfig) *http.Client {
	proxy := strings.TrimSpace(p.ProxyURL)
	if proxy == "" {
		return a.http
	}

	a.clients.mu.Lock()
	defer a.clients.mu.Unlock()
	if client, ok := a.clients.byProxy[proxy]; ok {
		return client
	}
	client := a.newClient(proxy, p.TimeoutMs)
	a.clients.byProxy[proxy] = client
	return client
}

// newClient builds one HTTP client. The timeout is deliberately zero unless the
// provider sets one: the idle watchdog in the relay bounds a stuck stream, and a
// hard request timeout would cut legitimate long generations.
func (a *Adapter) newClient(proxy string, timeoutMs int) *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   defaultConnectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if proxy != "" {
		if parsed, err := url.Parse(proxy); err == nil {
			transport.Proxy = http.ProxyURL(parsed)
		}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   time.Duration(timeoutMs) * time.Millisecond,
	}
}

// closeIdleConnections releases pooled connections. The application calls it on
// shutdown.
func (a *Adapter) closeIdleConnections() {
	if a.http != nil {
		a.http.CloseIdleConnections()
	}
	a.clients.mu.Lock()
	defer a.clients.mu.Unlock()
	for _, client := range a.clients.byProxy {
		client.CloseIdleConnections()
	}
}

// readLimited reads at most n bytes, so classifying an error cannot pull an
// unbounded response into memory.
func readLimited(r io.Reader, n int64) string {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	var total int64
	for total < n {
		remaining := n - total
		if int64(len(tmp)) > remaining {
			tmp = tmp[:remaining]
		}
		read, err := r.Read(tmp)
		if read > 0 {
			buf = append(buf, tmp[:read]...)
			total += int64(read)
		}
		if err != nil {
			break
		}
	}
	return string(buf)
}
