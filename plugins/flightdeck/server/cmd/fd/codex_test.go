package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/service"
)

// codex 설치 자산의 관문.
//
// ★ 여기서 무는 것 중 둘은 **되돌리기가 유난히 비싸다.** 신뢰 해시는 명령 문자열만 보므로
// 템플릿의 명령이 나중에 한 글자라도 바뀌면 **이미 신뢰한 사용자 전원이 TUI 재승인**이다.
// 그래서 `--harness codex` 와 고정 경로는 **처음부터** 들어 있어야 하고, 그 사실을 시험이
// 문다. 나중에 고치면 되는 종류가 아니다.

// fdHookNames 는 fd 진입점이 아는 훅 이름이다(hook.go 의 switch 와 같아야 한다).
var fdHookNames = map[string]bool{
	"session-start": true, "user-prompt": true, "post-tool": true, "pre-compact": true,
	"stop": true, "session-end": true,
}

// TestCodexHooksTemplateIsWiredAsDesigned 는 embed 된 템플릿 자체를 문다.
func TestCodexHooksTemplateIsWiredAsDesigned(t *testing.T) {
	const wrapper = "/home/someone/.local/bin/fd-hook"
	rendered := RenderCodexHooks(codexHooksTemplate, wrapper)

	if strings.Contains(rendered, "__FD_HOOK_PATH__") {
		t.Fatal("치환 뒤에도 플레이스홀더가 남았다 — 훅이 없는 파일을 부르고, 그 실패는 codex 의 침묵 뒤에 숨는다")
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("템플릿이 유효한 JSON 이 아니다: %v — codex 는 훅을 통째로 못 읽는다", err)
	}
	if len(doc.Hooks) == 0 {
		t.Fatal("템플릿에 훅이 하나도 없다")
	}

	// ★ **여섯 이벤트를 표로 문다.** glob 처럼 "있는 것만" 보면 이벤트가 사라진 날
	// 그 사실이 화면에서 침묵한다(repo_hooks_test.go 의 같은 규율).
	//
	// ★★ **SessionEnd 는 2026-09-01 에 재고 넣었다** — 앞 판이 "발화를 못 봤다"고 뺀 자리다.
	// 그 관측이 틀렸다: **발화는 한다. codex 가 그것을 로그에 안 찍을 뿐이다.**
	// 실측(codex-cli 0.151.0, `codex exec`): 훅 래퍼 자신에 프로브를 심어 재니
	// `event=session-end … rc=0` 이 매 실행마다 남는데, 같은 실행의 codex stderr 에는
	// `hook: SessionEnd` 가 **한 줄도 없다**(SessionStart·UserPromptSubmit·PostToolUse·Stop 은
	// 전부 찍힌다). `--dangerously-bypass-hook-trust` 로 신뢰 축까지 배제하고 갈랐다.
	//
	// ★ 그러므로 **codex 로그의 무출력을 미발화로 읽지 마라.** 이 저장소가 반복해서 만난
	// "관문의 무출력은 통과가 아니다"의 거울상이다 — 여기서는 무출력을 **미발화**로 읽었고,
	// 그 오독 하나가 훅 하나를 판 넷에 걸쳐 안 싣게 만들었다. 발화를 재려면 로그가 아니라
	// **훅 자신**에 프로브를 심어라.
	//
	// ★★ **다만 그 발화는 codex 판에 달렸다(2026-09-02 리눅스 실측).** `0.137.0` 에는
	// 이 이벤트가 **아예 없다** — 바이너리에 `session_end` 가 어떤 표기로도 0건이고
	// (대조군 `session_start` 는 11건), 훅에 실어도 프로브에 한 번도 안 잡힌다.
	// **그래도 여기서 요구하는 것이 옳다**: 같은 판에 이 항목이 든 hooks.json 을 줘도
	// 나머지 다섯이 전부 정상 발화한다(통째 거부가 아니라 조용한 무시다). 안 실으면
	// 0.151 사용자가 카드를 못 닫고, 실으면 0.137 사용자는 그 훅만 안 돈다 — 후자가 싸다.
	want := map[string]bool{
		"SessionStart": true, "UserPromptSubmit": true, "PostToolUse": true,
		"PreCompact": true, "Stop": true, "SessionEnd": true,
	}
	for ev := range doc.Hooks {
		if !want[ev] {
			t.Errorf("템플릿에 표에 없는 이벤트 %q 가 있다 — 표를 먼저 고쳐라", ev)
		}
	}
	for ev := range want {
		if _, ok := doc.Hooks[ev]; !ok {
			t.Errorf("템플릿에 %s 가 없다", ev)
		}
	}

	for ev, groups := range doc.Hooks {
		for _, g := range groups {
			// ★ PostToolUse 에 matcher 를 주면 안 된다. codex 의 tool_name 축은 닫힌
			// 열거가 아니고(hook.go 의 codex 갈래), 추측이 틀리면 발자국이 통째로 0건이 된다.
			if ev == "PostToolUse" && strings.TrimSpace(g.Matcher) != "" {
				t.Errorf("PostToolUse 에 matcher %q 가 있다 — codex 도구 이름은 닫힌 열거가 "+
					"아니라 틀리면 발자국이 통째로 0건이 된다", g.Matcher)
			}
			for _, h := range g.Hooks {
				if h.Type != "command" {
					t.Errorf("%s 의 type 이 %q 다", ev, h.Type)
				}
				if h.Timeout <= 0 {
					t.Errorf("%s 에 타임아웃이 없다 — 훅이 안 끊기면 세션이 멈춘다", ev)
				}
				// ★ 두 하네스가 **같은 예산**을 갖게 잠근다. 한쪽만 고치면 같은 콜드
				// 스타트에서 codex 세션만 조용히 처방을 잃는다(근거는 저쪽 상수 주석).
				if promptPathHooks[ev] && h.Timeout < promptPathHookMinTimeout {
					t.Errorf("codex 템플릿의 %s 예산이 %d초다 — 콜드 스타트 실측 3.36초를 못 덮는다. "+
						"claude 쪽(hooks/hooks.json)과 같은 최소 %d초여야 한다",
						ev, h.Timeout, promptPathHookMinTimeout)
				}
				fields := strings.Fields(h.Command)
				harness, rest := SplitHarnessFlag(fields)
				// ★ **처음부터 실려 있어야 한다.** 나중에 추가하면 명령 문자열이 바뀌어
				// 이미 신뢰한 사용자 전원이 TUI 재승인이다 — 이 항목에서 되돌리기 제일
				// 비싼 실수가 이것이다.
				if harness != "codex" {
					t.Errorf("%s 의 명령이 --harness codex 를 안 싣는다(%q) — "+
						"나중에 넣으면 신뢰한 사용자 전원이 재승인이다", ev, h.Command)
				}
				// ★ 고정 경로여야 한다. 버전이 든 경로면 fd 판올림마다 재승인이고,
				// 재승인 전까지 훅은 조용히 안 돈다 — 이 항목의 핵심 주장이다.
				if codexVersionedCommand(h.Command) {
					t.Errorf("%s 의 명령이 버전 든 경로를 부른다(%q) — 판올림마다 재승인이다",
						ev, h.Command)
				}
				if !strings.Contains(h.Command, wrapper) {
					t.Errorf("%s 의 명령이 래퍼 경로를 안 부른다(%q)", ev, h.Command)
				}
				if len(rest) == 0 {
					t.Fatalf("%s 의 명령에 하네스 선언 말고 아무것도 없다(%q)", ev, h.Command)
				}
				if name := rest[len(rest)-1]; !fdHookNames[name] {
					t.Errorf("%s 가 fd 가 모르는 훅 이름 %q 를 부른다 — fd 는 fail-open 이라 "+
						"조용히 아무것도 안 하고 0 을 낸다", ev, name)
				}
			}
		}
	}
}

