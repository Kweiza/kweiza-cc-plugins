package service

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// ─────────────────────────────────────────────────────────────────────────────
// 명시 선점이 선행을 **말한다** — 막지는 않는다
// ─────────────────────────────────────────────────────────────────────────────
//
// pickExplicit 에는 선행 판정이 아예 없었다. after 는 추천 경로 전용 관문이라,
// `pick(item_id=X)` 는 막힌 항목을 **조용히** 집을 수 있었다 — render 가 `선행:` 목록을
// 찍긴 했지만 충족 여부는 안 찍었다.
//
// ★ **거절이 아니라 알림인 근거는 실측이다**(2026-08-22, 원장의 item.claim 1053건을
// 호출 단위로 복원): 해소 전 선점 53건 중 27건이 그 선행을 같은 호출에서 함께 집은
// 정상 경로였고(blockedOnlyBy), 나머지 26건도 되돌린 흔적이 없었다. 셋은 선행이 이미
// 폐기된 항목이라 거절했으면 영영 못 집었을 것이다. 전문은 PickResult.AfterCheck 의 머리말.

// 막힌 항목을 명시로 집으면 — **집힌다. 그리고 그 사실이 응답에 뜬다.**
func TestExplicitPickOfBlockedItemIsClaimedAndSaysSo(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc1", "나")

	addItem(t, s, "p", "dep", nil, nil) // 안 끝났다 — open 이다
	addItem(t, s, "p", "waiter", nil, []model.After{{Item: "dep"}})

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: sess.Session.ID, ItemID: "waiter"})
	if err != nil {
		t.Fatalf("막힌 항목을 **거절했다**: %v\n"+
			"이 서버는 사람의 명시 선택을 안 덮는다 — steal_reason·release 를 거절하는 것과 같은 결이다.\n"+
			"막으면 실측된 정상 경로(선행과 함께 집는 묶음 흡수 27건)가 함께 죽는다.", err)
	}
	if res.Mode != PickClaimed {
		t.Fatalf("mode 가 %q 다 — 집혔어야 한다", res.Mode)
	}

	// ── 그런데 조용하면 안 된다 ──
	if res.AfterCheck == nil {
		t.Fatal("AfterCheck 가 nil 이다 — 이 축을 안 읽었다는 뜻이 된다.\n" +
			"그러면 화면이 '구서버이거나 낡은 캐시다' 를 찍는데 둘 다 거짓이고,\n" +
			"막힌 항목을 조용히 집는다는 이 결함이 그대로 남는다")
	}
	if res.AfterCheck.Satisfied {
		t.Fatal("선행이 충족됐다고 한다 — dep 은 아직 open 이다")
	}
	if len(res.AfterCheck.Reasons) != 1 || !strings.Contains(res.AfterCheck.Reasons[0], judge.AfterUnmetItem) {
		t.Errorf("사유가 %v 다 — %s 하나여야 한다(사유 코드가 곧 처방이다)",
			res.AfterCheck.Reasons, judge.AfterUnmetItem)
	}
	if len(res.AfterCheck.WithInCall) != 0 {
		t.Errorf("WithInCall 이 %v 다 — 단독 호출이라 함께 집는 선행이 없다", res.AfterCheck.WithInCall)
	}
}

// 충족된 항목에도 **말한다.** 침묵하면 "충족됐다"와 "이 축을 안 봤다"가 같은 화면이 된다.
func TestExplicitPickFillsAfterCheckWhenSatisfied(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc1", "나")

	addItem(t, s, "p", "dep", nil, nil)
	addItem(t, s, "p", "waiter", nil, []model.After{{Item: "dep"}})
	// dep 을 정식 경로로 끝낸다 — 상태를 손으로 쓰면 이 시험이 재는 것이 달라진다.
	if _, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: sess.Session.ID, ItemID: "dep"}); err != nil {
		t.Fatalf("dep 선점 실패: %v", err)
	}
	if _, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: sess.Session.ID, ItemID: "dep",
		Outcome: model.ItemDone, Title: "dep 마무리", Body: "①왜②기각③안한것④닫은것",
	}); err != nil {
		t.Fatalf("dep 종료 실패: %v", err)
	}

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: sess.Session.ID, ItemID: "waiter"})
	if err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	if res.AfterCheck == nil || !res.AfterCheck.Satisfied {
		t.Fatalf("AfterCheck 가 %+v 다 — dep 이 done 이므로 충족이어야 하고, nil 이면 안 된다", res.AfterCheck)
	}
}

