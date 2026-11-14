package ui

import (
	"fmt"
	gui "github.com/egoist/mygo/ui"
	"github.com/learningdog1/ada_prune/internal/store"
)

// credentialsPage lists upstream keys.
func (s *uiState) credentialsPage(c *gui.Context) {
	t := c.Theme()
	gui.Scroll(c).Grow(1).FillWidth().Children(func() {
		gui.Column(c).FillWidth().Padding(32, spL, spL, 32).Gap(spM).Children(func() {
			s.errorLine(c)

			gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
				pill(c, fmt.Sprintf("%d 个", len(s.credentials)), t.TextMuted, t.TextMuted.Alpha(0.1))
				gui.Spacer(c)
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
				gui.Table(c, &s.credTable, []gui.TableColumn{
					{Title: "名称", Width: 200},
					{Title: "供应商", Width: 190},
					{Title: "密钥", Width: 210},
					{Title: "状态", Width: 120},
					{Title: "固定", Width: 80},
					{Title: "最近使用"},
					{Title: "操作", Width: 84, Fixed: true, Align: gui.Center},
				}, len(rows), func(row, col int) {
					if row < 0 || row >= len(rows) {
						return
					}
					entry := rows[row]
					selected := row == s.credSel
					primary, secondary := rowColors(c, selected)
					switch col {
					case 0:
						gui.Text(c, entry.Name).FontSize(11).TextColor(primary).SingleLine()
					case 1:
						gui.Text(c, entry.ProviderName).FontSize(11).TextColor(secondary).SingleLine()
					case 2:
						gui.Text(c, entry.SecretMask).FontSize(11).TextColor(secondary).SingleLine()
					case 3:
						credentialPill(c, entry, selected)
					case 4:
						if entry.Pinned {
							fg, bg := pillTone(c, selected, t.Accent)
							pill(c, "已固定", fg, bg)
						} else {
							gui.Text(c, "-").FontSize(11).TextColor(secondary)
						}
					case 5:
						last := "-"
						if entry.LastUsedAt != nil {
							last = formatTime(*entry.LastUsedAt)
						}
						gui.Text(c, last).FontSize(11).TextColor(secondary)
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
func (s *uiState) credentialRowActions(c *gui.Context, index int, entry store.Credential) {
	rowActions(c, func(m *gui.Menu) {
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

func (s *uiState) credentialDetail(c *gui.Context, entry store.Credential) {
	panel(c, "凭据 · "+entry.Name, func() {
		gui.Row(c).Children(func() { credentialPill(c, entry, false) })
		gui.Column(c).Gap(spXS).Children(func() {
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

// credentialPill renders the routing state of a credential.
func credentialPill(c *gui.Context, entry store.Credential, selected bool) {
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
