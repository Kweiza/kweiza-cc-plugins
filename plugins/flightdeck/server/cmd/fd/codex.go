package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kweiza/flightdeck/internal/service"
)

// 설치 자산은 **바이너리에 실어 나른다.**
//
// ★ 파일로 두고 찾게 하지 않는 이유: `fd` 는 `~/.cache/flightdeck/bin` 에서 돌고
// `CLAUDE_PLUGIN_ROOT` 는 **codex 세션에 아예 없다**. 자산을 플러그인 트리에 두면 fd 가
// 그것을 찾는 탐색을 또 짜야 하는데, 그 탐색은 래퍼 안에 이미 한 벌 있다 — 같은 판정이
// 두 자리에 살면 반드시 표류한다(이 레포가 FD_STATE_DIR 규칙에서 겪은 그 모양이다).
// embed 는 그 질문 자체를 없앤다: 자산은 바이너리와 **같은 판**이고 찾을 것이 없다.

//go:embed assets/fd-hook
var codexWrapperScript []byte

//go:embed assets/codex-hooks.json
var codexHooksTemplate string

// codex 설치 축 — **무출력은 통과가 아니다.**
//
// 이 파일이 있는 이유는 codex 의 훅 신뢰 모델 하나다. TUI 첫 실행에서 "Hooks need review" 를
// 통과시키면 `~/.codex/config.toml` 에
// `[hooks.state."<hooks.json 경로>:<event>:<group>:<hook>"] trusted_hash` 가 박히고,
// **신뢰가 없거나 깨진 상태의 `codex exec` 는 훅을 조용히 건너뛴다** — 로그에 훅 얘기가
// 한 줄도 안 나온다(2026-08-30 실측, codex-cli 0.151.0). 판올림 뒤 훅이 죽어도 아무도 모른다.
//
// ★ **그런데 codex 자신의 진단은 이 축을 안 잰다.** `codex doctor` 는 19개 체크(system·disk·
// security·runtime·install·search·git·terminal·title·state·threads·config·auth·mcp·sandbox·
// updates·network·websocket·reachability) 어디서도 훅을 언급하지 않는다(2026-08-31 실측,
// `codex doctor --summary` 전량 확인). 그래서 이 침묵을 잡을 자리는 여기뿐이다.
//
// ★ **trusted_hash 를 재현하지 않는다.** 해시 입력 규칙은 codex 내부 계약이고 우리가 그것을
// 베끼면 codex 가 규칙을 바꾸는 날 이 코드가 **조용히 거짓**이 된다 — 잡으려는 병과 똑같은
// 모양이다. 대신 재현이 필요 없는 것만 잰다: ⑴ hooks.json 이 있나 ⑵ 그 경로로 신뢰가 박혀
// 있나 ⑶ 명령이 **고정 경로**인가. 셋이면 실용적으로 침묵을 다 가른다.

// CodexState 는 **관측된 값 전부**다. 판정은 이 값만 본다(setup.go 의 SetupState 와 같은 규율).
type CodexState struct {
	Present     bool   // codex 가 이 머신에 있나(PATH 또는 ~/.codex)
	Home        string // ~/.codex
	HooksPath   string // ~/.codex/hooks.json
	HooksRaw    string // 그 내용. "" 면 없다
	HooksErr    string // 읽다 실패한 사유. 있으면 "없다"와 구분된다
	ConfigRaw   string // ~/.codex/config.toml 내용
	WrapperPath string // 고정 경로 래퍼가 있어야 할 자리
	WrapperOK   bool   // 그 자리에 실행 가능한 파일이 있나
	NetDisabled bool   // CODEX_SANDBOX_NETWORK_DISABLED=1 — 이면 fd 가 서버에 통째로 못 붙는다

	// ── `fd` 그 자체 ────────────────────────────────────────────────────────
	//
	// ★ 축이 셋인 이유: "fd 가 안 된다"의 원인이 셋이고 **처방이 전부 다르다.**
	// 하나로 접으면 화면이 "안 된다"만 말하고 사람은 셋을 다 뒤진다.
	CLIPath       string // 깔려야 할 자리(래퍼 옆)
	CLIOK         bool   // 그 자리에 실행 가능한 파일이 있나
	CLIOnPath     bool   // 그 디렉토리가 PATH 에 있나
	CLIShadowedBy string // PATH 에서 `fd` 를 **먼저** 잡는 다른 것(fd-find 충돌). 없으면 빈 문자열
}

