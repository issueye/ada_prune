package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/learningdog1/ada_prune/internal/version"
)

// view builds the window: the title bar the app draws itself, the sidebar with
// the navigation, the page header, the page body, the status footer and any open
// dialog.
func (s *uiState) view(c *ui.Context) {
	t := c.Theme()
	s.registerShortcuts(c)
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		s.titleBar(c)
		ui.Divider(c)
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			s.sidebar(c)
			ui.Divider(c)
			ui.Column(c).Grow(1).FillHeight().Children(func() {
				s.header(c)
				s.pageBody(c)
			})
		})
		ui.Divider(c)
		s.footer(c)

		// The provider form is a modal so it can be opened from anywhere and
		// cannot be confused with the page behind it. Escape or a click outside
		// closes it (mygo clears the flag).
		ui.Modal(c, &s.formOpen, func() {
			s.providerDialog(c)
		})
		ui.Modal(c, &s.modelFormOpen, func() {
			s.modelDialog(c)
		})
		ui.Modal(c, &s.discoverOpen, func() {
			s.discoverDialog(c)
		})
		ui.Modal(c, &s.routeFormOpen, func() {
			s.routeDialog(c)
		})
		ui.Modal(c, &s.paletteOpen, func() {
			s.paletteDialog(c)
		})

		// The destructive-action confirmation sits above everything, including
		// the forms it may be opened from. Escape or 取消 drops the action.
		choice := ui.AlertDialog(c, &s.confirmOpen, s.confirmTitle, s.confirmMessage,
			"取消", s.confirmLabel)
		if choice == 1 {
			action := s.confirmAction
			s.confirmAction = nil
			if action != nil {
				action()
			}
		} else if !s.confirmOpen {
			// 取消 or Escape closed it: nothing destructive may run later.
			s.confirmAction = nil
		}
	})
}

// registerShortcuts wires the window-level keys: refresh, page jumps by number
// and the command palette. They yield while a dialog covers the window, so a
// stray chord never acts on the page underneath.
func (s *uiState) registerShortcuts(c *ui.Context) {
	c.OnShortcut(ui.Cmd, ui.KeyR, func() {
		if s.dialogOpen() {
			return
		}
		s.pageErr = ""
		s.reload()
	})
	c.OnShortcut(ui.Cmd, ui.KeyK, func() {
		if s.dialogOpen() {
			return
		}
		s.paletteQuery = ""
		s.paletteOpen = true
	})
	// The sidebar toggle lives on Cmd+\ : Cmd+B collides with the text editor's
	// emacs bindings (B is caret-left), which would swallow it while typing.
	c.OnShortcut(ui.Cmd, ui.KeyBackslash, func() {
		if s.dialogOpen() {
			return
		}
		s.toggleSidebar()
	})
	for i, item := range navItems() {
		if i >= 9 {
			break
		}
		key := ui.Key1 + ui.Key(i)
		target := item.Path
		c.OnShortcut(ui.Cmd, key, func() {
			if s.dialogOpen() {
				return
			}
			s.navigate(target)
		})
	}
}

