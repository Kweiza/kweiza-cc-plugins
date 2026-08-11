package service

import (
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 선점 이력 — **`claim` 표는 과거를 모른다.**
//
//	PRIMARY KEY (project, item_id)
//
// 항목 하나에 행 하나다. 반납은 `released_at` 을 채우고 재선점은 그 행을 **덮는다.**
// 그래서 "시각 t 에 이 항목을 누가 쥐고 있었나"에 그 표는 답할 수 없고, 더 나쁘게는
// **조용히 틀린 답**을 낸다 — 지금 쥔 사람을 그때 쥔 사람인 것처럼 낸다.
//
// 실물로 걸렸다(2026-08-11): unclaimed 처방 118건의 성격을 가르려고 `claim` 을 그대로 읽었더니
// 발화 시점에 분명히 선점돼 있던 항목이 "이 대화의 선점 총 0건"으로 나왔다. 오류도 경고도
// 없었다. 답은 추가전용 원장(`item.claim`·`item.finish`·`claim.reclaim`)을 시간순으로 재생해야
// 나오고, 그 재생 지식이 그때는 일회용 스크립트에만 있었다.
//
// ★ 이 시험이 잠그는 것은 **그 지식이 코드에 있다**는 것이다.

// pickItemForHistoryTest 는 **실제 서비스 경로**로 항목을 집는다.
//
// ★ claimItemForPrescribeTest 를 쓰면 안 된다 — 그쪽은 store.ClaimItem 을 직접 불러
// `item.claim` **이벤트를 안 남긴다.** 이 파일이 재는 것은 그 이벤트로 하는 재생이라,
// 그 헬퍼를 쓰면 시험이 아무것도 안 재면서 빨간불/초록불이 엉뚱한 이유로 난다.
// 실제 경로를 태우는 것이 payload 키 이름(`item`)까지 함께 잠근다.
func pickItemForHistoryTest(t *testing.T, s *Service, sessionID, itemID string, paths []string) {
	t.Helper()
	addItem(t, s, "p", itemID, paths, nil)
	if _, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: sessionID, ItemID: itemID}); err != nil {
		t.Fatalf("선점 실패(%s): %v", itemID, err)
	}
}

// TestClaimHolderAtAnswersThePastAfterAReclaim 은 이 축의 전부다 —
// 재선점이 표를 덮은 **뒤에도** 옛 점유자를 답한다.
func TestClaimHolderAtAnswersThePastAfterAReclaim(t *testing.T) {
	svc, st := newSvc(t)
	repo := newRepo(t)
	first := openSession(t, svc, "p", repo, repo, "cc-처음", "처음 쥔 세션").Session.ID
	second := openSession(t, svc, "p", repo, repo, "cc-나중", "나중 쥔 세션").Session.ID

	pickItemForHistoryTest(t, svc, first, "fd-x", []string{"cmd/fd"})
	during := time.Now().UTC()

	// 사람이 회수한다 — 실물에서 재선점이 나는 경로다(만료도 자동 반납도 없다).
	if err := st.ForceReleaseClaim(ctx(), "p", "fd-x", "응답이 없다"); err != nil {
		t.Fatalf("회수 실패: %v", err)
	}
	st.LogEvent(ctx(), "claim.reclaim", "", "", map[string]any{"item": "fd-x", "holder": first})
	// 같은 항목을 **다른 세션**이 다시 집는다 — 이 순간 claim 행이 덮인다.
	if _, err := svc.Pick(ctx(), PickInput{Project: "p", SessionID: second, ItemID: "fd-x"}); err != nil {
		t.Fatalf("재선점 실패: %v", err)
	}
	after := time.Now().UTC()

	// ── ① 과거: 표가 덮였어도 그때 쥔 사람은 first 다.
	got, err := st.ClaimHolderAt(ctx(), "p", "fd-x", during)
	if err != nil {
		t.Fatalf("이력 조회 실패: %v", err)
	}
	if got != first {
		cur, _ := st.GetClaim(ctx(), "p", "fd-x")
		t.Fatalf("시각 %s 의 점유자를 %q 라 했다 — 기대 %q(지금 표의 값은 %q 다)\n"+
			"claim 표를 그냥 읽으면 나는 바로 그 조용한 오답이다",
			during.Format(time.RFC3339Nano), got, first, cur.SessionID)
	}

	// ── ② 현재: 지금 쥔 사람은 second 다.
	got, err = st.ClaimHolderAt(ctx(), "p", "fd-x", after)
	if err != nil {
		t.Fatalf("현재 조회 실패: %v", err)
	}
	if got != second {
		t.Fatalf("지금 점유자를 %q 라 했다 — 기대 %q", got, second)
	}
}

