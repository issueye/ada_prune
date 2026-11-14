package ui

import (
	"sync"

	gui "github.com/egoist/mygo/ui"
)

// The icon set: 24×24 stroke SVGs in the feather style, drawn in the
// currentColor of the text beside them (gui.Icon recolors every shape). Each
// parse runs once; the SVG sources are part of the program, never input.
//
// The field hints use the 16-px infoIcon in widgets.go; everything here is a
// navigation or action glyph, sized one step above the label it accompanies.

// strokeIcon wraps a feather-style path set into a full SVG document.
func strokeIcon(body string) []byte {
	return []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none"` +
		` stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` +
		body + `</svg>`)
}

// iconDashboard is the overview page: four quadrants.
var iconDashboard = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/>` +
			`<rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>`))
})

// iconLogs is the request log: lines with bullets.
var iconLogs = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/>` +
			`<line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/>` +
			`<line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/>`))
})

// iconUsage is consumption: three bars.
var iconUsage = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<line x1="18" y1="20" x2="18" y2="10"/><line x1="12" y1="20" x2="12" y2="4"/>` +
			`<line x1="6" y1="20" x2="6" y2="14"/>`))
})

// iconProviders is an upstream service: two server racks.
var iconProviders = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/>` +
			`<line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/>`))
})

// iconModels is a model: a 3D box.
var iconModels = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/>` +
			`<polyline points="3.27 6.96 12 12.01 20.73 6.96"/><line x1="12" y1="22.08" x2="12" y2="12"/>`))
})

// iconRoutes is an aggregated route: a branching path.
var iconRoutes = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<line x1="6" y1="3" x2="6" y2="15"/><circle cx="18" cy="6" r="3"/>` +
			`<circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>`))
})

// iconCredentials is an upstream key: a key.
var iconCredentials = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4"/>`))
})

// iconKeys is a downstream key: a shield.
var iconKeys = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>`))
})

// iconEndpoints is a data-plane entry: a chain link.
var iconEndpoints = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>` +
			`<path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>`))
})

// iconSettings is configuration: three sliders.
var iconSettings = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<line x1="4" y1="21" x2="4" y2="14"/><line x1="4" y1="10" x2="4" y2="3"/>` +
			`<line x1="12" y1="21" x2="12" y2="12"/><line x1="12" y1="8" x2="12" y2="3"/>` +
			`<line x1="20" y1="21" x2="20" y2="16"/><line x1="20" y1="12" x2="20" y2="3"/>` +
			`<line x1="1" y1="14" x2="7" y2="14"/><line x1="9" y1="8" x2="15" y2="8"/>` +
			`<line x1="17" y1="16" x2="23" y2="16"/>`))
})

// iconAbout is the about page: a circled i.
var iconAbout = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/>` +
			`<line x1="12" y1="8" x2="12.01" y2="8"/>`))
})

// iconClose dismisses a panel: a cross.
var iconClose = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(`<line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>`))
})

// iconTheme toggles the light/night palette: a half-filled circle.
var iconTheme = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<circle cx="12" cy="12" r="9"/><path d="M12 3a9 9 0 0 1 0 18z" fill="currentColor" stroke="none"/>`))
})

// iconRefresh is reload: two circular arrows.
var iconRefresh = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/>` +
			`<path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/>`))
})

// iconPlus is create: a cross.
var iconPlus = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>`))
})

// iconDownload is import from upstream: an arrow into a tray.
var iconDownload = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/>` +
			`<line x1="12" y1="15" x2="12" y2="3"/>`))
})

// iconCopy is copy to clipboard: two offset rectangles.
var iconCopy = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>`))
})

// iconSearch is filter: a magnifier.
var iconSearch = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(
		`<circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>`))
})

// iconChevronLeft points left: collapse the sidebar.
var iconChevronLeft = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(`<polyline points="15 18 9 12 15 6"/>`))
})

// iconChevronRight points right: expand the sidebar.
var iconChevronRight = sync.OnceValue(func() *gui.SVG {
	return gui.MustParseSVG(strokeIcon(`<polyline points="9 18 15 12 9 6"/>`))
})
