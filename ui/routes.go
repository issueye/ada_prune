package ui

import (
	"fmt"

	gui "github.com/egoist/mygo/ui"

	"github.com/learningdog1/ada_prune/internal/admin"
	"github.com/learningdog1/ada_prune/internal/store"
)

// routeModelsPage aggregates models from several providers under one public
// name. The name is what clients send in the model field; the members are the
// failover chain.
func (s *uiState) routeModelsPage(c *gui.Context) {
	t := c.Theme()
	gui.Scroll(c).Grow(1).FillWidth().Children(func() {
		gui.Column(c).FillWidth().Padding(32, spL, spL, 32).Gap(spM).Children(func() {
			s.errorLine(c)

			gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
				gui.Text(c, "把多家供应商的模型聚合成一个对外 ID，客户端只认这个 ID。").
					FontSize(11).TextColor(t.TextMuted).SingleLine()
				gui.Spacer(c)
				pill(c, fmt.Sprintf("%d 个", len(s.routeModels)), t.TextMuted, t.TextMuted.Alpha(0.1))
				if iconButton(c, iconPlus(), "新建路由模型").Clicked() {
					s.openRouteForm()
				}
			})

			rows := s.routeModels
			if len(rows) == 0 {
				card(c, func() {
					emptyStateAction(c, "还没有路由模型", "把一个或多个供应商的成员模型聚合成一个对外 ID", func() {
						if primaryIconButton(c, iconPlus(), "新建路由模型").Clicked() {
							s.openRouteForm()
						}
					})
				})
				return
			}

			gui.Grid(c).FillWidth().Columns(2).Gap(spM).Children(func() {
				for i, r := range rows {
					s.routeCard(c, i, r)
				}
			})
		})
	})
}

// routeCard is one route model: the public id with its copy button, the
// strategy, and the member chain with each member's priority.
func (s *uiState) routeCard(c *gui.Context, index int, r store.RouteModel) {
	t := c.Theme()
	gui.Column(c).FillWidth().Padding(spL).Gap(spS).Radius(12).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
			gui.Text(c, r.Name).FontSize(12).Bold().SingleLine().Grow(1).Tooltip(r.Name)
			if iconButton(c, iconCopy(), "复制").FontSize(10).Padding(2, 8).Clicked() {
				c.WriteClipboard(r.Name)
				s.flash("已复制到剪贴板", false)
			}
			s.routeActions(c, index, r)
		})

		strategy := "优先级（主备）"
		if r.Strategy == store.StrategyWeighted {
			strategy = "加权"
		}
		gui.Text(c, fmt.Sprintf("%s · %d 个成员", strategy, len(r.Members))).
			FontSize(11).TextColor(t.TextMuted).SingleLine()
		if r.Remark != "" {
			gui.Text(c, r.Remark).FontSize(10).TextColor(t.TextMuted).SingleLine()
		}

		gui.Column(c).Gap(2).Children(func() {
			for _, member := range r.Members {
				s.routeMemberRow(c, member)
			}
		})

		gui.Row(c).Gap(spS).Wrap().Children(func() {
			routablePill(c, s.routeRoutable(r), "", false)
			statusPill(c, r.Enabled, "已启用", "已停用", false)
		})
		if r.Enabled && !s.routeRoutable(r) {
			gui.Text(c, "成员都不可路由：检查模型、供应商和凭据是否启用").
				FontSize(10).TextColor(t.Warning).SingleLine()
		}
	})
}

// routeMemberRow is one member of a route card.
func (s *uiState) routeMemberRow(c *gui.Context, member store.RouteModelMember) {
	t := c.Theme()
	gui.Row(c).FillWidth().Gap(spS).AlignItems(gui.Center).Children(func() {
		gui.Text(c, member.ProviderName).FontSize(10).TextColor(t.TextMuted).Width(96).SingleLine()
		gui.Text(c, member.PublicName).FontSize(11).Grow(1).SingleLine().Tooltip(member.PublicName)
		pill(c, fmt.Sprintf("P%d", member.Priority), t.TextMuted, t.TextMuted.Alpha(0.1))
		if !member.Enabled {
			pill(c, "成员停用", t.Warning, t.Warning.Alpha(0.12))
		}
	})
}

// routeActions is the dropdown of one route card.
func (s *uiState) routeActions(c *gui.Context, index int, r store.RouteModel) {
	rowActions(c, func(menu *gui.Menu) {
		if menu.Item("编辑").Chosen() {
			s.openEditRoute(index)
		}
		label := "启用"
		if r.Enabled {
			label = "停用"
		}
		if menu.Item(label).Chosen() {
			s.toggleRoute(index, !r.Enabled)
		}
		menu.Separator()
		if menu.Item("删除").Chosen() {
			s.deleteRoute(index)
		}
	})
}

