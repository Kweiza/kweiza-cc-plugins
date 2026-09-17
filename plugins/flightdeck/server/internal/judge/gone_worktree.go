package judge

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kweiza/flightdeck/internal/model"
)

// 워크트리가 사라진 카드 — 서버가 **스스로** 카드를 닫는 유일한 판정이다
// (설계 §4 「세션은 어떻게 닫히나」의 셋째 경로).
//
// ★ 이것은 생존 판정이 아니다. 신호 나이·pid·무응답을 **한 글자도 안 본다.** 앞선 두 오판
// (죽었다고 본 세션이 6커밋 랜딩 · 419분 무갱신이 17초 전 살아 있음)은 둘 다 **세션의 행동**에서
// 죽음을 추론했다. 여기서 보는 것은 **서버가 git 에서 직접 관측하는 좌표의 부재**다 —
// §4 신호 표의 `commit·push` 가 "클라이언트를 안 믿는 유일한 신호"인 것과 같은 부류다.
// 카드의 워크트리가 git 목록에서 빠지고 디렉토리도 없으면, 그 카드는 그 좌표에서 일할 수 없다.
//
// ★ 판정은 둘로 갈린다. 섞으면 "좌표가 없다"와 "없어도 닫으면 안 된다"가 한 사유 문자열로
// 뭉개져, 안 닫힌 카드를 보고 어느 쪽 때문인지 못 가린다.
//
//	WorktreeGone      — 좌표의 부재(git 목록 · 관례 자리 · 서버 파일시스템)
//	MayCloseGoneCard  — 그 카드를 닫아도 되나(state · 선점 · 요청자 · 앞선 자동 닫기)
//
// ★★ 모든 불확실은 **안 닫는 쪽**으로 접힌다. 못 읽었다 · 못 쟀다 · 근거가 없다 · 경로 꼴이
// 애매하다 — 전부 "안 닫는다"다. 틀려서 안 닫으면 유령 카드가 남고(사람이 `fd close` 로 치운다,
// 오늘까지의 상태), 틀려서 닫으면 살아 있는 카드가 보드에서 사라진다. 두 비용이 대칭이 아니다.

// GoneWorktreeFacts 는 좌표 판정에 필요한 **관측 결과**다. 이 구조체는 git 도 파일시스템도 모른다.
type GoneWorktreeFacts struct {
	// Worktree 는 카드가 저장한 워크트리 값 그대로다(3중키의 둘째 축).
	Worktree string
	// ListOK 는 **이 카드의 프로젝트 저장소**에서 `git worktree list` 가 성공했는지다.
	//
	// ★ Listed 가 비었는지로 대신 판정하지 않는다. "목록을 못 읽었다"와 "목록에 없다"는 다른
	// 사실이고, 호출부가 실패 시 무엇을 채워 넘기든(옛 목록·주 저장소 하나) 이 값이 거짓이면
	// 그 목록은 부재의 근거가 못 된다.
	ListOK bool
	// Listed 는 그 목록의 경로 전부다. **prunable 도 포함한다** — git 이 아직 기록하고 있는
	// 워크트리는 "목록에 있다"이고, 컨테이너 서버에서 마운트 밖 워크트리가 정확히 그 모양이다.
	Listed []string
	// TreePresence 는 카드의 관례 워크트리 루트(GoneWorktreeCoords 의 tree)를 서버에서
	// lstat 한 결과다. 루트가 없으면 그 아래 어떤 경로도 없으므로 카드 경로 자체보다 강한 관측이다.
	TreePresence PathPresence
	// HomePresence 는 그 루트를 담는 목록 워크트리(GoneWorktreeCoords 의 home)를 서버에서
	// stat 한 결과다(디렉토리여야 Present).
	HomePresence PathPresence
}

// GoneWorktreeVerdict 는 좌표 판정이다. 사유는 **언제나** 채운다.
type GoneWorktreeVerdict struct {
	Gone   bool
	Tree   string // 부재를 잰 관례 워크트리 루트. 못 찾았으면 빈 문자열
	Home   string // 그 루트를 담는 목록 워크트리. 못 찾았으면 빈 문자열
	Reason string
}

// GoneWorktreeCoords 는 카드 워크트리에서 **잴 자리 둘**을 낸다. 순수 함수다.
//
//	tree — 카드 워크트리를 담거나 그것과 같은 **가장 안쪽** 관례 워크트리 루트
//	       (`.flightdeck/worktrees/<이름>` · `.claude/worktrees/<이름>`)
//	home — tree 를 **엄격히** 담는 목록 워크트리 중 가장 안쪽 것
//
// 호출부(service)가 이 둘을 stat 해서 GoneWorktreeFacts 에 싣고, WorktreeGone 이 **같은
// 함수로** 두 자리를 다시 구한다 — 자리를 두 곳에서 따로 구하면 잰 경로와 판정한 경로가
// 조용히 어긋난다.
func GoneWorktreeCoords(worktree string, listed []string) (tree, home string) {
	roots := conventionRoots(worktree)
	if len(roots) == 0 {
		return "", ""
	}
	tree = roots[len(roots)-1]
	for _, l := range listed {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		l = filepath.Clean(l)
		if strictlyInside(tree, l) && len(l) > len(home) {
			home = l
		}
	}
	return tree, home
}

