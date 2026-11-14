package ui

import (
	"fmt"
	gui "github.com/egoist/mygo/ui"
	"github.com/learningdog1/ada_prune/internal/admin"
	"strings"
)

// modelsPage shows callable models as a card grid: every card carries the
// mapping, the capabilities and the same dropdown actions the tables use.
func (s *uiState) modelsPage(c *gui.Context) {
	t := c.Theme()
	gui.Scroll(c).Grow(1).FillWidth().Children(func() {
		gui.Column(c).FillWidth().Padding(32, spL, spL, 32).Gap(spM).Children(func() {
			s.errorLine(c)

			gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
				gui.Icon(c, iconSearch()).FontSize(13).TextColor(t.TextMuted)
				// The catalogue is already in memory; filtering stays local, so
				// typing never round-trips to the store or shows a spinner.
				query := s.modelQuery
				if gui.TextInput(c, &query).Width(220).Placeholder("对外名 / 上游名").Changed() {
					s.modelQuery = query
					s.filterModels()
				}
				gui.Spacer(c)
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

			gui.Grid(c).FillWidth().Columns(3).Gap(spM).Children(func() {
				for i, m := range rows {
					s.modelCard(c, i, m)
				}
			})
		})
	})
}

// modelCard is one model of the grid: the names, the mapping, the capabilities
// and the price, with the actions dropdown in the title row.
func (s *uiState) modelCard(c *gui.Context, index int, m admin.ModelView) {
	t := c.Theme()
	gui.Column(c).FillWidth().Padding(spL).Gap(spS).Radius(12).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
			gui.Text(c, m.PublicName).FontSize(12).Bold().SingleLine().Grow(1).Tooltip(m.PublicName)
			if iconButton(c, iconCopy(), "复制").FontSize(10).Padding(2, 8).Clicked() {
				c.WriteClipboard(m.PublicName)
				s.flash("已复制到剪贴板", false)
			}
			s.modelActions(c, index, m)
		})
		gui.Text(c, m.UpstreamName).FontSize(11).TextColor(t.TextMuted).SingleLine()
		gui.Text(c, m.ProviderName+" · "+protocolLabel(m.UpstreamProtocol)).
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
			gui.Row(c).Gap(spXS).Wrap().Children(func() {
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
		gui.Text(c, price).FontSize(10).TextColor(t.TextMuted).SingleLine()
		context := "上下文未设置"
		if m.ContextWindow > 0 || m.MaxTokens > 0 {
			context = fmt.Sprintf("上下文 %s · 最大输出 %s",
				fmtTokens(int64(m.ContextWindow)), fmtTokens(int64(m.MaxTokens)))
		}
		gui.Text(c, context).FontSize(10).TextColor(t.TextMuted).SingleLine()

		gui.Row(c).Gap(spS).Wrap().Children(func() {
			routablePill(c, m.Routable, "", false)
			statusPill(c, m.Enabled, "已启用", "已停用", false)
		})
		if !m.Routable && m.RoutableReason != "" {
			reason := routableReasonLabel(m.RoutableReason)
			gui.Text(c, reason).FontSize(10).TextColor(t.Warning).SingleLine().Tooltip(reason)
		}
	})
}

