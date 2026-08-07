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
	Root        string   // 이 보고가 걸린 워크트리 루트
	Recorded    []string // 그 트리에 대해 기록된 서로 다른 값들. 정렬된다. 언제나 2개 이상
	SessionIDs  []string // 이 보고에 걸린 카드 전부. 정렬된다
}

// DetectUnnormalizedSplit 은 워크트리 정규화가 안 돈 흔적을 찾는다. 순수 함수다.
//
// 판정은 하나다 — **같은 (머신, cc, 소유 트리)인데 기록된 worktree 값이 둘 이상.**
// 정규화가 도는 클라이언트는 언제나 그 트리의 git 루트를 적으므로(cmd/fd/env.go
// resolveProject 의 --show-toplevel) 값이 여럿이면 최소 하나는 정규화 없이 열린 것이다.
//
// 둘째 반환값은 **어느 트리에도 못 붙인 카드 수**다. 침묵하면 "갈림 없음"과
// "그 트리를 못 알아봤다"가 화면에서 같아진다 — 호출부가 이 수를 반드시 낸다.
//
// ★★ 판정 규칙이 두 번 무너졌고, 그 실측이 이 설계의 근거다.
//
//	① 조상-자손 경로 쌍            → 조상-자손 쌍 100건 중 56건(56%)이 거짓 양성
//	② 살아 있는 git 워크트리 루트   → 보고 31건 중 26건(84%)이 거짓 양성
//	③ 살아 있는 루트 ∪ 관례 복원    → 보고 36건 중 거짓 양성 0건
//
// ①: 링크 워크트리가 `<repo>/.flightdeck/worktrees/X` 즉 저장소 루트의 자손 경로에
// 살아서, 정규화가 완벽히 도는 클라이언트도 조상-자손 쌍을 만든다.
//
// ②: 원장의 링크-워크트리 경로 93개 중 81개가 **이미 지워진** 워크트리다(랜딩 뒤
// 정리한다). git 은 살아 있는 것만 아므로 지워진 트리의 카드가 저장소 루트로 흡수돼
// ①의 거짓 양성이 그대로 재생산된다.
//
// ★ 울타리:
//   - 이것은 정체 판정도 겹침 판정도 **아니다. 보고다.** 어느 소비자도 이 결과로 두
//     카드를 같은 세션이라고 보지 않는다. 카드는 여전히 3중키로만 같다.
//   - **CCSessionID 가 같을 때만 본다.** 앞선 세션이 상하위 17건을 가짜 겹침으로 셌다가
//     "전부 다른 대화였다"로 정정한 사고가 있다. 그 17건은 cc 가 달라 여기 안 걸린다.
//   - **형제 트리는 안 건드린다.** 소유 루트가 다르면 아예 다른 묶음이 된다.
//   - **빈 cc 끼리는 같다고 보지 않는다.**
//   - **알려진 루트가 하나도 없으면 아무것도 보고하지 않는다**(카드 전부가 둘째
//     반환값으로 나간다).
func DetectUnnormalizedSplit(cards []SplitCard, worktreeRoots []string) ([]SplitReport, int) {
	seen := map[string]bool{}
	var roots []string
	add := func(p string) {
		if p = strings.TrimSpace(p); p == "" {
			return
		}
		p = filepath.Clean(p)
		if !seen[p] {
			seen[p] = true
			roots = append(roots, p)
		}
	}
	for _, r := range worktreeRoots {
		add(r)
	}
	// 관례로 알려진 루트를 카드 경로에서 되읽는다 — 지워진 워크트리를 덮는다.
	// ★ 경로 하나가 관례 루트를 **여럿** 품을 수 있다(워크트리 안에서 하네스가 또
	//   `.claude/worktrees/X` 를 만드는 배치). 첫 것만 담으면 안쪽 트리가 roots 에
	//   아예 안 들어가 owningRoot 의 최장 매칭이 복구할 수 없고, 정상 정규화된 루트
	//   둘이 한 보고로 묶인다. 전부 담는다.
	for _, c := range cards {
		for _, r := range conventionRoots(c.Worktree) {
			add(r)
		}
	}

	type key struct{ machine, cc, root string }
	groups := map[key]map[string][]string{} // (머신,cc,트리) → worktree 값 → 세션 id 들
	unattributed := 0
	for _, c := range cards {
		cc := strings.TrimSpace(c.CCSessionID)
		m := strings.TrimSpace(c.MachineID)
		wt := strings.TrimSpace(c.Worktree)
		// ★ 빈 cc 판정을 여기서 다시 쓰지 않는다 — SameConversation 이 "빈 값끼리는
		//   같지 않다"를 정의하는 한 자리이고, 그 판정이 두 자리에 살면 한쪽만 고칠 때
		//   조용히 어긋난다.
		if !SameConversation(cc, cc) || m == "" || wt == "" {
			continue // 3중키가 안 서는 카드다 — 판정 대상이 아니고 '버렸다'고 셀 것도 아니다
		}
		wt = filepath.Clean(wt)
		if wt == "." {
			// 3중키는 섰는데 경로가 아무 데도 안 가리킨다. **세어서 낸다** —
			// 조용히 넘기면 판정 대상이었던 카드가 흔적 없이 사라진다.
			unattributed++
			continue
		}
		root := owningRoot(wt, roots)
		if root == "" {
			unattributed++ // git 도 관례도 모르는 트리다. **세어서 낸다**
			continue
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
	return out, unattributed
}

// conventionRoots 는 경로 안에서 **관례로 알려진** 워크트리 루트를 전부 되읽는다. 순수 함수다.
//
// ★ 추측이 아니다. flightdeck 자신이 그 자리에 워크트리를 만든다 — `pick` 응답의
// "워크트리 준비" 절이 `git worktree add '.flightdeck/worktrees/<항목id>'` 를 출력한다.
// `.claude/worktrees/<이름>` 도 같은 부류(하네스가 만드는 자리)다. 이 제품이 스스로
// 지키는 불변식이라 경로에서 되읽을 수 있다.
//
// ★ 이것이 없으면 **지워진 워크트리**의 카드가 저장소 루트로 흡수된다. 실측에서
// 원장의 링크-워크트리 경로 93개 중 81개가 이미 지워진 것이었고, 그 상태로는
// 보고의 84%가 거짓 양성이었다.
//
// ★ **전부** 낸다. 하나만 내면(첫 매치에서 멈추면) 중첩 배치
// `<repo>/.flightdeck/worktrees/A/.claude/worktrees/B` 에서 안쪽 트리 B 가 루트 목록에
// 아예 안 들어가고, 그러면 정상 정규화된 두 루트가 한 보고로 묶인다(거짓 양성).
// 오늘 원장에 그 배치는 0건이지만, 워크트리 안에서 도는 세션에 하네스가 자기 워크트리를
// 만들면 바로 생긴다.
//
// ★ 이 관례가 **거짓 음성** 쪽으로 틀린다는 것을 알고 쓴다. `worktrees/X` 모양인데
// 실제로는 워크트리가 아닌 디렉토리면 그 카드가 자기만의 트리로 빠져나가 진짜 갈림이
// 안 보고된다. 거짓 양성으로 두 번(56%·84%) 신뢰를 잃은 축이라 이 방향을 택했다.
//
// 못 찾으면 nil 이다.
func conventionRoots(p string) []string {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil
	}
	segs := strings.Split(filepath.ToSlash(filepath.Clean(p)), "/")
	var out []string
	for i := 0; i+2 < len(segs); i++ {
		if segs[i] != ".flightdeck" && segs[i] != ".claude" {
			continue
		}
		if segs[i+1] != "worktrees" {
			continue
		}
		out = append(out, filepath.FromSlash(strings.Join(segs[:i+3], "/")))
	}
	return out
}

// CarriesWorktreePrefix 는 **이미 상대화된 경로**가 관례 워크트리 루트를 성분으로
// 이고 있는지다. 발자국의 포함 축이 이것으로 트리 밖을 한 겹 더 가른다.
//
// ★ **상대경로에만 쓴다.** 절대경로에 쓰면 워크트리 안에서 도는 세션의 경로가 전부
// 걸린다 — 실측상 observed 발자국 1296건 중 1135건(87%)이 그런 세션의 것이다.
// 판정의 전부는 "카드의 기준 트리로 상대화한 **뒤에도** 접두가 남았는가"이고,
// 남았다는 것은 그 카드가 자기 트리 밖(물리적으로는 자손인 다른 git 트리)을 만졌다는 뜻이다.
//
// ★ 왜 filepath.Rel 만으로 부족한가. Rel 은 **파일시스템 포함**을 재는데 링크 워크트리는
// `<repo>/.flightdeck/worktrees/<id>` 라 저장소 루트의 물리적 자손이다. git 은 그것을
// 자기 것으로 안 본다(`.git/info/exclude`). 두 포함 개념이 갈리는 틈이 이 함수의 존재 이유다.
func CarriesWorktreePrefix(rel string) bool { return len(conventionRoots(rel)) > 0 }

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
