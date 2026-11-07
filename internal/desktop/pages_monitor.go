package desktop

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/learningdog1/ada_proxy/internal/admin"
	"github.com/learningdog1/ada_proxy/internal/store"
)

// dashboardPage is the landing page: headline numbers, the last day's traffic and
// the health of every provider.
func (s *uiState) dashboardPage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(spXL, spL, spL, spXL).Gap(spL).Children(func() {
			s.errorLine(c)

			ui.Row(c).Gap(spM).Children(func() {
				statTile(c, "今日请求", fmtInt(s.overview.Today.Requests), "所有端点", t.Accent)
				statTile(c, "成功率", percent(s.overview.Today.SuccessRate), "今日", t.Success)
				statTile(c, "输入 token", fmtTokens(s.overview.Today.InputTokens), "含缓存", t.Accent)
				statTile(c, "输出 token", fmtTokens(s.overview.Today.OutputTokens), "生成", t.Warning)
				statTile(c, "费用", fmtMoney(s.overview.Today.Cost), "按模型单价", t.Warning)
				statTile(c, "在途", fmtInt(s.overview.Inflight), "当前请求", t.TextMuted)
			})

			panel(c, "最近 24 小时", func() {
				s.chart(c)
			})

			panel(c, "供应商健康度", func() {
				if len(s.providers) == 0 {
					emptyState(c, "还没有供应商", "到「供应商」页新建一个，或通过 HTTP API 创建")
					return
				}
				ui.Row(c).FillWidth().Padding(spXS, 0).Gap(spS).Children(func() {
					ui.Text(c, "供应商").FontSize(10).TextColor(t.TextMuted).Width(200)
					ui.Text(c, "协议").FontSize(10).TextColor(t.TextMuted).Width(170)
					ui.Text(c, "凭据").FontSize(10).TextColor(t.TextMuted).Width(90)
					ui.Text(c, "模型").FontSize(10).TextColor(t.TextMuted).Width(90)
					ui.Text(c, "状态").FontSize(10).TextColor(t.TextMuted)
				})
				for _, p := range s.providers {
					s.providerHealthRow(c, p)
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
				ui.Column(c).Gap(spXS).Children(func() {
					for _, pair := range grid {
						ui.Row(c).Gap(spS).Children(func() {
							ui.Text(c, pair[0]).FontSize(11).TextColor(t.TextMuted).Width(110)
							ui.Text(c, pair[1]).FontSize(11).SingleLine()
						})
					}
				})
			})
		})
	})
}

// providerHealthRow is one line of the health table.
func (s *uiState) providerHealthRow(c *ui.Context, p admin.ProviderView) ui.Element {
	t := c.Theme()
	return ui.Row(c).FillWidth().Padding(spXS, 0).Gap(spS).AlignItems(ui.Center).Children(func() {
		ui.Text(c, p.Name).FontSize(11).Width(200).SingleLine()
		ui.Text(c, s.providerProtocolLabel(p)).FontSize(11).TextColor(t.TextMuted).Width(170).SingleLine()
		ui.Text(c, fmt.Sprintf("%d/%d", p.ActiveCredentialCount, p.CredentialCount)).
			FontSize(11).TextColor(t.TextMuted).Width(90)
		ui.Text(c, fmt.Sprintf("%d/%d", p.EnabledModelCount, p.ModelCount)).
			FontSize(11).TextColor(t.TextMuted).Width(90)
		routablePill(c, p.Routable, p.RoutableReason, false)
	})
}

// chart draws the rolling 24 hour request bars.
func (s *uiState) chart(c *ui.Context) {
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

	// Each bucket is a column that pushes its bar to the bottom, so a bar's
	// baseline stays put no matter how short the row of bars is.
	ui.Row(c).FillWidth().Height(104).Gap(3).AlignItems(ui.Stretch).Children(func() {
		for _, point := range points {
			point := point
			ui.Column(c).Grow(1).Justify(ui.End).Children(func() {
				height := float32(point.Requests) / float32(peak) * 96
				bar := ui.Box(c).FillWidth().Radius(3)
				switch {
				case point.Requests == 0:
					// An empty hour still gets a baseline tick, or the chart looks
					// like it is missing data instead of showing a quiet hour.
					bar.Height(3).Background(t.Border)
				case float32(point.Requests)/float32(peak) > 0.9:
					bar.Height(height).Background(t.Accent)
				default:
					bar.Height(height).Background(t.Accent.Alpha(0.55))
				}
			})
		}
	})

	ui.Row(c).Gap(spL).Children(func() {
		ui.Text(c, "合计 "+fmtInt(s.overview.Last24h.Total)).FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, "峰值 "+fmtInt(s.overview.Last24h.Peak)).FontSize(11).TextColor(t.TextMuted)
		if len(points) > 0 {
			first := time.UnixMilli(points[0].TS).Format("15:04")
			last := time.UnixMilli(points[len(points)-1].TS).Format("15:04")
			ui.Text(c, first+" → "+last).FontSize(11).TextColor(t.TextMuted)
		}
	})
}

