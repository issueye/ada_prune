package ui

import (
	gui "github.com/egoist/mygo/ui"
	"github.com/learningdog1/ada_prune/internal/gateway"
	"strings"
)

// providerForm creates or edits a provider: name, kind, base URL, upstream key,
// network proxy and custom headers.
// providerDialog is the modal body for creating or editing a provider. The modal
// itself is opened by view, so the dialog works from any page.
func (s *uiState) providerDialog(c *gui.Context) {
	t := c.Theme()
	title := "编辑供应商"
	if s.formIsNew {
		title = "新建供应商"
	}

	gui.Column(c).Width(640).Gap(spM).Children(func() {
		gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
			gui.Text(c, title).FontSize(15).Bold()
			hintIcon(c, "表单", "先选类型再选协议或适配器；密钥加密存储，出参恒为掩码。")
		})

		if s.formError != "" {
			gui.Row(c).FillWidth().Padding(6, 10).Radius(8).
				Background(t.Danger.Alpha(0.1)).Children(func() {
				gui.Text(c, s.formError).FontSize(11).TextColor(t.Danger).SingleLine()
			})
		}

		// 名称与类型同行；其余字段各独占一排，长输入有完整的宽度。
		gui.Row(c).Gap(spL).AlignItems(gui.Start).Children(func() {
			gui.Column(c).Grow(1).Children(func() {
				fieldWith(c, "名称", "唯一，用于列表与模型默认对外名", func() {
					gui.TextInput(c, &s.formName).FillWidth().Placeholder("例如 Command Code 主号")
				})
			})
			if s.formIsNew {
				// Select 的最小宽度是 140（theme.Space(35)），列宽必须不小于它。
				gui.Column(c).Width(150).Children(func() {
					fieldWith(c, "类型", "接入方式", func() {
						label := accessModeLabel(s.formAccessMode)
						if gui.Select(c, &label, []string{"API", "反代"}).FillWidth().Changed() {
							mode := gateway.AccessAPI
							if label == accessModeLabel(gateway.AccessReverseProxy) {
								mode = gateway.AccessReverseProxy
							}
							s.setFormAccessMode(mode)
						}
					})
				})
			}
		})

		s.providerProtocolField(c)

		fieldWith(c, "BaseURL", "上游服务根地址", func() {
			gui.TextInput(c, &s.formBaseURL).FillWidth().Placeholder("https://api.commandcode.ai")
		})
		fieldWith(c, "网络代理", "http / https / socks5，留空直连", func() {
			gui.TextInput(c, &s.formProxy).FillWidth().Placeholder("http://127.0.0.1:7890")
		})

		hint := "加密存储、出参恒为掩码；留空表示不修改"
		placeholder := "user_..."
		if !s.formIsNew && s.formCredentialMask != "" {
			hint = "当前密钥 " + s.formCredentialMask + "；留空或用掩码表示不修改"
			placeholder = "粘贴新密钥以轮换"
		} else if s.formIsNew {
			hint = "新建供应商必须填写；加密存储、出参恒为掩码"
		}
		fieldWith(c, "上游密钥", hint, func() {
			gui.TextInput(c, &s.formSecret).FillWidth().Placeholder(placeholder)
		})

		fieldWith(c, "自定义 Header", "每行一个 “Key: Value”，随每次上游请求发送", func() {
			gui.TextArea(c, &s.formHeaders).FillWidth().Height(72).
				Placeholder("x-custom: 1\nx-org: my-team")
		})

		enabled := s.formEnabled
		gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
			gui.Toggle(c, &enabled, "启用该供应商")
			if enabled != s.formEnabled {
				s.formEnabled = enabled
			}
		})

		gui.Row(c).FillWidth().Gap(spS).Justify(gui.End).Children(func() {
			if gui.Button(c, "取消").Clicked() {
				s.closeProviderForm()
			}
			if gui.PrimaryButton(c, "保存").Clicked() {
				s.saveProviderForm()
			}
		})
	})
}

// providerProtocolField renders the protocol or adapter choice as its own
// full-width row: new reverse-proxy providers pick a registered adapter, new
// API providers pick a protocol, and editing is limited to what the adapter
// allows.
func (s *uiState) providerProtocolField(c *gui.Context) {
	if s.formIsNew {
		if s.formAccessMode == gateway.AccessReverseProxy {
			fieldWith(c, "适配器", "反代适配器，由注册表提供", func() {
				s.reverseProxySelect(c)
			})
			return
		}
		fieldWith(c, "协议", "决定适配器与上游请求格式", func() {
			label := protocolLabel(s.formProtocol)
			if gui.Select(c, &label, protocolLabels(providerProtocols)).FillWidth().Changed() {
				s.formProtocol = protocolForLabel(label)
			}
		})
		return
	}
	p := s.formProviderView()
	if p == nil {
		// The list can refresh while the form is open; the dialog must not
		// dereference a provider that is gone.
		fieldWith(c, "协议", "供应商已不在列表中", func() {
			gui.Text(c, "无法显示协议，请关闭后重新打开").FontSize(11).TextColor(c.Theme().TextMuted)
		})
		return
	}
	if editableProtocol(*p) {
		fieldWith(c, "协议", "可在 OpenAI Chat / Responses 之间切换", func() {
			options := []string{protocolOpenAIChat, protocolOpenAIResponses}
			label := protocolLabel(s.formProtocol)
			if gui.Select(c, &label, protocolLabels(options)).FillWidth().Changed() {
				s.formProtocol = protocolForLabel(label)
			}
		})
		return
	}
	fieldWith(c, "协议", "由该供应商的适配器决定，不可修改", func() {
		gui.Text(c, s.providerProtocolLabel(*p)).FontSize(11).TextColor(c.Theme().TextMuted)
	})
}

// adapterKinds lists the registered kinds for the form's select.
func (s *uiState) adapterKinds() []string {
	kinds := make([]string, 0, len(s.adapters))
	for _, d := range s.adapters {
		kinds = append(kinds, string(d.Kind))
	}
	return kinds
}

// adapterChoices names each kind for the operator.
func (s *uiState) adapterChoices() string {
	if len(s.adapters) == 0 {
		return ""
	}
	parts := make([]string, 0, len(s.adapters))
	for _, d := range s.adapters {
		parts = append(parts, string(d.Kind)+" = "+d.Label)
	}
	return strings.Join(parts, " · ")
}

// reverseProxySelect is the form's adapter choice inside the reverse-proxy
// group: the options come from the registry, never a hardcoded vendor list.
func (s *uiState) reverseProxySelect(c *gui.Context) {
	adapters := s.reverseProxyAdapters()
	if len(adapters) == 0 {
		gui.Text(c, "没有已注册的反代适配器").FontSize(11).TextColor(c.Theme().TextMuted)
		return
	}
	labels := make([]string, 0, len(adapters))
	current := ""
	for _, d := range adapters {
		labels = append(labels, d.Label)
		if string(d.Kind) == s.formKind {
			current = d.Label
		}
	}
	if gui.Select(c, &current, labels).FillWidth().Changed() {
		for _, d := range adapters {
			if d.Label == current {
				s.formKind = string(d.Kind)
			}
		}
	}
}