// TestCodexWrapperScriptIsUsable 는 embed 된 래퍼가 실제로 쓸 수 있는 물건인지 문다.
func TestCodexWrapperScriptIsUsable(t *testing.T) {
	s := string(codexWrapperScript)
	if strings.TrimSpace(s) == "" {
		t.Fatal("래퍼가 비었다 — embed 가 안 붙었다")
	}
	if !strings.HasPrefix(s, "#!") {
		t.Fatal("래퍼에 shebang 이 없다 — exec 되지 않는다")
	}
	// ★ 래퍼는 **정식 설치본만** 골라야 한다. 저장소 체크아웃을 가리키면 낡은 판이
	// 최신인 척 돌고 아무도 모른다.
	if !strings.Contains(s, ".claude/plugins/cache") {
		t.Error("래퍼가 플러그인 캐시를 안 본다 — 정식 설치본을 고르는 것이 이 파일의 계약이다")
	}
	// ★ 훅에서 불리므로 실패해도 종료코드 0 이어야 한다. 끊기면 세션이 안 뜬다.
	if !strings.Contains(s, "exit 0") {
		t.Error("래퍼에 fail-open 종료가 없다 — 훅이 실패로 끊기면 세션이 안 뜬다")
	}
}

func TestCodexHookCommands(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		want   int
		parsed bool
	}{
		{"빈 것", "", 0, false},
		{"깨진 JSON", `{"hooks":`, 0, false},
		{"훅 없음", `{"hooks":{}}`, 0, true},
		{"둘", `{"hooks":{"Stop":[{"hooks":[{"command":"a"},{"command":"b"}]}]}}`, 2, true},
		{"빈 명령은 안 센다", `{"hooks":{"Stop":[{"hooks":[{"command":"  "}]}]}}`, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := codexHookCommands(c.raw)
			if ok != c.parsed {
				t.Fatalf("parsed=%v, %v 를 기대했다", ok, c.parsed)
			}
			if len(got) != c.want {
				t.Fatalf("명령 %d개, %d개를 기대했다: %v", len(got), c.want, got)
			}
		})
	}
}

