package mcpsrv

import (
	"strings"
	"testing"
)

// 중첩 기동 — 두 하네스의 세션 id 가 **동시에** 관측되는데 선언이 없는 경우.
//
// 이 함대에서 흔한 모양이다(Claude 의 Bash 에서 codex 를 띄운다). 그때
// harnessProbeOrder 가 claude 를 먼저 집고 break 하므로, 오늘은 codex 작업이
// 조용히 Claude 카드로 들어간다 — 경고도 거절도 0건이다.
//
// 소비자 좌표계를 지킨다: Identity 필드가 아니라 **거절 사유와 배너 문구**로 단정한다.

// nested 는 두 축이 다 찬 환경이다. 이 파일의 모든 시험이 같은 환경을 본다.
func nested(t *testing.T) Identity {
	t.Helper()
	return ResolveIdentity(env(map[string]string{
		EnvSessionID:      "claude-uuid",
		EnvCodexSessionID: "codex-uuid",
		EnvProjectDir:     "/home/a/proj",
	}), "/home/a/proj", nil, "box", nil)
}

// 세션 귀속 도구는 거절한다 — 그리고 사유가 나가는 길을 직접 말해야 한다.
//
// 거절인 이유: 값이 **비어** 있는 것이 아니라 **차 있는데 틀렸다**. GateTool 이
// 이미 세운 기준("익명으로 진행하면 그 행이 거짓이 된다")이 그대로 적용된다.
func TestNestedHarnessRejectsSessionBoundTools(t *testing.T) {
	id := nested(t)
	for _, tool := range []string{"pick", "note", "add", "finish", "land", "label"} {
		t.Run(tool, func(t *testing.T) {
			ok, reason := GateTool(tool, id)
			if ok {
				t.Fatalf("GateTool(%q) 가 통과했다 — 두 하네스가 동시에 관측된 실행이다 (사유: %s)", tool, reason)
			}
			// 막기만 하고 나가는 길을 안 적으면 문서가 사용자를 버린다.
			if !strings.Contains(reason, "--harness") {
				t.Fatalf("거절 사유가 탈출구를 안 낸다: %s", reason)
			}
			// 두 축의 이름이 사유에 있어야 "무엇이 부딪혔나"를 사람이 안다.
			for _, want := range []string{EnvSessionID, EnvCodexSessionID} {
				if !strings.Contains(reason, want) {
					t.Fatalf("거절 사유에 %q 가 없다: %s", want, reason)
				}
			}
		})
	}
}

// board·alloc 은 그대로 통과한다 — 이 항목은 게이트의 **층위를 안 바꾼다.**
//
// 읽기까지 막으면 배너가 "서버가 통째로 죽었다"로 읽히고, 그러면 사람이
// 무엇이 부딪혔는지 알아낼 화면 자체를 잃는다.
func TestNestedHarnessStillAnswersReadOnlyTools(t *testing.T) {
	id := nested(t)
	for _, tool := range []string{"board", "alloc"} {
		t.Run(tool, func(t *testing.T) {
			ok, reason := GateTool(tool, id)
			if !ok {
				t.Fatalf("GateTool(%q) 가 거절했다 — 세션 귀속이 없는 도구다 (사유: %s)", tool, reason)
			}
		})
	}
}

// 선언이 있으면 부딪힘이 아니다 — 두 축이 다 차 있어도 통과한다.
//
// ★ 이것이 탈출구가 실제로 열려 있다는 단정이다. 이 시험이 없으면
// "거절한다"만 구현하고 나가는 길을 막아 놓아도 아무도 안 깨진다.
func TestDeclaredHarnessPassesEvenWhenBothAxesAreSet(t *testing.T) {
	both := map[string]string{
		EnvSessionID:      "claude-uuid",
		EnvCodexSessionID: "codex-uuid",
		EnvProjectDir:     "/home/a/proj",
	}
	for _, h := range []string{HarnessClaude, HarnessCodex} {
		t.Run(h, func(t *testing.T) {
			id := ResolveIdentityAs(h, env(both), "/home/a/proj", nil, "box", nil)
			ok, reason := GateTool("note", id)
			if !ok {
				t.Fatalf("--harness %s 를 실었는데 거절했다: %s", h, reason)
			}
			// 선언한 하네스의 세션 id 를 집어야 한다 — 아무 값이나 집고 통과하면
			// 관문은 섰는데 귀속은 여전히 틀린다.
			want := map[string]string{HarnessClaude: "claude-uuid", HarnessCodex: "codex-uuid"}[h]
			if id.CCSessionID != want {
				t.Fatalf("--harness %s 인데 세션 id 가 %q 다 — %q 여야 한다", h, id.CCSessionID, want)
			}
		})
	}
}