// modelActions is the dropdown of one model card.
func (s *uiState) modelActions(c *gui.Context, index int, m admin.ModelView) {
	rowActions(c, func(menu *gui.Menu) {
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
func (s *uiState) modelDialog(c *gui.Context) {
	t := c.Theme()
	title := "添加模型"
	if !s.modelFormIsNew {
		title = "编辑模型"
	}
	gui.Column(c).Width(600).Gap(spM).Children(func() {
		gui.Text(c, title).FontSize(15).Bold()
		gui.Text(c, "对外模型 ID 与模型能力；客户端在请求的 model 字段原样回传即可路由。").
			FontSize(11).TextColor(t.TextMuted)

		if s.modelFormError != "" {
			gui.Row(c).FillWidth().Padding(6, 10).Radius(8).
				Background(t.Danger.Alpha(0.1)).Children(func() {
				gui.Text(c, s.modelFormError).FontSize(11).TextColor(t.Danger).SingleLine()
			})
		}

		if len(s.providers) == 0 {
			emptyState(c, "还没有供应商", "先到「供应商」页新建一个，再回来添加模型")
		} else {
			gui.Row(c).Gap(spM).Children(func() {
				gui.Column(c).Grow(1).Children(func() {
					fieldWith(c, "供应商", "模型归属的上游，创建后不可改", func() {
						if s.modelFormIsNew {
							label := s.modelFormProviderName()
							names := make([]string, 0, len(s.providers))
							for _, p := range s.providers {
								names = append(names, p.Name)
							}
							if gui.Select(c, &label, names).FillWidth().Changed() {
								for _, p := range s.providers {
									if p.Name == label {
										s.modelFormProviderID = p.ID
										break
									}
								}
							}
							return
						}
						gui.Text(c, s.modelFormProviderName()).FontSize(11).
							TextColor(t.TextMuted).SingleLine()
					})
				})
				gui.Column(c).Width(180).Children(func() {
					fieldWith(c, "上游协议", "创建时由供应商决定，编辑不可改", func() {
						label := protocolLabel(s.modelFormProtocol)
						gui.Text(c, label).FontSize(11).TextColor(t.TextMuted).SingleLine()
					})
				})
			})

			fieldWith(c, "上游模型名", "上游请求里的 model 值", func() {
				gui.TextInput(c, &s.modelFormUpstream).FillWidth().
					Placeholder("例如 deepseek/deepseek-v4-flash")
			})
			fieldWith(c, "显示名", "仅用于列表展示，留空取上游名", func() {
				gui.TextInput(c, &s.modelFormDisplay).FillWidth()
			})
			fieldWith(c, "对外模型 ID", "客户端在请求的 model 字段回传这一整串", func() {
				gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
					id := s.modelFormPublicName()
					if strings.TrimSpace(s.modelFormUpstream) == "" && s.modelFormIsNew {
						id = s.modelFormProviderName() + "/…"
					}
					gui.Text(c, id).FontSize(12).Bold().SingleLine().Grow(1).Tooltip(id)
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

			gui.Row(c).Gap(spM).Children(func() {
				gui.Column(c).Grow(1).Children(func() {
					fieldWith(c, "上下文", "上下文窗口 token 数", func() {
						gui.TextInput(c, &s.modelFormContext).FillWidth().Placeholder("例如 128000")
					})
				})
				gui.Column(c).Grow(1).Children(func() {
					fieldWith(c, "最大输出", "单次生成上限 token 数", func() {
						gui.TextInput(c, &s.modelFormMaxTokens).FillWidth().Placeholder("例如 64000")
					})
				})
			})

			gui.Row(c).Gap(spM).Children(func() {
				gui.Column(c).Grow(1).Children(func() {
					fieldWith(c, "输入价格", "每百万 token，美元", func() {
						gui.TextInput(c, &s.modelFormInputPrice).FillWidth().Placeholder("例如 0.5")
					})
				})
				gui.Column(c).Grow(1).Children(func() {
					fieldWith(c, "缓存价格", "命中缓存的输入价格，每百万 token", func() {
						gui.TextInput(c, &s.modelFormCachePrice).FillWidth().Placeholder("例如 0.1")
					})
				})
				gui.Column(c).Grow(1).Children(func() {
					fieldWith(c, "输出价格", "每百万 token，美元", func() {
						gui.TextInput(c, &s.modelFormOutputPrice).FillWidth().Placeholder("例如 1.5")
					})
				})
			})

			gui.Row(c).Gap(spL).AlignItems(gui.Center).Children(func() {
				vision := s.modelFormVision
				gui.Checkbox(c, &vision, "支持识图（视觉）")
				s.modelFormVision = vision

				tools := s.modelFormTools
				gui.Checkbox(c, &tools, "支持工具调用")
				s.modelFormTools = tools

				enabled := s.modelFormEnabled
				gui.Toggle(c, &enabled, "启用该模型")
				s.modelFormEnabled = enabled
			})
		}

		gui.Row(c).FillWidth().Gap(spS).Justify(gui.End).Children(func() {
			if gui.Button(c, "取消").Clicked() {
				s.modelFormOpen = false
				s.modelFormError = ""
			}
			save := gui.PrimaryButton(c, "保存")
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
func (s *uiState) discoverDialog(c *gui.Context) {
	t := c.Theme()
	gui.Column(c).Width(560).Gap(spM).Children(func() {
		gui.Text(c, "从上游拉取模型").FontSize(15).Bold()
		gui.Text(c, "调用供应商的模型目录接口（OpenAI 兼容即 /v1/models），勾选要导入的模型。").
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
			gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
				label := current
				if gui.Select(c, &label, names).Width(260).Changed() {
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
			gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
				gui.Spinner(c)
				gui.Text(c, "正在拉取模型目录…").FontSize(11).TextColor(t.TextMuted)
			})
		}
		if s.discoverError != "" {
			gui.Row(c).FillWidth().Padding(6, 10).Radius(8).
				Background(t.Danger.Alpha(0.1)).Children(func() {
				gui.Text(c, s.discoverError).FontSize(11).TextColor(t.Danger).SingleLine()
			})
		}
		if s.discoverSummary != "" {
			gui.Row(c).FillWidth().Padding(6, 10).Radius(8).
				Background(t.Success.Alpha(0.1)).Children(func() {
				gui.Text(c, s.discoverSummary).FontSize(11).TextColor(t.Success).SingleLine()
			})
		}

		if len(s.discoverRows) > 0 {
			s.discoverList(c)
		}

		gui.Row(c).Gap(spS).Children(func() {
			checked := s.discoverCheckedCount()
			importBtn := primaryIconButton(c, iconDownload(), fmt.Sprintf("导入所选（%d）", checked))
			if checked == 0 || s.discoverImporting {
				importBtn.Disabled(true)
			}
			if importBtn.Clicked() {
				s.importDiscovered()
			}
			if gui.Button(c, "关闭").Clicked() {
				s.discoverOpen = false
			}
		})
	})
}

// discoverList is the catalogue: one checkbox per upstream model, the ones
// already imported shown disabled.
func (s *uiState) discoverList(c *gui.Context) {
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
	gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
		if importable > 0 {
			checked := all
			gui.Checkbox(c, &checked, "全选")
			if checked != all {
				for _, r := range s.discoverRows {
					if !r.Exists {
						s.discoverChecked[r.UpstreamName] = checked
					}
				}
			}
		}
		gui.Spacer(c)
		pill(c, fmt.Sprintf("%d 个模型", len(s.discoverRows)), t.TextMuted, t.TextMuted.Alpha(0.1))
	})
	gui.Scroll(c).Height(280).FillWidth().Children(func() {
		gui.Column(c).FillWidth().Gap(spXS).Children(func() {
			providerName := s.discoverProviderName()
			for _, r := range s.discoverRows {
				name := r.UpstreamName
				if r.Exists {
					gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
						checked := false
						gui.Checkbox(c, &checked, name).Disabled(true)
						pill(c, "已导入", t.TextMuted, t.TextMuted.Alpha(0.1))
					})
					continue
				}
				checked := s.discoverChecked[name]
				gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
					gui.Checkbox(c, &checked, name)
					// 预览导入后的对外名：供应商/上游名。
					gui.Text(c, providerName+"/"+name).FontSize(10).TextColor(t.TextMuted).SingleLine().Grow(1)
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
