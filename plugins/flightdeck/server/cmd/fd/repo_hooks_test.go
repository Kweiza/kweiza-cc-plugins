package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 훅 관문을 **저장소 전체**로 넓힌다 — 옆 플러그인의 훅은 아무도 안 보고 있었다.
//
// `plugin_test.go` 의 `TestHooksJSONIsWiredAsDesigned` 는 촘촘하지만 그 `pluginRoot` 는
// `plugins/flightdeck` 이다. `plugins/grafik-bar/hooks/hooks.json` 은 그 시야 밖이고 그
// 플러그인 안에는 Go 코드가 없다(훅 하나와 셸 스크립트 둘뿐). 그래서 이것들이 전부 조용히
// 지나갔다 — 경로가 틀려도, 이벤트 이름에 오타가 나도, matcher 가 플랫폼이 안 쏘는 값이어도.
//
// ★ **훅이 죽는 모양은 오류가 아니라 부재다.** `plugin_test.go` 머리말이 그 이유를 적어 뒀다:
// "훅 경로가 틀리면 세션이 그냥 아무것도 안 한다." grafik-bar 는 **SessionStart 훅으로
// 스스로 설치되는** 플러그인이라(`plugin.json` 의 description 이 그렇게 선언한다), 그 경로가
// 틀리면 상태줄이 안 뜨는 것으로만 나타난다. 화면에 아무 오류도 안 뜬다.
//
// ★ **잠글 것의 근거는 어디서 오나.** flightdeck 의 관문은 matcher·async·timeout 을
// DESIGN 에서 끌어와 잠근다. grafik-bar 에는 DESIGN 이 없다 — 그래서 이 파일이 무는 것은
// 근거가 **저장소 밖 둘 중 하나**에서 오는 축뿐이다:
//
//	① 플랫폼 실측 — 설치본 2.1.240 바이너리를 뜯어 얻은 이벤트 이름 31종과 matcher 열거값
//	② 플러그인 자신의 선언 — `plugin.json` 의 description 이 약속한 것
//
// 그 둘 중 어느 것도 안 대는 축(예: "timeout 은 정확히 N 초여야 한다")은 여기 없다.
// 값이 아니라 **존재**를 무는 이유가 그것이다(`hookTimeoutIsRequired` 주석 참고).

// repoHookEvents 는 **플러그인별 훅 이벤트 집합**이다. 표와 디스크를 양방향으로 문다.
//
// ★ 표가 필요한 이유는 `repoSkillCounts` 와 같다: 훑기가 glob 뿐이면 "아무것도 못 찾았다"와
// "전부 봤다"가 화면에서 같다. 플러그인이 하나 더 생기거나 훅 파일이 사라지면 이 표가 먼저
// 빨개져서, **검사받지 않은 채 들어오는 길**이 없다.
//
// nil 도 적는다. `session-handoff` 은 훅이 없는 플러그인이고, "훅이 없다"는 사실 자체가
// 표에 있어야 훅이 생긴 날 이 관문이 답한다.
var repoHookEvents = map[string][]string{
	"flightdeck": {
		"PostToolUse", "PreCompact", "SessionEnd", "SessionStart", "Stop", "UserPromptSubmit",
	},
	"grafik-bar":      {"SessionStart"},
	"session-handoff": nil,
}

// platformHookEvents 는 설치본이 **아는 훅 이벤트 이름 전부**다(2.1.240 실측, 31종).
//
// 뽑은 자리: 번들의 zod 스키마 `hook_event_name:Ht("<이름>")` 전수.
// DESIGN §6 이 "훅 이벤트 31종에도 프로세스 종료를 알리는 것이 없다"고 적은 그 31 과 같은 수다.
//
// ★ 이름에 오타가 나면 **아무 일도 안 일어난다** — 설정은 그대로 실리고 그 훅만 영원히 안 돈다.
// 그것이 이 표가 무는 것이다. 플랫폼이 이벤트를 늘리면 여기가 낡는데, 그때 할 일은 표를
// 지우는 것이 아니라 **새 이름을 재고 적는 것**이다.
var platformHookEvents = map[string]bool{
	"ConfigChange": true, "CwdChanged": true, "DirectoryAdded": true, "Elicitation": true,
	"ElicitationResult": true, "FileChanged": true, "InstructionsLoaded": true,
	"MessageDisplay": true, "Notification": true, "PermissionDenied": true,
	"PermissionRequest": true, "PostCompact": true, "PostToolBatch": true, "PostToolUse": true,
	"PostToolUseFailure": true, "PreCompact": true, "PreToolUse": true, "SessionEnd": true,
	"SessionStart": true, "Setup": true, "Stop": true, "StopFailure": true,
	"SubagentStart": true, "SubagentStop": true, "TaskCompleted": true, "TaskCreated": true,
	"TeammateIdle": true, "UserPromptExpansion": true, "UserPromptSubmit": true,
	"WorktreeCreate": true, "WorktreeRemove": true,
}

// matcherSpec 은 matcher 가 **닫힌 열거값**인 이벤트 하나의 계약이다(2.1.240 실측).
//
// 뽑은 자리: 이벤트 메타의 `matcherMetadata:{fieldToMatch:"<필드>",values:[…]}`.
// zod 스키마 쪽 `source:Dr([…])` 와 교차로 맞췄다 — 두 자리가 같은 다섯을 낸다.
type matcherSpec struct {
	values []string
	// complete 는 이 레포의 훅이 그 열거값을 **전부** 받아야 하는가다.
	// 부분집합 검사만으로는 **빠진 값**을 못 잡는다 — 오타는 잡히는데 누락은 조용하다.
	complete bool
	// why 는 complete 가 그 값인 근거다. 실패 메시지에 실린다 — 다음 사람이
	// 이 단정을 만났을 때 고칠지 말지를 근거로 정하도록.
	why string
}

// platformMatcherValues 는 matcher 를 잠그는 이벤트들이다.
//
// ★ **여기 없는 이벤트의 matcher 는 안 문다.** `PostToolUse` 의 matcher 는 도구 **이름**이라
// 열거가 아니고(`values:[]` 로 비어 온다), `UserPromptSubmit`·`Stop` 은 matcher 자체가 없다.
// 모르는 것을 아는 척 잠그면 그 관문이 다음 사람을 틀린 데로 보낸다.
//
// ★ `SessionStart` 에 **`fork` 가 있다**. DESIGN §6 의 표는 2.1.221·2.1.222 실측 기준이고
// 그때는 이 값이 없었다 — 플랫폼이 움직인 자리다. 세션 전환 사유 여덟
// (`clear`·`resume`·`fork`·`remote_attach`·`cd`·`spare_claim`·`hydrate`·`startup_custom_id`)
// 중 훅으로 오는 것이 이 다섯이고, `/fork` 는 `/clear` 와 같은 계열의 **같은 창 안 전환**이다.
var platformMatcherValues = map[string]matcherSpec{
	"SessionStart": {
		values:   []string{"clear", "compact", "fork", "resume", "startup"},
		complete: true,
		why: "이 레포의 SessionStart 훅 둘은 **갈래를 안 가린다** — flightdeck 은 페이로드의 " +
			"`source` 를 한 번도 안 읽고(cmd/fd/hook.go 의 HookPayload 는 그 필드를 파싱만 한다), " +
			"grafik-bar 의 setup 은 멱등이다. 그래서 빠진 값은 의도가 아니라 표류이고, " +
			"그 갈래로 시작한 세션은 카드가 없거나(보드의 거짓) 상태줄이 없다",
	},
	"SessionEnd": {
		values: []string{"clear", "logout", "other", "prompt_input_exit", "resume"},
		// ★ 전부가 **아니다**. flightdeck 은 `clear` 하나만 받아야 하고 그것이 DESIGN §6 의
		// 핵심 단정이다(넷은 아무도 안 쏘고, `resume` 은 `/fork` 와 사유를 공유한다).
		// 그 폭은 plugin_test.go 의 TestHooksJSONIsWiredAsDesigned 가 따로 붙들고 있다.
		complete: false,
		why:      "SessionEnd 는 좁아야 한다 — 폭을 넓히는 것을 DESIGN §6 이 금지한다",
	},
	"PreCompact": {
		values:   []string{"auto", "manual"},
		complete: false,
		why:      "PreCompact 는 matcher 없이(= 전부) 걸려 있어 완전성을 따로 안 문다",
	},
}

// hookRef 는 훑어 낸 훅 파일 하나다.
type hookRef struct {
	plugin string
	path   string
	root   string // 그 플러그인의 루트(= ${CLAUDE_PLUGIN_ROOT} 가 가리키는 자리)
}

// hookRefs 는 `plugins/*/hooks/hooks.json` 을 전수로 낸다.
//
// 표본을 자르지 않는다 — 이 레포에는 `head` 파이프가 목록을 말없이 잘라 "전수"가 거짓이 된
// 선례가 있다(`94fc82e`). 하나도 못 찾으면 초록으로 지나가지 않고 그 자리에서 죽는다.
func hookRefs(t *testing.T, root string) []hookRef {
	t.Helper()
	pents, err := os.ReadDir(filepath.Join(root, "plugins"))
	if err != nil {
		t.Fatalf("plugins/ 를 못 읽었다: %v", err)
	}
	var out []hookRef
	for _, p := range pents {
		if !p.IsDir() {
			continue
		}
		proot := filepath.Join(root, "plugins", p.Name())
		hp := filepath.Join(proot, "hooks", "hooks.json")
		if _, err := os.Stat(hp); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("%s 의 hooks.json 을 stat 못 했다: %v", p.Name(), err)
		}
		out = append(out, hookRef{plugin: p.Name(), path: hp, root: proot})
	}
	if len(out) == 0 {
		t.Fatalf("hooks.json 을 하나도 못 찾았다(레포 루트 %s) — 훑기가 눈이 먼 것이지 통과가 아니다", root)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].plugin < out[j].plugin })
	return out
}

