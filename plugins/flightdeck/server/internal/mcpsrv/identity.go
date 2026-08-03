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
)

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

	Missing  []string // 관측되지 않은 축의 **이름**
	Warnings []string // 관측은 됐으나 대체값을 쓴 축
}

// ResolveIdentity 는 환경·cwd·hostname 에서 세션 정체를 만든다. 순수 함수다.
//
// 인자로 받는 이유는 시험이 전역 환경을 흔들지 않고 이 판정을 직접 부를 수 있게 하기 위해서다.
// 못 읽은 축은 **지어내지 않고 이름으로 남긴다.**
func ResolveIdentity(get func(string) (string, bool), cwd string, cwdErr error, hostname string, hostErr error) Identity {
	id := Identity{}

	if v, ok := get(EnvSessionID); ok && strings.TrimSpace(v) != "" {
		id.CCSessionID = strings.TrimSpace(v)
	} else {
		id.Missing = append(id.Missing, EnvSessionID)
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
		id.Missing = append(id.Missing, "project")
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
		b.WriteString("   되는 것: 읽기(board)·발번(alloc).\n")
		b.WriteString("   안 되는 것: pick·note·add·finish — 귀속할 세션이 없으면 원장이 거짓이 된다.\n")
		b.WriteString("   지어내지 않는다. `fd doctor` 가 이 축들을 실제로 잰다.\n")
	}
	for _, w := range id.Warnings {
		fmt.Fprintf(&b, "⚠ %s\n", w)
	}
	return strings.TrimRight(b.String(), "\n")
}

func axisWhy(axis string) string {
	switch axis {
	case EnvSessionID:
		return "세션 정체의 원천이다. 이것이 없으면 이 프로세스는 자기가 어느 세션인지 모른다"
	case EnvProjectDir:
		return "프로젝트 루트다. cwd 로 대신하지만 그 둘이 다를 수 있다"
	case "cwd":
		return "워크트리 절대경로다. 없으면 경로 겹침 축이 통째로 죽는다"
	case "project":
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
var sessionBoundTools = map[string]bool{
	"pick": true, "note": true, "add": true, "finish": true,
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
		return false, fmt.Sprintf("%s 를 못 읽어 세션 정체가 없다 — %s 는 원장에 세션 id 로 행을 남기므로 "+
			"익명으로 진행하면 그 행이 거짓이 된다. 지어내지 않는다", EnvSessionID, tool)
	}
	if !filepath.IsAbs(id.Worktree) {
		return false, fmt.Sprintf("워크트리 절대경로가 없다(관측값 %q) — "+
			"세션 정체는 (machine, worktree, cc_session) 3중키다", clip(id.Worktree, 120))
	}
	return true, fmt.Sprintf("3중키가 전부 있다(machine=%s, worktree=%s, cc_session=%s)",
		clip(id.MachineID, 40), clip(id.Worktree, 80), clip(id.CCSessionID, 40))
}

// MissingAxes 는 정렬된 결손 축 이름이다. 로그에 싣는다(사람이 두 실행을 비교할 수 있게).
func (id Identity) MissingAxes() []string {
	out := append([]string(nil), id.Missing...)
	sort.Strings(out)
	return out
}
