package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/learningdog1/ada_prune/internal/admin"
	"github.com/learningdog1/ada_prune/internal/gateway"
	"github.com/learningdog1/ada_prune/internal/store"
)

// The gateway pages share one shape: a table whose last column is a compact
// dropdown of row actions, and a read-only detail panel under it for the chosen
// row.
//
// The dropdown trigger is sized to fit a text row (11px font, small padding),
// and its menu is drawn as a window overlay, so neither changes the row height.

// providersPage lists upstream services.
func (s *uiState) providersPage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(spXL, spL, spL, spXL).Gap(spM).Children(func() {
			s.errorLine(c)

			ui.Row(c).Gap(spS).AlignItems(ui.Center).Children(func() {
				pill(c, fmt.Sprintf("%d 个", len(s.providers)), t.TextMuted, t.TextMuted.Alpha(0.1))
				ui.Spacer(c)
				if primaryIconButton(c, iconPlus(), "新建供应商").Clicked() {
					s.openNewProvider()
				}
			})

			rows := s.providers
			if len(rows) == 0 {
				card(c, func() {
					emptyStateAction(c, "还没有供应商", "先建一个供应商，再为它添加凭据与模型", func() {
						if primaryIconButton(c, iconPlus(), "新建供应商").Clicked() {
							s.openNewProvider()
						}
					})
				})
				return
			}

			s.providerTable.Selected = &s.providerSel
			s.providerTable.Key = func(i int) any {
				if i >= 0 && i < len(rows) {
					return rows[i].ID
				}
				return i
			}
			card(c, func() {
				ui.Table(c, &s.providerTable, []ui.TableColumn{
					{Title: "名称", Width: 220},
					{Title: "类型", Width: 80},
					{Title: "协议", Width: 190},
					{Title: "凭据", Width: 90, Align: ui.End},
					{Title: "模型", Width: 90, Align: ui.End},
					{Title: "状态"},
					{Title: "操作", Width: 84, Fixed: true, Align: ui.Center},
				}, len(rows), func(row, col int) {
					if row < 0 || row >= len(rows) {
						return
					}
					p := rows[row]
					selected := row == s.providerSel
					primary, secondary := rowColors(c, selected)
					switch col {
					case 0:
						ui.Text(c, p.Name).FontSize(11).TextColor(primary).SingleLine()
					case 1:
						accessModePill(c, p.AccessMode, selected)
					case 2:
						ui.Text(c, s.providerProtocolLabel(p)).FontSize(11).TextColor(secondary).SingleLine()
					case 3:
						ui.Text(c, fmt.Sprintf("%d/%d", p.ActiveCredentialCount, p.CredentialCount)).
							FontSize(11).TextColor(secondary)
					case 4:
						ui.Text(c, fmt.Sprintf("%d/%d", p.EnabledModelCount, p.ModelCount)).
							FontSize(11).TextColor(secondary)
					case 5:
						routablePill(c, p.Routable, p.RoutableReason, selected)
					case 6:
						s.providerRowActions(c, row, p)
					}
				})
			})
			ui.Text(c, "选中一行查看适配器能力与详情。").FontSize(10).TextColor(t.TextMuted)

			if s.providerSel >= 0 && s.providerSel < len(rows) {
				s.providerDetail(c, rows[s.providerSel])
			}
		})
	})
}