// WorktreeGone 은 카드의 좌표가 git 과 서버 파일시스템 양쪽에서 사라졌는지 판정한다. 순수 함수다.
//
// 조건 일곱이 **전부** 서야 Gone 이다. 순서는 사유의 우선순위다.
//
//  1. git worktree list 를 읽었다 — 못 읽었으면 부재를 말할 근거가 없다(마운트 밖·권한·일시 오류)
//  2. 카드 워크트리가 그 목록에 없다 — 비교는 Clean + 대소문자 무시(같은 경로를 다르다고 보면
//     살아 있는 카드를 닫는다. 대소문자만 다른 두 워크트리를 같다고 보는 오류는 안 닫는 쪽이다)
//  3. 카드 워크트리가 **관례 워크트리 자리** 안에 있다 — 없으면 주 체크아웃이거나(그 하위
//     디렉토리 포함) 이 저장소의 워크트리였다는 근거가 없다. git 은 지운 워크트리를 기억하지
//     않으므로 "여기가 워크트리였다"를 서버가 아는 유일한 근거가 flightdeck 자신이 지키는
//     자리 관례다(conventionRoots — 갈림 탐지가 같은 근거로 지워진 워크트리를 되읽는다)
//  4. 그 관례 루트도 목록에 없다 — 있으면 카드는 살아 있는 워크트리의 하위 디렉토리일 뿐이다
//  5. 그 루트를 담는 목록 워크트리가 있다 — 없으면 이 저장소의 자리라는 근거가 없다(다른
//     저장소·다른 머신의 경로다)
//  6. 담는 워크트리가 서버에서 보인다 — 안 보이면 마운트 밖이라 부재를 못 잰다
//  7. 관례 루트가 서버에서 없다(ErrNotExist) — 있거나 못 쟀으면 안 닫는다
func WorktreeGone(f GoneWorktreeFacts) GoneWorktreeVerdict {
	if !f.ListOK {
		return GoneWorktreeVerdict{Reason: "git worktree list 를 못 읽었다 — 목록이 없으면 부재를 말할 근거가 없다(마운트 밖·권한·일시 오류일 수 있다)"}
	}
	wt := strings.TrimSpace(f.Worktree)
	if wt == "" {
		return GoneWorktreeVerdict{Reason: "카드의 워크트리 값이 비었다"}
	}
	if listedWorktree(wt, f.Listed) {
		return GoneWorktreeVerdict{Reason: "git worktree list 에 있다"}
	}
	tree, home := GoneWorktreeCoords(wt, f.Listed)
	v := GoneWorktreeVerdict{Tree: tree, Home: home}
	switch {
	case tree == "":
		v.Reason = "관례 워크트리 자리(.flightdeck/worktrees/<이름> · .claude/worktrees/<이름>) 안이 아니다 — 주 체크아웃이거나 이 저장소의 워크트리였다는 근거가 없다"
	case listedWorktree(tree, f.Listed):
		v.Reason = fmt.Sprintf("담는 워크트리 %s 가 git worktree list 에 있다 — 카드는 그 하위 디렉토리일 뿐이다", tree)
	case home == "":
		v.Reason = fmt.Sprintf("%s 를 담는 워크트리가 git worktree list 에 없다 — 이 저장소의 좌표라는 근거가 없다", tree)
	case f.HomePresence != PathPresent:
		v.Reason = fmt.Sprintf("담는 워크트리 %s 가 서버에서 안 보인다 — 마운트 밖이면 부재를 못 잰다", home)
	case f.TreePresence == PathPresent:
		v.Reason = fmt.Sprintf("%s 가 서버에 아직 있다", tree)
	case f.TreePresence != PathAbsent:
		v.Reason = fmt.Sprintf("%s 를 서버에서 못 쟀다 — 없다고 볼 근거가 아니다", tree)
	default:
		v.Gone = true
		v.Reason = fmt.Sprintf("git worktree list 에 없고 서버에도 없다(%s · 담는 워크트리 %s)", tree, home)
	}
	return v
}

// PriorAutoClose 는 이 카드가 **앞서 자동으로 닫힌 적이 있나**의 관측이다.
//
// ★ 0값이 Unknown 이다(PathPresence 와 같은 규율). "못 읽었다"가 "없었다"로 접히면
// 한 번만 닫는다는 한도가 원장 조회 실패 한 번에 풀린다.
type PriorAutoClose int

