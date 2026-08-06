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

// TestFinishRefusesAFollowupThatBelongsToSomeoneElse 는 이 기능의 **안전 축**이다.
//
// ★ 회귀이기도 하다. 오늘은 남의 항목 id 를 후속으로 넣으면 항목만 안 만들어지고
// **판단 링크는 그대로 걸린다**(finish.go:199 가 AddItem 보다 먼저 링크를 짜고
// judgment_link.target_id 에 REFERENCES 가 없다). 즉 오타 하나로 남의 항목이 내 판단에
// 조용히 이어진다. 아래 링크 0건 단정이 그 문을 닫는다.
//
// ★ **title·body 를 일부러 채운다.** 안 채우면 지금 판이 finish.go:166 의
// "제목이나 본문이 없다" 로 **먼저** 거절하고, 그 사유 문자열에 id 가 박혀 있어
// 아래 Contains 까지 참이 된다 — err != nil · id 포함 · 판단 0건 · 링크 0건이
// 전부 만족돼 시험이 **구현 전에도 초록**이 된다(실측으로 셋 다 PASS 했다).
// 그러면 이 시험은 자격 축을 하나도 안 잠근다 — classifyFollowups 를 통째로 지워도 초록이다.
// 채우면 지금 판이 실제로 성공하고(err == nil) judgment_link 에 남의 항목이 걸려,
// 링크 0건 단정이 진짜로 회귀를 잠근다. 구현 뒤에는 classifyFollowups 거절이
// 제목·본문 관문보다 먼저 오므로(Step 4 의 ②→③ 순서) 이 값들은 경로를 안 바꾼다.
// id 만 싣는 잇기 경로는 Task 4 의 성공 시험이 잠근다.
func TestFinishRefusesAFollowupThatBelongsToSomeoneElse(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	other := openSession(t, s, "p", repo, repo, "cc-2", "트랙7")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")
	addItemAs(t, s, "p", other.Session.ID, "someone-elses")

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "본문",
		Followups: []FollowupInput{{ID: "someone-elses", Title: "제목", Body: "본문"}},
	})
	if err == nil {
		t.Fatalf("남이 만든 항목을 후속으로 이었는데 통과했다")
	}
	if !strings.Contains(err.Error(), "someone-elses") {
		t.Fatalf("거절 사유가 어느 id 인지 안 낸다:\n%s", err.Error())
	}
	// ★ **title/body 거절로 되돌아가는 회귀를 이 줄이 막는다.** "이을 자격이 없다" 는
	//   refuseIneligibleFollowup 의 Reason 에만 있고 다른 어떤 거절에도 없다.
	//   ("이을 수 있는 것은" 은 그 Guidance 첫 줄에도 있어 사유 갈래와 무관하게 늘 맞으므로
	//    쓰면 안 된다 — 이 셋 중 둘은 len(eligible)==0 갈래라 Reason 쪽 그 문구가 안 나온다.)
	if !strings.Contains(err.Error(), "이을 자격이 없다") {
		t.Fatalf("자격 축이 아닌 다른 관문이 먼저 거절했다 — 이 시험은 무엇도 안 잠근다:\n%s", err.Error())
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절인데 판단이 %d건 남았다 — 트랜잭션 진입 전이라 아무것도 안 써야 한다", n)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM judgment_link WHERE target_id = 'someone-elses'`); n != 0 {
		t.Fatalf("남의 항목에 판단 링크가 %d건 걸렸다 — 오타 하나로 남의 항목이 내 판단에 붙는다", n)
	}
}