// codexHookCommands 는 hooks.json 에서 훅 명령 문자열을 전부 뽑는다. 순수 함수다.
//
// 두 번째 반환값은 **파싱이 됐나**다. 못 읽은 것과 훅이 0개인 것을 가르지 않으면
// 깨진 JSON 이 "훅 없음"으로 조용히 접힌다.
func codexHookCommands(raw string) (cmds []string, ok bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, false
	}
	for _, groups := range doc.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if c := strings.TrimSpace(h.Command); c != "" {
					cmds = append(cmds, c)
				}
			}
		}
	}
	return cmds, true
}

// codexTrusted 는 config.toml 에 이 hooks.json 경로로 신뢰가 박혀 있나를 본다. 순수 함수다.
//
// ★ 키 모양은 `[hooks.state."<hooks.json 경로>:<event>:<group>:<hook>"]` 이다(바이너리에서
// 확인한 형식 문자열: `hooks.state."…".trusted_hash`). TOML 파서를 안 쓴다 — 이 판정에
// 필요한 것은 **그 경로로 시작하는 키가 하나라도 있나**뿐이고, 그러자고 의존성을 하나
// 늘리면 이 레포의 의존성 목록(sqlite·x/sys 둘뿐)이 이 축 하나 때문에 커진다.
func codexTrusted(configRaw, hooksPath string) bool {
	if strings.TrimSpace(configRaw) == "" || strings.TrimSpace(hooksPath) == "" {
		return false
	}
	return strings.Contains(configRaw, `hooks.state."`+hooksPath+`:`)
}

// codexVersionedCommand 는 명령이 **버전이 든 경로**를 부르는지 본다. 순수 함수다.
//
// ★ 이것이 이 항목의 핵심 주장이다. 신뢰 해시는 명령 문자열만 보므로, 명령이
// `${CLAUDE_PLUGIN_ROOT}/bin/fd`(=`…/plugins/cache/<마켓>/flightdeck/<버전>/bin/fd`)면
// **fd 판올림마다 TUI 재승인**이고, 재승인 전까지 훅은 조용히 안 돈다.
func codexVersionedCommand(cmd string) bool {
	return strings.Contains(cmd, "CLAUDE_PLUGIN_ROOT") ||
		strings.Contains(cmd, "/plugins/cache/")
}

