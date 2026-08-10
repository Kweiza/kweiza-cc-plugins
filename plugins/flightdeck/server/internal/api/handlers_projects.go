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
