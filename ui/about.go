package ui

import (
	"fmt"
	gui "github.com/egoist/mygo/ui"
	"time"
)

// aboutPage shows build identity and runtime paths.
func (s *uiState) aboutPage(c *gui.Context) {
	t := c.Theme()
	gui.Scroll(c).Grow(1).FillWidth().Children(func() {
		gui.Column(c).FillWidth().Padding(32, spL, spL, 32).Gap(spM).Children(func() {
			s.errorLine(c)

			panel(c, "AdaPrune", func() {
				rows := [][2]string{
					{"版本", s.status2.Version},
					{"数据目录", s.status2.DataDir},
					{"数据库", s.status2.DatabasePath},
					{"日志文件", s.status2.LogFile},
					{"运行时长", fmtUptime(s.status2.UptimeSec)},
					{"协程数", fmt.Sprintf("%d", s.status2.GoRoutines)},
					{"存储", storageLabel(s.status2.Degraded)},
				}
				for _, pair := range rows {
					gui.Row(c).Gap(spS).Children(func() {
						gui.Text(c, pair[0]).FontSize(11).TextColor(t.TextMuted).Width(110)
						gui.Text(c, pair[1]).FontSize(11).SingleLine()
					})
				}
			})

			panel(c, "已注册适配器", func() {
				if len(s.adapters) == 0 {
					emptyState(c, "没有适配器", "检查 provider 包的 init 注册")
					return
				}
				for _, a := range s.adapters {
					gui.Row(c).FillWidth().Padding(spXS, 0).Gap(spS).AlignItems(gui.Center).Children(func() {
						gui.Text(c, a.Label).FontSize(11).Width(140).SingleLine()
						gui.Text(c, string(a.Kind)).FontSize(11).TextColor(t.TextMuted).Width(150)
						if a.Capabilities.Fingerprint {
							pill(c, "指纹", t.Accent, t.Accent.Alpha(0.12))
						}
						if a.Capabilities.AccountUsage {
							pill(c, "账号用量", t.Accent, t.Accent.Alpha(0.12))
						}
						if a.Capabilities.ModelDiscovery {
							pill(c, "模型发现", t.TextMuted, t.TextMuted.Alpha(0.1))
						}
					})
				}
			})

			panel(c, "运行时", func() {
				windows := s.router().Windows().Len()
				gui.Text(c, fmt.Sprintf("被限流窗口阻塞的凭据: %d", windows)).
					FontSize(11).TextColor(t.TextMuted)
				gui.Text(c, "许可: MIT · Command Code 协议的派生声明见 NOTICE").
					FontSize(10).TextColor(t.TextMuted)
			})
		})
	})
}

// formatTime renders a millisecond timestamp for table cells.
func formatTime(ts int64) string {
	if ts <= 0 {
		return "-"
	}
	return time.UnixMilli(ts).Format("01-02 15:04")
}
