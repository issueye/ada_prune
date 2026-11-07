package desktop

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

// settingsPage edits the runtime configuration.
func (s *uiState) settingsPage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(spXL, spL, spL, spXL).Gap(spM).Children(func() {
			s.errorLine(c)

			// Save stays at the top: the form is longer than the window, and a
			// button below the fold is a button that never gets pressed.
			ui.Row(c).Gap(spS).Children(func() {
				if ui.PrimaryButton(c, "保存").Clicked() {
					s.saveSettings()
				}
				if ui.Button(c, "重新载入").Clicked() {
					s.reload()
				}
			})

			panel(c, "服务", func() {
				fieldWith(c, "监听地址", "改动需重启生效", func() {
					ui.TextInput(c, &s.hostText).Width(220)
				})
				fieldWith(c, "端口", "改动需重启生效", func() {
					ui.TextInput(c, &s.portText).Width(120)
				})
				fieldWith(c, "在途上限", "0 表示不限", func() {
					ui.TextInput(c, &s.maxInflightTxt).Width(120)
				})
			})

			panel(c, "协议", func() {
				fieldWith(c, "默认模型", "请求未带 model 时使用；留空则要求显式指定", func() {
					ui.TextInput(c, &s.defaultModel).Width(320).Placeholder("例如 OpenAI 主号/gpt-5.6-luna")
				})
				ui.Text(c, "流式空闲看门狗、请求体上限等只读参数通过配置文件调整。").FontSize(10).TextColor(t.TextMuted)
			})

			panel(c, "日志", func() {
				fieldWith(c, "级别", "debug / info / warn / error", func() {
					levels := []string{"debug", "info", "warn", "error"}
					ui.Select(c, &s.levelText, levels).Width(220)
				})
				fieldWith(c, "保留天数", "超过天数的请求日志会被清理", func() {
					ui.TextInput(c, &s.retentionText).Width(120)
				})
			})

			if len(s.settings.EnvPinned) > 0 {
				panel(c, "被环境变量钉住的键", func() {
					ui.Text(c, fmt.Sprintf("%v 由环境变量提供，界面上的修改会被忽略。", s.settings.EnvPinned)).
						FontSize(11).TextColor(t.Warning)
				})
			}

			ui.Text(c, "配置文件: "+s.settings.ConfigPath).FontSize(10).TextColor(t.TextMuted).SingleLine()
		})
	})
}

// aboutPage shows build identity and runtime paths.
func (s *uiState) aboutPage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(spXL, spL, spL, spXL).Gap(spM).Children(func() {
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
					ui.Row(c).Gap(spS).Children(func() {
						ui.Text(c, pair[0]).FontSize(11).TextColor(t.TextMuted).Width(110)
						ui.Text(c, pair[1]).FontSize(11).SingleLine()
					})
				}
			})

			panel(c, "已注册适配器", func() {
				if len(s.adapters) == 0 {
					emptyState(c, "没有适配器", "检查 provider 包的 init 注册")
					return
				}
				for _, a := range s.adapters {
					ui.Row(c).FillWidth().Padding(spXS, 0).Gap(spS).AlignItems(ui.Center).Children(func() {
						ui.Text(c, a.Label).FontSize(11).Width(140).SingleLine()
						ui.Text(c, string(a.Kind)).FontSize(11).TextColor(t.TextMuted).Width(150)
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
				ui.Text(c, fmt.Sprintf("被限流窗口阻塞的凭据: %d", windows)).
					FontSize(11).TextColor(t.TextMuted)
				ui.Text(c, "许可: MIT · Command Code 协议的派生声明见 NOTICE").
					FontSize(10).TextColor(t.TextMuted)
			})
		})
	})
}

// fieldWith renders a label, its hint and the control they describe as one
// group, so a control is never mistaken for the next field's. The hint stays
// out of the layout: an info icon by the label shows it as a tooltip.
func fieldWith(c *ui.Context, label, hint string, build func()) {
	ui.Column(c).Gap(spXS).Children(func() {
		ui.Row(c).Gap(spXS).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(11).Bold()
			if hint != "" {
				hintIcon(c, label, hint)
			}
		})
		build()
	})
}

// ctx returns a request context for UI-triggered actions.
func (s *uiState) ctx() context.Context { return context.Background() }

// formatTime renders a millisecond timestamp for table cells.
func formatTime(ts int64) string {
	if ts <= 0 {
		return "-"
	}
	return time.UnixMilli(ts).Format("01-02 15:04")
}
