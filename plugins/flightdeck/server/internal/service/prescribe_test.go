package service

import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 처방이 발화되면 event 에 남고, 두 번째 호출에는 안 뜬다.
// **이것이 이 서비스의 유일한 불변식이다** — 억제가 DB 를 통해 돌아야 세션이 재시작해도 유효하다.
func TestPrescriptionsAreEmittedOnceAcrossCalls(t *testing.T) {
	svc, st := newSvc(t)

	sess := openSessionForPrescribeTest(t, svc)
	touchPathForPrescribeTest(t, st, sess, "cmd/fd/hook.go")

	first, err := svc.Prescriptions(ctx(), sess)
	if err != nil {
		t.Fatalf("첫 호출 실패: %v", err)
	}
	if len(first.All) == 0 {
		t.Fatal("선점 없이 편집했는데 처방이 0건이다")
	}

	second, err := svc.Prescriptions(ctx(), sess)
	if err != nil {
		t.Fatalf("둘째 호출 실패: %v", err)
	}
	if len(second.All) != 0 {
		t.Fatalf("같은 키가 다시 떴다: %+v", second.All)
	}

	evs, err := st.ListSessionEvents(ctx(), sess, "prescribe", time.Time{})
	if err != nil {
		t.Fatalf("이벤트 조회 실패: %v", err)
	}
	if len(evs) != len(first.All) {
		t.Fatalf("발화 기록 수가 다르다: events=%d, prescriptions=%d", len(evs), len(first.All))
	}
	if !strings.Contains(evs[0].Payload, `"key"`) {
		t.Fatalf("payload 에 key 가 없다: %s", evs[0].Payload)
	}
}

// 접힌 것도 발화 기록된다. 요약된 것은 "안 낸 것"이 아니다.
func TestFoldedPrescriptionsAreStillRecorded(t *testing.T) {
	svc, st := newSvc(t)

	sess := openSessionForPrescribeTest(t, svc)
	claimItemForPrescribeTest(t, svc, st, sess, "fd-x", []string{"internal/judge"})
	for _, p := range []string{"a/1.go", "b/2.go", "c/3.go", "d/4.go", "e/5.go"} {
		touchPathForPrescribeTest(t, st, sess, p)
	}

	res, err := svc.Prescriptions(ctx(), sess)
	if err != nil {
		t.Fatalf("호출 실패: %v", err)
	}
	if res.Folded == 0 {
		t.Fatalf("5개 경로가 선언 밖인데 안 접혔다: shown=%d", len(res.Shown))
	}
	evs, _ := st.ListSessionEvents(ctx(), sess, "prescribe", time.Time{})
	if len(evs) != len(res.All) {
		t.Fatalf("접힌 것이 발화 기록에서 빠졌다: events=%d, all=%d", len(evs), len(res.All))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 헬퍼 — 이 패키지의 기존 헬퍼(newSvc·openSession·addItem)를 조립한 것뿐이다.
// ─────────────────────────────────────────────────────────────────────────────

// openSessionForPrescribeTest 는 실물 저장소로 세션 하나를 열고 id 를 낸다.
func openSessionForPrescribeTest(t *testing.T, s *Service) string {
	t.Helper()
	repo := newRepo(t)
	res := openSession(t, s, "p", repo, repo, "cc-1", "처방시험")
	return res.Session.ID
}

// touchPathForPrescribeTest 는 origin=observed 발자국을 하나 남긴다 —
// PostToolUse 훅이 실제 편집을 봤을 때와 같은 모양이다.
func touchPathForPrescribeTest(t *testing.T, st *store.Store, sessionID, path string) {
	t.Helper()
	if err := st.Touch(ctx(), sessionID, path, model.OriginObserved, time.Now()); err != nil {
		t.Fatalf("발자국 기록 실패(%s): %v", path, err)
	}
}

// claimItemForPrescribeTest 는 항목을 등록하고 이 세션이 바로 선점하게 한다.
func claimItemForPrescribeTest(t *testing.T, s *Service, st *store.Store, sessionID, itemID string, paths []string) {
	t.Helper()
	sess, err := st.GetSession(ctx(), sessionID)
	if err != nil {
		t.Fatalf("세션 조회 실패: %v", err)
	}
	addItem(t, s, sess.Project, itemID, paths, nil)
	if _, err := st.ClaimItem(ctx(), sess.Project, itemID, sessionID); err != nil {
		t.Fatalf("선점 실패(%s): %v", itemID, err)
	}
}
