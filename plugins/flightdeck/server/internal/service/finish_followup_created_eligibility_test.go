package service

import (
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 이 파일이 잠그는 것: **자격은 넓히고 관문은 넓히지 않는다.**
//
// `finish` 의 `followups` 로 만들어진 항목은 `item.add` 이벤트가 없었다(그 이벤트를 남기는
// 자리가 Service.AddItem 하나였다). 그래서 `sessionSpawnedOpen` 이 그 항목을 못 보고,
// **같은 세션이 앞선 마무리에서 만든 후속을 다음 마무리에서 이으려 하면 자격 집합에 없어
// 거절됐다** — 이 기능이 권하는 사용법이 정확히 막혀 있었다.
//
// 고치는 방법이 하나가 아니어서 항목이 셋을 열어 뒀다. 고른 것은 **ⓑ 관문과 자격을 서로
// 다른 술어로 가르기**이고, 근거는 술어의 정확성이다:
//
//	· **자격**(이을 수 있는 것) = 이 세션이 이 선점 뒤 만든 열린 항목. 만든 경로가
//	  add 든 finish 든 성질이 같다 — 같은 세션·같은 작업에서 나왔다.
//	· **관문**(바닥에 떨어뜨린 것) = 그중 **아직 어느 판단에도 안 매달린** 것.
//	  finish 로 만든 후속은 **이미 그 마무리의 판단에 매달려 있다.** 바닥에 떨어진 것이
//	  아니라 이미 원장에 이어진 것이다.
//
// ⓐ(별도 origin 이벤트를 두고 자격만 그것을 본다)로는 부족하다. 두 목록이 한 함수에서
// 나오는 것이 앞선 설계의 근거였고, 이벤트를 하나 더 두는 것만으로는 그 단일성이 자격을
// 넓히는 순간 관문도 함께 넓혀 **거짓 거절**을 만든다. 술어를 갈라야 그 결합이 끊긴다.
//
// ★ 그래서 이 파일의 시험 둘은 **짝이다.** 하나만 있으면 반쪽이 조용히 뒤집힌다.

// ① 자격 — 앞선 마무리에서 만든 후속을 다음 마무리에서 이을 수 있다.
//
// 배치가 실제 사용과 같다: 묶음으로 둘을 집고 하나씩 끝낸다(그 순서가 되어야 후속 생성이
// 둘째 항목의 선점 시각 **뒤**가 되어 시각 조건을 지난다).
func TestFinishCanLinkAFollowupItCreatedInAnEarlierFinish(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	addItem(t, s, "p", "batch8", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")
	claimed(t, s, "p", me.Session.ID, "batch8")

	// 첫 마무리가 후속을 **만든다**.
	res1, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "batch7", Body: "①…②…③…④…",
		Followups: []FollowupInput{{ID: "spawned", Title: "후속", Body: "본문"}},
	})
	if err != nil {
		t.Fatalf("첫 마무리 실패: %v", err)
	}
	if len(res1.Followups) != 1 || res1.Followups[0].ID != "spawned" {
		t.Fatalf("후속이 안 만들어졌다 — 이 시험의 전제가 깨졌다: %+v", res1.Followups)
	}

	// 둘째 마무리가 그것을 **잇는다**. 이것이 막혀 있던 자리다.
	res2, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch8",
		Outcome: model.ItemDone, Title: "batch8", Body: "①…②…③…④…",
		Followups: []FollowupInput{{ID: "spawned"}},
	})
	if err != nil {
		t.Fatalf("앞선 마무리가 만든 후속을 이을 수 없다 — 이 기능이 권하는 사용법이 막혀 있다: %v", err)
	}
	if len(res2.LinkedFollowups) != 1 || res2.LinkedFollowups[0] != "spawned" {
		t.Fatalf("이어지지 않았다: linked=%v skipped=%v", res2.LinkedFollowups, res2.SkippedFollowups)
	}
	// 원장에도 남아야 한다 — 잇기는 응답만이 아니라 판단 링크가 정본이다.
	if n := countRows(t, st, `SELECT count(*) FROM judgment_link
		WHERE target_kind='item' AND target_id='spawned'`); n != 2 {
		t.Errorf("판단 링크가 %d건이다(기대 2: 만든 마무리 + 이은 마무리)", n)
	}
}

// ② 관문 — 자격이 넓어져도 "바닥에 떨어뜨린 후속" 관문은 넓어지지 않는다.
//
// ★ **이 시험은 변경 전에도 초록이었다**(그때는 자격 목록 자체가 그 항목을 못 봐서 우연히
// 맞았다). 지금은 다른 이유로 초록이어야 한다 — 자격 목록에는 들어오지만 **판단에 매달려
// 있어** 관문에서 빠지기 때문이다. 그 술어가 사라지면 여기가 빨개진다.
func TestFollowupsGateIgnoresItemsAlreadyLinkedToAJudgment(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	addItem(t, s, "p", "batch8", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")
	claimed(t, s, "p", me.Session.ID, "batch8")

	if _, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "batch7", Body: "①…②…③…④…",
		Followups: []FollowupInput{{ID: "spawned", Title: "후속", Body: "본문"}},
	}); err != nil {
		t.Fatalf("첫 마무리 실패: %v", err)
	}

	// 둘째 마무리는 후속을 **안 싣는다**. 관문이 여기서 돈다.
	// `spawned` 는 이 세션이 만든 열린 항목이지만 **이미 판단에 매달렸다** —
	// 바닥에 떨어진 것이 아니므로 붙잡으면 거짓 거절이다.
	if _, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch8",
		Outcome: model.ItemDone, Title: "batch8", Body: "①…②…③…④…",
	}); err != nil {
		t.Fatalf("이미 판단에 매달린 항목을 '바닥에 떨어뜨린 후속'으로 붙잡았다 — 거짓 거절이다: %v", err)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM event WHERE kind='item.finish_followups_missing'`); n != 0 {
		t.Errorf("관문이 %d번 발화했다 — 이미 이어진 항목은 그 대상이 아니다", n)
	}

	// 대조: **판단에 안 매달린** 항목은 여전히 붙잡아야 한다. 이 짝이 없으면 위 단정은
	// "관문을 통째로 껐다"로도 만족된다.
	addItem(t, s, "p", "batch9", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch9")
	addItemAs(t, s, "p", me.Session.ID, "dropped-on-the-floor")
	if _, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch9",
		Outcome: model.ItemDone, Title: "batch9", Body: "①…②…③…④…",
	}); err == nil {
		t.Fatalf("바닥에 떨어뜨린 후속이 있는데 관문이 안 막았다 — 관문이 통째로 꺼졌다")
	}
}
