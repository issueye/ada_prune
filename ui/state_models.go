package ui

import (
	"context"
	"fmt"
	"github.com/learningdog1/ada_prune/internal/admin"
	"math"
	"strconv"
	"strings"
)

// filterModels applies the search box locally: the page holds the whole
// catalogue, so typing filters in memory with no round trip.
func (s *uiState) filterModels() {
	query := strings.ToLower(strings.TrimSpace(s.modelQuery))
	if query == "" {
		s.models = s.modelsAll
		return
	}
	out := make([]admin.ModelView, 0, len(s.modelsAll))
	for _, m := range s.modelsAll {
		if strings.Contains(strings.ToLower(m.PublicName), query) ||
			strings.Contains(strings.ToLower(m.UpstreamName), query) {
			out = append(out, m)
		}
	}
	s.models = out
}

func (s *uiState) toggleModel(index int, enabled bool) {
	rows := s.models
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	s.run("更新模型", func() error {
		return s.app.Admin.SetModelEnabled(context.Background(), id, enabled)
	})
}

func (s *uiState) deleteModel(index int) {
	rows := s.models
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	name := rows[index].PublicName
	s.confirm("删除模型 "+name+"？",
		"客户端将无法再用这个模型 ID 发起请求。若它仍是某个路由模型的成员，删除会被服务端拒绝。此操作不可撤销。",
		"删除", func() {
			s.run("删除模型", func() error {
				return s.app.Admin.DeleteModel(context.Background(), id)
			})
		})
}

// openModelForm resets and opens the manual add dialog, preselecting the
// provider the dialog used last (or the first one).
func (s *uiState) openModelForm() {
	s.modelFormOpen = true
	s.modelFormIsNew = true
	s.modelFormID = 0
	s.modelFormError = ""
	s.modelFormSaving = false
	s.modelFormUpstream = ""
	s.modelFormDisplay = ""
	s.modelFormPublicID = ""
	s.modelFormProtocol = ""
	s.modelFormContext = ""
	s.modelFormMaxTokens = ""
	s.modelFormInputPrice = ""
	s.modelFormCachePrice = ""
	s.modelFormOutputPrice = ""
	s.modelFormVision = false
	s.modelFormTools = true
	s.modelFormEnabled = true
	if len(s.providers) == 0 {
		return
	}
	for _, p := range s.providers {
		if p.ID == s.modelFormProviderID {
			return
		}
	}
	s.modelFormProviderID = s.providers[0].ID
}

// openEditModel fills the dialog from the chosen card. The public id is
// preserved on save, so clients holding that id keep routing to the model.
func (s *uiState) openEditModel(index int) {
	rows := s.models
	if index < 0 || index >= len(rows) {
		return
	}
	m := rows[index]
	s.modelFormOpen = true
	s.modelFormIsNew = false
	s.modelFormID = m.ID
	s.modelFormProviderID = m.ProviderID
	s.modelFormError = ""
	s.modelFormSaving = false
	s.modelFormUpstream = m.UpstreamName
	s.modelFormDisplay = m.DisplayName
	s.modelFormPublicID = m.PublicName
	s.modelFormProtocol = m.UpstreamProtocol
	s.modelFormContext = numberText(float64(m.ContextWindow))
	s.modelFormMaxTokens = numberText(float64(m.MaxTokens))
	s.modelFormInputPrice = numberText(m.InputPrice)
	s.modelFormCachePrice = numberText(m.CachedInputPrice)
	s.modelFormOutputPrice = numberText(m.OutputPrice)
	s.modelFormVision = m.SupportsVision
	s.modelFormTools = m.SupportsTools
	s.modelFormEnabled = m.Enabled
}

// modelFormProvider returns the provider chosen in the dialog.
func (s *uiState) modelFormProvider() *admin.ProviderView {
	for i := range s.providers {
		if s.providers[i].ID == s.modelFormProviderID {
			return &s.providers[i]
		}
	}
	return nil
}

// modelFormProviderName names the provider chosen in the dialog.
func (s *uiState) modelFormProviderName() string {
	if p := s.modelFormProvider(); p != nil {
		return p.Name
	}
	return ""
}