// providerForm creates or edits a provider: name, kind, base URL, upstream key,
// network proxy and custom headers.
// providerDialog is the modal body for creating or editing a provider. The modal
// itself is opened by view, so the dialog works from any page.
func (s *uiState) providerDialog(c *ui.Context) {
	t := c.Theme()
	title := "编辑供应商"
	if s.formIsNew {
		title = "新建供应商"
	}

	ui.Column(c).Width(640).Gap(spM).Children(func() {
		ui.Row(c).Gap(spS).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(15).Bold()
			hintIcon(c, "表单", "先选类型再选协议或适配器；密钥加密存储，出参恒为掩码。")
		})

		if s.formError != "" {
			ui.Row(c).FillWidth().Padding(6, 10).Radius(8).
				Background(t.Danger.Alpha(0.1)).Children(func() {
				ui.Text(c, s.formError).FontSize(11).TextColor(t.Danger).SingleLine()
			})
		}

		// 名称与类型同行；其余字段各独占一排，长输入有完整的宽度。
		ui.Row(c).Gap(spL).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).Children(func() {
				fieldWith(c, "名称", "唯一，用于列表与模型默认对外名", func() {
					ui.TextInput(c, &s.formName).FillWidth().Placeholder("例如 Command Code 主号")
				})
			})
			if s.formIsNew {
				// Select 的最小宽度是 140（theme.Space(35)），列宽必须不小于它。
				ui.Column(c).Width(150).Children(func() {
					fieldWith(c, "类型", "接入方式", func() {
						label := accessModeLabel(s.formAccessMode)
						if ui.Select(c, &label, []string{"API", "反代"}).FillWidth().Changed() {
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
			ui.TextInput(c, &s.formBaseURL).FillWidth().Placeholder("https://api.commandcode.ai")
		})
		fieldWith(c, "网络代理", "http / https / socks5，留空直连", func() {
			ui.TextInput(c, &s.formProxy).FillWidth().Placeholder("http://127.0.0.1:7890")
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
			ui.TextInput(c, &s.formSecret).FillWidth().Placeholder(placeholder)
		})

		fieldWith(c, "自定义 Header", "每行一个 “Key: Value”，随每次上游请求发送", func() {
			ui.TextArea(c, &s.formHeaders).FillWidth().Height(72).
				Placeholder("x-custom: 1\nx-org: my-team")
		})

		enabled := s.formEnabled
		ui.Row(c).Gap(spS).AlignItems(ui.Center).Children(func() {
			ui.Toggle(c, &enabled, "启用该供应商")
			if enabled != s.formEnabled {
				s.formEnabled = enabled
			}
		})

		ui.Row(c).FillWidth().Gap(spS).Justify(ui.End).Children(func() {
			if ui.Button(c, "取消").Clicked() {
				s.closeProviderForm()
			}
			if ui.PrimaryButton(c, "保存").Clicked() {
				s.saveProviderForm()
			}
		})
	})
}

// providerProtocolField renders the protocol or adapter choice as its own
// full-width row: new reverse-proxy providers pick a registered adapter, new
// API providers pick a protocol, and editing is limited to what the adapter
// allows.
func (s *uiState) providerProtocolField(c *ui.Context) {
	if s.formIsNew {
		if s.formAccessMode == gateway.AccessReverseProxy {
			fieldWith(c, "适配器", "反代适配器，由注册表提供", func() {
				s.reverseProxySelect(c)
			})
			return
		}
		fieldWith(c, "协议", "决定适配器与上游请求格式", func() {
			label := protocolLabel(s.formProtocol)
			if ui.Select(c, &label, protocolLabels(providerProtocols)).FillWidth().Changed() {
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
			ui.Text(c, "无法显示协议，请关闭后重新打开").FontSize(11).TextColor(c.Theme().TextMuted)
		})
		return
	}
	if editableProtocol(*p) {
		fieldWith(c, "协议", "可在 OpenAI Chat / Responses 之间切换", func() {
			options := []string{protocolOpenAIChat, protocolOpenAIResponses}
			label := protocolLabel(s.formProtocol)
			if ui.Select(c, &label, protocolLabels(options)).FillWidth().Changed() {
				s.formProtocol = protocolForLabel(label)
			}
		})
		return
	}
	fieldWith(c, "协议", "由该供应商的适配器决定，不可修改", func() {
		ui.Text(c, s.providerProtocolLabel(*p)).FontSize(11).TextColor(c.Theme().TextMuted)
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
func (s *uiState) reverseProxySelect(c *ui.Context) {
	adapters := s.reverseProxyAdapters()
	if len(adapters) == 0 {
		ui.Text(c, "没有已注册的反代适配器").FontSize(11).TextColor(c.Theme().TextMuted)
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
	if ui.Select(c, &current, labels).FillWidth().Changed() {
		for _, d := range adapters {
			if d.Label == current {
				s.formKind = string(d.Kind)
			}
		}
	}
}

// providerRowActions is the dropdown of one provider row.
func (s *uiState) providerRowActions(c *ui.Context, index int, p admin.ProviderView) {
	rowActions(c, func(m *ui.Menu) {
		if m.Item("编辑").Chosen() {
			s.openEditProvider(index)
		}
		if m.Item("探活").Chosen() {
			s.probeProvider(index)
		}
		label := "启用"
		if p.Enabled {
			label = "停用"
		}
		if m.Item(label).Chosen() {
			s.toggleProvider(index, !p.Enabled)
		}
		m.Separator()
		if m.Item("删除").Chosen() {
			s.deleteProvider(index)
		}
	})
}

// providerDetail shows the adapter configuration of the chosen provider.
func (s *uiState) providerDetail(c *ui.Context, p admin.ProviderView) {
	t := c.Theme()
	panel(c, "供应商 · "+p.Name, func() {
		ui.Row(c).Gap(spS).Children(func() {
			statusPill(c, p.Enabled, "已启用", "已停用", false)
			accessModePill(c, p.AccessMode, false)
		})

		if p.AdapterCapabilities == nil {
			emptyState(c, "该供应商没有可用的适配器", "检查 providers.kind 是否已注册")
			return
		}
		caps := *p.AdapterCapabilities
		ui.Row(c).Gap(spS).Wrap().Children(func() {
			capability := func(on bool, label string) {
				if on {
					pill(c, label, t.Success, t.Success.Alpha(0.12))
					return
				}
				pill(c, label, t.TextMuted, t.TextMuted.Alpha(0.1))
			}
			capability(caps.Stream, "流式")
			capability(caps.Tools, "工具")
			capability(caps.Vision, "视觉")
			capability(caps.ModelDiscovery, "模型发现")
			capability(caps.AccountUsage, "账号用量")
			capability(caps.Fingerprint, "设备指纹")
			capability(caps.Quota, "额度语义")
		})

		ui.Column(c).Gap(spXS).Children(func() {
			s.infoRow(c, "基础地址", p.BaseURL)
			if p.ProxyURL != "" {
				s.infoRow(c, "网络代理", p.ProxyURL)
			}
			if caps.ProtocolVersion != "" {
				s.infoRow(c, "协议版本", caps.ProtocolVersion)
			}
			if p.Remark != "" {
				s.infoRow(c, "备注", p.Remark)
			}
		})
	})
}

// modelsPage shows callable models as a card grid: every card carries the
// mapping, the capabilities and the same dropdown actions the tables use.
func (s *uiState) modelsPage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(spXL, spL, spL, spXL).Gap(spM).Children(func() {
			s.errorLine(c)

			ui.Row(c).Gap(spS).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconSearch()).FontSize(13).TextColor(t.TextMuted)
				// The catalogue is already in memory; filtering stays local, so
				// typing never round-trips to the store or shows a spinner.
				query := s.modelQuery
				if ui.TextInput(c, &query).Width(220).Placeholder("对外名 / 上游名").Changed() {
					s.modelQuery = query
					s.filterModels()
				}
				ui.Spacer(c)
				pill(c, fmt.Sprintf("%d 个", len(s.models)), t.TextMuted, t.TextMuted.Alpha(0.1))
				if iconButton(c, iconPlus(), "添加模型").Clicked() {
					s.openModelForm()
				}
				if primaryIconButton(c, iconDownload(), "从上游拉取").Clicked() {
					s.openDiscover()
				}
			})

			rows := s.models
			if len(rows) == 0 {
				card(c, func() {
					hint := "点击「添加模型」手动添加，或「从上游拉取」从供应商目录勾选导入"
					if s.modelQuery != "" {
						hint = "换一个关键字试试，或清空搜索后再添加"
					}
					emptyState(c, "没有匹配的模型", hint)
				})
				return
			}

			ui.Grid(c).FillWidth().Columns(3).Gap(spM).Children(func() {
				for i, m := range rows {
					s.modelCard(c, i, m)
				}
			})
		})
	})
}

