// Package ui is AdaPrune's native management window. It draws with mygo's UI
// toolkit: no webview, no front-end build, one binary.
//
// The package is split by function so a change lands in one small unit:
//
//	run.go        process shell: single instance, listener, window, tray
//	shell.go      page frame: shortcuts, body switch
//	chrome.go     title bar, page header, status footer
//	sidebar.go    navigation rail and its toggle
//	theme.go      "松针 / 纸" light and dark tokens
//	icons.go      24px stroke SVG set
//	components.go shared surfaces: cards, pills, empty states, pager
//	format.go     number, money, duration and mask formatting
//	forms.go      field helpers shared by settings and provider forms
//	dashboard.go  overview: KPI strip, 24h traffic, provider health
//	logs.go       request log: filters, table, pager, detail
//	usage.go      usage: token summaries and trend
//	providers.go  provider list and selected detail
//	provider_form.go provider create/edit dialog
//	models.go     model catalogue cards, forms and discovery
//	routes.go     route-model list and members
//	credentials.go upstream credential table
//	keys.go       client-key table and one-time plaintext
//	endpoints.go  built-in and custom endpoints
//	settings.go   settings categories and sections
//	about.go      version, storage, adapters
//	palette.go    command palette (Ctrl+K)
//
// State lives in state.go (frame state and loading) plus state_*.go for the
// operations of one feature, mirroring the page files.
package ui