// modelFormPublicName is the id the dialog previews: the provider-namespaced
// upstream name, exactly what a client sends back in the model field. Editing
// keeps the stored id instead of recomputing it.
func (s *uiState) modelFormPublicName() string {
	if !s.modelFormIsNew && s.modelFormPublicID != "" {
		return s.modelFormPublicID
	}
	upstream := strings.TrimSpace(s.modelFormUpstream)
	p := s.modelFormProvider()
	if p == nil {
		return upstream
	}
	return p.Name + "/" + upstream
}

// saveModelForm creates a new model or updates the edited one.
func (s *uiState) saveModelForm() {
	if s.modelFormSaving {
		return
	}
	upstream := strings.TrimSpace(s.modelFormUpstream)
	if upstream == "" {
		s.modelFormError = "上游模型名不能为空"
		return
	}
	contextWindow, err := parseOptionalInt("上下文", s.modelFormContext)
	if err != nil {
		s.modelFormError = err.Error()
		return
	}
	maxTokens, err := parseOptionalInt("最大输出", s.modelFormMaxTokens)
	if err != nil {
		s.modelFormError = err.Error()
		return
	}
	inputPrice, err := parseOptionalPrice("输入价格", s.modelFormInputPrice)
	if err != nil {
		s.modelFormError = err.Error()
		return
	}
	cachePrice, err := parseOptionalPrice("缓存价格", s.modelFormCachePrice)
	if err != nil {
		s.modelFormError = err.Error()
		return
	}
	outputPrice, err := parseOptionalPrice("输出价格", s.modelFormOutputPrice)
	if err != nil {
		s.modelFormError = err.Error()
		return
	}

	p := s.modelFormProvider()
	if p == nil {
		s.modelFormError = "还没有供应商，请先新建供应商"
		return
	}

	vision, tools, enabled := s.modelFormVision, s.modelFormTools, s.modelFormEnabled
	in := admin.ModelInput{
		ProviderID:       p.ID,
		DisplayName:      strings.TrimSpace(s.modelFormDisplay),
		UpstreamName:     upstream,
		ContextWindow:    contextWindow,
		MaxTokens:        maxTokens,
		InputPrice:       inputPrice,
		CachedInputPrice: cachePrice,
		OutputPrice:      outputPrice,
		SupportsVision:   &vision,
		SupportsTools:    &tools,
		Enabled:          &enabled,
	}
	if s.modelFormIsNew {
		// Only the three expressible protocols are sent; a vendor adapter leaves
		// it empty so the service falls back to vendor_native (same as import).
		switch proto := protocolOf(*p); proto {
		case protocolAnthropic, protocolOpenAIChat, protocolOpenAIResponses:
			in.UpstreamProtocol = proto
		}
	} else {
		// Preserve the id and the protocol the model was created with.
		in.PublicName = s.modelFormPublicID
		in.UpstreamProtocol = s.modelFormProtocol
	}

	isNew, id := s.modelFormIsNew, s.modelFormID
	s.modelFormError = ""
	s.modelFormSaving = true
	go func() {
		var publicName string
		var saveErr error
		if isNew {
			created, err := s.app.Admin.CreateModel(context.Background(), in)
			saveErr, publicName = err, created.PublicName
		} else {
			updated, err := s.app.Admin.UpdateModel(context.Background(), id, in)
			saveErr, publicName = err, updated.PublicName
		}
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			s.modelFormSaving = false
			if saveErr != nil {
				s.modelFormError = saveErr.Error()
				return
			}
			s.modelFormOpen = false
			label := "模型已添加："
			if !isNew {
				label = "模型已保存："
			}
			s.flash(label+publicName, false)
			s.modelFormUpstream = ""
			s.modelFormDisplay = ""
			s.reload()
		})
	}()
}

// parseOptionalInt reads a blank-or-number field; empty means zero.
func parseOptionalInt(field, raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s必须是不小于 0 的整数", field)
	}
	return value, nil
}

// parseOptionalPrice reads a blank-or-number price; empty means zero.
func parseOptionalPrice(field, raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%s必须是不小于 0 的数字", field)
	}
	return value, nil
}

// numberText renders a number for an editable text field: zero is blank.
func numberText(v float64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}
