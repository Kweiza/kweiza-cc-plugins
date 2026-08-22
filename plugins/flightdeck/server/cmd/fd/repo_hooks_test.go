package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
