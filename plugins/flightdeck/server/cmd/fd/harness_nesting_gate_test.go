package main

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/mcpsrv"
)

// 관문이 **CLI 에도** 서는지 본다.
//
// ★ 이 파일이 없으면 봉인이 반쪽이다. mcpsrv 의 ResolveIdentityAs 에만 갈래를 넣으면
// MCP 는 막히고 **맨손 CLI 는 그대로 뚫린다** — 그리고 이 항목이 막으려는 것이 바로
// 그 맨손 경로다(codex 에 fd 를 깔면 열린다).
//
// 구조적 이유: App.ccSessionID 가 훑기 로직의 **사본**을 들고 있어 그 함수를 안 지나간다.
// identity.go 가 "이 파일이 유일한 정체의 원천"이라 선언했으므로 사본 쪽이 결함이다.

// nestedEnv 는 두 하네스의 세션 id 가 동시에 찬 환경이다 — 중첩 기동의 실물.
// (Claude 의 Bash 에서 codex 를 띄우면 이 모양이 된다. 이 함대에서 흔하다.)
func nestedEnv(h *harness, extra map[string]string) map[string]string {
	env := map[string]string{}
	for k, v := range h.env {
		env[k] = v
	}
	env[mcpsrv.EnvCodexSessionID] = "codex-session-uuid-1"
	for k, v := range extra {
		env[k] = v
	}
	return env
}

// 세션 귀속 명령은 거절한다 — 그리고 화면이 **무엇이 부딪혔는지**와 **나가는 길**을 낸다.
func TestCLIRejectsNestedHarnessOnSessionBoundCommands(t *testing.T) {
	h := newHarness(t)
	code, out := h.runEnv(nestedEnv(h, nil), "", "open")
	if code == 0 {
		t.Fatalf("open 이 성공했다 — 두 하네스가 동시에 관측된 실행이다:\n%s", out)
	}
	for _, want := range []string{
		"하네스가 부딪힌다",
		mcpsrv.EnvSessionID, mcpsrv.EnvCodexSessionID,
		"--harness",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("출력에 %q 가 없다:\n%s", want, out)
		}
	}
	// ★ 옛 문구가 남아 있으면 안 된다 — 이 경우 세션 id 는 **읽혔다**(둘이나).
	// "못 읽었다" 는 거짓이고, 거짓 안내는 사람을 없는 결함으로 보낸다.
	if strings.Contains(out, "를 못 읽었다") {
		t.Fatalf("거짓 안내가 났다 — 축은 읽혔고 둘인 것이 문제다:\n%s", out)
	}
}

// 선언을 실으면 통과한다 — 탈출구가 실제로 열려 있다는 단정이다.
//
// 이 시험이 없으면 "거절한다"만 구현하고 나가는 길을 막아 놓아도 아무도 안 깨진다.
func TestCLIDeclaredHarnessPassesNesting(t *testing.T) {
	for _, h := range []string{mcpsrv.HarnessClaude, mcpsrv.HarnessCodex} {
		t.Run(h, func(t *testing.T) {
			hs := newHarness(t)
			code, out := hs.runEnv(nestedEnv(hs, nil), "", "--harness", h, "open")
			if code != 0 {
				t.Fatalf("--harness %s 를 실었는데 %d 로 끝났다:\n%s", h, code, out)
			}
		})
	}
}

// ★ 회귀 방지 — 축이 **하나만** 찬 경우는 오늘 거동 그대로다.
//
// 선언 없이 부르는 것은 지금 함대의 정상 상태다(Claude 쪽 설치물이 아직 --harness 를
// 안 싣는다). 여기서 막으면 살아 있는 모든 세션이 멈춘다.
func TestCLISingleAxisIsUnchanged(t *testing.T) {
	h := newHarness(t)
	code, out := h.run("", "open")
	if code != 0 {
		t.Fatalf("축이 하나뿐인데 open 이 %d 로 끝났다 — 부딪힘이 아니다:\n%s", code, out)
	}
	if strings.Contains(out, "하네스가 부딪힌다") {
		t.Fatalf("축이 하나뿐인데 부딪힘을 말한다:\n%s", out)
	}
}
