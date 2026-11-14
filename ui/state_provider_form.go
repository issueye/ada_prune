package ui

import (
	"context"
	"fmt"
	"github.com/learningdog1/ada_prune/internal/admin"
	"github.com/learningdog1/ada_prune/internal/gateway"
	"github.com/learningdog1/ada_prune/internal/provider"
	"github.com/learningdog1/ada_prune/internal/store"
	"strings"
)

// protocolForLabel reverses protocolLabel, for the select's display values.
func protocolForLabel(label string) string {
	for _, p := range providerProtocols {
		if protocolLabel(p) == label {
			return p
		}
	}
	return label
}

// protocolLabels returns the display names for the form's select.
func protocolLabels(protocols []string) []string {
	out := make([]string, 0, len(protocols))
	for _, p := range protocols {
		out = append(out, protocolLabel(p))
	}
	return out
}

// kindForProtocol maps a protocol onto the adapter that speaks it.
func kindForProtocol(protocol string) string {
	switch protocol {
	case protocolAnthropic:
		return "anthropic"
	case protocolOpenAIChat, protocolOpenAIResponses:
		return "openai_compatible"
	default:
		return ""
	}
}

// protocolOf reads the protocol a provider speaks.
//
// Anthropic has its own adapter; the OpenAI family shares one adapter and records
// the configured protocol in adapter_config.
func protocolOf(p admin.ProviderView) string {
	switch p.Kind {
	case "anthropic":
		return protocolAnthropic
	case "openai_compatible":
		if v, ok := p.AdapterConfig["protocol"].(string); ok {
			switch v {
			case protocolOpenAIChat, protocolOpenAIResponses:
				return v
			}
		}
		return protocolOpenAIChat
	default:
		// Other adapter kinds (a vendor-specific one, say) have no protocol the
		// form can express; show the kind itself.
		return p.Kind
	}
}

// providerProtocolLabel is the protocol column's text: the protocol's display
// name for the three form protocols, the registry label for vendor adapters.
func (s *uiState) providerProtocolLabel(p admin.ProviderView) string {
	proto := protocolOf(p)
	switch proto {
	case protocolAnthropic, protocolOpenAIChat, protocolOpenAIResponses:
		return protocolLabel(proto)
	}
	for _, d := range s.adapters {
		if string(d.Kind) == p.Kind {
			return d.Label
		}
	}
	return proto
}

// editableProtocol reports whether the form may change this provider's protocol.
func editableProtocol(p admin.ProviderView) bool {
	// Staying inside the OpenAI adapter keeps stored models valid; crossing to
	// another adapter would not.
	return p.Kind == "openai_compatible"
}

// reverseProxyAdapters lists the registered adapters classified as reverse
// proxies, for the form's type-grouped choice.
func (s *uiState) reverseProxyAdapters() []provider.Descriptor {
	var out []provider.Descriptor
	for _, d := range s.adapters {
		if d.AccessMode == gateway.AccessReverseProxy {
			out = append(out, d)
		}
	}
	return out
}

// setFormAccessMode switches the form's type group. Reverse proxies pick their
// adapter directly (the first registered one by default); API providers derive
// the kind from the protocol at save time.
func (s *uiState) setFormAccessMode(mode gateway.AccessMode) {
	s.formAccessMode = mode
	if mode == gateway.AccessReverseProxy {
		s.formKind = ""
		if adapters := s.reverseProxyAdapters(); len(adapters) > 0 {
			s.formKind = string(adapters[0].Kind)
		}
	}
}

// openNewProvider resets the form for a new provider.
func (s *uiState) openNewProvider() {
	s.formOpen = true
	s.formIsNew = true
	s.formProviderID = 0
	s.formError = ""
	s.formName = ""
	s.formBaseURL = ""
	s.formProxy = ""
	s.formSecret = ""
	s.formHeaders = ""
	s.formEnabled = true
	s.formCredentialID = 0
	s.formCredentialMask = ""
	s.formAccessMode = gateway.AccessAPI
	s.formProtocol = protocolAnthropic
	s.formKind = kindForProtocol(s.formProtocol)
}

// openEditProvider fills the form from a provider and its primary credential.
// The form remembers the provider by id, and the table selection follows it:
// a row action can be used without clicking the row first, and the rest of the
// dialog must still describe the row whose menu was opened.
func (s *uiState) openEditProvider(index int) {
	if index < 0 || index >= len(s.providers) {
		return
	}
	p := s.providers[index]
	s.providerSel = index
	s.formOpen = true
	s.formIsNew = false
	s.formProviderID = p.ID
	s.formError = ""
	s.formKind = p.Kind
	s.formAccessMode = p.AccessMode
	s.formProtocol = protocolOf(p)
	s.formName = p.Name
	s.formBaseURL = p.BaseURL
	s.formProxy = p.ProxyURL
	s.formEnabled = p.Enabled
	s.formSecret = ""
	s.formCredentialID = 0
	s.formCredentialMask = ""
	s.formHeaders = renderHeaders(p.ExtraHeaders)

	// The form edits the credential that routing would use first.
	go func() {
		rows, err := s.app.Admin.ListCredentials(context.Background(), p.ID)
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			if err != nil {
				return
			}
			for _, row := range rows {
				if row.Status == store.CredentialActive && row.Enabled {
					s.formCredentialID = row.ID
					s.formCredentialMask = row.SecretMask
					return
				}
			}
			if len(rows) > 0 {
				s.formCredentialID = rows[0].ID
				s.formCredentialMask = rows[0].SecretMask
			}
		})
	}()
}

