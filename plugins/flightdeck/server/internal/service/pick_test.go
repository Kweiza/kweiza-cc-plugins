package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// lastEval 은 원장에 마지막으로 쌓인 판정을 읽는다.
// pick_eval 의 소비자는 **SQL 질의**다(§10: "사유 분포가 질의 가능해진다") —
// 그래서 이 단정은 서비스 반환값이 아니라 표를 직접 본다.
func lastEval(t *testing.T, st *store.Store) (picked string, rejected []model.Rejection) {
	t.Helper()
	var p sql.NullString
	var raw string
	err := st.DB().QueryRowContext(ctx(),
		`SELECT picked, rejected FROM pick_eval ORDER BY id DESC LIMIT 1`).Scan(&p, &raw)
	if err != nil {
		t.Fatalf("pick_eval 읽기 실패: %v", err)
	}
	if err := json.Unmarshal([]byte(raw), &rejected); err != nil {
		t.Fatalf("탈락 사유 해석 실패(%s): %v", raw, err)
	}
	return p.String, rejected
}

func reasonCodes(rs []model.Rejection) map[string]string {
	out := map[string]string{}
	for _, r := range rs {
		out[r.Item+"/"+r.Reason] = r.Detail
	}
	return out
}

func TestPickRecordsEvalAndEveryRejectionWhenNothingIsEligible(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	other := openSession(t, s, "p", repo, repo, "cc-2", "트랙7")

	// 남이 선점한 항목
	addItem(t, s, "p", "a-dep", []string{"pipeline/"}, nil)
	if _, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: other.Session.ID, ItemID: "a-dep"}); err != nil {
		t.Fatalf("남의 선점 준비 실패: %v", err)
	}
	// 그 항목을 기다리는 항목
	addItem(t, s, "p", "b-work", []string{"services/"}, []model.After{{Item: "a-dep"}})
	// 폐기된 선행을 기다리는 항목 — 기다려도 **영영 안 풀린다**
	addItem(t, s, "p", "z-dropped", nil, nil)
	if err := st.SetItemState(ctx(), "p", "z-dropped", model.ItemDropped, "중복이라 버린다"); err != nil {
		t.Fatalf("폐기 준비 실패: %v", err)
	}
	addItem(t, s, "p", "c-blocked", []string{"console/"}, []model.After{{Item: "z-dropped"}})

	// ★ 대조가 성립하는지 **결과를 읽기 전에** 단정한다.
	//   원장이 비어 있어야 아래에서 읽는 행이 이번 호출의 것임이 보장된다.
	if n := countRows(t, st, `SELECT count(*) FROM pick_eval`); n != 0 {
		t.Fatalf("사전 조건이 깨졌다 — pick_eval 에 이미 %d행이 있다", n)
	}

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("추천 실패: %v", err)
	}

	if res.Mode != PickNone || res.Item != nil {
		t.Fatalf("적격이 0건이어야 한다: mode=%s item=%+v", res.Mode, res.Item)
	}
	if res.Reason == "" {
		t.Fatalf("왜 못 골랐는지가 비었다 — 사유 없는 큐는 두 번째 세션부터 무시된다")
	}
	if !strings.Contains(res.Scope, "살아 있지 않은 세션") {
		t.Fatalf("무엇을 후보로 봤는지가 안 실렸다: %q", res.Scope)
	}

	// 반환값의 탈락 사유 — 처방이 다른 셋이 각각 다른 코드로 나와야 한다.
	got := reasonCodes(res.Rejected)
	for _, want := range []string{
		"a-dep/" + judge.RejectClaimed,
		"b-work/" + judge.AfterUnmetItem,
		"c-blocked/" + judge.AfterDroppedDep,
	} {
		if _, ok := got[want]; !ok {
			t.Fatalf("탈락 사유 %q 가 없다: %+v", want, res.Rejected)
		}
	}
	if d := got["a-dep/"+judge.RejectClaimed]; !strings.Contains(d, other.Session.ID) {
		t.Fatalf("누가 선점했는지가 사유에 없다(%q) — 그러면 누구에게 물어야 할지 모른다", d)
	}

	// 원장 — 추천을 못 해도 남는다. 적격 0건도 기록이다.
	if n := countRows(t, st, `SELECT count(*) FROM pick_eval`); n != 1 {
		t.Fatalf("pick_eval 이 %d행이다, 기대 1행", n)
	}
	picked, stored := lastEval(t, st)
	if picked != "" {
		t.Fatalf("적격 0건인데 picked=%q 가 적혔다", picked)
	}
	if len(stored) != len(res.Rejected) {
		t.Fatalf("원장의 탈락 사유가 %d줄, 반환값은 %d줄 — 화면과 원장이 갈리면 분포를 못 믿는다",
			len(stored), len(res.Rejected))
	}
	storedCodes := reasonCodes(stored)
	for k := range got {
		if _, ok := storedCodes[k]; !ok {
			t.Fatalf("원장에 %q 가 안 남았다: %+v", k, stored)
		}
	}
}

func TestPickReturnsResumeContextForOwnClaim(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", []string{"pipeline/"}, nil)

	first, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemID: "batch7"})
	if err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	if first.Mode != PickClaimed || first.Claim == nil {
		t.Fatalf("첫 호출은 선점이어야 한다: %+v", first)
	}
	// 이 항목에 판단을 하나 걸어 둔다 — 재개가 그것을 다시 내야 한다.
	if _, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentNotDone,
		Title: "일부러 안 한 것", Body: "DLQ 재처리는 계약 대기라 손대지 않았다", ItemID: "batch7",
	}); err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}

	// 컨텍스트가 날아가 같은 워크트리로 돌아온 세션이 여기 온다.
	again, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemID: "batch7"})
	if err != nil {
		t.Fatalf("재개는 거절이 아니라 맥락 재출력이어야 한다: %v", err)
	}
	if again.Mode != PickResumed {
		t.Fatalf("mode = %s, 기대 %s", again.Mode, PickResumed)
	}
	if again.Item == nil || again.Item.ID != "batch7" || again.Branch != "batch7" {
		t.Fatalf("항목 본문·브랜치가 안 실렸다: %+v", again.Item)
	}
	if len(again.Setup) == 0 {
		t.Fatalf("워크트리 준비 명령이 안 실렸다")
	}
	if len(again.Notes) != 1 || !strings.Contains(again.Notes[0].Body, "DLQ 재처리") {
		t.Fatalf("연결된 판단 전문이 안 실렸다: %+v", again.Notes)
	}
	// 선점 시각을 덮으면 "언제부터 쥐고 있나"가 사라진다(회수 판단 다섯 축 중 하나다).
	if again.Claim == nil || !again.Claim.At.Equal(first.Claim.At) {
		t.Fatalf("재개가 선점 시각을 덮었다: %v → %v", first.Claim.At, again.Claim.At)
	}
}

