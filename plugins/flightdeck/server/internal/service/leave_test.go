package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 이 파일은 LeaveClaim(산 세션이 **자기** 선점을 놓는다)만 다룬다.
// 회수(ReclaimClaim)는 reclaim_test.go 다 — 두 축은 방벽이 서로 반대 방향이라
// 한 파일에 섞으면 "점유자 대조"라는 같은 말이 두 뜻으로 읽힌다.
//
// ★ 시험을 짤 때 **한 번에 조건 하나씩만 어긴다.** 여러 조건을 함께 어기면 어느 검사가
//   물었는지 알 수 없고, 두 방벽이 서로를 가려 어느 쪽도 변이가 안 닿는다.

// leaveFixture 는 세션 하나가 항목 둘을 쥔 상태를 만든다(묶음 선점의 최소형).
// 항목 수가 둘인 것이 중요하다 — 하나면 "전부 놓기"와 "하나 놓기"가 같은 결과라
// itemID 갈래의 변이가 안 닿는다.
func leaveFixture(t *testing.T) (*Service, *store.Store, model.Session) {
	t.Helper()
	svc, st := newSvc(t)
	return svc, st, seedLeaveClaims(t, st)
}

// seedLeaveClaims 는 leaveFixture 의 씨앗만 떼어낸 것이다 — 시계를 주입한 서비스
// (newSvcWithClock)는 Service 를 자기가 짓기 때문에 씨앗을 따로 부를 자리가 필요하다.
func seedLeaveClaims(t *testing.T, st *store.Store) model.Session {
	t.Helper()
	ctx := context.Background()
	if err := st.UpsertProject(ctx, model.Project{ID: "p", Path: "/p", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMachine(ctx, model.Machine{ID: "m", Hostname: "h"}); err != nil {
		t.Fatal(err)
	}
	sess, _, err := st.OpenSession(ctx, "p", "m", "/wt", "cc1", "라벨", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"x", "y"} {
		if err := st.AddItem(ctx, model.Item{Project: "p", ID: id, Title: "t", Body: "b", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.ClaimItem(ctx, "p", id, sess.ID, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	return sess
}

// 반납의 본체다 — 항목이 open 으로 **살아서** 돌아오고, 판단이 not-done 으로 남는다.
// finish(dropped) 로 때우면 잃는 것이 정확히 이 둘이다.
func TestLeaveClaimReleasesOwnClaimAndItemSurvivesAsOpen(t *testing.T) {
	svc, st, sess := leaveFixture(t)
	ctx := context.Background()

	res, err := svc.LeaveClaim(ctx, LeaveInput{Project: "p", SessionID: sess.ID, ItemID: "x", Reason: "재측 기한 미충족 — 2주 뒤에 열린다"})
	if err != nil {
		t.Fatalf("반납이 실패했다: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0] != "x" {
		t.Fatalf("놓은 항목이 x 하나가 아니다: %v", res.Items)
	}

	// ★ 항목이 **살아 있어야** 한다. dropped 로 닫히면 id 가 바뀌고 after 참조가 끊긴다.
	it, err := st.GetItem(ctx, "p", "x")
	if err != nil {
		t.Fatalf("놓은 항목을 못 읽었다 — 사라졌나: %v", err)
	}
	if it.State != model.ItemOpen {
		t.Fatalf("항목이 open 으로 안 돌아갔다: %v", it.State)
	}

	// y 는 안 건드려야 한다 — itemID 를 준 호출이 전부를 놓으면 그 갈래가 무의미해진다.
	mine, _ := st.ClaimedItems(ctx, sess.ID)
	if len(mine) != 1 || mine[0] != "y" {
		t.Fatalf("지정 안 한 선점까지 놓였다: %v", mine)
	}

	// 항목으로 조회한다 — 판단이 저장됐다는 것과 **항목에 이어졌다**는 것을 한 번에 문다.
	js, err := st.JudgmentsForItem(ctx, "p", "x")
	if err != nil {
		t.Fatal(err)
	}
	var found *model.Judgment
	for i := range js {
		if js[i].ID == res.JudgmentID {
			found = &js[i]
		}
	}
	if found == nil {
		t.Fatalf("반납 판단이 항목 x 에 안 이어졌다 (id=%s, 항목 판단 %d건)", res.JudgmentID, len(js))
	}
	// ★ decision 이 아니라 not-done 이다. 회수와 답하는 질문이 다르다.
	if found.Kind != model.JudgmentNotDone {
		t.Fatalf("판단 종류가 not-done 이 아니다: %v", found.Kind)
	}
	if !strings.Contains(found.Body, "재측 기한 미충족") {
		t.Fatalf("사유가 판단 본문에 안 실렸다: %q", found.Body)
	}
}

// itemID 를 안 주면 이 세션이 쥔 **전부**를 놓는다 — 묶음은 함께 집히므로 함께 놓인다.
func TestLeaveClaimWithoutItemIDReleasesEveryClaimOfThisSession(t *testing.T) {
	svc, st, sess := leaveFixture(t)
	ctx := context.Background()

	res, err := svc.LeaveClaim(ctx, LeaveInput{Project: "p", SessionID: sess.ID, ItemID: "", Reason: "묶음 통째로 놓는다"})
	if err != nil {
		t.Fatalf("반납이 실패했다: %v", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("둘 다 안 놓였다: %v", res.Items)
	}
	if mine, _ := st.ClaimedItems(ctx, sess.ID); len(mine) != 0 {
		t.Fatalf("선점이 남았다: %v", mine)
	}
}

// ★ 방벽이 회수와 **반대 방향**이다 — 남의 것이면 거절한다.
// 이게 빠지면 반납이 조용한 회수가 되고, pick 이 steal_reason 을 거절해 잠근 축이 함께 풀린다.
func TestLeaveClaimRefusesSomeoneElsesClaimAndPointsAtReclaim(t *testing.T) {
	svc, st, sess := leaveFixture(t)
	ctx := context.Background()

	// 남의 세션. **경로만 다르게** 한다 — 세션 정체가 (machine, worktree, cc_session_id) 라
	// 이 한 축만 달리하면 "다른 세션"이 성립하고, 나머지는 위 fixture 와 같은 값으로 남는다.
	other, _, err := st.OpenSession(ctx, "p", "m", "/wt2", "cc2", "남", time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.LeaveClaim(ctx, LeaveInput{Project: "p", SessionID: other.ID, ItemID: "x", Reason: "내 것인 줄 알았다"})
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("남의 선점 반납이 거절되지 않았다: %v", err)
	}
	// 거절이 선점을 건드리면 안 된다 — 조용히 뺏기는 것이 최악이다.
	if mine, _ := st.ClaimedItems(ctx, sess.ID); len(mine) != 2 {
		t.Fatalf("거절됐는데 원 점유자의 선점이 줄었다: %v", mine)
	}
	// 처방이 회수 표면을 가리켜야 한다 — 안 그러면 막힌 사람이 갈 곳이 없다.
	if !strings.Contains(refused.Guidance, "fd claim release") {
		t.Fatalf("처방이 회수 표면을 안 가리킨다: %q", refused.Guidance)
	}
}

// 사유 없는 반납은 되짚을 수 없다 — landing_queue 의 left_detail CHECK 와 같은 규율이다.
func TestLeaveClaimRefusesEmptyReasonAndClaimSurvives(t *testing.T) {
	svc, st, sess := leaveFixture(t)
	ctx := context.Background()

	_, err := svc.LeaveClaim(ctx, LeaveInput{Project: "p", SessionID: sess.ID, ItemID: "x", Reason: "   "})
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("빈 사유가 거절되지 않았다: %v", err)
	}
	if mine, _ := st.ClaimedItems(ctx, sess.ID); len(mine) != 2 {
		t.Fatalf("거절됐는데 선점이 사라졌다: %v", mine)
	}
}

// ★ 회수와 정반대의 요구다. 회수는 세션을 요구하면 탈출구가 막히지만,
// 반납은 세션이 없으면 **누구 것을 놓는지**가 안 정해진다 — 그건 회수다.
func TestLeaveClaimRefusesEmptySessionAndPointsAtReclaim(t *testing.T) {
	svc, _, _ := leaveFixture(t)
	ctx := context.Background()

	_, err := svc.LeaveClaim(ctx, LeaveInput{Project: "p", SessionID: "  ", ItemID: "x", Reason: "사유는 있다"})
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("빈 세션이 거절되지 않았다: %v", err)
	}
	// ★ **"거절됐다"만 보면 이 시험은 아무것도 안 문다.** 빈 세션 방벽을 지워도 아래
	//   "남의 것이면 거절" 방벽이 대신 잡아서(빈 문자열 != 점유자) 여전히 RefusedError 다.
	//   실제로 변이로 확인했다 — 사유를 안 물었을 때 이 시험은 방벽 제거에 초록이었다.
	//   그래서 **이 방벽만이 낼 수 있는 사유**를 문다.
	if !strings.Contains(refused.Reason, "세션이 비었다") {
		t.Fatalf("빈 세션 방벽이 아니라 다른 방벽이 잡았다 — 이 방벽은 지워져도 안 보인다: %q", refused.Reason)
	}
	if !strings.Contains(refused.Guidance, "fd claim release") {
		t.Fatalf("처방이 회수 표면을 안 가리킨다: %q", refused.Guidance)
	}
}

// 쥔 게 없는데 부르면 조용히 성공하면 안 된다 — "놓았다"는 응답이 거짓이 된다.
func TestLeaveClaimWithNothingHeldIsRefused(t *testing.T) {
	svc, st, _ := leaveFixture(t)
	ctx := context.Background()

	empty, _, err := st.OpenSession(ctx, "p", "m", "/wt3", "cc3", "빈손", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.LeaveClaim(ctx, LeaveInput{Project: "p", SessionID: empty.ID, ItemID: "", Reason: "놓을 게 없다"})
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("빈손 반납이 거절되지 않았다: %v", err)
	}
}

// ★ 묶음 반납의 **중간 실패**다. 여러 항목을 놓는 도중 하나가 깨지면 앞서 놓은 것이
// 롤백되어야 한다 — 안 그러면 실패한 반납이 **절반만 남고**, 응답은 오류인데 원장에는
// 놓인 항목이 있다. 그 상태를 아무 문장도 설명하지 못한다.
//
// 여기까지 이 자리의 근거는 "한 Tx 안이라 설계상 된다"였고 **그것은 관측이 아니다.**
//
// ## 무엇이 중간에 깨지나 — 지어낸 실패가 아니다
//
// 대상 후보(ClaimedItems)는 트랜잭션 **밖**에서 읽는다(reclaim.go 의 주석: 쓰기 잠금을
// 쥔 채 커넥션 풀을 기다리면 그 대기가 다른 쓰기 전부를 세운다). 권위는 Tx 안의
// LiveClaim 이고, 그래서 그 사이에 창이 있다 — 사람이 `fd claim release` 로 회수하면
// 후보에 실린 항목의 살아 있는 선점이 사라진다. 프로덕션 주석이 예고한 "후보가 낡았으면
// 거기서 걸린다"의 바로 그 자리다.
//
// ## 창을 시계로 잠근다
//
// LeaveClaim 은 `now := s.now()` 를 **후보 조회와 Tx 사이에서 정확히 한 번** 부른다.
// 주입한 시계 안에서 y 를 회수하면 경합이 결정론이 된다 — helper_test.go 의
// newSvcWithClock 주석이 지정한 용법("시계가 불리는 자리가 곧 창인 갈래")이고
// 선례는 outOfWindowLister 다.
//
// ## 변이로 닿는 것을 실측했다(둘 다 빨간)
//
//	루프의 `return lerr` → `continue`          x 가 놓인 채 남고 판단까지 남는다
//	릴리스를 Tx **앞**으로 옮긴다(Tx 를 가른다) x 의 반납이 커밋돼 롤백이 사라진다
func TestLeaveClaimRollsBackTheFirstReleaseWhenALaterTargetIsGone(t *testing.T) {
	ctx := context.Background()

	var st *store.Store
	fired := false
	var reclaimErr error
	svc, opened := newSvcWithClock(t, func() time.Time {
		// 후보 조회는 끝났고 Tx 는 아직 안 열렸다 — 사람의 회수가 여기서 끼어든다.
		// 한 번만 쏜다: 이 갈래가 시계를 한 번 부르는 것에 시험을 걸지 않는다.
		if st != nil && !fired {
			fired = true
			reclaimErr = st.ForceReleaseClaim(ctx, "p", "y", "무신호 20시간을 보고 사람이 회수했다")
		}
		return time.Now()
	})
	st = opened
	sess := seedLeaveClaims(t, st)

	_, err := svc.LeaveClaim(ctx, LeaveInput{Project: "p", SessionID: sess.ID, ItemID: "", Reason: "묶음 통째로 놓는다"})
	if reclaimErr != nil {
		t.Fatalf("창을 만드는 회수 자체가 실패했다 — 시험이 성립하지 않았다: %v", reclaimErr)
	}
	if !fired {
		t.Fatal("시계가 안 불렸다 — 창이 후보 조회와 Tx 사이에서 사라졌다면 이 시험은 아무것도 안 문다")
	}
	// 낡은 후보를 권위로 삼으면 안 된다 — 이미 없는 선점을 "놓았다"고 답하는 것이다.
	var nf *store.NotFoundError
	if !errors.As(err, &nf) || nf.Kind != store.NFLiveClaim || nf.ID != "y" {
		t.Fatalf("의도한 실패가 아니다(y 의 살아 있는 선점 없음이어야 한다): %v", err)
	}

	// ★ 본체. 실패 전에 놓은 x 가 되돌아와야 한다.
	mine, err := st.ClaimedItems(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 || mine[0] != "x" {
		t.Fatalf("x 의 반납이 롤백되지 않았다 — 실패한 반납이 절반만 남았다: %v", mine)
	}
	// claim 만 되돌리고 item.state 를 흘리면 x 는 **선점 없는 claimed** 나
	// **선점 있는 open** 이 된다 — 같은 Tx 라야 둘이 함께 되돌아온다.
	it, err := st.GetItem(ctx, "p", "x")
	if err != nil {
		t.Fatal(err)
	}
	if it.State != model.ItemClaimed {
		t.Fatalf("선점은 살아 있는데 항목 상태가 흘렀다: %v", it.State)
	}
	// 판단도 안 남아야 한다 — "놓았다"는 불변 기록이 안 놓인 항목에 붙으면
	// 다음에 집는 사람이 읽는 첫 문장이 거짓이 된다.
	if js, err := st.JudgmentsForItem(ctx, "p", "x"); err != nil {
		t.Fatal(err)
	} else if len(js) != 0 {
		t.Fatalf("실패한 반납이 판단을 남겼다: %d건", len(js))
	}
}
