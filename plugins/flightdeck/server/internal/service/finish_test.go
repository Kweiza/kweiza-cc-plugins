package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// claimed 는 항목 하나를 이 세션의 선점 상태로 만든다.
func claimed(t *testing.T, s *Service, project, sessionID, itemID string) {
	t.Helper()
	res, err := s.Pick(ctx(), PickInput{Project: project, SessionID: sessionID, ItemID: itemID})
	if err != nil {
		t.Fatalf("선점 실패(%s): %v", itemID, err)
	}
	if res.Mode != PickClaimed {
		t.Fatalf("선점 상태를 못 만들었다: %s", res.Mode)
	}
}

func TestFinishWithoutBodyRefusesAndSaysWhatToWrite(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	// 대조 성립 단정 — 읽기 전에 먼저.
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("사전 조건이 깨졌다 — 판단이 이미 %d건 있다", n)
	}

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Body: "",
	})
	if err == nil {
		t.Fatalf("본문 없는 마무리는 거절돼야 한다")
	}

	// ★ 소비자 좌표계 — MCP·REST 가 사용자에게 그대로 보이는 것은 이 문자열이다.
	msg := err.Error()
	for _, want := range []string{
		"왜 그렇게 했나",
		"무엇을 기각했나",
		"일부러 안 한 것",
		"확인했으나 못 한 것",
		"followups",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("거절 응답에 %q 가 없다 — 무엇을 적어야 하는지를 그 자리에서 내야 한다:\n%s", want, msg)
		}
	}

	// 거절은 아무것도 쓰지 않는다.
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절했는데 판단이 %d건 저장됐다", n)
	}
	it, err := st.GetItem(ctx(), "p", "batch7")
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if it.State != model.ItemClaimed {
		t.Fatalf("거절했는데 항목 상태가 %s 로 바뀌었다", it.State)
	}
}

// 후속 등록이 실패하면 넷 전부가 롤백된다.
//
// ★ 실패 수단이 **중복 id 에서 성립하지 않는 선행 조건으로 바뀌었다.** 계약은 그대로다.
//
//	앞선 판은 중복 id 로 이 축을 쟀는데, 그 갈래는 이제 흡수한다 — 같은 id 의 항목이
//	이미 존재하므로 원자성이 지키려던 "후속이 유입되지 않은" 상태가 성립하지 않고,
//	롤백하면 **원리적으로 파생 불가한 판단 본문**이 함께 사라지기 때문이다
//	(finish.go 의 ② 주석). 중복은 실패를 만들기 편한 수단이었을 뿐 이 시험의 대상이
//	아니었으므로, 수단만 갈고 롤백 계약은 여기서 계속 지킨다.
func TestFinishRollsBackEverythingWhenFollowupFails(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	// ★ 대조가 성립하는지 **결과를 읽기 전에** 단정한다.
	//   ① 후속 id 가 비어 있어야 "중복이 아닌 사유로 실패했다"가 성립한다
	//   ② 끝내려는 항목이 선점 상태여야 "종료가 롤백됐다"가 의미를 가진다
	//   ③ 판단이 0건이어야 "판단도 안 남았다"를 말할 수 있다
	if n := countRows(t, st, `SELECT count(*) FROM item WHERE project='p' AND id='bad-after'`); n != 0 {
		t.Fatalf("사전 조건이 깨졌다 — 후속 id 가 이미 %d건 있어 중복 갈래로 샌다", n)
	}
	before, err := st.GetItem(ctx(), "p", "batch7")
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if before.State != model.ItemClaimed {
		t.Fatalf("사전 조건이 깨졌다 — batch7 상태가 %s 다", before.State)
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("사전 조건이 깨졌다 — 판단이 이미 %d건 있다", n)
	}

	_, err = s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone,
		Title:   "batch7 랜딩",
		Body:    "①…②…③…④…",
		Followups: []FollowupInput{
			// 선행 조건이 비었다 — item·job·sha 중 0개다. duplicate 가 아닌 거절이다.
			{ID: "bad-after", Title: "후속", Body: "본문", After: []model.After{{}}},
		},
	})
	if err == nil {
		t.Fatalf("불변식을 깨는 후속은 실패해야 한다 — 흡수는 중복 id 한 갈래뿐이다")
	}
	if !strings.Contains(err.Error(), "후속 항목 bad-after 등록 실패") {
		t.Fatalf("실패 사유가 후속 등록임을 말하지 않는다: %v", err)
	}

	// ★ 한 트랜잭션이었는가 — 넷 전부가 되돌아가야 한다.
	after, err := st.GetItem(ctx(), "p", "batch7")
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if after.State != model.ItemClaimed {
		t.Fatalf("후속이 실패했는데 항목이 %s 로 종료됐다 — 한 트랜잭션이 아니다", after.State)
	}
	if after.ClosedAt != nil {
		t.Fatalf("closed_at 이 남았다: %v", after.ClosedAt)
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("판단이 %d건 남았다 — 후속 실패에 함께 롤백되지 않았다", n)
	}
	cl, err := st.GetClaim(ctx(), "p", "batch7")
	if err != nil {
		t.Fatalf("선점 조회 실패: %v", err)
	}
	if cl.ReleasedAt != nil {
		t.Fatalf("선점이 반납됐다 — 종료가 롤백됐으면 반납도 롤백돼야 한다")
	}
	// 계측은 트랜잭션과 별개로 남는다 — "무엇을 시도했다 실패했나"가 감사 원장의 존재 이유다.
	if n := countRows(t, st, `SELECT count(*) FROM event WHERE kind='item.finish'`); n != 1 {
		t.Fatalf("실패한 시도가 원장에 %d건 — 롤백돼도 남아야 한다", n)
	}
	// 그리고 **왜** 실패했는지도 남아야 한다. 시도만 남으면 실패율은 세지되
	// 무엇을 고쳐야 하는지는 답하지 못한다.
	var payload string
	if err := st.DB().QueryRowContext(ctx(),
		`SELECT payload FROM event WHERE kind='item.finish.fail' ORDER BY id DESC LIMIT 1`).
		Scan(&payload); err != nil {
		t.Fatalf("실패 사유 이벤트가 없다: %v", err)
	}
	if !strings.Contains(payload, "bad-after") {
		t.Fatalf("실패 사유에 무엇이 실패했는지가 없다: %s", payload)
	}
}