// routeDialog is the create/edit modal. It lists every model of every provider
// with a checkbox, so one route can span providers; priority decides the
// failover order.
func (s *uiState) routeDialog(c *gui.Context) {
	t := c.Theme()
	title := "新建路由模型"
	if !s.routeFormIsNew {
		title = "编辑路由模型"
	}
	gui.Column(c).Width(640).Gap(spM).Children(func() {
		gui.Text(c, title).FontSize(15).Bold()
		gui.Text(c, "成员可以来自不同供应商；客户端请求的 model 字段填路由模型的 ID。").
			FontSize(11).TextColor(t.TextMuted)

		if s.routeFormError != "" {
			gui.Row(c).FillWidth().Padding(6, 10).Radius(8).
				Background(t.Danger.Alpha(0.1)).Children(func() {
				gui.Text(c, s.routeFormError).FontSize(11).TextColor(t.Danger).SingleLine()
			})
		}

		fieldWith(c, "路由模型 ID", "客户端在 model 字段回传这一整串；不能与已有模型 ID 重复", func() {
			gui.TextInput(c, &s.routeFormName).FillWidth().Placeholder("例如 fast")
		})
		fieldWith(c, "备注", "仅用于列表展示，可留空", func() {
			gui.TextInput(c, &s.routeFormRemark).FillWidth()
		})

		gui.Row(c).Gap(spL).AlignItems(gui.Center).Children(func() {
			label := "优先级（主备）"
			if s.routeFormStrategy == store.StrategyWeighted {
				label = "加权"
			}
			gui.Text(c, "策略").FontSize(11).TextColor(t.TextMuted)
			gui.Text(c, label).FontSize(11).Bold()
			enabled := s.routeFormEnabled
			gui.Toggle(c, &enabled, "启用该路由模型")
			s.routeFormEnabled = enabled
		})

		gui.Row(c).FillWidth().Gap(spS).AlignItems(gui.Center).Children(func() {
			gui.Text(c, "成员模型").FontSize(11).Bold()
			hintIcon(c, "成员模型", "可跨供应商选择；优先级数字越大越先尝试，失败自动切到下一个可用成员")
			pill(c, fmt.Sprintf("已选 %d", s.routeMemberCount()), t.Accent, t.Accent.Alpha(0.1))
			gui.Spacer(c)
			if gui.Button(c, "全选").FontSize(10).Padding(2, 8).Clicked() {
				for _, draft := range s.routeFormMembers {
					draft.Selected = true
				}
			}
			if gui.Button(c, "清空").FontSize(10).Padding(2, 8).Clicked() {
				for _, draft := range s.routeFormMembers {
					draft.Selected = false
				}
			}
		})

		if len(s.models) == 0 {
			emptyState(c, "还没有模型", "先到「模型」页添加模型，再回来创建路由模型")
		} else {
			// Keep the modal compact for a short catalogue and scroll only when
			// the list would otherwise make it taller than the window allows.
			listHeight := float32(len(s.models))*32 + 4
			if listHeight > 300 {
				listHeight = 300
			}
			gui.Scroll(c).Height(listHeight).FillWidth().Children(func() {
				gui.Column(c).FillWidth().Gap(2).Children(func() {
					for _, m := range s.models {
						draft := s.routeFormMembers[m.ID]
						if draft == nil {
							draft = &routeMemberDraft{Priority: "0", Weight: 1, Enabled: true}
							s.routeFormMembers[m.ID] = draft
						}
						s.routeMemberRowForm(c, m, draft)
					}
				})
			})
		}

		gui.Row(c).FillWidth().Gap(spS).Justify(gui.End).Children(func() {
			if gui.Button(c, "取消").Clicked() {
				s.routeFormOpen = false
				s.routeFormError = ""
			}
			save := gui.PrimaryButton(c, "保存")
			if len(s.models) == 0 || s.routeFormSaving {
				save.Disabled(true)
			}
			if save.Clicked() {
				s.saveRouteForm()
			}
		})
	})
}

// routeMemberRowForm is one selectable model row: the checkbox, which provider
// it belongs to, and its priority when selected.
func (s *uiState) routeMemberRowForm(c *gui.Context, m admin.ModelView, draft *routeMemberDraft) {
	t := c.Theme()
	gui.Row(c).FillWidth().Gap(spS).AlignItems(gui.Center).Children(func() {
		// The prefix keeps the checkbox label distinct from the card's member
		// text behind the modal; the page and the dialog can show the same id.
		gui.Checkbox(c, &draft.Selected, "选择 "+m.PublicName)
		gui.Text(c, m.ProviderName).FontSize(10).TextColor(t.TextMuted).Width(96).SingleLine()
		gui.Spacer(c)
		if draft.Selected {
			gui.Text(c, "优先级").FontSize(10).TextColor(t.TextMuted)
			gui.TextInput(c, &draft.Priority).Width(64).Placeholder("0")
		}
	})
}