func TestCodexTrusted(t *testing.T) {
	const hooks = "/home/x/.codex/hooks.json"
	cfg := `[hooks.state."` + hooks + `:SessionStart:0:0"]` + "\ntrusted_hash = \"sha256:abc\"\n"
	if !codexTrusted(cfg, hooks) {
		t.Error("신뢰가 박힌 config 를 못 읽었다")
	}
	if codexTrusted(cfg, "/other/hooks.json") {
		t.Error("다른 경로의 신뢰를 이 경로의 것으로 읽었다")
	}
	if codexTrusted("", hooks) {
		t.Error("빈 config 를 신뢰로 읽었다")
	}
	// ★ 신뢰가 **없는** 것이 이 축의 핵심 판정이다 — 그 상태에서 codex 는 훅을 조용히 건너뛴다.
	if codexTrusted(`[projects."/home/x"]`+"\ntrust_level = \"trusted\"\n", hooks) {
		t.Error("프로젝트 신뢰를 훅 신뢰로 읽었다 — 둘은 다른 관문이다")
	}
}

func TestCodexVersionedCommand(t *testing.T) {
	yes := []string{
		`"${CLAUDE_PLUGIN_ROOT}/bin/fd" hook stop`,
		`"/home/x/.claude/plugins/cache/m/flightdeck/0.31.0/bin/fd" hook stop`,
	}
	for _, c := range yes {
		if !codexVersionedCommand(c) {
			t.Errorf("버전 든 경로를 못 잡았다: %q", c)
		}
	}
	if codexVersionedCommand(`"/home/x/.local/bin/fd-hook" hook stop --harness codex`) {
		t.Error("고정 경로를 버전 든 것으로 잡았다")
	}
}

