package service

import (
	"context"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/store"
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

// ProjectRemoval 은 삭제 요청의 결과다. **세기만 한 경우와 지운 경우가 같은 타입이다** —
// 두 타입으로 가르면 CLI 가 둘을 각자 그리게 되고 문구가 갈린다.
type ProjectRemoval struct {
	Project string         `json:"project"`
	Counts  map[string]int `json:"counts"`
	Removed bool           `json:"removed"`
	Refusal string         `json:"refusal,omitempty"`
}

// RemoveProject 는 프로젝트를 지운다. confirm 이 false 면 세기만 한다.
//
// ★ 세는 것과 지우는 것이 같은 함수인 이유: 다른 함수로 세면 세고 나서 지우기 전에 바뀐
// 것을 못 본다. 같은 자리에서 세고 판정하고 지운다.
func (s *Service) RemoveProject(ctx context.Context, id, actor, reason string,
	confirm bool) (ProjectRemoval, error) {

	if strings.TrimSpace(reason) == "" {
		return ProjectRemoval{}, &RefusedError{What: "project remove",
			Reason:   "사유가 비었다",
			Guidance: "되돌릴 수 없는 삭제다 — 왜 지우는지를 적어라. 원장에 남는다."}
	}
	counts, err := s.st.ProjectRefCounts(ctx, id)
	if err != nil {
		return ProjectRemoval{}, err
	}
	out := ProjectRemoval{Project: id, Counts: counts}
	if ok, why := store.JudgeProjectRemoval(counts); !ok {
		out.Refusal = why
		return out, nil
	}
	if !confirm {
		out.Refusal = "확인이 없다 — 무엇이 함께 지워질지 위에 있다. 지우려면 --yes 를 붙여라"
		return out, nil
	}
	if err := s.st.RemoveProject(ctx, id); err != nil {
		return ProjectRemoval{}, err
	}
	s.st.LogEvent(ctx, "project.removed", id, "", map[string]any{
		"actor": clip(actor, 120), "reason": clip(reason, 400), "counts": counts,
	})
	out.Removed = true
	return out, nil
}