// CodexAxes 는 codex 설치 축을 doctor 줄로 만든다. 순수 함수다.
//
// Observed=false 를 **이름과 함께** 낸다(service.DoctorAxis 의 규율). 축이 없다는 사실
// 자체가 답인 자리가 여기다 — 훅이 안 깔린 것과 깔렸는데 신뢰가 없는 것은 다른 병이고
// 처방도 다르다.
func CodexAxes(st CodexState) []service.DoctorAxis {
	if !st.Present {
		return []service.DoctorAxis{{
			Name:   "codex",
			Detail: "이 머신에 codex 가 없다 — 이 축들은 재지 않는다(없는 것이 정상이다)",
		}}
	}

	axes := make([]service.DoctorAxis, 0, 4)

	// ── ① hooks.json 이 있나 ────────────────────────────────────────────────
	hooksAxis := service.DoctorAxis{
		Name:   "codex 훅 파일",
		Detail: "codex 세션이 fd 에 신호를 보내는 유일한 통로. 없으면 codex 카드가 보드에 영영 안 뜬다",
	}
	cmds, parsed := codexHookCommands(st.HooksRaw)
	switch {
	case st.HooksErr != "":
		hooksAxis.Detail = "읽다 실패했다: " + clip(st.HooksErr, 160) + " — " + hooksAxis.Detail
	case strings.TrimSpace(st.HooksRaw) == "":
		hooksAxis.Detail = "없다(" + clip(st.HooksPath, 120) + ") — `fd setup` 이 깐다. " + hooksAxis.Detail
	case !parsed:
		hooksAxis.Detail = "JSON 이 깨졌다(" + clip(st.HooksPath, 120) + ") — codex 가 훅을 통째로 못 읽는다"
	default:
		hooksAxis.Observed = true
		hooksAxis.Value = fmt.Sprintf("훅 %d건 · %s", len(cmds), clip(st.HooksPath, 100))
	}
	axes = append(axes, hooksAxis)

	// ── ② 신뢰가 박혀 있나 — **이 축이 침묵을 잡는다** ──────────────────────
	trustAxis := service.DoctorAxis{
		Name: "codex 훅 신뢰",
		Detail: "config.toml 의 hooks.state 항목. **없으면 codex 는 훅을 조용히 건너뛴다** — " +
			"로그에 한 줄도 안 남으므로 이 줄이 유일한 관측 창구다",
	}
	if hooksAxis.Observed {
		if codexTrusted(st.ConfigRaw, st.HooksPath) {
			trustAxis.Observed = true
			trustAxis.Value = "신뢰됨(config.toml 에 항목이 있다)"
		} else {
			trustAxis.Detail = "**아직 신뢰를 안 받았다 — 지금 훅은 조용히 안 돈다.** " +
				"codex TUI 를 한 번 띄워 \"Hooks need review\" 를 통과시켜라. " + trustAxis.Detail
		}
	} else {
		trustAxis.Detail = "hooks.json 이 먼저다. " + trustAxis.Detail
	}
	axes = append(axes, trustAxis)

	// ── ③ 명령이 고정 경로인가 — 판올림을 넘기는가 ──────────────────────────
	pinAxis := service.DoctorAxis{
		Name: "codex 훅 명령",
		Detail: "신뢰 해시는 **명령 문자열만** 본다. 버전이 든 경로를 부르면 fd 판올림마다 " +
			"TUI 재승인이고, 그때까지 훅은 조용히 안 돈다",
	}
	if hooksAxis.Observed {
		var bad []string
		for _, c := range cmds {
			if codexVersionedCommand(c) {
				bad = append(bad, c)
			}
		}
		if len(bad) == 0 {
			pinAxis.Observed = true
			pinAxis.Value = "고정 경로 — 판올림해도 재승인이 없다"
		} else {
			pinAxis.Detail = fmt.Sprintf("**훅 %d건이 버전 든 경로를 부른다** — 다음 판올림에 조용히 죽는다: %s · ",
				len(bad), clip(bad[0], 120)) + pinAxis.Detail
		}
	} else {
		pinAxis.Detail = "hooks.json 이 먼저다. " + pinAxis.Detail
	}
	axes = append(axes, pinAxis)

	// ── ④ 고정 경로 래퍼가 제자리에 있나 ────────────────────────────────────
	wrapAxis := service.DoctorAxis{
		Name:   "codex 훅 래퍼",
		Detail: "훅 명령이 부르는 고정 자리. 없으면 훅은 신뢰돼 있어도 아무것도 못 부른다",
	}
	if st.WrapperOK {
		wrapAxis.Observed = true
		wrapAxis.Value = clip(st.WrapperPath, 140)
	} else {
		wrapAxis.Detail = "없거나 실행 권한이 없다(" + clip(st.WrapperPath, 120) + ") — `fd setup` 이 깐다. " +
			wrapAxis.Detail
	}
	axes = append(axes, wrapAxis)

	// ── ⑤ `fd` 그 자체 ─────────────────────────────────────────────────────
	//
	// ★ 래퍼 축과 다른 질문이다. 래퍼는 **훅이** 부르는 절대경로라 PATH 와 무관하지만,
	// 이 축은 **사람이 이름으로** 부르는 것이라 PATH 가 전부다. 둘을 한 축으로 접으면
	// "훅은 도는데 손으로는 못 부른다"는 오늘의 실제 상태가 화면에서 사라진다.
	cliAxis := service.DoctorAxis{
		Name:   "codex 창의 fd",
		Detail: "codex 창에서 사람이 손으로 부르는 통로. 없으면 보드도 큐도 판단도 부를 방법이 없다",
	}
	switch {
	case !st.CLIOK:
		cliAxis.Detail = "없다(" + clip(st.CLIPath, 120) + ") — `fd setup` 이 깐다. " + cliAxis.Detail
	case st.CLIShadowedBy != "":
		// 깔려 있어도 **다른 fd 가 먼저 잡히면** 사람은 그것을 부르고 있다.
		// fd-find(rust) 가 흔한 상대다 — 그쪽은 파일 검색기라 인자가 통째로 다르다.
		cliAxis.Detail = "**" + clip(st.CLIShadowedBy, 100) + " 가 먼저 잡힌다** — " +
			"`fd` 를 쳐도 그쪽이 돈다(fd-find 라면 파일 검색기다). " +
			"별칭을 쓰거나 " + clip(filepath.Dir(st.CLIPath), 80) + " 를 PATH 앞에 둬라"
	case !st.CLIOnPath:
		cliAxis.Detail = "깔렸으나 " + clip(filepath.Dir(st.CLIPath), 80) + " 가 PATH 에 없다 — " +
			"이름으로는 못 부른다. 절대경로로 부르거나 PATH 에 더해라"
		cliAxis.Value = clip(st.CLIPath, 140)
	default:
		cliAxis.Observed = true
		cliAxis.Value = clip(st.CLIPath, 140) + " · PATH 에 있다"
	}
	axes = append(axes, cliAxis)

	// ── ⑥ 샌드박스 네트워크 ────────────────────────────────────────────────
	//
	// ★ 환경 축(CODEX_SANDBOX_NETWORK_DISABLED)은 service.ProbePlatform 이 이미 잰다.
	// 여기서 또 재지 않고 **처방만** 얹는다 — 같은 사실을 두 줄로 내면 사람이 둘 중
	// 무엇이 참인지 고르게 된다. 다만 이 값이 1 이면 위 축들이 전부 무의미하므로
	// (fd 가 서버에 아예 못 붙는다) 그 사실은 이름으로 말한다.
	if st.NetDisabled {
		axes = append(axes, service.DoctorAxis{
			Name: "codex 네트워크",
			Detail: "**차단돼 있다 — 위 축이 전부 맞아도 fd 는 서버에 못 붙는다.** " +
				"`-c sandbox_workspace_write.network_access=true` 로 켜라(무엇을 왜 여는지 알고 켜라)",
		})
	}

	return axes
}

