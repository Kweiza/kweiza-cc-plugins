package service

import (
	"database/sql"
	"encoding/json"
	"errors"
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
