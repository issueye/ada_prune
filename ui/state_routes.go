package ui

import (
	"context"
	"fmt"
	"github.com/learningdog1/ada_prune/internal/admin"
	"github.com/learningdog1/ada_prune/internal/store"
	"strconv"
	"strings"
)

// openRouteForm starts a new route model. Every model starts unselected so the
// operator sees the whole catalogue and picks deliberately.
func (s *uiState) openRouteForm() {
	s.routeFormOpen = true
	s.routeFormIsNew = true
	s.routeFormID = 0
	s.routeFormName = ""
	s.routeFormStrategy = store.StrategyPriority
	s.routeFormRemark = ""
	s.routeFormEnabled = true
	s.routeFormError = ""
	s.routeFormSaving = false
	s.routeFormMembers = make(map[int64]*routeMemberDraft, len(s.models))
	for _, m := range s.models {
		s.routeFormMembers[m.ID] = &routeMemberDraft{Priority: "0", Weight: 1, Enabled: true}
	}
}

// openEditRoute fills the form from an existing route, preserving member order
// priority, weight and each member's enabled flag.
func (s *uiState) openEditRoute(index int) {
	rows := s.routeModels
	if index < 0 || index >= len(rows) {
		return
	}
	r := rows[index]
	s.routeFormOpen = true
	s.routeFormIsNew = false
	s.routeFormID = r.ID
	s.routeFormName = r.Name
	s.routeFormStrategy = r.Strategy
	s.routeFormRemark = r.Remark
	s.routeFormEnabled = r.Enabled
	s.routeFormError = ""
	s.routeFormSaving = false
	s.routeFormMembers = make(map[int64]*routeMemberDraft, len(s.models))
	for _, m := range s.models {
		s.routeFormMembers[m.ID] = &routeMemberDraft{Priority: "0", Weight: 1, Enabled: true}
	}
	for _, member := range r.Members {
		draft, ok := s.routeFormMembers[member.ModelID]
		if !ok {
			continue
		}
		draft.Selected = true
		draft.Priority = strconv.Itoa(member.Priority)
		draft.Weight = member.Weight
		draft.Enabled = member.Enabled
	}
}

// routeMemberCount counts the selected members of the open form.
func (s *uiState) routeMemberCount() int {
	count := 0
	for _, draft := range s.routeFormMembers {
		if draft.Selected {
			count++
		}
	}
	return count
}

// saveRouteForm validates the draft and creates or updates the route model.
func (s *uiState) saveRouteForm() {
	if s.routeFormSaving {
		return
	}
	name := strings.TrimSpace(s.routeFormName)
	if name == "" {
		s.routeFormError = "路由模型 ID 不能为空"
		return
	}

	members := make([]admin.RouteMemberInput, 0, len(s.routeFormMembers))
	for _, m := range s.models {
		draft := s.routeFormMembers[m.ID]
		if draft == nil || !draft.Selected {
			continue
		}
		priority, err := parseOptionalInt("优先级", draft.Priority)
		if err != nil {
			s.routeFormError = fmt.Sprintf("%s（%s）", err, m.PublicName)
			return
		}
		weight := draft.Weight
		if weight <= 0 {
			weight = 1
		}
		enabled := draft.Enabled
		members = append(members, admin.RouteMemberInput{
			ModelID:  m.ID,
			Priority: priority,
			Weight:   weight,
			Enabled:  &enabled,
		})
	}
	if len(members) == 0 {
		s.routeFormError = "至少选择一个成员模型"
		return
	}

	strategy := s.routeFormStrategy
	if strategy == "" {
		strategy = store.StrategyPriority
	}
	enabled := s.routeFormEnabled
	in := admin.RouteModelInput{
		Name:     name,
		Strategy: strategy,
		Enabled:  &enabled,
		Remark:   strings.TrimSpace(s.routeFormRemark),
		Members:  members,
	}

	isNew, id := s.routeFormIsNew, s.routeFormID
	s.routeFormError = ""
	s.routeFormSaving = true
	go func() {
		var saveErr error
		if isNew {
			_, saveErr = s.app.Admin.CreateRouteModel(context.Background(), in)
		} else {
			_, saveErr = s.app.Admin.UpdateRouteModel(context.Background(), id, in)
		}
		if s.win == nil {
			return
		}
		s.win.Update(func() {
			s.routeFormSaving = false
			if saveErr != nil {
				s.routeFormError = saveErr.Error()
				return
			}
			s.routeFormOpen = false
			label := "路由模型已创建："
			if !isNew {
				label = "路由模型已保存："
			}
			s.flash(label+name, false)
			s.reload()
		})
	}()
}

// routeInput rebuilds an update payload from a stored route, changing only the
// enabled flag. Member enabled states and weights are preserved.
func (s *uiState) routeInput(r store.RouteModel, enabled bool) admin.RouteModelInput {
	members := make([]admin.RouteMemberInput, 0, len(r.Members))
	for _, m := range r.Members {
		memberEnabled := m.Enabled
		members = append(members, admin.RouteMemberInput{
			ModelID:  m.ModelID,
			Priority: m.Priority,
			Weight:   m.Weight,
			Enabled:  &memberEnabled,
		})
	}
	return admin.RouteModelInput{
		Name:     r.Name,
		Strategy: r.Strategy,
		Enabled:  &enabled,
		Remark:   r.Remark,
		Members:  members,
	}
}

func (s *uiState) toggleRoute(index int, enabled bool) {
	rows := s.routeModels
	if index < 0 || index >= len(rows) {
		return
	}
	in := s.routeInput(rows[index], enabled)
	id := rows[index].ID
	s.run("更新路由模型", func() error {
		_, err := s.app.Admin.UpdateRouteModel(context.Background(), id, in)
		return err
	})
}

func (s *uiState) deleteRoute(index int) {
	rows := s.routeModels
	if index < 0 || index >= len(rows) {
		return
	}
	id := rows[index].ID
	name := rows[index].Name
	s.confirm("删除路由模型 "+name+"？",
		"客户端请求这个 ID 的流量将立即失去路由，成员关系随级联删除。此操作不可撤销。",
		"删除", func() {
			s.run("删除路由模型", func() error {
				return s.app.Admin.DeleteRouteModel(context.Background(), id)
			})
		})
}

// routeRoutable reports whether any enabled member can serve right now: the
// model is enabled, its provider is enabled and it has an active credential.
func (s *uiState) routeRoutable(r store.RouteModel) bool {
	if !r.Enabled {
		return false
	}
	routable := make(map[int64]bool, len(s.models))
	for _, m := range s.models {
		routable[m.ID] = m.Routable
	}
	for _, member := range r.Members {
		if member.Enabled && routable[member.ModelID] {
			return true
		}
	}
	return false
}
