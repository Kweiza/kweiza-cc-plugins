package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 판정은 **사유로** 갈려야 한다.
//
// 불리언만 보면 다섯 거절이 한 덩어리가 되고, 그러면 "왜 못 옮기나"에 답할 자리가 없다.
// 각 케이스가 자기 축의 단어를 담는지까지 본다 — 안 그러면 사유가 통째로 뒤바뀌어도 초록이다.
func TestJudgeMoveGivesADistinctReasonPerAxis(t *testing.T) {
	for _, c := range []struct {
		name        string
		found       bool
		from, to    string
		holder      string
		targetFound bool
		wantOK      bool
		wantWord    string
	}{
		{"대상이 비었다", true, "a", "", "", true, false, "대상 프로젝트가 비었다"},
		{"항목이 없다", false, "a", "b", "", true, false, "그런 항목이 없다"},
		{"이미 그 프로젝트", true, "a", "a", "", true, false, "이미 프로젝트"},
		{"대상 프로젝트 부재", true, "a", "b", "", false, false, "원장에 없다"},
		{"선점 중", true, "a", "b", "sess-9", true, false, "sess-9"},
		{"옮길 수 있다", true, "a", "b", "", true, true, "옮길 수 있다"},
	} {
		t.Run(c.name, func(t *testing.T) {
			v := JudgeMove(c.found, c.from, c.to, c.holder, c.targetFound)
			if v.OK != c.wantOK {
				t.Fatalf("OK=%v 기대 %v (사유: %s)", v.OK, c.wantOK, v.Reason)
			}
			if !strings.Contains(v.Reason, c.wantWord) {
				t.Errorf("사유에 %q 가 없다: %s", c.wantWord, v.Reason)
			}
		})
	}
}

// ★ 순서 축: 항목이 없으면서 대상도 비었을 때 **대상 먼저** 답해야 한다.
//
// 뒤집히면 "그런 항목이 없다"가 나가고, 사람은 id 를 고치러 간다 — 실제로 틀린 것은
// 명령줄의 --project 인데. 순서가 곧 처방이라 여기서 못박는다.
func TestJudgeMoveAnswersTheEmptyTargetBeforeTheMissingItem(t *testing.T) {
	v := JudgeMove(false, "a", "", "", false)
	if v.OK {
		t.Fatal("빈 대상이 통과했다")
	}
	if !strings.Contains(v.Reason, "대상 프로젝트가 비었다") {
		t.Errorf("항목 부재가 먼저 답했다 — 사람이 엉뚱한 자리를 고치러 간다: %s", v.Reason)
	}
}