func TestPickReportsOverlapWithoutFilteringIt(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	other := openSession(t, s, "p", repo, repo, "cc-2", "트랙7")

	// 남이 이미 만지고 있는 경로
	if err := s.Beat(ctx(), other.Session.ID, model.SignalTool,
		[]string{filepath.Join(repo, "pipeline", "run.py")}); err != nil {
		t.Fatalf("비트 실패: %v", err)
	}
	addItem(t, s, "p", "batch7", []string{"pipeline/"}, nil)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemID: "batch7"})
	if err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	// ★ 거르지 않는다. 실제 텍스트 충돌은 8일에 1건이라 배제는 과잉이다.
	if res.Mode != PickClaimed {
		t.Fatalf("겹친다고 선점을 거절하면 안 된다: %s", res.Mode)
	}
	// ★ 그리고 침묵하지 않는다. 침묵하면 "겹침 없음"과 "이 축을 안 본다"가 구분되지 않는다.
	if len(res.Overlaps) != 1 || res.Overlaps[0].SessionID != other.Session.ID {
		t.Fatalf("겹침이 안 실렸다: %+v", res.Overlaps)
	}
	if len(res.Overlaps[0].Pairs) == 0 {
		t.Fatalf("무엇이 겹쳤는지 쌍이 없다 — 세션 id 만으로는 무엇을 조심할지 알 수 없다")
	}
	pair := res.Overlaps[0].Pairs[0]
	if pair[0] != "pipeline/" || pair[1] != "pipeline/run.py" {
		t.Fatalf("겹친 쌍이 틀렸다: %v", pair)
	}
}

// TestPickDoesNotReportSiblingCardAsOverlap 은 **배선**을 잠근다.
//
// judge 쪽 판정(sameConversation)은 eligible_test 가 본다. 이 시험은 그 판정이
// 실제 진입점으로 들어오는지를 본다 — `liveFor` 가 cc 를 안 채우거나 `selfCCOf` 가
// 안 불리면 판정은 멀쩡한데 화면은 그대로 거짓말을 한다. 머신 축이 남긴 교훈이다:
// 시험이 구조체를 직접 조립하면 "호출부가 정말 채우는가"를 원리적으로 못 본다.
//
// 재현하는 것은 실측된 그 모양이다 — 같은 대화(cc-1)가 워크트리 두 깊이로 갈려
// 카드 두 장이 된 상태(2026-08-05: 카드 34장 = 대화 11개).
func TestPickDoesNotReportSiblingCardAsOverlap(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")

	me := openSession(t, s, "p", repo, wt, "cc-1", "내 카드")
	sibling := openSession(t, s, "p", repo, repo, "cc-1", "같은 대화의 다른 카드")
	other := openSession(t, s, "p", repo, repo, "cc-2", "진짜 남")

	// 형제와 남이 **같은 경로**를 만진다. 판정이 안 돌면 둘 다 겹침으로 나온다.
	for _, id := range []string{sibling.Session.ID, other.Session.ID} {
		if err := s.Beat(ctx(), id, model.SignalTool,
			[]string{filepath.Join(repo, "pipeline", "run.py")}); err != nil {
			t.Fatalf("비트 실패: %v", err)
		}
	}
	addItem(t, s, "p", "batch7", []string{"pipeline/"}, nil)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemID: "batch7"})
	if err != nil {
		t.Fatalf("선점 실패: %v", err)
	}

	// 형제 카드가 겹침으로 나오면 세션이 자기 자신과 조율하게 된다.
	for _, ov := range res.Overlaps {
		if ov.SessionID == sibling.Session.ID {
			t.Fatalf("형제 카드가 겹침으로 나왔다 — 자기 자신과 조율하라는 화면이다: %+v", res.Overlaps)
		}
	}
	// ★ 그리고 진짜 남은 **반드시 남아야 한다.** 이 단정이 없으면 겹침 축을 통째로
	// 꺼 버리는 변경이 위 단정만 보고 초록으로 지나간다.
	if len(res.Overlaps) != 1 || res.Overlaps[0].SessionID != other.Session.ID {
		t.Fatalf("진짜 남과의 겹침이 사라졌다 — 형제를 빼면서 축을 통째로 껐다: %+v", res.Overlaps)
	}
}

func TestPickRecommendationDoesNotClaim(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", []string{"pipeline/"}, nil)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("추천 실패: %v", err)
	}
	if res.Mode != PickRecommended || res.Item == nil || res.Item.ID != "batch7" {
		t.Fatalf("추천이 안 나왔다: %+v", res)
	}
	if res.Claim != nil {
		t.Fatalf("인자 없는 pick 은 선점하지 않는다: %+v", res.Claim)
	}
	if !strings.Contains(res.Reason, "아직 선점하지 않았다") {
		t.Fatalf("선점 여부가 응답에 없다: %q", res.Reason)
	}
	if _, err := st.GetClaim(ctx(), "p", "batch7"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("추천이 선점 행을 만들었다: %v", err)
	}
	// 원장에는 남는다.
	picked, _ := lastEval(t, st)
	if picked != "batch7" {
		t.Fatalf("추천이 원장에 안 남았다: %q", picked)
	}
}

func TestPickRefusesSomeoneElsesClaimWithHolder(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	other := openSession(t, s, "p", repo, repo, "cc-2", "트랙7")
	addItem(t, s, "p", "batch7", nil, nil)

	if _, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: other.Session.ID, ItemID: "batch7"}); err != nil {
		t.Fatalf("남의 선점 준비 실패: %v", err)
	}
	_, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemID: "batch7"})
	if err == nil {
		t.Fatalf("남이 쥔 항목은 거절돼야 한다")
	}
	var held *store.ClaimHeldError
	if !errors.As(err, &held) {
		t.Fatalf("점유자를 담은 오류여야 한다: %T %v", err, err)
	}
	if held.Holder != other.Session.ID {
		t.Fatalf("점유자가 틀렸다: %q", held.Holder)
	}
}

func TestAddItemRefusesUnsafeIDAndEmptyBody(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	openSession(t, s, "p", repo, repo, "cc-1", "")

	if _, err := s.AddItem(ctx(), AddItemInput{Project: "p", ID: "a;rm -rf /", Title: "t", Body: "b"}); err == nil {
		t.Fatalf("셸 메타문자가 든 id 는 거절돼야 한다")
	}
	if _, err := s.AddItem(ctx(), AddItemInput{Project: "p", ID: "ok-1", Title: "t", Body: ""}); err == nil {
		t.Fatalf("본문 없는 항목은 거절돼야 한다")
	} else if !strings.Contains(err.Error(), "본문") {
		t.Fatalf("거절 사유가 공허하다: %v", err)
	}
	// 표 밖 케이스: 선행 조건에 두 축을 채우면(브랜치 이름을 우회로 넣으려는 시도가 이 모양이다) 거절이다.
	_, err := s.AddItem(ctx(), AddItemInput{
		Project: "p", ID: "ok-2", Title: "t", Body: "b",
		After: []model.After{{Item: "x", SHA: "deadbeef"}},
	})
	if err == nil || !strings.Contains(err.Error(), "선행") {
		t.Fatalf("축 두 개짜리 선행은 거절돼야 한다: %v", err)
	}
}

