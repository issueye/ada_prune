package ui

import (
	"context"
	"strconv"
	"strings"
)

func (s *uiState) saveSettings() {
	// Field validation stays in the page: an invalid port silently falling
	// back to the old value reads as "saved but nothing happened".
	s.settingsError = ""
	port, err := strconv.Atoi(strings.TrimSpace(s.portText))
	if err != nil || port < 1 || port > 65535 {
		s.settingsError = "端口必须是 1–65535 的整数"
		return
	}
	host := strings.TrimSpace(s.hostText)
	if host == "" {
		s.settingsError = "监听地址不能为空"
		return
	}
	inflight, err := strconv.ParseInt(strings.TrimSpace(s.maxInflightTxt), 10, 64)
	if err != nil || inflight < 0 {
		s.settingsError = "在途上限必须是不小于 0 的整数"
		return
	}
	retention, err := strconv.Atoi(strings.TrimSpace(s.retentionText))
	if err != nil || retention < 1 {
		s.settingsError = "日志保留天数必须不小于 1"
		return
	}
	streamIdle, err := strconv.Atoi(strings.TrimSpace(s.streamIdleText))
	if err != nil || streamIdle < 1 || streamIdle > 3600 {
		s.settingsError = "流式空闲超时必须是 1–3600 的整数（秒）"
		return
	}
	nonStreamIdle, err := strconv.Atoi(strings.TrimSpace(s.nonStreamIdleText))
	if err != nil || nonStreamIdle < 1 || nonStreamIdle > 3600 {
		s.settingsError = "非流式空闲超时必须是 1–3600 的整数（秒）"
		return
	}
	patch := map[string]any{
		"logLevel":         s.levelText,
		"defaultModel":     strings.TrimSpace(s.defaultModel),
		"host":             host,
		"logRetentionDays": retention,
		"port":             port,
		"maxInflight":      inflight,
		// The watchdogs apply to new upstream requests without a restart.
		"streamIdleMs":    streamIdle * 1000,
		"nonStreamIdleMs": nonStreamIdle * 1000,
	}
	go func() {
		result, err := s.app.Admin.SaveSettings(context.Background(), patch)
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			if err != nil {
				s.settingsError = err.Error()
				s.flash("保存失败: "+err.Error(), true)
				return
			}
			s.settingsDirty = false
			if len(result.RestartRequired) > 0 {
				s.flash("已保存，需重启生效: "+strings.Join(result.RestartRequired, ", "), true)
				s.reloadQuiet()
				return
			}
			s.flash("设置已保存", false)
			s.reloadQuiet()
		})
	}()
}