// bannerCollisionMark 는 **부딪힘 고유의** 표지다.
//
// ★ "--harness" 로는 못 가른다 — 축이 하나만 찬 경우의 기존 경고(갈래 (가))도
// 그 문자열을 이미 낸다("설치물이 --harness 를 실어야 한다"). 두 화면을 가르는
// 어구가 따로 없으면 회귀 시험이 부딪힘과 정상 경고를 구별하지 못한다.
const bannerCollisionMark = "하네스가 부딪힌다"

// 배너가 이 축을 말한다 — 거절당한 도구만 보면 "이 도구가 원래 안 되나"로 읽힌다.
func TestNestedHarnessBannerNamesTheCollision(t *testing.T) {
	b := nested(t).Banner()
	for _, want := range []string{bannerCollisionMark, EnvSessionID, EnvCodexSessionID, "--harness"} {
		if !strings.Contains(b, want) {
			t.Fatalf("배너에 %q 가 없다:\n%s", want, b)
		}
	}
}

// 오타난 하네스 이름도 같은 관문을 지난다.
//
// ★ `--harness codx` 는 선언이 **아니다** — 아는 이름이 아니므로 「미상」으로 접히고
// 훑기로 떨어진다. 그 갈래에도 사본이 아니라 같은 판정이 서야 한다. 안 그러면
// 오타 하나로 봉인이 통째로 열리고, 오타는 어떤 시험도 안 깨서 조용하다.
func TestUnknownHarnessNameStillHitsTheGate(t *testing.T) {
	id := ResolveIdentityAs("codx", env(map[string]string{
		EnvSessionID:      "claude-uuid",
		EnvCodexSessionID: "codex-uuid",
		EnvProjectDir:     "/home/a/proj",
	}), "/home/a/proj", nil, "box", nil)

	ok, reason := GateTool("note", id)
	if ok {
		t.Fatalf("모르는 이름 + 두 축인데 통과했다 (사유: %s)", reason)
	}
	if !strings.Contains(reason, "하네스가 부딪힌다") {
		t.Fatalf("거절 사유가 부딪힘을 안 말한다: %s", reason)
	}
	// 오타 자체도 계속 말해야 한다 — 두 사실이 함께 있어야 사람이 무엇을 고칠지 안다.
	if !strings.Contains(id.Banner(), "모르는 하네스 이름이다") {
		t.Fatalf("배너가 오타를 안 말한다:\n%s", id.Banner())
	}
}

// ★ 회귀 방지 — 축이 **하나만** 찬 경우는 오늘 거동 그대로여야 한다.
//
// identity.go 의 ★("무조건 경고하지 마라 — 잡음이 붙으면 사람이 배너를 안 읽는다")가
// 이 항목으로 깨지는 것을 막는다. 선언 없이 부르는 것은 지금 함대의 정상 상태다.
func TestSingleAxisIsUnchanged(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{"claude 축만", map[string]string{EnvSessionID: "u", EnvProjectDir: "/home/a/proj"}},
		{"codex 축만", map[string]string{EnvCodexSessionID: "u", EnvProjectDir: "/home/a/proj"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := ResolveIdentity(env(c.env), "/home/a/proj", nil, "box", nil)
			if ok, reason := GateTool("note", id); !ok {
				t.Fatalf("축이 하나뿐인데 거절했다 — 부딪힘이 아니다: %s", reason)
			}
			// ★ "--harness" 로 단정하면 안 된다 — codex 축만 찬 경우의 기존 경고가
			// 그 문자열을 정당하게 낸다. 부딪힘 고유의 표지로만 가른다.
			if strings.Contains(id.Banner(), bannerCollisionMark) {
				t.Fatalf("축이 하나뿐인데 배너가 부딪힘을 말한다:\n%s", id.Banner())
			}
		})
	}
}
