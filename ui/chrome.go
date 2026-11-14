package ui

import (
	"fmt"
	gui "github.com/egoist/mygo/ui"
	"github.com/learningdog1/ada_prune/internal/version"
)

// footer is the window's status strip: run state and endpoint on the left, the
// changing numbers and where the data lives on the right.
//
// Everything that merely *reports* lives here rather than in the title bar: the
// title bar is a drag handle, and status that moves around under the pointer is
// hard to read.
func (s *uiState) footer(c *gui.Context) {
	t := c.Theme()
	gui.Row(c).Height(28).FillWidth().Padding(0, spM, 0, spL).Gap(spM).
		Background(t.Surface).AlignItems(gui.Center).Children(func() {
		dot := t.Success
		if s.overview.Degraded {
			dot = t.Warning
		}
		gui.Row(c).Gap(spS).AlignItems(gui.Center).Children(func() {
			gui.Box(c).Size(6, 6).Radius(3).Background(dot)
			gui.Text(c, "运行中").FontSize(10).TextColor(t.TextMuted)
		})
		gui.Text(c, s.endpoint()).FontSize(10).TextColor(t.TextMuted).SingleLine()
		gui.Text(c, "数据目录 "+s.overview.DataDir).FontSize(10).TextColor(t.TextMuted).
			MaxWidth(420).SingleLine()

		gui.Spacer(c)

		gui.Text(c, fmt.Sprintf("在途 %s · 运行 %s · 存储 %s · 适配器 %d · v%s",
			fmtInt(s.overview.Inflight),
			fmtUptime(s.overview.UptimeSec),
			storageLabel(s.overview.Degraded),
			len(s.adapters),
			version.Version,
		)).FontSize(10).TextColor(t.TextMuted).SingleLine()
	})
}

// titleBar replaces the OS title bar: brand and version on the left. It stays
// deliberately empty otherwise — run state and counts belong to the footer.
// The window's own controls float above it, so the padding comes from the room
// they take.
func (s *uiState) titleBar(c *gui.Context) {
	t := c.Theme()
	bar := c.TitleBar()
	height := bar.Height
	if height < 44 {
		height = 44
	}

	gui.Row(c).Height(height).Padding(0, bar.Right+spM, 0, bar.Left+spM).DragWindow().
		Background(t.Surface).AlignItems(gui.Center).Children(func() {
		gui.Row(c).Gap(9).AlignItems(gui.Center).Children(func() {
			gui.Box(c).Size(24, 24).Radius(7).Center().Background(t.Accent).
				Children(func() {
					gui.Text(c, "A").FontSize(13).Bold().TextColor(t.AccentText)
				})
			gui.Text(c, "AdaPrune").FontSize(13).Bold().SingleLine()
			pill(c, "v"+version.Version, t.TextMuted, t.TextMuted.Alpha(0.1))
		})

		// The palette's doorway sits in the title bar so it is reachable from
		// every page without hunting for a shortcut.
		search := gui.Row(c).Width(320).Height(28).Radius(8).Padding(0, 9).Gap(7).
			Border(1, t.Border).Background(t.SurfaceHover).AlignItems(gui.Center).
			Cursor(gui.CursorPointer)
		search.Children(func() {
			gui.Icon(c, iconSearch()).FontSize(13).TextColor(t.TextMuted)
			gui.Text(c, "搜索页面、上游或模型").FontSize(11).TextColor(t.TextMuted).SingleLine()
			gui.Spacer(c)
			gui.Text(c, "Ctrl K").FontSize(9).TextColor(t.TextMuted)
		})
		if search.Clicked() {
			s.paletteQuery = ""
			s.paletteOpen = true
		}

		gui.Spacer(c)
		label := "切换到深色"
		if s.dark {
			label = "切换到浅色"
		}
		b := gui.Button(c, "").Padding(2, 6)
		b.Children(func() {
			gui.Icon(c, iconTheme()).FontSize(15).TextColor(t.TextMuted).Label(label).Tooltip(label)
		})
		if b.Clicked() {
			s.toggleTheme()
		}
	})
}

// endpoint renders the listener address. The actually bound host/port wins:
// the configured port can drift when the desktop shell scans for a free one,
// and the UI must never advertise an address that does not answer.
func (s *uiState) endpoint() string {
	host := s.boundHost
	if host == "" {
		host = s.overview.Host
	}
	if host == "" {
		host = "127.0.0.1"
	}
	port := s.boundPort
	if port == 0 {
		port = s.overview.Port
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

func (s *uiState) header(c *gui.Context) {
	t := c.Theme()
	meta, ok := pageTitles[s.page]
	if !ok {
		meta = pageTitle{"", "总览", ""}
	}
	gui.Row(c).Padding(32, spM, spL, spM).Gap(spM).AlignItems(gui.Center).Children(func() {
		gui.Column(c).Gap(2).Children(func() {
			if meta.Kicker != "" {
				gui.Text(c, meta.Kicker).FontSize(9).TextColor(t.TextMuted).LetterSpacing(0.4)
			}
			gui.Text(c, meta.Title).FontSize(22).Bold().LineHeight(1.1)
			gui.Text(c, meta.Subtitle).FontSize(12).TextColor(t.TextMuted)
		})
		gui.Spacer(c)
		if s.page == "/" {
			if primaryIconButton(c, iconPlus(), "新建供应商").Clicked() {
				s.openNewProvider()
			}
		}
		if s.status != "" {
			fg, bg := t.Success, t.Success.Alpha(0.12)
			if s.isErr {
				fg, bg = t.Danger, t.Danger.Alpha(0.12)
			}
			pill(c, s.status, fg, bg)
		}
		if s.loading {
			gui.Spinner(c)
		}
		if iconButton(c, iconRefresh(), "刷新").Clicked() {
			s.pageErr = ""
			s.reload()
		}
	})
}
