package service

import (
	"context"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// 선행 절단 — 처방을 집행하고 **그 결과로 무엇이 남았는지** 같은 응답에서 말한다
// ─────────────────────────────────────────────────────────────────────────────

func seedCut(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	if err := st.UpsertProject(ctx, model.Project{ID: "p", Path: "/repo/p"}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	for _, id := range []string{"dep", "waiter"} {
		if err := st.AddItem(ctx, model.Item{Project: "p", ID: id, Title: id, Body: "본문"}); err != nil {
			t.Fatalf("항목 등록 실패(%s): %v", id, err)
		}
	}
}

// 끊고 나서 **남은 선행**을 같은 응답에 싣는다.
//
// 이 값이 없으면 세션은 "이제 집을 수 있나"에 답하려고 pick 을 다시 불러야 한다. 그리고 이 동사는
// 하나씩 끊으므로, 남은 것이 안 보이면 "하나 풀었더니 또 하나"가 반복된다 —
// judge.AfterSatisfied 가 사유를 **전부** 내는 것과 같은 이유다.
func TestCutAfterReportsWhatIsStillAttached(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	seedCut(t, st)
	if err := st.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.AddAfter("p", "waiter", model.After{Item: "dep"}); err != nil {
			return err
		}
		return tx.AddAfter("p", "waiter", model.After{SHA: "0f19bf3"})
	}); err != nil {
		t.Fatalf("선행 등록 실패: %v", err)
	}

	res, err := svc.CutAfter(ctx, CutAfterInput{
		Project: "p", ItemID: "waiter", Dep: model.After{Item: "dep"},
	})
	if err != nil {
		t.Fatalf("절단 실패: %v", err)
	}

	if len(res.Item.After) != 1 || res.Item.After[0].SHA != "0f19bf3" {
		t.Errorf("남은 선행이 %+v 다 — sha 하나만 남아야 한다", res.Item.After)
	}
	// 끊은 것 자체도 되돌려준다. 응답만 보고 원장에 판단을 쓸 수 있어야 한다.
	if res.Cut.Item != "dep" {
		t.Errorf("무엇을 끊었는지가 결과에 없다: %+v", res.Cut)
	}
}

// 빈 좌표는 DB 를 건드리기 전에 거절한다.
//
// 빈 항목 id 로 내려가면 "그런 선행이 없다"(404)가 나가는데, 진짜 사유는 **인자를 안 준 것**이다.
// 그러면 사람은 dep 이름을 의심하러 가고, 그 조사가 통째로 헛것이 된다.
func TestCutAfterRefusesEmptyCoordinatesBeforeTouchingTheStore(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	seedCut(t, st)

	for _, c := range []struct {
		name string
		in   CutAfterInput
		word string
	}{
		{"프로젝트 없음", CutAfterInput{ItemID: "waiter", Dep: model.After{Item: "dep"}}, "프로젝트"},
		{"항목 없음", CutAfterInput{Project: "p", Dep: model.After{Item: "dep"}}, "항목"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.CutAfter(ctx, c.in)
			if err == nil {
				t.Fatal("빈 좌표가 통과했다")
			}
			if !strings.Contains(err.Error(), c.word) {
				t.Errorf("어느 인자가 비었는지 안 적혔다: %v", err)
			}
		})
	}
}