// observeCodex 는 codex 축을 **실제로 잰다.** 판정은 안 한다(CodexAxes 가 한다).
func (a *App) observeCodex() CodexState {
	home := homeDir(a.env)
	st := CodexState{Home: filepath.Join(home, ".codex")}
	if home == "" {
		return st
	}
	st.HooksPath = filepath.Join(st.Home, "hooks.json")
	st.WrapperPath = CodexWrapperPath(home)

	// codex 가 있나 — PATH 에 있거나 ~/.codex 가 있으면 있다고 본다.
	// 둘 중 하나만 보면 놓친다: npm 설치는 PATH 에 있고, 처음 띄우기 전에는 ~/.codex 가 없다.
	if _, err := os.Stat(st.Home); err == nil {
		st.Present = true
	}
	if lookPath("codex") {
		st.Present = true
	}
	if !st.Present {
		return st
	}

	if b, err := os.ReadFile(st.HooksPath); err == nil {
		st.HooksRaw = string(b)
	} else if !os.IsNotExist(err) {
		st.HooksErr = err.Error()
	}
	if b, err := os.ReadFile(filepath.Join(st.Home, "config.toml")); err == nil {
		st.ConfigRaw = string(b)
	}
	if fi, err := os.Stat(st.WrapperPath); err == nil && fi.Mode()&0o111 != 0 {
		st.WrapperOK = true
	}
	if v, ok := a.env("CODEX_SANDBOX_NETWORK_DISABLED"); ok && strings.TrimSpace(v) == "1" {
		st.NetDisabled = true
	}

	// `fd` 그 자체 — 셋을 따로 잰다.
	st.CLIPath = CodexCLIPath(home)
	if fi, err := os.Stat(st.CLIPath); err == nil && fi.Mode()&0o111 != 0 {
		st.CLIOK = true
	}
	pathRaw, _ := a.env("PATH")
	dir := filepath.Dir(st.CLIPath)
	for _, p := range filepath.SplitList(pathRaw) {
		if p == "" {
			continue
		}
		if filepath.Clean(p) == filepath.Clean(dir) {
			st.CLIOnPath = true
			break
		}
		// ★ **우리 자리보다 앞에서** fd 를 잡는 것만 가림이다. 뒤에 있는 것은 안 가린다 —
		// 그 구분이 없으면 fd-find 를 깐 사람 전원에게 거짓 경보가 뜬다.
		cand := filepath.Join(p, "fd")
		if fi, err := os.Stat(cand); err == nil && fi.Mode()&0o111 != 0 && st.CLIShadowedBy == "" {
			st.CLIShadowedBy = cand
		}
	}
	return st
}

