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