// modelCard is one model of the grid: the names, the mapping, the capabilities
// and the price, with the actions dropdown in the title row.
func (s *uiState) modelCard(c *ui.Context, index int, m admin.ModelView) {
	t := c.Theme()
	ui.Column(c).FillWidth().Padding(spL).Gap(spS).Radius(12).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(spS).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.PublicName).FontSize(12).Bold().SingleLine().Grow(1).Tooltip(m.PublicName)
			if iconButton(c, iconCopy(), "复制").FontSize(10).Padding(2, 8).Clicked() {
				c.WriteClipboard(m.PublicName)
				s.flash("已复制到剪贴板", false)
			}
			s.modelActions(c, index, m)
		})
		ui.Text(c, m.UpstreamName).FontSize(11).TextColor(t.TextMuted).SingleLine()
		ui.Text(c, m.ProviderName+" · "+protocolLabel(m.UpstreamProtocol)).
			FontSize(11).TextColor(t.TextMuted).SingleLine()

		var caps []string
		if m.SupportsStream {
			caps = append(caps, "流式")
		}
		if m.SupportsTools {
			caps = append(caps, "工具")
		}
		if m.SupportsVision {
			caps = append(caps, "视觉")
		}
		if len(caps) > 0 {
			ui.Row(c).Gap(spXS).Wrap().Children(func() {
				for _, label := range caps {
					pill(c, label, t.Success, t.Success.Alpha(0.12))
				}
			})
		}

		price := "价格未设置"
		if m.InputPrice != 0 || m.OutputPrice != 0 {
			price = fmt.Sprintf("输入 $%s · 输出 $%s（每百万 token）",
				fmtMoney(m.InputPrice), fmtMoney(m.OutputPrice))
		}
		ui.Text(c, price).FontSize(10).TextColor(t.TextMuted).SingleLine()
		context := "上下文未设置"
		if m.ContextWindow > 0 || m.MaxTokens > 0 {
			context = fmt.Sprintf("上下文 %s · 最大输出 %s",
				fmtTokens(int64(m.ContextWindow)), fmtTokens(int64(m.MaxTokens)))
		}
		ui.Text(c, context).FontSize(10).TextColor(t.TextMuted).SingleLine()

		ui.Row(c).Gap(spS).Wrap().Children(func() {
			routablePill(c, m.Routable, "", false)
			statusPill(c, m.Enabled, "已启用", "已停用", false)
		})
		if !m.Routable && m.RoutableReason != "" {
			reason := routableReasonLabel(m.RoutableReason)
			ui.Text(c, reason).FontSize(10).TextColor(t.Warning).SingleLine().Tooltip(reason)
		}
	})
}

