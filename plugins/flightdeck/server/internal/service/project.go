package service

import (
	"context"
	"time"
)

// ProjectSummary 는 프로젝트 하나의 상태와 실적이다.
// 사람이 이 표를 보고 무엇을 보관하고 무엇을 지울지 정한다.
type ProjectSummary struct {
	ID            string    `json:"id"`
	Path          string    `json:"path"`
	Pinned        bool      `json:"pinned"`
	Archived      bool      `json:"archived"`
	Items         int       `json:"items"`
	Sessions      int       `json:"sessions"`
	Judgments     int       `json:"judgments"`
	Events        int       `json:"events"`
	LastSessionAt time.Time `json:"last_session_at"`
}

// ListProjectSummaries 는 전 프로젝트의 요약이다.
//
// ★ 프로젝트 수는 사람이 등록한 만큼이라(store.ListProjects 의 그 주석) 프로젝트당
// 질의 몇 개는 감당한다. 이 경로는 화면 머리가 아니라 CLI 와 REST 전용이다 —
// 매 렌더 도는 자리에 두면 프로젝트가 늘수록 화면 전체가 느려진다.
func (s *Service) ListProjectSummaries(ctx context.Context) ([]ProjectSummary, error) {
	projects, err := s.st.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectSummary, 0, len(projects))
	for _, p := range projects {
		counts, err := s.st.ProjectRefCounts(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		last, err := s.st.LastSessionAt(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, ProjectSummary{
			ID: p.ID, Path: p.Path,
			Pinned: !p.PinnedAt.IsZero(), Archived: !p.ArchivedAt.IsZero(),
			Items: counts["item"], Sessions: counts["session"],
			Judgments: counts["judgment"], Events: counts["event"],
			LastSessionAt: last,
		})
	}
	return out, nil
}
