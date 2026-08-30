package mcpsrv

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// 세션 정체 — 실측된 환경 축에서만 온다(설계 §13).
//
// ★ 도구에 세션 인자가 없다. 파생 가능한 값에 파라미터를 만들면 틀린 값이 들어오고,
// 그 틀린 값은 검사로도 막히지 않는다(우회할 필드가 있으면 우회된다 — 원칙 ①).
// 그래서 이 파일이 **유일한** 정체의 원천이고, 못 읽으면 조용히 익명으로 진행하지 않는다.

// 정체를 만드는 환경 축의 이름. 문자열을 여기저기 박지 않는다 —
// fd doctor 의 platformAxes 와 같은 이름을 써야 "무엇이 안 왔나"가 두 도구에서 같은 말이 된다.
const (
	EnvSessionID  = "CLAUDE_CODE_SESSION_ID"
	EnvProjectDir = "CLAUDE_PROJECT_DIR"

	// EnvCodexSessionID 는 codex 가 **셸 자식**에 심는 세션 id 다(2026-08-30 실측,
	// codex-cli 0.151.0). CODEX_THREAD_ID 와 같은 값이고, 훅 페이로드의 session_id 및
	// codex 배너의 session id 와도 같다 — 셋이 일치하므로 codex 에는 표류가 없다.
	//
	// ★ **MCP 자식에는 안 온다.** codex 는 MCP 프로세스에 코어 환경 13개만 주므로
	// 이 변수를 거기서 찾으면 영원히 없다. 그 갈래는 별도 설계(DESIGN §14 ④)다.
	EnvCodexSessionID = "CODEX_SESSION_ID"
)

// 하네스 — 정체가 **어느 도구**에서 왔는가(DESIGN 「14. 하네스 축」).
//
// ★ 이 축은 환경으로 못 가른다. 중첩 실행이 양방향으로 거짓말하기 때문이다 —
// Claude 세션 안에서 띄운 codex 는 CLAUDE_CODE_SESSION_ID 를 물려받고, 그 반대도 같다
// (실측). 그래서 **진입점이 선언한다**(`--harness`). 아는 쪽은 설치물이다.
const (
	HarnessClaude = "claude"
	HarnessCodex  = "codex"
)

// harnessSessionEnv 는 하네스별 세션 id 환경변수다. 이 표가 "어느 이름을 부를 것인가"의
// 유일한 원천이다 — 배너·거절 사유·doctor 가 같은 말을 해야 하기 때문이다.
var harnessSessionEnv = map[string]string{
	HarnessClaude: EnvSessionID,
	HarnessCodex:  EnvCodexSessionID,
}

// harnessProbeOrder 는 **선언이 없을 때** 세션 id 를 찾는 순서다.
//
// ★ 이것은 하네스 판정이 아니다. 값을 하나 찾을 뿐이고 Identity.Harness 는 빈 채로 남는다 —
// 관측되지 않은 것을 이름 붙이면 그것이 곧 지어내는 것이다(DESIGN §14 ②).
var harnessProbeOrder = []string{HarnessClaude, HarnessCodex}

// KnownHarness 는 이 이름을 아는가다. 순수 함수다.
func KnownHarness(name string) bool {
	_, ok := harnessSessionEnv[strings.TrimSpace(name)]
	return ok
}