// modelActions is the dropdown of one model card.
func (s *uiState) modelActions(c *ui.Context, index int, m admin.ModelView) {
	rowActions(c, func(menu *ui.Menu) {
		if menu.Item("编辑").Chosen() {
			s.openEditModel(index)
		}
		label := "启用"
		if m.Enabled {
			label = "停用"
		}
		if menu.Item(label).Chosen() {
			s.toggleModel(index, !m.Enabled)
		}
		menu.Separator()
		if menu.Item("删除").Chosen() {
			s.deleteModel(index)
		}
	})
}

// modelDialog is the modal body for adding or editing a model. The public id
// is shown with its own copy button, because that string is exactly what the
// client sends in the model field; editing keeps it stable.
func (s *uiState) modelDialog(c *ui.Context) {
	t := c.Theme()
	title := "添加模型"
	if !s.modelFormIsNew {
		title = "编辑模型"
	}
	ui.Column(c).Width(600).Gap(spM).Children(func() {
		ui.Text(c, title).FontSize(15).Bold()
		ui.Text(c, "对外模型 ID 与模型能力；客户端在请求的 model 字段原样回传即可路由。").
			FontSize(11).TextColor(t.TextMuted)

		if s.modelFormError != "" {
			ui.Row(c).FillWidth().Padding(6, 10).Radius(8).
				Background(t.Danger.Alpha(0.1)).Children(func() {
				ui.Text(c, s.modelFormError).FontSize(11).TextColor(t.Danger).SingleLine()
			})
		}

		if len(s.providers) == 0 {
			emptyState(c, "还没有供应商", "先到「供应商」页新建一个，再回来添加模型")
		} else {
			ui.Row(c).Gap(spM).Children(func() {
				ui.Column(c).Grow(1).Children(func() {
					fieldWith(c, "供应商", "模型归属的上游，创建后不可改", func() {
						if s.modelFormIsNew {
							label := s.modelFormProviderName()
							names := make([]string, 0, len(s.providers))
							for _, p := range s.providers {
								names = append(names, p.Name)
							}
							if ui.Select(c, &label, names).FillWidth().Changed() {
								for _, p := range s.providers {
									if p.Name == label {
										s.modelFormProviderID = p.ID
										break
									}
								}
							}
							return
						}
						ui.Text(c, s.modelFormProviderName()).FontSize(11).
							TextColor(t.TextMuted).SingleLine()
					})
				})
				ui.Column(c).Width(180).Children(func() {
					fieldWith(c, "上游协议", "创建时由供应商决定，编辑不可改", func() {
						label := protocolLabel(s.modelFormProtocol)
						ui.Text(c, label).FontSize(11).TextColor(t.TextMuted).SingleLine()
					})
				})
			})

			fieldWith(c, "上游模型名", "上游请求里的 model 值", func() {
				ui.TextInput(c, &s.modelFormUpstream).FillWidth().
					Placeholder("例如 deepseek/deepseek-v4-flash")
			})
			fieldWith(c, "显示名", "仅用于列表展示，留空取上游名", func() {
				ui.TextInput(c, &s.modelFormDisplay).FillWidth()
			})
			fieldWith(c, "对外模型 ID", "客户端在请求的 model 字段回传这一整串", func() {
				ui.Row(c).Gap(spS).AlignItems(ui.Center).Children(func() {
					id := s.modelFormPublicName()
					if strings.TrimSpace(s.modelFormUpstream) == "" && s.modelFormIsNew {
						id = s.modelFormProviderName() + "/…"
					}
					ui.Text(c, id).FontSize(12).Bold().SingleLine().Grow(1).Tooltip(id)
					copyBtn := iconButton(c, iconCopy(), "复制 ID").FontSize(10).Padding(2, 8)
					if strings.TrimSpace(s.modelFormUpstream) == "" && s.modelFormIsNew {
						copyBtn.Disabled(true)
					}
					if copyBtn.Clicked() {
						c.WriteClipboard(id)
						s.flash("已复制到剪贴板", false)
					}
				})
			})

			ui.Row(c).Gap(spM).Children(func() {
				ui.Column(c).Grow(1).Children(func() {
					fieldWith(c, "上下文", "上下文窗口 token 数", func() {
						ui.TextInput(c, &s.modelFormContext).FillWidth().Placeholder("例如 128000")
					})
				})
				ui.Column(c).Grow(1).Children(func() {
					fieldWith(c, "最大输出", "单次生成上限 token 数", func() {
						ui.TextInput(c, &s.modelFormMaxTokens).FillWidth().Placeholder("例如 64000")
					})
				})
			})

			ui.Row(c).Gap(spM).Children(func() {
				ui.Column(c).Grow(1).Children(func() {
					fieldWith(c, "输入价格", "每百万 token，美元", func() {
						ui.TextInput(c, &s.modelFormInputPrice).FillWidth().Placeholder("例如 0.5")
					})
				})
				ui.Column(c).Grow(1).Children(func() {
					fieldWith(c, "缓存价格", "命中缓存的输入价格，每百万 token", func() {
						ui.TextInput(c, &s.modelFormCachePrice).FillWidth().Placeholder("例如 0.1")
					})
				})
				ui.Column(c).Grow(1).Children(func() {
					fieldWith(c, "输出价格", "每百万 token，美元", func() {
						ui.TextInput(c, &s.modelFormOutputPrice).FillWidth().Placeholder("例如 1.5")
					})
				})
			})

			ui.Row(c).Gap(spL).AlignItems(ui.Center).Children(func() {
				vision := s.modelFormVision
				ui.Checkbox(c, &vision, "支持识图（视觉）")
				s.modelFormVision = vision

				tools := s.modelFormTools
				ui.Checkbox(c, &tools, "支持工具调用")
				s.modelFormTools = tools

				enabled := s.modelFormEnabled
				ui.Toggle(c, &enabled, "启用该模型")
				s.modelFormEnabled = enabled
			})
		}

		ui.Row(c).FillWidth().Gap(spS).Justify(ui.End).Children(func() {
			if ui.Button(c, "取消").Clicked() {
				s.modelFormOpen = false
				s.modelFormError = ""
			}
			save := ui.PrimaryButton(c, "保存")
			if len(s.providers) == 0 || s.modelFormSaving {
				save.Disabled(true)
			}
			if save.Clicked() {
				s.saveModelForm()
			}
		})
	})
}

