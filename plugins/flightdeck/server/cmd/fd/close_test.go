package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 선점이 없으면 닫는다. 그리고 **DB 가 실제로 done 이어야** 한다 —
// "보냈다"는 단정은 "저장됐다"를 말하지 못한다(harness_test.go 머리말).
func TestCloseSetsSessionDone(t *testing.T) {
	h := newHarness(t)
	code, out := h.run("", "open")
	if code != 0 {
		t.Fatalf("open 실패(%d): %s", code, out)
	}

	code, out = h.run("", "close", "--why", "핸드오프 끝")
	if code != 0 {
		t.Fatalf("close 실패(%d): %s", code, out)
	}

	if got := len(h.liveSessions()); got != 0 {
		t.Fatalf("닫았는데 살아 있는 세션이 %d건 남았다", got)
	}
}

// 선점이 남아 있으면 **거절한다.**
//
// ★ done 카드는 ListLive 에서 빠지고, 그러면 그 세션이 든 선점이 아무에게도 안 보인다 —
// 항목을 아무도 못 집는데 누가 잡았는지도 안 보이는 상태가 된다. 그래서 우회 플래그를
// 두지 않는다: 우회할 필드가 있으면 우회된다.
func TestCloseRefusesWhileHoldingClaims(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "add", "--id", "it-1", "--title", "제목", "--body", "본문"); code != 0 {
		t.Fatalf("add 실패(%d): %s", code, out)
	}
	if code, out := h.run("", "pick", "it-1"); code != 0 {
		t.Fatalf("pick 실패(%d): %s", code, out)
	}

	code, out := h.run("", "close")
	if code == 0 {
		t.Fatalf("선점을 든 채 닫혔다 — 그 선점이 보드에서 사라진다:\n%s", out)
	}
	if !strings.Contains(out, "it-1") {
		t.Errorf("무엇이 남았는지를 안 냈다 — 사유 없는 거절은 다음 사람이 못 푼다:\n%s", out)
	}
	if !strings.Contains(out, "fd finish") {
		t.Errorf("처방(fd finish)을 안 냈다:\n%s", out)
	}
	if got := len(h.liveSessions()); got != 1 {
		t.Errorf("거절했는데 살아 있는 세션이 %d건이다", got)
	}
}

// TestCloseRefusesRatherThanMintingACard 는 **닫으려다 카드를 만드는 것**을 막는다.
//
// ★ 실물 재현(2026-08-06 판단): board 가 스스로 인쇄한 「이 MCP 프로세스가 카드를 연 값」을
// 그대로 `fd close -cc-session <값>` 에 넣었더니 **그 호출이 새 카드를 만들어서 그것을 닫았다.**
// 실제 카드는 그대로 열려 있었다. `runClose` 가 카드를 정하는 유일한 호출(a.OpenSession)이
// 조회가 아니라 **3중키 upsert** 라서다 — 그리고 반환값의 Created 를 아무도 안 봤다.
//
// ★ 왜 이것이 특히 나쁜가. 새 카드는 선점이 0건이라 위의 「선점이 남아 있으면 거절」
// 가드도 안 걸린다. 즉 진단하려는 사람이 **카드를 하나씩 더 만들면서** 진단하고,
// 그 카드들이 겹침 판정에 창 내내 잡힌다.
func TestCloseRefusesRatherThanMintingACard(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "open"); code != 0 {
		t.Fatalf("open 실패(%d): %s", code, out)
	}
	before := h.liveSessions()
	if len(before) != 1 {
		t.Fatalf("전제가 깨졌다 — 살아 있는 카드가 %d건이다, want 1", len(before))
	}

	// 이 좌표에 카드가 없는 cc 다. 옛 거동은 여기서 카드를 만들고 그것을 닫았다.
	code, out := h.run("", "close", "--cc-session", "cc-that-has-no-card")
	if code == 0 {
		t.Fatalf("없는 카드를 닫았다고 보고했다 — 그 호출이 카드를 만든 것이다:\n%s", out)
	}

	after := h.liveSessions()
	if len(after) != 1 || after[0].Session.ID != before[0].Session.ID {
		t.Fatalf("살아 있는 카드가 바뀌었다: %d건 — 원래 카드 %s 는 그대로 열려 있어야 한다",
			len(after), before[0].Session.ID)
	}
	if !strings.Contains(out, "cc-that-has-no-card") {
		t.Errorf("무엇으로 찾았는지를 안 냈다 — 사유 없는 거절은 다음 사람이 못 푼다:\n%s", out)
	}

	// ★ **두 번째가 이 시험의 핵심이다.** "만들어 놓고 거절한다"로 고치면 첫 호출은
	// 거절이지만 카드는 남고, 그래서 두 번째 호출은 그 카드를 찾아 **닫아 버린다.**
	// 조회 전용 갈래(FindSession)를 써야만 둘 다 거절이다.
	code2, out2 := h.run("", "close", "--cc-session", "cc-that-has-no-card")
	if code2 == 0 {
		t.Fatalf("두 번째 호출이 통과했다 — 첫 호출이 카드를 남긴 것이다(만들고 거절했다):\n%s", out2)
	}
}