// 묶음 흡수 — 선행을 **같은 호출에서 함께 집는** 경우는 그렇게 말한다.
//
// ★ 이 갈래가 실측 53건 중 27건(51%)이다. 이것을 다른 미충족과 같은 문장으로 내면
// 정상 경로에서 절반 넘게 뜨는 경고가 되고, 그런 줄은 결국 아무도 안 읽는다.
func TestBundleMemberBlockedOnlyByLeadNamesItAsInCall(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc1", "나")

	addItem(t, s, "p", "dep", nil, nil)
	addItem(t, s, "p", "waiter", nil, []model.After{{Item: "dep"}})

	res, err := s.Pick(ctx(), PickInput{
		Project: "p", SessionID: sess.Session.ID, ItemIDs: []string{"dep", "waiter"}})
	if err != nil {
		t.Fatalf("묶음 선점 실패: %v", err)
	}
	if res.Bundle == nil || len(res.Bundle.Members) != 1 {
		t.Fatalf("구성원이 %v 다 — waiter 하나여야 한다", res.Bundle)
	}
	m := res.Bundle.Members[0]
	if m.AfterCheck == nil {
		t.Fatal("구성원의 AfterCheck 가 nil 이다 — PathCheck·CloseDeclared 과 같은 계약이라\n" +
			"여기서 안 나르면 저 둘은 실리는데 이 축만 조용히 사라진다")
	}
	if m.AfterCheck.Satisfied {
		t.Fatal("충족이라고 한다 — 선두 dep 은 지금 claimed 이지 done 이 아니다")
	}
	if len(m.AfterCheck.WithInCall) != 1 || m.AfterCheck.WithInCall[0] != "dep" {
		t.Fatalf("WithInCall 이 %v 다 — [dep] 이어야 한다.\n"+
			"이 칸이 비면 화면이 정상적인 묶음 흡수를 다른 미충족과 같은 문장으로 내고,\n"+
			"그러면 실측 53건 중 27건에서 헛걸리는 경고가 된다", m.AfterCheck.WithInCall)
	}
}

// afterVerdictFrom 은 순수 함수다 — WithInCall 이 무엇을 담고 무엇을 안 담는지 못박는다.
func TestAfterVerdictFromFiltersWithInCall(t *testing.T) {
	item := model.Item{ID: "me", After: []model.After{
		{Item: "unmet-in-call"},
		{Item: "unmet-outside"},
		{Item: "done-in-call"},
		{Item: "me"}, // 자기 자신을 가리킨다 — 스키마가 안 막는다
	}}
	facts := judge.AfterFacts{ItemStates: map[string]model.ItemState{
		"unmet-in-call": model.ItemOpen,
		"unmet-outside": model.ItemOpen,
		"done-in-call":  model.ItemDone,
		"me":            model.ItemClaimed,
	}}
	v := afterVerdictFrom(item, facts, []string{"me", "unmet-in-call", "done-in-call"})

	if v.Satisfied {
		t.Fatal("충족이라고 한다 — 미충족이 셋이다")
	}
	want := []string{"unmet-in-call"}
	if len(v.WithInCall) != len(want) || v.WithInCall[0] != want[0] {
		t.Fatalf("WithInCall 이 %v 다 — %v 여야 한다.\n"+
			"  · unmet-outside 는 이 호출에 없다\n"+
			"  · done-in-call 은 이미 충족이라 이 줄이 말하려는 사실이 아니다\n"+
			"  · me 는 자기 자신이다 — 안 빼면 자기를 선행으로 가리키는 항목이 "+
			"'함께 집는다' 로 접힌다", v.WithInCall, want)
	}
}