// TestFinishRefusesWithUnobservedEligibilityWhenClaimIsUnreadable 는
// **observed bool 배선을 잠그는 유일한 통합 시험이다.**
//
// ★ 리뷰 실측: classifyFollowups 안에서 `eligible, _ := s.sessionSpawnedOpen(...)` 로
// 바꾸고 observed 를 true 로 하드코딩해도 이 시험 전에는 전 스위트가 초록이었다 —
// TestRefuseIneligibleFollowupSaysWhichOfTheThreeReasons 는 refuseIneligibleFollowup 을
// 순수 함수로 직접 불러 문구 세 갈래를 잠그지만, **classifyFollowups 가 sessionSpawnedOpen 의
// observed 를 실제로 실어 나르는지는** 그 시험도 다른 통합 시험도 안 본다(다른 통합 시험은
// 전부 claimed() 를 먼저 해서 observed=true 갈래만 밟는다). Task 2 가 이 bool 을 따로 만든
// 이유(관측 실패와 "만든 것이 없다"를 가르는 것)가 이 시험이 없으면 무방비였다.
//
// ★ observed=false 로 떨어지는 길: 이 항목을 claim 한 적이 없으면 sessionSpawnedOpen 이
// GetClaim 에서 오류를 받아 (nil, false) 를 낸다 — 아래에서 claimed() 를 일부러 안 부르는
// 것이 이 시험의 핵심이다(다른 모든 통합 시험은 claimed() 를 부른다).
func TestFinishRefusesWithUnobservedEligibilityWhenClaimIsUnreadable(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	other := openSession(t, s, "p", repo, repo, "cc-2", "트랙7")
	addItem(t, s, "p", "batch7", nil, nil) // ★ claimed() 를 안 부른다 — 선점 기록이 없어야 한다
	addItemAs(t, s, "p", other.Session.ID, "someone-elses")

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "본문",
		Followups: []FollowupInput{{ID: "someone-elses", Title: "제목", Body: "본문"}},
	})
	if err == nil {
		t.Fatalf("선점 기록이 없는 항목을 마무리하는데 통과했다")
	}
	if !strings.Contains(err.Error(), "못 읽어 자격을 판정할 수 없다") {
		t.Fatalf("관측 실패 갈래가 아닌 다른 사유로 거절했다:\n%s", err.Error())
	}
	// ★ 다른 갈래의 Reason 문구가 안 섞이는 것도 함께 단정한다 — 그래야 갈래가 진짜로
	//   갈렸다는 것이 잠긴다. ("이을 수 있는 것은" 은 여기서 못 쓴다 — refuseIneligibleFollowup
	//   의 Guidance 첫 줄에도 고정으로 박혀 있어 사유 갈래와 무관하게 늘 참이다. 위
	//   TestFinishRefusesAFollowupThatBelongsToSomeoneElse 의 주석이 같은 함정을 적어 뒀다.)
	if strings.Contains(err.Error(), "하나도 없다") {
		t.Fatalf("관측 실패 갈래인데 '만든 것이 없다' 갈래의 문구도 섞여 나온다 — 갈래가 실제로는 안 갈렸다:\n%s",
			err.Error())
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절인데 판단이 %d건 남았다 — 트랜잭션 진입 전이라 아무것도 안 써야 한다", n)
	}
}

// TestFinishRefusesAFollowupThatIsAlreadyClosed 는 닫힌 항목을 못 잇게 한다.
//
// 닫힌 것을 이으면 판단이 "이 작업이 낳은 후속"이라고 말하는 대상이 이미 끝난 일이 된다.
// 관문(sessionSpawnedOpen)도 같은 이유로 닫힌 것을 안 센다 — 두 목록이 한 정의에서 나온다.
func TestFinishRefusesAFollowupThatIsAlreadyClosed(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")
	addItemAs(t, s, "p", me.Session.ID, "already-landed")
	if err := st.SetItemState(ctx(), "p", "already-landed", model.ItemDone, "남이 끝냈다"); err != nil {
		t.Fatalf("전제 구성 실패: %v", err)
	}

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "본문",
		Followups: []FollowupInput{{ID: "already-landed", Title: "제목", Body: "본문"}},
	})
	if err == nil {
		t.Fatalf("이미 닫힌 항목을 후속으로 이었는데 통과했다")
	}
	// ★ 위 시험과 같은 이유로 자격 축 문구를 못 박는다 — title·body 를 빼면 다른 관문이
	//   먼저 거절해 이 시험이 구현 전에도 초록이 된다.
	if !strings.Contains(err.Error(), "이을 자격이 없다") {
		t.Fatalf("자격 축이 아닌 다른 관문이 먼저 거절했다 — 이 시험은 무엇도 안 잠근다:\n%s", err.Error())
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절인데 판단이 %d건 남았다", n)
	}
}

