package ui

import (
	"fmt"
	gui "github.com/egoist/mygo/ui"
	"github.com/learningdog1/ada_prune/internal/store"
	"time"
)

// usagePage aggregates consumption. It leads with token numbers because that is
// what the gateway meters, then shows the daily token trend before the detail
// table, whose grouping the user picks.
func (s *uiState) usagePage(c *gui.Context) {
	t := c.Theme()
	gui.Scroll(c).Grow(1).FillWidth().Children(func() {
		gui.Column(c).FillWidth().Padding(32, spL, spL, 32).Gap(spM).Children(func() {
			s.errorLine(c)

			// Two selectors share the row: the table's dimension on the left, the
			// time window on the right. The window also bounds the summary and the
			// trend, so changing either re-fetches.
			groups := []string{"day", "model", "provider", "clientKey"}
			index := indexOf(groups, s.usageGroup)
			ranges := []string{"7d", "30d", "all"}
			rangeIndex := indexOf(ranges, s.usageRange)
			gui.Row(c).FillWidth().AlignItems(gui.Center).Children(func() {
				if gui.Tabs(c, &index, "按天", "按模型", "按供应商", "按密钥").Changed() {
					s.usageGroup = groups[index]
					s.reload()
				}
				gui.Spacer(c)
				gui.Text(c, "区间").FontSize(11).TextColor(t.TextMuted)
				if gui.Tabs(c, &rangeIndex, "近 7 天", "近 30 天", "全部").Changed() {
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
			gui.Row(c).Gap(spM).Children(func() {
				statTile(c, "请求", fmtInt(summary.Requests), fmt.Sprintf("平均 %s token", fmtInt(average)), t.Accent)
				statTile(c, "输入 token", fmtTokens(summary.InputTokens), fmtInt(summary.InputTokens), t.Accent)
				statTile(c, "输出 token", fmtTokens(summary.OutputTokens), fmtInt(summary.OutputTokens), t.Warning)
				statTile(c, "缓存 token", fmtTokens(summary.CachedTokens), cacheHint, t.Success)
				statTile(c, "合计 token", fmtTokens(total), "输入 + 输出", t.Text)
				statTile(c, "费用", fmtMoney(summary.Cost), "按模型单价", t.Warning)
			})

			panel(c, "Token 趋势", func() {
				gui.Row(c).FillWidth().AlignItems(gui.Center).Children(func() {
					gui.Text(c, usageRangeLabel(s.usageRange)+" · 每日输入与输出堆叠").FontSize(10).TextColor(t.TextMuted)
					gui.Spacer(c)
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
				gui.Text(c, title).FontSize(13).Bold()
				gui.Divider(c)
				gui.Table(c, &s.usageTable, []gui.TableColumn{
					{Title: label, Width: 260},
					{Title: "请求", Width: 80, Align: gui.End},
					{Title: "输入", Width: 100, Align: gui.End},
					{Title: "输出", Width: 100, Align: gui.End},
					{Title: "缓存", Width: 100, Align: gui.End},
					{Title: "合计", Width: 100, Align: gui.End},
					{Title: "费用", Align: gui.End},
				}, len(rows), func(row, col int) {
					if row < 0 || row >= len(rows) {
						return
					}
					entry := rows[row]
					switch col {
					case 0:
						gui.Text(c, usageLabel(s.usageGroup, entry)).FontSize(11).SingleLine()
					case 1:
						gui.Text(c, fmtInt(entry.Requests)).FontSize(11)
					case 2:
						gui.Text(c, fmtTokens(entry.InputTokens)).FontSize(11).TextColor(t.TextMuted)
					case 3:
						gui.Text(c, fmtTokens(entry.OutputTokens)).FontSize(11).TextColor(t.TextMuted)
					case 4:
						gui.Text(c, fmtTokens(entry.CachedTokens)).FontSize(11).TextColor(t.TextMuted)
					case 5:
						gui.Text(c, fmtTokens(entry.InputTokens+entry.OutputTokens)).FontSize(11)
					case 6:
						gui.Text(c, fmtMoney(entry.Cost)).FontSize(11).TextColor(t.TextMuted)
					}
				})
			})
		})
	})
}

// tokenChart stacks input under output per day. Days without usage keep a
// baseline tick so a quiet day reads as zero rather than missing data, and a
// hover reads a day out without crosshair maths.
func (s *uiState) tokenChart(c *gui.Context) {
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

	gui.Row(c).FillWidth().Height(108).Gap(3).AlignItems(gui.Stretch).Children(func() {
		for _, day := range days {
			day := day
			r := byDay[day]
			input, output := r.InputTokens, r.OutputTokens
			v := input + output
			col := gui.Column(c).Grow(1).Justify(gui.End)
			col.Children(func() {
				if v == 0 {
					gui.Box(c).FillWidth().Height(3).Radius(2).Background(t.Border)
					return
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
				if output > 0 {
					gui.Box(c).FillWidth().Height(outputHeight).Radius(2).Background(t.Warning.Alpha(0.7))
				}
				if input > 0 {
					gui.Box(c).FillWidth().Height(inputHeight).Radius(2).Background(t.Accent.Alpha(0.7))
				}
			})
			col.Tooltip(fmt.Sprintf("%s · 输入 %s · 输出 %s", day, fmtTokens(input), fmtTokens(output)))
		}
	})

	gui.Row(c).FillWidth().AlignItems(gui.Center).Children(func() {
		gui.Text(c, "合计 "+fmtInt(total)+" token").FontSize(11).TextColor(t.TextMuted)
		gui.Spacer(c)
		gui.Text(c, "峰值 "+fmtInt(peak)).FontSize(11).TextColor(t.TextMuted)
	})
	gui.Row(c).FillWidth().AlignItems(gui.Center).Children(func() {
		gui.Text(c, days[0]).FontSize(10).TextColor(t.TextMuted)
		gui.Spacer(c)
		if len(days) > 1 {
			gui.Text(c, days[len(days)-1]).FontSize(10).TextColor(t.TextMuted)
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
