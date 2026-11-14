package ui

import (
	"fmt"
	gui "github.com/egoist/mygo/ui"
	"github.com/learningdog1/ada_prune/internal/admin"
	"time"
)

// dashboardPage is the landing page: headline numbers, the last day's traffic and
// the health of every provider.
func (s *uiState) dashboardPage(c *gui.Context) {
	t := c.Theme()
	gui.Scroll(c).Grow(1).FillWidth().Children(func() {
		gui.Column(c).FillWidth().Padding(32, spL, spL, 32).Gap(spL).Children(func() {
			s.errorLine(c)

			// First run: the three steps with their actions beat a lone empty
			// state, because the next move is a click instead of a direction.
			if len(s.providers) == 0 {
				s.onboardingCard(c)
			}

			kpiStrip(c, []kpiCell{
				{Label: "今日请求", Value: fmtInt(s.overview.Today.Requests), Hint: "所有端点"},
				{Label: "成功率", Value: percent(s.overview.Today.SuccessRate), Hint: "今日"},
				{Label: "输入 token", Value: fmtTokens(s.overview.Today.InputTokens), Hint: "含缓存"},
				{Label: "输出 token", Value: fmtTokens(s.overview.Today.OutputTokens), Hint: "生成"},
				{Label: "费用 · USD", Value: fmtMoney(s.overview.Today.Cost), Hint: "按模型单价"},
			})

			panel(c, "最近 24 小时", func() {
				s.chart(c)
			})

			panel(c, "供应商健康度", func() {
				if len(s.providers) == 0 {
					emptyState(c, "还没有供应商", "按上面的三步接入，或到「供应商」页新建一个")
					return
				}
				gui.Row(c).FillWidth().Padding(spXS, 0).Gap(spS).Children(func() {
					gui.Text(c, "供应商").FontSize(10).TextColor(t.TextMuted).Width(200)
					gui.Text(c, "协议").FontSize(10).TextColor(t.TextMuted).Width(170)
					gui.Text(c, "凭据").FontSize(10).TextColor(t.TextMuted).Width(90)
					gui.Text(c, "模型").FontSize(10).TextColor(t.TextMuted).Width(90)
					gui.Text(c, "状态").FontSize(10).TextColor(t.TextMuted)
				})
				for i, p := range s.providers {
					s.providerHealthRow(c, i, p)
				}
			})

			panel(c, "服务信息", func() {
				grid := [][2]string{
					{"监听地址", s.endpoint()},
					{"数据目录", s.overview.DataDir},
					{"供应商", fmt.Sprintf("%d（启用 %d）", s.overview.Providers.Total, s.overview.Providers.Enabled)},
					{"模型", fmt.Sprintf("%d（启用 %d）", s.overview.Models.Total, s.overview.Models.Enabled)},
					{"凭据", fmt.Sprintf("%d（可用 %d，耗尽 %d）",
						s.overview.Credentials.Total, s.overview.Credentials.Active, s.overview.Credentials.Exhausted)},
					{"客户端密钥", fmt.Sprintf("%d（启用 %d）", s.overview.ClientKeys.Total, s.overview.ClientKeys.Enabled)},
					{"存储", storageLabel(s.overview.Degraded)},
					{"版本", s.overview.Version},
				}
				gui.Column(c).Gap(spXS).Children(func() {
					for _, pair := range grid {
						gui.Row(c).Gap(spS).Children(func() {
							gui.Text(c, pair[0]).FontSize(11).TextColor(t.TextMuted).Width(110)
							gui.Text(c, pair[1]).FontSize(11).SingleLine()
						})
					}
				})
			})
		})
	})
}

// providerHealthRow is one line of the health table. The whole row is one
// click: it opens the provider's page with that row selected, the detail the
// health summary points at.
func (s *uiState) providerHealthRow(c *gui.Context, index int, p admin.ProviderView) gui.Element {
	t := c.Theme()
	row := gui.Row(c).FillWidth().Padding(spXS, 0).Gap(spS).AlignItems(gui.Center).
		Cursor(gui.CursorPointer)
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		gui.Text(c, p.Name).FontSize(11).Width(200).SingleLine()
		gui.Text(c, s.providerProtocolLabel(p)).FontSize(11).TextColor(t.TextMuted).Width(170).SingleLine()
		gui.Text(c, fmt.Sprintf("%d/%d", p.ActiveCredentialCount, p.CredentialCount)).
			FontSize(11).TextColor(t.TextMuted).Width(90)
		gui.Text(c, fmt.Sprintf("%d/%d", p.EnabledModelCount, p.ModelCount)).
			FontSize(11).TextColor(t.TextMuted).Width(90)
		routablePill(c, p.Routable, p.RoutableReason, false)
	})
	if row.Clicked() {
		s.navigate("/providers")
		s.providerSel = index
	}
	return row
}

