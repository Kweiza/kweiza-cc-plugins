package service

import (
	"context"
	"errors"
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
//
// ★ 이 함수의 판정 하나만으로는 부족하다 — 이 함수와 s.st.RemoveProject 는 **서로 다른
// 트랜잭션**이라 그 사이 원장이 바뀔 수 있다(리뷰 #1). store.RemoveProject 는 자기
// 트랜잭션 안에서 같은 순수 함수(store.JudgeProjectRemoval)를 한 번 더 평가하고,
// 거절하면 *store.RemovalRefusedError 를 낸다 — 그것을 여기서 Refusal 로 접는다.
// 두 번째 판정을 별도 오류(500 류)로 흘리지 않는 이유는, 그것은 사용자 잘못이 아니라
// 서버가 정직하게 다시 확인한 결과이기 때문이다 — 처음부터 --yes 없이 불렀을 때와 같은
// 모양(ProjectRemoval.Refusal)으로 보여야 CLI 가 문구를 새로 안 만든다.
func (s *Service) RemoveProject(ctx context.Context, id, actor, reason string,
	confirm bool) (ProjectRemoval, error) {

	if strings.TrimSpace(reason) == "" {
		return ProjectRemoval{}, &RefusedError{What: "project remove",
			Reason:   "사유가 비었다",
			Guidance: "되돌릴 수 없는 삭제다 — 왜 지우는지를 적어라. 원장에 남는다."}
	}
	// ★ 존재 확인을 먼저 한다(리뷰 #5). 안 하면 오타 난 id 는 카운트가 전부 0이라
	// 판정을 통과하고 "확인이 없다 … --yes 를 붙여라" 가 나간다 — 없는 프로젝트를
	// 지우라고 권하는 꼴이다. GetProject 가 못 찾으면 그 자체가 이미 정확한 사유
	// (store.NotFoundError → NFProject)라 그대로 올린다.
	if _, err := s.st.GetProject(ctx, id); err != nil {
		return ProjectRemoval{}, err
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
		var raced *store.RemovalRefusedError
		if errors.As(err, &raced) {
			// ★ Counts 도 재-판정 시점의 새 값으로 바꾼다 — 호출 밖의 낡은 Counts 를
			// 그대로 두면 "0건이라며 왜 거절이냐"는 모순된 화면이 나간다.
			out.Counts = raced.Counts
			out.Refusal = raced.Reason
			return out, nil
		}
		return ProjectRemoval{}, err
	}
	s.st.LogEvent(ctx, "project.removed", id, "", map[string]any{
		"actor": clip(actor, 120), "reason": clip(reason, 400), "counts": counts,
	})
	out.Removed = true
	return out, nil
}
