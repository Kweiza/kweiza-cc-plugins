package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// 폐기 관문 — 종속이 있는 항목을 버릴 때 **한 번** 붙잡는다
// ─────────────────────────────────────────────────────────────────────────────
//
// ★ 결함의 진짜 진입로가 여기다. 지금은 종속이 있는 항목을 dropped 로 닫아도 **경고가 0이다.**
// 그 순간 기대던 항목들은 judge.AfterSatisfied 에서 after-dropped-dep 로 떨어지고,
// 그 사유는 "기다려도 안 풀린다 — 선행을 고쳐라"라고 말한다. 실측 피해 2건이 그 모양으로 났다.
//
// ★ **한 번만 막는다** — judgeMissingFollowups 와 같은 설계다. 영영 막으면 벽이 되고,
// 세션은 close_reason 으로 우회하거나 종속을 거짓으로 손본다. 둘 다 원장을 더럽힌다.

// dropCase 는 선행 하나와 그것에 기대는 항목 하나를 세운 뒤 세션이 선행을 쥔 상태를 만든다.
func dropCase(t *testing.T, svc *Service, st *store.Store) (sessionID string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	sess, err := svc.OpenSession(ctx, OpenSessionInput{
		Project: "p", ProjectPath: dir, MachineID: "m1", Hostname: "h1",
		Worktree: dir, CCSessionID: "cc-1",
	})
	if err != nil {
		t.Fatalf("세션 열기 실패: %v", err)
	}
	for _, id := range []string{"dep", "waiter"} {
		if err := st.AddItem(ctx, model.Item{Project: "p", ID: id, Title: id, Body: "본문"}); err != nil {
			t.Fatalf("항목 등록 실패(%s): %v", id, err)
		}
	}
	if err := st.Tx(ctx, func(tx *store.Tx) error {
		return tx.AddAfter("p", "waiter", model.After{Item: "dep"})
	}); err != nil {
		t.Fatalf("선행 등록 실패: %v", err)
	}
	if _, err := st.ClaimItem(ctx, "p", "dep", sess.Session.ID, time.Time{}); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	return sess.Session.ID
}

func dropInput(sessionID string) FinishInput {
	return FinishInput{
		Project: "p", SessionID: sessionID, ItemID: "dep",
		Outcome: model.ItemDropped, Body: "판단 본문",
		CloseReason: "이 축은 접는다",
		// 후속 관문에 안 걸리게 비워 두되 — 이 세션은 add 를 안 썼으므로 그쪽은 발화하지 않는다.
	}
}

// 종속이 있는 항목을 폐기하려 하면 한 번 붙잡고, **이름을 낸다.**
func TestFinishRefusesDroppingADepThatOthersStillWaitOnAndNamesThem(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	sessionID := dropCase(t, svc, st)

	_, err := svc.Finish(ctx, dropInput(sessionID))
	if err == nil {
		t.Fatal("종속이 있는데 조용히 폐기됐다 — 기대던 항목이 영구히 굶는다")
	}
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("거절이 아닌 오류다: %v", err)
	}
	if !strings.Contains(refused.Reason, "waiter") {
		t.Errorf("무엇이 막히는지 이름이 없다 — 세션이 다시 조사해야 한다: %s", refused.Reason)
	}
	// 처방이 **집행 가능한 동사**를 가리켜야 한다. 이 관문을 낳은 결함이 정확히
	// "처방은 있는데 그것을 집행할 동사가 없다"였다.
	if !strings.Contains(refused.Guidance, "after cut") {
		t.Errorf("처방이 집행 수단을 안 가리킨다: %s", refused.Guidance)
	}

	// ── 아무것도 안 써야 한다. 거절은 트랜잭션 전이라 항목이 그대로 열려 있어야 한다.
	it, gerr := st.GetItem(ctx, "p", "dep")
	if gerr != nil {
		t.Fatalf("항목 조회 실패: %v", gerr)
	}
	if it.State == model.ItemDropped {
		t.Error("거절했는데 항목이 닫혔다 — 거절이 부작용을 남겼다")
	}
}

// 두 번째 호출은 통과시킨다. **관문이지 벽이 아니다.**
//
// 영영 막으면 세션은 종속을 거짓으로 손보거나 close_reason 으로 우회한다 — 그 둘이
// 이 항목의 선례에서 실제로 일어난 일이다(같은 벽이 두 번 나왔고 둘 다 우회됐다).
func TestFinishLetsTheSecondDropThroughAfterWarningOnce(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	sessionID := dropCase(t, svc, st)

	if _, err := svc.Finish(ctx, dropInput(sessionID)); err == nil {
		t.Fatal("전제가 깨졌다 — 첫 호출이 안 막혔다")
	}
	if _, err := svc.Finish(ctx, dropInput(sessionID)); err != nil {
		t.Fatalf("두 번째도 막혔다 — 관문이 벽이 됐다: %v", err)
	}

	it, err := st.GetItem(ctx, "p", "dep")
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if it.State != model.ItemDropped {
		t.Errorf("두 번째가 통과했는데 항목 상태가 %s 다", it.State)
	}
}

// 종속이 없으면 아예 안 발화한다 — 흔한 경로에 비용도 문구도 얹지 않는다.
func TestFinishDoesNotWarnWhenNothingDependsOnTheDroppedItem(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	sessionID := dropCase(t, svc, st)

	// 기대던 쪽을 먼저 끊는다 — 이 동사가 그러라고 있는 것이다.
	if _, err := svc.CutAfter(ctx, CutAfterInput{
		Project: "p", ItemID: "waiter", Dep: model.After{Item: "dep"}, SessionID: sessionID,
	}); err != nil {
		t.Fatalf("선행 절단 실패: %v", err)
	}

	if _, err := svc.Finish(ctx, dropInput(sessionID)); err != nil {
		t.Fatalf("종속이 없는데 막혔다: %v", err)
	}
}

// done 은 안 막는다. **끝난 선행은 충족이다** — judge.AfterSatisfied 가 그렇게 읽는다.
func TestFinishDoesNotWarnOnDoneEvenWithDependents(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	sessionID := dropCase(t, svc, st)

	in := dropInput(sessionID)
	in.Outcome = model.ItemDone
	in.CloseReason = ""
	if _, err := svc.Finish(ctx, in); err != nil {
		t.Fatalf("done 인데 막혔다 — 끝난 선행은 기대던 쪽을 풀어 준다: %v", err)
	}
}