// footer is the window's status strip: run state and endpoint on the left, the
// changing numbers and where the data lives on the right.
//
// Everything that merely *reports* lives here rather than in the title bar: the
// title bar is a drag handle, and status that moves around under the pointer is
// hard to read.
func (s *uiState) footer(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Height(28).FillWidth().Padding(0, spM, 0, spL).Gap(spM).
		Background(t.Surface).AlignItems(ui.Center).Children(func() {
		dot := t.Success
		if s.overview.Degraded {
			dot = t.Warning
		}
		ui.Row(c).Gap(spS).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(6, 6).Radius(3).Background(dot)
			ui.Text(c, "运行中").FontSize(10).TextColor(t.TextMuted)
		})
		ui.Text(c, s.endpoint()).FontSize(10).TextColor(t.TextMuted).SingleLine()
		ui.Text(c, "数据目录 "+s.overview.DataDir).FontSize(10).TextColor(t.TextMuted).
			MaxWidth(420).SingleLine()

		ui.Spacer(c)

		ui.Text(c, fmt.Sprintf("在途 %s · 运行 %s · 存储 %s · 适配器 %d · v%s",
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
func (s *uiState) titleBar(c *ui.Context) {
	t := c.Theme()
	bar := c.TitleBar()
	height := bar.Height
	if height < 42 {
		height = 42
	}

	ui.Row(c).Height(height).Padding(0, bar.Right+spM, 0, bar.Left+spM).DragWindow().
		Background(t.Surface).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(24, 24).Radius(7).Center().
				Gradient(ui.Hex("#0d9488"), ui.Hex("#0891b2"), 135).Children(func() {
				ui.Text(c, "A").FontSize(13).Bold().TextColor(ui.Hex("#ffffff"))
			})
			ui.Text(c, "AdaPrune").FontSize(13).Bold().SingleLine()
			pill(c, "v"+version.Version, t.TextMuted, t.TextMuted.Alpha(0.1))
		})
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

func (s *uiState) sidebar(c *ui.Context) {
	t := c.Theme()
	previous := s.page

	// Two shapes, one tree: the rail keeps every page reachable by its icon,
	// the full bar adds the group titles and labels. Width is the only thing
	// the rest of the window sees.
	width := float32(206)
	if s.sidebarCollapsed {
		width = 56
	}
	ui.Column(c).Width(width).FillHeight().Background(t.Background).Children(func() {
		ui.Column(c).Padding(spS, 10, spS, 10).Gap(1).Grow(1).Children(func() {
			for _, group := range navGroups {
				if s.sidebarCollapsed {
					// The rail drops the titles but keeps the group rhythm.
					ui.Box(c).Height(spS)
				} else {
					ui.Text(c, group.Title).FontSize(10).TextColor(t.TextMuted).Padding(10, 8, spXS, 10)
				}
				for _, item := range group.Items {
					s.navRow(c, item)
				}
			}
		}).Grow(1)

		ui.Divider(c)
		s.sidebarFooter(c)
		s.sidebarToggle(c)
	})

	if s.page != previous {
		s.onPageChange()
	}
}

// navRow is one sidebar item: a surface pill with an accent bar when chosen.
// Enter or Space activates the focused row, so the sidebar works without a
// pointer. Folded, the icon alone carries the page — named for the reader and
// screen readers, with the label as a tooltip.
func (s *uiState) navRow(c *ui.Context, item navItem) {
	t := c.Theme()
	selected := s.page == item.Path
	folded := s.sidebarCollapsed

	row := ui.Row(c.Key(item.Path)).Padding(0, 10).Height(30).Radius(7).
		Gap(spS).AlignItems(ui.Center).Focusable().Cursor(ui.CursorPointer)
	if folded {
		row = row.Padding(0, 0).Justify(ui.Center)
	}
	if selected {
		row.Background(t.Surface).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.05))
	} else if row.Hovered() {
		row.Background(t.SurfaceHover)
	}

	labelColor := t.Text
	if selected {
		labelColor = t.Accent
	}

	row.Children(func() {
		bar := ui.Box(c).Size(3, 14).Radius(2)
		if selected {
			bar.Background(t.Accent)
		}
		if item.Icon != nil {
			icon := ui.Icon(c, item.Icon()).FontSize(14).TextColor(labelColor)
			if folded {
				icon.Label(item.Label).Tooltip(item.Label)
			}
		}
		if !folded {
			ui.Text(c, item.Label).FontSize(12).TextColor(labelColor).SingleLine()
		}
	})

	if row.Clicked() || row.Shortcut(0, ui.KeyEnter) || row.Shortcut(0, ui.KeySpace) {
		s.page = item.Path
	}
}