// HarnessNames 는 아는 하네스 이름들이다. 거절 사유가 목록을 낼 때 쓴다.
func HarnessNames() []string {
	out := make([]string, 0, len(harnessSessionEnv))
	for k := range harnessSessionEnv {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SessionEnvFor 는 그 하네스의 세션 id 환경변수 이름이다. 모르는 이름이면 빈 문자열이다.
func SessionEnvFor(harness string) string { return harnessSessionEnv[strings.TrimSpace(harness)] }

// 환경 축이 **아닌** 결손 축의 이름. 위 둘과 달리 환경변수에서 오지 않고 판정에서 나온다.
//
// ★ 이름을 상수로 뽑는 이유는 소비자가 셋으로 늘어서다 — 결손 목록에 넣는 자리(ResolveIdentity),
// 주입 뒤 다시 판정하는 자리(mcpsrv.go), 그리고 그 축이 무엇을 막는지 배너가 갈래를 트는 자리
// (Banner). 문자열을 세 곳에 박으면 한 곳만 고칠 때 조용히 어긋나고, 어긋난 쪽은 **배너가
// 막힌 도구를 된다고 약속하는** 모양으로 드러난다 — 그것이 이 상수가 생긴 경위다.
const axisProject = "project"

// Identity 는 이 MCP 서버 프로세스가 자기가 누구인지 아는 전부다.
//
// Missing 이 **이름 목록**인 것이 핵심이다. "정체 불명"이라는 불리언 하나로 접으면
// 어느 탐지가 깨졌는지 알 수 없고, 그러면 플랫폼이 바뀐 날 아무도 눈치채지 못한다
// (먼저 같은 문제를 푼 플러그인이 CLAUDE_ENV_FILE 에서 정확히 그렇게 조용히 죽었다 — 설계 §13).
type Identity struct {
	CCSessionID string
	ProjectDir  string
	Cwd         string
	Worktree    string // 세션의 작업 트리 절대경로. MCP stdio 서버의 cwd 가 그 값이다
	ProjectID   string
	ProjectPath string
	MachineID   string
	Hostname    string

	// Harness 는 **선언된** 하네스다. 관측이 아니라 선언이므로 빈 값이 곧 「미상」이고,
	// 그것을 claude 로 접지 않는다 — 접으면 codex 설치가 인자를 빠뜨린 날 그 카드가
	// 조용히 claude 로 들어간다.
	Harness string
	// SessionEnv 는 이 실행이 세션 id 를 **실제로 찾은(또는 찾으려 한)** 환경변수 이름이다.
	// 배너가 이 값을 불러야 "없는 변수를 가리키는 안내"가 안 나온다.
	SessionEnv string

	Missing  []string // 관측되지 않은 축의 **이름**
	Warnings []string // 관측은 됐으나 대체값을 쓴 축
}

// ResolveIdentity 는 환경·cwd·hostname 에서 세션 정체를 만든다. 순수 함수다.
//
// 인자로 받는 이유는 시험이 전역 환경을 흔들지 않고 이 판정을 직접 부를 수 있게 하기 위해서다.
// 못 읽은 축은 **지어내지 않고 이름으로 남긴다.**
func ResolveIdentity(get func(string) (string, bool), cwd string, cwdErr error, hostname string, hostErr error) Identity {
	return ResolveIdentityAs("", get, cwd, cwdErr, hostname, hostErr)
}

// ResolveIdentityAs 는 **선언된 하네스로** 정체를 만든다. 순수 함수다.
//
// harness 가 비면 하네스를 모르는 것이고, 그때는 아는 이름들을 순서대로 훑어 세션 id 를
// 찾되 **Harness 는 빈 채로 남긴다.** 값을 찾은 것과 하네스를 아는 것은 다른 질문이다.
func ResolveIdentityAs(harness string, get func(string) (string, bool), cwd string, cwdErr error, hostname string, hostErr error) Identity {
	id := Identity{}

	harness = strings.TrimSpace(harness)
	switch {
	case harness == "":
		// 선언이 없다 — 오늘까지의 동작 그대로 훑는다. 결손 이름은 첫 후보(claude)의 것이고,
		// 그 사실을 경고로 남긴다. 이 조합이 곧 "하네스를 모른다"의 정직한 표현이다.
		id.SessionEnv = EnvSessionID
		for _, h := range harnessProbeOrder {
			if v, ok := get(harnessSessionEnv[h]); ok && strings.TrimSpace(v) != "" {
				id.CCSessionID, id.SessionEnv = strings.TrimSpace(v), harnessSessionEnv[h]
				break
			}
		}
		// ★ **여기서 무조건 경고하지 마라.** 선언 없이 부르는 것은 지금 함대의 정상 상태다
		// (Claude 쪽 설치물이 아직 --harness 를 안 싣는다). 무조건 달면 살아 있는 모든 세션의
		// **모든 도구 응답 꼬리**에 잡음이 붙고, 그러면 사람이 배너를 안 읽게 된다 —
		// 배너가 무언가를 말해야 할 날 그것이 안 읽히는 것이 이 기구의 유일한 실패 양식이다.
		// 시험이 이 회귀를 잡았다(TestBannerNamesEveryMissingAxis).
		//
		// 말할 값이 있는 경우는 둘뿐이다:
		if id.SessionEnv != EnvSessionID && id.CCSessionID != "" {
			// (가) claude 가 아닌 축에서 값이 나왔다 — 이 창은 사실상 다른 하네스인데
			// 선언이 없다. 카드의 하네스가 「미상」으로 남는다는 사실을 말해야 한다.
			id.Warnings = append(id.Warnings,
				"하네스 선언이 없는데 세션 id 가 "+id.SessionEnv+" 에서 나왔다 — "+
					"이 창은 claude 가 아닌 것으로 보이나 카드의 하네스는 「미상」으로 남는다. "+
					"설치물이 --harness 를 실어야 한다")
		} else if id.CCSessionID == "" {
			// (나) 아무 축에서도 못 찾았다 — 무엇을 찾았는지 이름으로 말한다.
			// 결손 목록은 첫 후보의 이름만 싣기 때문이다.
			id.Warnings = append(id.Warnings,
				"하네스 선언이 없어 "+strings.Join([]string{EnvSessionID, EnvCodexSessionID}, "·")+
					" 를 순서대로 찾았고 둘 다 없었다")
		}
	case KnownHarness(harness):
		id.Harness, id.SessionEnv = harness, harnessSessionEnv[harness]
		if v, ok := get(id.SessionEnv); ok && strings.TrimSpace(v) != "" {
			id.CCSessionID = strings.TrimSpace(v)
		}
	default:
		// 모르는 이름을 조용히 무시하면 오타 하나가 「미상」으로 접혀 안 보인다.
		id.SessionEnv = EnvSessionID
		id.Warnings = append(id.Warnings,
			"모르는 하네스 이름이다: "+clip(harness, 40)+" — 아는 것은 "+
				strings.Join(HarnessNames(), "·")+" 뿐이다. 「미상」으로 진행한다")
		for _, h := range harnessProbeOrder {
			if v, ok := get(harnessSessionEnv[h]); ok && strings.TrimSpace(v) != "" {
				id.CCSessionID, id.SessionEnv = strings.TrimSpace(v), harnessSessionEnv[h]
				break
			}
		}
	}
	if id.CCSessionID == "" {
		id.Missing = append(id.Missing, id.SessionEnv)
	}

	if v, ok := get(EnvProjectDir); ok && strings.TrimSpace(v) != "" {
		id.ProjectDir = filepath.Clean(strings.TrimSpace(v))
	}

	if cwdErr != nil {
		id.Warnings = append(id.Warnings, "cwd 를 못 읽었다: "+clip(cwdErr.Error(), 200))
	} else if strings.TrimSpace(cwd) != "" {
		id.Cwd = filepath.Clean(strings.TrimSpace(cwd))
	}

	// 워크트리는 cwd 다(설계 §13: MCP stdio 서버의 cwd 가 프로젝트 디렉토리).
	// cwd 를 못 읽었을 때만 CLAUDE_PROJECT_DIR 로 대신하고, 대신했다는 사실을 남긴다 —
	// 워크트리에서 띄운 세션은 그 둘이 다를 수 있고, 그때 조용히 바꿔치면
	// 겹침 축이 남의 트리를 가리킨다.
	switch {
	case filepath.IsAbs(id.Cwd):
		id.Worktree = id.Cwd
	case filepath.IsAbs(id.ProjectDir):
		id.Worktree = id.ProjectDir
		id.Warnings = append(id.Warnings,
			"cwd 대신 "+EnvProjectDir+" 를 워크트리로 썼다 — 둘이 다르면 겹침 축이 다른 트리를 가리킨다")
	default:
		id.Missing = append(id.Missing, "cwd")
	}

	// 프로젝트 좌표. 경로는 git 파생의 뿌리이고 id 는 큐·보드의 좌표다.
	id.ProjectPath = id.ProjectDir
	if id.ProjectPath == "" {
		id.ProjectPath = id.Worktree
	}
	if id.ProjectDir == "" {
		id.Missing = append(id.Missing, EnvProjectDir)
	}
	id.ProjectID = ProjectIDFor(id.ProjectDir, id.Cwd)
	if id.ProjectID == "" {
		id.Missing = append(id.Missing, axisProject)
	}

	switch {
	case hostErr != nil:
		id.MachineID, id.Hostname = "unknown-machine", ""
		id.Warnings = append(id.Warnings,
			"hostname 을 못 읽어 machine_id 를 unknown-machine 으로 뒀다: "+clip(hostErr.Error(), 200))
	case strings.TrimSpace(hostname) == "":
		id.MachineID, id.Hostname = "unknown-machine", ""
		id.Warnings = append(id.Warnings, "hostname 이 비어 machine_id 를 unknown-machine 으로 뒀다")
	default:
		id.Hostname = strings.TrimSpace(hostname)
		id.MachineID = id.Hostname
	}

	return id
}

// ProjectIDFor 는 프로젝트 좌표 id 를 고른다. 순수 함수다.
//
// CLAUDE_PROJECT_DIR 의 마지막 성분이 정본이고, 없으면 cwd 의 마지막 성분이다.
// 루트("/")·상대 조각(".", "..")은 좌표가 못 된다 — 빈 문자열을 돌려주고 호출부가 거절한다.
func ProjectIDFor(projectDir, cwd string) string {
	for _, p := range []string{projectDir, cwd} {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		base := filepath.Base(filepath.Clean(p))
		switch base {
		case ".", "..", string(filepath.Separator), "":
			continue
		}
		return base
	}
	return ""
}

// Banner 는 정체·환경의 결손을 사람이 읽는 한 덩이로 만든다. 없으면 빈 문자열이다.
//
// ★ 이것이 "조용히 익명으로 진행하지 않는다"의 실물이다. 모든 도구 응답 꼬리에 붙는다 —
// 거절당한 도구만 보면 "이 도구가 원래 안 되나"로 읽히고, 되는 도구(board·alloc)의 결과는
// 정체 없이 나온 값이라는 사실이 화면에서 사라진다.
func (id Identity) Banner() string {
	if len(id.Missing) == 0 && len(id.Warnings) == 0 {
		return ""
	}
	var b strings.Builder
	if len(id.Missing) > 0 {
		fmt.Fprintf(&b, "⚠ 세션 정체가 반쪽이다 — 관측되지 않은 축: %s\n", strings.Join(id.Missing, " · "))
		for _, m := range id.Missing {
			fmt.Fprintf(&b, "   · %s: %s\n", m, axisWhy(m))
		}
		// ★ **프로젝트 축은 다른 축들과 급이 다르다 — 되는 것이 하나도 없다.**
		//
		// GateTool 의 첫 갈래(`id.ProjectID == ""`)가 sessionBoundTools 검사보다 **앞**이라
		// board·alloc 까지 함께 막는다. 그런데 이 배너는 결손이 무엇이든 "되는 것: 읽기
		// (board)·발번(alloc)" 을 찍었다 — 그 경우에 **거짓말**이고, 사람은 화면이 된다고
		// 한 도구를 부르고 거절당한다.
		//
		// 오래 안 드러난 이유는 이 자리가 **도달 불가**였기 때문이다: 프로젝트 좌표는 폴백
		// (경로의 마지막 성분)이 늘 채워서 첫 갈래가 사실상 안 걸렸다. 그 폴백을 "호출자가
		// 모른다고 답한 경우"에 안 깨우게 고치면서 이 길이 열렸고, 그래서 문구도 같이 참이
		// 돼야 한다 — 없던 결함이 아니라 **잠자던 것에 이빨이 달린** 자리다.
		if containsAxis(id.Missing, axisProject) {
			b.WriteString("   되는 것: 없다 — 프로젝트 좌표가 없으면 board 도 못 낸다(어느 프로젝트의 보드인지 모른다).\n")
			b.WriteString("   안 되는 것: 전부. 이 축은 다른 결손과 급이 달라 읽기(board)·발번(alloc)까지 막는다.\n")
			b.WriteString("   고치는 법: git 저장소 안에서 부르거나, FD_PROJECT 로 프로젝트를 명시해라.\n")
		} else {
			b.WriteString("   되는 것: 읽기(board)·발번(alloc).\n")
			b.WriteString("   안 되는 것: pick·note·add·finish·land·label — 귀속할 세션이 없으면 원장이 거짓이 된다.\n")
		}
		b.WriteString("   지어내지 않는다. `fd doctor` 가 이 축들을 실제로 잰다.\n")
	}
	for _, w := range id.Warnings {
		fmt.Fprintf(&b, "⚠ %s\n", w)
	}
	return strings.TrimRight(b.String(), "\n")
}

func axisWhy(axis string) string {
	switch axis {
	case EnvSessionID, EnvCodexSessionID:
		return "세션 정체의 원천이다. 이것이 없으면 이 프로세스는 자기가 어느 세션인지 모른다"
	case EnvProjectDir:
		return "프로젝트 루트다. cwd 로 대신하지만 그 둘이 다를 수 있다"
	case "cwd":
		return "워크트리 절대경로다. 없으면 경로 겹침 축이 통째로 죽는다"
	case axisProject:
		return "프로젝트 좌표다. 없으면 큐도 보드도 어느 프로젝트인지 모른다"
	default:
		return "관측되지 않았다"
	}
}

// 세션 귀속이 필요한 도구. 여기 있는 것은 원장에 세션 id 로 행을 남긴다.
//
// board·alloc 은 빠져 있다 — 전자는 읽기이고, 후자의 원장 행은 프로젝트 귀속이다.
// 정체가 반쪽이어도 그 둘은 답할 수 있고, 답할 수 있는 것까지 막으면
// 배너가 "서버가 통째로 죽었다"로 읽힌다.
//
// ★ 표에 없는 도구는 거절이 아니라 **통과**다(GateTool 의 `if !sessionBoundTools[tool]`).
//
//	land 는 resource_hold.session_id 와 landing_queue.session_id 로 원장에 행을 남기므로
//	반드시 넣는다. 빼먹으면 세션 좌표 없이 레인을 잡고, 실패는 "정체가 없다"가 아니라
//	FK/CHECK 위반으로 엉뚱하게 나온다 — 그리고 어떤 시험도 안 깨져서 조용하다.
//
//	label 도 같은 이유로 넣는다 — store.SetLabels 가 event 표에 item.label 행을
//	session_id 와 함께 남긴다(store/item.go 의 LogEvent 호출). event.session_id 는
//	FK/NOT NULL 이 없어 land 처럼 크래시로 드러나지는 않는다 — 세션 없이 불러도
//	조용히 성공하고 귀속이 빈 원장 행이 그대로 쌓인다. 크래시가 없다는 것은 조용해도
//	된다는 뜻이 아니다: 이 표의 기준은 "원장에 세션 id 로 행을 남기는가" 하나뿐이고
//	label 은 그 기준을 그대로 만족한다.
var sessionBoundTools = map[string]bool{
	"pick": true, "note": true, "add": true, "finish": true, "land": true, "label": true,
}

// GateTool 은 이 정체로 그 도구를 부를 수 있는지 판정한다. 순수 함수다.
//
// 불리언이 아니라 **사유**를 함께 돌려준다. 통과할 때도 사유를 채운다 —
// "왜 통과했나"가 없으면 게이트가 실제로 무엇을 봤는지 시험이 단정할 수 없고,
// 그러면 공허한 단정(항상 참인 조건을 검사하는 시험)이 통과한다.
func GateTool(tool string, id Identity) (ok bool, reason string) {
	if !KnownTool(tool) {
		return false, fmt.Sprintf("도구 %q 는 이 서버에 없다 — %s 뿐이다",
			clip(tool, 64), strings.Join(ToolNames(), " · "))
	}
	if id.ProjectID == "" {
		return false, fmt.Sprintf("프로젝트 좌표가 없다 — %s 도 cwd 도 못 읽었다. "+
			"어느 프로젝트의 보드·큐인지 정할 수 없다", EnvProjectDir)
	}
	if !sessionBoundTools[tool] {
		return true, fmt.Sprintf("%s 는 세션 귀속이 없어도 답할 수 있다(프로젝트 %s)", tool, id.ProjectID)
	}
	if id.CCSessionID == "" {
		// ★ **이 자리가 이 항목의 이유다.** 여기에 EnvSessionID 를 박아 두면 codex 세션에서
		// 존재하지도 않는 환경변수 이름을 사람에게 가리킨다 — 지어내지 말라고 만든 문장이
		// 그 자리에서 거짓이 된다. 관측한 이름(id.SessionEnv)을 부른다.
		return false, fmt.Sprintf("%s 를 못 읽어 세션 정체가 없다 — %s 는 원장에 세션 id 로 행을 남기므로 "+
			"익명으로 진행하면 그 행이 거짓이 된다. 지어내지 않는다", id.SessionEnvName(), tool)
	}
	if !filepath.IsAbs(id.Worktree) {
		return false, fmt.Sprintf("워크트리 절대경로가 없다(관측값 %q) — "+
			"세션 정체는 (machine, worktree, cc_session) 3중키다", clip(id.Worktree, 120))
	}
	return true, fmt.Sprintf("3중키가 전부 있다(machine=%s, worktree=%s, cc_session=%s)",
		clip(id.MachineID, 40), clip(id.Worktree, 80), clip(id.CCSessionID, 40))
}

// containsAxis · withoutAxis 는 결손 목록을 다루는 두 조각이다. 순수 함수다.
//
// 목록이 **이름의 슬라이스**라 집합 연산이 필요한데, 그 자료형을 map 으로 바꾸지 않는 이유는
// Missing 이 **순서 있는 관측 기록**이기 때문이다 — 배너가 그 순서로 찍고, 사람이 두 실행의
// 배너를 눈으로 견준다. 축이 다섯을 안 넘으므로 선형 훑기의 값은 재는 대상이 아니다.
func containsAxis(axes []string, want string) bool {
	for _, a := range axes {
		if a == want {
			return true
		}
	}
	return false
}

func withoutAxis(axes []string, drop string) []string {
	out := axes[:0:0] // 원본을 공유하지 않는다 — 호출부가 들고 있는 슬라이스를 덮어쓰면 안 된다
	for _, a := range axes {
		if a != drop {
			out = append(out, a)
		}
	}
	return out
}

// MissingAxes 는 정렬된 결손 축 이름이다. 로그에 싣는다(사람이 두 실행을 비교할 수 있게).
func (id Identity) MissingAxes() []string {
	out := append([]string(nil), id.Missing...)
	sort.Strings(out)
	return out
}

// SessionEnvName 은 이 정체가 세션 id 를 찾은(또는 찾으려 한) 환경변수 이름이다.
// 빈 값이면 claude 의 이름을 낸다 — 옛 값으로 만든 Identity 도 문장을 만들 수 있어야 한다.
func (id Identity) SessionEnvName() string {
	if strings.TrimSpace(id.SessionEnv) == "" {
		return EnvSessionID
	}
	return id.SessionEnv
}

// HarnessLabel 은 화면에 찍을 하네스 이름이다. 선언이 없으면 「미상」이다.
func (id Identity) HarnessLabel() string {
	if strings.TrimSpace(id.Harness) == "" {
		return "미상"
	}
	return id.Harness
}
