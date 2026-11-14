package ui

import (
	"context"
	"errors"
	gui "github.com/egoist/mygo/ui"
	"github.com/learningdog1/ada_prune/internal/admin"
	"github.com/learningdog1/ada_prune/internal/store"
	"strings"
)

func (s *uiState) setCredentialStatus(index int, status string) {
	rows := s.credentials
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	s.run("更新凭据状态", func() error {
		return s.app.Admin.SetCredentialStatus(context.Background(), id, status)
	})
}

func (s *uiState) pinCredential(index int) {
	rows := s.credentials
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	pinned := rows[index].Pinned
	s.run("更新固定凭据", func() error {
		if pinned {
			return s.app.Admin.PinCredential(context.Background(), 0)
		}
		return s.app.Admin.PinCredential(context.Background(), id)
	})
}

func (s *uiState) deleteCredential(index int) {
	rows := s.credentials
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	name := rows[index].Name
	s.confirm("删除凭据 "+name+"？",
		"该上游密钥立即不可用；若它是路由固定的凭据，固定设置一并清除。此操作不可撤销。",
		"删除", func() {
			s.run("删除凭据", func() error {
				return s.app.Admin.DeleteCredential(context.Background(), id)
			})
		})
}

func (s *uiState) createClientKey() {
	name := strings.TrimSpace(s.newKeyName)
	if name == "" {
		s.flash("请先填写密钥名称", true)
		return
	}
	go func() {
		created, err := s.app.Admin.CreateClientKey(context.Background(), admin.ClientKeyInput{Name: name})
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			if err != nil {
				s.flash("创建失败: "+err.Error(), true)
				return
			}
			s.createdToken = created.Token
			s.newKeyName = ""
			s.flash("已创建，可随时从行操作取回", false)
			s.reloadQuiet()
		})
	}()
}

func (s *uiState) rotateClientKey(index int) {
	rows := s.clientKeys
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	name := rows[index].Name
	s.confirm("轮换密钥 "+name+"？",
		"旧明文立即失效，所有正在使用它的下游工具会断开；新明文会弹出，之后也可随时从行操作取回。",
		"轮换", func() {
			go func() {
				token, err := s.app.Admin.RotateClientKey(context.Background(), id)
				if s.win == nil {
					return
				}
				s.win.Update(func() {
					if err != nil {
						s.flash("轮换失败: "+err.Error(), true)
						return
					}
					s.createdToken = token
					s.flash("已轮换，旧密钥立即失效", false)
					s.reloadQuiet()
				})
			}()
		})
}

func (s *uiState) deleteClientKey(index int) {
	rows := s.clientKeys
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	name := rows[index].Name
	s.confirm("删除客户端密钥 "+name+"？",
		"使用这个密钥的下游工具将立即收到 401，需要换新密钥才能恢复。此操作不可撤销。",
		"删除", func() {
			s.run("删除密钥", func() error {
				return s.app.Admin.DeleteClientKey(context.Background(), id)
			})
		})
}

// copyClientKey decrypts the stored plaintext onto the clipboard. It runs on
// the main thread on purpose: the reveal is one indexed read plus a decrypt,
// and the clipboard write needs the render context.
func (s *uiState) copyClientKey(c *gui.Context, index int) {
	rows := s.clientKeys
	if index < 0 || index >= len(rows) {
		return
	}
	token, err := s.app.Admin.RevealClientKey(context.Background(), rows[index].ID)
	if err != nil {
		// Keys minted before encrypted storage have no plaintext to reveal;
		// rotation is the way out, so say that instead of showing raw English.
		if errors.Is(err, store.ErrClientKeyNotRecoverable) {
			s.flash("该密钥创建于旧版本，无法取回明文；轮换一次即可获得可复制的新密钥", true)
			return
		}
		s.flash("复制失败: "+err.Error(), true)
		return
	}
	c.WriteClipboard(token)
	s.flash("已复制到剪贴板", false)
}