// TestCloseBySessionIDClosesCardWhoseCCMovedAway 는 **카드 id 입구의 본체**다.
//
// ★ 위 TestCloseRefusesRatherThanMintingACard 가 막은 것은 "없는 cc 로 카드를 만드는 것"이고,
// 그 결과 사람은 안전하게 거절당한다. 그런데 거절만으로는 **원래 하려던 일**을 못 한다 —
// 갈린 카드를 닫는 것. 이 시험이 그 일을 잰다.
//
// 재현 조건은 실물 그대로다: `/clear` 가 오면 훅이 카드의 cc 를 새 값으로 rekey 하는데,
// 이미 뜬 MCP 프로세스의 environ 은 안 바뀐다(mcpsrv/drift.go 머리말의 실측).
// 그래서 3중키 조회는 **아무것도 못 찾고**, cc 축으로는 그 카드에 손이 닿지 않는다.
// 카드 id 는 rekey 를 건너 보존되므로(store.Rekey 는 cc 컬럼만 UPDATE 한다) 그 축만 남는다.
func TestCloseBySessionIDClosesCardWhoseCCMovedAway(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "open"); code != 0 {
		t.Fatalf("open 실패(%d): %s", code, out)
	}
	live := h.liveSessions()
	if len(live) != 1 {
		t.Fatalf("전제가 깨졌다 — 살아 있는 카드가 %d건이다, want 1", len(live))
	}
	card := live[0].Session.ID

	if _, err := h.st.Rekey(context.Background(), card, "cc-after-clear"); err != nil {
		t.Fatalf("rekey 실패: %v", err)
	}

	// ★ **대조를 먼저 세운다.** 이것이 없으면 아래 초록이 "id 로 닫았다"인지
	// "rekey 가 안 먹어서 cc 로도 닫혔을 것"인지 구분되지 않는다.
	if code, out := h.run("", "close"); code == 0 {
		t.Fatalf("전제가 깨졌다 — cc 축으로 닫혔다. rekey 가 카드를 안 옮긴 것이다:\n%s", out)
	}

	code, out := h.run("", "close", "--session", card)
	if code != 0 {
		t.Fatalf("카드 id 로 못 닫았다(%d) — 배너가 내는 유일한 안정 축이 쓸 데가 없다:\n%s", code, out)
	}
	if got := len(h.liveSessions()); got != 0 {
		t.Fatalf("닫았다고 보고했는데 살아 있는 세션이 %d건이다", got)
	}
}

// 카드 id 입구도 **선점 가드를 그대로 지난다.**
//
// ★ 입구를 새로 내는 일의 상시 위험이 이것이다 — 새 갈래가 기존 갈래의 가드를 우회한다.
// 여기서 우회되면 done 카드가 선점을 든 채 ListLive 에서 빠지고, 그 항목은 아무도 못 집는데
// 누가 잡았는지도 안 보인다(runClose 머리말). "우회할 필드가 있으면 우회된다"의 실물이다.
func TestCloseBySessionIDStillRefusesWhileHoldingClaims(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "add", "--id", "it-1", "--title", "제목", "--body", "본문"); code != 0 {
		t.Fatalf("add 실패(%d): %s", code, out)
	}
	if code, out := h.run("", "pick", "it-1"); code != 0 {
		t.Fatalf("pick 실패(%d): %s", code, out)
	}
	live := h.liveSessions()
	if len(live) != 1 {
		t.Fatalf("전제가 깨졌다 — 살아 있는 카드가 %d건이다, want 1", len(live))
	}
	card := live[0].Session.ID

	code, out := h.run("", "close", "--session", card)
	if code == 0 {
		t.Fatalf("id 입구가 선점 가드를 우회했다 — 그 선점이 보드에서 사라진다:\n%s", out)
	}
	if !strings.Contains(out, "it-1") {
		t.Errorf("무엇이 남았는지를 안 냈다 — 사유 없는 거절은 다음 사람이 못 푼다:\n%s", out)
	}
	if !strings.Contains(out, "fd finish") {
		t.Errorf("처방(fd finish)을 안 냈다 — 입구가 달라도 받는 처방은 같아야 한다:\n%s", out)
	}
	if got := len(h.liveSessions()); got != 1 {
		t.Errorf("거절했는데 살아 있는 세션이 %d건이다", got)
	}
}

