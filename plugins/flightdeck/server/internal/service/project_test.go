package service

import (
	"context"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// TestListProjectSummariesCounts 는 요약의 수치가 실제 행 수와 맞는지 본다.
// 지우기 전에 무엇이 있는지 보는 유일한 길이라, 이 수가 틀리면 사람이 틀린 판단을 한다.
func TestListProjectSummariesCounts(t *testing.T) {
	ctx := context.Background()
	svc, st := newSvc(t)

	for _, id := range []string{"live", "empty"} {
		if err := st.UpsertProject(ctx, model.Project{
			ID: id, Path: "/tmp/" + id, DefaultBranch: "main",
		}); err != nil {
			t.Fatalf("프로젝트 등록 실패(%s): %v", id, err)
		}
	}
	// live 에만 항목 둘을 붙인다.
	for _, itemID := range []string{"a", "b"} {
		if err := st.Tx(ctx, func(tx *store.Tx) error {
			return tx.AddItem(model.Item{
				Project: "live", ID: itemID, Title: itemID, Body: "본문",
				State: "open", CreatedAt: time.Now().UTC(),
			})
		}); err != nil {
			t.Fatalf("항목 등록 실패(%s): %v", itemID, err)
		}
	}
	if err := st.SetProjectView(ctx, "empty", time.Time{}, time.Now().UTC()); err != nil {
		t.Fatalf("보관 실패: %v", err)
	}

	list, err := svc.ListProjectSummaries(ctx)
	if err != nil {
		t.Fatalf("요약 실패: %v", err)
	}
	by := map[string]ProjectSummary{}
	for _, p := range list {
		by[p.ID] = p
	}
	if by["live"].Items != 2 {
		t.Fatalf("live 의 항목이 %d건, 기대 2건 — 이 수를 보고 사람이 지울지 정한다", by["live"].Items)
	}
	if by["empty"].Items != 0 {
		t.Fatalf("empty 의 항목이 %d건, 기대 0건", by["empty"].Items)
	}
	if !by["empty"].Archived {
		t.Fatal("empty 가 보관으로 안 나온다")
	}
	if by["live"].Archived || by["live"].Pinned {
		t.Fatal("live 는 핀도 보관도 아니어야 한다")
	}
}