func TestPickSurfacesMissingDependencyInsteadOfCallingItWaiting(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	// 존재하지 않는 선행 — 오타다. "기다리면 풀린다"로 보이면 그 항목이 영구히 굶는다.
	addItem(t, s, "p", "b-work", nil, []model.After{{Item: "없는항목"}})

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("추천 실패: %v", err)
	}
	if res.Mode != PickNone {
		t.Fatalf("선행이 안 풀렸으므로 적격 0건이어야 한다: %s", res.Mode)
	}
	var found bool
	for _, f := range res.Failures {
		if strings.Contains(f.Axis, "after-item") && strings.Contains(f.Detail, "큐에 없다") {
			found = true
		}
	}
	if !found {
		t.Fatalf("없는 선행이 표면에 안 나왔다: %+v", res.Failures)
	}
	// 그리고 사유 코드는 "조회하지 않았다" 그대로여야 한다 — 조회 결과를 지어내지 않는다.
	if _, ok := reasonCodes(res.Rejected)["b-work/"+judge.AfterUnknown]; !ok {
		t.Fatalf("탈락 사유가 %s 여야 한다: %+v", judge.AfterUnknown, res.Rejected)
	}
}

// TestPickCountsTheQueueAfterTheClaimNotBefore 는 이 설계에서 가장 쉽게 틀리는 자리다.
//
// 진입부에서 세면 방금 집은 항목이 아직 state='open' 이라 카운트에 들어간다
// (ClaimItem 이 open→claimed 로 옮긴다). 그러면 pick 응답이 board 보다 정확히 1 크고,
// 같은 응답의 항목 블록은 [claimed] 라고 찍혀 있다 — 한 화면이 자기를 반박한다.
func TestPickCountsTheQueueAfterTheClaimNotBefore(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	for _, id := range []string{"a", "b", "c"} {
		addItem(t, s, "p", id, nil, nil)
	}

	got, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemID: "a"})
	if err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	if got.Mode != PickClaimed {
		t.Fatalf("mode = %s, 기대 %s", got.Mode, PickClaimed)
	}
	if got.QueueOpen == nil {
		t.Fatal("큐 열림 수가 안 실렸다 — nil 은 '이 응답에 없다'는 뜻이고 선점 경로에는 있어야 한다")
	}
	if *got.QueueOpen != 2 {
		t.Fatalf("큐 열림 %d건 (기대 2) — 방금 집은 a 를 아직 열림으로 세고 있다", *got.QueueOpen)
	}
}

// TestPickResumeReportsTheSameQueueSizeAsTheClaim 은 재개가 **재출력**임을 단정한다.
//
// pick.go 는 재개 경로가 "아무것도 쓰지 않는다"고 못박아 뒀다. 재출력이 원본과 다른 수를
// 내면 그건 재출력이 아니고, 이 경로에 오는 세션은 정의상 앞 응답의 기억이 없어서
// 그 차이를 "큐가 하나 줄었다"로 읽는다 — 아무도 아무것도 안 끝냈는데.
func TestPickResumeReportsTheSameQueueSizeAsTheClaim(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	for _, id := range []string{"a", "b", "c"} {
		addItem(t, s, "p", id, nil, nil)
	}

	first, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemID: "a"})
	if err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	again, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemID: "a"})
	if err != nil {
		t.Fatalf("재개 실패: %v", err)
	}
	if again.Mode != PickResumed {
		t.Fatalf("mode = %s, 기대 %s", again.Mode, PickResumed)
	}
	if first.QueueOpen == nil || again.QueueOpen == nil {
		t.Fatalf("큐 열림 수가 안 실렸다: 선점 %v, 재개 %v", first.QueueOpen, again.QueueOpen)
	}
	if *first.QueueOpen != *again.QueueOpen {
		t.Fatalf("선점 %d건 → 재개 %d건 — 그 사이에 add 도 finish 도 없었다",
			*first.QueueOpen, *again.QueueOpen)
	}
}

// TestPickRecommendationQueueSizeCannotDivergeFromItsOwnScopeLine 은
// 추천 응답의 두 줄이 **같은 관측**에서 나왔음을 단정한다.
//
// 진입부에서 따로 세면 그 사이에 sessionCards(이 서버에서 가장 비싼 함수)가 끼어들어
// 인접한 두 줄이 다른 수를 찍을 수 있다. candidates() 가 쥔 값을 그대로 쓰면 구조적으로 못 갈린다.
func TestPickRecommendationQueueSizeCannotDivergeFromItsOwnScopeLine(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	for _, id := range []string{"a", "b", "c"} {
		addItem(t, s, "p", id, nil, nil)
	}

	got, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("추천 실패: %v", err)
	}
	if got.Mode != PickRecommended {
		t.Fatalf("mode = %s, 기대 %s", got.Mode, PickRecommended)
	}
	if got.QueueOpen == nil {
		t.Fatal("큐 열림 수가 안 실렸다")
	}
	if *got.QueueOpen != 3 {
		t.Fatalf("큐 열림 %d건 (기대 3) — 추천은 선점하지 않으므로 셋 다 열림이다", *got.QueueOpen)
	}
	if !strings.Contains(got.Scope, "열린 항목 3건") {
		t.Fatalf("범위 문자열과 큐 수가 갈렸다: %q vs %d건", got.Scope, *got.QueueOpen)
	}
}

// TestPickNoneModeQueueSizeCannotDivergeFromItsOwnScopeLine 은 none 모드도
// QueueOpen 을 낸다는 것과, 그 수가 자기 사유 안의 수와 갈리지 않는다는 것을 단정한다.
//
// none 모드는 다른 모드보다 갈림이 더 위험하다 — Reason 자체가 Scope 문장을 그대로
// 이어붙이므로(pick.go:329-330) QueueOpen 이 어긋나면 **한 응답 안에서 서로 다른 두 숫자가
// 나란히 찍힌다.** recommended 모드는 그 옆에 실제 항목이라도 있어 사람이 위화감을 느낄
// 여지가 있지만, none 모드에는 보여줄 항목 자체가 없어 그 모순을 알아챌 다른 단서가 없다.
func TestPickNoneModeQueueSizeCannotDivergeFromItsOwnScopeLine(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")

	// 서로가 서로의 선행이 되는 순환 — 영영 안 풀린다(TestPickRecordsEvalAndEveryRejectionWhenNothingIsEligible
	// 이 쓴 것과 같은 수법: 선행이 안 끝나 대기 상태로 남기되, 둘 다 열린 채로 둔다).
	addItem(t, s, "p", "a", nil, []model.After{{Item: "b"}})
	addItem(t, s, "p", "b", nil, []model.After{{Item: "a"}})

	got, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("추천 실패: %v", err)
	}
	if got.Mode != PickNone {
		t.Fatalf("mode = %s, 기대 %s", got.Mode, PickNone)
	}
	if got.QueueOpen == nil {
		t.Fatal("큐 열림 수가 안 실렸다 — none 모드도 예외가 아니다")
	}
	if *got.QueueOpen != 2 {
		t.Fatalf("큐 열림 %d건 (기대 2) — 순환 대기 중인 둘 다 열림이다", *got.QueueOpen)
	}
	want := fmt.Sprintf("열린 항목 %d건", *got.QueueOpen)
	if !strings.Contains(got.Reason, want) {
		t.Fatalf("사유 문자열과 큐 수가 갈렸다: %q vs %d건", got.Reason, *got.QueueOpen)
	}
	if !strings.Contains(got.Scope, want) {
		t.Fatalf("범위 문자열과 큐 수가 갈렸다: %q vs %d건", got.Scope, *got.QueueOpen)
	}
}

