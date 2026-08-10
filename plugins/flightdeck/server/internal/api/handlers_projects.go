package api

import "net/http"

// handleListProjects 는 프로젝트 요약 전부를 낸다.
//
// ★ 읽기 전용이라 화면 쓰기 사슬을 안 탄다. 이 경로가 내는 것은 등록된 프로젝트와 그 실적뿐이고,
// 그것을 읽을 자격은 이미 게이트가 판정했다.
func (s *server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.ListProjectSummaries(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"projects": list})
}

// handleRemoveProject 는 프로젝트를 지운다. **되돌릴 수 없다.** 사유가 필수고, confirm 이
// 없으면 세기만 한다 — 그 두 안전판은 service.RemoveProject 가 지킨다(여기서 다시 판정하지
// 않는다. 두 벌이 되면 반드시 표류한다).
func (s *server) handleRemoveProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Actor   string `json:"actor"`
		Reason  string `json:"reason"`
		Confirm bool   `json:"confirm"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	res, err := s.svc.RemoveProject(r.Context(), r.PathValue("id"), req.Actor, req.Reason, req.Confirm)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, res)
}
