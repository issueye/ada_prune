package ui

import (
	"fmt"
	gui "github.com/egoist/mygo/ui"
)

// view builds the window: the title bar the app draws itself, the sidebar with
// the navigation, the page header, the page body, the status footer and any open
// dialog.
// view builds the window: the title bar the app draws itself, the sidebar with
// the navigation, the page header, the page body, the status footer and any open
// dialog.
func (s *uiState) view(c *gui.Context) {
	c.SetTheme(appTheme(s.dark))
	t := c.Theme()
	s.registerShortcuts(c)
	gui.Column(c).Fill().Background(t.Background).Children(func() {
		s.titleBar(c)
		gui.Divider(c)
		gui.Row(c).Grow(1).AlignItems(gui.Stretch).Children(func() {
			s.sidebar(c)
			gui.Divider(c)
			gui.Column(c).Grow(1).FillHeight().Children(func() {
				s.header(c)
				s.pageBody(c)
			})
		})
		gui.Divider(c)
		s.footer(c)

		// The provider form is a modal so it can be opened from anywhere and
		// cannot be confused with the page behind it. Escape or a click outside
		// closes it (mygo clears the flag).
		gui.Modal(c, &s.formOpen, func() {
			s.providerDialog(c)
		})
		gui.Modal(c, &s.modelFormOpen, func() {
			s.modelDialog(c)
		})
		gui.Modal(c, &s.discoverOpen, func() {
			s.discoverDialog(c)
		})
		gui.Modal(c, &s.routeFormOpen, func() {
			s.routeDialog(c)
		})
		gui.Modal(c, &s.paletteOpen, func() {
			s.paletteDialog(c)
		})

		// The destructive-action confirmation sits above everything, including
		// the forms it may be opened from. Escape or 取消 drops the action.
		choice := gui.AlertDialog(c, &s.confirmOpen, s.confirmTitle, s.confirmMessage,
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
// registerShortcuts wires the window-level keys: refresh, page jumps by number
// and the command palette. They yield while a dialog covers the window, so a
// stray chord never acts on the page underneath.
func (s *uiState) registerShortcuts(c *gui.Context) {
	c.OnShortcut(gui.Cmd, gui.KeyR, func() {
		if s.dialogOpen() {
			return
		}
		s.pageErr = ""
		s.reload()
	})
	c.OnShortcut(gui.Cmd, gui.KeyK, func() {
		if s.dialogOpen() {
			return
		}
		s.paletteQuery = ""
		s.paletteOpen = true
	})
	// The sidebar toggle lives on Cmd+\ : Cmd+B collides with the text editor's
	// emacs bindings (B is caret-left), which would swallow it while typing.
	c.OnShortcut(gui.Cmd, gui.KeyBackslash, func() {
		if s.dialogOpen() {
			return
		}
		s.toggleSidebar()
	})
	for i, item := range navItems() {
		if i >= 9 {
			break
		}
		key := gui.Key1 + gui.Key(i)
		target := item.Path
		c.OnShortcut(gui.Cmd, key, func() {
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

// titleBar replaces the OS title bar: brand and version on the left. It stays
// deliberately empty otherwise — run state and counts belong to the footer.
// The window's own controls float above it, so the padding comes from the room
// they take.

// endpoint renders the listener address. The actually bound host/port wins:
// the configured port can drift when the desktop shell scans for a free one,
// and the UI must never advertise an address that does not answer.

// navRow is one sidebar item: a surface pill with an accent bar when chosen.
// Enter or Space activates the focused row, so the sidebar works without a
// pointer. Folded, the icon alone carries the page — named for the reader and
// screen readers, with the label as a tooltip.

// sidebarToggle folds or unfolds the rail; the choice persists (see
// toggleSidebar). Folded, the chevron alone fills the row.

func (s *uiState) pageBody(c *gui.Context) {
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