// pick 은 세 모드 전부에서 경로 실재 판정을 낸다.
// none(적격 0건)에는 항목이 없으므로 안 낸다 — 관측할 대상이 없다.
func TestPickCarriesPathCheckInEveryModeThatHasAnItem(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "proj", repo, repo, "cc-1", "")
	writeFile(t, repo, "internal/x/y.go", "package x\n")
	addItem(t, s, "proj", "t-here", []string{"internal/x/y.go"}, nil)

	// ① 추천
	rec, err := s.Pick(ctx(), PickInput{Project: "proj", SessionID: sess.Session.ID})
	if err != nil {
		t.Fatalf("추천 실패: %v", err)
	}
	if rec.PathCheck == nil {
		t.Fatal("추천에 경로 실재 판정이 없다")
	}
	if rec.PathCheck.Kind != judge.KindOK {
		t.Fatalf("Kind 가 %q 다 — ok 여야 한다: %s", rec.PathCheck.Kind, rec.PathCheck.Summary)
	}

	// ② 선점
	cl, err := s.Pick(ctx(), PickInput{Project: "proj", SessionID: sess.Session.ID, ItemID: "t-here"})
	if err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	if cl.Mode != PickClaimed || cl.PathCheck == nil {
		t.Fatalf("선점에 경로 실재 판정이 없다(mode=%s)", cl.Mode)
	}

	// ③ 재개 — 같은 세션이 다시 부른다
	re, err := s.Pick(ctx(), PickInput{Project: "proj", SessionID: sess.Session.ID, ItemID: "t-here"})
	if err != nil {
		t.Fatalf("재개 실패: %v", err)
	}
	if re.Mode != PickResumed || re.PathCheck == nil {
		t.Fatalf("재개에 경로 실재 판정이 없다(mode=%s)", re.Mode)
	}
}

// 적격 0건에는 항목이 없다 → 판정도 없다. nil 이어야 한다.
func TestPickNoneHasNoPathCheck(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "proj", repo, repo, "cc-1", "")

	res, err := s.Pick(ctx(), PickInput{Project: "proj", SessionID: sess.Session.ID})
	if err != nil {
		t.Fatalf("추천 실패: %v", err)
	}
	if res.Mode != PickNone {
		t.Fatalf("mode 가 %q 다 — none 이어야 한다(큐가 비었다)", res.Mode)
	}
	if res.PathCheck != nil {
		t.Fatalf("항목이 없는데 경로 실재 판정이 실렸다: %+v", res.PathCheck)
	}
}

// item.paths 는 가장 큰 경로 컬럼인데 검증이 하나도 없었다.
// 여기를 통과시키면 그 항목의 겹침 축이 영영 죽는다 — 조용히.
func TestAddItemRejectsNonSlashCoordinatePaths(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	openSession(t, s, "p", repo, repo, "cc-1", "")
	cases := []struct {
		name  string
		paths []string
		want  string
	}{
		{"드라이브 절대경로", []string{`C:\repo\x.go`}, "드라이브 절대경로"},
		{"UNC", []string{`\\host\share\x.go`}, "UNC"},
		{"상대 백슬래시", []string{`internal\api\x.go`}, "백슬래시"},
		{"정상 경로 뒤에 섞여 있어도", []string{"internal/api/x.go", `b\c.go`}, "백슬래시"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.AddItem(ctx(), AddItemInput{
				Project: "p", ID: fmt.Sprintf("fd-x%d", i), Title: "t", Body: "b",
				Paths: c.paths,
			})
			if err == nil {
				t.Fatal("잘못된 좌표계의 경로를 통과시켰다")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("오류 %q 가 원인(%q)을 안 짚는다", err.Error(), c.want)
			}
		})
	}
}

// 몇 번째 경로가 문제인지 말해야 한다 — 목록이 길면 "어딘가 틀렸다"로는 못 고친다.
func TestAddItemSaysWhichPathIsWrong(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	openSession(t, s, "p", repo, repo, "cc-1", "")
	_, err := s.AddItem(ctx(), AddItemInput{
		Project: "p", ID: "fd-which", Title: "t", Body: "b",
		Paths: []string{"a/b.go", "c/d.go", `e\f.go`},
	})
	if err == nil {
		t.Fatal("통과시켰다")
	}
	// ★ 틀린 것은 목록의 **세 번째**(`e\f.go`)다. 그러므로 "3번째"라고 말해야 한다 —
	// 전에는 range 인덱스를 그대로 실어 "2번째"라고 했고, 그 말을 믿은 사람은
	// 멀쩡한 `c/d.go` 를 고치러 갔다.
	if !strings.Contains(err.Error(), "3번째") {
		t.Errorf("몇 번째 경로인지 안 말한다(1-based 여야 한다): %s", err.Error())
	}
}

// 정상 경로는 그대로 들어간다.
func TestAddItemAcceptsSlashCoordinatePaths(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	openSession(t, s, "p", repo, repo, "cc-1", "")
	it, err := s.AddItem(ctx(), AddItemInput{
		Project: "p", ID: "fd-ok", Title: "t", Body: "b",
		Paths: []string{"internal/api/x.go", "Makefile", "tools/"},
	})
	if err != nil {
		t.Fatalf("정상 경로를 거절했다: %v", err)
	}
	if len(it.Paths) != 3 {
		t.Fatalf("경로 %d개, want 3개", len(it.Paths))
	}
}

// makeSiblings 는 항목들을 형제로 만든다.
// finish 가 만드는 모양 그대로다 — 끝낸 항목과 후속 전부가 한 handoff 판단에 매달린다
// (finish.go:148-152). 이 관계의 생산자는 실질적으로 finish 하나뿐이다.
func makeSiblings(t *testing.T, st *store.Store, project string, items ...string) {
	t.Helper()
	links := make([]model.JudgmentLink, 0, len(items))
	for _, id := range items {
		links = append(links, model.JudgmentLink{TargetKind: "item", TargetID: id})
	}
	if _, err := st.AddJudgment(ctx(), model.Judgment{
		Project: project, Kind: model.JudgmentHandoff,
		Title: "쪼갰다", Body: "이건 따로 빼자", Links: links,
	}); err != nil {
		t.Fatalf("형제 준비 실패(%v): %v", items, err)
	}
}

// 형제 둘이 열려 있으면 추천이 묶음으로 온다.
func TestPickRecommendsBundleOfSiblings(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "묶음시험")

	addItem(t, s, "p", "b1-sib", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "b2-sib", []string{"services/b.go"}, nil)
	makeSiblings(t, st, "p", "b1-sib", "b2-sib")

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if res.Bundle == nil {
		t.Fatal("묶음 축이 nil 이다 — 서버가 그 축을 읽었으면 non-nil 이어야 한다")
	}
	if len(res.Bundle.Members) != 1 {
		t.Fatalf("형제 하나가 구성원이어야 한다: %+v", res.Bundle.Members)
	}
	m := res.Bundle.Members[0]
	if m.Link.Detail == "" {
		t.Fatal("왜 묶였는지가 비었다")
	}
	if len(m.Link.Axes) == 0 || m.Link.Axes[0] != judge.AxisSibling {
		t.Fatalf("형제 축이 안 붙었다: %v", m.Link.Axes)
	}
	// 브랜치는 선두 하나다.
	if res.Branch != res.Item.ID {
		t.Fatalf("브랜치가 %q 인데 선두는 %q 다", res.Branch, res.Item.ID)
	}
}

