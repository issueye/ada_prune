package ui

import (
	"fmt"
	gui "github.com/egoist/mygo/ui"
	"github.com/learningdog1/ada_prune/internal/store"
	"strconv"
	"time"
)

// logsPage lists recent calls with filters.
func (s *uiState) logsPage(c *gui.Context) {
	t := c.Theme()
	gui.Scroll(c).Grow(1).FillWidth().Children(func() {
		gui.Column(c).FillWidth().Padding(32, spL, spL, 32).Gap(spM).Children(func() {
			s.errorLine(c)

			gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
				gui.Text(c, "端点").FontSize(11).TextColor(t.TextMuted)
				endpoints := []string{"全部", "/v1/chat/completions", "/v1/responses", "/v1/messages"}
				selected := s.logEndpoint
				if gui.Select(c, &selected, endpoints).Changed() || selected != s.logEndpoint {
					s.logEndpoint = selected
					s.logPage = 1
					s.reload()
				}
				gui.Text(c, "状态").FontSize(11).TextColor(t.TextMuted)
				statuses := []string{"全部", "2xx", "4xx", "5xx"}
				status := s.logStatus
				if gui.Select(c, &status, statuses).Changed() || status != s.logStatus {
					s.logStatus = status
					s.logPage = 1
					s.reload()
				}
				gui.Spacer(c)
				// The tail follows the 5s poll while on; off means the table only
				// moves when the user refreshes, so a row stays put to read.
				live := s.liveTail
				gui.Toggle(c, &live, "实时刷新")
				s.liveTail = live
				pill(c, fmt.Sprintf("共 %d 条", s.logs.Total),
					t.TextMuted, t.TextMuted.Alpha(0.1))
			})

			rows := s.logs.Rows
			if len(rows) == 0 {
				card(c, func() {
					emptyState(c, "还没有请求", "把下游工具的 Base URL 指向本服务即可")
				})
				return
			}

			wide := false
			if w, _ := c.Size(); w >= 1410 {
				wide = true
			}
			timeW, endpointW, modelW, statusW, streamW, durW, ttftW, tokenW := float32(82), float32(168), float32(180), float32(60), float32(40), float32(70), float32(70), float32(70)
			if wide {
				timeW, endpointW, modelW, statusW, streamW, durW, ttftW, tokenW = float32(76), float32(140), float32(150), float32(52), float32(34), float32(60), float32(60), float32(60)
			}
			gui.Row(c).FillWidth().AlignItems(gui.Stretch).Gap(spM).Children(func() {
				gui.Column(c).Grow(1).Gap(spM).Children(func() {
					s.logTable.Selected = &s.logSel
					s.logTable.Key = func(i int) any {
						if i >= 0 && i < len(rows) {
							return rows[i].ID
						}
						return i
					}
					card(c, func() {
						gui.Table(c, &s.logTable, []gui.TableColumn{
							{Title: "时间", Width: timeW},
							{Title: "端点", Width: endpointW},
							{Title: "模型", Width: modelW},
							{Title: "状态", Width: statusW, Align: gui.End},
							{Title: "流", Width: streamW, Align: gui.End},
							{Title: "耗时", Width: durW, Align: gui.End},
							{Title: "TTFT", Width: ttftW, Align: gui.End},
							{Title: "输入", Width: tokenW, Align: gui.End},
							{Title: "输出", Width: tokenW, Align: gui.End},
							{Title: "费用", Align: gui.End},
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
								gui.Text(c, time.UnixMilli(entry.TS).Format("15:04:05")).FontSize(11).TextColor(primary)
							case 1:
								gui.Text(c, entry.Endpoint).FontSize(11).TextColor(primary).SingleLine()
							case 2:
								gui.Text(c, entry.Model).FontSize(11).TextColor(primary).SingleLine()
							case 3:
								gui.Text(c, fmt.Sprintf("%d", entry.StatusCode)).FontSize(11).
									TextColor(statusTone)
							case 4:
								stream := ""
								if entry.Stream {
									stream = "SSE"
								}
								gui.Text(c, stream).FontSize(11).TextColor(secondary)
							case 5:
								gui.Text(c, fmtDuration(entry.DurationMs)).FontSize(11).TextColor(secondary)
							case 6:
								gui.Text(c, fmtDuration(entry.TTFTMs)).FontSize(11).TextColor(secondary)
							case 7:
								gui.Text(c, fmtTokens(entry.InputTokens)).FontSize(11).TextColor(secondary)
							case 8:
								gui.Text(c, fmtTokens(entry.OutputTokens)).FontSize(11).TextColor(secondary)
							case 9:
								gui.Text(c, fmtMoney(entry.Cost)).FontSize(11).TextColor(secondary)
							}
						})
					})

					// History is a page flip away; the strip sits below the table.
					s.logPager(c)
				})
				if wide && s.logSel >= 0 && s.logSel < len(rows) {
					s.logDrawer(c, rows[s.logSel])
				}
			})

			// Narrow windows keep the detail below the table: a 360px drawer
			// would leave less than the table's fixed columns need.
			if !wide && s.logSel >= 0 && s.logSel < len(rows) {
				s.logDetail(c, rows[s.logSel])
			}
		})
	})
}

