package judge

import (
	"path/filepath"
	"sort"
	"strings"
)

// SameConversation 은 sameConversation 의 공개 이름이다.
//
// 로직은 prescribe.go 의 그것 하나뿐이다 — 여기는 껍데기다. service 가 cc 를 직접
// 비교하면 같은 판정이 두 자리에 살고, 그 어긋남은 어느 화면에도 안 뜬다.
// 껍데기를 이 파일에 두는 이유는 prescribe.go 를 안 열기 위해서다(남의 자리에 가깝다).
func SameConversation(a, b string) bool { return sameConversation(a, b) }

// SplitCard 는 갈림 탐지에 필요한 카드 한 장의 좌표다.
//
// LiveSession 을 재사용하지 않는다 — 그 구조체에는 머신도 워크트리도 없고,
// 겹침·처방 두 축이 이미 그것을 쓰고 있어 성분을 더하면 두 축의 입력이 함께 바뀐다.
type SplitCard struct {
	SessionID   string
	MachineID   string
	Worktree    string
	CCSessionID string
}

// SplitReport 는 한 대화가 **같은 트리**에 대해 서로 다른 worktree 값을 기록했다는 보고다.
type SplitReport struct {
	CCSessionID string
	MachineID   string
	Root        string   // git 이 아는 워크트리 루트
	Recorded    []string // 그 트리에 대해 기록된 서로 다른 값들. 정렬된다. 언제나 2개 이상
	SessionIDs  []string // 이 보고에 걸린 카드 전부. 정렬된다
}

// DetectUnnormalizedSplit 은 워크트리 정규화가 안 돈 흔적을 찾는다. 순수 함수다.
//
// 판정은 하나다 — **같은 (머신, cc, 소유 트리)인데 기록된 worktree 값이 둘 이상.**
// 정규화가 도는 클라이언트는 언제나 그 트리의 git 루트를 적으므로(cmd/fd/env.go
// resolveProject 의 --show-toplevel) 값이 여럿이면 최소 하나는 정규화 없이 열린 것이다.
//
// ★★ 왜 조상-자손 경로 쌍으로 판정하지 않는가. 앞선 판이 그렇게 했다가 실측에서
// **거짓 양성 56%** 를 냈다(2026-08-05, 조상-자손 쌍 100건 중 56건). 이 저장소의 링크
// 워크트리는 `<repo>/.flightdeck/worktrees/X` 즉 저장소 루트의 **자손 경로**에 살고,
// 그것은 정규화가 완벽히 도는 클라이언트도 만드는 정당한 모양이다. 경로 모양만으로는
// 못 가르고, git 이 아는 루트 목록이 있어야 갈린다.
//
// ★ 울타리:
//   - 이것은 정체 판정도 겹침 판정도 **아니다. 보고다.** 어느 소비자도 이 결과로 두
//     카드를 같은 세션이라고 보지 않는다. 카드는 여전히 3중키로만 같다.
//   - **CCSessionID 가 같을 때만 본다.** 앞선 세션이 상하위 17건을 가짜 겹침으로 셌다가
//     "전부 다른 대화였다"로 정정한 사고가 있다. 그 17건은 cc 가 달라 여기 안 걸린다.
//   - **형제 트리는 안 건드린다.** 소유 루트가 다르면 아예 다른 묶음이 된다.
//   - **빈 cc 끼리는 같다고 보지 않는다.**
//   - **worktreeRoots 가 비면 아무것도 보고하지 않는다.** 못 읽었다는 사실은 호출부가
//     파생 실패로 남긴다 — 여기서 추측으로 보고하면 위의 56%가 그대로 돌아온다.
func DetectUnnormalizedSplit(cards []SplitCard, worktreeRoots []string) []SplitReport {
	roots := make([]string, 0, len(worktreeRoots))
	for _, r := range worktreeRoots {
		if r = strings.TrimSpace(r); r != "" {
			roots = append(roots, filepath.Clean(r))
		}
	}
	if len(roots) == 0 {
		return nil
	}

	type key struct{ machine, cc, root string }
	groups := map[key]map[string][]string{} // (머신,cc,트리) → worktree 값 → 세션 id 들
	for _, c := range cards {
		cc := strings.TrimSpace(c.CCSessionID)
		m := strings.TrimSpace(c.MachineID)
		wt := strings.TrimSpace(c.Worktree)
		if cc == "" || m == "" || wt == "" {
			continue
		}
		wt = filepath.Clean(wt)
		if wt == "." {
			continue
		}
		root := owningRoot(wt, roots)
		if root == "" {
			continue // git 이 모르는 트리다 — "접혔어야 한다"를 판정할 근거가 없다
		}
		k := key{m, cc, root}
		if groups[k] == nil {
			groups[k] = map[string][]string{}
		}
		groups[k][wt] = append(groups[k][wt], c.SessionID)
	}

	var out []SplitReport
	for k, byWT := range groups {
		if len(byWT) < 2 {
			continue // 한 트리에 값 하나 — 정규화가 돈 모양이다
		}
		rec := make([]string, 0, len(byWT))
		var ids []string
		for wt, sids := range byWT {
			rec = append(rec, wt)
			ids = append(ids, sids...)
		}
		sort.Strings(rec)
		sort.Strings(ids)
		out = append(out, SplitReport{
			CCSessionID: k.cc, MachineID: k.machine,
			Root: k.root, Recorded: rec, SessionIDs: ids,
		})
	}
	// ★ 정렬 키가 셋이어야 결정적이다. cc 하나만 쓰면 같은 cc 가 서로 다른 머신
	//   둘에서 보고를 만들 때 두 보고의 상대 순서가 맵 순회에 따라 흔들린다 —
	//   그리고 "같은 cc 가 여러 머신에 걸친다"는 이 축이 감지하려는 모양 중 하나다.
	sort.Slice(out, func(i, j int) bool {
		if out[i].CCSessionID != out[j].CCSessionID {
			return out[i].CCSessionID < out[j].CCSessionID
		}
		if out[i].MachineID != out[j].MachineID {
			return out[i].MachineID < out[j].MachineID
		}
		return out[i].Root < out[j].Root
	})
	return out
}

// owningRoot 는 이 경로를 소유한 워크트리 루트다 — 조상-또는-자기인 루트 중 **가장 긴** 것.
//
// ★ 가장 긴 것을 골라야 한다. 가장 짧은 것을 고르면 `<repo>/.flightdeck/worktrees/X` 가
// 통째로 저장소 루트에 흡수되고, 그 순간 거짓 양성 56%가 돌아온다.
func owningRoot(p string, roots []string) string {
	best := ""
	for _, r := range roots {
		if p == r || isDescendant(r, p) {
			if len(r) > len(best) {
				best = r
			}
		}
	}
	return best
}

// isDescendant 는 child 가 parent **아래**인지다. 순수 함수다.
//
// ★ 문자열 접두가 아니라 **경로 성분 경계**로 본다. 접두로 보면 `/repo` 가
// `/repo-backup` 의 조상이 되고, 그것은 서로 무관한 두 저장소다.
func isDescendant(parent, child string) bool {
	if parent == child {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