// TestCodexAxesGradesTheSilence 는 네 상태를 **가르는지** 문다.
//
// ★ 이 축의 값은 "신뢰 없음"을 **이름으로** 말하는 데 있다. 그 상태에서 codex 는 훅을
// 조용히 건너뛰고 로그에 한 줄도 안 남기므로, 이 줄이 유일한 관측 창구다.
func TestCodexAxesGradesTheSilence(t *testing.T) {
	const hooks = "/h/.codex/hooks.json"
	good := `{"hooks":{"Stop":[{"hooks":[{"command":"\"/h/.local/bin/fd-hook\" hook stop --harness codex"}]}]}}`
	trusted := `[hooks.state."` + hooks + `:Stop:0:0"]`

	find := func(axes []service.DoctorAxis, name string) service.DoctorAxis {
		for _, a := range axes {
			if a.Name == name {
				return a
			}
		}
		t.Fatalf("축 %q 가 없다", name)
		return service.DoctorAxis{}
	}

	t.Run("codex 가 없으면 재지 않는다", func(t *testing.T) {
		axes := (CodexAxes(CodexState{Present: false}))
		if len(axes) != 1 || axes[0].Observed {
			t.Fatalf("codex 부재 축이 이상하다: %+v", axes)
		}
	})

	t.Run("훅이 깔렸는데 신뢰가 없다", func(t *testing.T) {
		axes := (CodexAxes(CodexState{
			Present: true, HooksPath: hooks, HooksRaw: good, ConfigRaw: "",
		}))
		if a := find(axes, "codex 훅 파일"); !a.Observed {
			t.Error("훅 파일을 관측 못 했다")
		}
		a := find(axes, "codex 훅 신뢰")
		if a.Observed {
			t.Fatal("신뢰가 없는데 관측됐다고 했다 — 이러면 침묵이 화면에서 다시 침묵이 된다")
		}
		if !strings.Contains(a.Detail, "조용히") {
			t.Errorf("신뢰 없음의 처방이 침묵을 안 말한다: %q", a.Detail)
		}
	})

	t.Run("신뢰까지 있으면 초록", func(t *testing.T) {
		axes := (CodexAxes(CodexState{
			Present: true, HooksPath: hooks, HooksRaw: good, ConfigRaw: trusted,
			WrapperPath: "/h/.local/bin/fd-hook", WrapperOK: true,
		}))
		for _, n := range []string{"codex 훅 파일", "codex 훅 신뢰", "codex 훅 명령", "codex 훅 래퍼"} {
			if a := find(axes, n); !a.Observed {
				t.Errorf("%s 가 관측 안 됨이다: %q", n, a.Detail)
			}
		}
	})

	t.Run("버전 든 경로를 이름으로 잡는다", func(t *testing.T) {
		bad := `{"hooks":{"Stop":[{"hooks":[{"command":"\"${CLAUDE_PLUGIN_ROOT}/bin/fd\" hook stop --harness codex"}]}]}}`
		axes := (CodexAxes(CodexState{
			Present: true, HooksPath: hooks, HooksRaw: bad, ConfigRaw: trusted,
		}))
		a := find(axes, "codex 훅 명령")
		if a.Observed {
			t.Fatal("버전 든 경로인데 통과시켰다 — 다음 판올림에 조용히 죽는다")
		}
	})

	t.Run("네트워크가 끊겼으면 그 사실이 위를 무효로 만든다고 말한다", func(t *testing.T) {
		axes := (CodexAxes(CodexState{
			Present: true, HooksPath: hooks, HooksRaw: good, ConfigRaw: trusted,
			WrapperOK: true, NetDisabled: true,
		}))
		a := find(axes, "codex 네트워크")
		if !strings.Contains(a.Detail, "못 붙는다") {
			t.Errorf("네트워크 차단의 처방이 약하다: %q", a.Detail)
		}
	})
}

// ── 신뢰가 「항목 존재」로 초록이 되던 자리 ────────────────────────────────
//
// ★ 2026-09-02 사건: `0.34.0` 이 훅 예산을 2→5 로 올렸더니 codex 가 **예산이 바뀐 훅만**
// 조용히 건너뛰었다(SessionStart·PostToolUse 는 예산이 그대로라 계속 돌았다). 그런데
// `fd doctor` 는 `✓ 신뢰됨` 을, `fd setup` 은 `✓ 훅 신뢰는 이미 박혀 있다` 를 냈다 —
// **방금 자기가 깨뜨린 것을 그렇게 말했다.** 저장된 `trusted_hash` 는 승인 시점의 값이라
// 파일을 고쳐도 안 바뀌기 때문이다.
//
// 아래 셋은 그 화면들이 **한계를 말하는지**를 문다. 해시 재현으로 고치지 않는 이유는
// `c5e4aa5` 기각 ① 그대로다 — 규칙을 베끼면 codex 가 그것을 바꾸는 날 우리가 조용히 거짓이 된다.

