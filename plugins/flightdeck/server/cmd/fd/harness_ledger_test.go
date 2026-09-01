package main

import (
	"context"
	"testing"

	"github.com/kweiza/flightdeck/internal/mcpsrv"
)

// 선언이 **원장까지 닿는가** — 이 사슬의 마지막 칸이다.
//
// 저장층(store.OpenSessionAs)만 있으면 칼럼은 있는데 아무도 안 채운다. 선언은
// cmd/fd 의 --harness 에서 출발해 REST 를 건너 session.harness 에 앉아야 하고,
// 그 사이 계층이 넷이다(App → openReq → api → service → store). 한 칸만 빠져도
// **조용히** 빈 값이 남는다 — 그리고 빈 값은 「미상」과 구별되지 않으므로
// 어느 화면에서도 결함으로 안 보인다.

// envFor 는 그 하네스의 세션 id **하나만** 찬 환경이다.
//
// ★ 두 축을 다 채우면 미선언 호출이 부딪힘 관문에 걸린다(identity-harness-nesting-gate).
// 여기서 재려는 것은 그 관문이 아니라 **선언이 원장까지 닿는가**이므로 축을 하나로 둔다.
func envFor(h *harness, harnessName string) map[string]string {
	env := map[string]string{}
	for k, v := range h.env {
		env[k] = v
	}
	delete(env, mcpsrv.EnvSessionID)
	delete(env, mcpsrv.EnvCodexSessionID)
	env[mcpsrv.SessionEnvFor(harnessName)] = "sess-uuid-" + harnessName
	return env
}

// harnessOf 는 이 프로젝트의 유일한 세션 카드가 실은 하네스다.
func harnessOf(t *testing.T, h *harness) string {
	t.Helper()
	sessions, err := h.st.ListSessions(context.Background(), h.project)
	if err != nil {
		t.Fatalf("세션 목록 조회 실패: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("세션이 %d건이다 — 이 시험은 1건을 전제한다: %+v", len(sessions), sessions)
	}
	return sessions[0].Harness
}

func TestDeclaredHarnessReachesTheLedger(t *testing.T) {
	for _, want := range []string{"claude", "codex"} {
		t.Run(want, func(t *testing.T) {
			h := newHarness(t)
			if code, out := h.runEnv(envFor(h, want), "", "--harness", want, "open"); code != 0 {
				t.Fatalf("open 이 %d 로 끝났다:\n%s", code, out)
			}
			if got := harnessOf(t, h); got != want {
				t.Fatalf("원장의 하네스가 %q 다 — %q 여야 한다.\n"+
					"선언이 App → REST → service → store 사슬 어딘가에서 끊겼다", got, want)
			}
		})
	}
}

// 선언이 없으면 원장도 비어 있다 — claude 로 접지 않는다.
//
// ★ 이것이 이 항목의 「소급해서 채우지 마라」와 같은 규율의 실행시 판본이다.
// 접으면 codex 설치가 인자를 빠뜨린 날 그 카드가 조용히 claude 로 들어간다.
func TestUndeclaredHarnessStaysEmptyInTheLedger(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "open"); code != 0 {
		t.Fatalf("open 이 %d 로 끝났다:\n%s", code, out)
	}
	if got := harnessOf(t, h); got != "" {
		t.Fatalf("선언이 없는데 원장의 하네스가 %q 다 — 「미상」은 빈 값이다", got)
	}
}

// ★ 미선언 재개가 기존 값을 안 지운다 — **실행 경로로** 단정한다.
//
// store 단위 시험이 같은 규칙을 이미 물지만, 그것은 저장층이 규칙을 지키는지만 본다.
// 여기서 보는 것은 **오늘 함대의 실제 모양**이다: codex 훅이 --harness 를 싣고 연 카드에
// Claude 쪽 설치물(아직 인자를 안 싣는다)이 같은 3중키로 닿는 순간.
func TestUndeclaredReopenDoesNotEraseTheLedgerHarness(t *testing.T) {
	h := newHarness(t)
	// ★ codex 축 하나만 찬 환경이다 — 그래야 미선언 재개가 **같은 3중키**로 떨어진다.
	env := envFor(h, "codex")
	if code, out := h.runEnv(env, "", "--harness", "codex", "open"); code != 0 {
		t.Fatalf("첫 open 이 %d 로 끝났다:\n%s", code, out)
	}
	if code, out := h.runEnv(env, "", "open"); code != 0 {
		t.Fatalf("재개 open 이 %d 로 끝났다:\n%s", code, out)
	}
	if got := harnessOf(t, h); got != "codex" {
		t.Fatalf("미선언 재개 뒤 하네스가 %q 다 — codex 가 남아야 한다", got)
	}
}