// logsPage lists recent calls with filters.
func (s *uiState) logsPage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(spXL, spL, spL, spXL).Gap(spM).Children(func() {
			s.errorLine(c)

			ui.Row(c).Gap(spS).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "端点").FontSize(11).TextColor(t.TextMuted)
				endpoints := []string{"全部", "/v1/chat/completions", "/v1/responses", "/v1/messages"}
				selected := s.logEndpoint
				if ui.Select(c, &selected, endpoints).Changed() || selected != s.logEndpoint {
					s.logEndpoint = selected
					s.reload()
				}
				ui.Text(c, "状态").FontSize(11).TextColor(t.TextMuted)
				statuses := []string{"全部", "2xx", "4xx", "5xx"}
				status := s.logStatus
				if ui.Select(c, &status, statuses).Changed() || status != s.logStatus {
					s.logStatus = status
					s.reload()
				}
				ui.Spacer(c)
				pill(c, fmt.Sprintf("%d 条", s.logs.Total), t.TextMuted, t.TextMuted.Alpha(0.1))
			})

			rows := s.logs.Rows
			if len(rows) == 0 {
				card(c, func() {
					emptyState(c, "还没有请求", "把下游工具的 Base URL 指向本服务即可")
				})
				return
			}

			s.logTable.Selected = &s.logSel
			s.logTable.Key = func(i int) any {
				if i >= 0 && i < len(rows) {
					return rows[i].ID
				}
				return i
			}
			card(c, func() {
				ui.Table(c, &s.logTable, []ui.TableColumn{
					{Title: "时间", Width: 82},
					{Title: "端点", Width: 168},
					{Title: "模型", Width: 180},
					{Title: "状态", Width: 60, Align: ui.End},
					{Title: "流", Width: 40, Align: ui.End},
					{Title: "耗时", Width: 70, Align: ui.End},
					{Title: "TTFT", Width: 70, Align: ui.End},
					{Title: "输入", Width: 70, Align: ui.End},
					{Title: "输出", Width: 70, Align: ui.End},
					{Title: "费用", Align: ui.End},
				}, len(rows), func(row, col int) {
					if row < 0 || row >= len(rows) {
						return
					}
					entry := rows[row]
					selected := row == s.logSel
					primary, secondary := rowColors(c, selected)
					statusTone := statusColor(c, entry.StatusCode)
					if selected {
						statusTone = primary
					}
					switch col {
					case 0:
						ui.Text(c, time.UnixMilli(entry.TS).Format("15:04:05")).FontSize(11).TextColor(primary)
					case 1:
						ui.Text(c, entry.Endpoint).FontSize(11).TextColor(primary).SingleLine()
					case 2:
						ui.Text(c, entry.Model).FontSize(11).TextColor(primary).SingleLine()
					case 3:
						ui.Text(c, fmt.Sprintf("%d", entry.StatusCode)).FontSize(11).
							TextColor(statusTone)
					case 4:
						stream := ""
						if entry.Stream {
							stream = "SSE"
						}
						ui.Text(c, stream).FontSize(11).TextColor(secondary)
					case 5:
						ui.Text(c, fmtDuration(entry.DurationMs)).FontSize(11).TextColor(secondary)
					case 6:
						ui.Text(c, fmtDuration(entry.TTFTMs)).FontSize(11).TextColor(secondary)
					case 7:
						ui.Text(c, fmtTokens(entry.InputTokens)).FontSize(11).TextColor(secondary)
					case 8:
						ui.Text(c, fmtTokens(entry.OutputTokens)).FontSize(11).TextColor(secondary)
					case 9:
						ui.Text(c, fmtMoney(entry.Cost)).FontSize(11).TextColor(secondary)
					}
				})
			})
		})
	})
}

