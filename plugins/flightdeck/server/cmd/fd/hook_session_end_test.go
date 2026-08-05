package main

import (
	"encoding/json"
	"testing"
)

// reason=clear 면 그 cc 의 카드를 닫는다.
func TestSessionEndClearClosesCard(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "open"); code != 0 {
		t.Fatalf("open 실패(%d): %s", code, out)
	}

	if code, out := h.run(sessionEndPayload(t, "clear"), "hook", "session-end"); code != 0 {
		t.Fatalf("훅은 항상 0 이어야 한다(%d): %s", code, out)
	}
	if got := len(h.liveSessions()); got != 0 {
		t.Fatalf("/clear 인데 카드가 %d건 살아 있다 — 죽은 cc 의 고아가 남는다", got)
	}
}

// ★ matcher 를 못 믿는 경우의 이중 방어. hooks.json 의 matcher 가 바뀌거나 플랫폼이
// 다른 사유를 쏘기 시작하면, 이 갈래가 없을 때 살아 있는 세션이 조용히 닫힌다.
//
// resume 이 여기 있는 것이 특히 중요하다 — /fork 도 같은 사유로 오므로 일부러 뺐고,
// 그 결정을 코드가 아니라 이 시험이 붙들고 있다.
func TestSessionEndIgnoresEveryReasonButClear(t *testing.T) {
	for _, reason := range []string{"resume", "logout", "prompt_input_exit", "other", ""} {
		name := reason
		if name == "" {
			name = "빈사유"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			if code, out := h.run("", "open"); code != 0 {
				t.Fatalf("open 실패(%d): %s", code, out)
			}
			if code, out := h.run(sessionEndPayload(t, reason), "hook", "session-end"); code != 0 {
				t.Fatalf("훅은 항상 0 이어야 한다(%d): %s", code, out)
			}
			if got := len(h.liveSessions()); got != 1 {
				t.Fatalf("reason=%q 인데 카드를 닫았다(살아 있는 세션 %d건)", reason, got)
			}
		})
	}
}

// 선점을 든 카드는 안 닫는다 — rekey 가 거절되면 그 선점이 통째로 안 보이게 된다.
func TestSessionEndKeepsCardHoldingClaims(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "add", "--id", "it-1", "--title", "제목", "--body", "본문"); code != 0 {
		t.Fatalf("add 실패(%d): %s", code, out)
	}
	if code, out := h.run("", "pick", "it-1"); code != 0 {
		t.Fatalf("pick 실패(%d): %s", code, out)
	}
	if code, out := h.run(sessionEndPayload(t, "clear"), "hook", "session-end"); code != 0 {
		t.Fatalf("훅은 항상 0 이어야 한다(%d): %s", code, out)
	}
	if got := len(h.liveSessions()); got != 1 {
		t.Fatalf("선점을 든 카드를 닫았다 — 그 선점이 보드에서 사라진다(살아 있는 세션 %d건)", got)
	}
}

// 페이로드가 깨져도 종료코드는 0 이다. 훅이 세션을 막으면 안 된다.
func TestSessionEndNeverBlocksTheSession(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("이건 JSON 이 아니다", "hook", "session-end"); code != 0 {
		t.Fatalf("깨진 페이로드에 종료코드 %d 를 냈다 — 훅이 세션을 막는다: %s", code, out)
	}
}

// sessionEndPayload 는 플랫폼이 주는 SessionEnd stdin 이다.
// 필드 이름은 설치본 2.1.222 의 zod 스키마와 같다: 기본 훅 필드 + hook_event_name + reason.
func sessionEndPayload(t *testing.T, reason string) string {
	t.Helper()
	buf, err := json.Marshal(map[string]any{
		"session_id":      "cc-session-uuid-1",
		"cwd":             ".",
		"hook_event_name": "SessionEnd",
		"reason":          reason,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(buf)
}
