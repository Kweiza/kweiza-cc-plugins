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

// SplitReport 는 한 대화의 카드가 상하위 경로로 갈렸다는 **보고**다.
type SplitReport struct {
	CCSessionID string
	MachineID   string
	Ancestor    string   // 조상 쪽 워크트리(가장 짧은 것)
	Descendants []string // 그 아래로 잡힌 워크트리들. 정렬된다
	SessionIDs  []string // 이 보고에 걸린 카드 전부. 정렬된다
}

// DetectUnnormalizedSplit 은 워크트리 정규화가 안 돈 흔적을 찾는다. 순수 함수다.
//
// ★★ 이 함수는 이 저장소에서 **경로 접두 관계를 쓰는 유일한 자리**다. DESIGN §3 이
// 일부러 없앤 축이므로 울타리를 여기 못박는다:
//
//   - 이것은 정체 판정도 겹침 판정도 **아니다. 보고다.** 어느 소비자도 이 결과로 두
//     카드를 같은 세션이라고 보지 않는다. 카드는 여전히 3중키로만 같다.
//   - **CCSessionID 가 같을 때만 본다.** 앞선 세션이 상하위 17건을 가짜 겹침으로 셌다가
//     "전부 다른 대화였다"로 정정한 사고가 있다. 그 17건은 cc 가 달라 여기 안 걸린다.
//   - **형제 트리는 안 건드린다.** `.flightdeck/worktrees/A` 와 `/B` 는 서로 다른 git
//     워크트리이고 같은 repo-상대 경로를 만지면 병합 때 실제로 충돌한다 — 진짜 겹침이다.
//   - **빈 cc 끼리는 같다고 보지 않는다.** 못 읽음을 값으로 접으면 관측이 깨진 순간
//     이 축이 통째로 거짓 초록을 낸다.
//
// 정규화(`cmd/fd/env.go` resolveProject, 커밋 4de4b21)가 도는 클라이언트는 이 모양을
// **만들 수 없다** — 그래서 보고 하나가 곧 "그 카드를 연 클라이언트가 낡았다"의 증거다.
func DetectUnnormalizedSplit(cards []SplitCard) []SplitReport {
	// (머신, cc) 로 묶는다. 빈 cc 는 아예 안 담는다.
	type key struct{ machine, cc string }
	groups := map[key][]SplitCard{}
	for _, c := range cards {
		cc := strings.TrimSpace(c.CCSessionID)
		m := strings.TrimSpace(c.MachineID)
		if cc == "" || m == "" {
			continue
		}
		groups[key{m, cc}] = append(groups[key{m, cc}], c)
	}

	var out []SplitReport
	for k, g := range groups {
		// 워크트리를 정규화해 중복을 없앤다. 한 워크트리에 카드가 여럿이면 그것은
		// 이 축이 아니다(같은 트리의 다른 창 — 영영 안 합쳐지는 것이 옳다).
		byWT := map[string][]string{}
		for _, c := range g {
			wt := filepath.Clean(strings.TrimSpace(c.Worktree))
			if wt == "" || wt == "." {
				continue
			}
			byWT[wt] = append(byWT[wt], c.SessionID)
		}
		if len(byWT) < 2 {
			continue
		}
		wts := make([]string, 0, len(byWT))
		for wt := range byWT {
			wts = append(wts, wt)
		}
		sort.Strings(wts)

		// 가장 짧은 것부터 보며, 자기 아래로 들어간 것을 모은다.
		for _, anc := range wts {
			var desc []string
			for _, d := range wts {
				if d != anc && isDescendant(anc, d) {
					desc = append(desc, d)
				}
			}
			if len(desc) == 0 {
				continue
			}
			ids := append([]string{}, byWT[anc]...)
			for _, d := range desc {
				ids = append(ids, byWT[d]...)
			}
			sort.Strings(desc)
			sort.Strings(ids)
			out = append(out, SplitReport{
				CCSessionID: k.cc, MachineID: k.machine,
				Ancestor: anc, Descendants: desc, SessionIDs: ids,
			})
			break // 대화 하나에 보고 하나. 가장 짧은 조상이 대표한다
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CCSessionID < out[j].CCSessionID })
	return out
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