// TestFinishRefusesAFollowupMadeBeforeTheClaim 은 **선점 전**에 만든 자기 항목도 못 잇게 한다.
//
// 오래 사는 세션은 앞선 작업의 항목을 갖고 있다. 그것을 이으면 이번 판단이 낳지 않은 일까지
// "이 작업의 후속"이 된다 — 관문이 선점 시각으로 자르는 것과 같은 이유다.
func TestFinishRefusesAFollowupMadeBeforeTheClaim(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItemAs(t, s, "p", me.Session.ID, "from-earlier-work")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "본문",
		Followups: []FollowupInput{{ID: "from-earlier-work", Title: "제목", Body: "본문"}},
	})
	if err == nil {
		t.Fatalf("선점 전에 만든 항목을 후속으로 이었는데 통과했다")
	}
	// ★ 위 둘과 같은 이유로 자격 축 문구를 못 박는다.
	if !strings.Contains(err.Error(), "이을 자격이 없다") {
		t.Fatalf("자격 축이 아닌 다른 관문이 먼저 거절했다 — 이 시험은 무엇도 안 잠근다:\n%s", err.Error())
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절인데 판단이 %d건 남았다", n)
	}
}

// TestFinishStillRequiresTitleAndBodyForANewFollowup 은 이 태스크가 **안 뒤집는** 계약이다
// (설계 §6-4). 새로 만드는 후속은 여전히 title·body 가 필수다.
//
// ★ 이 관문의 문구 "제목이나 본문"을 단정하는 시험이 저장소에 **하나도 없다** —
// FollowupInput 을 쓰는 시험 13곳이 전부 Title·Body 를 채운다. 그런데 이 태스크는
// 그 검사를 in.Followups 루프에서 plan.Create 루프로 **옮기고**, Task 6 은
// followupSchema 의 required 를 id 하나로 낮춘다(tools.go:67). 그러면 title 없이 보내는 것이
// **처음으로 정상 경로**가 되고 이 서비스 계층 검사가 남는 유일한 관문이 되는데,
// 그 관문을 보는 시험이 없으면 조건을 흘려도(Create 가 비어 루프를 안 돌거나, c.Item 대신
// 다른 것을 보거나) 전 스위트가 초록이다. 스키마 required 를 단정하는 시험도 저장소에 없다.
//
// ★ **적격 잇기를 첫째에, 새 후속을 둘째에 놓는다.** 이것이 이 시험을 빨강으로도 만든다:
//
//	지금 판은 in.Followups 를 전수로 도므로 **첫째**(spun-off-axis)에서 먼저 죽어
//	"1번째 후속(spun-off-axis)에 제목이나 본문이 없다" 를 낸다 — 아래 "2번째 후속(brand-new)"
//	단정이 그것을 잡는다. 구현 뒤에는 첫째가 잇기로 빠지고 둘째만 관문을 지나며,
//	요청 좌표(followupCreate.Index)가 살아 있어야만 "2번째" 가 나온다.
//	그 필드를 이 태스크가 일부러 만들었는데 그것을 보는 단정이 여기 말고 없다.
func TestFinishStillRequiresTitleAndBodyForANewFollowup(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")
	addItemAs(t, s, "p", me.Session.ID, "spun-off-axis") // 선점 뒤 · 이 세션 · 열림 → 이을 수 있다

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "판단 본문",
		Followups: []FollowupInput{
			{ID: "spun-off-axis"}, // id 만 — 이것은 이어진다(제목·본문을 다시 안 적는다)
			{ID: "brand-new"},     // id 만 — 이것은 **새로 만들 것**이라 거절돼야 한다
		},
	})
	if err == nil {
		t.Fatalf("제목·본문 없는 새 후속이 통과했다 — 빈 항목이 큐에 들어간다")
	}
	msg := err.Error()
	for _, want := range []string{"2번째 후속(brand-new)", "제목이나 본문이 없다"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("거절 사유에 %q 가 없다:\n%s", want, msg)
		}
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절인데 판단이 %d건 남았다 — 트랜잭션 진입 전이라 아무것도 안 써야 한다", n)
	}
	if n := countRows(t, st, `SELECT count(*) FROM item WHERE id = 'brand-new'`); n != 0 {
		t.Fatalf("거절인데 항목이 %d건 만들어졌다", n)
	}
}