// ★ 이 시험이 부재 규율을 지킨다.
// 묶을 게 없어도 Bundle 은 non-nil 이고 구성원이 0건이다.
// nil 은 "이 응답은 그 축을 안 읽었다" 하나만 뜻해야 한다.
func TestPickSoloStillCarriesBundleAxis(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "단독시험")
	addItem(t, s, "p", "alone", []string{"services/x.go"}, nil)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if res.Bundle == nil {
		t.Fatal("단독인데 묶음 축이 nil 이다 — '묶을 게 없다'와 '안 읽었다'가 접혔다")
	}
	if len(res.Bundle.Members) != 0 {
		t.Fatalf("단독인데 구성원이 있다: %+v", res.Bundle.Members)
	}
}

// 추천은 아직 안 집은 것이므로 구성원의 판단 전문을 안 싣는다(컨텍스트 예산 — 설계 §6).
func TestPickRecommendDoesNotLoadMemberNotes(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "판단시험")
	addItem(t, s, "p", "n1", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "n2", []string{"services/b.go"}, nil)
	makeSiblings(t, st, "p", "n1", "n2")

	// 구성원 쪽에만 걸리는 판단을 하나 더 둔다.
	if _, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentNotDone,
		Title: "일부러 안 한 것", Body: "여기는 손대지 않았다", ItemID: "n2",
	}); err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	// ★ 사전 조건. 이게 없으면 "형제 축이 아예 안 붙어 구성원이 0건"인 회귀에서도
	// 아래 루프가 0번 돌아 이 시험이 그린으로 남는다 — 아무것도 확인하지 않은 채.
	if res.Bundle == nil || len(res.Bundle.Members) == 0 {
		t.Fatalf("사전 조건이 깨졌다 — 구성원이 있어야 이 시험이 뭔가를 확인한다: %+v", res.Bundle)
	}
	for _, m := range res.Bundle.Members {
		if len(m.Notes) != 0 {
			t.Fatalf("추천인데 구성원 %q 의 판단 전문을 실었다(%d건)", m.Item.ID, len(m.Notes))
		}
	}
}

