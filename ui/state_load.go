package ui

import (
	"context"
	"github.com/learningdog1/ada_prune/internal/admin"
	"github.com/learningdog1/ada_prune/internal/store"
	"strconv"
)

// reload fetches the current page with the header spinner.
func (s *uiState) reload() { s.load(true) }

// reloadQuiet refreshes without touching the spinner: a poll that blinks reads as
// flicker rather than progress.
// reloadQuiet refreshes without touching the spinner: a poll that blinks reads as
// flicker rather than progress.
func (s *uiState) reloadQuiet() { s.load(false) }

func (s *uiState) load(showSpinner bool) {
	if s.win == nil {
		return
	}
	if showSpinner {
		s.loading = true
	}
	// Only the newest load may apply. A filter change must never be swallowed
	// because an older load is still in flight, and a slow answer for a page
	// or filter the user already left must not overwrite fresher data.
	s.loadSeq++
	seq := s.loadSeq
	page := s.page
	go func() {
		f, err := s.fetch(page)
		// Every mutation of the state happens on the main thread.
		s.win.Update(func() {
			if seq != s.loadSeq {
				return
			}
			if showSpinner {
				s.loading = false
			}
			if err != nil {
				s.pageErr = err.Error()
				s.flash("加载失败", true)
				return
			}
			s.pageErr = ""
			s.apply(page, f)
		})
	}()
}

// refresh loads synchronously. The view test calls it without a window.
func (s *uiState) refresh() error {
	f, err := s.fetch(s.page)
	if err != nil {
		s.pageErr = err.Error()
		return err
	}
	s.pageErr = ""
	s.apply(s.page, f)
	return nil
}

func (s *uiState) fetch(page string) (fetched, error) {
	var f fetched
	ctx := context.Background()

	ov, err := s.app.Admin.Overview(ctx)
	if err != nil {
		return f, err
	}
	f.overview = &ov

	st, err := s.app.Admin.Status(ctx)
	if err == nil {
		f.status = &st
	}
	// The adapter catalogue drives the provider form's kind list, so every page
	// that can open the form needs it. It lands with the fetched payload and is
	// applied on the main thread, like every other state write.
	if descriptors, adapterErr := s.app.Admin.Adapters(ctx); adapterErr == nil {
		f.adapters = descriptors
	}

	switch page {
	case "/":
		// The dashboard shows provider health as well as the aggregates.
		rows, err := s.app.Admin.ListProviders(ctx)
		if err != nil {
			return f, err
		}
		f.lists = &lists{providers: rows}

	case "/logs":
		page := s.logPage
		size := s.logPageSize
		if page < 1 {
			page = 1
		}
		if size < 1 {
			size = 20
		}
		q := admin.LogsQuery{Limit: size, Offset: (page - 1) * size}
		if s.logEndpoint != "" && s.logEndpoint != "全部" {
			q.Endpoint = s.logEndpoint
		}
		if s.logStatus != "" && s.logStatus != "全部" {
			q.Status = s.logStatus
		}
		pageData, err := s.app.Admin.Logs(ctx, q)
		if err != nil {
			return f, err
		}
		// The log can shrink under the reader (retention cleanup): an offset
		// past the end lands on the last page that still has rows, so the
		// user sees history instead of an empty page.
		if len(pageData.Rows) == 0 && pageData.Total > 0 && int64(q.Offset) >= pageData.Total {
			page = int((pageData.Total + int64(size) - 1) / int64(size))
			q.Offset = (page - 1) * size
			pageData, err = s.app.Admin.Logs(ctx, q)
			if err != nil {
				return f, err
			}
			f.logPageFix = page
		}
		f.lists = &lists{logs: pageData}

	case "/usage":
		group := store.UsageGroup(s.usageGroup)
		from := s.usageFrom()
		result, err := s.app.Admin.Usage(ctx, admin.UsageQuery{From: from, GroupBy: group})
		if err != nil {
			return f, err
		}
		// The token trend always needs the daily buckets, whatever the table
		// groups by.
		daily, err := s.app.Admin.Usage(ctx, admin.UsageQuery{From: from, GroupBy: store.GroupByDay})
		if err != nil {
			return f, err
		}
		f.lists = &lists{usage: result, usageDaily: daily}

	case "/providers":
		rows, err := s.app.Admin.ListProviders(ctx)
		if err != nil {
			return f, err
		}
		f.lists = &lists{providers: rows}

	case "/models":
		// The whole catalogue loads once; the search box filters it locally, so
		// typing never waits on the store and never shows a spinner.
		rows, err := s.app.Admin.ListModels(ctx, admin.ModelFilterInput{})
		if err != nil {
			return f, err
		}
		// The discovery dialog picks among providers, so the page needs them too.
		provs, err := s.app.Admin.ListProviders(ctx)
		if err != nil {
			return f, err
		}
		f.lists = &lists{models: rows, providers: provs}

	case "/route-models":
		// The form picks members from every provider, so the page loads the
		// whole model catalogue alongside the routes.
		routes, err := s.app.Admin.ListRouteModels(ctx)
		if err != nil {
			return f, err
		}
		models, err := s.app.Admin.ListModels(ctx, admin.ModelFilterInput{})
		if err != nil {
			return f, err
		}
		f.lists = &lists{routeModels: routes, models: models}

	case "/credentials":
		rows, err := s.app.Admin.ListCredentials(ctx, 0)
		if err != nil {
			return f, err
		}
		f.lists = &lists{credentials: rows}

	case "/keys":
		rows, err := s.app.Admin.ListClientKeys(ctx)
		if err != nil {
			return f, err
		}
		f.lists = &lists{clientKeys: rows}

	case "/endpoints":
		rows, err := s.app.Admin.ListEndpoints(ctx)
		if err != nil {
			return f, err
		}
		f.lists = &lists{endpoints: rows}

	case "/settings":
		view, err := s.app.Admin.Settings(ctx)
		if err != nil {
			return f, err
		}
		f.settings = &view
	}
	return f, nil
}