// readHooksFile 은 hooks.json 을 **모르는 키를 거절하며** 읽는다.
//
// ★ `DisallowUnknownFields` 는 일부러다. 훅 항목 스키마에는 이 구조체가 안 든 필드가 더 있다
// (2.1.240 실측: `args`·`if`·`shell`·`statusMessage`·`once`·`asyncRewake`·`rewakeMessage`·
// `rewakeSummary`). 그것들을 쓰기 시작하는 날 이 관문이 먼저 빨개지는 것이 옳다 —
// **검사받지 않은 축이 조용히 들어오는 길**을 막는 것이 이 파일의 일이기 때문이다.
// 그날 할 일은 이 단정을 지우는 것이 아니라 `hooksFile`(plugin_test.go)에 그 필드를 더하고
// 그것이 무엇을 보장해야 하는지 여기 적는 것이다.
func readHooksFile(t *testing.T, h hookRef) hooksFile {
	t.Helper()
	raw, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatalf("%s 의 hooks.json 을 못 읽었다: %v", h.plugin, err)
	}
	var hf hooksFile
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&hf); err != nil {
		t.Fatalf("%s 의 hooks.json 이 유효한 JSON 이 아니거나 모르는 키가 있다: %v\n"+
			"깨진 hooks.json 은 그 플러그인의 훅을 통째로 지운다 — 화면에 오류가 안 뜨는 부류다", h.plugin, err)
	}
	if len(hf.Hooks) == 0 {
		t.Fatalf("%s 의 hooks.json 에 훅이 하나도 없다 — 파일만 있고 배선이 없는 것은 통과가 아니다", h.plugin)
	}
	return hf
}

// pluginRootRefRe 는 command 에서 `${CLAUDE_PLUGIN_ROOT}` 뒤의 경로를 뽑는다.
var pluginRootRefRe = regexp.MustCompile(`\$\{CLAUDE_PLUGIN_ROOT\}(/[^"'\s]+)`)

// TestEveryHooksJSONInTheRepoIsInScope 는 표와 디스크를 양방향으로 문다.
func TestEveryHooksJSONInTheRepoIsInScope(t *testing.T) {
	root := repoRootFromCmdFd(t)

	pents, err := os.ReadDir(filepath.Join(root, "plugins"))
	if err != nil {
		t.Fatalf("plugins/ 를 못 읽었다: %v", err)
	}
	onDisk := map[string]bool{}
	for _, p := range pents {
		if p.IsDir() {
			onDisk[p.Name()] = true
		}
	}
	for name := range onDisk {
		if _, ok := repoHookEvents[name]; !ok {
			t.Fatalf("플러그인 %s 가 훅 표에 없다 — 표에 없는 플러그인의 훅은\n"+
				"이 파일의 어느 관문도 보지 않는다. 이름을 적기 전에 그 훅들을 먼저 읽어라", name)
		}
	}
	for name := range repoHookEvents {
		if !onDisk[name] {
			t.Fatalf("표의 %s 가 plugins/ 에 없다 — 죽은 이름이 표에 남으면\n"+
				"그 표를 근거로 센 것이 전부 틀린다", name)
		}
	}

	// 훅 파일이 **있는** 플러그인의 이벤트 집합이 표와 같아야 한다.
	got := map[string][]string{}
	for _, h := range hookRefs(t, root) {
		hf := readHooksFile(t, h)
		evs := keysOf(hf.Hooks)
		sort.Strings(evs)
		got[h.plugin] = evs
	}
	for name, want := range repoHookEvents {
		have, ok := got[name]
		if len(want) == 0 {
			if ok {
				t.Fatalf("표는 %s 에 훅이 없다는데 hooks.json 이 %v 를 싣고 있다 —\n"+
					"훅이 생겼으면 그것이 이 파일의 관문들을 지나는지부터 보고 표를 고쳐라", name, have)
			}
			continue
		}
		if !ok {
			t.Fatalf("표는 %s 가 훅 %v 를 갖는다는데 hooks.json 이 없다", name, want)
		}
		if strings.Join(have, ",") != strings.Join(want, ",") {
			t.Fatalf("플러그인 %s 의 훅 이벤트가 %v 인데 표는 %v 라 한다 —\n"+
				"수를 고치기 전에 늘어난 쪽이 이 파일의 관문들을 지나는지부터 봐라", name, have, want)
		}
	}

	// 이벤트 이름이 **플랫폼이 아는 것**이어야 한다. 오타는 조용히 안 걸린다.
	for _, h := range hookRefs(t, root) {
		hf := readHooksFile(t, h)
		for ev := range hf.Hooks {
			if !platformHookEvents[ev] {
				t.Fatalf("%s 가 플랫폼이 모르는 훅 이벤트 %q 를 쓴다 —\n"+
					"모르는 이름은 오류를 안 내고 그 훅만 영원히 안 돈다(2.1.240 실측 31종)", h.plugin, ev)
			}
		}
	}
}

// TestEveryHookCommandPointsAtSomethingReal 은 훅이 **실재하는 것**을 부르는지 본다.
//
// ★ 이 항목의 뿌리다. 경로가 틀리면 세션이 그냥 아무것도 안 하고, grafik-bar 의 경우
// 상태줄이 안 뜨는 것으로만 나타난다 — 오류가 아니라 부재로.
func TestEveryHookCommandPointsAtSomethingReal(t *testing.T) {
	root := repoRootFromCmdFd(t)
	canLint := canLintShell(t)
	seen := 0
	for _, h := range hookRefs(t, root) {
		hf := readHooksFile(t, h)
		for ev, groups := range hf.Hooks {
			for gi, g := range groups {
				if len(g.Hooks) == 0 {
					t.Fatalf("%s 의 %s 그룹 %d 에 훅이 하나도 없다", h.plugin, ev, gi)
				}
				for hi, hk := range g.Hooks {
					where := h.plugin + "/" + ev
					// 훅 타입은 셋뿐이다(2.1.240 실측: command·prompt·mcp_tool).
					// 이 레포는 command 만 쓴다 — 다른 것을 쓰는 날 그 계약을 여기 적어라.
					if hk.Type != "command" {
						t.Fatalf("%s 의 훅 %d 의 type 이 %q 다 — 이 레포는 command 만 쓴다", where, hi, hk.Type)
					}
					// ★ 절대경로여야 한다. 훅 실행 환경이 Bash 도구와 같다는 보장이 없다(DESIGN §13).
					m := pluginRootRefRe.FindStringSubmatch(hk.Command)
					if m == nil {
						t.Fatalf("%s 의 명령이 ${CLAUDE_PLUGIN_ROOT} 절대경로를 안 쓴다: %q\n"+
							"플러그인 경로에는 **버전이 들어간다** — 갱신되면 하드코딩한 경로가 조용히 죽는다",
							where, hk.Command)
					}
					target := filepath.Join(h.root, filepath.FromSlash(strings.TrimPrefix(m[1], "/")))
					st, err := os.Stat(target)
					if err != nil {
						t.Fatalf("%s 의 명령이 가리키는 %s 가 없다: %v\n"+
							"이름이 밀리면 훅은 오류가 아니라 **부재**로 죽는다 — 아무도 못 본다", where, m[1], err)
					}
					if st.IsDir() {
						t.Fatalf("%s 의 명령이 디렉토리 %s 를 가리킨다", where, m[1])
					}
					seen++
					if canLint && (strings.HasSuffix(target, ".sh") || isShellScript(t, target)) {
						lintShell(t, target, where+" 가 부르는 "+m[1])
					}
				}
			}
		}
	}
	if seen == 0 {
		t.Fatalf("훅 명령을 하나도 안 봤다 — 훑기가 눈이 먼 것이지 통과가 아니다")
	}
}

// canLintShell 은 이 머신에서 셸 문법을 잴 수 있는지 본다.
//
// ★ 못 재면 **밝히며** 건너뛴다. 조용히 공허해지는 것이 이 레포가 두 번 밟은 자리다
// (plugin_test.go 의 로케일 프로브와 같은 규율) — 도구가 없는 머신에서 관문이 아무것도
// 안 물면서 초록인 것은 통과가 아니다.
func canLintShell(t *testing.T) bool {
	t.Helper()
	if err := exec.Command("/bin/bash", "-c", "exit 0").Run(); err != nil {
		t.Logf("이 머신에서 /bin/bash 를 못 돌린다(%v) — 셸 문법 축(bash -n)만 건너뛴다. 나머지는 그대로 잰다", err)
		return false
	}
	return true
}

// lintShell 은 셸 스크립트가 파싱되는지 본다.
//
// ★ 셸 문법 오류는 컴파일러가 안 본다 — 그 스크립트가 도는 그 순간에만 드러나고,
// 훅과 상태줄은 둘 다 **오류가 아니라 부재**로 죽는 자리다.
func lintShell(t *testing.T, path, where string) {
	t.Helper()
	if out, err := exec.Command("/bin/bash", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("%s 가 bash 문법을 안 지킨다: %v\n%s", where, err, out)
	}
}

// isShellScript 는 첫 줄의 shebang 으로 셸 스크립트인지 본다(확장자가 없는 런처용).
func isShellScript(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 64)
	n, _ := f.Read(buf)
	head := string(buf[:n])
	return strings.HasPrefix(head, "#!") && (strings.Contains(head, "bash") || strings.Contains(head, "/sh"))
}

// TestEveryHookMatcherIsAValueThePlatformSends 는 matcher 가 **오는 값**인지 본다.
//
// ★ 안 오는 값을 적으면 "잡고 있다"는 착각만 생기고, 빠뜨린 값은 그 갈래에서 훅이 통째로
// 안 도는 것으로 나타난다. 둘 다 조용하다.
func TestEveryHookMatcherIsAValueThePlatformSends(t *testing.T) {
	root := repoRootFromCmdFd(t)
	checked := 0
	for _, h := range hookRefs(t, root) {
		hf := readHooksFile(t, h)
		for ev, groups := range hf.Hooks {
			spec, ok := platformMatcherValues[ev]
			if !ok {
				continue // 열거가 아닌 matcher 는 안 문다(위 표 주석 참고)
			}
			set := map[string]bool{}
			for _, v := range spec.values {
				set[v] = true
			}
			got := map[string]bool{}
			wideOpen := false
			for _, g := range groups {
				if g.Matcher == "" {
					wideOpen = true // 빈 matcher 는 "전부"다
					continue
				}
				for _, part := range strings.Split(g.Matcher, "|") {
					part = strings.TrimSpace(part)
					if !set[part] {
						t.Fatalf("%s 의 %s matcher 가 플랫폼이 안 쏘는 값 %q 를 쓴다 — 아는 값은 %v 다\n"+
							"안 오는 값은 오류를 안 낸다: 그 갈래가 조용히 영원히 안 돌 뿐이다",
							h.plugin, ev, part, spec.values)
					}
					got[part] = true
					checked++
				}
			}
			// ★★ 누락을 문다. 오타는 위에서 잡히지만 **빠진 값은 조용하다** —
			// 그 갈래에서 훅이 통째로 안 도는 것으로만 나타난다.
			if !spec.complete || wideOpen {
				continue
			}
			var missing []string
			for _, v := range spec.values {
				if !got[v] {
					missing = append(missing, v)
				}
			}
			if len(missing) > 0 {
				t.Fatalf("%s 의 %s matcher 가 %v 를 빠뜨렸다 — 플랫폼이 쏘는 값은 %v 다.\n"+
					"%s.\n"+
					"일부러 좁히는 것이면 이 표의 complete 를 끄고 그 근거를 적어라 — 지금은 근거가 반대다",
					h.plugin, ev, missing, spec.values, spec.why)
			}
		}
	}
	if checked == 0 {
		t.Fatalf("matcher 를 하나도 안 봤다 — 훑기가 눈이 먼 것이지 통과가 아니다")
	}
}