// usagePage aggregates consumption. It leads with token numbers because that is
// what the gateway meters, then shows the daily token trend before the detail
// table, whose grouping the user picks.
func (s *uiState) usagePage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(spXL, spL, spL, spXL).Gap(spM).Children(func() {
			s.errorLine(c)

			// Two selectors share the row: the table's dimension on the left, the
			// time window on the right. The window also bounds the summary and the
			// trend, so changing either re-fetches.
			groups := []string{"day", "model", "provider", "clientKey"}
			index := indexOf(groups, s.usageGroup)
			ranges := []string{"7d", "30d", "all"}
			rangeIndex := indexOf(ranges, s.usageRange)
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				if ui.Tabs(c, &index, "按天", "按模型", "按供应商", "按密钥").Changed() {
					s.usageGroup = groups[index]
					s.reload()
				}
				ui.Spacer(c)
				ui.Text(c, "区间").FontSize(11).TextColor(t.TextMuted)
				if ui.Tabs(c, &rangeIndex, "近 7 天", "近 30 天", "全部").Changed() {
					s.usageRange = ranges[rangeIndex]
					s.reload()
				}
			})

			summary := s.usage.Summary
			total := summary.InputTokens + summary.OutputTokens
			var average int64
			if summary.Requests > 0 {
				average = total / summary.Requests
			}
			cacheHint := "占输入 -"
			if summary.InputTokens > 0 {
				cacheHint = fmt.Sprintf("占输入 %.0f%%", float64(summary.CachedTokens)/float64(summary.InputTokens)*100)
			}
			ui.Row(c).Gap(spM).Children(func() {
				statTile(c, "请求", fmtInt(summary.Requests), fmt.Sprintf("平均 %s token", fmtInt(average)), t.Accent)
				statTile(c, "输入 token", fmtTokens(summary.InputTokens), fmtInt(summary.InputTokens), t.Accent)
				statTile(c, "输出 token", fmtTokens(summary.OutputTokens), fmtInt(summary.OutputTokens), t.Warning)
				statTile(c, "缓存 token", fmtTokens(summary.CachedTokens), cacheHint, t.Success)
				statTile(c, "合计 token", fmtTokens(total), "输入 + 输出", t.Text)
				statTile(c, "费用", fmtMoney(summary.Cost), "按模型单价", t.Warning)
			})

			panel(c, "Token 趋势", func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, usageRangeLabel(s.usageRange)+" · 每日输入与输出堆叠").FontSize(10).TextColor(t.TextMuted)
					ui.Spacer(c)
					legendDot(c, t.Accent, "输入")
					legendDot(c, t.Warning, "输出")
				})
				s.tokenChart(c)
			})

			rows := s.usage.Rows
			if len(rows) == 0 {
				card(c, func() {
					emptyState(c, "这个区间没有用量", "换一个区间或分组维度，或先发起调用")
				})
				return
			}

			card(c, func() {
				title := map[string]string{
					"day": "按天", "model": "按模型", "provider": "按供应商", "clientKey": "按密钥",
				}[s.usageGroup]
				label := map[string]string{
					"day": "日期", "model": "模型", "provider": "供应商", "clientKey": "密钥",
				}[s.usageGroup]
				ui.Text(c, title).FontSize(13).Bold()
				ui.Divider(c)
				ui.Table(c, &s.usageTable, []ui.TableColumn{
					{Title: label, Width: 260},
					{Title: "请求", Width: 80, Align: ui.End},
					{Title: "输入", Width: 100, Align: ui.End},
					{Title: "输出", Width: 100, Align: ui.End},
					{Title: "缓存", Width: 100, Align: ui.End},
					{Title: "合计", Width: 100, Align: ui.End},
					{Title: "费用", Align: ui.End},
				}, len(rows), func(row, col int) {
					if row < 0 || row >= len(rows) {
						return
					}
					entry := rows[row]
					switch col {
					case 0:
						ui.Text(c, usageLabel(s.usageGroup, entry)).FontSize(11).SingleLine()
					case 1:
						ui.Text(c, fmtInt(entry.Requests)).FontSize(11)
					case 2:
						ui.Text(c, fmtTokens(entry.InputTokens)).FontSize(11).TextColor(t.TextMuted)
					case 3:
						ui.Text(c, fmtTokens(entry.OutputTokens)).FontSize(11).TextColor(t.TextMuted)
					case 4:
						ui.Text(c, fmtTokens(entry.CachedTokens)).FontSize(11).TextColor(t.TextMuted)
					case 5:
						ui.Text(c, fmtTokens(entry.InputTokens+entry.OutputTokens)).FontSize(11)
					case 6:
						ui.Text(c, fmtMoney(entry.Cost)).FontSize(11).TextColor(t.TextMuted)
					}
				})
			})
		})
	})
}