// TestClaimHolderAtIsEmptyBetweenTheTwoClaims 는 **반납 구간**을 잠근다.
//
// 이것이 없으면 위 시험은 "마지막 item.claim 을 무조건 낸다"로도 초록불이 난다 —
// 그 오답은 반납을 아예 안 보는 것이라 "아무도 안 쥐었다"를 영영 못 낸다.
func TestClaimHolderAtIsEmptyBetweenTheTwoClaims(t *testing.T) {
	svc, st := newSvc(t)
	repo := newRepo(t)
	first := openSession(t, svc, "p", repo, repo, "cc-처음", "처음 쥔 세션").Session.ID

	pickItemForHistoryTest(t, svc, first, "fd-x", []string{"cmd/fd"})
	if _, err := svc.Finish(ctx(), FinishInput{
		Project: "p", SessionID: first, ItemID: "fd-x", Outcome: model.ItemDone,
		Title: "끝냈다", Body: "무엇을 정했고 무엇을 기각했나",
	}); err != nil {
		t.Fatalf("finish 실패: %v", err)
	}
	idle := time.Now().UTC()

	got, err := st.ClaimHolderAt(ctx(), "p", "fd-x", idle)
	if err != nil {
		t.Fatalf("이력 조회 실패: %v", err)
	}
	if got != "" {
		t.Fatalf("반납된 뒤인데 점유자를 %q 라 했다 — 반납을 안 보고 있다", got)
	}
}

// TestClaimHolderAtIgnoresRefusalsAndResumes 는 **점유자를 안 바꾸는 이벤트**를 잠근다.
//
// 원장에는 선점을 건드리는 것처럼 보이지만 실제로는 안 바꾸는 kind 가 섞여 있다:
//
//	item.claim.fail     선점이 거절됐다 — 점유자는 그대로다
//	item.finish.refused 마무리가 관문에 막혔다 — 반납이 안 일어났다
//	item.resume         이미 자기 선점일 때만 난다(pick.go 의 resume 갈래) — 주인이 그대로다
//
// 이것들을 세면 "거절된 선점이 성공한 것처럼" 또는 "막힌 마무리가 반납한 것처럼" 보인다.
func TestClaimHolderAtIgnoresRefusalsAndResumes(t *testing.T) {
	svc, st := newSvc(t)
	repo := newRepo(t)
	holder := openSession(t, svc, "p", repo, repo, "cc-주인", "쥔 세션").Session.ID
	other := openSession(t, svc, "p", repo, repo, "cc-남", "남").Session.ID

	pickItemForHistoryTest(t, svc, holder, "fd-x", []string{"cmd/fd"})
	st.LogEvent(ctx(), "item.claim.fail", "p", other,
		map[string]any{"item": "fd-x", "error": "이미 선점돼 있다"})
	st.LogEvent(ctx(), "item.finish.refused", "p", holder,
		map[string]any{"item": "fd-x", "gate": "judge"})
	st.LogEvent(ctx(), "item.resume", "p", holder, map[string]any{"item": "fd-x"})

	got, err := st.ClaimHolderAt(ctx(), "p", "fd-x", time.Now().UTC())
	if err != nil {
		t.Fatalf("이력 조회 실패: %v", err)
	}
	if got != holder {
		t.Fatalf("점유자를 %q 라 했다 — 기대 %q\n"+
			"거절·재개 이벤트를 선점 변경으로 셌다", got, holder)
	}
}