// CodexCLIPath 는 `fd` 그 자체가 놓일 자리다 — **래퍼 바로 옆**이다.
//
// ★ 왜 래퍼와 같은 디렉토리인가: PATH 안내가 한 줄로 끝나야 한다. 둘이 갈리면
// "무엇을 PATH 에 넣어야 하나"가 두 답이 되고, 사람은 둘 중 하나만 한다.
//
// ★ 왜 래퍼와 **같은 스크립트**를 심나: 그 스크립트가 설치본 중 가장 높은 판을 골라
// exec 한다(assets/fd-hook). 버전이 박힌 경로를 심으면 판올림마다 낡고, 낡은 것이
// 도는 동안 아무 화면도 그 사실을 안 말한다 — 이 레포가 19시간·115커밋만큼 낡은
// 수동 빌드로 돈 적이 있다.
func CodexCLIPath(home string) string {
	return filepath.Join(home, ".local", "bin", "fd")
}

// CodexWrapperPath 는 고정 경로 래퍼가 놓일 자리다.
//
// ★ **이 값을 바꾸면 신뢰가 통째로 깨진다.** 경로가 곧 명령 문자열이고, 명령이 바뀌면
// 이미 신뢰한 사용자 전원이 TUI 를 다시 띄워야 한다. 바꿀 이유가 생기면 그것은
// 새 설치가 아니라 **이주**이고, 그 비용을 문서에 적고 나서 바꿔라.
//
// ★ `~/.local/bin` 은 이 머신의 깨끗한 로그인 셸 PATH 에 **없다**(2026-08-31 실측:
// `env -i HOME=$HOME zsh -lc` 의 PATH 20개 항목 어디에도 없다). 그래도 이 자리를 쓰는
// 이유는 훅 명령이 **절대경로**라 PATH 와 무관하기 때문이다 — 사람이 손으로 `fd-hook` 을
// 이름으로 부르는 것만 안 될 뿐이고, 그 사실은 setup 이 말한다.
func CodexWrapperPath(home string) string {
	return filepath.Join(home, ".local", "bin", "fd-hook")
}

// RenderCodexHooks 는 템플릿의 플레이스홀더를 실제 래퍼 경로로 바꾼다. 순수 함수다.
//
// ★ 왜 템플릿에 플레이스홀더인가: 홈 경로는 사람마다 다른데 codex 가 `~` 나 `$HOME` 을
// 확장하는지 **재지 않았다**. 확장을 기대했다가 안 되면 훅은 "없는 파일"을 부르고,
// 그 실패는 codex 의 침묵 뒤에 숨는다. 절대경로를 박으면 그 질문 자체가 사라진다.
func RenderCodexHooks(tmpl, wrapperPath string) string {
	return strings.ReplaceAll(tmpl, "__FD_HOOK_PATH__", wrapperPath)
}