func TestFinishIsOneCallForJudgmentFollowupCloseAndRelease(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", []string{"pipeline/"}, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	// 이 세션이 자원을 쥔 상태로 시작한다(반납이 마무리의 네 번째 몫이다).
	if _, err := st.AcquireResource(ctx(), "p", "staging",
		store.Holder{SessionID: me.Session.ID}, time.Time{}); err != nil {
		t.Fatalf("자원 점유 준비 실패: %v", err)
	}

	res, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone,
		Title:   "batch7 랜딩",
		Body:    "① 왜 그렇게 했나 … ④ 확인했으나 못 한 것 …",
		Followups: []FollowupInput{
			{ID: "batch8", Title: "다음 배치", Body: "batch7 에서 나온 후속", Paths: []string{"pipeline/"}},
		},
	})
	if err != nil {
		t.Fatalf("마무리 실패: %v", err)
	}

	if res.Item.State != model.ItemDone {
		t.Fatalf("항목이 안 닫혔다: %s", res.Item.State)
	}
	if len(res.Followups) != 1 || res.Followups[0].ID != "batch8" {
		t.Fatalf("후속이 안 들어갔다: %+v", res.Followups)
	}
	if got, err := st.GetItem(ctx(), "p", "batch8"); err != nil || got.State != model.ItemOpen {
		t.Fatalf("후속이 열린 항목으로 안 남았다: %+v %v", got, err)
	}
	if len(res.Released) != 1 || res.Released[0] != "staging" {
		t.Fatalf("자원이 안 반납됐다: %v", res.Released)
	}
	if _, err := st.HeldBy(ctx(), "p", "staging"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("자원 점유가 살아 있다: %v", err)
	}
	cl, err := st.GetClaim(ctx(), "p", "batch7")
	if err != nil || cl.ReleasedAt == nil {
		t.Fatalf("선점이 안 반납됐다: %+v %v", cl, err)
	}

	// 판단 ↔ 항목·후속이 FK 로 이어져야 한다. 경로 문자열 포인터였다면 끊어질 자리다.
	j, err := st.GetJudgment(ctx(), res.Judgment.ID)
	if err != nil {
		t.Fatalf("판단 조회 실패: %v", err)
	}
	if j.Kind != model.JudgmentHandoff {
		t.Fatalf("판단 종류가 %s 다", j.Kind)
	}
	links := map[string]bool{}
	for _, l := range j.Links {
		links[l.TargetKind+":"+l.TargetID] = true
	}
	for _, want := range []string{"item:batch7", "item:batch8"} {
		if !links[want] {
			t.Fatalf("판단 링크 %q 가 없다: %v", want, j.Links)
		}
	}
}

