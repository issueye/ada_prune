package ui

import (
	"fmt"
	gui "github.com/egoist/mygo/ui"
)

// settingsPage edits the runtime configuration: the categories on the left,
// the chosen one's fields on the right.
func (s *uiState) settingsPage(c *gui.Context) {
	t := c.Theme()
	gui.Scroll(c).Grow(1).FillWidth().Children(func() {
		gui.Column(c).FillWidth().Padding(32, spL, spL, 32).Gap(spM).Children(func() {
			s.errorLine(c)

			gui.Row(c).FillWidth().AlignItems(gui.Start).Gap(spL).Children(func() {
				// Left: the categories. 环境 appears only when the environment
				// actually pins keys, like its panel used to.
				gui.Column(c).Width(150).Gap(1).Children(func() {
					for _, section := range s.settingsSections() {
						s.settingsSectionRow(c, section)
					}
				})

				// Right: the chosen section. Save stays at the top — a button
				// below the fold is a button that never gets pressed.
				gui.Column(c).Grow(1).Gap(spM).Children(func() {
					gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
						if gui.PrimaryButton(c, "保存").Clicked() {
							s.saveSettings()
						}
						if gui.Button(c, "重新载入").Clicked() {
							s.settingsDirty = false
							s.settingsError = ""
							s.reload()
						}
						if s.settingsDirty {
							pill(c, "未保存", t.Warning, t.Warning.Alpha(0.12))
						}
					})
					if s.settingsError != "" {
						gui.Row(c).FillWidth().Padding(8, 12).Radius(8).
							Background(t.Danger.Alpha(0.1)).Children(func() {
							gui.Text(c, s.settingsError).FontSize(11).TextColor(t.Danger)
						})
					}

					switch s.settingsSection {
					case "协议":
						s.settingsProtocolSection(c)
					case "看门狗":
						s.settingsWatchdogSection(c)
					case "日志":
						s.settingsLogSection(c)
					case "环境":
						s.settingsEnvSection(c)
					default:
						s.settingsServerSection(c)
					}

					gui.Text(c, "配置文件: "+s.settings.ConfigPath).FontSize(10).
						TextColor(t.TextMuted).SingleLine().Tooltip(s.settings.ConfigPath)
				})
			})
		})
	})
}

// settingsSections lists the settings page's categories; 环境 joins only when
// the environment pins configuration keys.
func (s *uiState) settingsSections() []string {
	sections := []string{"服务", "协议", "看门狗", "日志"}
	if len(s.settings.EnvPinned) > 0 {
		sections = append(sections, "环境")
	}
	return sections
}

// settingsSectionRow is one entry of the settings page's left column.
func (s *uiState) settingsSectionRow(c *gui.Context, section string) {
	t := c.Theme()
	selected := s.settingsSection == section
	row := gui.Row(c.Key("settings-"+section)).FillWidth().Padding(0, 10).Height(30).
		Radius(7).Gap(spS).AlignItems(gui.Center).Focusable().Cursor(gui.CursorPointer)
	if selected {
		row.Background(t.Surface).Shadow(0, 1, 2, 0, gui.RGBA(0, 0, 0, 0.05))
	} else if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	labelColor := t.Text
	if selected {
		labelColor = t.Accent
	}
	row.Children(func() {
		bar := gui.Box(c).Size(3, 14).Radius(2)
		if selected {
			bar.Background(t.Accent)
		}
		gui.Text(c, section).FontSize(12).TextColor(labelColor).SingleLine()
	})
	if row.Clicked() || row.Shortcut(0, gui.KeyEnter) || row.Shortcut(0, gui.KeySpace) {
		if s.settingsSection != section {
			s.settingsSection = section
		}
	}
}

// settingsServerSection holds the listener fields; host and port need a
// restart, the in-flight cap does not.
func (s *uiState) settingsServerSection(c *gui.Context) {
	panel(c, "服务", func() {
		fieldWith(c, "监听地址", "改动需重启生效", func() {
			if gui.TextInput(c, &s.hostText).Width(220).Changed() {
				s.settingsDirty = true
			}
		})
		fieldWith(c, "端口", "改动需重启生效", func() {
			if gui.TextInput(c, &s.portText).Width(120).Changed() {
				s.settingsDirty = true
			}
		})
		fieldWith(c, "在途上限", "0 表示不限", func() {
			if gui.TextInput(c, &s.maxInflightTxt).Width(120).Changed() {
				s.settingsDirty = true
			}
		})
	})
}

// settingsProtocolSection holds the fallback model.
func (s *uiState) settingsProtocolSection(c *gui.Context) {
	panel(c, "协议", func() {
		fieldWith(c, "默认模型", "请求未带 model 时使用；留空则要求显式指定", func() {
			if gui.TextInput(c, &s.defaultModel).Width(320).Placeholder("例如 OpenAI 主号/gpt-5.6-luna").Changed() {
				s.settingsDirty = true
			}
		})
		gui.Text(c, "请求体上限等其余参数仍通过配置文件调整。").FontSize(10).TextColor(c.Theme().TextMuted)
	})
}

// settingsWatchdogSection tunes the upstream idle watchdogs. They apply to new
// requests immediately — no restart, no browser-style reload dance.
func (s *uiState) settingsWatchdogSection(c *gui.Context) {
	panel(c, "看门狗", func() {
		fieldWith(c, "流式空闲超时（秒）", "上游流连续静默超过该时长即 504 熔断；每个事件都会重置计时", func() {
			if gui.TextInput(c, &s.streamIdleText).Width(120).Changed() {
				s.settingsDirty = true
			}
		})
		fieldWith(c, "非流式空闲超时（秒）", "非流式上游响应的整体空闲上限", func() {
			if gui.TextInput(c, &s.nonStreamIdleText).Width(120).Changed() {
				s.settingsDirty = true
			}
		})
		gui.Text(c, "保存后立即对新的上游请求生效，无需重启。").FontSize(10).TextColor(c.Theme().TextMuted)
	})
}

// settingsLogSection holds the log level and retention.
func (s *uiState) settingsLogSection(c *gui.Context) {
	panel(c, "日志", func() {
		fieldWith(c, "级别", "debug / info / warn / error", func() {
			levels := []string{"debug", "info", "warn", "error"}
			if gui.Select(c, &s.levelText, levels).Width(220).Changed() {
				s.settingsDirty = true
			}
		})
		fieldWith(c, "保留天数", "超过天数的请求日志会被清理", func() {
			if gui.TextInput(c, &s.retentionText).Width(120).Changed() {
				s.settingsDirty = true
			}
		})
	})
}

// settingsEnvSection shows the keys the environment pins; they are read-only
// because the process would ignore edits anyway.
func (s *uiState) settingsEnvSection(c *gui.Context) {
	panel(c, "被环境变量钉住的键", func() {
		gui.Text(c, fmt.Sprintf("%v 由环境变量提供，界面上的修改会被忽略。", s.settings.EnvPinned)).
			FontSize(11).TextColor(c.Theme().Warning)
	})
}