const (
	PriorAutoCloseUnknown PriorAutoClose = iota // 원장을 못 읽었다 — 닫을 근거가 아니다
	PriorAutoCloseNone                          // 읽었고, 없었다
	PriorAutoCloseSeen                          // 읽었고, 커밋된 자동 닫기가 있었다
)

// GoneCardGuards 는 좌표가 사라진 카드를 **닫아도 되는지** 판정하는 데 필요한 사실이다.
type GoneCardGuards struct {
	State  model.SessionState
	Claims int  // 이 카드가 쥔 선점 수(프로젝트 무관)
	IsSelf bool // 이 판정을 부른 요청이 바로 이 카드에서 왔다
	Prior  PriorAutoClose
}

// MayCloseGoneCard 는 좌표가 사라진 카드를 닫아도 되는지 판정한다. 순수 함수다.
//
//   - **active 만 닫는다.** blocked 는 사람이 사유와 함께 남긴 판단이다. paused 도 사람이 쓴
//     상태이고, 닫았다 되살리면(Tx.OpenSession 은 done → active 만 한다) paused 표시가 조용히
//     사라진다 — 되돌릴 수 없는 닫기는 이 경로의 전제를 깬다.
//   - **선점을 든 카드는 안 닫는다.** 닫힌 카드는 ListLive 에서 빠지고 그 선점이 아무에게도
//     안 보인다.
//   - **요청한 카드 자신은 안 닫는다.** 지금 부르고 있다는 것이 그 카드가 살아 있다는 관측이다.
//   - **카드당 한 번이다.** 자동으로 닫혔는데 다시 active 라면 그 3중키로 OpenSession 이
//     돌았다는 뜻이고(훅·MCP), 그 관측이 이 판정을 이긴다. 한도가 없으면 사라진 cwd 에서
//     신호를 보내는 세션이 신호와 보드마다 열림·닫힘을 오가고, 닫힐 때마다 지울 수 없는
//     event 행이 쌓인다.
func MayCloseGoneCard(g GoneCardGuards) (bool, string) {
	switch g.State {
	case model.SessionActive:
	case model.SessionBlocked:
		return false, "blocked 다 — 사람이 사유와 함께 남긴 판단이라 안 건드린다"
	case model.SessionPaused:
		return false, "paused 다 — 사람이 쓴 상태라 닫았다 되살리면 그 표시가 사라진다"
	default:
		return false, fmt.Sprintf("active 가 아니다(state=%q)", string(g.State))
	}
	if g.Claims > 0 {
		return false, fmt.Sprintf("선점 %d건을 쥐고 있다 — 닫으면 그 선점이 아무에게도 안 보인다", g.Claims)
	}
	if g.IsSelf {
		return false, "이 요청을 보낸 카드다 — 지금 부르고 있다는 것이 그 카드가 살아 있다는 관측이다"
	}
	switch g.Prior {
	case PriorAutoCloseNone:
		return true, "active · 선점 0건 · 요청자 아님 · 앞선 자동 닫기 없음"
	case PriorAutoCloseSeen:
		return false, "이미 한 번 자동으로 닫혔다가 다시 열렸다 — 그 좌표로 신호가 왔다는 관측이 이긴다"
	default:
		return false, "앞선 자동 닫기를 원장에서 못 읽었다 — 카드당 한 번이라는 한도를 못 지킬 수 있다"
	}
}

// listedWorktree 는 경로가 목록에 있는지 본다. Clean 뒤 대소문자를 무시한다.
//
// ★ 대소문자를 무시하는 것은 **안 닫는 쪽**으로 기우는 선택이다. macOS 기본 파일시스템은
// 대소문자를 안 가리므로 같은 워크트리가 두 꼴로 적힐 수 있고, 그것을 다르다고 보면 살아
// 있는 카드를 닫는다. 대소문자만 다른 **서로 다른** 워크트리 둘을 같다고 보는 반대 오류는
// 유령 하나를 남길 뿐이다.
func listedWorktree(p string, listed []string) bool {
	p = filepath.Clean(strings.TrimSpace(p))
	for _, l := range listed {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if strings.EqualFold(p, filepath.Clean(strings.TrimSpace(l))) {
			return true
		}
	}
	return false
}

// strictlyInside 는 p 가 root 의 **엄격한** 하위 경로인지 성분 단위로 본다.
//
// ★ 문자열 접두가 아니다 — 접두로 보면 root="/a/b" 일 때 "/a/bc/d" 가 안이라고 나온다
// (service/itempaths.go 의 observeOne 이 같은 이유로 filepath.Rel 을 쓴다).
// 둘 중 하나라도 절대경로가 아니면 false 다 — 상대경로는 어디 기준인지 모르는 좌표다.
// 대소문자는 **가린다** — 담는다고 보는 쪽이 닫는 쪽이라, 애매하면 안 담는 것으로 둔다.
func strictlyInside(p, root string) bool {
	if !filepath.IsAbs(p) || !filepath.IsAbs(root) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