// discoverDialog is the modal body of the upstream catalogue import: pick a
// provider, fetch its /models list, check what to create.
func (s *uiState) discoverDialog(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(560).Gap(spM).Children(func() {
		ui.Text(c, "从上游拉取模型").FontSize(15).Bold()
		ui.Text(c, "调用供应商的模型目录接口（OpenAI 兼容即 /v1/models），勾选要导入的模型。").
			FontSize(11).TextColor(t.TextMuted)

		options := s.discoverableProviders()
		if len(options) == 0 {
			emptyState(c, "没有支持模型发现的供应商", "OpenAI 兼容与 Command Code 适配器支持拉取模型目录")
		} else {
			names := make([]string, 0, len(options))
			current := options[0].Name
			for _, p := range options {
				names = append(names, p.Name)
				if p.ID == s.discoverProviderID {
					current = p.Name
				}
			}
			ui.Row(c).Gap(spS).AlignItems(ui.Center).Children(func() {
				label := current
				if ui.Select(c, &label, names).Width(260).Changed() {
					for _, p := range options {
						if p.Name == label {
							s.discoverProviderID = p.ID
							break
						}
					}
					s.discoverRows = nil
					s.discoverChecked = map[string]bool{}
					s.discoverError = ""
					s.discoverSummary = ""
				}
				if iconButton(c, iconDownload(), "拉取模型列表").Disabled(s.discoverLoading).Clicked() {
					s.fetchDiscovered()
				}
			})
		}

		if s.discoverLoading {
			ui.Row(c).Gap(spS).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				ui.Text(c, "正在拉取模型目录…").FontSize(11).TextColor(t.TextMuted)
			})
		}
		if s.discoverError != "" {
			ui.Row(c).FillWidth().Padding(6, 10).Radius(8).
				Background(t.Danger.Alpha(0.1)).Children(func() {
				ui.Text(c, s.discoverError).FontSize(11).TextColor(t.Danger).SingleLine()
			})
		}
		if s.discoverSummary != "" {
			ui.Row(c).FillWidth().Padding(6, 10).Radius(8).
				Background(t.Success.Alpha(0.1)).Children(func() {
				ui.Text(c, s.discoverSummary).FontSize(11).TextColor(t.Success).SingleLine()
			})
		}

		if len(s.discoverRows) > 0 {
			s.discoverList(c)
		}

		ui.Row(c).Gap(spS).Children(func() {
			checked := s.discoverCheckedCount()
			importBtn := primaryIconButton(c, iconDownload(), fmt.Sprintf("导入所选（%d）", checked))
			if checked == 0 || s.discoverImporting {
				importBtn.Disabled(true)
			}
			if importBtn.Clicked() {
				s.importDiscovered()
			}
			if ui.Button(c, "关闭").Clicked() {
				s.discoverOpen = false
			}
		})
	})
}