// TestEveryHookHasATimeout 은 훅마다 **예산이 적혀 있는지** 본다.
//
// ★★ **값이 아니라 존재를 문다.** 이 레포는 flightdeck 의 훅 예산(2s·3s·10s)을 DESIGN 에서
// 끌어오지만, grafik-bar 에는 DESIGN 이 없다 — 그래서 "정확히 N 초"를 여기서 잠그지 않는다.
// 근거가 안 따라오는 수를 관문에 들이면 그것은 관문이 아니라 장식이다(`skillLineCaps` 를
// 저장소 밖으로 안 가져온 것과 같은 규율, `repo_skills_test.go` 머리말).
//
// ★ **그런데 존재는 근거가 따라온다.** `timeout` 은 플랫폼 스키마에서 optional 이고
// (2.1.240 실측: `timeout:Xe().positive().optional()`), 안 적으면 **플랫폼 기본값**이 쓰인다.
// 그 기본값이 몇 초인지는 **이 레포가 모른다** — 2.1.240 바이너리에서 command 훅의 기본
// 예산을 못 떴다(Bun 런타임 코드에 묻혀 있다). 모르는 예산에 세션 시작을 맡기지 않는다.
// SessionStart 훅은 **사람이 첫 글자를 치기 전에** 끝나야 하고, 이 레포의 두 플러그인은
// 그 자리에서 **함께** 돈다 — 합이 곧 세션이 안 뜨는 시간이다.
func TestEveryHookHasATimeout(t *testing.T) {
	root := repoRootFromCmdFd(t)
	seen := 0
	for _, h := range hookRefs(t, root) {
		hf := readHooksFile(t, h)
		for ev, groups := range hf.Hooks {
			for _, g := range groups {
				for _, hk := range g.Hooks {
					if hk.Timeout <= 0 {
						t.Fatalf("%s 의 %s 에 타임아웃이 없다 — 훅이 안 끊기면 세션이 멈춘다.\n"+
							"플랫폼 기본값이 몇 초인지 이 레포는 재지 못했다(2.1.240 에서 못 떴다) — "+
							"모르는 예산에 세션 시작을 맡기지 마라", h.plugin, ev)
					}
					seen++
				}
			}
		}
	}
	if seen == 0 {
		t.Fatalf("훅을 하나도 안 봤다 — 훑기가 눈이 먼 것이지 통과가 아니다")
	}
}

// grafikStatusLineHopRe 는 setup 스크립트가 가리키는 **상태줄 스크립트**를 뽑는다.
var grafikStatusLineHopRe = regexp.MustCompile(`script_path="\$root/([^"]+)"`)

// TestGrafikBarSetupReachesAWorkingStatusLine 은 **두 번째 홉**을 문다.
//
// ★★ 훅 JSON 만 보면 이 축이 안 보인다. hooks.json → `setup-statusline.sh` 까지는 위
// 관문이 잠그는데, 그 스크립트가 다시 가리키는 `scripts/statusline.sh` 는 **JSON 밖**이라
// 아무도 안 본다. 그 이름이 밀리면 훅은 정상 종료(0)하고 상태줄만 안 뜬다 —
// 이 플러그인이 죽는 실제 경로이고, `plugin.json` 의 description 이 "auto-installs via a
// SessionStart hook" 이라 약속한 것이 바로 여기서 끊긴다.
//
// ★ 이 시험은 grafik-bar 하나를 이름으로 겨눈다. 일반화하지 않은 이유는 **두 번째 홉의
// 모양이 스크립트마다 다르기 때문**이다 — 일반 규칙인 척하는 정규식 하나는 다음 플러그인에서
// 조용히 아무것도 안 잡는다. 셋째 플러그인이 같은 모양을 쓰면 그때 함께 묶어라.
func TestGrafikBarSetupReachesAWorkingStatusLine(t *testing.T) {
	root := repoRootFromCmdFd(t)
	proot := filepath.Join(root, "plugins", "grafik-bar")
	setup := filepath.Join(proot, "scripts", "setup-statusline.sh")
	raw, err := os.ReadFile(setup)
	if err != nil {
		t.Fatalf("setup-statusline.sh 를 못 읽었다: %v", err)
	}
	m := grafikStatusLineHopRe.FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("setup-statusline.sh 에서 `script_path=\"$root/…\"` 를 못 찾았다 —\n" +
			"이 시험이 무는 자리가 밀렸다. 스크립트가 상태줄을 어디로 가리키는지 다시 읽고 여기를 고쳐라")
	}
	target := filepath.Join(proot, filepath.FromSlash(m[1]))
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("setup-statusline.sh 가 가리키는 %s 가 없다: %v\n"+
			"훅은 그대로 0 으로 끝나고 상태줄만 안 뜬다 — 화면에 오류가 없는 부류다", m[1], err)
	}
	// ★★ **실재만으로는 부족하다.** 이 파일은 훅이 직접 부르는 것이 아니라 두 번째 홉이라,
	// 위 TestEveryHookCommandPointsAtSomethingReal 의 문법 축이 여기까지 안 닿는다 —
	// 변이로 확인했다(statusline.sh 에 안 닫은 `if` 를 넣으면 그 시험은 초록이었다).
	// 상태줄은 셸이 파싱에 실패해도 화면에 오류를 안 낸다: 그냥 줄이 사라진다.
	if canLintShell(t) {
		lintShell(t, target, "상태줄 스크립트 "+m[1])
	}
}

// platformFileWritingTools 는 플랫폼이 **파일을 편집하는 도구**로 치는 것 전부다(2.1.240 실측).
//
// 뽑은 자리 셋이 서로 맞물린다:
//
//	① 판별 함수 자체 — `S3S=["Edit","Write","NotebookEdit"]` 과 `function Usl(e){return S3S.includes(e)}`.
//	   그 옆이 권한 판정에서 `getPath` 로 편집 대상 경로를 뽑는 코드다.
//	② 지표 설명 — `claude_code.code_edit_tool.decision` 이 "for Edit, Write, and NotebookEdit tools".
//	③ 내장 도구 목록 — `BUILTIN_TOOL_NAMES` 22종. 그 중 파일을 쓰는 것은 위 셋뿐이다.
//
// ★ `MultiEdit` 은 **여기 없다.** 도구 이름 상수 정의가 0건이고 `BUILTIN_TOOL_NAMES` 에도 없다 —
// 유일한 등장이 권한 규칙 문자열을 `Edit` 로 접는 비교 하나다(레거시 호환 문자열이다).
// 넣으면 이 관문이 빨개지는 것이 옳다: 없는 도구를 잡는 척하는 matcher 는 잡는 것도 없이
// 다음 사람에게 "덮여 있다"고 말한다.
//
// ★★ **이 표는 표류하는 값이다.** 플랫폼이 파일 쓰는 도구를 늘리는 날 여기가 낡는데, 그때
// 할 일은 표를 지우는 것이 아니라 **새 이름을 재고 적는 것**이다. 이 관문이 태어난 경로가
// 정확히 그것이다 — `NotebookEdit` 이 생겼는데 matcher 가 안 따라왔고, 아무도 그것을 못 봤다.
var platformFileWritingTools = []string{"Edit", "NotebookEdit", "Write"}

