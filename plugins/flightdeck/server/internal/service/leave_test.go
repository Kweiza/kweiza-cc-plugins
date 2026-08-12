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
	return svc, st, sess
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