// discoverList is the catalogue: one checkbox per upstream model, the ones
// already imported shown disabled.
func (s *uiState) discoverList(c *ui.Context) {
	t := c.Theme()
	all := true
	importable := 0
	for _, r := range s.discoverRows {
		if r.Exists {
			continue
		}
		importable++
		if !s.discoverChecked[r.UpstreamName] {
			all = false
		}
	}
	ui.Row(c).Gap(spS).AlignItems(ui.Center).Children(func() {
		if importable > 0 {
			checked := all
			ui.Checkbox(c, &checked, "全选")
			if checked != all {
				for _, r := range s.discoverRows {
					if !r.Exists {
						s.discoverChecked[r.UpstreamName] = checked
					}
				}
			}
		}
		ui.Spacer(c)
		pill(c, fmt.Sprintf("%d 个模型", len(s.discoverRows)), t.TextMuted, t.TextMuted.Alpha(0.1))
	})
	ui.Scroll(c).Height(280).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(spXS).Children(func() {
			providerName := s.discoverProviderName()
			for _, r := range s.discoverRows {
				name := r.UpstreamName
				if r.Exists {
					ui.Row(c).Gap(spS).AlignItems(ui.Center).Children(func() {
						checked := false
						ui.Checkbox(c, &checked, name).Disabled(true)
						pill(c, "已导入", t.TextMuted, t.TextMuted.Alpha(0.1))
					})
					continue
				}
				checked := s.discoverChecked[name]
				ui.Row(c).Gap(spS).AlignItems(ui.Center).Children(func() {
					ui.Checkbox(c, &checked, name)
					// 预览导入后的对外名：供应商/上游名。
					ui.Text(c, providerName+"/"+name).FontSize(10).TextColor(t.TextMuted).SingleLine().Grow(1)
				})
				if checked != s.discoverChecked[name] {
					s.discoverChecked[name] = checked
				}
			}
		})
	})
}

// discoverCheckedCount counts the checked, not-yet-imported models.
func (s *uiState) discoverCheckedCount() int {
	n := 0
	for _, r := range s.discoverRows {
		if !r.Exists && s.discoverChecked[r.UpstreamName] {
			n++
		}
	}
	return n
}

// credentialsPage lists upstream keys.
func (s *uiState) credentialsPage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(spXL, spL, spL, spXL).Gap(spM).Children(func() {
			s.errorLine(c)

			ui.Row(c).Gap(spS).AlignItems(ui.Center).Children(func() {
				pill(c, fmt.Sprintf("%d 个", len(s.credentials)), t.TextMuted, t.TextMuted.Alpha(0.1))
				ui.Spacer(c)
				if iconButton(c, iconRefresh(), "刷新额度").Clicked() {
					s.run("刷新额度", func() error {
						_, err := s.app.Admin.RefreshAllUsage(s.ctx())
						return err
					})
				}
			})

			rows := s.credentials
			if len(rows) == 0 {
				card(c, func() {
					emptyStateAction(c, "还没有凭据", "在供应商页添加上游密钥，或从 CLI 登录导入", func() {
						if iconButton(c, iconProviders(), "前往供应商页").Clicked() {
							s.navigate("/providers")
						}
					})
				})
				return
			}

			s.credTable.Selected = &s.credSel
			s.credTable.Key = func(i int) any {
				if i >= 0 && i < len(rows) {
					return rows[i].ID
				}
				return i
			}
			card(c, func() {
				ui.Table(c, &s.credTable, []ui.TableColumn{
					{Title: "名称", Width: 200},
					{Title: "供应商", Width: 190},
					{Title: "密钥", Width: 210},
					{Title: "状态", Width: 120},
					{Title: "固定", Width: 80},
					{Title: "最近使用"},
					{Title: "操作", Width: 84, Fixed: true, Align: ui.Center},
				}, len(rows), func(row, col int) {
					if row < 0 || row >= len(rows) {
						return
					}
					entry := rows[row]
					selected := row == s.credSel
					primary, secondary := rowColors(c, selected)
					switch col {
					case 0:
						ui.Text(c, entry.Name).FontSize(11).TextColor(primary).SingleLine()
					case 1:
						ui.Text(c, entry.ProviderName).FontSize(11).TextColor(secondary).SingleLine()
					case 2:
						ui.Text(c, entry.SecretMask).FontSize(11).TextColor(secondary).SingleLine()
					case 3:
						credentialPill(c, entry, selected)
					case 4:
						if entry.Pinned {
							fg, bg := pillTone(c, selected, t.Accent)
							pill(c, "已固定", fg, bg)
						} else {
							ui.Text(c, "-").FontSize(11).TextColor(secondary)
						}
					case 5:
						last := "-"
						if entry.LastUsedAt != nil {
							last = formatTime(*entry.LastUsedAt)
						}
						ui.Text(c, last).FontSize(11).TextColor(secondary)
					case 6:
						s.credentialRowActions(c, row, entry)
					}
				})
			})

			if s.credSel >= 0 && s.credSel < len(rows) {
				s.credentialDetail(c, rows[s.credSel])
			}
		})
	})
}