// ─────────────────────────────────────────────────────────────────────────────
// 설치 — `fd setup --install-codex` 만 이 자리를 부른다.
//
// ★ **`fd setup` 의 기본 갈래는 여전히 아무것도 안 깐다.** 이것은 그 규율의 예외가 아니다:
// 보고 갈래는 "이 명령을 실행해라"까지만 내고, 설치는 사람이 그 명령을 승인해서 부를 때만
// 일어난다(setup.go 머리말의 판정 그대로다).
//
// ★ 이 템플릿이 싣는 이벤트는 **다섯**이다(SessionStart·UserPromptSubmit·PostToolUse·
// PreCompact·Stop). 빠진 것과 그 이유:
//   - `SessionEnd` — codex 문서에는 있으나 **발화를 못 봤다.** 안 재고 실으면 "도는 줄
//     알았는데 안 도는" 침묵이 하나 더 생긴다. 이 항목이 통째로 싸우는 것이 그 침묵이다.
//   - `PostToolUse` 에 **matcher 를 안 준다.** codex 의 tool_name 축은 닫힌 열거가 아니고
//     (hook.go 의 codex 갈래 주석), 같은 패치가 apply_patch 로도 셸 heredoc 으로도 온다.
//     매처를 추측했다가 틀리면 발자국이 **통째로 0건**이 된다 — 그것이 설계 §6 이 가장
//     두려워하는 모양이고, fd 는 이미 tool_input 을 내용으로 판별하므로 거를 이유도 없다.
//   - `async` 필드를 안 쓴다. Claude 쪽 확장이고 codex 스키마에서 재지 않았다 —
//     모르는 필드로 스키마 검증에 걸리면 훅이 통째로 안 읽힌다.
// ─────────────────────────────────────────────────────────────────────────────

// InstallCodex 는 래퍼와 hooks.json 을 실제로 깐다.
func (a *App) InstallCodex(out io.Writer) int {
	home := homeDir(a.env)
	if home == "" {
		fmt.Fprintln(out, "HOME 을 못 읽어 설치할 자리가 없다.")
		return 1
	}
	st := a.observeCodex()
	if !st.Present {
		fmt.Fprintf(out, "이 머신에 codex 가 없다(%s 도 없고 PATH 에도 없다) — 깔 것이 없다.\n", st.Home)
		return 1
	}

	// ── ① 고정 경로 래퍼 ────────────────────────────────────────────────────
	//
	// ★ 래퍼는 **덮어써도 된다.** 신뢰 해시는 명령 문자열만 보므로 이 파일의 내용이 바뀌어도
	// 신뢰가 안 깨진다(2026-08-30 실측: 원복하면 스크립트를 통째로 갈아도 다시 신뢰된다).
	// 오히려 판올림 때 여기가 갱신돼야 새 판을 고르는 규칙이 산다.
	wrapper := CodexWrapperPath(home)
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		fmt.Fprintf(out, "래퍼를 둘 자리를 못 만들었다(%s): %v\n", filepath.Dir(wrapper), err)
		return 1
	}
	if err := os.WriteFile(wrapper, codexWrapperScript, 0o755); err != nil {
		fmt.Fprintf(out, "래퍼를 못 썼다(%s): %v\n", wrapper, err)
		return 1
	}
	fmt.Fprintf(out, "✓ 래퍼 %s\n", wrapper)

	// ── ①-b `fd` 그 자체 ────────────────────────────────────────────────────
	//
	// ★ **같은 스크립트**를 심는다. 그것이 설치본 중 가장 높은 판을 골라 exec 하므로
	// 판올림이 자동으로 따라온다 — 버전 박힌 경로를 심으면 낡고, 낡은 채로 도는 동안
	// 아무 화면도 그 사실을 안 말한다.
	//
	// ★ 그리고 **이것을 까는 것은 `--harness` 없는 호출 경로를 여는 일**이다. 그 봉인
	// (중첩 기동에서 두 세션 축이 동시에 차면 거절)이 먼저 서 있어야 한다 —
	// identity-harness-nesting-gate 가 그 선행이고, 같은 묶음에서 이미 섰다.
	cli := CodexCLIPath(home)
	if err := os.WriteFile(cli, codexWrapperScript, 0o755); err != nil {
		fmt.Fprintf(out, "fd 를 못 썼다(%s): %v\n", cli, err)
		return 1
	}
	fmt.Fprintf(out, "✓ fd %s\n", cli)

	// ── ② hooks.json ────────────────────────────────────────────────────────
	//
	// ★ **있으면 안 덮는다.** 사용자의 다른 훅이 거기 있을 수 있고, 덮으면 복구 경로가 0이다.
	// 대신 무엇을 넣어야 하는지 그대로 찍어서 사람이 병합하게 한다.
	want := RenderCodexHooks(codexHooksTemplate, wrapper)
	if strings.TrimSpace(st.HooksRaw) != "" {
		if strings.Contains(st.HooksRaw, wrapper) {
			fmt.Fprintf(out, "✓ hooks.json 에 이미 이 래퍼가 실려 있다 (%s) — 안 건드렸다\n", st.HooksPath)
		} else {
			fmt.Fprintf(out, "! hooks.json 이 이미 있다 (%s) — **안 덮었다.**\n", st.HooksPath)
			fmt.Fprintln(out, "  거기 당신의 다른 훅이 있을 수 있고 덮으면 되돌릴 길이 없다.")
			fmt.Fprintln(out, "  아래를 손으로 병합해라(각 이벤트의 hooks 배열에 넣으면 된다):")
			fmt.Fprintln(out, indentBlock(want, "    "))
			return 1
		}
	} else {
		if err := os.MkdirAll(st.Home, 0o755); err != nil {
			fmt.Fprintf(out, "%s 를 못 만들었다: %v\n", st.Home, err)
			return 1
		}
		if err := os.WriteFile(st.HooksPath, []byte(want), 0o644); err != nil {
			fmt.Fprintf(out, "hooks.json 을 못 썼다(%s): %v\n", st.HooksPath, err)
			return 1
		}
		fmt.Fprintf(out, "✓ hooks.json %s\n", st.HooksPath)
	}

	fmt.Fprint(out, RenderCodexNextSteps(a.observeCodex()))
	return 0
}