// TestPostToolUseCatchesEveryFileWritingTool 은 **미커밋 발자국의 유일한 원천**에 구멍이 없는지 본다.
//
// ★★ `PostToolUse` 의 matcher 는 도구 **이름**이라 닫힌 열거가 아니다(실측에서 `values:[]` 로
// 비어 온다) — 그래서 위 `platformMatcherValues` 는 이 이벤트를 일부러 안 문다. 대신
// **저장소가 표를 든다.** 여기서 무는 것은 "플랫폼이 쏘는 값인가"가 아니라
// **"이 훅이 하는 일에 필요한 값 전부인가"** 다. 근거가 다르므로 관문도 따로 산다.
//
// ★ 양방향이다. 빠지면 그 도구로 고친 파일이 화면에서 통째로 사라지고(설계 §6: 착수 직후
// 구간은 브랜치 diff 가 정의상 비어 있어 이 훅 말고는 원천이 없다), 더하면 발자국을 안 남기는
// 도구에 훅이 돌면서 훅 호출만 늘고 "잡고 있다"는 착각이 생긴다.
func TestPostToolUseCatchesEveryFileWritingTool(t *testing.T) {
	root := repoRootFromCmdFd(t)
	want := map[string]bool{}
	for _, tool := range platformFileWritingTools {
		want[tool] = true
	}
	seen := 0
	for _, h := range hookRefs(t, root) {
		hf := readHooksFile(t, h)
		groups, ok := hf.Hooks["PostToolUse"]
		if !ok {
			continue
		}
		got := map[string]bool{}
		for _, g := range groups {
			// ★ 빈 matcher 는 "전 도구"다. 그것은 이 계약이 아니다 — Read·Grep·Bash 까지
			// 매 호출마다 훅 프로세스가 뜨는데, 그 예산은 이 훅이 **편집마다** 도는 것만으로
			// 이미 횟수 쪽에서 걸리는 자리다(hook.go 의 hookBudget 주석). 표류에 강하다는
			// 이유로 비우려면 그 예산을 먼저 재고 설계 문서에 적어라 — 지금 근거는 반대다.
			if strings.TrimSpace(g.Matcher) == "" {
				t.Fatalf("%s 의 PostToolUse matcher 가 비었다 — 그것은 **전 도구**다.\n"+
					"이 훅은 파일을 쓰는 도구 %v 만 받아야 한다: 나머지는 발자국을 안 남기는데\n"+
					"훅 프로세스만 매 호출 뜬다", h.plugin, platformFileWritingTools)
			}
			for _, part := range strings.Split(g.Matcher, "|") {
				part = strings.TrimSpace(part)
				if !want[part] {
					t.Fatalf("%s 의 PostToolUse matcher 가 %q 를 쓴다 — 파일을 쓰는 도구는 %v 다.\n"+
						"파일을 안 쓰는 도구를 받으면 경로 없는 발자국이 쌓이고, **없는 도구**를 적으면\n"+
						"(MultiEdit 이 그렇다) 잡는 것도 없이 덮여 있다고 말한다",
						h.plugin, part, platformFileWritingTools)
				}
				got[part] = true
				seen++
			}
		}
		var missing []string
		for _, tool := range platformFileWritingTools {
			if !got[tool] {
				missing = append(missing, tool)
			}
		}
		if len(missing) > 0 {
			t.Fatalf("%s 의 PostToolUse matcher 가 %v 를 빠뜨렸다 — 파일을 쓰는 도구는 %v 다.\n"+
				"빠진 도구로 고친 파일은 **발자국이 안 남는다**: 이 훅이 미커밋 발자국의 유일한\n"+
				"원천이라(설계 §6) 그 세션의 겹침 판정과 발자국이 통째로 조용해진다.\n"+
				"경로 추출(cmd/fd/hook.go 의 EditedPaths)은 이미 그 도구들의 키를 전부 본다 —\n"+
				"여기만 안 따라오면 그 코드가 **불리지 않아** 죽는다", h.plugin, missing, platformFileWritingTools)
		}
	}
	if seen == 0 {
		t.Fatalf("PostToolUse matcher 를 하나도 안 봤다 — 훑기가 눈이 먼 것이지 통과가 아니다")
	}
}

// grafikStatusLinePayload 는 상태줄 stdin 의 **실물 모양**이다.
//
// 이 저장소가 파싱하는 것이 아니라 `statusline.sh` 가 jq 로 읽는 필드들이라, 여기 적힌 것은
// 계약이 아니라 **표본**이다. 필드가 하나 사라지면 그 조각이 빠질 뿐 줄은 떠야 한다 —
// 아래 시험이 무는 것이 그 성질이다(빈 stdin·깨진 JSON 도 같은 축이다).
const grafikStatusLinePayload = `{"model":{"display_name":"Opus 5"},` +
	`"effort":{"level":"high"},"context_window":{"used_percentage":42.5},` +
	`"rate_limits":{"five_hour":{"used_percentage":10,"resets_at":9999999999},` +
	`"seven_day":{"used_percentage":20,"resets_at":9999999999}},` +
	`"workspace":{"project_dir":"/tmp/proj","current_dir":"/tmp/proj"},` +
	`"cost":{"total_cost_usd":1.234,"total_lines_added":10,"total_lines_removed":3,` +
	`"total_duration_ms":65000}}`

// statusLineRun 은 상태줄을 **격리해서 실제로 돌린 결과**다.
type statusLineRun struct {
	code       int
	stdout     string
	stderr     string
	curlCalled bool
}

// runStatusLine 은 `statusline.sh` 를 봉인된 환경에서 돌린다.
//
// ★★ **봉인이 이 시험의 절반이다.** 이 스크립트는 켜 두면 밖으로 나간다 —
// `claude auth status` 로 인증을 조회하고, `curl` 로 api.anthropic.com 을 친다.
// 시험이 그것을 타면 느려지는 정도가 아니라 **머신마다 다른 답**을 내고, 그런 관문은
// 빨간 날 원인이 코드인지 네트워크인지 못 가른다. 그래서:
//
//	· HOME  — 빈 디렉토리. ~/.claude/settings.json·.credentials.json 이 없는 상태다
//	· TMPDIR — 갓 만든 사용량 캐시를 심는다. 나이가 TTL 안이라 스크립트가 fetch 를 건너뛴다
//	· PATH  — 스텁이 앞에 선다. claude 는 실패하고, curl 은 **불린 사실을 파일로 남긴다**
//	· tput·stty — 둘 다 실패시킨다. 그래야 폭 폴백 경로가 시험 머신의 tty 유무와 무관해진다
//
// curl 이 불렸는지를 시험이 직접 재는 이유는 그것이 **조용한 회귀**이기 때문이다:
// 캐시 로직이 무너져도 화면은 똑같고 상태줄만 매 렌더 네트워크를 친다.
// statusLineOpts 는 봉인을 부분적으로 여는 손잡이다.
type statusLineOpts struct {
	// stubs 는 PATH 앞에 세울 추가 스텁이다(이름 → 스크립트 본문).
	stubs map[string]string
	// env 는 봉인된 환경에 더할 변수다.
	//
	// ★ 폭 분기를 태우는 **유일한** 손잡이가 `COLUMNS` 다. `/dev/tty` 는 자식 프로세스에
	// 넘길 수 없어서 `tput cols </dev/tty` 는 리다이렉션 단계에서 실패한다 — 스텁 tput 은
	// 불리지도 않는다. 그래서 폭을 바꾸려면 스크립트가 환경을 보게 하는 수밖에 없다.
	env map[string]string
}

func runStatusLine(t *testing.T, stdin string, o statusLineOpts) statusLineRun {
	t.Helper()
	root := repoRootFromCmdFd(t)
	script := filepath.Join(root, "plugins", "grafik-bar", "scripts", "statusline.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("statusline.sh 가 없다: %v", err)
	}

	home := t.TempDir()
	tmp := t.TempDir()
	bin := t.TempDir()
	marker := filepath.Join(bin, "curl-was-called")

	// ★★ 가짜 자격증명을 심는다. 없으면 `fetch_usage` 가 토큰을 못 읽고 **curl 앞에서**
	// 되돌아가, 아래 `curlCalled` 단정이 영영 안 닿는다 — 변이 M11 이 그 사각을 드러냈다.
	// (캐시 키를 밀어도 초록이었다: 봉인이 너무 강해 그 축이 도달 불가였다.)
	// 밖으로 나갈 위험은 없다 — `curl` 은 PATH 앞의 스텁이고 그 스텁은 호출 사실만 남긴다.
	credDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(credDir, 0o755); err != nil {
		t.Fatalf("자격증명 디렉토리를 못 만들었다: %v", err)
	}
	cred := `{"claudeAiOauth":{"accessToken":"not-a-real-token"}}`
	if err := os.WriteFile(filepath.Join(credDir, ".credentials.json"), []byte(cred), 0o600); err != nil {
		t.Fatalf("가짜 자격증명을 못 심었다: %v", err)
	}

	// 사용량 캐시를 **신선하게** 심는다 — 이것이 없으면 스크립트가 curl 을 부른다.
	cache := filepath.Join(tmp, fmt.Sprintf("grafik-bar-usage-%d.json", os.Getuid()))
	if err := os.WriteFile(cache, []byte(`{"limits":[]}`), 0o600); err != nil {
		t.Fatalf("사용량 캐시를 못 심었다: %v", err)
	}

	stubs := map[string]string{
		"claude": "#!/bin/sh\nexit 1\n",
		"curl":   "#!/bin/sh\necho called >> " + marker + "\nexit 1\n",
		"tput":   "#!/bin/sh\nexit 1\n",
		"stty":   "#!/bin/sh\nexit 1\n",
	}
	for name, body := range o.stubs {
		stubs[name] = body
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatalf("스텁 %s 를 못 만들었다: %v", name, err)
		}
	}

	cmd := exec.Command("/bin/bash", script)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	cmd.Env = []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + home,
		"TMPDIR=" + tmp,
	}
	for k, v := range o.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("statusline.sh 를 못 돌렸다: %v", err)
		}
		code = ee.ExitCode()
	}
	_, statErr := os.Stat(marker)
	return statusLineRun{code: code, stdout: out.String(), stderr: errb.String(), curlCalled: statErr == nil}
}

// canRunStatusLine 은 이 머신에서 상태줄을 실제로 돌릴 수 있는지 본다.
//
// ★ 못 돌리면 **밝히며** 건너뛴다. 조용히 공허해지는 것이 이 레포가 두 번 밟은 자리다
// (`canLintShell` 과 같은 규율). jq 는 이 스크립트의 전제라 없으면 정상 경로 축이 성립하지
// 않는다 — 그 자체가 결함이 아니라 **잴 수 없음**이고, 둘을 화면에서 가른다.
func canRunStatusLine(t *testing.T) bool {
	t.Helper()
	if !canLintShell(t) {
		return false
	}
	if err := workingJQ(); err != nil {
		t.Logf("이 머신에 jq 가 없다(%v) — 상태줄 실행 축을 건너뛴다. 렌더는 전적으로 jq 에 달려 있다", err)
		return false
	}
	return true
}

// workingJQ 는 jq 가 **도는지** 본다 — 있는지가 아니라.
//
// ★ `command -v` 로는 부족한 자리가 이 레포에 둘이다(상태줄·설치 스크립트). 이름은 있는데
// 못 도는 jq(깨진 설치·아키텍처 불일치·실행 권한 없음)가 같은 자리인데, 그 둘은 화면에
// 아주 다른 것을 낸다. 그래서 판정 하나를 둘이 나눠 쓴다.
func workingJQ() error { return exec.Command("jq", "--version").Run() }

