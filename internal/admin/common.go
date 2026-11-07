package admin

import (
	"net/url"
	"strings"

	"github.com/learningdog1/ada_prune/internal/gateway"
	"github.com/learningdog1/ada_prune/internal/gateway/ir"
	"github.com/learningdog1/ada_prune/internal/store"
)

// maxNameLen bounds human-facing names so a paste cannot bloat the table.
const maxNameLen = 128

// validateName checks the common "unique human label" field.
func validateName(field, name string, required bool) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		if required {
			return "", FieldError(400, field, "%s is required", field)
		}
		return "", nil
	}
	if len([]rune(trimmed)) > maxNameLen {
		return "", FieldError(400, field, "%s must be at most %d characters", field, maxNameLen)
	}
	return trimmed, nil
}

// validateBaseURL checks an optional http(s) upstream root.
func validateBaseURL(field, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", FieldError(400, field, "%s is not a valid URL", field)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", FieldError(400, field, "%s must use http or https", field)
	}
	if u.Host == "" {
		return "", FieldError(400, field, "%s has no host", field)
	}
	return trimmed, nil
}

// validateProxyURL checks an optional network proxy.
func validateProxyURL(field, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", FieldError(400, field, "%s is not a valid URL", field)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", FieldError(400, field, "%s must use http, https or socks5", field)
	}
	if u.Host == "" {
		return "", FieldError(400, field, "%s has no host", field)
	}
	return trimmed, nil
}

// parseProtocol validates an upstream protocol, allowing the vendor-native value
// that adapters use for their own wire format.
func parseProtocol(field, raw string) (ir.Protocol, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", FieldError(400, field, "%s is required", field)
	}
	if ir.Protocol(value) == ir.ProtoVendorNative {
		return ir.ProtoVendorNative, nil
	}
	protocol, err := ir.ParseProtocol(value)
	if err != nil {
		return "", FieldError(400, field,
			"%s must be openai_chat, openai_responses, anthropic or vendor_native", field)
	}
	return protocol, nil
}

// providerConfigOf projects a stored provider onto the adapter-facing config.
func providerConfigOf(p store.Provider) gateway.ProviderConfig {
	return gateway.ProviderConfig{
		ID:            p.ID,
		Name:          p.Name,
		Kind:          gateway.Kind(p.Kind),
		BaseURL:       p.BaseURL,
		TimeoutMs:     p.TimeoutMs,
		ProxyURL:      p.ProxyURL,
		ExtraHeaders:  p.ExtraHeaders,
		AdapterConfig: p.AdapterConfig,
	}
}

// credentialOf projects a stored credential onto the adapter-facing shape.
func credentialOf(c store.Credential) gateway.Credential {
	return gateway.Credential{
		ID:       c.ID,
		Name:     c.Name,
		Secret:   c.Secret,
		Priority: c.Priority,
	}
}

// modelRefOf projects a stored model onto the adapter-facing shape.
func modelRefOf(m store.Model) gateway.ModelRef {
	return gateway.ModelRef{
		ID:               m.ID,
		PublicName:       m.PublicName,
		DisplayName:      m.DisplayName,
		UpstreamName:     m.UpstreamName,
		UpstreamProtocol: ir.Protocol(m.UpstreamProtocol),
		MaxTokens:        m.MaxTokens,
		SupportsStream:   m.SupportsStream,
		SupportsTools:    m.SupportsTools,
		SupportsVision:   m.SupportsVision,
	}
}

// adapterFor returns the adapter implementing a provider's kind.
func (s *Service) adapterFor(kind string) (gateway.Adapter, error) {
	if s.d.Registry == nil {
		return nil, Errorf(500, "no adapters are registered")
	}
	adapter, ok := s.d.Registry.Get(gateway.Kind(kind))
	if !ok {
		return nil, FieldError(400, "kind", "unknown adapter kind %q", kind)
	}
	return adapter, nil
}

// routableProvider reports whether a provider can serve traffic: enabled, with at
// least one active credential.
func routableProvider(p store.Provider) (bool, string) {
	if !p.Enabled {
		return false, "provider is disabled"
	}
	if p.ActiveCredentialCount == 0 {
		return false, "provider has no active credential"
	}
	return true, ""
}

// routableModel extends provider routability with the model's own flag.
func routableModel(m store.Model, p store.Provider) (bool, string) {
	if !m.Enabled {
		return false, "model is disabled"
	}
	return routableProvider(p)
}

// isLoopbackIP reports whether an address is local, used by the allow-anonymous
// hint on the settings page.
func isLoopbackIP(host string) bool {
	switch host {
	case "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	}
	return strings.HasPrefix(host, "127.")
}

// kindLabel returns a human label for a kind, for error messages.
func (s *Service) kindLabel(kind string) string {
	if s.d.Registry != nil {
		if label := s.d.Registry.Label(gateway.Kind(kind)); label != "" {
			return label
		}
	}
	return kind
}