// RenderCodexNextSteps 는 설치 뒤 **사람이 해야 하는 것**을 낸다. 순수 함수다.
//
// ★ 신뢰를 자동으로 박지 않는다. 그것은 사용자가 눌러야 하는 관문이고, 도구가 대신 누르면
// 그 관문이 존재할 이유가 없어진다.
func RenderCodexNextSteps(st CodexState) string {
	var b strings.Builder
	b.WriteString("\n■ 이제 사람이 해야 하는 것\n")

	if !codexTrusted(st.ConfigRaw, st.HooksPath) {
		b.WriteString("  1. **codex TUI 를 한 번 띄워 훅을 신뢰해라** — \"Hooks need review\" 를 통과시켜야 한다.\n")
		b.WriteString("     $ codex\n")
		b.WriteString("     ★ 이것을 안 하면 codex 는 훅을 **조용히 건너뛴다**. 로그에 한 줄도 안 남고,\n")
		b.WriteString("       보드에 codex 카드가 영영 안 뜬다. 자동으로 박지 않는 이유는 이것이\n")
		b.WriteString("       당신이 눌러야 하는 관문이기 때문이다.\n")
	} else {
		b.WriteString("  1. ✓ 훅 신뢰는 이미 박혀 있다(config.toml).\n")
	}

	b.WriteString("  2. **샌드박스 네트워크를 열어라** — codex 기본값은 네트워크를 끊고,\n")
	b.WriteString("     그 상태의 fd 는 서버에 통째로 못 붙는다(실측: `connect: operation not permitted`).\n")
	b.WriteString("     $ codex -c sandbox_workspace_write.network_access=true\n")
	b.WriteString("     또는 ~/.codex/config.toml 에 그 값을 박아라. 이것은 당신의 샌드박스 정책을\n")
	b.WriteString("     여는 일이다 — 무엇을 왜 여는지 알고 켜라.\n")
	if st.NetDisabled {
		b.WriteString("     ! 지금 이 프로세스에서 CODEX_SANDBOX_NETWORK_DISABLED=1 이 관측된다.\n")
	}

	// ★ **PATH 를 말해야 한다.** 안 말하면 `fd` 를 깔아 놓고도 "명령을 못 찾겠다"에서
	// 끝난다 — 이 머신의 깨끗한 로그인 셸 PATH 20개 항목 어디에도 ~/.local/bin 이 없다
	// (2026-08-31 실측). 안 되는 것만 적고 우회로를 안 적으면 문서가 사용자를 버린다.
	dir := filepath.Dir(st.CLIPath)
	switch {
	case st.CLIShadowedBy != "":
		b.WriteString("  3. **`fd` 이름이 이미 다른 것에 잡혀 있다** — " + st.CLIShadowedBy + " 가 먼저다.\n")
		b.WriteString("     그것이 fd-find(파일 검색기)라면 인자가 통째로 다르다. 둘 중 하나를 골라라:\n")
		b.WriteString("     $ export PATH=\"" + dir + ":$PATH\"        # 이쪽을 앞에 둔다\n")
		b.WriteString("     $ alias fdk=" + st.CLIPath + "   # 또는 다른 이름으로 부른다\n")
	case !st.CLIOnPath:
		b.WriteString("  3. **PATH 에 " + dir + " 를 더해라** — 안 그러면 `fd` 를 이름으로 못 부른다.\n")
		b.WriteString("     $ export PATH=\"" + dir + ":$PATH\"   # ~/.zshrc 에도 넣어라\n")
		b.WriteString("     지금 당장은 절대경로로 부를 수 있다: $ " + st.CLIPath + " board\n")
	default:
		b.WriteString("  3. ✓ `fd` 가 PATH 에 있다(" + dir + ").\n")
	}

	b.WriteString("  4. 확인: codex 세션을 새로 열고 `fd board` 에 그 세션 카드가 뜨는지 봐라.\n")
	b.WriteString("     `fd doctor` 의 ■ codex 절이 네 축을 이름으로 낸다.\n")
	return b.String()
}