// renderedLines 는 상태줄이 **화면에서 먹는 줄 수**다.
//
// ★ `TrimRight` 로 끝의 개행을 털고 세면 안 된다 — 그러면 `…jq\n\n` 같은 **끝의 빈 줄**이
// 한 줄로 세어져 화면을 먹는 변이가 초록으로 지나간다(변이 M13 에서 실제로 그랬다).
// 개행 하나가 줄 하나를 끝내고, 개행 없이 끝나면 그 조각도 한 줄이다.
func renderedLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// TestGrafikBarStatusLineActuallyRenders 는 상태줄이 **실제로 도는지** 본다.
//
// ★★ 이 항목의 뿌리다. 지금까지 이 9.6KB 를 재는 관문은 `bash -n`(파싱) 하나였다 —
// 파싱은 되는데 렌더가 죽는 경우를 아무도 안 봤다. 상태줄은 **오류를 화면에 못 낸다**:
// 죽으면 줄이 그냥 사라진다. 그래서 재는 것이 셋이다.
//
//	① 종료 코드가 0 이다 — 상태줄은 비정상 종료를 사용자에게 못 알린다
//	② 무언가를 낸다 — 초록인 채로 빈 줄을 내는 것은 통과가 아니다
//	③ **입력이 출력에 도달한다** — stdin 의 model.display_name 이 화면에 나타난다.
//	   ②만 재면 jq 가 전부 실패해도 껍데기(52바이트)를 내고 초록이다. 실측으로 봤다.
//
// ★ 줄 수도 문다. tput·stty 를 둘 다 실패시켰으니 폭은 **폴백 값**으로 정해지는데,
// 그 폴백이 죽어 있으면(빈 문자열) 스크립트는 최협 레이아웃으로 떨어져 줄이 여럿이 된다.
// 폭 분기 자체를 재는 것이 아니라 **폴백이 살아 있는가**를 재는 것이다 — 이 결함은
// tty 없는 환경(IDE·원격·웹)에서만 나타나 사람 눈에는 영영 안 보인다.
func TestGrafikBarStatusLineActuallyRenders(t *testing.T) {
	if !canRunStatusLine(t) {
		return
	}
	r := runStatusLine(t, grafikStatusLinePayload, statusLineOpts{})
	if r.code != 0 {
		t.Fatalf("상태줄이 %d 로 끝났다 — 0 이어야 한다.\nstdout=%q\nstderr=%s\n"+
			"상태줄은 실패를 화면에 못 낸다: 줄이 그냥 사라지고 사용자는 원인을 모른다",
			r.code, r.stdout, r.stderr)
	}
	if strings.TrimSpace(r.stdout) == "" {
		t.Fatalf("상태줄이 아무것도 안 냈다 — 초록인 채로 빈 줄은 통과가 아니다.\nstderr=%s", r.stderr)
	}
	if !strings.Contains(r.stdout, "Opus 5") {
		t.Fatalf("stdin 의 model.display_name(\"Opus 5\")이 출력에 없다.\nstdout=%q\n"+
			"파싱이 통째로 실패해도 이 스크립트는 껍데기를 내고 0 으로 끝난다(실측) —\n"+
			"**입력이 출력에 도달하는가**를 안 물으면 그 껍데기가 초록으로 지나간다", r.stdout)
	}
	if r.curlCalled {
		t.Fatalf("사용량 캐시가 신선한데 curl 이 불렸다 — 렌더가 매번 네트워크를 친다.\n" +
			"화면은 똑같아서 이 회귀는 조용하다")
	}
	if n := renderedLines(r.stdout); n != 1 {
		t.Fatalf("상태줄이 %d줄이다 — 폭 폴백이 죽었다.\nstdout=%q\n"+
			"tput·stty 를 둘 다 실패시켰으므로 폭은 폴백 값이어야 하고, 그 값이면 한 줄이다.\n"+
			"폴백이 빈 문자열이면 최협 레이아웃으로 떨어진다 — tty 가 없는 환경에서만 나타나\n"+
			"사람 눈에는 영영 안 보이는 부류다", n, r.stdout)
	}
}

// TestGrafikBarStatusLineSurvivesDegradedInput 은 **열화 경로**를 문다.
//
// ★ 상태줄은 매 렌더마다 도는 것이라 나쁜 입력 하나가 곧 줄의 영구 부재다.
// 셋 다 실제로 오는 모양이다: 파이프가 빈 채로 오는 판, 스키마가 바뀌는 날,
// 그리고 **jq 가 사라진 머신**(setup 은 jq 가 없으면 설치를 안 하지만, 이미 설치된 뒤
// jq 가 사라지면 이 스크립트만 남는다).
func TestGrafikBarStatusLineSurvivesDegradedInput(t *testing.T) {
	if !canRunStatusLine(t) {
		return
	}
	for _, c := range []struct {
		name  string
		stdin string
		env   map[string]string
		// minLines 는 그 폭에서 **최소 몇 줄이 나와야 하는가**다. 0 이면 안 문다.
		//
		// ★★ 이 축이 없으면 `COLUMNS` 폴백을 지우는 변경이 **초록으로 지나간다**(변이 M16).
		// 그러면 아래 폭 케이스들이 전부 기본 폭으로 떨어지고, 기본 폭 분기는 `printf` 로
		// 끝나 늘 0 이라 **종료 코드 축이 조용히 도달 불가**가 된다. 손잡이가 사라지는 것
		// 자체를 물어야 관문이 스스로 공허해지는 길을 막는다.
		minLines int
	}{
		{"빈 stdin", "", nil, 0},
		{"JSON 이 아니다", "not json at all", nil, 0},
		{"빈 객체", "{}", nil, 0},
		{"필드가 null 이다", `{"model":null,"cost":null,"workspace":null}`, nil, 0},
		// ★★ 좁은·중간 폭. 이것이 없으면 **종료 코드 축이 안 닿는다** — 변이 M7 이 그것을
		// 드러냈다(끝의 `exit 0` 을 지워도 초록이었다). 넓은 분기는 `printf` 로 끝나 늘 0 인데,
		// 좁은·중간 분기는 `[ -n "$x" ] && printf …` 로 끝나서 **마지막 조각이 비면 exit 1** 이다.
		// 그래서 통계·한도가 없는 페이로드를 그 분기에 태워야 이 축이 산다.
		{"좁은 폭, 통계 없음", `{"model":{"display_name":"Opus 5"}}`, map[string]string{"COLUMNS": "60"}, 2},
		{"중간 폭, 한도 없음", `{"model":{"display_name":"Opus 5"}}`, map[string]string{"COLUMNS": "100"}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := runStatusLine(t, c.stdin, statusLineOpts{env: c.env})
			if r.code != 0 {
				t.Fatalf("%s 에 %d 로 끝났다 — 0 이어야 한다.\nstdout=%q\nstderr=%s\n"+
					"열화 입력에 죽으면 그 세션의 상태줄이 통째로 사라진다", c.name, r.code, r.stdout, r.stderr)
			}
			if n := renderedLines(r.stdout); c.minLines > 0 && n < c.minLines {
				t.Fatalf("%s 에서 %d줄이 나왔다 — 최소 %d줄이어야 한다.\nstdout=%q\n"+
					"이 케이스는 COLUMNS 로 좁은 레이아웃을 태워 **종료 코드 축**을 재는 것이다.\n"+
					"줄 수가 모자라면 그 폭이 안 먹은 것이고, 그러면 위 단정이 기본 폭 분기를\n"+
					"재고 있다 — 그 분기는 printf 로 끝나 늘 0 이라 **아무것도 안 문다**",
					c.name, n, c.minLines, r.stdout)
			}
		})
	}
}

// TestGrafikBarStatusLineSaysSoWhenJQIsGone 은 jq 가 없을 때 **말을 하는지** 본다.
//
// ★★ 실측: jq 를 빼면 이 스크립트는 52바이트짜리 껍데기를 내고 stderr 로
// `jq: command not found` 를 열댓 줄 쏟았다. 종료 코드는 0 이 아니었다.
// 사용자에게는 **깨진 줄**로 보이고 원인은 어디에도 안 적힌다.
//
// 그래서 무는 것은 "죽지 않는다"가 아니라 **"원인을 화면에 적는다"** 다.
// 상태줄에서 stdout 은 유일하게 사람이 보는 채널이다 — stderr 는 아무 데도 안 뜬다.
func TestGrafikBarStatusLineSaysSoWhenJQIsGone(t *testing.T) {
	if !canRunStatusLine(t) {
		return
	}
	// ★ `command -v jq` 로는 이 상황을 못 만든다 — 스텁이 PATH 에 있으면 찾아지기 때문이다.
	// 그래서 스크립트도 **실행해 보고** 판정해야 하고, 이 스텁이 그 계약을 문다:
	// jq 라는 이름은 있는데 돌리면 실패하는 머신(깨진 설치·권한·아키텍처 불일치)도 같은 자리다.
	r := runStatusLine(t, grafikStatusLinePayload, statusLineOpts{
		stubs: map[string]string{"jq": "#!/bin/sh\nexit 127\n"},
	})
	if r.code != 0 {
		t.Fatalf("jq 가 없을 때 %d 로 끝났다 — 0 이어야 한다.\nstdout=%q", r.code, r.stdout)
	}
	if !strings.Contains(r.stdout, "jq") {
		t.Fatalf("jq 가 없는데 출력이 그 사실을 안 적는다: %q\n"+
			"stdout 은 상태줄에서 사람이 보는 유일한 채널이다 — 여기 안 적으면\n"+
			"사용자는 깨진 줄만 보고 원인을 영영 모른다", r.stdout)
	}
	if n := renderedLines(r.stdout); n != 1 {
		t.Fatalf("jq 진단이 %d줄이다 — 한 줄이어야 한다.\nstdout=%q\n"+
			"상태줄이 여러 줄을 먹으면 그 자체가 화면 파괴다", n, r.stdout)
	}
}