// 두 입구를 **함께 주면 거절한다.**
//
// ★ 둘이 같은 카드를 가리키는 조합으로 잰다. 그래야 "충돌해서 거절"이 아니라
// **"둘을 함께 주는 것 자체를 거절"** 임이 증명된다 — 어긋난 조합으로 재면 값 비교로
// 통과시키는 구현도 초록이 되고, 그러면 사람은 어느 축이 이겼는지 모르는 채 카드를 닫는다.
//
// ★ 플래그를 **줬는가**로 판정해야 한다. 값으로 접으면 `--cc-session ""` 이 "안 줬다"와
// 구분되지 않아 조용히 id 갈래로 흐른다(flagsSet 이 있는 이유가 그것이다).
func TestCloseRefusesBothEntrancesAtOnce(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "open"); code != 0 {
		t.Fatalf("open 실패(%d): %s", code, out)
	}
	live := h.liveSessions()
	if len(live) != 1 {
		t.Fatalf("전제가 깨졌다 — 살아 있는 카드가 %d건이다, want 1", len(live))
	}
	card := live[0].Session.ID

	// 둘 다 이 카드를 가리킨다 — env 의 cc 가 그 카드를 연 값이다(harness_test.go).
	code, out := h.run("", "close", "--session", card, "--cc-session", "cc-session-uuid-1")
	if code == 0 {
		t.Fatalf("두 입구를 함께 줬는데 닫았다 — 어느 축이 이겼는지 사람이 모른다:\n%s", out)
	}
	if got := len(h.liveSessions()); got != 1 {
		t.Fatalf("거절했는데 살아 있는 세션이 %d건이다", got)
	}
	mustContain(t, "거절 사유", out, "--session", "--cc-session")

	// 빈 값도 **준 것**이다.
	code, out = h.run("", "close", "--session", card, "--cc-session", "")
	if code == 0 {
		t.Fatalf("--cc-session \"\" 을 값으로 접어 id 갈래로 흘렸다 — 준 것과 안 준 것이 같아졌다:\n%s", out)
	}
	if got := len(h.liveSessions()); got != 1 {
		t.Fatalf("거절했는데 살아 있는 세션이 %d건이다", got)
	}
}

// 닫은 뒤에도 신호 하나면 살아난다 — store 의 안전핀이 cmd 계층에서도 도는지 본다.
func TestClosedSessionRevivesOnNextBeat(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "open"); code != 0 {
		t.Fatalf("open 실패(%d): %s", code, out)
	}
	if code, out := h.run("", "close"); code != 0 {
		t.Fatalf("close 실패(%d): %s", code, out)
	}
	if got := len(h.liveSessions()); got != 0 {
		t.Fatalf("닫았는데 살아 있는 세션이 %d건이다", got)
	}
	if code, out := h.run("", "beat", "--kind", "prompt"); code != 0 {
		t.Fatalf("beat 실패(%d): %s", code, out)
	}
	if got := len(h.liveSessions()); got != 1 {
		t.Fatalf("닫힌 세션이 신호를 냈는데 안 살아났다(살아 있는 세션 %d건) — 그 세션의 발자국을 아무도 못 본다", got)
	}
}

// finish 는 **기본으로 세션을 안 닫는다.**
//
// ★ 항목 하나를 끝내도 세션은 다음 항목으로 갈 수 있다. 거기서 자동으로 닫으면
// 살아 있는 세션이 보드에서 사라지고, 그 사이 남들의 겹침 판정이 이 세션을 못 본다.
func TestFinishDoesNotCloseSessionByDefault(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "add", "--id", "it-1", "--title", "제목", "--body", "본문"); code != 0 {
		t.Fatalf("add 실패(%d): %s", code, out)
	}
	if code, out := h.run("", "pick", "it-1"); code != 0 {
		t.Fatalf("pick 실패(%d): %s", code, out)
	}
	if code, out := h.run("", "finish", "it-1", "--body", "왜 그렇게 했나"); code != 0 {
		t.Fatalf("finish 실패(%d): %s", code, out)
	}
	if got := len(h.liveSessions()); got != 1 {
		t.Fatalf("finish 가 세션을 닫았다(살아 있는 세션 %d건) — 다음 항목으로 갈 세션이 보드에서 사라진다", got)
	}
}

// --close 를 주면 항목을 끝내고 세션도 닫는다.
func TestFinishWithCloseClosesSession(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "add", "--id", "it-1", "--title", "제목", "--body", "본문"); code != 0 {
		t.Fatalf("add 실패(%d): %s", code, out)
	}
	if code, out := h.run("", "pick", "it-1"); code != 0 {
		t.Fatalf("pick 실패(%d): %s", code, out)
	}
	code, out := h.run("", "finish", "it-1", "--body", "왜 그렇게 했나", "--close")
	if code != 0 {
		t.Fatalf("finish --close 실패(%d): %s", code, out)
	}
	if got := len(h.liveSessions()); got != 0 {
		t.Fatalf("--close 인데 살아 있는 세션이 %d건이다", got)
	}
}

// liveSessions 는 서버가 실제로 들고 있는 살아 있는 세션이다.
//
// ★ 창을 제로값으로 준다. 이 시험들이 보는 축은 **state 이지 신호의 나이가 아니다** —
// 창으로 걸러 버리면 "닫혀서 빠졌다"와 "오래돼서 빠졌다"가 구분되지 않는다.
func (h *harness) liveSessions() []model.SessionView {
	h.t.Helper()
	live, err := h.st.ListLive(context.Background(), h.project, time.Time{})
	if err != nil {
		h.t.Fatalf("살아 있는 세션 조회 실패: %v", err)
	}
	return live
}