// TestTrustAxisDoesNotClaimTheHooksAreLive 는 신뢰 축이 **못 재는 것을 못 잰다고** 하는지 문다.
func TestTrustAxisDoesNotClaimTheHooksAreLive(t *testing.T) {
	st := CodexState{
		Present: true, HooksPath: "/h/.codex/hooks.json",
		HooksRaw:  `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"\"/h/.local/bin/fd-hook\" hook session-start --harness codex","timeout":10}]}]}}`,
		ConfigRaw: `[hooks.state."/h/.codex/hooks.json:session_start:0:0"]` + "\ntrusted_hash = \"sha256:abc\"\n",
	}
	var trust service.DoctorAxis
	for _, ax := range CodexAxes(st) {
		if ax.Name == "codex 훅 신뢰" {
			trust = ax
		}
	}
	if trust.Name == "" {
		t.Fatal("신뢰 축이 없다")
	}
	if !trust.Observed {
		t.Fatalf("항목이 있는데 ✗ 로 났다: %q", trust.Detail)
	}
	// ★ 값이 "신뢰됨" 에서 끝나면 안 된다 — 그것이 훅이 도는 것으로 읽힌다.
	if !strings.Contains(trust.Value, "못 잰다") {
		t.Errorf("신뢰 축이 한계를 안 말한다 — 「항목이 있다」를 「훅이 돈다」로 읽게 된다:\n  %q", trust.Value)
	}
	if !strings.Contains(trust.Value, "재승인") {
		t.Errorf("파일을 고쳤을 때 무엇이 필요한지 안 말한다: %q", trust.Value)
	}
}

// TestPinAxisDoesNotPromiseNoReapprovalOnUpgrade 는 「판올림해도 재승인이 없다」가
// **경로 축에 한정된 주장**임을 화면이 말하는지 문다. 그 한정어가 빠지면 거짓이다.
func TestPinAxisDoesNotPromiseNoReapprovalOnUpgrade(t *testing.T) {
	st := CodexState{
		Present: true, HooksPath: "/h/.codex/hooks.json",
		HooksRaw: `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"\"/h/.local/bin/fd-hook\" hook stop --harness codex","timeout":5}]}]}}`,
	}
	var pin service.DoctorAxis
	for _, ax := range CodexAxes(st) {
		if ax.Name == "codex 훅 명령" {
			pin = ax
		}
	}
	if !pin.Observed {
		t.Fatalf("고정 경로인데 ✗ 로 났다: %q", pin.Detail)
	}
	if !strings.Contains(pin.Value, "경로 때문에는") {
		t.Errorf("한정어가 없다 — 「판올림해도 재승인이 없다」는 hooks.json 내용이 바뀌면 거짓이다:\n  %q", pin.Value)
	}
}

// TestNextStepsAskForReapprovalWhenItJustWroteHooks 는 setup 이 **방금 쓴 파일**에 대해
// 재승인을 요구하는지 문다. 이 갈래가 없으면 사용자는 훅이 죽은 줄 모른다.
func TestNextStepsAskForReapprovalWhenItJustWroteHooks(t *testing.T) {
	trusted := CodexState{
		Present: true, HooksPath: "/h/.codex/hooks.json",
		ConfigRaw: `[hooks.state."/h/.codex/hooks.json:stop:0:0"]` + "\ntrusted_hash = \"sha256:abc\"\n",
	}
	// ① 이번에 안 썼다 → 이미 박혀 있다고 해도 된다
	if got := RenderCodexNextSteps(trusted, false); !strings.Contains(got, "이미 박혀 있다") {
		t.Errorf("파일을 안 건드렸는데 재승인을 요구한다:\n%s", got)
	}
	// ② 이번에 썼다 → **신뢰 항목이 있어도** 재승인을 요구해야 한다
	got := RenderCodexNextSteps(trusted, true)
	if strings.Contains(got, "이미 박혀 있다") {
		t.Errorf("hooks.json 을 방금 썼는데 「이미 박혀 있다」고 한다 — 방금 자기가 깨뜨린 것이다:\n%s", got)
	}
	if !strings.Contains(got, "다시 신뢰해라") {
		t.Errorf("새로 쓴 뒤 재승인 안내가 없다:\n%s", got)
	}
	if !strings.Contains(got, "timeout") {
		t.Errorf("예산 한 칸으로도 깨진다는 사실을 안 말한다 — 그것이 이 사건의 원인이었다:\n%s", got)
	}
}