// credentialRowActions is the dropdown of one credential row.
func (s *uiState) credentialRowActions(c *ui.Context, index int, entry store.Credential) {
	rowActions(c, func(m *ui.Menu) {
		if m.Item("固定到路由首选项").Chosen() {
			s.pinCredential(index)
		}
		if entry.Status == store.CredentialExhausted {
			if m.Item("重置为可用").Chosen() {
				s.setCredentialStatus(index, store.CredentialActive)
			}
		} else if m.Item("停用").Chosen() {
			s.setCredentialStatus(index, store.CredentialDisabled)
		}
		m.Separator()
		if m.Item("删除").Chosen() {
			s.deleteCredential(index)
		}
	})
}

func (s *uiState) credentialDetail(c *ui.Context, entry store.Credential) {
	panel(c, "凭据 · "+entry.Name, func() {
		ui.Row(c).Children(func() { credentialPill(c, entry, false) })
		ui.Column(c).Gap(spXS).Children(func() {
			s.infoRow(c, "供应商", entry.ProviderName)
			s.infoRow(c, "密钥", entry.SecretMask+"（明文只存在服务端）")
			if entry.Priority != 0 {
				s.infoRow(c, "优先级", fmt.Sprintf("%d", entry.Priority))
			}
			if entry.ExpiresAt != nil {
				s.infoRow(c, "过期时间", formatTime(*entry.ExpiresAt))
			}
			if entry.Remark != "" {
				s.infoRow(c, "备注", entry.Remark)
			}
		})
	})
}

// keysPage manages downstream keys.
func (s *uiState) keysPage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(spXL, spL, spL, spXL).Gap(spM).Children(func() {
			s.errorLine(c)

			panel(c, "新建客户端密钥", func() {
				ui.Row(c).Gap(spS).AlignItems(ui.Center).Children(func() {
					ui.TextInput(c, &s.newKeyName).Width(220).Placeholder("名称，例如 codex-local")
					if primaryIconButton(c, iconPlus(), "创建").Clicked() {
						s.createClientKey()
					}
				})
				if s.createdToken != "" {
					ui.Row(c).FillWidth().Padding(8, 12).Radius(8).
						Background(t.Accent.Alpha(0.1)).Gap(spS).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "明文:").FontSize(11).TextColor(t.Accent)
						ui.Text(c, s.createdToken).FontSize(11).Bold().SingleLine().Grow(1).Tooltip(s.createdToken)
						if iconButton(c, iconCopy(), "复制").FontSize(11).Padding(2, 8).Clicked() {
							c.WriteClipboard(s.createdToken)
							s.flash("已复制到剪贴板", false)
						}
						// The banner answers only to its owner: closing it (or
						// leaving the page) clears it from state. The plaintext
						// itself stays retrievable through the row actions.
						if ui.Button(c, "关闭").FontSize(11).Padding(2, 8).Clicked() {
							s.createdToken = ""
						}
					})
				}
				ui.Text(c, "密钥加密存储；校验用 SHA-256 哈希，行操作里可随时取回明文。").FontSize(10).TextColor(t.TextMuted)
			})

			rows := s.clientKeys
			if len(rows) == 0 {
				card(c, func() {
					emptyState(c, "还没有客户端密钥", "下游工具用 sk-prune-* 作为 Bearer token")
				})
				return
			}

			s.keyTable.Selected = &s.keySel
			s.keyTable.Key = func(i int) any {
				if i >= 0 && i < len(rows) {
					return rows[i].ID
				}
				return i
			}
			card(c, func() {
				ui.Table(c, &s.keyTable, []ui.TableColumn{
					{Title: "名称", Width: 190},
					{Title: "前缀", Width: 210},
					{Title: "状态", Width: 100},
					{Title: "额度", Width: 140, Align: ui.End},
					{Title: "最近使用"},
					{Title: "操作", Width: 84, Fixed: true, Align: ui.Center},
				}, len(rows), func(row, col int) {
					if row < 0 || row >= len(rows) {
						return
					}
					entry := rows[row]
					selected := row == s.keySel
					primary, secondary := rowColors(c, selected)
					switch col {
					case 0:
						ui.Text(c, entry.Name).FontSize(11).TextColor(primary).SingleLine()
					case 1:
						ui.Text(c, entry.KeyPrefix).FontSize(11).TextColor(secondary).SingleLine()
					case 2:
						statusPill(c, entry.Enabled, "启用", "停用", selected)
					case 3:
						quota := "不限"
						if entry.QuotaTokens > 0 {
							quota = fmt.Sprintf("%s/%s", fmtTokens(entry.QuotaUsed), fmtTokens(entry.QuotaTokens))
						}
						ui.Text(c, quota).FontSize(11).TextColor(secondary)
					case 4:
						last := "-"
						if entry.LastUsedAt != nil {
							last = formatTime(*entry.LastUsedAt)
						}
						ui.Text(c, last).FontSize(11).TextColor(secondary)
					case 5:
						s.keyRowActions(c, row, entry)
					}
				})
			})

			if s.keySel >= 0 && s.keySel < len(rows) {
				s.keyDetail(c, rows[s.keySel])
			}
		})
	})
}