// 항목만 옮기면 안 된다 — (project, item_id) 를 함께 든 행이 따라와야 한다.
//
// 이 시험이 없으면 결함이 두 모양으로 샌다. item_after·claim 은 FK 가 UPDATE 를 거부해
// 시끄럽게 실패하지만, item_dependents·job 은 FK 가 없어 **조용히 옛 프로젝트에 남는다.**
// 후자는 오류가 없으므로 아무도 눈치채지 못한다.
func TestMoveItemCarriesEveryRowKeyedByProject(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "from-proj")
	seed(t, s, "to-proj")
	mustItem(t, s, "from-proj", "it-1")

	// 선점했다가 반납한다 — 반납해도 claim 행 자체는 남는다(released_at 만 찍힌다).
	// 실제로 옮겨야 할 항목들이 이 모양이라 이 조건을 그대로 재현한다.
	sess := mustSession(t, s, "from-proj", "cc-1")
	if _, err := s.ClaimItem(ctx, "from-proj", "it-1", sess.ID, time.Time{}); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	if err := s.ReleaseClaim(ctx, "from-proj", "it-1", sess.ID); err != nil {
		t.Fatalf("반납 실패: %v", err)
	}
	// 선행(랜딩된 sha)도 하나 건다 — item_after 는 FK 가 걸린 쪽이다.
	if err := s.Tx(ctx, func(tx *Tx) error {
		return tx.AddAfter("from-proj", "it-1", model.After{SHA: "abc1234"})
	}); err != nil {
		t.Fatalf("선행 등록 실패: %v", err)
	}

	// ── 대조 전제: 옮기기 전에 딸린 행이 정말 있는가.
	// 없으면 이 시험은 "안 깨졌다"가 아니라 **아무것도 안 본 것**이다.
	for _, tbl := range []string{"claim", "item_after"} {
		if n := countIn(t, s, tbl, "from-proj", "it-1"); n == 0 {
			t.Fatalf("대조 전제가 깨졌다 — %s 에 딸린 행이 0건이라 이동을 볼 수 없다", tbl)
		}
	}

	cross, err := s.MoveItem(ctx, "from-proj", "it-1", "to-proj", "sess-x")
	if err != nil {
		t.Fatalf("이동 실패: %v", err)
	}
	if cross != 0 {
		t.Errorf("끊기는 선행 참조가 %d건이라는데 이 시험은 안 만들었다", cross)
	}

	// 항목 자신
	if _, err := s.GetItem(ctx, "to-proj", "it-1"); err != nil {
		t.Fatalf("대상 프로젝트에 항목이 없다: %v", err)
	}
	if _, err := s.GetItem(ctx, "from-proj", "it-1"); err == nil {
		t.Error("원 프로젝트에 항목이 그대로 남아 있다")
	}
	// 딸린 행 — 옛 자리에 0건, 새 자리에 그대로.
	for _, tbl := range []string{"claim", "item_after", "item_dependents", "job"} {
		if n := countIn(t, s, tbl, "from-proj", "it-1"); n != 0 {
			t.Errorf("%s 에 옛 프로젝트 행이 %d건 남았다 — 조용한 고아다", tbl, n)
		}
	}
	for _, tbl := range []string{"claim", "item_after"} {
		if n := countIn(t, s, tbl, "to-proj", "it-1"); n == 0 {
			t.Errorf("%s 가 대상 프로젝트로 안 따라왔다", tbl)
		}
	}

	// ── 원장. **SSE 발행으로 대신할 수 없다** — 그쪽은 지금 보고 있는 사람에게만 가고 사라진다.
	// 처음 구현은 publish(SSE)만 불렀고, 실물 이동 뒤 event 표를 조회해서야 드러났다.
	var kind, payload string
	err = s.db.QueryRowContext(ctx,
		`SELECT kind, payload FROM event WHERE kind = 'item.move' ORDER BY id DESC LIMIT 1`).
		Scan(&kind, &payload)
	if err != nil {
		t.Fatalf("원장에 item.move 가 없다 — 옮긴 사실이 어디에도 안 남았다: %v", err)
	}
	for _, want := range []string{"it-1", "from-proj", "to-proj"} {
		if !strings.Contains(payload, want) {
			t.Errorf("원장 payload 에 %q 가 없다: %s", want, payload)
		}
	}
}

// 선점 중인 항목은 거절한다 — 선점은 세션에 걸리고 세션은 프로젝트에 걸린다.
func TestMoveItemRefusesWhileClaimedAndNamesTheHolder(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "from-proj")
	seed(t, s, "to-proj")
	mustItem(t, s, "from-proj", "it-1")
	sess := mustSession(t, s, "from-proj", "cc-1")
	if _, err := s.ClaimItem(ctx, "from-proj", "it-1", sess.ID, time.Time{}); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}

	_, err := s.MoveItem(ctx, "from-proj", "it-1", "to-proj", "sess-x")
	if err == nil {
		t.Fatal("선점 중인데 이동이 통과했다")
	}
	if !strings.Contains(err.Error(), sess.ID) {
		t.Errorf("누가 쥐고 있는지 안 알려 준다 — 물어볼 상대를 모른다: %v", err)
	}
	// 거절했으면 아무것도 안 옮겨져 있어야 한다.
	if _, gerr := s.GetItem(ctx, "from-proj", "it-1"); gerr != nil {
		t.Errorf("거절했는데 원 항목이 사라졌다: %v", gerr)
	}
}

// 없는 프로젝트로는 못 옮긴다 — 자동 생성이 유령 프로젝트를 만든 경로다.
func TestMoveItemRefusesUnknownTargetInsteadOfCreatingIt(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "from-proj")
	mustItem(t, s, "from-proj", "it-1")

	if _, err := s.MoveItem(ctx, "from-proj", "it-1", "오타-프로젝트", "sess-x"); err == nil {
		t.Fatal("없는 프로젝트로 이동이 통과했다")
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project WHERE id = ?`, "오타-프로젝트").Scan(&n); err != nil {
		t.Fatalf("프로젝트 조회 실패: %v", err)
	}
	if n != 0 {
		t.Errorf("거절했는데 프로젝트가 %d건 생겼다", n)
	}
}

func countIn(t *testing.T, s *Store, table, project, itemID string) int {
	t.Helper()
	var n int
	q := "SELECT COUNT(*) FROM " + table + " WHERE project = ? AND item_id = ?"
	if err := s.db.QueryRowContext(context.Background(), q, project, itemID).Scan(&n); err != nil {
		t.Fatalf("%s 조회 실패: %v", table, err)
	}
	return n
}