// ── setup-statusline.sh — **설치 경로**를 실행으로 잰다 ────────────────────────
//
// ★★ 위 상태줄 관문이 잠근 것은 렌더다. 그 앞에 **쓰기**가 있다:
// `setup-statusline.sh` 는 `~/.claude/settings.json` — 사용자의 실제 설정 파일 — 을 고친다.
// 그리고 이 스크립트가 **이 플러그인의 설치 경로 전부**다. 실패하면 상태줄이 안 뜨는 것으로만
// 나타나고, 최악은 사용자 설정을 망가뜨리는 것이다. 그러면 상태줄만 죽는 게 아니다.
//
// ★ **경계를 먼저 정했다** — 이 시험이 재는 것은 jq 가 아니라 스크립트다. 그래서:
//
//	· 결과 파일은 **Go 로 판다**(`readSettings`). jq 로 다시 읽으면 관문이 재는 것이
//	  스크립트인지 jq 인지 흐려지고, jq 의 출력 형식(들여쓰기·키 순서)에 시험이 묶인다
//	· jq 의 **동작**은 안 문다. 무는 것은 **jq 가 망가졌을 때 스크립트가 무엇을 하는가**다
//	· 관측 대상은 stdout 이 아니라 **파일에 남은 것**이다 — 이 스크립트의 산출물이 그것이다
//
// ★ 위 `runStatusLine` 을 **안 쓴다.** 그 봉인(사용량 캐시·가짜 자격증명·curl·tput·stty)은
// 렌더의 세계이고 설치 스크립트는 그 중 어느 것도 안 본다. 대신 이쪽이 필요한 것은 반대 모양이다:
// HOME 이 **빈 디렉토리가 아니라 준비된 상태**여야 한다. 공유하는 것은 jq 판정 하나뿐이다.

// setupRun 은 setup-statusline.sh 를 격리된 HOME 에서 **실제로 돌린 결과**다.
type setupRun struct {
	code   int
	stdout string
	stderr string
	// settings 는 스크립트가 겨눈 경로다($HOME/.claude/settings.json).
	settings string
	// target 은 **내용이 실제로 가야 할 곳**이다. 심링크가 아니면 settings 와 같다.
	target string
	// claudeDir 은 $HOME/.claude — 임시 파일 잔해를 여기서 센다.
	claudeDir string
	// before 는 keepBefore 일 때 **돌리기 전** target 의 파일 정보다.
	before os.FileInfo
}

// setupOpts 는 돌리기 전의 세계를 정한다.
type setupOpts struct {
	// seed 는 settings.json 에 심을 내용이다. nil 이면 **파일을 안 만든다**(첫 설치).
	// 빈 문자열을 가리키면 0바이트 파일을 만든다 — 그 둘은 스크립트에서 다른 갈래다.
	seed *string
	// seedMode 는 심은 파일의 모드다. 0 이면 0600.
	seedMode os.FileMode
	// symlink 는 settings.json 을 $HOME/dotfiles/settings.json 을 가리키는 **링크**로 만든다.
	// dotfiles 저장소로 설정을 관리하는 사람의 모양이다. "absolute" 나 "relative" —
	// **둘 다 실물이다**: `ln -s ~/dotfiles/…` 와 `ln -s ../dotfiles/…` 는 스크립트에서
	// 다른 갈래를 탄다(상대 링크는 링크가 있는 디렉토리 기준으로 풀어야 한다).
	symlink string
	// stubs 는 PATH 앞에 세울 스텁이다(이름 → 본문).
	stubs map[string]string
	// noJQ 는 PATH 에서 jq 를 **통째로** 없앤다. 스텁으로는 못 만드는 상황이다.
	noJQ bool
	// keepBefore 는 돌리기 **전**의 파일 정보를 `setupRun.before` 에 담아 온다
	// (원자적 교체를 `os.SameFile` 로 재려면 전후 둘 다 필요하다).
	keepBefore bool
}

func seedJSON(s string) *string { return &s }

// sealedPATHWithoutJQ 는 jq 가 **없는** PATH 를 만든다.
//
// ★ 스텁으로는 이 상황을 못 만든다 — PATH 앞에 `jq` 라는 이름이 있으면 `command -v` 가
// 찾아낸다. 그래서 PATH 자체를 갈고, 스크립트가 실제로 쓰는 외부 도구만 심링크로 넣는다.
func sealedPATHWithoutJQ(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range []string{"mkdir", "mktemp", "mv", "rm", "readlink", "dirname"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("이 머신에 %s 가 없다 — jq 부재 축을 만들 수 없다(잴 수 없음이지 결함이 아니다)", tool)
		}
		if err := os.Symlink(p, filepath.Join(dir, tool)); err != nil {
			t.Fatalf("%s 심링크를 못 만들었다: %v", tool, err)
		}
	}
	return dir
}

// canRunSetup 은 이 머신에서 설치 스크립트의 **정상 경로**를 잴 수 있는지 본다.
//
// ★ 못 재면 밝히며 건너뛴다 — 조용히 공허해지는 것이 이 레포가 두 번 밟은 자리다.
// jq 부재·고장 축은 이 판정에 안 걸린다: 그쪽은 jq 가 없는 것이 **전제**다.
func canRunSetup(t *testing.T) bool {
	t.Helper()
	if !canLintShell(t) {
		return false
	}
	if err := workingJQ(); err != nil {
		t.Logf("이 머신에 도는 jq 가 없다(%v) — 설치의 정상 경로 축을 건너뛴다. 이 스크립트의 전제가 jq 다", err)
		return false
	}
	return true
}

func runSetup(t *testing.T, o setupOpts) setupRun {
	t.Helper()
	root := repoRootFromCmdFd(t)
	proot := filepath.Join(root, "plugins", "grafik-bar")
	script := filepath.Join(proot, "scripts", "setup-statusline.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("setup-statusline.sh 가 없다: %v", err)
	}

	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("$HOME/.claude 를 못 만들었다: %v", err)
	}
	settings := filepath.Join(claudeDir, "settings.json")
	target := settings

	if o.symlink != "" {
		dots := filepath.Join(home, "dotfiles")
		if err := os.MkdirAll(dots, 0o755); err != nil {
			t.Fatalf("dotfiles 디렉토리를 못 만들었다: %v", err)
		}
		target = filepath.Join(dots, "settings.json")
		link := target
		switch o.symlink {
		case "absolute":
		case "relative":
			link = filepath.Join("..", "dotfiles", "settings.json")
		default:
			t.Fatalf("symlink 값이 %q 다 — \"absolute\" 나 \"relative\" 여야 한다", o.symlink)
		}
		if err := os.Symlink(link, settings); err != nil {
			t.Fatalf("settings.json 심링크를 못 만들었다: %v", err)
		}
	}
	if o.seed != nil {
		mode := o.seedMode
		if mode == 0 {
			mode = 0o600
		}
		if err := os.WriteFile(target, []byte(*o.seed), mode); err != nil {
			t.Fatalf("settings.json 을 못 심었다: %v", err)
		}
	}

	var before os.FileInfo
	if o.keepBefore {
		fi, err := os.Stat(target)
		if err != nil {
			t.Fatalf("돌리기 전 파일을 못 봤다: %v", err)
		}
		before = fi
	}

	bin := t.TempDir()
	for name, body := range o.stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatalf("스텁 %s 를 못 만들었다: %v", name, err)
		}
	}
	path := bin + string(os.PathListSeparator)
	if o.noJQ {
		path += sealedPATHWithoutJQ(t)
	} else {
		path += os.Getenv("PATH")
	}

	cmd := exec.Command("/bin/bash", script)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	// ★ CLAUDE_PLUGIN_ROOT 를 준다 — 훅이 주는 것이 그것이고, 그러면 스크립트가 겨누는
	// statusline.sh 가 **이 저장소의 것**이 되어 결과를 이름으로 확인할 수 있다.
	cmd.Env = []string{
		"PATH=" + path,
		"HOME=" + home,
		"CLAUDE_PLUGIN_ROOT=" + proot,
	}
	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("setup-statusline.sh 를 못 돌렸다: %v", err)
		}
		code = ee.ExitCode()
	}
	return setupRun{
		code: code, stdout: out.String(), stderr: errb.String(),
		settings: settings, target: target, claudeDir: claudeDir, before: before,
	}
}

// readSettings 는 돌린 뒤의 settings.json 을 **Go 로** 판다(jq 로 다시 읽지 않는다).
func readSettings(t *testing.T, r setupRun) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(r.target)
	if err != nil {
		t.Fatalf("돌린 뒤 settings.json 을 못 읽었다: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("돌린 뒤 settings.json 이 JSON 이 아니다: %v\n%s", err, raw)
	}
	return m
}

// statusLineCommand 는 결과에서 `.statusLine.command` 를 뽑는다. 없으면 "" 다.
func statusLineCommand(m map[string]any) string {
	sl, ok := m["statusLine"].(map[string]any)
	if !ok {
		return ""
	}
	cmd, _ := sl["command"].(string)
	return cmd
}

// tempLeftovers 는 $HOME/.claude 에 남은 **임시 파일** 이름들이다.
//
// ★ 되감기가 반쪽이면 사용자의 설정 디렉토리에 `settings.json.a1B2c3` 이 매 세션 하나씩
// 쌓인다. 화면에는 아무것도 안 뜬다.
func tempLeftovers(t *testing.T, r setupRun) []string {
	t.Helper()
	ents, err := os.ReadDir(r.claudeDir)
	if err != nil {
		t.Fatalf("$HOME/.claude 를 못 읽었다: %v", err)
	}
	var left []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "settings.json.") {
			left = append(left, e.Name())
		}
	}
	sort.Strings(left)
	return left
}

// TestGrafikBarSetupKeepsEverythingElse 는 **기존 키 보존**을 문다.
//
// ★★ 이 파일은 grafik-bar 만의 것이 아니다 — 사용자의 권한 규칙·모델·환경변수가 같이 산다.
// `.statusLine` 하나를 바꾸면서 나머지를 날리면 그것은 상태줄 결함이 아니라 **설정 파괴**다.
// 스크립트 머리말이 "touches ONLY the statusLine key, preserves everything else" 라고
// 약속한 자리이고, 지금까지 그 약속을 재는 것이 하나도 없었다.
func TestGrafikBarSetupKeepsEverythingElse(t *testing.T) {
	if !canRunSetup(t) {
		return
	}
	before := `{"model":"opus","permissions":{"allow":["Bash(ls:*)"],"deny":[]},` +
		`"env":{"FOO":"1"},"statusLine":{"type":"command","command":"bash /old/elsewhere.sh"}}`
	r := runSetup(t, setupOpts{seed: seedJSON(before)})
	if r.code != 0 {
		t.Fatalf("설치가 %d 로 끝났다 — 0 이어야 한다.\nstdout=%q\nstderr=%s", r.code, r.stdout, r.stderr)
	}
	after := readSettings(t, r)

	var want map[string]any
	if err := json.Unmarshal([]byte(before), &want); err != nil {
		t.Fatalf("심을 JSON 이 JSON 이 아니다: %v", err)
	}
	for _, key := range []string{"model", "permissions", "env"} {
		if !reflect.DeepEqual(after[key], want[key]) {
			t.Fatalf("설치가 %q 를 바꿨다 — `.statusLine` 만 만져야 한다.\n전: %#v\n후: %#v\n"+
				"이 파일에는 사용자의 권한 규칙과 환경변수가 같이 산다. 여기가 무너지면\n"+
				"상태줄이 아니라 **설정이** 깨지는 것이고, 그 피해는 이 플러그인 밖으로 나간다",
				key, want[key], after[key])
		}
	}
	if got := statusLineCommand(after); !strings.Contains(got, filepath.Join("grafik-bar", "scripts", "statusline.sh")) {
		t.Fatalf("`.statusLine.command` 가 이 플러그인을 안 가리킨다: %q\n"+
			"낡은 값(플러그인 갱신으로 경로가 밀린 뒤)을 못 고치면 상태줄은 조용히 안 뜬다", got)
	}
	if left := tempLeftovers(t, r); len(left) > 0 {
		t.Fatalf("성공한 갱신이 임시 파일을 남겼다: %v", left)
	}
}

