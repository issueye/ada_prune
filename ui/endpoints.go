package ui

import (
	gui "github.com/egoist/mygo/ui"
)

// endpointsPage lists data-plane entry points.
func (s *uiState) endpointsPage(c *gui.Context) {
	t := c.Theme()
	gui.Scroll(c).Grow(1).FillWidth().Children(func() {
		gui.Column(c).FillWidth().Padding(32, spL, spL, 32).Gap(spM).Children(func() {
			s.errorLine(c)
			card(c, func() {
				gui.Text(c, "数据面入口").FontSize(13).Bold()
				gui.Divider(c)
				rows := s.endpoints
				if len(rows) == 0 {
					emptyState(c, "没有端点", "内置的三条端点始终可用")
					return
				}
				gui.Column(c).Gap(spS).Children(func() {
					for _, e := range rows {
						gui.Row(c).FillWidth().Gap(spS).AlignItems(gui.Center).Children(func() {
							gui.Text(c, e.Path).FontSize(11).Width(240).SingleLine()
							gui.Text(c, e.InboundProtocol).FontSize(11).TextColor(t.TextMuted).Width(140)
							if e.Builtin {
								pill(c, "内置", t.Accent, t.Accent.Alpha(0.12))
							} else {
								pill(c, "自定义", t.TextMuted, t.TextMuted.Alpha(0.1))
							}
							statusPill(c, e.Enabled, "启用", "停用", false)
							gui.Spacer(c)
							if e.AuthRequired {
								pill(c, "需鉴权", t.TextMuted, t.TextMuted.Alpha(0.1))
							}
						})
					}
				})
			})
			gui.Text(c, "自定义端点当前通过管理 API 创建（见 docs/05 契约）。").FontSize(10).TextColor(t.TextMuted)
		})
	})
}

// infoRow is a label/value line in a detail panel. Long values truncate, so
// the full text rides along as a tooltip.
func (s *uiState) infoRow(c *gui.Context, label, value string) {
	t := c.Theme()
	gui.Row(c).Gap(spS).Children(func() {
		gui.Text(c, label).FontSize(11).TextColor(t.TextMuted).Width(96)
		gui.Text(c, value).FontSize(11).SingleLine().Tooltip(value)
	})
}