// TestFinishNamesTheLinkTargetWhenTheFollowupIDDoesNotExist 는 **오타의 사유가 갈리는 자리**다.
//
// id 만 실은 후속은 이제 "잇겠다"는 뜻이다(도구 스키마가 그렇게 가르친다 — Task 6).
// 그 id 가 오타면 그런 항목이 없어 분류가 '만들기'로 떨어지고, 거절은 "제목이나 본문이 없다"가
// 된다 — 세션은 진짜 사유를 못 받고 제목·본문을 지어내 **쌍둥이**를 만든다. 이으려던 항목은
// 큐에 그대로 남는다. 이 도구가 없애려는 부류의 조용한 거짓과 같은 모양이라 여기서 닫는다.
func TestFinishNamesTheLinkTargetWhenTheFollowupIDDoesNotExist(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")
	addItemAs(t, s, "p", me.Session.ID, "spun-off-axis") // 이을 수 있었던 것

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "판단 본문",
		Followups: []FollowupInput{{ID: "spun-of-axis"}}, // 오타 — 이런 항목은 없다
	})
	if err == nil {
		t.Fatalf("없는 id 를 id 만 실었는데 통과했다")
	}
	for _, want := range []string{"이을 셈이었다면", "spun-off-axis"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("거절이 %q 를 안 낸다 — 세션이 제목·본문을 지어내 쌍둥이를 만든다:\n%s", want, err.Error())
		}
	}
	if n := countRows(t, st, `SELECT count(*) FROM item WHERE id = 'spun-of-axis'`); n != 0 {
		t.Fatalf("오타 id 로 항목이 %d건 만들어졌다", n)
	}
}

// TestRefuseIneligibleFollowupSaysWhichOfTheThreeReasons 는
// **`observed` bool 을 지키는 유일한 자리다.**
//
// 위 통합 시험 셋은 전부 `observed=true · eligible 빈 목록` 한 갈래만 밟는다(셋 다 claimed 를
// 먼저 하고, 자격자가 있으면 거절이 아니라 잇기가 되기 때문이다). 관측 실패 갈래와 이름을 내는
// 갈래는 통합 경로로 안 걸린다. 그래서 세 갈래를 여기서 **순수 함수로 직접** 부른다 —
// 이 단정이 없으면 다음 개정이 sessionSpawnedOpen 을 []string 하나로 되접어도 전부 초록이다.
// 같은 빈 값이 두 뜻을 갖는 모양은 이 저장소가 반복해서 닫아 온 실패고,
// render_accounting_test.go:245 가 StillHeld 의 nil 갈래를 같은 방식으로 직접 단정한다.
func TestRefuseIneligibleFollowupSaysWhichOfTheThreeReasons(t *testing.T) {
	unobserved := refuseIneligibleFollowup(1, "x", "batch7", nil, false)
	none := refuseIneligibleFollowup(1, "x", "batch7", nil, true)
	named := refuseIneligibleFollowup(2, "x", "batch7", []string{"spun-off-axis"}, true)

	if !strings.Contains(unobserved.Reason, "못 읽어") {
		t.Fatalf("관측 실패를 사유로 안 낸다:\n%s", unobserved.Reason)
	}
	if unobserved.Reason == none.Reason {
		t.Fatalf("관측 실패와 '만든 것이 없다'가 같은 문구다 — 세션이 없는 사고를 쫓는다:\n%s",
			unobserved.Reason)
	}
	if !strings.Contains(none.Reason, "하나도 없다") {
		t.Fatalf("관측은 했는데 자격자가 없다는 사실을 안 낸다:\n%s", none.Reason)
	}
	if !strings.Contains(named.Reason, "spun-off-axis") {
		t.Fatalf("이을 수 있는 것의 이름을 안 낸다 — 수만 말하면 다시 조사해야 한다:\n%s",
			named.Reason)
	}
	if !strings.Contains(named.Reason, "2번째") {
		t.Fatalf("요청 좌표(몇 번째 후속인지)를 잃었다:\n%s", named.Reason)
	}
}
