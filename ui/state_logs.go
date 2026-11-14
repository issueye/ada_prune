package ui

// logTotalPages is the page count of the current log filter and page size, at
// least one.
func (s *uiState) logTotalPages() int {
	size := s.logPageSize
	if size < 1 {
		size = 20
	}
	pages := int((s.logs.Total + int64(size) - 1) / int64(size))
	if pages < 1 {
		return 1
	}
	return pages
}

// setLogPageSize adopts a chosen page size and returns to the first page, so
// the new window always opens on the newest rows.
func (s *uiState) setLogPageSize(size int) {
	if size < 1 || size == s.logPageSize {
		return
	}
	s.logPageSize = size
	s.logPage = 1
	s.logSel = -1
	s.reload()
}

// moveLogPage flips to the given log page (1-based) and reloads. The pager
// disables out-of-range moves, but the clamp stays as a guard.
func (s *uiState) moveLogPage(page int) {
	if page < 1 {
		page = 1
	}
	if max := s.logTotalPages(); page > max {
		page = max
	}
	if page == s.logPage {
		return
	}
	s.logPage = page
	s.logSel = -1
	s.reload()
}
