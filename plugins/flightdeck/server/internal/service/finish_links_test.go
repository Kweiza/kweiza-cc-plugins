package service

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// TestFinishSurvivesALinkThatRepeatsTheItem 은 **오늘 판단이 사라지는 자리**를 잠근다.
//
// judgment_link 의 PK 는 (judgment_id, target_kind, target_id) 이고 AddJudgment 는
// 평범한 INSERT 다. finish 는 in.ItemID · in.Links · 후속 id 를 중복 제거 없이 이어 붙이므로,
// 링크 하나가 항목을 한 번 더 가리키면 ① 에서 ConflictDuplicate 가 나고 Store.Tx 가
// ①②③④ 를 통째로 롤백한다 — 넷 중 **판단만이 원리적으로 파생 불가**한데 그것이 사라진다.
//
// 잠금은 이 창을 못 닫는다. _txlock=immediate 가 배제하는 것은 **다른 커넥션**이고,
// 이 겹침은 한 호출 안에서 자기와 부딪힌다.
func TestFinishSurvivesALinkThatRepeatsTheItem(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	res, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "본문",
		Links: []model.JudgmentLink{{TargetKind: "item", TargetID: "batch7"}},
	})
	if err != nil {
		t.Fatalf("링크가 항목을 두 번 가리켰다고 마무리가 통째로 실패했다 — 판단이 사라진다: %v", err)
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 1 {
		t.Fatalf("판단이 %d건이다 — 1건이어야 한다", n)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM judgment_link WHERE judgment_id = ? AND target_id = 'batch7'`,
		res.Judgment.ID); n != 1 {
		t.Fatalf("판단 링크가 %d건이다 — 중복이 제거돼 1건이어야 한다", n)
	}
}

// TestFinishRefusesTheSameFollowupIDTwiceInOneCall 은 같은 호출의 자기 충돌을 그 자리에서 잡는다.
//
// ★ **오늘 이것은 흡수가 아니라 판단 소실이다.** 실측: 같은 id 를 두 번 실으면 ① 의 AddJudgment 가
// 링크 twin 을 두 번 INSERT 해 PK(schema.sql:271)에 부딪히고 tx 전체가 롤백된다 —
// 판단 0건 · 항목 0건 · 원래 항목은 claimed 인 채로 남는다. 오류도 RefusedError 가 아니라
// raw *store.ConflictError(code=1555)이고 그 문구에는 어느 id 인지가 안 나온다
// (writeErr 가 target=item/twin 을 담은 포맷 문자열을 버린다 — store/constraint.go:201).
// 즉 중복 후속 id 하나가 오늘 이미 판단을 통째로 없앤다.
//
// ★ Step 3 의 dedupeLinks 가 들어가면 링크는 살지만 두 번째 t.AddItem 이 자기 트랜잭션의
// 첫 INSERT 때문에 ConflictDuplicate 를 받아 흡수 갈래로 빠진다 — 세션은 "후속 2건"을 실었는데
// 응답은 1건 등록 + 1건 건너뜀이 되고, **그 건너뜀의 사유가 거짓**이다(남이 만든 것이 아니라
// 자기가 만들었다). 이 시험은 그 두 상태를 **둘 다** 잠근다.
func TestFinishRefusesTheSameFollowupIDTwiceInOneCall(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "본문",
		Followups: []FollowupInput{
			{ID: "twin", Title: "제목", Body: "본문"},
			{ID: "twin", Title: "제목", Body: "본문"},
		},
	})
	if err == nil {
		t.Fatalf("같은 후속 id 를 두 번 실었는데 통과했다")
	}
	if !strings.Contains(err.Error(), "twin") {
		t.Fatalf("거절 사유가 어느 id 인지 안 낸다:\n%s", err.Error())
	}
	// ★ 이 관문의 **고유 문구**를 못 박는다. 다른 관문(제목·본문 · 경로 · 자격)이 먼저
	//   거절해도 위 단정 셋은 전부 참이 되므로, 이 줄이 없으면 이 시험이 무엇을 잠그는지
	//   모르게 된다. Global Constraints 의 "빨강은 의도한 문구로 실패해야 한다"가 이것이다.
	if !strings.Contains(err.Error(), "같은 호출에 두 번 실렸다") {
		t.Fatalf("중복 관문이 아닌 다른 것이 거절했다 — 이 시험은 무엇도 안 잠근다:\n%s", err.Error())
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절했는데 판단이 %d건 남았다 — 트랜잭션 진입 전이라 아무것도 안 써야 한다", n)
	}
	if n := countRows(t, st, `SELECT count(*) FROM item WHERE id = 'twin'`); n != 0 {
		t.Fatalf("거절했는데 항목이 %d건 만들어졌다", n)
	}
}
