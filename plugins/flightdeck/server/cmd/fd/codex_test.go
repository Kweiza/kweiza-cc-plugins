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

	// ★ **다섯 이벤트를 표로 문다.** glob 처럼 "있는 것만" 보면 이벤트가 사라진 날
	// 그 사실이 화면에서 침묵한다(repo_hooks_test.go 의 같은 규율).
	// SessionEnd 는 **일부러 없다** — codex 문서에는 있으나 발화를 못 봤고, 안 재고 실으면
	// "도는 줄 알았는데 안 도는" 침묵이 하나 더 생긴다. 재고 나서 넣어라.
	want := map[string]bool{
		"SessionStart": true, "UserPromptSubmit": true, "PostToolUse": true,
		"PreCompact": true, "Stop": true,
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
	if _, ok := doc.Hooks["SessionEnd"]; ok {
		t.Error("SessionEnd 가 실렸다 — codex 에서 이 이벤트의 발화를 아직 못 봤다. " +
			"재고 나서 넣어라(안 재고 실으면 안 도는 훅을 실은 것이다)")
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