// onboardingCard is the first-run guide: the three steps, each with its next
// action one click away, and the address a client should point at.
func (s *uiState) onboardingCard(c *gui.Context) {
	t := c.Theme()
	card(c, func() {
		gui.Text(c, "三步接入").FontSize(13).Bold()
		gui.Divider(c)
		steps := []string{
			"1. 新建供应商：选类型 → 填基础地址 → 加上游密钥",
			"2. 拉取或新建模型，确认它可路由",
			"3. 创建客户端密钥，把下游工具的 Base URL 指向 " + s.endpoint(),
		}
		gui.Column(c).Gap(spXS).Children(func() {
			for _, step := range steps {
				gui.Text(c, step).FontSize(11).TextColor(t.TextMuted)
			}
		})
		gui.Row(c).Gap(spS).Children(func() {
			if primaryIconButton(c, iconPlus(), "新建供应商").Clicked() {
				s.openNewProvider()
			}
			if iconButton(c, iconKeys(), "创建客户端密钥").Clicked() {
				s.navigate("/keys")
			}
			if iconButton(c, iconCopy(), "复制服务地址").Clicked() {
				c.WriteClipboard(s.endpoint())
				s.flash("已复制到剪贴板", false)
			}
		})
	})
}

// chart draws the rolling 24 hour request bars. Errors stack at the top of
// each bar in the danger tone, and every bar reads itself out on hover, so a
// spike can be traced to an hour without opening the log.
func (s *uiState) chart(c *gui.Context) {
	t := c.Theme()
	points := s.overview.Last24h.Points
	if len(points) == 0 {
		emptyState(c, "还没有请求", "数据面收到第一笔调用后这里就会有柱状图")
		return
	}
	peak := s.overview.Last24h.Peak
	if peak <= 0 {
		peak = 1
	}

	gui.Row(c).Gap(spM).AlignItems(gui.Center).Children(func() {
		legendDot(c, t.Accent, "请求")
		legendDot(c, t.Danger, "错误")
	})

	// Each bucket is a column that pushes its bar to the bottom, so a bar's
	// baseline stays put no matter how short the row of bars is.
	gui.Row(c).FillWidth().Height(104).Gap(3).AlignItems(gui.Stretch).Children(func() {
		for _, point := range points {
			point := point
			label := fmt.Sprintf("%s · 请求 %d · 错误 %d · $%s",
				time.UnixMilli(point.TS).Format("15:04"),
				point.Requests, point.Errors, fmtMoney(point.Cost))
			col := gui.Column(c).Grow(1).Justify(gui.End)
			col.Children(func() {
				if point.Requests == 0 {
					// An empty hour still gets a baseline tick, or the chart looks
					// like it is missing data instead of showing a quiet hour.
					gui.Box(c).FillWidth().Height(3).Radius(2).Background(t.Border)
					return
				}
				height := float32(point.Requests) / float32(peak) * 96
				if point.Errors > 0 {
					// The error segment rides on top of the request volume.
					errHeight := height * float32(point.Errors) / float32(point.Requests)
					baseHeight := height - errHeight
					gui.Box(c).FillWidth().Height(errHeight).Radius(2).Background(t.Danger)
					if baseHeight > 0 {
						gui.Box(c).FillWidth().Height(baseHeight).Radius(2).
							Background(t.Accent.Alpha(0.55))
					}
					return
				}
				bar := gui.Box(c).FillWidth().Radius(3)
				if float32(point.Requests)/float32(peak) > 0.9 {
					bar.Height(height).Background(t.Accent)
				} else {
					bar.Height(height).Background(t.Accent.Alpha(0.55))
				}
			})
			col.Tooltip(label)
		}
	})

	gui.Row(c).Gap(spL).Children(func() {
		gui.Text(c, "合计 "+fmtInt(s.overview.Last24h.Total)).FontSize(11).TextColor(t.TextMuted)
		gui.Text(c, "峰值 "+fmtInt(s.overview.Last24h.Peak)).FontSize(11).TextColor(t.TextMuted)
		if len(points) > 0 {
			first := time.UnixMilli(points[0].TS).Format("15:04")
			last := time.UnixMilli(points[len(points)-1].TS).Format("15:04")
			gui.Text(c, first+" → "+last).FontSize(11).TextColor(t.TextMuted)
		}
	})
}