// TestGrafikBarSetupDoesNothingWhenAlreadyPointedHere 는 **멱등성**을 문다.
//
// ★★ 이것이 SessionStart 예산(hooks.json 의 timeout 5초)의 근거다 — 매 세션 시작마다
// 도는 훅이라, 이미 맞을 때 아무것도 안 하는 것이 계약이다. 그리고 **매번 쓰면** 사용자의
// settings.json 이 매 세션 새 inode 가 되어, 그 파일을 보는 다른 것들(에디터·dotfiles
// 동기화·파일 감시자)이 매번 깨어난다.
//
// ★ `desired` 문자열을 시험이 손으로 만들지 않는다 — 그러면 스크립트의 형식을 복제하게 되고,
// 형식이 밀리는 날 시험이 **틀린 채로 초록**이 된다. 대신 **두 번 돌린다**: 첫 회가 정답을 쓴다.
func TestGrafikBarSetupDoesNothingWhenAlreadyPointedHere(t *testing.T) {
	if !canRunSetup(t) {
		return
	}
	first := runSetup(t, setupOpts{seed: seedJSON(`{"model":"opus"}`)})
	if first.code != 0 {
		t.Fatalf("첫 설치가 %d 로 끝났다.\nstderr=%s", first.code, first.stderr)
	}
	configured, err := os.ReadFile(first.target)
	if err != nil {
		t.Fatalf("첫 설치 결과를 못 읽었다: %v", err)
	}

	// 첫 회가 쓴 그대로를 다시 심고 두 번째를 돌린다.
	second := runSetup(t, setupOpts{seed: seedJSON(string(configured))})
	if second.code != 0 {
		t.Fatalf("두 번째 설치가 %d 로 끝났다.\nstderr=%s", second.code, second.stderr)
	}
	if second.stdout != "" || second.stderr != "" {
		t.Fatalf("이미 맞는데 말을 했다 — 조용해야 한다.\nstdout=%q\nstderr=%q\n"+
			"매 세션 시작마다 도는 훅이다. 여기서 한 줄이 나오면 그 줄이 매 세션 나온다",
			second.stdout, second.stderr)
	}
	again, err := os.ReadFile(second.target)
	if err != nil {
		t.Fatalf("두 번째 설치 결과를 못 읽었다: %v", err)
	}
	if string(again) != string(configured) {
		t.Fatalf("이미 맞는데 파일이 바뀌었다.\n전:\n%s\n후:\n%s\n"+
			"멱등이 아니면 5초 예산의 근거가 없고, 이 파일을 보는 것들이 매 세션 깨어난다",
			configured, again)
	}
}

// TestGrafikBarSetupRefusesToTouchInvalidJSON 은 **깨진 JSON 거부**를 문다.
//
// ★ 사용자가 settings.json 을 손으로 고치다 만 상태가 이것이다. 그때 이 스크립트가
// 덮어쓰면 사용자는 **고치던 내용을 통째로** 잃는다 — 복구 경로가 없다.
func TestGrafikBarSetupRefusesToTouchInvalidJSON(t *testing.T) {
	if !canRunSetup(t) {
		return
	}
	broken := "{\n  \"model\": \"opus\",\n  // 여기서 편집이 멈췄다\n"
	r := runSetup(t, setupOpts{seed: seedJSON(broken)})
	if r.code != 0 {
		t.Fatalf("깨진 JSON 에 %d 로 끝났다 — 0 이어야 한다(훅은 세션을 방해하면 안 된다).\nstderr=%s", r.code, r.stderr)
	}
	after, err := os.ReadFile(r.target)
	if err != nil {
		t.Fatalf("돌린 뒤 파일을 못 읽었다: %v", err)
	}
	if string(after) != broken {
		t.Fatalf("깨진 JSON 을 건드렸다.\n전:\n%s\n후:\n%s\n"+
			"이 상태는 대개 **편집 중**이다. 덮어쓰면 사용자가 쓰던 것이 사라지고 되돌릴 길이 없다",
			broken, after)
	}
	// ★★ 여기서 무는 것은 **원인을 적는가**이지 "무언가 적는가"가 아니다. 검증 관문을 통째로
	// 떼도 변환 jq 가 대신 실패해 파일은 그대로 남고 "could not update settings.json" 이
	// 나온다 — 위 두 단정만으로는 그것이 **초록으로 지나갔다**(변이 M8). 그러면 사용자는
	// 자기 파일이 깨졌다는 사실을 못 듣고, 상태줄이 왜 안 뜨는지도 모른다.
	//
	// 이 문장은 **바로 위 시험(깨진 jq)과 한 쌍**이다: 저쪽은 이 문장이 나오면 안 되고,
	// 여기는 나와야 한다. 둘이 같이 있어야 진단이 옳은 쪽을 가리킨다.
	if !strings.Contains(r.stderr, "not valid JSON") {
		t.Fatalf("건드리진 않았는데 **원인을 안 적었다**: stderr=%q\n"+
			"파일이 깨졌다는 것을 여기서 안 말하면 사용자는 상태줄이 안 뜨는 이유를 영영 모르고,\n"+
			"자기 settings.json 이 깨진 채로 남아 있다는 것도 모른다", r.stderr)
	}
}

// TestGrafikBarSetupRollsBackAFailedWrite 는 **원자적 쓰기**를 문다.
//
// ★ 변환이 도중에 죽는 판(디스크·메모리·jq 자신)에서 원본이 반쪽으로 남으면 안 된다.
// 스텁은 `jq -e .`(검증)는 통과시키고 `--arg`(변환)만 죽인다 — 그것이 **쓰는 순간의 실패**다.
func TestGrafikBarSetupRollsBackAFailedWrite(t *testing.T) {
	if !canRunSetup(t) {
		return
	}
	real, err := exec.LookPath("jq")
	if err != nil {
		t.Skipf("jq 경로를 못 찾았다: %v", err)
	}
	// `jq -e .`(검증)는 통과시키고 `--arg`(변환)만 죽인다 — 그것이 **쓰는 순간의 실패**다.
	dyingJQ := map[string]string{"jq": "#!/bin/sh\n" +
		"for a in \"$@\"; do case \"$a\" in --arg) exit 5;; esac; done\n" +
		"exec " + real + " \"$@\"\n"}
	before := `{"model":"opus","permissions":{"allow":["Bash(ls:*)"]}}`

	t.Run("설정이 있었다", func(t *testing.T) {
		r := runSetup(t, setupOpts{seed: seedJSON(before), stubs: dyingJQ})
		if r.code != 0 {
			t.Fatalf("변환 실패에 %d 로 끝났다 — 0 이어야 한다.\nstderr=%s", r.code, r.stderr)
		}
		after, err := os.ReadFile(r.target)
		if err != nil {
			t.Fatalf("돌린 뒤 파일을 못 읽었다: %v", err)
		}
		if string(after) != before {
			t.Fatalf("변환이 죽었는데 원본이 바뀌었다.\n전:\n%s\n후:\n%s\n"+
				"쓰기 도중 실패는 **전부 아니면 전무**여야 한다 — 반쪽 settings.json 은 세션을 못 뜨게 한다",
				before, after)
		}
		if left := tempLeftovers(t, r); len(left) > 0 {
			t.Fatalf("되감기가 임시 파일을 남겼다: %v\n"+
				"매 세션 시작마다 도는 훅이라 이것은 사용자의 설정 디렉토리에 **쌓인다**", left)
		}
	})

	// ★★ 설정 파일이 **없던** 판. 여기가 없으면 "쓸 수 있는지 알기 전에 파일부터 만드는"
	// 옛 순서가 초록으로 지나간다(변이 M4) — 그 순서는 만들어 놓고 "left unchanged" 라고
	// 적었다. 위 깨진-jq 시험은 이 변이를 못 잡는다: 탐침에서 먼저 나가버려 여기까지 안 온다.
	t.Run("설정이 없었다", func(t *testing.T) {
		r := runSetup(t, setupOpts{stubs: dyingJQ})
		if r.code != 0 {
			t.Fatalf("변환 실패에 %d 로 끝났다 — 0 이어야 한다.\nstderr=%s", r.code, r.stderr)
		}
		if _, err := os.Stat(r.target); err == nil {
			raw, _ := os.ReadFile(r.target)
			t.Fatalf("설치를 못 했는데 settings.json 을 만들어 놨다: %q\n"+
				"stderr 는 \"left unchanged\" 라고 적는다 — 그 문장이 거짓이다.\n"+
				"실패한 실행은 파일 시스템을 **찾은 그대로** 두고 나가야 한다", raw)
		}
		if left := tempLeftovers(t, r); len(left) > 0 {
			t.Fatalf("되감기가 임시 파일을 남겼다: %v", left)
		}
	})
}