// TestFinishWhileHoldingTheLaneLetsTheSessionQueueAgain — 레인을 쥔 채 마무리한 세션이
// 뒤탈 없이 다시 줄을 설 수 있어야 한다.
//
// ★ 단정은 "자원이 반납됐나"가 아니라 **"그 세션이 다시 줄을 설 수 있나"**다. 자원만 보면
// (아래 res.Released 확인만으로는) 줄 행이 유령으로 남는 결함을 못 잡는다 — 자원은 정상
// 반납되고, EnqueueLanding 은 재진입 안전이라 살아 있는 유령 행을 오류 없이 그대로
// 돌려주기 때문이다(store/landing.go). 그러면 새로 서는 줄이 옛 유령 행(오래된 id)에
// 계속 들러붙어, 이미 기다리던 세션을 영영 추월 못 하게 막는다 —
// TestFailedReportSendsTheSessionToTheBack(landing_test.go)이 report 경로에서 잠근
// "새로 서면 맨 뒤" 규율이 finish 경로에서만 깨져 있었던 자리다.
func TestFinishWhileHoldingTheLaneLetsTheSessionQueueAgain(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	other := openSession(t, s, "p", repo, repo, "cc-2", "트랙7")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	mine, err := s.Land(ctx(), LandInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("줄 서기 실패: %v", err)
	}
	if mine.State != "turn" {
		t.Fatalf("빈 레인의 첫 세션이 차례를 못 받았다: %+v", mine)
	}
	theirs, err := s.Land(ctx(), LandInput{Project: "p", SessionID: other.Session.ID})
	if err != nil {
		t.Fatalf("두 번째 세션의 줄 서기 실패: %v", err)
	}
	if theirs.State != "waiting" {
		t.Fatalf("레인을 쥔 세션이 있는데 두 번째가 %q 다(기대 waiting): %+v", theirs.State, theirs)
	}

	res, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "batch7 랜딩", Body: "① 왜 그렇게 했나 …",
	})
	if err != nil {
		t.Fatalf("레인을 쥔 채 마무리가 실패했다: %v", err)
	}
	if len(res.Released) != 1 || res.Released[0] != LaneResource {
		t.Fatalf("레인 자원이 반납 목록에 없다: %v", res.Released)
	}

	// ★ 여기부터가 핵심 단정이다 — 자원 반납이 아니라 재입장.
	again, err := s.Land(ctx(), LandInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("마무리 뒤 다시 줄 서기가 실패했다: %v", err)
	}
	if again.RowID <= theirs.RowID {
		t.Fatalf("옛 유령 줄 행(%d)이 재사용됐다 — finish 가 줄 행을 안 닫았다"+
			"(다시 선 행 %d, 기다리던 행 %d)", mine.RowID, again.RowID, theirs.RowID)
	}
	if again.State != "waiting" || again.Position != 2 {
		t.Fatalf("마무리하고 다시 선 세션이 유령 행 덕에 새치기했다: %+v", again)
	}

	// 옛 행은 finish 로 닫혀 있어야 한다 — 유령이 없다.
	if n := countRows(t, st,
		`SELECT count(*) FROM landing_queue WHERE id = ? AND left_kind = 'finish'`, mine.RowID); n != 1 {
		t.Fatalf("첫 줄 행이 finish 로 안 닫혔다(id=%d)", mine.RowID)
	}
}

