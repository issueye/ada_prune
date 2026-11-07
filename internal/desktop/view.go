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
	})
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

// endpoint renders the listener address.
func (s *uiState) endpoint() string {
	host := s.overview.Host
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d", host, s.overview.Port)
}

func (s *uiState) sidebar(c *ui.Context) {
	t := c.Theme()
	previous := s.page

	ui.Column(c).Width(206).FillHeight().Background(t.Background).Children(func() {
		ui.Column(c).Padding(spS, 10, spS, 10).Gap(1).Children(func() {
			for _, group := range navGroups {
				ui.Text(c, group.Title).FontSize(10).TextColor(t.TextMuted).Padding(10, 8, spXS, 10)
				for _, item := range group.Items {
					s.navRow(c, item)
				}
				ui.Box(c).Height(spS)
			}
		}).Grow(1)

		ui.Divider(c)
		s.sidebarFooter(c)
	})

	if s.page != previous {
		s.onPageChange()
	}
}

// navRow is one sidebar item: a surface pill with an accent bar when chosen.
func (s *uiState) navRow(c *ui.Context, item navItem) {
	t := c.Theme()
	selected := s.page == item.Path

	row := ui.Row(c.Key(item.Path)).Padding(0, 10).Height(30).Radius(7).
		Gap(spS).AlignItems(ui.Center).Focusable()
	if selected {
		row.Background(t.Surface).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.05))
	} else if row.Hovered() {
		row.Background(t.SurfaceHover)
	}

	row.Children(func() {
		bar := ui.Box(c).Size(3, 14).Radius(2)
		if selected {
			bar.Background(t.Accent)
		}
		labelColor := t.Text
		if selected {
			labelColor = t.Accent
		}
		ui.Text(c, item.Label).FontSize(12).TextColor(labelColor).SingleLine()
	})

	if row.Clicked() {
		s.page = item.Path
	}
}

func (s *uiState) sidebarFooter(c *ui.Context) {
	t := c.Theme()
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
		if ui.Button(c, "刷新").Clicked() {
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