// logPager is the log table's pagination strip: one flip to any page of the
// filtered history, with the page size beside the totals.
func (s *uiState) logPager(c *gui.Context) {
	pager(c, s.logPage, s.logTotalPages(),
		fmt.Sprintf("共 %d 条", s.logs.Total),
		func() {
			t := c.Theme()
			gui.Text(c, "每页").FontSize(10).TextColor(t.TextMuted)
			// The select speaks in strings; the state keeps the number. A new
			// size reopens on page 1, the newest rows.
			size := strconv.Itoa(s.logPageSize)
			if gui.Select(c, &size, logPageSizeOptions).Width(88).Changed() {
				if n, err := strconv.Atoi(size); err == nil {
					s.setLogPageSize(n)
				}
			}
		},
		s.moveLogPage)
}

// logDetail is the narrow-window detail: the same fields as the drawer, but
// below the table where a fixed-width column would not fit.
func (s *uiState) logDetail(c *gui.Context, r store.RequestRow) {
	panel(c, "请求 · "+time.UnixMilli(r.TS).Format("01-02 15:04:05"), func() {
		s.logDetailFields(c, r)
	})
}

// logDrawer is the wide-window detail: a 360px card beside the table, keeping
// the list in view while a row is inspected.
func (s *uiState) logDrawer(c *gui.Context, r store.RequestRow) {
	t := c.Theme()
	gui.Column(c).Width(360).Children(func() {
		card(c, func() {
			gui.Row(c).FillWidth().Gap(spS).AlignItems(gui.Center).Children(func() {
				gui.Column(c).Gap(2).Children(func() {
					gui.Text(c, "请求详情").FontSize(13).Bold()
					gui.Text(c, time.UnixMilli(r.TS).Format("01-02 15:04:05")).
						FontSize(9).TextColor(t.TextMuted)
				})
				gui.Spacer(c)
				close := gui.Button(c, "").Padding(2, 5)
				close.Children(func() {
					gui.Icon(c, iconClose()).FontSize(14).TextColor(t.TextMuted).
						Label("关闭请求详情")
				})
				if close.Clicked() {
					s.logSel = -1
				}
			})
			gui.Divider(c)
			s.logDetailFields(c, r)
			gui.Spacer(c)
			if gui.Button(c, "复制请求 ID").Clicked() {
				c.WriteClipboard(fmt.Sprintf("%d", r.ID))
				s.flash("已复制到剪贴板", false)
			}
		})
	})
}

// logDetailFields is the request anatomy shared by the drawer and the
// narrow-window panel: status, routing, timing, tokens and the failure reason.
func (s *uiState) logDetailFields(c *gui.Context, r store.RequestRow) {
	t := c.Theme()
	gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
		gui.Text(c, fmt.Sprintf("%d", r.StatusCode)).FontSize(11).Bold().
			TextColor(statusColor(c, r.StatusCode))
		if r.Stream {
			pill(c, "流式", t.Accent, t.Accent.Alpha(0.12))
		}
		if r.ClientDisconnected {
			pill(c, "客户端断开", t.Warning, t.Warning.Alpha(0.12))
		}
	})
	gui.Column(c).Gap(spXS).Children(func() {
		s.infoRow(c, "请求 ID", fmt.Sprintf("%d", r.ID))
		s.infoRow(c, "端点", r.Endpoint)
		s.infoRow(c, "入站协议", r.InboundProtocol)
		if r.ProviderName != nil {
			s.infoRow(c, "供应商", *r.ProviderName)
		}
		if r.ClientKeyName != nil {
			s.infoRow(c, "客户端密钥", *r.ClientKeyName)
		}
		if r.CredentialID != nil {
			s.infoRow(c, "凭据 ID", fmt.Sprintf("%d", *r.CredentialID))
		}
		s.infoRow(c, "模型", r.Model)
		s.infoRow(c, "耗时 / TTFT", fmtDuration(r.DurationMs)+" / "+fmtDuration(r.TTFTMs))
		s.infoRow(c, "Token", fmt.Sprintf("输入 %s · 输出 %s · 缓存 %s",
			fmtTokens(r.InputTokens), fmtTokens(r.OutputTokens), fmtTokens(r.CachedTokens)))
		s.infoRow(c, "费用", "$"+fmtMoney(r.Cost))
		if r.FinishReason != "" {
			s.infoRow(c, "完成原因", r.FinishReason)
		}
		if r.ErrorType != "" {
			s.infoRow(c, "错误类型", r.ErrorType)
		}
	})
}