// keyRowActions is the dropdown of one client key row.
func (s *uiState) keyRowActions(c *ui.Context, index int, entry store.ClientKey) {
	rowActions(c, func(m *ui.Menu) {
		if m.Item("复制").Chosen() {
			s.copyClientKey(c, index)
		}
		if m.Item("轮换").Chosen() {
			s.rotateClientKey(index)
		}
		m.Separator()
		if m.Item("删除").Chosen() {
			s.deleteClientKey(index)
		}
	})
}

func (s *uiState) keyDetail(c *ui.Context, entry store.ClientKey) {
	panel(c, "密钥 · "+entry.Name, func() {
		ui.Row(c).Children(func() { statusPill(c, entry.Enabled, "启用", "停用", false) })
		ui.Column(c).Gap(spXS).Children(func() {
			s.infoRow(c, "前缀", entry.KeyPrefix)
			quota := "不限"
			if entry.QuotaTokens > 0 {
				quota = fmt.Sprintf("%s / %s token", fmtTokens(entry.QuotaUsed), fmtTokens(entry.QuotaTokens))
			}
			s.infoRow(c, "额度", quota)
			if len(entry.AllowedModels) > 0 {
				s.infoRow(c, "允许模型", strings.Join(entry.AllowedModels, ", "))
			} else {
				s.infoRow(c, "允许模型", "全部")
			}
			if entry.AllowedIPs != "" {
				s.infoRow(c, "允许来源", entry.AllowedIPs)
			}
			if entry.ExpiresAt != nil {
				s.infoRow(c, "过期时间", formatTime(*entry.ExpiresAt))
			}
		})
	})
}

// endpointsPage lists data-plane entry points.
func (s *uiState) endpointsPage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(spXL, spL, spL, spXL).Gap(spM).Children(func() {
			s.errorLine(c)
			card(c, func() {
				ui.Text(c, "数据面入口").FontSize(13).Bold()
				ui.Divider(c)
				rows := s.endpoints
				if len(rows) == 0 {
					emptyState(c, "没有端点", "内置的三条端点始终可用")
					return
				}
				ui.Column(c).Gap(spS).Children(func() {
					for _, e := range rows {
						ui.Row(c).FillWidth().Gap(spS).AlignItems(ui.Center).Children(func() {
							ui.Text(c, e.Path).FontSize(11).Width(240).SingleLine()
							ui.Text(c, e.InboundProtocol).FontSize(11).TextColor(t.TextMuted).Width(140)
							if e.Builtin {
								pill(c, "内置", t.Accent, t.Accent.Alpha(0.12))
							} else {
								pill(c, "自定义", t.TextMuted, t.TextMuted.Alpha(0.1))
							}
							statusPill(c, e.Enabled, "启用", "停用", false)
							ui.Spacer(c)
							if e.AuthRequired {
								pill(c, "需鉴权", t.TextMuted, t.TextMuted.Alpha(0.1))
							}
						})
					}
				})
			})
			ui.Text(c, "自定义端点当前通过管理 API 创建（见 docs/05 契约）。").FontSize(10).TextColor(t.TextMuted)
		})
	})
}

// infoRow is a label/value line in a detail panel. Long values truncate, so
// the full text rides along as a tooltip.
func (s *uiState) infoRow(c *ui.Context, label, value string) {
	t := c.Theme()
	ui.Row(c).Gap(spS).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(t.TextMuted).Width(96)
		ui.Text(c, value).FontSize(11).SingleLine().Tooltip(value)
	})
}

// credentialPill renders the routing state of a credential.
func credentialPill(c *ui.Context, entry store.Credential, selected bool) {
	t := c.Theme()
	tone, text := t.TextMuted, entry.Status
	if !entry.Enabled {
		text = "已停用"
	} else {
		switch entry.Status {
		case store.CredentialActive:
			tone, text = t.Success, "可用"
		case store.CredentialExhausted:
			tone, text = t.Warning, "额度耗尽"
		case store.CredentialDisabled:
			text = "已停用"
		}
	}
	fg, bg := pillTone(c, selected, tone)
	pill(c, text, fg, bg)
}