func (s *uiState) closeProviderForm() {
	s.formOpen = false
	s.formError = ""
}

// saveProviderForm validates the form and writes the provider and its key.
func (s *uiState) saveProviderForm() {
	name := strings.TrimSpace(s.formName)
	baseURL := strings.TrimSpace(s.formBaseURL)
	protocol := strings.TrimSpace(s.formProtocol)
	proxy := strings.TrimSpace(s.formProxy)

	if name == "" {
		s.formError = "名称不能为空"
		return
	}

	// The adapter is derived from the protocol. A provider created with another
	// adapter (a vendor-specific one) keeps its kind and skips the protocol
	// choice: its wire format is not one of the three.
	var (
		kind         string
		existing     *admin.ProviderView
		keepProtocol bool
	)
	if s.formIsNew {
		if s.formAccessMode == gateway.AccessReverseProxy {
			kind = s.formKind
			if kind == "" {
				s.formError = "请选择适配器"
				return
			}
		} else {
			kind = kindForProtocol(protocol)
			if kind == "" {
				s.formError = "请选择协议"
				return
			}
		}
	} else {
		existing = s.formProviderView()
		if existing == nil {
			s.formError = "未选中供应商"
			return
		}
		kind = existing.Kind
		if !editableProtocol(*existing) {
			// Nothing to choose: the protocol is whatever the adapter speaks.
			protocol = protocolOf(*existing)
			keepProtocol = true
		}
	}
	headers, headerErr := parseHeaders(s.formHeaders)
	if headerErr != nil {
		s.formError = headerErr.Error()
		return
	}
	if s.formIsNew && strings.TrimSpace(s.formSecret) == "" {
		s.formError = "新建供应商需要填写上游密钥"
		return
	}

	// Keep the adapter's other settings; only the protocol key is owned by the
	// form.
	adapterConfig := map[string]any{}
	if existing != nil {
		for k, v := range existing.AdapterConfig {
			adapterConfig[k] = v
		}
	}
	if kind == "openai_compatible" && !keepProtocol {
		adapterConfig["protocol"] = protocol
	}

	enabled := s.formEnabled
	input := admin.ProviderInput{
		Name:          name,
		Kind:          kind,
		BaseURL:       baseURL,
		Enabled:       &enabled,
		ProxyURL:      proxy,
		ExtraHeaders:  headers,
		AdapterConfig: adapterConfig,
	}

	secret := strings.TrimSpace(s.formSecret)
	credentialID := s.formCredentialID
	isNew := s.formIsNew
	providerID := int64(0)
	if !isNew {
		// The kind is immutable; the service rejects a change anyway.
		providerID = s.formProviderID
	}

	go func() {
		ctx := context.Background()
		var err error
		if isNew {
			created, createErr := s.app.Admin.CreateProvider(ctx, input)
			if createErr != nil {
				err = createErr
			} else {
				_, err = s.app.Admin.CreateCredential(ctx, admin.CredentialInput{
					ProviderID: created.ID,
					Name:       "默认",
					Secret:     secret,
				})
			}
		} else {
			if providerID <= 0 {
				err = fmt.Errorf("no provider selected")
			} else {
				_, err = s.app.Admin.UpdateProvider(ctx, providerID, input)
				if err == nil && secret != "" && !secretboxLooksMasked(secret) {
					if credentialID > 0 {
						_, err = s.app.Admin.UpdateCredential(ctx, credentialID, admin.CredentialInput{
							Name:   "默认",
							Secret: secret,
						})
					} else {
						_, err = s.app.Admin.CreateCredential(ctx, admin.CredentialInput{
							ProviderID: providerID,
							Name:       "默认",
							Secret:     secret,
						})
					}
				}
			}
		}

		if s.win == nil {
			return
		}
		s.win.Update(func() {
			if err != nil {
				s.formError = err.Error()
				return
			}
			s.formOpen = false
			s.formError = ""
			s.flash("供应商已保存", false)
			s.reload()
		})
	}()
}

// formProviderView returns the provider the open form edits, if any. The form
// holds an id rather than the table index, so a background refresh that
// reorders the list cannot make it edit the wrong row.
func (s *uiState) formProviderView() *admin.ProviderView {
	if s.formIsNew || s.formProviderID <= 0 {
		return nil
	}
	for i := range s.providers {
		if s.providers[i].ID == s.formProviderID {
			return &s.providers[i]
		}
	}
	return nil
}

// renderHeaders turns a header map into the form's "Key: Value" lines.
func renderHeaders(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sortStrings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(headers[k])
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// parseHeaders reads "Key: Value" lines into a map.
func parseHeaders(text string) (map[string]string, error) {
	out := map[string]string{}
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			return nil, fmt.Errorf("第 %d 行缺少冒号，应为 “Key: Value”", i+1)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("第 %d 行缺少 Header 名称", i+1)
		}
		out[key] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// secretboxLooksMasked reports whether a value is a mask rather than a new key.
func secretboxLooksMasked(v string) bool {
	return strings.Contains(v, "…")
}

// sortStrings sorts in place without pulling sort into the view code.
func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
