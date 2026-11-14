package ui

import (
	"fmt"
	gui "github.com/egoist/mygo/ui"
	"github.com/learningdog1/ada_prune/internal/admin"
)

// providersPage lists upstream services.
func (s *uiState) providersPage(c *gui.Context) {
	t := c.Theme()
	gui.Scroll(c).Grow(1).FillWidth().Children(func() {
		gui.Column(c).FillWidth().Padding(32, spL, spL, 32).Gap(spM).Children(func() {
			s.errorLine(c)

			gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
				pill(c, fmt.Sprintf("%d 个", len(s.providers)), t.TextMuted, t.TextMuted.Alpha(0.1))
				gui.Spacer(c)
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
				gui.Table(c, &s.providerTable, []gui.TableColumn{
					{Title: "名称", Width: 220},
					{Title: "类型", Width: 80},
					{Title: "协议", Width: 190},
					{Title: "凭据", Width: 90, Align: gui.End},
					{Title: "模型", Width: 90, Align: gui.End},
					{Title: "状态"},
					{Title: "操作", Width: 84, Fixed: true, Align: gui.Center},
				}, len(rows), func(row, col int) {
					if row < 0 || row >= len(rows) {
						return
					}
					p := rows[row]
					selected := row == s.providerSel
					primary, secondary := rowColors(c, selected)
					switch col {
					case 0:
						gui.Text(c, p.Name).FontSize(11).TextColor(primary).SingleLine()
					case 1:
						accessModePill(c, p.AccessMode, selected)
					case 2:
						gui.Text(c, s.providerProtocolLabel(p)).FontSize(11).TextColor(secondary).SingleLine()
					case 3:
						gui.Text(c, fmt.Sprintf("%d/%d", p.ActiveCredentialCount, p.CredentialCount)).
							FontSize(11).TextColor(secondary)
					case 4:
						gui.Text(c, fmt.Sprintf("%d/%d", p.EnabledModelCount, p.ModelCount)).
							FontSize(11).TextColor(secondary)
					case 5:
						routablePill(c, p.Routable, p.RoutableReason, selected)
					case 6:
						s.providerRowActions(c, row, p)
					}
				})
			})
			gui.Text(c, "选中一行查看适配器能力与详情。").FontSize(10).TextColor(t.TextMuted)

			if s.providerSel >= 0 && s.providerSel < len(rows) {
				s.providerDetail(c, rows[s.providerSel])
			}
		})
	})
}

// providerRowActions is the dropdown of one provider row.
func (s *uiState) providerRowActions(c *gui.Context, index int, p admin.ProviderView) {
	rowActions(c, func(m *gui.Menu) {
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
func (s *uiState) providerDetail(c *gui.Context, p admin.ProviderView) {
	t := c.Theme()
	panel(c, "供应商 · "+p.Name, func() {
		gui.Row(c).Gap(spS).Children(func() {
			statusPill(c, p.Enabled, "已启用", "已停用", false)
			accessModePill(c, p.AccessMode, false)
		})

		if p.AdapterCapabilities == nil {
			emptyState(c, "该供应商没有可用的适配器", "检查 providers.kind 是否已注册")
			return
		}
		caps := *p.AdapterCapabilities
		gui.Row(c).Gap(spS).Wrap().Children(func() {
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

		gui.Column(c).Gap(spXS).Children(func() {
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