// tokenChart stacks input under output per day. Days without usage keep a
// baseline tick so a quiet day reads as zero rather than missing data.
func (s *uiState) tokenChart(c *ui.Context) {
	t := c.Theme()
	rows := s.usageDaily.Rows
	if len(rows) == 0 {
		emptyState(c, "这个区间没有 token 用量", "发起调用后这里会按天显示输入与输出 token")
		return
	}

	byDay := make(map[string]store.UsageRow, len(rows))
	for _, r := range rows {
		byDay[r.Day] = r
	}

	// The window fixes the x axis: 7 and 30 day views show every day including
	// the empty ones. All time charts only the most recent 60 days with data.
	days := make([]string, 0, 30)
	if n := s.usageRangeDays(); n > 0 {
		start := time.Now().UTC().AddDate(0, 0, -(n - 1))
		for i := 0; i < n; i++ {
			days = append(days, start.AddDate(0, 0, i).Format("2006-01-02"))
		}
	} else {
		for _, r := range rows {
			days = append(days, r.Day)
		}
		if len(days) > 60 {
			days = days[len(days)-60:]
		}
	}

	var peak, total int64
	for _, day := range days {
		r := byDay[day]
		v := r.InputTokens + r.OutputTokens
		total += v
		if v > peak {
			peak = v
		}
	}
	if peak <= 0 {
		emptyState(c, "这个区间没有 token 用量", "换一个区间，或先发起调用")
		return
	}

	ui.Row(c).FillWidth().Height(108).Gap(3).AlignItems(ui.Stretch).Children(func() {
		for _, day := range days {
			day := day
			r := byDay[day]
			input, output := r.InputTokens, r.OutputTokens
			v := input + output
			if v == 0 {
				ui.Column(c).Grow(1).Justify(ui.End).Children(func() {
					ui.Box(c).FillWidth().Height(3).Radius(2).Background(t.Border)
				})
				continue
			}
			height := float32(v) / float32(peak) * 100
			inputHeight := height * float32(input) / float32(v)
			outputHeight := height - inputHeight
			// Both series keep a visible sliver even when one of them is tiny.
			if input > 0 && inputHeight < 2 {
				inputHeight = 2
			}
			if output > 0 && outputHeight < 2 {
				outputHeight = 2
			}
			ui.Column(c).Grow(1).Justify(ui.End).Children(func() {
				if output > 0 {
					ui.Box(c).FillWidth().Height(outputHeight).Radius(2).Background(t.Warning.Alpha(0.7))
				}
				if input > 0 {
					ui.Box(c).FillWidth().Height(inputHeight).Radius(2).Background(t.Accent.Alpha(0.7))
				}
			})
		}
	})

	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Text(c, "合计 "+fmtInt(total)+" token").FontSize(11).TextColor(t.TextMuted)
		ui.Spacer(c)
		ui.Text(c, "峰值 "+fmtInt(peak)).FontSize(11).TextColor(t.TextMuted)
	})
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Text(c, days[0]).FontSize(10).TextColor(t.TextMuted)
		ui.Spacer(c)
		if len(days) > 1 {
			ui.Text(c, days[len(days)-1]).FontSize(10).TextColor(t.TextMuted)
		}
	})
}

// usageRangeLabel names the selected window for the trend header.
func usageRangeLabel(r string) string {
	switch r {
	case "7d":
		return "近 7 天"
	case "30d":
		return "近 30 天"
	default:
		return "全部时间"
	}
}

func usageLabel(group string, row store.UsageRow) string {
	switch group {
	case "model":
		return row.Model
	case "provider":
		return row.ProviderName
	case "clientKey":
		return row.KeyName
	default:
		return row.Day
	}
}

func percent(v float64) string {
	if v <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", v*100)
}

func storageLabel(degraded bool) string {
	if degraded {
		return "降级（内存，重启丢失）"
	}
	return "SQLite"
}

func indexOf(list []string, want string) int {
	for i, v := range list {
		if v == want {
			return i
		}
	}
	return 0
}
