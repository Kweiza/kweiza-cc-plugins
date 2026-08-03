package legacy

import (
	"fmt"
	"os"
	"path/filepath"
)

// OutTargetVerdict 는 되쓰기 대상 디렉토리에 써도 되는지의 판정이다.
//
// 불리언이 아니라 **사유**를 담는다. "쓰면 안 된다"만 알면 왜 안 되는지 몰라
// 사람이 `--force` 를 무턱대고 붙인다 — 그 순간 이 가드는 존재하지 않는 것과 같아진다.
type OutTargetVerdict struct {
	OK     bool
	Code   string // empty | not-empty | git-worktree | has-legacy | unreadable
	Reason string // 항상 채운다
}

// JudgeOutTarget 은 되쓰기 대상을 판정한다. 순수 함수다(입력은 관측된 사실뿐).
//
// ★ 이 가드가 있는 이유: 이 도구 전체가 "이관 원본은 읽기 전용"이라는 규율 위에 서 있는데,
// 그 규율을 깨는 **가장 짧은 경로가 도구 자신 안에 열려 있었다.**
// `--out` 을 필수로 만든 앞선 가드는 "기본값이 원본이 되는 것"만 막았고,
// **인자로 원본을 주는 것**은 하나도 안 막았다. 오타 한 번이면 살아 있는 세션들의
// `.claude/` 위에 수백 개 파일이 덮이고, 그 디렉토리는 gitignore 라 `git checkout` 으로 못 되돌린다.
//
// 판정 축 셋을 **가른다** — 처방이 다르기 때문이다:
//
//	git-worktree  작업 트리 안이다. 되쓰기 산출물이 남의 커밋에 휩쓸린다. **--force 로도 안 된다**
//	has-legacy    이미 .claude/{sessions,queue,handoffs} 가 있다. 살아 있는 원본일 가능성이 높다
//	not-empty     비어 있지 않다. 덮어쓸 수 있지만 무엇을 덮는지 사람이 알아야 한다
func JudgeOutTarget(exists, isGitWorktree, hasLegacy bool, entryCount int) OutTargetVerdict {
	switch {
	case isGitWorktree:
		return OutTargetVerdict{Code: "git-worktree",
			Reason: "되쓸 자리가 git 작업 트리 안이다 — 산출물이 남의 커밋에 휩쓸리고, " +
				".claude/ 는 gitignore 라 되돌릴 수도 없다. 트리 밖의 빈 디렉토리를 줘라"}
	case hasLegacy:
		return OutTargetVerdict{Code: "has-legacy",
			Reason: "되쓸 자리에 이미 .claude/{sessions,queue,handoffs} 가 있다 — " +
				"살아 있는 원본일 수 있다. 덮어쓰려면 --force 와 함께 그 자리가 원본이 아님을 확인해라"}
	case exists && entryCount > 0:
		return OutTargetVerdict{Code: "not-empty",
			Reason: fmt.Sprintf("되쓸 자리에 이미 %d개 항목이 있다 — 덮어쓰려면 --force 를 줘라", entryCount)}
	default:
		return OutTargetVerdict{OK: true, Code: "empty",
			Reason: "빈 자리다(또는 아직 없다) — 되쓸 수 있다"}
	}
}

// ForceAllows 는 --force 가 이 판정을 뒤집을 수 있는지다.
//
// **git 작업 트리는 --force 로도 못 뒤집는다.** 탈출구를 만들면 그것이 곧 사고 경로가 되고,
// 이 축은 되돌릴 방법이 아예 없어서(gitignore) 탈출구의 대가가 무한하다.
func ForceAllows(code string) bool { return code == "not-empty" || code == "has-legacy" }

// InspectOutTarget 은 디렉토리를 관측해 JudgeOutTarget 의 입력을 만든다.
func InspectOutTarget(dir string) (exists, isGitWorktree, hasLegacy bool, entries int, err error) {
	fi, serr := os.Stat(dir)
	switch {
	case os.IsNotExist(serr):
		exists = false
	case serr != nil:
		return false, false, false, 0, fmt.Errorf("되쓸 자리를 읽지 못했다(%q): %w", clip(dir, 200), serr)
	case !fi.IsDir():
		return true, false, false, 0, fmt.Errorf("되쓸 자리가 디렉토리가 아니다: %q", clip(dir, 200))
	default:
		exists = true
		ents, rerr := os.ReadDir(dir)
		if rerr != nil {
			return true, false, false, 0, fmt.Errorf("되쓸 자리를 훑지 못했다(%q): %w", clip(dir, 200), rerr)
		}
		entries = len(ents)
		for _, sub := range []string{"sessions", "queue", "handoffs"} {
			if _, e := os.Stat(filepath.Join(dir, ".claude", sub)); e == nil {
				hasLegacy = true
				break
			}
		}
	}
	// git 작업 트리 판정은 **위로 올라가며** 본다 — 대상이 레포 안 하위 디렉토리일 수 있다.
	isGitWorktree = insideGitWorktree(dir)
	return exists, isGitWorktree, hasLegacy, entries, nil
}

// insideGitWorktree 는 이 경로(또는 조상)에 .git 이 있는지 본다.
// git 명령을 부르지 않는다 — 없는 디렉토리에도 답할 수 있어야 하고, 판정이 외부 상태에 의존하면 안 된다.
func insideGitWorktree(dir string) bool {
	p, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	for {
		if _, e := os.Stat(filepath.Join(p, ".git")); e == nil {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}
