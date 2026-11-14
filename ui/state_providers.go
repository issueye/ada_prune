package ui

import (
	"context"
	"fmt"
	"github.com/learningdog1/ada_prune/internal/admin"
	"github.com/learningdog1/ada_prune/internal/gateway"
	"time"
)

func (s *uiState) toggleProvider(index int, enabled bool) {
	rows := s.providers
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	s.run("更新供应商", func() error {
		return s.app.Admin.SetProviderEnabled(context.Background(), id, enabled)
	})
}

func (s *uiState) probeProvider(index int) {
	rows := s.providers
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := s.app.Admin.ProbeProvider(ctx, id)
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			switch {
			case err != nil:
				s.flash("探活失败: "+err.Error(), true)
			case result.OK:
				s.flash(fmt.Sprintf("探活成功 (%dms)", result.DurationMs), false)
			default:
				s.flash("探活失败: "+result.Detail, true)
			}
		})
	}()
}

func (s *uiState) deleteProvider(index int) {
	rows := s.providers
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	name := rows[index].Name
	s.confirm("删除供应商 "+name+"？",
		"将一并删除它的凭据与模型。若仍有启用的模型，删除会被服务端拒绝。此操作不可撤销。",
		"删除", func() {
			s.run("删除供应商", func() error {
				return s.app.Admin.DeleteProvider(context.Background(), id)
			})
		})
}

// discoverProviderName names the dialog's selected provider, for the public-id
// preview of each catalogue row.
// discoverProviderName names the dialog's selected provider, for the public-id
// preview of each catalogue row.
func (s *uiState) discoverProviderName() string {
	for _, p := range s.providers {
		if p.ID == s.discoverProviderID {
			return p.Name
		}
	}
	return ""
}

// discoverableProviders lists providers whose adapter can enumerate models.
func (s *uiState) discoverableProviders() []admin.ProviderView {
	kinds := make(map[string]bool, len(s.adapters))
	for _, d := range s.adapters {
		if d.Capabilities.ModelDiscovery {
			kinds[string(d.Kind)] = true
		}
	}
	var out []admin.ProviderView
	for _, p := range s.providers {
		if kinds[p.Kind] {
			out = append(out, p)
		}
	}
	return out
}

// openDiscover resets the dialog and picks the first provider that can list
// models.
// openDiscover resets the dialog and picks the first provider that can list
// models.
func (s *uiState) openDiscover() {
	s.discoverOpen = true
	s.discoverLoading = false
	s.discoverImporting = false
	s.discoverError = ""
	s.discoverSummary = ""
	s.discoverRows = nil
	s.discoverChecked = map[string]bool{}
	if rows := s.discoverableProviders(); len(rows) > 0 {
		found := false
		for _, p := range rows {
			if p.ID == s.discoverProviderID {
				found = true
				break
			}
		}
		if !found {
			s.discoverProviderID = rows[0].ID
		}
	}
}

// fetchDiscovered asks the chosen provider's adapter for its upstream catalogue.
func (s *uiState) fetchDiscovered() {
	id := s.discoverProviderID
	if id == 0 || s.discoverLoading {
		return
	}
	s.discoverLoading = true
	s.discoverError = ""
	s.discoverSummary = ""
	s.discoverRows = nil
	s.discoverChecked = map[string]bool{}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
		defer cancel()
		result, err := s.app.Admin.DiscoverModels(ctx, id)
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			s.discoverLoading = false
			if err != nil {
				s.discoverError = err.Error()
				return
			}
			s.discoverRows = result.Rows
		})
	}()
}

// importProtocol is the upstream protocol imported models inherit: the
// provider's configured one, or empty so the service falls back to the
// adapter's native protocol.
// importProtocol is the upstream protocol imported models inherit: the
// provider's configured one, or empty so the service falls back to the
// adapter's native protocol.
func (s *uiState) importProtocol() string {
	for _, p := range s.providers {
		if p.ID != s.discoverProviderID {
			continue
		}
		switch proto := protocolOf(p); proto {
		case protocolAnthropic, protocolOpenAIChat, protocolOpenAIResponses:
			return proto
		}
		return ""
	}
	return ""
}

// importDiscovered creates the checked models under the chosen provider.
func (s *uiState) importDiscovered() {
	if s.discoverImporting {
		return
	}
	protocol := s.importProtocol()
	var rows []admin.ModelImportRow
	for _, r := range s.discoverRows {
		if r.Exists || !s.discoverChecked[r.UpstreamName] {
			continue
		}
		rows = append(rows, admin.ModelImportRow{
			UpstreamName:     r.UpstreamName,
			DisplayName:      r.DisplayName,
			UpstreamProtocol: protocol,
		})
	}
	if len(rows) == 0 {
		return
	}
	id := s.discoverProviderID
	s.discoverImporting = true
	s.discoverError = ""
	s.discoverSummary = ""
	go func() {
		result, err := s.app.Admin.ImportModels(context.Background(), id, rows)
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			s.discoverImporting = false
			if err != nil {
				s.discoverError = err.Error()
				return
			}
			s.discoverSummary = fmt.Sprintf("新建 %d 个 · 跳过 %d 个 · 失败 %d 个",
				len(result.Created), len(result.Skipped), len(result.Failed))
			created := make(map[string]bool, len(result.Created))
			for _, m := range result.Created {
				created[m.UpstreamName] = true
			}
			for i := range s.discoverRows {
				if created[s.discoverRows[i].UpstreamName] {
					s.discoverRows[i].Exists = true
					delete(s.discoverChecked, s.discoverRows[i].UpstreamName)
				}
			}
			s.reloadQuiet()
		})
	}()
}

// protocolForLabel reverses protocolLabel, for the select's display values.

// protocolLabels returns the display names for the form's select.

// kindForProtocol maps a protocol onto the adapter that speaks it.

// protocolOf reads the protocol a provider speaks.
//
// Anthropic has its own adapter; the OpenAI family shares one adapter and records
// the configured protocol in adapter_config.

// providerProtocolLabel is the protocol column's text: the protocol's display
// name for the three form protocols, the registry label for vendor adapters.

// editableProtocol reports whether the form may change this provider's protocol.

// accessModeLabel names an access mode for the operator.
func accessModeLabel(mode gateway.AccessMode) string {
	if mode == gateway.AccessReverseProxy {
		return "反代"
	}
	return "API"
}

// reverseProxyAdapters lists the registered adapters classified as reverse
// proxies, for the form's type-grouped choice.

// setFormAccessMode switches the form's type group. Reverse proxies pick their
// adapter directly (the first registered one by default); API providers derive
// the kind from the protocol at save time.

// openNewProvider resets the form for a new provider.

// openEditProvider fills the form from a provider and its primary credential.
// The form remembers the provider by id, and the table selection follows it:
// a row action can be used without clicking the row first, and the rest of the
// dialog must still describe the row whose menu was opened.

// saveProviderForm validates the form and writes the provider and its key.

// formProviderView returns the provider the open form edits, if any. The form
// holds an id rather than the table index, so a background refresh that
// reorders the list cannot make it edit the wrong row.

// renderHeaders turns a header map into the form's "Key: Value" lines.

// parseHeaders reads "Key: Value" lines into a map.

// secretboxLooksMasked reports whether a value is a mask rather than a new key.

// sortStrings sorts in place without pulling sort into the view code.