func (s *uiState) apply(page string, f fetched) {
	if f.overview != nil {
		s.overview = *f.overview
	}
	if f.status != nil {
		s.status2 = *f.status
	}
	if f.adapters != nil {
		s.adapters = f.adapters
	}
	if f.settings != nil {
		s.settings = *f.settings
		s.portText = strconv.Itoa(f.settings.Port)
		s.hostText = f.settings.Host
		s.levelText = f.settings.LogLevel
		s.retentionText = strconv.Itoa(f.settings.LogRetentionDays)
		s.maxInflightTxt = strconv.FormatInt(f.settings.MaxInflight, 10)
		s.defaultModel = f.settings.DefaultModel
		// The watchdogs display in seconds; the config stores milliseconds.
		s.streamIdleText = strconv.FormatInt(f.settings.StreamIdleMs/1000, 10)
		s.nonStreamIdleText = strconv.FormatInt(f.settings.NonStreamIdleMs/1000, 10)
		if s.settingsSection == "" {
			s.settingsSection = "服务"
		}
	}
	if f.lists == nil {
		return
	}
	switch page {
	case "/":
		s.providers = f.lists.providers
	case "/logs":
		// The fetch may have corrected an out-of-range page (rows deleted
		// under the reader): adopt the page the rows actually came from.
		if f.logPageFix > 0 {
			s.logPage = f.logPageFix
		}
		// The 5s poll must not cancel the row the user is reading: match the
		// selection by request id, and drop it only when the row is gone.
		selected := int64(-1)
		if s.logSel >= 0 && s.logSel < len(s.logs.Rows) {
			selected = s.logs.Rows[s.logSel].ID
		}
		s.logs = f.lists.logs
		s.logSel = -1
		if selected >= 0 {
			for i, row := range s.logs.Rows {
				if row.ID == selected {
					s.logSel = i
					break
				}
			}
		}
	case "/usage":
		s.usage = f.lists.usage
		s.usageDaily = f.lists.usageDaily
	case "/providers":
		s.providers = f.lists.providers
		if s.providerSel >= len(s.providers) {
			s.providerSel = -1
		}
	case "/models":
		s.modelsAll = f.lists.models
		s.filterModels()
		s.providers = f.lists.providers
	case "/route-models":
		s.routeModels = f.lists.routeModels
		s.models = f.lists.models
		s.modelsAll = f.lists.models
	case "/credentials":
		s.credentials = f.lists.credentials
		if s.credSel >= len(s.credentials) {
			s.credSel = -1
		}
	case "/keys":
		s.clientKeys = f.lists.clientKeys
		if s.keySel >= len(s.clientKeys) {
			s.keySel = -1
		}
	case "/endpoints":
		s.endpoints = f.lists.endpoints
	}
}
