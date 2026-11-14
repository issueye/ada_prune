package ui

import (
	"fmt"
	gui "github.com/egoist/mygo/ui"
	"github.com/learningdog1/ada_prune/internal/store"
	"strings"
)

// keysPage manages downstream keys.
func (s *uiState) keysPage(c *gui.Context) {
	t := c.Theme()
	gui.Scroll(c).Grow(1).FillWidth().Children(func() {
		gui.Column(c).FillWidth().Padding(32, spL, spL, 32).Gap(spM).Children(func() {
			s.errorLine(c)

			panel(c, "新建客户端密钥", func() {
				gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
					gui.TextInput(c, &s.newKeyName).Width(220).Placeholder("名称，例如 codex-local")
					if primaryIconButton(c, iconPlus(), "创建").Clicked() {
						s.createClientKey()
					}
				})
				if s.createdToken != "" {
					gui.Row(c).FillWidth().Padding(8, 12).Radius(8).
						Background(t.Accent.Alpha(0.1)).Gap(spS).AlignItems(gui.Center).Children(func() {
						gui.Text(c, "明文:").FontSize(11).TextColor(t.Accent)
						gui.Text(c, s.createdToken).FontSize(11).Bold().SingleLine().Grow(1).Tooltip(s.createdToken)
						if iconButton(c, iconCopy(), "复制").FontSize(11).Padding(2, 8).Clicked() {
							c.WriteClipboard(s.createdToken)
							s.flash("已复制到剪贴板", false)
						}
						// The banner answers only to its owner: closing it (or
						// leaving the page) clears it from state. The plaintext
						// itself stays retrievable through the row actions.
						if gui.Button(c, "关闭").FontSize(11).Padding(2, 8).Clicked() {
							s.createdToken = ""
						}
					})
				}
				gui.Text(c, "密钥加密存储；校验用 SHA-256 哈希，行操作里可随时取回明文。").FontSize(10).TextColor(t.TextMuted)
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
				gui.Table(c, &s.keyTable, []gui.TableColumn{
					{Title: "名称", Width: 190},
					{Title: "前缀", Width: 210},
					{Title: "状态", Width: 100},
					{Title: "额度", Width: 140, Align: gui.End},
					{Title: "最近使用"},
					{Title: "操作", Width: 84, Fixed: true, Align: gui.Center},
				}, len(rows), func(row, col int) {
					if row < 0 || row >= len(rows) {
						return
					}
					entry := rows[row]
					selected := row == s.keySel
					primary, secondary := rowColors(c, selected)
					switch col {
					case 0:
						gui.Text(c, entry.Name).FontSize(11).TextColor(primary).SingleLine()
					case 1:
						gui.Text(c, entry.KeyPrefix).FontSize(11).TextColor(secondary).SingleLine()
					case 2:
						statusPill(c, entry.Enabled, "启用", "停用", selected)
					case 3:
						quota := "不限"
						if entry.QuotaTokens > 0 {
							quota = fmt.Sprintf("%s/%s", fmtTokens(entry.QuotaUsed), fmtTokens(entry.QuotaTokens))
						}
						gui.Text(c, quota).FontSize(11).TextColor(secondary)
					case 4:
						last := "-"
						if entry.LastUsedAt != nil {
							last = formatTime(*entry.LastUsedAt)
						}
						gui.Text(c, last).FontSize(11).TextColor(secondary)
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
func (s *uiState) keyRowActions(c *gui.Context, index int, entry store.ClientKey) {
	rowActions(c, func(m *gui.Menu) {
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

func (s *uiState) keyDetail(c *gui.Context, entry store.ClientKey) {
	panel(c, "密钥 · "+entry.Name, func() {
		gui.Row(c).Children(func() { statusPill(c, entry.Enabled, "启用", "停用", false) })
		gui.Column(c).Gap(spXS).Children(func() {
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