// TestGrafikBarSetupSwapsTheFileInsteadOfOverwritingIt 은 **원자적 쓰기**를 성공 경로에서 문다.
//
// ★★ 위 되감기 시험은 실패 경로만 본다. 성공 경로에서 `mv` 가 `cp` 로 바뀌면 —
//
//	· 사용자의 파일이 **제자리에서 잘렸다 다시 채워진다**. 그 사이에 세션이 시작하면
//	  반쪽 JSON 을 읽고, 그 창은 설정 없이 뜬다
//	· 임시 파일이 `~/.claude/settings.json.a1B2c3` 로 **매 갱신마다 쌓인다**
//
// 둘 다 화면에 안 뜬다. 변이 M10 이 그 사각을 드러냈다 — 그때는 아무 시험도 안 물었다.
//
// ★ 무는 방법은 **파일이 바뀌었는가**다: rename 은 새 파일을 그 이름에 앉히므로 inode 가
// 갈리고, 제자리 덮어쓰기는 안 갈린다. `os.SameFile` 이 그것을 이식 가능하게 답한다
// (syscall.Stat_t 를 직접 뒤지면 GOOS=windows vet 이 깨진다).
func TestGrafikBarSetupSwapsTheFileInsteadOfOverwritingIt(t *testing.T) {
	if !canRunSetup(t) {
		return
	}
	r := runSetup(t, setupOpts{seed: seedJSON(`{"model":"opus"}`), keepBefore: true})
	if r.code != 0 {
		t.Fatalf("%d 로 끝났다.\nstderr=%s", r.code, r.stderr)
	}
	after, err := os.Stat(r.target)
	if err != nil {
		t.Fatalf("돌린 뒤 파일을 못 봤다: %v", err)
	}
	if os.SameFile(r.before, after) {
		t.Fatalf("갱신이 **제자리 덮어쓰기**였다 — 원자적 교체여야 한다.\n" +
			"제자리로 쓰면 그 순간에 시작한 세션이 반쪽 JSON 을 읽고, 그 창은 설정 없이 뜬다.\n" +
			"임시 파일에 쓰고 `mv` 로 앉혀라")
	}
	if left := tempLeftovers(t, r); len(left) > 0 {
		t.Fatalf("갱신이 임시 파일을 남겼다: %v\n"+
			"매 세션 시작마다 도는 훅이라 사용자의 설정 디렉토리에 **쌓인다**", left)
	}
}

// TestGrafikBarSetupNeverBlamesSettingsForABrokenJQ 는 jq 가 못 돌 때 **무고하지 않는지** 본다.
//
// ★★ 이것이 이 항목의 뿌리다. `command -v jq` 는 **이름만** 본다. 이름은 있는데 못 도는
// jq(깨진 설치·아키텍처 불일치·실행 권한 없음)를 만나면 스크립트는 검증 단계로 들어가고,
// 거기서 `jq -e . settings.json` 이 실패한다 — 그리고 그 실패를 **사용자 파일의 죄로 적는다**:
// "~/.claude/settings.json is not valid JSON". 파일은 멀쩡하다.
//
// ★ 이 오진이 나쁜 이유는 화면에 남는 문장이 **사용자에게 행동을 지시하기 때문**이다.
// 멀쩡한 settings.json 을 의심하게 만들고, 그 다음 수순은 그것을 지우거나 새로 쓰는 것이다.
// 그러면 이 스크립트가 **직접 건드리지 않고** 설정을 파괴한 것이 된다.
//
// 옆 스크립트(`statusline.sh`)는 같은 자리를 이미 `printf '{}' | jq -e .` 로 넘겼다.
// 이 관문이 그 규율을 설치 경로에도 잠근다.
func TestGrafikBarSetupNeverBlamesSettingsForABrokenJQ(t *testing.T) {
	if !canLintShell(t) {
		return
	}
	before := `{"model":"opus","permissions":{"allow":["Bash(ls:*)"]}}`
	for _, c := range []struct {
		name string
		opts setupOpts
	}{
		{"jq 가 PATH 에 없다", setupOpts{seed: seedJSON(before), noJQ: true}},
		{"jq 는 있는데 못 돈다", setupOpts{seed: seedJSON(before),
			stubs: map[string]string{"jq": "#!/bin/sh\nexit 127\n"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := runSetup(t, c.opts)
			if r.code != 0 {
				t.Fatalf("%s 에 %d 로 끝났다 — 0 이어야 한다.\nstderr=%s", c.name, r.code, r.stderr)
			}
			if strings.Contains(r.stderr, "not valid JSON") || strings.Contains(r.stdout, "not valid JSON") {
				t.Fatalf("%s 인데 **사용자 파일을 무고했다**.\nstdout=%q\nstderr=%q\n"+
					"파일은 멀쩡하고 못 도는 것은 jq 다. 이 문장은 사용자에게 멀쩡한 설정을\n"+
					"의심하고 손대라고 지시한다 — 스크립트가 직접 안 건드리고 설정을 파괴하는 경로다.\n"+
					"`command -v jq` 대신 **돌려 보고** 판정해라(statusline.sh 가 이미 그렇게 한다)",
					c.name, r.stdout, r.stderr)
			}
			after, err := os.ReadFile(r.target)
			if err != nil {
				t.Fatalf("돌린 뒤 파일을 못 읽었다: %v", err)
			}
			if string(after) != before {
				t.Fatalf("%s 인데 파일이 바뀌었다.\n전:\n%s\n후:\n%s", c.name, before, after)
			}
		})
	}
	// jq 가 못 도는데 settings.json 이 아예 없던 판. 스크립트는 아무것도 안 만들어야 한다.
	t.Run("jq 가 못 도는데 설정 파일이 없다", func(t *testing.T) {
		r := runSetup(t, setupOpts{stubs: map[string]string{"jq": "#!/bin/sh\nexit 127\n"}})
		if r.code != 0 {
			t.Fatalf("%d 로 끝났다 — 0 이어야 한다.\nstderr=%s", r.code, r.stderr)
		}
		if _, err := os.Stat(r.target); err == nil {
			raw, _ := os.ReadFile(r.target)
			t.Fatalf("설치를 못 하면서 settings.json 을 만들어 놨다: %q\n"+
				"stderr 는 \"left unchanged\" 라고 적는다 — 그 문장이 거짓이다.\n"+
				"쓸 수 있는지 **먼저** 확인하고, 못 하면 파일 시스템을 안 건드려야 한다", raw)
		}
	})
}

// TestGrafikBarSetupWritesThroughASymlink 는 **심링크를 안 끊는지** 본다.
//
// ★★ dotfiles 로 설정을 관리하면 `~/.claude/settings.json` 이 저장소 안 파일을 가리키는
// **링크**다. `mv tmp settings` 는 그 링크를 **정규 파일로 갈아치운다** — 링크가 사라지고,
// 저장소의 진짜 파일은 옛 내용 그대로 남는다.
//
// 그 다음이 이 결함의 실물이다: 사용자는 상태줄이 뜨니 잘 된 줄 알고, dotfiles 저장소는
// 그날부터 이 파일의 변경을 **하나도 못 받는다**. 다음 머신에서 설정을 복원하면 그 사이의
// 모든 변경이 없다. 화면에는 아무것도 안 뜬다 — 이 레포가 계속 쫓는 그 부류다.
func TestGrafikBarSetupWritesThroughASymlink(t *testing.T) {
	if !canRunSetup(t) {
		return
	}
	// ★ 절대·상대 **둘 다** 돈다. 상대 링크(`ln -s ../dotfiles/settings.json`)는 링크가
	// 있는 디렉토리 기준으로 풀어야 하는데, 그것을 안 하면 `$PWD` 기준으로 풀려 엉뚱한
	// 곳에 쓰거나 아무것도 못 찾는다 — 절대 링크만 재면 그 갈래가 **안 닿는다**.
	for _, kind := range []string{"absolute", "relative"} {
		t.Run(kind, func(t *testing.T) {
			r := runSetup(t, setupOpts{symlink: kind, seed: seedJSON(`{"model":"opus"}`)})
			if r.code != 0 {
				t.Fatalf("%d 로 끝났다 — 0 이어야 한다.\nstderr=%s", r.code, r.stderr)
			}
			fi, err := os.Lstat(r.settings)
			if err != nil {
				t.Fatalf("돌린 뒤 settings.json 을 못 봤다: %v", err)
			}
			if fi.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("%s 링크가 **정규 파일로 갈렸다**.\n"+
					"dotfiles 저장소의 진짜 파일(%s)은 옛 내용 그대로 남고, 그날부터 이 파일의\n"+
					"변경을 하나도 못 받는다. 상태줄은 떠서 사용자는 잘 된 줄 안다 —\n"+
					"쓰기 전에 링크를 따라가라", kind, r.target)
			}
			after := readSettings(t, r)
			if got := statusLineCommand(after); !strings.Contains(got, filepath.Join("grafik-bar", "scripts", "statusline.sh")) {
				t.Fatalf("%s 링크 대상에 설정이 안 들어갔다: %q", kind, got)
			}
			if m, ok := after["model"].(string); !ok || m != "opus" {
				t.Fatalf("%s 링크 대상의 기존 키가 사라졌다: %#v", kind, after["model"])
			}
		})
	}
}

// TestGrafikBarSetupLeavesSettingsUnwritableByOthers 는 쓰고 난 뒤의 **모드**를 문다.
//
// ★ 이 파일은 `~/.claude/.credentials.json` 옆에 산다. 여기 무는 것은 "모드를 보존하는가"가
// 아니라 — 그것은 이식 가능한 한 줄이 없어서 **일부러 안 한다**(판단 본문 참고) —
// **남이 쓸 수 있게 열리지 않는가**다. 값이 아니라 불변식을 무는 자리다.
func TestGrafikBarSetupLeavesSettingsUnwritableByOthers(t *testing.T) {
	if !canRunSetup(t) {
		return
	}
	r := runSetup(t, setupOpts{seed: seedJSON(`{"model":"opus"}`), seedMode: 0o644})
	if r.code != 0 {
		t.Fatalf("%d 로 끝났다.\nstderr=%s", r.code, r.stderr)
	}
	fi, err := os.Stat(r.target)
	if err != nil {
		t.Fatalf("돌린 뒤 파일을 못 봤다: %v", err)
	}
	if perm := fi.Mode().Perm(); perm&0o022 != 0 {
		t.Fatalf("설치가 settings.json 을 %04o 로 남겼다 — 그룹·타인 쓰기가 열렸다.\n"+
			"이 파일은 .credentials.json 옆에 살고, 여기 쓸 수 있으면 statusLine 의\n"+
			"`command` 로 임의 명령이 매 렌더 돈다", perm)
	}
}