// sidebarToggle folds or unfolds the rail; the choice persists (see
// toggleSidebar). Folded, the chevron alone fills the row.
func (s *uiState) sidebarToggle(c *ui.Context) {
	t := c.Theme()
	folded := s.sidebarCollapsed

	row := ui.Row(c.Key("sidebar-toggle")).Height(30).Radius(7).Gap(spS).
		AlignItems(ui.Center).Focusable().Cursor(ui.CursorPointer)
	if folded {
		row = row.Padding(0, 0).Justify(ui.Center)
	}
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		if folded {
			ui.Icon(c, iconChevronRight()).FontSize(14).TextColor(t.TextMuted).
				Label("展开侧栏").Tooltip("展开侧栏")
			return
		}
		ui.Icon(c, iconChevronLeft()).FontSize(14).TextColor(t.TextMuted)
		ui.Text(c, "收起侧栏").FontSize(11).TextColor(t.TextMuted).SingleLine()
	})
	if row.Clicked() || row.Shortcut(0, ui.KeyEnter) || row.Shortcut(0, ui.KeySpace) {
		s.toggleSidebar()
	}
}

func (s *uiState) sidebarFooter(c *ui.Context) {
	t := c.Theme()
	if s.sidebarCollapsed {
		// The rail keeps the one glanceable bit: the storage dot.
		dot := t.Success
		if s.overview.Degraded {
			dot = t.Warning
		}
		ui.Row(c).Justify(ui.Center).Padding(spS, 0).Children(func() {
			ui.Box(c).Size(6, 6).Radius(3).Background(dot).Tooltip(storageLabel(s.overview.Degraded))
		})
		return
	}
	ui.Column(c).Padding(spL, spM, spL, spM).Gap(spXS).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			dot := t.Success
			if s.overview.Degraded {
				dot = t.Warning
			}
			ui.Box(c).Size(6, 6).Radius(3).Background(dot)
			label := "存储正常"
			if s.overview.Degraded {
				label = "存储降级"
			}
			ui.Text(c, label).FontSize(10).TextColor(t.TextMuted)
		})
		ui.Text(c, s.endpoint()).FontSize(10).TextColor(t.TextMuted).SingleLine()
	})
}

func (s *uiState) header(c *ui.Context) {
	t := c.Theme()
	meta, ok := pageTitles[s.page]
	if !ok {
		meta = pageTitle{"总览", ""}
	}
	ui.Row(c).Padding(spXL, spM, spL, spM).Gap(spM).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, meta.Title).FontSize(20).Bold().LineHeight(1.15)
			ui.Text(c, meta.Subtitle).FontSize(12).TextColor(t.TextMuted)
		})
		ui.Spacer(c)
		if s.status != "" {
			fg, bg := t.Success, t.Success.Alpha(0.12)
			if s.isErr {
				fg, bg = t.Danger, t.Danger.Alpha(0.12)
			}
			pill(c, s.status, fg, bg)
		}
		if s.loading {
			ui.Spinner(c)
		}
		if iconButton(c, iconRefresh(), "刷新").Clicked() {
			s.pageErr = ""
			s.reload()
		}
	})
}

func (s *uiState) pageBody(c *ui.Context) {
	switch s.page {
	case "/logs":
		s.logsPage(c)
	case "/usage":
		s.usagePage(c)
	case "/providers":
		s.providersPage(c)
	case "/models":
		s.modelsPage(c)
	case "/route-models":
		s.routeModelsPage(c)
	case "/credentials":
		s.credentialsPage(c)
	case "/keys":
		s.keysPage(c)
	case "/endpoints":
		s.endpointsPage(c)
	case "/settings":
		s.settingsPage(c)
	case "/about":
		s.aboutPage(c)
	default:
		s.dashboardPage(c)
	}
}

// fmtUptime renders seconds as a compact uptime.
func fmtUptime(seconds int64) string {
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds < 86400:
		return fmt.Sprintf("%dh%02dm", seconds/3600, (seconds%3600)/60)
	default:
		return fmt.Sprintf("%dd%02dh", seconds/86400, (seconds%86400)/3600)
	}
}
