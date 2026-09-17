package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// closeIfActiveUnclaimed 는 시험용 단발 트랜잭션이다.
func closeIfActiveUnclaimed(t *testing.T, s *Store, id string) bool {
	t.Helper()
	var closed bool
	if err := s.Tx(context.Background(), func(tx *Tx) error {
		var err error
		closed, err = tx.CloseSessionWhoseWorktreeIsGone(id, "p", map[string]any{"worktree": "/w"})
		return err
	}); err != nil {
		t.Fatalf("조건부 닫기 실패: %v", err)
	}
	return closed
}

// 서버가 워크트리가 사라진 카드를 닫는 쓰기는 **판정 뒤에 조건을 한 번 더** 건다.
//
// ★ 왜 판정(judge.MayCloseGoneCard)만으로 부족한가. 판정은 ListLive 스냅숏을 보고, 그 읽기와
// 이 쓰기 사이에 사람이 blocked 를 걸거나 그 세션이 항목을 집을 수 있다. 이 시험은 그 창에서
// 바뀐 상태를 **UPDATE 한 문장이** 스스로 거르는지 본다 — 판정 쪽 조건을 지워도 여기가 막고,
// 여기를 지워도 판정이 막는다. 둘 중 하나만 남기면 창 하나가 열린다.
func TestCloseSessionWhoseWorktreeIsGoneRefusesBlockedClaimedAndDone(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()

	// ① active · 선점 0 → 닫힌다
	free := mustSession(t, s, "p", "cc-free")
	if !closeIfActiveUnclaimed(t, s, free.ID) {
		t.Fatal("active · 선점 0 인 카드를 안 닫았다")
	}
	if got, _ := s.GetSession(ctx, free.ID); got.State != model.SessionDone {
		t.Fatalf("닫았다고 했는데 state=%q 다", got.State)
	}
	// ★ 되돌릴 수 있어야 한다 — 같은 3중키로 열면 살아난다(Tx.OpenSession).
	again, created, err := s.OpenSession(ctx, "p", "m1", free.Worktree, free.CCSessionID, "", time.Time{})
	if err != nil || created || again.ID != free.ID || again.State != model.SessionActive {
		t.Fatalf("자동으로 닫힌 카드가 다시 열기로 안 살아난다: id=%q created=%v state=%q err=%v",
			again.ID, created, again.State, err)
	}

	// ② blocked → 안 닫는다. 사유도 그대로다
	blocked := mustSession(t, s, "p", "cc-blocked")
	if err := s.SetSessionState(ctx, blocked.ID, model.SessionBlocked, "레인이 물렸다"); err != nil {
		t.Fatalf("막힘 표시 실패: %v", err)
	}
	if closeIfActiveUnclaimed(t, s, blocked.ID) {
		t.Fatal("blocked 카드를 닫았다 — 사람이 사유와 함께 남긴 판단이다")
	}
	if got, _ := s.GetSession(ctx, blocked.ID); got.State != model.SessionBlocked || got.BlockedWhy != "레인이 물렸다" {
		t.Fatalf("blocked 카드가 바뀌었다: state=%q why=%q", got.State, got.BlockedWhy)
	}

	// ③ 선점을 든 active → 안 닫는다
	holder := mustSession(t, s, "p", "cc-holder")
	if err := s.AddItem(ctx, model.Item{Project: "p", ID: "x", Title: "t", Body: "b", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimItem(ctx, "p", "x", holder.ID, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if closeIfActiveUnclaimed(t, s, holder.ID) {
		t.Fatal("선점을 든 카드를 닫았다 — ListLive 에서 빠져 그 선점이 아무에게도 안 보인다")
	}
	if got, _ := s.GetSession(ctx, holder.ID); got.State != model.SessionActive {
		t.Fatalf("선점 카드의 state 가 %q 로 바뀌었다", got.State)
	}

	// ④ 이미 done → 안 닫았다고 말한다(한 번 더 닫았다고 세면 원장에 헛 닫기가 남는다)
	done := mustSession(t, s, "p", "cc-done")
	if err := s.SetSessionState(ctx, done.ID, model.SessionDone, ""); err != nil {
		t.Fatal(err)
	}
	if closeIfActiveUnclaimed(t, s, done.ID) {
		t.Fatal("이미 done 인 카드를 닫았다고 한다 — 호출부가 그것을 원장에 닫기로 적는다")
	}
}

// mcp 신호 조건은 state·선점과 같은 자리(UPDATE 한 문장)에 있어야 한다 — judge.MayCloseGoneCard
// 만 있으면 스냅숏을 읽은 뒤 첫 Beat(mcp) 가 끼는 창에서 이 쓰기가 신호를 무시하고 닫는다
// (§4 「알고 남기는 구멍」). 이 시험은 그 쓰기 문장을 판정 없이 직접 불러 SQL 층만 잰다.
func TestCloseSessionWhoseWorktreeIsGoneRefusesMCPSignaled(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()

	// ① mcp 신호가 있는 카드 → 안 닫는다. MCP 는 프로세스당 한 번만 열어 닫히면 되살릴 길이 없다.
	mcpCard := mustSession(t, s, "p", "cc-mcp")
	if err := s.Beat(ctx, mcpCard.ID, model.SignalMCP, time.Time{}); err != nil {
		t.Fatalf("mcp 신호 기록 실패: %v", err)
	}
	if closeIfActiveUnclaimed(t, s, mcpCard.ID) {
		t.Fatal("mcp 신호가 있는 카드를 닫았다 — MCP 는 그 카드를 다시 안 열어 done 카드가 선점을 쥘 수 있다")
	}
	if got, _ := s.GetSession(ctx, mcpCard.ID); got.State != model.SessionActive {
		t.Fatalf("mcp 신호가 있는 카드의 state 가 %q 로 바뀌었다", got.State)
	}

	// ② mcp 신호가 없는 다른 카드는 그대로 닫힌다 — 서브쿼리가 session.id 상관 참조를
	//    잃으면(변이) ①의 mcp 신호 한 행이 프로젝트의 모든 카드를 막는다. 이 카드는 그
	//    신호와 무관하므로 정상 닫기가 여기서 드러난다.
	plain := mustSession(t, s, "p", "cc-plain")
	if !closeIfActiveUnclaimed(t, s, plain.ID) {
		t.Fatal("mcp 신호 없는 카드를 안 닫았다 — 다른 카드의 mcp 신호가 상관 없이 전부를 막았을 수 있다")
	}
	if got, _ := s.GetSession(ctx, plain.ID); got.State != model.SessionDone {
		t.Fatalf("닫혔어야 하는데 state=%q 다", got.State)
	}

	// ③ mcp 가 아닌 신호(tool)만 있는 카드도 그대로 닫힌다 — kind 조건이 'mcp' 하나만 봐야 한다.
	toolOnly := mustSession(t, s, "p", "cc-tool")
	if err := s.Beat(ctx, toolOnly.ID, model.SignalTool, time.Time{}); err != nil {
		t.Fatalf("tool 신호 기록 실패: %v", err)
	}
	if !closeIfActiveUnclaimed(t, s, toolOnly.ID) {
		t.Fatal("tool 신호만 있는 카드를 안 닫았다 — mcp 아닌 신호까지 막으면 정상 닫기가 깨진다")
	}
}

// goneEvents 는 그 카드의 자동 닫기 원장 행 payload 들이다.
func goneEvents(t *testing.T, s *Store, id string) []map[string]any {
	t.Helper()
	evs, err := s.ListSessionEvents(context.Background(), id, EventSessionCloseWorktreeGone, time.Time{})
	if err != nil {
		t.Fatalf("이벤트 조회 실패: %v", err)
	}
	out := make([]map[string]any, 0, len(evs))
	for _, e := range evs {
		var p map[string]any
		if err := json.Unmarshal([]byte(e.Payload), &p); err != nil {
			t.Fatalf("payload 해석 실패(%s): %v", e.Payload, err)
		}
		out = append(out, p)
	}
	return out
}

// ★ 원장 행은 전이와 **원자적**이다 — 커밋되면 둘 다 있고, 롤백되면 둘 다 없다.
//
// 왜 이것이 가드인가: 이 행이 "카드당 한 번" 한도의 근거다. 예약 이벤트(Tx.LogEvent)로 쓰면
// ① 롤백된 시도도 행을 남겨(tx=rolled_back) 닫지 않은 카드가 이미 닫혔다고 읽히고, ② 커밋 뒤
// 따로 흐르는 INSERT 가 실패하면 WARN 한 줄로 삼켜져 닫혔는데 행이 없는 카드가 생겨 한도가 풀린다.
func TestCloseSessionWhoseWorktreeIsGoneWritesItsLedgerRowAtomically(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()

	// ① 롤백: 호출부 트랜잭션이 뒤에서 실패하면 state 도 원장 행도 안 남는다.
	rolled := mustSession(t, s, "p", "cc-rolled")
	boom := errors.New("뒤따르는 쓰기가 실패했다")
	err := s.Tx(ctx, func(tx *Tx) error {
		closed, err := tx.CloseSessionWhoseWorktreeIsGone(rolled.ID, "p", map[string]any{"worktree": "/w"})
		if err != nil || !closed {
			t.Fatalf("전제가 깨졌다 — 트랜잭션 안에서 안 닫혔다: closed=%v err=%v", closed, err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("주입한 실패가 안 올라왔다: %v", err)
	}
	if got, _ := s.GetSession(ctx, rolled.ID); got.State != model.SessionActive {
		t.Fatalf("롤백됐는데 state=%q 다", got.State)
	}
	if evs := goneEvents(t, s, rolled.ID); len(evs) != 0 {
		t.Fatalf("롤백된 닫기가 원장에 %d행을 남겼다 — 한도가 이 카드를 이미 닫힌 것으로 읽는다: %v", len(evs), evs)
	}

	// ② 커밋: state 와 원장 행이 **함께** 있다. 행이 커밋과 함께만 존재하므로 tx=committed 다.
	committed := mustSession(t, s, "p", "cc-committed")
	if !closeIfActiveUnclaimed(t, s, committed.ID) {
		t.Fatal("active · 선점 0 인 카드를 안 닫았다")
	}
	evs := goneEvents(t, s, committed.ID)
	if len(evs) != 1 {
		t.Fatalf("커밋된 닫기의 원장 행이 %d행이다 — 정확히 1행이어야 한다", len(evs))
	}
	if evs[0]["tx"] != TxCommitted || evs[0]["worktree"] != "/w" {
		t.Fatalf("원장 행 payload 가 틀렸다: %v", evs[0])
	}
}

// ★ 카드당 한 번의 **정본**은 쓰기 문장이다. 서비스의 미리 거르기(원장 조회)와 이 쓰기 사이에
// 다른 파생이 닫고 늦게 온 훅이 되살릴 수 있다 — 그 뒤 도착한 쓰기가 되살아난 카드를 또 닫으면
// 안 된다. 여기서는 미리 거르기 없이 쓰기만 두 번 부른다.
func TestCloseSessionWhoseWorktreeIsGoneClosesACardOnlyOnce(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()

	card := mustSession(t, s, "p", "cc-once")
	if !closeIfActiveUnclaimed(t, s, card.ID) {
		t.Fatal("첫 닫기가 안 됐다")
	}
	// 훅이 되살린다 — 같은 3중키로 연다.
	if again, _, err := s.OpenSession(ctx, "p", "m1", card.Worktree, card.CCSessionID, "", time.Time{}); err != nil ||
		again.State != model.SessionActive {
		t.Fatalf("전제가 깨졌다 — 되살아나지 않았다: state=%q err=%v", again.State, err)
	}
	// 미리 거르기를 통과한(원장을 그 전에 읽은) 늦은 쓰기가 도착한다.
	if closeIfActiveUnclaimed(t, s, card.ID) {
		t.Fatal("되살아난 카드를 또 닫았다 — 그 좌표로 열기가 왔다는 관측을 쓰기가 덮었다")
	}
	if got, _ := s.GetSession(ctx, card.ID); got.State != model.SessionActive {
		t.Fatalf("되살아난 카드의 state 가 %q 로 바뀌었다", got.State)
	}
	if evs := goneEvents(t, s, card.ID); len(evs) != 1 {
		t.Fatalf("자동 닫기 원장 행이 %d행이다 — 카드당 한 번이어야 한다", len(evs))
	}
}
