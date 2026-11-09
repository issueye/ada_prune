package provider

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// proxyClients caches one client per proxy address, so repeated requests share a
// connection pool instead of dialing a fresh transport for every call.
var proxyClients sync.Map // map[string]*http.Client

// HTTPClientFor returns the client an adapter should use for a provider: the
// base client when the provider sets no proxy, otherwise a client that routes
// through that proxy. The cache is keyed by the proxy address, so an edited
// proxy URL takes effect on the next request without a restart.
func HTTPClientFor(base *http.Client, proxyURL string) *http.Client {
	proxy := strings.TrimSpace(proxyURL)
	if proxy == "" {
		if base != nil {
			return base
		}
		return http.DefaultClient
	}
	timeout := time.Duration(0)
	if base != nil {
		timeout = base.Timeout
	}
	// The timeout is part of the key: two providers may share one proxy address
	// but expect different deadlines.
	key := proxy + "\x00" + timeout.String()
	if cached, ok := proxyClients.Load(key); ok {
		return cached.(*http.Client)
	}

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if parsed, err := url.Parse(proxy); err == nil {
		transport.Proxy = http.ProxyURL(parsed)
	}

	client := &http.Client{Transport: transport, Timeout: timeout}
	actual, _ := proxyClients.LoadOrStore(key, client)
	return actual.(*http.Client)
}
