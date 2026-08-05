package service

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// addItemAs 는 **세션을 밝히고** 항목을 만든다.
//
// 공용 addItem 헬퍼는 SessionID 를 안 넘겨서 item.add 이벤트에 세션이 안 남는다.
// 이 관문은 "누가 만들었나"로 판정하므로 그 헬퍼로는 원리적으로 못 시험한다.
func addItemAs(t *testing.T, s *Service, project, sessionID, id string) {
	t.Helper()
	if _, err := s.AddItem(ctx(), AddItemInput{
		Project: project, SessionID: sessionID, ID: id,
		Title: id + " 제목", Body: id + " 본문",
	}); err != nil {
		t.Fatalf("항목 등록 실패(%s): %v", id, err)
	}
}

func finishNoFollowups(s *Service, sessionID, itemID string) error {
	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: sessionID, ItemID: itemID,
		Outcome: model.ItemDone, Title: "끝냈다",
		Body: "왜 그렇게 했나 · 무엇을 기각했나 · 일부러 안 한 것 · 확인했으나 못 한 것",
	})
	return err
}

// TestFinishStopsOnceWhenFollowupsFellOnTheFloor 는 이 관문의 **양쪽**을 한 시험에서 본다:
// 한 번은 막고, 두 번째는 통과시킨다.
//
// 둘을 갈라 쓰면 안 된다 — "막는다"만 시험하면 벽이 된 것을 못 보고,
// "통과한다"만 시험하면 관문이 죽은 것을 못 본다. 이 관문의 값어치는 **정확히 한 번**에 있다.
func TestFinishStopsOnceWhenFollowupsFellOnTheFloor(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	// 선점 **뒤에** 딴 축을 발견해 항목을 올린다 — 이 항목이 만들어진 그 상황이다.
	addItemAs(t, s, "p", me.Session.ID, "spun-off-axis")

	// ① 한 번은 막는다. 그리고 **무엇이 걸렸는지 이름을 낸다** —
	//    수만 말하면 무엇을 후속으로 넣을지 다시 조사해야 한다.
	err := finishNoFollowups(s, me.Session.ID, "batch7")
	if err == nil {
		t.Fatalf("후속을 바닥에 떨어뜨렸는데 통과했다")
	}
	msg := err.Error()
	for _, want := range []string{"spun-off-axis", "followups"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("거절 사유에 %q 가 없다:\n%s", want, msg)
		}
	}

	// ② 두 번째는 통과한다. 이 단정이 관문을 벽과 가른다.
	if err := finishNoFollowups(s, me.Session.ID, "batch7"); err != nil {
		t.Fatalf("두 번째 호출까지 막았다 — 관문이 아니라 벽이다: %v", err)
	}
}

// TestFinishFollowupGateIgnoresClosedAndPreClaimItems 는 **거짓 거절 둘**을 막는다.
//
// 이 시험이 없으면 관문이 상시 발동해 판별력이 0이 된다.
func TestFinishFollowupGateIgnoresClosedAndPreClaimItems(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")

	// ① 선점 **전에** 만든 항목 — 앞선 작업의 산물이다. 세면 항목을 끝낼 때마다
	//    과거 전부가 딸려 온다.
	addItemAs(t, s, "p", me.Session.ID, "from-earlier-work")

	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	// ② 선점 뒤에 만들었지만 **이미 닫힌** 항목 — 남이 집어 끝냈을 수 있다.
	//    실측 사례가 있다(fd-footprint-has-no-containment-gate).
	addItemAs(t, s, "p", me.Session.ID, "someone-else-landed-it")
	if err := st.SetItemState(ctx(), "p", "someone-else-landed-it", model.ItemDone, "남이 끝냈다"); err != nil {
		t.Fatalf("전제 구성 실패: %v", err)
	}

	if err := finishNoFollowups(s, me.Session.ID, "batch7"); err != nil {
		t.Fatalf("거짓 거절이다 — 선점 전 항목과 닫힌 항목은 세면 안 된다: %v", err)
	}
}

// TestFinishFollowupGateIsPerItem 은 경고가 **항목마다** 따로라는 것이다.
//
// 한 세션이 항목 여럿을 연달아 마무리한다(실측: 이 저장소에서 한 세션이 두 건을 닫았다).
// 세션 단위로만 기억하면 첫 항목에서 발화한 뒤 둘째 항목은 조용히 지나간다 —
// 그러면 이 관문은 세션당 한 번만 살아 있는 셈이 된다.
func TestFinishFollowupGateIsPerItem(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")

	addItem(t, s, "p", "first", nil, nil)
	addItem(t, s, "p", "second", nil, nil)

	claimed(t, s, "p", me.Session.ID, "first")
	addItemAs(t, s, "p", me.Session.ID, "spun-off-a")
	if err := finishNoFollowups(s, me.Session.ID, "first"); err == nil {
		t.Fatalf("첫 항목에서 안 막았다")
	}
	if err := finishNoFollowups(s, me.Session.ID, "first"); err != nil {
		t.Fatalf("첫 항목 두 번째 호출을 막았다: %v", err)
	}

	// 둘째 항목 — 첫 항목의 경고가 이것까지 통과시키면 안 된다.
	claimed(t, s, "p", me.Session.ID, "second")
	addItemAs(t, s, "p", me.Session.ID, "spun-off-b")
	err := finishNoFollowups(s, me.Session.ID, "second")
	if err == nil {
		t.Fatalf("둘째 항목이 첫 항목의 경고로 통과했다 — 경고가 항목마다여야 한다")
	}
	if !strings.Contains(err.Error(), "spun-off-b") {
		t.Fatalf("둘째 항목의 거절이 자기 후속을 안 낸다:\n%s", err.Error())
	}
}