// 원장에 선두와 나머지가 갈려 남는다. pick_eval 의 소비자는 SQL 질의다.
func TestPickRecordsBundleInPickEval(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "원장시험")
	addItem(t, s, "p", "e1", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "e2", []string{"services/b.go"}, nil)
	makeSiblings(t, st, "p", "e1", "e2")

	// 대조가 성립하는지 결과를 읽기 전에 단정한다.
	if n := countRows(t, st, `SELECT count(*) FROM pick_eval`); n != 0 {
		t.Fatalf("원장이 비어 있어야 한다: %d행", n)
	}
	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	lead := res.Item.ID
	if n := countRows(t, st,
		`SELECT count(*) FROM pick_eval WHERE picked = ?`, lead); n != 1 {
		t.Fatalf("picked 에 선두 %q 가 안 남았다", lead)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM pick_eval WHERE picked_with IS NOT NULL AND picked_with <> '[]'`); n != 1 {
		t.Fatalf("picked_with 에 나머지가 안 남았다")
	}
}

// siblingIndex 는 오류를 안 돌려준다 — 조회가 실패하면 빈 색인을 낸다.
//
// s.siblingIndex 를 직접 부른다(Pick 전체를 거치지 않는다). judgment_link 표를
// 지우면 뒤이어 pickRecommend 가 부르는 linkedJudgments(선두의 판단 전문)도
// **같은 표**를 써서 같이 죽으므로, Pick 전체로 시험하면 이 축과 무관한 이유로
// 실패해 "형제 색인만" 격리해 보지 못한다(landing_test.go 의 표 이름 바꾸기
// 관용구를 이 함수 하나에만 겨눈다).
func TestSiblingIndexReturnsEmptyOnQueryFailure(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	openSession(t, s, "p", repo, repo, "cc-1", "형제색인실패시험")
	f1 := addItem(t, s, "p", "f1-sib", []string{"services/a.go"}, nil)
	f2 := addItem(t, s, "p", "f2-sib", []string{"services/b.go"}, nil)
	makeSiblings(t, st, "p", "f1-sib", "f2-sib")

	// 사전 조건: 표가 멀쩡할 때는 색인이 실제로 채워지고 두 번째 값은 true 다.
	sib, ok := s.siblingIndex(ctx(), "p", []judge.Candidate{{Item: f1}, {Item: f2}})
	if !ok {
		t.Fatal("사전 조건이 깨졌다 — 표가 멀쩡한데 읽기 실패로 나왔다")
	}
	if len(sib) == 0 {
		t.Fatalf("사전 조건이 깨졌다 — 표가 멀쩡한데 색인이 비었다: %v", sib)
	}

	if _, err := st.DB().ExecContext(ctx(),
		`ALTER TABLE judgment_link RENAME TO judgment_link_hidden`); err != nil {
		t.Fatalf("형제 색인 조회를 실패시키지 못했다: %v", err)
	}

	got, ok2 := s.siblingIndex(ctx(), "p", []judge.Candidate{{Item: f1}, {Item: f2}})
	if ok2 {
		t.Fatal("조회가 실패했는데 두 번째 값이 true 다 — 호출부가 못 읽은 사실을 놓친다")
	}
	if got == nil {
		t.Fatal("조회가 실패했는데 nil 색인을 냈다 — 빈 맵이어야 나머지 두 축이 판정에서 계속 돈다")
	}
	if len(got) != 0 {
		t.Fatalf("조회가 실패했는데 색인에 값이 남았다: %v", got)
	}
}

// bundleScope 는 순수 함수다 — total 을 "적격 항목"이라고 부르면 안 된다(len(cands) 는
// 적격 여부와 무관하게 후보 전부를 센 수다). 리뷰 라운드 1 finding 2: 실측에서
// 후보 5·적격 3 인데 "이웃 후보는 적격 항목 5건이다"라고 낸 적이 있다.
func TestBundleScopeDoesNotClaimEligibleCount(t *testing.T) {
	got := bundleScope(5, true)
	if strings.Contains(got, "적격 항목") {
		t.Fatalf("적격 여부와 무관한 수인데 '적격 항목'이라고 주장한다: %q", got)
	}
	if !strings.Contains(got, "5건") {
		t.Fatalf("total 값 5가 문장에 없다: %q", got)
	}
}

// bundleScope 는 형제 축을 못 읽었다는 사실을 문장에 남긴다 — 리뷰 라운드 1 finding 3.
// 안 남기면 "구성원 0건"(형제가 진짜로 없다)과 "형제 축을 아예 못 읽었다"가 응답에서
// 같은 값으로 접힌다. 키 부재를 값으로 접지 않는다는 전역 규율이 이 문장에도 적용된다.
func TestBundleScopeNamesUnreadSiblingAxis(t *testing.T) {
	read := bundleScope(5, true)
	if strings.Contains(read, "못 읽") {
		t.Fatalf("다 읽었는데 못 읽었다고 말한다: %q", read)
	}
	unread := bundleScope(5, false)
	if !strings.Contains(unread, "못 읽") {
		t.Fatalf("형제 축을 못 읽었다는 사실이 문장에 없다: %q", unread)
	}
}

// Bundle.Scope 는 실제 pick 호출을 거쳐도 "적격 항목"을 주장하지 않는다 — 후보 중
// 하나가 남에게 선점돼 적격에서 빠지는 실제 시나리오로 후보 수(len(cands))와
// 적격 수가 갈리게 만들고, 그 응답을 확인한다(bundleScope 단위 시험이 pickRecommend
// 안에서 실제로 그 값으로 불리는지까지 닫는다).
func TestPickBundleScopeReflectsRealCandidateCount(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "적격시험")
	other := openSession(t, s, "p", repo, repo, "cc-2", "남시험")

	addItem(t, s, "p", "s1-open", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "s2-claimed", []string{"services/b.go"}, nil)
	if _, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: other.Session.ID, ItemID: "s2-claimed"}); err != nil {
		t.Fatalf("남의 선점 준비 실패: %v", err)
	}

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	// 사전 조건: 후보 2건(s1-open, s2-claimed 는 살아있는 남의 선점이라 후보에 든다) 중
	// 적격은 s1-open 하나뿐이어야 이 시험이 실제로 후보 수 ≠ 적격 수인 상황을 잰다.
	if len(res.Rejected) == 0 {
		t.Fatalf("사전 조건이 깨졌다 — 탈락이 하나도 없으면 후보 수와 적격 수가 갈리지 않는다: %+v", res.Rejected)
	}
	if res.Bundle == nil {
		t.Fatal("묶음 축이 nil 이다")
	}
	if strings.Contains(res.Bundle.Scope, "적격 항목") {
		t.Fatalf("Scope 가 여전히 '적격 항목'을 주장한다: %q", res.Bundle.Scope)
	}
	if !strings.Contains(res.Bundle.Scope, "2건") {
		t.Fatalf("Scope 의 수가 실제 후보 수(2건)와 안 맞는다: %q", res.Bundle.Scope)
	}
}

// 구성원의 PathCheck 는 그 항목 **자신의** 경로로 본다 — 선두의 경로를 빌려주면 안 된다.
// 합치면 `fd move <id> --project X` 처방이 엉뚱한 id 를 가리키게 된다(리뷰 라운드 1
// finding 4). 선두와 구성원이 서로 다른 경로-실재 판정을 받도록 만들어 갈라둔다:
// 선두는 실재하는 경로(README.md, newRepo 가 만든다)를, 구성원은 어디에도 없는
// 경로를 선언한다.
func TestPickBundleMemberPathCheckIsPerItemNotLead(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "경로시험")

	addItem(t, s, "p", "path-a", []string{"README.md"}, nil)
	addItem(t, s, "p", "path-b", []string{"does/not/exist.go"}, nil)
	makeSiblings(t, st, "p", "path-a", "path-b")

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	// 사전 조건: id 사전순 동점 처리로 path-a 가 선두여야 한다(설계대로).
	if res.Item == nil || res.Item.ID != "path-a" {
		t.Fatalf("사전 조건이 깨졌다 — 선두가 path-a 가 아니다: %+v", res.Item)
	}
	if res.PathCheck == nil || res.PathCheck.Kind != judge.KindOK {
		t.Fatalf("사전 조건이 깨졌다 — 선두(path-a)의 경로 판정이 OK 가 아니다: %+v", res.PathCheck)
	}
	if res.Bundle == nil || len(res.Bundle.Members) != 1 {
		t.Fatalf("구성원이 정확히 1건이어야 한다: %+v", res.Bundle)
	}
	m := res.Bundle.Members[0]
	if m.Item.ID != "path-b" {
		t.Fatalf("구성원 id 가 path-b 가 아니다: %s", m.Item.ID)
	}
	if m.PathCheck == nil {
		t.Fatal("구성원의 경로 판정이 nil 이다")
	}
	if m.PathCheck.Kind == judge.KindOK {
		t.Fatalf("구성원의 경로 판정이 선두 것을 빌려 썼다 — "+
			"path-b 는 어디에도 없는데 OK 로 나왔다: %+v", m.PathCheck)
	}
	if m.PathCheck.Kind != judge.KindNowhere {
		t.Fatalf("구성원의 경로 판정이 nowhere 가 아니다: %+v", m.PathCheck)
	}
}

// 추천 응답의 Reason 은 지금 실제로 통하는 인자만 처방한다 — 리뷰 라운드 1
// finding 1(CRITICAL). item_ids 는 태스크 9에서 온다. mcpsrv 의 pick 도구는 지금
// item_id 하나만 받고(additionalProperties:false, DisallowUnknownFields) 모르는
// 필드를 거절하므로, item_ids 를 처방하면 이 응답의 유일한 실행 가능한 줄이
// "json: unknown field \"item_ids\"" 로 죽는다.
func TestPickRecommendReasonPrescribesItemIDNotItemIDs(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "인자시험")
	addItem(t, s, "p", "arg-item", []string{"services/a.go"}, nil)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if strings.Contains(res.Reason, "item_ids") {
		t.Fatalf("아직 없는 인자 item_ids 를 처방했다: %q", res.Reason)
	}
	if !strings.Contains(res.Reason, "item_id") {
		t.Fatalf("실제로 통하는 item_id 인자를 아예 언급하지 않는다: %q", res.Reason)
	}
	// ★ 부분 문자열 "item_id"·id 존재만으로는 안 된다 — best.Reason 자체가 이미
	// "선두 <id>" 를 담고 있어(bundleAround 의 ④ 키) 그 값만 보면 이 처방 줄이
	// id 를 실제로 넣었는지와 무관하게 항상 참이 된다. "item_id 에 <id>" 라는
	// **처방 문구 자체**가 있는지를 본다.
	if !strings.Contains(res.Reason, "item_id 에 "+res.Item.ID) {
		t.Fatalf("처방 문구에 item_id 와 선두 id 가 나란히 없다: %q", res.Reason)
	}
}

// 선두를 못 집으면 아무것도 안 쓴다 — 브랜치가 정의되지 않으므로
// "묶음을 집었다"고 말할 수 없다.
func TestPickBundleLeadIsAtomic(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	other := openSession(t, s, "p", repo, repo, "cc-2", "남")

	addItem(t, s, "p", "lead", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "mem", []string{"services/b.go"}, nil)
	if _, err := s.Pick(ctx(), PickInput{
		Project: "p", SessionID: other.Session.ID, ItemID: "lead"}); err != nil {
		t.Fatalf("남의 선점 준비 실패: %v", err)
	}

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"lead", "mem"}})
	if err == nil {
		t.Fatalf("선두가 남의 것인데 성공했다: %+v", res)
	}
	// mem 에 선점 행이 생기면 안 된다.
	if _, cerr := st.GetClaim(ctx(), "p", "mem"); !errors.Is(cerr, store.ErrNotFound) {
		t.Fatalf("선두가 막혔는데 구성원을 집었다 (GetClaim err=%v)", cerr)
	}
}

// 선두를 집었으면 구성원 하나가 막혀도 나머지는 살아야 한다.
func TestPickBundleKeepsLeadWhenMemberBlocked(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	other := openSession(t, s, "p", repo, repo, "cc-2", "남")

	addItem(t, s, "p", "lead", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "m1-taken", []string{"services/b.go"}, nil)
	addItem(t, s, "p", "m2-free", []string{"services/c.go"}, nil)
	if _, err := s.Pick(ctx(), PickInput{
		Project: "p", SessionID: other.Session.ID, ItemID: "m1-taken"}); err != nil {
		t.Fatalf("남의 선점 준비 실패: %v", err)
	}

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"lead", "m1-taken", "m2-free"}})
	if err != nil {
		t.Fatalf("구성원 하나가 막혔다고 pick 이 실패했다: %v", err)
	}
	if res.Mode != PickClaimed {
		t.Fatalf("모드가 %q 다", res.Mode)
	}
	if res.Branch != "lead" {
		t.Fatalf("브랜치가 %q 다 — 선두 id 여야 한다", res.Branch)
	}
	var claimed, blocked int
	for _, m := range res.Bundle.Members {
		if m.Claimed {
			claimed++
			continue
		}
		blocked++
		if m.Rejection == nil || m.Rejection.Reason == "" {
			t.Fatalf("못 집은 구성원 %q 에 사유가 없다", m.Item.ID)
		}
		if m.Rejection.Detail == "" {
			t.Fatalf("못 집은 구성원 %q 에 상세가 없다 — 사유 코드만으로는 왜인지 모른다", m.Item.ID)
		}
	}
	if claimed != 1 || blocked != 1 {
		t.Fatalf("집은 것 %d · 막힌 것 %d", claimed, blocked)
	}
}

// 집었으면 구성원의 판단 전문이 온다 — 추천과 다른 점이 이것이다.
func TestPickBundleLoadsMemberNotesWhenClaimed(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	addItem(t, s, "p", "lead", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "mem", []string{"services/b.go"}, nil)
	if _, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentNotDone,
		Title: "일부러 안 한 것", Body: "DLQ 재처리는 계약 대기라 손대지 않았다", ItemID: "mem",
	}); err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"lead", "mem"}})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if len(res.Bundle.Members) != 1 || len(res.Bundle.Members[0].Notes) == 0 {
		t.Fatalf("집은 구성원의 판단 전문이 없다: %+v", res.Bundle.Members)
	}
}

// 원소 1개짜리 item_ids 는 기존 item_id 와 같은 결과여야 한다.
// 다르면 CLI 가 인자 하나를 넘겼을 때 조용히 다른 경로를 탄다.
func TestPickBundleOfOneEqualsSinglePick(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	a := openSession(t, s, "p", repo, repo, "cc-1", "A")
	b := openSession(t, s, "p", repo, repo, "cc-2", "B")
	addItem(t, s, "p", "one", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "two", []string{"services/b.go"}, nil)

	viaID, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: a.Session.ID, ItemID: "one"})
	if err != nil {
		t.Fatalf("단독 선점 실패: %v", err)
	}
	viaIDs, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: b.Session.ID, ItemIDs: []string{"two"}})
	if err != nil {
		t.Fatalf("원소 1개 묶음 선점 실패: %v", err)
	}
	if viaID.Mode != viaIDs.Mode {
		t.Fatalf("모드가 갈렸다: %q vs %q", viaID.Mode, viaIDs.Mode)
	}
	if viaIDs.Branch != "two" {
		t.Fatalf("브랜치가 %q 다", viaIDs.Branch)
	}
	if len(viaIDs.Setup) != len(viaID.Setup) {
		t.Fatalf("워크트리 명령 수가 갈렸다: %d vs %d", len(viaID.Setup), len(viaIDs.Setup))
	}
	if viaIDs.Bundle == nil || len(viaIDs.Bundle.Members) != 0 {
		t.Fatalf("원소 1개인데 구성원이 있다: %+v", viaIDs.Bundle)
	}
}

// 큐 열림 수는 **모든 쓰기 뒤에** 센다. 묶음 3건을 집으면 3이 빠진다.
func TestPickBundleCountsQueueAfterWrites(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	for _, id := range []string{"q1", "q2", "q3", "q4", "q5"} {
		addItem(t, s, "p", id, []string{"services/" + id + ".go"}, nil)
	}
	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"q1", "q2", "q3"}})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if res.QueueOpen == nil {
		t.Fatal("큐 열림 수가 없다")
	}
	if *res.QueueOpen != 2 {
		t.Fatalf("큐 열림이 %d 다 — 5건 중 3건을 집었으니 2여야 한다", *res.QueueOpen)
	}
}

// item_ids 가 왔는데(길이 > 0) 다듬고 나면 쓸 게 없으면(전부 공백) 거절해야 한다 —
// 조용히 추천 경로로 미끄러지면 세션은 묶음을 넣은 줄 알고 기다리는데 서버는
// 다른 질문(추천)에 답한다. 아무 항목도 안 만들어졌는지도 함께 본다.
func TestPickBundleRefusesAllBlankItemIDs(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	addItem(t, s, "p", "untouched", []string{"services/a.go"}, nil)

	_, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"", "   "}})
	if err == nil {
		t.Fatalf("전부 공백인 item_ids 가 통과했다")
	}
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("거절 오류 타입이어야 한다: %T %v", err, err)
	}
	// 추천 경로로 미끄러졌다면 이 항목이 선점됐을 것이다.
	if _, cerr := st.GetClaim(ctx(), "p", "untouched"); !errors.Is(cerr, store.ErrNotFound) {
		t.Fatalf("거절돼야 하는데 뭔가를 집었다 (GetClaim err=%v)", cerr)
	}
}

// 같은 id 를 두 번 주면 둘째 사본은 버려진다(순서 보존 중복 제거) — 안 그러면
// 둘째 사본이 pickExplicit 의 재개 경로("이미 내 선점")를 타 구성원 목록에
// "막혔다"가 아니라 "재개했다"는 엉뚱한 결과가 섞인다.
func TestPickBundleCollapsesDuplicateItemIDs(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	addItem(t, s, "p", "lead", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "mem", []string{"services/b.go"}, nil)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"lead", "lead", "mem"}})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if res.Branch != "lead" {
		t.Fatalf("브랜치가 %q 다", res.Branch)
	}
	// 중복이 안 걷혔다면 구성원이 둘(lead 사본 + mem)이 됐을 것이다.
	if len(res.Bundle.Members) != 1 {
		t.Fatalf("구성원이 %d건이다 — 중복 lead 가 안 걷혔다: %+v", len(res.Bundle.Members), res.Bundle.Members)
	}
	if res.Bundle.Members[0].Item.ID != "mem" {
		t.Fatalf("남은 구성원이 %q 다 — mem 이어야 한다", res.Bundle.Members[0].Item.ID)
	}
	if !strings.Contains(res.Reason, "묶음 2건") {
		t.Fatalf("사유의 묶음 수가 중복 제거 전 수를 세고 있다: %q", res.Reason)
	}
}

// 겹침은 묶음 전체 경로의 합집합으로 다시 봐야 한다 — "남과 부딪히는가"는 항목
// 단위가 아니라 묶음 단위 질문이다. 선두 자신의 경로는 남과 안 겹치지만
// 구성원의 경로가 겹치는 상황을 만들어, 그 겹침이 선두 단독 판정에는 안 잡히고
// 묶음 재계산에서만 잡힌다는 것을 확인한다.
func TestPickBundleOverlapsCoverWholeBundle(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "나")
	other := openSession(t, s, "p", repo, repo, "cc-2", "남")

	// 남이 구성원의 경로만 만지고 있다 — 선두의 경로는 건드리지 않는다.
	if err := s.Beat(ctx(), other.Session.ID, model.SignalTool,
		[]string{filepath.Join(repo, "services", "mem.go")}); err != nil {
		t.Fatalf("비트 실패: %v", err)
	}
	addItem(t, s, "p", "lead", []string{"services/lead.go"}, nil)
	addItem(t, s, "p", "mem", []string{"services/mem.go"}, nil)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"lead", "mem"}})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if len(res.Bundle.Members) != 1 || !res.Bundle.Members[0].Claimed {
		t.Fatalf("사전 조건이 깨졌다 — 구성원이 집혔어야 한다: %+v", res.Bundle.Members)
	}
	if len(res.Overlaps) != 1 || res.Overlaps[0].SessionID != other.Session.ID {
		t.Fatalf("구성원 경로의 겹침이 묶음 응답에 안 실렸다: %+v", res.Overlaps)
	}
}

// 못 집은 구성원도 실물 Item 을 실어야 한다 — 리뷰 라운드 1 finding 1.
// pickExplicit 이 실패하면 자기가 이미 읽은 항목도 버리고 빈 PickResult 를 낸다.
// 그걸 그대로 두면 구성원의 Item 이 {Project:"" ID:"" State:""} 로 찍히고, 추천
// 경로는 항상 실물 Item 을 내므로 태스크 10의 화면이 못 집은 구성원만 빈 줄로
// 그린다. id·state 가 살아 있는지를 본다(존재하는 항목이 남에게 막힌 경우).
func TestPickBundleBlockedMemberCarriesRealItem(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	other := openSession(t, s, "p", repo, repo, "cc-2", "남")

	addItem(t, s, "p", "lead", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "taken", []string{"services/b.go"}, nil)
	if _, err := s.Pick(ctx(), PickInput{
		Project: "p", SessionID: other.Session.ID, ItemID: "taken"}); err != nil {
		t.Fatalf("남의 선점 준비 실패: %v", err)
	}

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"lead", "taken"}})
	if err != nil {
		t.Fatalf("구성원 하나가 막혔다고 pick 이 실패했다: %v", err)
	}
	if len(res.Bundle.Members) != 1 {
		t.Fatalf("사전 조건이 깨졌다 — 구성원이 1건이어야 한다: %+v", res.Bundle.Members)
	}
	m := res.Bundle.Members[0]
	if m.Claimed {
		t.Fatalf("사전 조건이 깨졌다 — 못 집었어야 한다: %+v", m)
	}
	if m.Item.ID != "taken" {
		t.Fatalf("못 집은 구성원의 Item.ID 가 %q 다 — 요청한 id(taken)와 같아야 한다", m.Item.ID)
	}
	if m.Item.State == "" {
		t.Fatalf("못 집은 구성원의 Item.State 가 비었다 — 항목이 실재하는데 빈 값을 냈다: %+v", m.Item)
	}
}

// 요청한 id 가 애초에 큐에 없으면(재조회도 실패) State 를 지어내지 않되,
// 요청한 id 자체는 Item.ID 에 남아야 한다 — BundleMember.Item 계약의 나머지 절반.
func TestPickBundleUnknownMemberIDStillCarriesRequestedID(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	addItem(t, s, "p", "lead", []string{"services/a.go"}, nil)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"lead", "no-such-item"}})
	if err != nil {
		t.Fatalf("구성원 하나가 없다고 pick 이 실패했다: %v", err)
	}
	if len(res.Bundle.Members) != 1 {
		t.Fatalf("사전 조건이 깨졌다 — 구성원이 1건이어야 한다: %+v", res.Bundle.Members)
	}
	m := res.Bundle.Members[0]
	if m.Item.ID != "no-such-item" {
		t.Fatalf("없는 id 인데 Item.ID 에 요청한 id 가 안 남았다: %q", m.Item.ID)
	}
	if m.Item.State != "" {
		t.Fatalf("존재하지 않는 항목의 상태를 지어냈다: %q", m.Item.State)
	}
	if m.Rejection == nil || m.Rejection.Reason != RejectClaimNotFound {
		t.Fatalf("사유 코드가 %q 여야 한다: %+v", RejectClaimNotFound, m.Rejection)
	}
}

// Derived 는 묶음 전체를 반영해 다시 계산해야 한다 — 리뷰 라운드 1 finding 2.
// 구성원 처리 중에 쌓인 실패(안전하지 않은 id 라 워크트리 준비 명령을 못 낸 구성원)가
// 선두 단독 호출 시점의 스냅샷에는 없다. 서비스 계층의 ValidateItemID 검증을
// 정상 경로(AddItem)로는 우회할 수 없으므로 store 를 직접 써서 그런 항목을 넣는다.
func TestPickBundleDerivedReflectsMemberSetupFailure(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	addItem(t, s, "p", "lead", []string{"services/a.go"}, nil)

	// '..' 가 있어 ValidateItemID 가 거절하는 모양이다 — 워크트리 준비 명령을 못 낸다.
	unsafeID := "bad id/../x"
	if err := st.Tx(ctx(), func(t *store.Tx) error {
		return t.AddItem(model.Item{
			Project: "p", ID: unsafeID, Title: "위험한 id", Body: "store 로 직접 넣었다",
			Paths: []string{"services/unsafe.go"}, State: model.ItemOpen,
		})
	}); err != nil {
		t.Fatalf("안전하지 않은 id 항목 준비 실패: %v", err)
	}

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"lead", unsafeID}})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	// 사전 조건: JudgeClaim 은 id 형식을 안 본다 — 안전하지 않은 id 도 선점 자체는 성공한다.
	if len(res.Bundle.Members) != 1 || !res.Bundle.Members[0].Claimed {
		t.Fatalf("사전 조건이 깨졌다 — 안전하지 않은 id 도 선점은 성공해야 한다: %+v", res.Bundle.Members)
	}
	var found bool
	for _, f := range res.Failures {
		if strings.Contains(f.Axis, "setup:") && strings.Contains(f.Axis, "bad id") {
			found = true
		}
	}
	if !found {
		t.Fatalf("구성원의 워크트리 준비 실패가 묶음 응답의 파생 실패 목록에 없다: %+v", res.Failures)
	}
}