// indentBlock 은 여러 줄을 통째로 들여쓴다.
func indentBlock(s, pad string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

// RenderCodexSetup 은 `fd setup` 보고 갈래의 codex 절이다. 순수 함수다.
//
// ★ 설치를 **하지 않고** 무엇이 없는지와 다음 명령만 낸다.
func RenderCodexSetup(st CodexState) string {
	if !st.Present {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n■ codex\n")
	cmds, parsed := codexHookCommands(st.HooksRaw)
	switch {
	case strings.TrimSpace(st.HooksRaw) == "":
		b.WriteString("  훅이 안 깔렸다 — codex 세션은 보드에 안 뜬다.\n")
		b.WriteString("    $ fd setup --install-codex\n")
		b.WriteString("    왜: 래퍼(고정 경로)와 ~/.codex/hooks.json 을 깐다. 신뢰는 당신이 TUI 에서 누른다\n")
	case !parsed:
		b.WriteString("  hooks.json 이 깨졌다 — codex 가 훅을 통째로 못 읽는다: " + clip(st.HooksPath, 120) + "\n")
	case !codexTrusted(st.ConfigRaw, st.HooksPath):
		b.WriteString(fmt.Sprintf("  훅 %d건이 깔려 있는데 **신뢰가 없다 — 지금 조용히 안 돈다.**\n", len(cmds)))
		b.WriteString("    $ codex          (TUI 를 띄워 \"Hooks need review\" 를 통과시켜라)\n")
	default:
		b.WriteString(fmt.Sprintf("  ✓ 훅 %d건 · 신뢰됨\n", len(cmds)))
	}
	if !st.WrapperOK {
		b.WriteString("  래퍼가 없다(" + clip(st.WrapperPath, 120) + ") — 위 명령이 깐다\n")
	}
	return b.String()
}
