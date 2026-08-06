package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 이 파일은 ReclaimClaim(사람의 선점 회수)만 다룬다.
//
// 회수 로직은 web 대시보드에 인라인으로 먼저 생겼다(actions.go). 그 로직을 service 로
// 올려 CLI·REST·web 이 **같은 함수**를 부르게 하는 것이 이 축이다 — 레인 회수
// (ReleaseLaneRow)를 web 과 CLI 가 같은 함수로 부르는 것과 같은 형태다.

// claimFixture 는 선점 하나가 살아 있는 상태를 만든다.
func claimFixture(t *testing.T) (*Service, *store.Store, model.Session) {
	t.Helper()
	svc, st := newSvc(t)
	ctx := context.Background()
	if err := st.UpsertProject(ctx, model.Project{ID: "p", Path: "/p", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMachine(ctx, model.Machine{ID: "m", Hostname: "h"}); err != nil {
		t.Fatal(err)
	}
	sess, _, err := st.OpenSession(ctx, "p", "m", "/wt", "cc1", "라벨")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddItem(ctx, model.Item{Project: "p", ID: "x", Title: "t", Body: "b", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimItem(ctx, "p", "x", sess.ID); err != nil {
		t.Fatal(err)
	}
	return svc, st, sess
}

func TestReclaimClaimRefusesEmptyReasonAndClaimSurvives(t *testing.T) {
	svc, st, sess := claimFixture(t)
	ctx := context.Background()

	_, err := svc.ReclaimClaim(ctx, "p", "x", "사람", "   ")
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("빈 사유가 거절되지 않았다: %v", err)
	}
	// 거절이 선점을 건드리면 안 된다 — 사유 없는 회수가 실제로 회수되는 것이 최악이다.
	if got, _ := st.ClaimedItems(ctx, sess.ID); len(got) != 1 {
		t.Fatalf("거절됐는데 선점이 사라졌다: %v", got)
	}
}

func TestReclaimClaimRefusesEmptyProjectAndItem(t *testing.T) {
	svc, _, _ := claimFixture(t)
	ctx := context.Background()
	var refused *RefusedError
	if _, err := svc.ReclaimClaim(ctx, "", "x", "사람", "죽은 세션"); !errors.As(err, &refused) {
		t.Errorf("빈 project 가 거절되지 않았다: %v", err)
	}
	if _, err := svc.ReclaimClaim(ctx, "p", "", "사람", "죽은 세션"); !errors.As(err, &refused) {
		t.Errorf("빈 item 이 거절되지 않았다: %v", err)
	}
}

func TestReclaimClaimReleasesAndLeavesDecisionJudgment(t *testing.T) {
	svc, st, sess := claimFixture(t)
	ctx := context.Background()

	// actor 는 폴백 문구("대시보드(사람)")와 글자가 겹치지 않는 값이어야 한다 —
	// 겹치면 폴백 갈래가 사라져도 이 시험이 못 잡는다(판별력 0).
	res, err := svc.ReclaimClaim(ctx, "p", "x", "운영자-갑", "무신호 20시간 — 보드 근거로 회수한다")
	if err != nil {
		t.Fatalf("회수 실패: %v", err)
	}
	if res.Item != "x" || res.Holder != sess.ID {
		t.Errorf("결과가 회수 대상을 안 말한다: %+v (점유자 기대 %s)", res, sess.ID)
	}

	// 항목은 open 으로 돌아오고 선점은 풀린다 — 다음 pick 이 집을 수 있어야 회수다.
	it, err := st.GetItem(ctx, "p", "x")
	if err != nil {
		t.Fatal(err)
	}
	if it.State != model.ItemOpen {
		t.Errorf("항목이 open 으로 안 돌아왔다: %s", it.State)
	}
	if got, _ := st.ClaimedItems(ctx, sess.ID); len(got) != 0 {
		t.Errorf("선점이 살아 있다: %v", got)
	}

	// 회수 행위 자체가 판단(decision)으로 남는다 — 사유·점유자가 빠지면
	// 이 기록은 나중에 아무것도 말하지 못한다.
	js, err := st.JudgmentsForItem(ctx, "p", "x")
	if err != nil {
		t.Fatal(err)
	}
	var dec *model.Judgment
	for i := range js {
		if js[i].Kind == model.JudgmentDecision {
			dec = &js[i]
			break
		}
	}
	if dec == nil {
		t.Fatalf("decision 판단이 안 남았다: %+v", js)
	}
	if res.JudgmentID != dec.ID {
		t.Errorf("결과의 판단 id 가 실물과 다르다: %s vs %s", res.JudgmentID, dec.ID)
	}
	for _, want := range []string{"x", sess.ID, "무신호 20시간", "행위자: 운영자-갑", "마지막 신호"} {
		if !strings.Contains(dec.Body+dec.Title, want) {
			t.Errorf("판단 본문에 %q 가 없다:\n%s", want, dec.Body)
		}
	}
	if strings.Contains(dec.Body, "대시보드(사람)") {
		t.Errorf("actor 를 줬는데 폴백 문구가 나왔다 — 갈래가 안 갈린다:\n%s", dec.Body)
	}

	// 원장 이벤트 — 회수 시도가 세어져야 다음 조사가 이 축을 잴 수 있다.
	evs, err := st.ListEvents(ctx, "claim.reclaim", time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Errorf("claim.reclaim 이벤트가 %d건이다(기대 1)", len(evs))
	}
}

func TestReclaimClaimWithoutLiveClaimIsNotFound(t *testing.T) {
	svc, st, sess := claimFixture(t)
	ctx := context.Background()

	// 이미 회수된 선점을 다시 회수한다 — 두 번째는 "살아 있는 선점 없음"이어야 한다.
	if _, err := svc.ReclaimClaim(ctx, "p", "x", "사람", "첫 회수"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ReclaimClaim(ctx, "p", "x", "사람", "둘째 회수")
	var nf *store.NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("살아 있는 선점이 없는데 NotFound 가 아니다: %v", err)
	}
	// 실패한 시도도 원장에는 남는다 — LogEvent 를 트랜잭션 앞에 예약하는 규율
	// (성공만 세면 "회수가 안 되는데 아무도 모른다"가 재현된다).
	evs, err := st.ListEvents(ctx, "claim.reclaim", time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("claim.reclaim 이벤트가 %d건이다(기대 2 — 실패 시도 포함)", len(evs))
	}
	// 실패 시도의 holder 는 **빈 값**이어야 한다(최신순이라 [0]이 둘째 시도다) —
	// 반납된 옛 점유자를 실으면 "아무도 안 쥔 항목"의 실패가 옛 세션의 것으로 읽힌다.
	if strings.Contains(evs[0].Payload, sess.ID) {
		t.Errorf("실패 시도 이벤트에 옛 점유자가 holder 로 남았다: %s", evs[0].Payload)
	}
	if !strings.Contains(evs[0].Payload, `"holder":""`) {
		t.Errorf("실패 시도의 holder 가 빈 값이 아니다: %s", evs[0].Payload)
	}
	// 실패 자체도 .fail 로 남는다(레인 회수와 같은 결선).
	fails, err := st.ListEvents(ctx, "claim.reclaim.fail", time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(fails) != 1 {
		t.Errorf("claim.reclaim.fail 이벤트가 %d건이다(기대 1)", len(fails))
	}
}

// 회수 판단의 시각은 주입된 시계를 따른다 — 본문의 나이 계산(주입 시계)과 judgment.at
// (저장층 벽시계)이 갈리면 한 기록 안의 두 시각 축이 어긋난다. 레인이 커밋 a60c77f 로
// 고친 바로 그 결함 부류다.
func TestReclaimClaimJudgmentUsesInjectedClock(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.OpenWithLogger(filepath.Join(t.TempDir(), "fd.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fixed := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := New(st, log, WithClock(func() time.Time { return fixed }))

	ctx := context.Background()
	if err := st.UpsertProject(ctx, model.Project{ID: "p", Path: "/p", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMachine(ctx, model.Machine{ID: "m", Hostname: "h"}); err != nil {
		t.Fatal(err)
	}
	sess, _, err := st.OpenSession(ctx, "p", "m", "/wt", "cc1", "라벨")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddItem(ctx, model.Item{Project: "p", ID: "x", Title: "t", Body: "b", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimItem(ctx, "p", "x", sess.ID); err != nil {
		t.Fatal(err)
	}

	res, err := svc.ReclaimClaim(ctx, "p", "x", "운영자", "시계 검증")
	if err != nil {
		t.Fatal(err)
	}
	js, err := st.JudgmentsForItem(ctx, "p", "x")
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range js {
		if j.ID != res.JudgmentID {
			continue
		}
		if !j.At.Equal(fixed) {
			t.Fatalf("판단 시각이 주입 시계가 아니다: %s (기대 %s) — 저장층 벽시계가 찍혔다", j.At, fixed)
		}
		return
	}
	t.Fatalf("회수 판단을 못 찾았다: %+v", js)
}

// 정체 대조는 순수 함수다 — 반납·재선점이 관측과 트랜잭션 사이에 끼는 경쟁은 시험에서
// 재현할 수 없으므로, 그 판정만 떼어 세 갈래를 잠근다.
func TestJudgeReclaimIdentity(t *testing.T) {
	if v := judgeReclaimIdentity("A", "A"); v != nil {
		t.Errorf("점유자가 그대로인데 거절했다: %v", v)
	}
	v := judgeReclaimIdentity("A", "B")
	if v == nil {
		t.Fatal("점유자가 바뀌었는데 통과했다 — 낡은 관측으로 산 세션의 선점을 끊는다")
	}
	if !strings.Contains(v.Reason, "A") || !strings.Contains(v.Reason, "B") {
		t.Errorf("거절 사유가 두 정체를 이름으로 안 부른다: %s", v.Reason)
	}
	if v := judgeReclaimIdentity("", "B"); v == nil {
		t.Error("관측 없이(조회 실패·반납 관측) 산 선점을 회수하는 것이 통과했다")
	}
}

// 신호 문장 세 갈래 — 조회 실패를 "없음"으로 접으면 거짓이 판단에 영구히 박힌다.
func TestSignalObservationThreeBranches(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	if got := signalObservation(nil, false, now); !strings.Contains(got, "읽지 못했다") ||
		!strings.Contains(got, "신호 나이를 보지 않고") {
		t.Errorf("조회 실패 갈래가 그 사실을 안 말한다: %s", got)
	}
	if got := signalObservation(nil, true, now); !strings.Contains(got, "없음") {
		t.Errorf("신호 없음 갈래가 다르게 말한다: %s", got)
	}
	at := now.Add(-3 * time.Hour)
	if got := signalObservation(&at, true, now); !strings.Contains(got, "3h") {
		t.Errorf("신호 나이가 안 나온다: %s", got)
	}
}