// TestFinishSurvivesAForcedReleaseRacingIt — 사람이 강제 회수를 finish 보다 먼저 커밋해도
// finish 는 판단을 남기고 성공해야 한다.
//
// ★ 판단(handoff)은 이 레포가 "원리적으로 파생 불가한 유일한 자산"이라 부르는 값이다.
// holds 를 트랜잭션 밖에서 읽던 시절에는(고치기 전) 그 사이에 사람이 레인을 강제
// 회수했을 때 ④ 의 반납 시도가 ErrNotFound 를 올리고, 그 오류가 ①②③ 을 통째로
// 롤백시켜 판단이 함께 사라졌다.
//
// ★ 이 시험이 고친 뒤 실제로 증명하는 것: guard 가 먼저 강제 회수하고 **커밋한다.**
// finish 는 그동안 guard 의 쓰기 잠금(BEGIN IMMEDIATE)에 막혀 대기하다가, guard 가
// 놓은 뒤에야 들어가 t.ListHeld 를 **자기 트랜잭션 안에서** 읽는다 — 그 시점엔 이미
// guard 의 회수가 커밋돼 있으므로 반납할 것이 하나도 없다. finish 는 반납을
// 시도조차 하지 않고도(아래 lane.release_skipped 원장 단정이 그것을 본다) 판단을
// 커밋한다.
//
// ★ 아래 sleep 이 왜 아직 있는가(실측으로 확인했다) — guardReady 핸드셰이크는
// "guard 가 잠금을 쥐고 회수를 실행했다"만 결정적으로 보장하지, "finish 의 **바깥**
// (트랜잭션 밖) 코드가 그 커밋 전에 뭔가를 읽었다"는 보장하지 않는다. 고친 코드
// 자체의 정확성에는 그 보장이 필요 없다 — t.ListHeld 는 쓰기 잠금에 물려 있어
// guard 뒤로 오는 순서가 sleep 유무와 무관하게 강제된다(그래서 위 시험은 sleep 을
// 빼도 늘 통과한다). 문제는 **회귀 검출**이다: "holds 를 트랜잭션 밖에서 다시 읽는"
// 변이(수정①을 되돌린 것)를 이 시험이 잡으려면 그 바깥 읽기가 guard 의 커밋 **전에**
// 실행돼 낡은 점유를 봐야 하는데, 그 읽기는 WAL 이라 아무 잠금에도 안 걸려 락으로
// 순서를 강제할 수단이 없다. 실측: sleep 을 빼고 그 변이를 넣으면 `go test -race
// -count=50` 에서 50/50 회 전부 못 잡았다(고루틴 예약이 guard 의 커밋보다 항상
// 늦었다 — race 계측 오버헤드가 그 편향을 더 키운다). sleep 을 되살리면 같은 조건에서
// 50/50 회 전부 잡는다. 즉 이 sleep 은 "고친 코드가 맞다"를 위한 게 아니라
// "이 시험이 그 회귀를 실제로 잡는다"를 위한 것이다.
func TestFinishSurvivesAForcedReleaseRacingIt(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	land, err := s.Land(ctx(), LandInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("줄 서기 실패: %v", err)
	}
	if land.State != "turn" {
		t.Fatalf("사전 조건이 깨졌다 — 레인을 못 쥐었다: %+v", land)
	}

	guardReady := make(chan struct{})
	pause := make(chan struct{})
	guardDone := make(chan error, 1)
	go func() {
		guardDone <- st.Tx(ctx(), func(t *store.Tx) error {
			if err := t.ForceReleaseResource("p", LaneResource,
				"레이스 시험: finish 의 ListHeld 와 Tx 사이에 사람이 회수했다"); err != nil {
				close(guardReady)
				return err
			}
			close(guardReady) // 회수는 이미 실행됐다(아직 커밋 전) — finish 를 열어도 안전하다
			<-pause
			return nil
		})
	}()
	<-guardReady

	type finishOutcome struct {
		res FinishResult
		err error
	}
	finishDone := make(chan finishOutcome, 1)
	go func() {
		res, err := s.Finish(ctx(), FinishInput{
			Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
			Outcome: model.ItemDone, Title: "batch7 랜딩", Body: "① 왜 그렇게 했나 …",
		})
		finishDone <- finishOutcome{res, err}
	}()

	// finish 의 바깥(트랜잭션 밖) 코드가 guard 의 커밋 전에 뭔가 읽을 시간을 번다 —
	// 위 함수 주석 참조. 고친 코드의 정확성 자체에는 이 sleep 이 필요 없지만
	// (쓰기 잠금이 순서를 대신 강제한다), 이게 없으면 "holds 를 트랜잭션 밖에서
	// 읽는" 회귀를 이 시험이 못 잡는다(실측 근거도 위에 있다).
	time.Sleep(100 * time.Millisecond)
	close(pause)

	if err := <-guardDone; err != nil {
		t.Fatalf("강제 회수(guard)가 실패했다: %v", err)
	}
	out := <-finishDone

	if out.err != nil {
		t.Fatalf("경합 중에도 마무리는 성공해야 한다(판단은 원리적으로 파생 불가한 유일한 자산이다): %v",
			out.err)
	}
	// ★ 이 시험이 수정 ①(holds 를 트랜잭션 안에서 읽기)을 실제로 잠그는 자리다.
	//   finish 의 t.ListHeld 가 guard 커밋 뒤에 도니 반납할 것이 애초에 없다 — 그러면
	//   ReleaseResource 를 부를 일도, 그 안의 멱등 처리(skip)를 탈 일도 없다. holds 를
	//   다시 트랜잭션 밖에서 읽게 되돌리면(변이) 밖에서 읽은 낡은 점유 때문에
	//   ReleaseResource 가 ErrNotFound 를 내고 멱등 분기가 그것을 삼켜 skip 이벤트를
	//   남긴다 — 그때만 이 카운트가 0 을 벗어난다. 즉 이 단정 없이는 멱등 처리 하나로도
	//   시험 전체가 초록이 돼, 이 태스크가 존재하는 이유인 "트랜잭션 안에서 읽기"가
	//   무방비로 남는다.
	if n := countRows(t, st,
		`SELECT count(*) FROM event WHERE project='p' AND kind='lane.release_skipped'`); n != 0 {
		t.Fatalf("트랜잭션 안에서 읽었으면 반납을 시도할 일이 없다 — 건너뛴 반납이 %d건 있다", n)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM judgment WHERE project='p' AND session_id=?`, me.Session.ID); n != 1 {
		t.Fatalf("판단 행이 안 남았다(%d건) — 강제 회수 경합이 핸드오프를 함께 지웠다", n)
	}
	it, err := st.GetItem(ctx(), "p", "batch7")
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if it.State != model.ItemDone {
		t.Fatalf("경합 뒤 항목이 안 끝났다: %s", it.State)
	}
	// 레인은 guard 가 이미 강제 회수했다 — finish 가 그것을 다시 반납했다고 보고하면 거짓이다.
	for _, r := range out.res.Released {
		if r == LaneResource {
			t.Fatalf("이미 강제 회수된 자원을 finish 가 다시 반납했다고 보고했다: %v", out.res.Released)
		}
	}
}

func TestFinishRefusesSomeoneElsesItem(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	other := openSession(t, s, "p", repo, repo, "cc-2", "트랙7")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", other.Session.ID, "batch7")

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Body: "본문",
	})
	if err == nil {
		t.Fatalf("남이 쥔 항목은 끝낼 수 없어야 한다")
	}
	var held *store.ClaimHeldError
	if !errors.As(err, &held) || held.Holder != other.Session.ID {
		t.Fatalf("점유자를 담은 오류여야 한다: %T %v", err, err)
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절했는데 판단이 %d건 남았다 — 판단만 남고 종료가 안 되는 반쪽 상태다", n)
	}
}

func TestNoteCountsRecipientsAndKeepsHistoryAppendOnly(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	other := openSession(t, s, "p", repo, repo, "cc-2", "트랙7")

	first, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentAsk,
		Title: "건드리면 곤란한 것", Body: "contracts/ 는 이번 주기에 내가 고친다",
	})
	if err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}
	if len(first.Recipients) != 1 || first.Recipients[0] != other.Session.ID {
		t.Fatalf("받을 세션이 틀렸다: %v", first.Recipients)
	}

	// 정정은 새 행 + supersedes 다. 덮어쓰기 경로가 존재하지 않는다.
	second, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentAsk,
		Body: "정정: contracts/raw-envelope 만 내가 고친다", Supersedes: first.Judgment.ID,
	})
	if err != nil {
		t.Fatalf("정정 저장 실패: %v", err)
	}
	if second.Judgment.ID == first.Judgment.ID {
		t.Fatalf("정정이 같은 행을 덮었다")
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 2 {
		t.Fatalf("판단이 %d건이다 — 원문이 남아야 한다", n)
	}

	if _, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentKind("메모"), Body: "x",
	}); err == nil {
		t.Fatalf("열거 밖 종류는 거절돼야 한다")
	}
	if _, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentNow, Body: "   ",
	}); err == nil {
		t.Fatalf("공백만 든 본문은 거절돼야 한다")
	}
}

func TestAllocIsAtomicAndStartsAtOne(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	openSession(t, s, "p", repo, repo, "cc-1", "")

	first, err := s.Alloc(ctx(), "p", "contract_revision")
	if err != nil {
		t.Fatalf("발번 실패: %v", err)
	}
	if first != 1 {
		t.Fatalf("첫 발급이 %d 다 — 0 은 '아직 안 씀'과 구분돼야 한다", first)
	}
	second, err := s.Alloc(ctx(), "p", "contract_revision")
	if err != nil {
		t.Fatalf("발번 실패: %v", err)
	}
	if second != 2 {
		t.Fatalf("두 번째 발급이 %d 다", second)
	}
	if _, err := s.Alloc(ctx(), "p", ""); err == nil {
		t.Fatalf("이름 없는 발번은 거절돼야 한다")
	}
}

func TestHealthAndDoctorNameWhatWasNotObserved(t *testing.T) {
	repo := newRepo(t)
	s, _ := newSvc(t)
	openSession(t, s, "p", repo, repo, "cc-1", "")

	h := s.Health(ctx())
	if !h.OK || !h.DBOK {
		t.Fatalf("DB 가 살아 있는데 헬스가 %+v 다", h)
	}
	if h.APIVersion == "" {
		t.Fatalf("api_version 이 비었다 — 클라이언트·서버 스큐를 알릴 축이다")
	}
	if h.DiskKnown && (h.DiskFreePct < 0 || h.DiskFreePct > 100) {
		t.Fatalf("디스크 여유가 %f%% 다", h.DiskFreePct)
	}
	if !h.DiskKnown && h.DiskError == "" {
		t.Fatalf("못 쟀으면 사유가 있어야 한다 — 0%%와 '못 쟀다'를 뭉개면 상시 빨간불이 된다")
	}

	// 환경을 주입해 "무엇이 관측되고 무엇이 안 됐는지"를 이름으로 내는지 본다.
	sd := New(s.st, nil, WithEnv(func(k string) (string, bool) {
		if k == "CLAUDE_CODE_SESSION_ID" {
			return "cc-1", true
		}
		return "", false
	}))
	rep := sd.Doctor(ctx())
	seen := map[string]bool{}
	for _, a := range rep.Platform {
		seen[a.Name] = a.Observed
	}
	if !seen["CLAUDE_CODE_SESSION_ID"] {
		t.Fatalf("관측된 축이 관측으로 안 잡혔다")
	}
	if _, ok := seen["CLAUDE_PLUGIN_ROOT"]; !ok {
		t.Fatalf("관측 안 된 축이 목록에서 통째로 빠졌다 — 부재를 기본값으로 접으면 그 사실이 영영 안 보인다")
	}
	if len(rep.Projects) != 1 || !rep.Projects[0].Readable || rep.Projects[0].HeadSHA == "" {
		t.Fatalf("프로젝트 git 도달성을 실제로 재지 않았다: %+v", rep.Projects)
	}
}

// finish 의 followup 은 t.AddItem 을 직접 불러 add(AddItem)의 좌표계 검증 루프를
// 거치지 않는 우회 문이었다 — followup 경로도 같은 관문(judgeItemPathsCoordinate)을
// 거쳐야 한다. 안 그러면 같은 사람이 같은 세션에서 add 는 거절당하고 finish 는
// 조용히 통과하는 반쪽 관문이 된다.
func TestFinishRejectsFollowupWithBadCoordinatePaths(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-coord", "좌표계")
	addItem(t, s, "p", "coord-main", nil, nil)
	claimed(t, s, "p", me.Session.ID, "coord-main")

	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("사전 조건이 깨졌다 — 판단이 이미 %d건 있다", n)
	}

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "coord-main",
		Outcome: model.ItemDone,
		Title:   "coord-main 랜딩",
		Body:    "① … ④ …",
		Followups: []FollowupInput{
			{ID: "coord-fu", Title: "다음 배치", Body: "본문",
				Paths: []string{"a/b.go", `internal\api\x.go`}},
		},
	})
	if err == nil {
		t.Fatal("좌표계가 틀린 후속 경로를 통과시켰다")
	}
	msg := err.Error()
	// 몇 번째 후속인지와, 그 후속 안에서 몇 번째 경로인지를 **둘 다** 말해야 한다.
	//
	// ★ 둘 다 1-based 다. 후속은 하나뿐이므로 "1번째 후속"이고, 그 안에서 틀린 것은
	// 두 번째 경로(`internal\api\x.go`)이므로 "2번째 경로"다. 전에는 각각 "0번째"·
	// "1번째"였다 — 사람이 세는 수와 어긋나 있었다.
	if !strings.Contains(msg, "1번째 후속") {
		t.Errorf("몇 번째 후속인지 안 말한다(1-based 여야 한다): %s", msg)
	}
	if !strings.Contains(msg, "2번째 경로") {
		t.Errorf("후속 안에서 몇 번째 경로인지 안 말한다(1-based 여야 한다): %s", msg)
	}
	if !strings.Contains(msg, "백슬래시") {
		t.Errorf("원인(백슬래시)을 안 짚는다: %s", msg)
	}

	// 거절이면 아무것도 안 쓴다 — 다른 followup 검증 실패와 같은 규율(§ 위 롤백 시험).
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절했는데 판단이 %d건 저장됐다", n)
	}
}

// 정상 좌표계의 followup 경로는 그대로 통과해야 한다 — 관문이 정상 입력까지 막으면
// 그것도 침묵만큼 나쁘다.
func TestFinishAcceptsFollowupWithGoodCoordinatePaths(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-coord-ok", "좌표계")
	addItem(t, s, "p", "coord-ok-main", nil, nil)
	claimed(t, s, "p", me.Session.ID, "coord-ok-main")

	res, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "coord-ok-main",
		Outcome: model.ItemDone,
		Title:   "coord-ok-main 랜딩",
		Body:    "① … ④ …",
		Followups: []FollowupInput{
			{ID: "coord-ok-fu", Title: "다음 배치", Body: "본문",
				Paths: []string{"internal/api/x.go", "Makefile"}},
		},
	})
	if err != nil {
		t.Fatalf("좌표계가 맞는 후속 경로를 거절했다: %v", err)
	}
	if len(res.Followups) != 1 || len(res.Followups[0].Paths) != 2 {
		t.Fatalf("후속 경로가 안 들어갔다: %+v", res.Followups)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 후속 id 충돌 — 부적격이면 tx 전에 거절하고 아무것도 안 쓴다
// ─────────────────────────────────────────────────────────────────────────────

// TestFinishWritesNothingWhenAFollowupIsIneligible 은 **옛 계약을 뒤집은 자리**다.
//
// 옛 계약: 후속 id 가 이미 있으면 그 후속만 건너뛰고 판단은 무조건 지킨다.
// 그 규율은 흡수가 **트랜잭션 안**에 있었기 때문이다 — 거기서 거절하면 판단이 함께 죽는다.
//
// 새 계약: 자격 판정이 트랜잭션 **밖**으로 나왔으므로 거절해도 아무것도 안 쓴다.
// 판단 본문은 아직 세션 손에 있고 그 후속만 빼면 그대로 다시 부를 수 있다 —
// title·body 누락 거절과 같은 자리·같은 성격이다. 그 대가로 오타 하나가 남의 항목을
// 내 판단에 잇는 문이 닫힌다(그 문은 지금 열려 있다 — 링크는 이미 걸리고 있었다).
//
// (단정 주의: claimed() 는 Pick→ClaimItem 을 거치므로 항목 상태는 이 시점에 이미 'claimed' 다.
//
//	"안 썼다" 를 볼 때 'open' 을 기대하면 구현이 맞아도 빨간불이 뜬다 — 실측으로 확인했다.)
func TestFinishWritesNothingWhenAFollowupIsIneligible(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	author := openSession(t, s, "p", repo, repo, "cc-a", "트랙A")
	me := openSession(t, s, "p", repo, wt, "cc-b", "트랙B")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")
	addItemAs(t, s, "p", author.Session.ID, "shared-followup")

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "판단 본문",
		Followups: []FollowupInput{{ID: "shared-followup", Title: "제목", Body: "본문"}},
	})
	if err == nil {
		t.Fatalf("남이 만든 항목을 후속으로 실었는데 통과했다")
	}
	// 자격 관문의 고유 문구 — 다른 관문이 먼저 거절하면 아래 단정들이 헛돈다.
	if !strings.Contains(err.Error(), "이을 자격이 없다") {
		t.Fatalf("자격 축이 아닌 다른 관문이 먼저 거절했다:\n%s", err.Error())
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절인데 판단이 %d건 남았다 — 트랜잭션 진입 전이라 반쪽 상태가 없어야 한다", n)
	}
	// 'open' 이 아니라 'claimed' 다 — 위 claimed() 가 Pick 을 거쳐 ClaimItem 을 부르고,
	// ClaimItem(store/item.go:719)이 항목 상태를 open→claimed 로 이미 옮겨 놨다.
	// 여기서 볼 것은 "거절이 그 상태를 건드리지 않았다" 이지 "아직 open 이다" 가 아니다.
	if n := countRows(t, st, `SELECT count(*) FROM item WHERE id = 'batch7' AND state = 'claimed'`); n != 1 {
		t.Fatalf("거절인데 항목이 여전히 이 세션 손에 있지 않다 — 그 후속만 빼고 그대로 다시 부를 수 있어야 한다")
	}
}

// TestFinishRefusesTheWholeCallWhenOneFollowupIsIneligible 은 **부분 성공을 기각한 자리**다.
//
// 옛 계약은 섞인 호출에서 정상 후속만 살렸다. 그러면 오타 하나가 "후속 1건 등록" 안에
// 조용히 섞여 나가고, 세션은 자기가 실은 둘 중 하나가 다른 뜻이 된 것을 못 본다.
// 지금은 전체를 세운다 — 되부르는 비용은 한 번이고, 그 한 번이 무엇이 틀렸는지 이름을 낸다.
func TestFinishRefusesTheWholeCallWhenOneFollowupIsIneligible(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	author := openSession(t, s, "p", repo, repo, "cc-a", "트랙A")
	me := openSession(t, s, "p", repo, wt, "cc-b", "트랙B")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")
	addItemAs(t, s, "p", author.Session.ID, "taken-id")

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "판단 본문",
		Followups: []FollowupInput{
			{ID: "taken-id", Title: "제목", Body: "본문"},
			{ID: "fresh-id", Title: "제목", Body: "본문"},
		},
	})
	if err == nil {
		t.Fatalf("부적격 후속이 섞였는데 통과했다")
	}
	if !strings.Contains(err.Error(), "taken-id") {
		t.Fatalf("거절 사유가 어느 후속인지 안 낸다:\n%s", err.Error())
	}
	// 자격 관문의 고유 문구 — 아래 "정상 후속도 안 만들어졌다" 단정은 어떤 거절에서도 참이라,
	// 이 줄이 없으면 **왜** 전체가 섰는지를 이 시험이 안 잠근다.
	if !strings.Contains(err.Error(), "이을 자격이 없다") {
		t.Fatalf("자격 축이 아닌 다른 관문이 먼저 거절했다:\n%s", err.Error())
	}
	if n := countRows(t, st, `SELECT count(*) FROM item WHERE id = 'fresh-id'`); n != 0 {
		t.Fatalf("정상 후속이 %d건 만들어졌다 — 전체가 거절돼야 한다", n)
	}
}

// TestFinishNeverLosesAJudgmentWhenTwoSessionsRaceTheSameFollowupID 는 경합의 **끝**을 잠근다.
//
// 자격 판정은 트랜잭션 **밖**이고 링크는 **안**이다. 그래서 두 세션이 같은 새 id 를 동시에
// 후속으로 실으면 끝이 둘 중 하나이고, **어느 쪽이 나오는지는 스케줄러가 정한다** —
// 시험이 고를 수 없다:
//
//	ⓐ 둘 다 classifyFollowups 를 지난 뒤에 커밋이 붙는다 → 진 쪽은 tx 안에서
//	  ConflictDuplicate 를 만나 **그 후속만 건너뛰고**(SkippedFollowups) 판단은 산다.
//	  흡수 갈래(finish.go 의 ②)가 실제로 밟히는 것은 이 경우다.
//	ⓑ 이긴 쪽이 커밋까지 마친 뒤에 진 쪽이 분류를 돈다 → 진 쪽에게 그 id 는 이미
//	  "있는 항목"이고 이을 자격이 없으므로 **트랜잭션 진입 전에 거절**된다.
//
// ★ **ⓑ 를 "판단 소실"이라고 부르면 안 된다.** 거절은 tx 진입 전이라 아무것도 안 쓰고,
//
//	판단 본문은 아직 세션 손에 있어 그 후속만 빼면 그대로 되부를 수 있다 —
//	title·body 누락 거절과 같은 자리·같은 성격이다. 이 시험이 잠그는 것은 "둘 다 성공한다"가
//	아니라 **반쪽 상태가 없다**는 것이다.
//
// ★ **갈래를 단정하지 않는 이유는 실측이다.** 갈래를 가르는 것은 **suite 부하가 아니라
//
//	-race 자체다** — 개정 전 주석이 "전체 suite 부하에서는 ⓐ" 라고 적었던 것은 틀렸다.
//	실측(리뷰 + 재확인, -run … -count=24~40 여러 배치): `-race` 로 돌리면 사실상 항상 ⓑ
//	(63/64), `-race` 없이 돌리면 사실상 항상 ⓐ(63/64). 원인은 -race 계측이 두 고루틴의
//	분류→커밋 구간을 늘려 서로의 tx 밖 classifyFollowups 를 갈라놓기 때문으로 보인다 —
//	suite 안의 다른 시험이 도는 것과 무관하다. 그래서 "둘 다 성공한다"로 단정하면 **관문이
//	-race 로 도는지 여부에 따라** 빨개지고, 원인이 시험이 아니라 계약이라 다음 세션이
//	시험만 고치다 이 설계의 진짜 모양을 덮는다.
//
// ★ **그래도 흡수의 산출은 잠근다 — 조건부로.** 이 시험이 지우는
//
//	TestFinishLogsSkippedFollowupToLedger 가 원장 축(item.followup_skipped 의 건수와
//	**어느** 후속인지)을 보던 유일한 자리였다. 갈래가 tx 안 전용이 됐다고 축까지 사라지면
//	그 발신이 통째로 죽거나 payload 에서 좌표가 빠져도 전 스위트가 초록이다. 그래서
//	"응답으로 건너뛰었다고 말한 세션 수" 를 세고, 원장 기록이 **정확히 그 수만큼** 있는지를
//	본다 — ⓑ 만 나온 회차에서는 둘 다 0이 되어 유령 기록도 함께 잡힌다.
func TestFinishNeverLosesAJudgmentWhenTwoSessionsRaceTheSameFollowupID(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	a := openSession(t, s, "p", repo, wt, "cc-a", "트랙A")
	b := openSession(t, s, "p", repo, repo, "cc-b", "트랙B")
	addItem(t, s, "p", "item-a", nil, nil)
	addItem(t, s, "p", "item-b", nil, nil)
	claimed(t, s, "p", a.Session.ID, "item-a")
	claimed(t, s, "p", b.Session.ID, "item-b")

	fin := func(sessionID, itemID string) (FinishResult, error) {
		return s.Finish(ctx(), FinishInput{
			Project: "p", SessionID: sessionID, ItemID: itemID,
			Outcome: model.ItemDone, Title: "끝냈다", Body: "판단 본문",
			Followups: []FollowupInput{{ID: "twin-race", Title: "제목", Body: "본문"}},
		})
	}
	type outcome struct {
		res FinishResult
		err error
	}
	outs := make(chan outcome, 2)
	go func() { r, e := fin(a.Session.ID, "item-a"); outs <- outcome{r, e} }()
	go func() { r, e := fin(b.Session.ID, "item-b"); outs <- outcome{r, e} }()

	ok, skipped := 0, 0
	for i := 0; i < 2; i++ {
		o := <-outs
		if o.err == nil {
			ok++
			// 흡수 갈래(ⓐ)를 밟았으면 응답이 **반드시** 그 사실을 말한다. 조용히 넘기면
			// 세션은 후속이 들어간 줄 알고 떠나는데 그 id 의 항목은 남이 만든 다른 것이다.
			if len(o.res.SkippedFollowups) == 1 && o.res.SkippedFollowups[0] == "twin-race" {
				skipped++
			} else if len(o.res.SkippedFollowups) != 0 {
				t.Fatalf("건너뛴 후속 목록이 %v 다 — twin-race 하나이거나 비어 있어야 한다",
					o.res.SkippedFollowups)
			}
			continue
		}
		// 거절이 아닌 오류는 tx 안에서 올라온 것이고, 그 갈래는 ① 의 판단을 함께 롤백한다.
		var refused *RefusedError
		if !errors.As(o.err, &refused) {
			t.Fatalf("경합이 거절 아닌 오류로 죽었다 — 이 모양이 판단을 잃는다: %v", o.err)
		}
		if !strings.Contains(o.err.Error(), "twin-race") {
			t.Fatalf("거절이 어느 후속인지 안 낸다:\n%s", o.err.Error())
		}
	}
	if ok == 0 {
		t.Fatalf("둘 다 거절됐다 — 먼저 커밋한 쪽은 반드시 성공해야 한다")
	}
	// ★ 판단 수 == 성공 수. 이것이 이 시험의 심장이다 — 성공했는데 판단이 없으면
	//   롤백이 파생 불가한 자산을 지운 것이고, 거절했는데 판단이 있으면 반쪽 상태가 남은 것이다.
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != ok {
		t.Fatalf("판단이 %d건인데 성공한 마무리는 %d건이다 — 성공마다 하나, 거절은 0건이어야 한다", n, ok)
	}
	if n := countRows(t, st, `SELECT count(*) FROM item WHERE id = 'twin-race'`); n != 1 {
		t.Fatalf("같은 id 의 항목이 %d건이다 — 정확히 하나여야 한다", n)
	}
	// 거절당한 세션의 항목은 **안 닫힌 채로** 남아야 되부를 수 있다(선점은 유지되므로 claimed 다).
	if n := countRows(t, st,
		`SELECT count(*) FROM item WHERE id IN ('item-a','item-b') AND state = 'done'`); n != ok {
		t.Fatalf("닫힌 항목이 %d건인데 성공은 %d건이다 — 거절당한 항목은 안 닫혀 있어야 한다", n, ok)
	}
	// ★ **건너뜀은 원장에도 남는다.** 응답 SkippedFollowups 는 그 세션과 함께 사라지므로,
	//   나중에 "왜 이 후속이 안 들어갔나"를 되짚을 수 있는 자리는 원장뿐이다.
	//   갈래를 단정하지 않으려고 기대값을 위에서 센 skipped 로 잡는다 — ⓐ 면 1, ⓑ 면 0이다.
	if n := countRows(t, st,
		`SELECT count(*) FROM event WHERE kind = 'item.followup_skipped'`); n != skipped {
		t.Fatalf("응답으로 건너뛰었다고 말한 세션은 %d개인데 원장 기록은 %d건이다 — 응답과 원장이 갈렸다",
			skipped, n)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM event WHERE kind = 'item.followup_skipped' AND payload LIKE '%twin-race%'`); n != skipped {
		t.Fatalf("원장 기록에 **어느** 후속을 건너뛰었는지가 없다 — 좌표 없는 기록은 나중에 못 되짚는다(기대 %d건, 실제 %d건)",
			skipped, n)
	}
}
