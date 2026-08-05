package judge

import (
	"fmt"
	"sort"
	"strings"
)

// 항목이 선언한 경로가 그 프로젝트에 실재하는가 — 그 판정이다.
//
// 이 판정이 있는 이유는 실물 사고다: 항목 10건이 남의 프로젝트에 등록돼 그 프로젝트에는
// 존재하지도 않는 경로를 가리켰고, 그중 하나는 id 가 전역 유일이라 회수되지 않아
// 그 이름이 영구히 죽었다. add 응답이 등록 시점을 막았고(b315980), 이것은 **두 번째 그물**이다.
//
// ★ 규칙의 핵심은 "없다"가 아니라 "저기 있다"이고, 그 '저기'가 **하나로 지목될 때만**이다.
// 정답이 있는 데이터(오등록 10건)로 채점한 결과 두 규칙 다 9건을 잡는데,
// 유일 지목 조건이 없으면 지금 큐에서 5건을 헛발화한다 — 전부 `docs/` 처럼
// 어디에나 있는 이름이라 세 프로젝트를 동시에 가리킨 경우였다.
// 세 곳을 동시에 가리키는 것은 지목이 아니라 잡음이다.

// PathPresence 는 경로 하나를 한 저장소에서 찾아본 결과다. 셋을 가른다.
//
// ★ 0값이 PathUnknown 이다. "못 봤다"가 "없다"로 접히면 이 기능 전체가 거짓말이 된다 —
// 관측하지 않은 경로를 근거로 남의 항목을 오등록이라 고발하게 된다.
// 같은 논증이 diskFreePct 에도 있다(못 재면 0이 아니라 오류를 낸다).
type PathPresence int

const (
	PathUnknown PathPresence = iota // 못 봤다 — 판정 근거로 쓸 수 없다
	PathAbsent                      // 봤고, 없다
	PathPresent                     // 봤고, 있다
)

// Kind 는 진단 여섯 갈래다.
type Kind string

const (
	KindNoPaths       Kind = "no-paths"      // 항목에 경로가 0개다 — 판정할 재료가 없다
	KindOK            Kind = "ok"            // 한 경로라도 여기 있다
	KindMisregistered Kind = "misregistered" // 여기 전부 없고, 다른 프로젝트 하나가 유일하게 지목된다
	KindAmbiguous     Kind = "ambiguous"     // 여기 전부 없는데 여럿이 지목된다 — 지목이 아니다
	KindNowhere       Kind = "nowhere"       // 어디에도 없다 — 경로가 틀렸거나 레포가 미등록이다
	KindUnknown       Kind = "unknown"       // 못 읽었다 — "없다"가 아니다
)

// ItemPathInput 은 판정에 필요한 **관측 결과**다. 이 구조체는 파일시스템을 모른다.
type ItemPathInput struct {
	Project    string
	Paths      []string
	Here       map[string]PathPresence            // 경로 → 이 프로젝트에서 본 결과
	Elsewhere  map[string]map[string]PathPresence // 프로젝트 → 경로 → 결과
	Unreadable []string                           // 아예 못 연 프로젝트
	// UnknownReason 은 Here 에서 PathUnknown 이 난 경로 → **왜** 못 읽었는지다.
	//
	// ★ 왜 별도 맵인가. PathUnknown 하나로 원인 셋이 모인다 — 프로젝트 루트가 통째로 없다 ·
	// 경로 토큰이 ".." 로 루트 밖에 나간다 · ErrNotExist 가 아닌 stat 오류(EACCES 등).
	// 셋은 **고칠 사람과 고칠 자리가 전부 다른데** 문장이 바이트 단위로 같았다.
	// 관측(service)은 errno 를 손에 쥐고 있었지만 넘길 통로가 없어 그 자리에서 버렸다.
	//
	// ★ PathPresence 를 구조체로 승격하지 않은 이유: 그 타입의 0값이 PathUnknown 이어야
	// 한다는 안전장치가 거기 걸려 있다("못 봤다"가 "없다"로 접히면 이 기능 전체가 거짓말이 된다).
	// 사유는 문장을 만들 때만 읽히는 곁가지라 판정 축을 건드릴 값어치가 없다.
	//
	// ★ Elsewhere 쪽은 안 담는다. 남의 프로젝트를 못 읽은 사실은 Unreadable 이 이미 나르고,
	// 그 축의 문장은 "지목이 그만큼 약하다"라 원인별로 갈릴 이유가 없다.
	UnknownReason map[string]string
}

// ItemPathVerdict 는 판정 하나다.
//
// ★ json 태그를 반드시 단다. 이 값은 REST 를 왕복한다(PickResult 에 실려 나갔다 돌아온다).
// 이 패키지 안에 판례가 갈려 있어서(prescribe.go 는 달고 eligible.go 는 안 단다)
// 안 달면 "Kind"·"Summary" 같은 대문자 키가 나가고, 그 모양이 굳으면 되돌릴 수 없다.
type ItemPathVerdict struct {
	Kind       Kind     `json:"kind"`
	Suggest    string   `json:"suggest,omitempty"`    // 유일 지목일 때 그 프로젝트 id
	Candidates []string `json:"candidates,omitempty"` // 여럿이 지목될 때 전부(정렬)
	Unreadable []string `json:"unreadable,omitempty"` // 판정 근거가 그만큼 약하다
	Summary    string   `json:"summary"`              // 한 줄. ★ 항상 채운다
}

// ClassifyItemPaths 는 관측 결과를 진단으로 옮긴다. 순수 함수다.
//
// **판정 순서가 곧 우선순위다**: no-paths → ok → unknown → 나머지 셋.
//
//   - ok 가 unknown 보다 앞인 이유: 한 경로라도 여기 **실재하는 것을 봤으면**
//     다른 경로를 못 읽었어도 "이 항목은 여기 앵커돼 있다"는 결론이 안 흔들린다.
//   - unknown 이 남은 셋보다 앞인 이유: 그 셋은 전부 "여기 없다"를 **전제**하는데,
//     PathUnknown 이 섞여 있으면 그 전제 자체가 관측되지 않은 것이다.
//     못 읽은 경로 하나를 Absent 로 접으면 정확히 이 기능이 없애려는 종류의 거짓말이 된다.
func ClassifyItemPaths(in ItemPathInput) ItemPathVerdict {
	v := ItemPathVerdict{Unreadable: in.Unreadable}

	if len(in.Paths) == 0 {
		v.Kind = KindNoPaths
		v.Summary = "경로 0 — 이 항목은 겹침 축에 안 잡힌다. 아무도 안 막고, 아무도 이 항목을 못 피한다."
		return v
	}

	var present, absent int
	var unknownPaths []string
	for _, p := range in.Paths {
		switch in.Here[p] {
		case PathPresent:
			present++
		case PathAbsent:
			absent++
		default:
			unknownPaths = append(unknownPaths, p)
		}
	}
	unknown := len(unknownPaths)

	switch {
	case present > 0:
		v.Kind = KindOK
		v.Summary = fmt.Sprintf("%d개 중 %d개가 이 프로젝트(%s)에 있다.", len(in.Paths), present, in.Project)
		if unknown > 0 {
			v.Summary += fmt.Sprintf(" %d개는 못 읽었다.", unknown)
		}
	case unknown > 0:
		// ★ 여기가 이 함수에서 가장 중요한 분기다. 남은 것이 전부 Absent 여도
		//   못 읽은 것이 하나라도 있으면 오등록이라 말하지 않는다.
		//
		// ★ 어느 경로를 못 읽었는지, 그리고 **왜** 못 읽었는지 문장에 낸다. 원인이 셋
		//   (루트 통째 없음 · ".." 로 루트 밖 · 권한 거부) 다 여기로 모이는데, 그것이 없으면
		//   세 경우가 바이트 단위로 같은 문장이 되어 운영자가 무엇이 진짜 고장인지 못 가린다(D2).
		v.Kind = KindUnknown
		v.Summary = fmt.Sprintf("%d개 중 %d개를 못 읽었다 — '없다'가 아니다: %s. 이 축은 판정하지 않았다.",
			len(in.Paths), unknown, unknownDetail(unknownPaths, in.UnknownReason))
	default:
		v = classifyAllAbsent(in, v, absent)
	}

	v.Summary += unreadableSuffix(in.Unreadable)
	return v
}

// classifyAllAbsent 는 "이 프로젝트에서 전부 없다"가 관측된 뒤의 세 갈래다.
//
// ★ in.Elsewhere == nil 은 네 번째 상태다. service 쪽 계약은 "목록 조회 실패 → nil,
// 성공(다른 프로젝트가 0개여도) → map{}"이다(internal/service/itempaths.go). 이 구분을
// 무시하고 nil 맵을 그냥 순회하면 hits 는 항상 0건이 되어 KindNowhere 로 빠진다 —
// 남의 프로젝트를 하나도 관측하지 않고 "등록된 어느 프로젝트에도 없다"를 단정하는
// 붕괴다(D1: SQLite 가 한 번 튀면 경로가 멀쩡한 항목이 "경로가 틀렸다"로 진단된다).
func classifyAllAbsent(in ItemPathInput, v ItemPathVerdict, absent int) ItemPathVerdict {
	if in.Elsewhere == nil {
		v.Kind = KindUnknown
		v.Summary = fmt.Sprintf(
			"%d개 전부 이 프로젝트(%s)에 없다 — 하지만 다른 프로젝트를 하나도 못 봤다. 이 축은 판정하지 않았다.",
			absent, in.Project)
		return v
	}

	hits := make([]string, 0, len(in.Elsewhere))
	for proj, m := range in.Elsewhere {
		for _, pres := range m {
			if pres == PathPresent {
				hits = append(hits, proj)
				break
			}
		}
	}
	sort.Strings(hits) // 지목이 여럿일 때 순서가 흔들리면 같은 사실이 다른 문장이 된다

	switch len(hits) {
	case 0:
		v.Kind = KindNowhere
		v.Summary = fmt.Sprintf(
			"%d개 전부 이 프로젝트(%s)에 없고 등록된 어느 프로젝트에도 없다. "+
				"경로가 틀렸거나(뿌리가 잘렸을 수 있다) 그 레포가 아직 미등록이다. "+
				"지금 이 항목은 겹침 축에서 아무도 안 막는다.", absent, in.Project)
	case 1:
		v.Kind, v.Suggest = KindMisregistered, hits[0]
		v.Summary = fmt.Sprintf(
			"%d개 전부 이 프로젝트(%s)에 없다 — %s 에는 있다. 오등록일 수 있다.",
			absent, in.Project, hits[0])
	default:
		v.Kind, v.Candidates = KindAmbiguous, hits
		v.Summary = fmt.Sprintf(
			"%d개 전부 이 프로젝트(%s)에 없다. 등록된 다른 %d개 프로젝트(%s)에도 같은 이름이 있어 "+
				"어느 하나를 지목하지 못한다 — 근거로 쓰지 않는다.",
			absent, in.Project, len(hits), strings.Join(hits, ", "))
	}
	return v
}

// unknownDetail 은 "무엇을 왜 못 읽었나"를 만든다. 순수 함수다.
//
// ★ **원인이 하나면 경로를 나열하지 않고 원인을 한 번만 말한다.** 이것이 이 함수의 요점이다.
// 실패 시나리오가 정확히 그 모양이기 때문이다 — 어느 프로젝트의 등록 경로가 옮겨지면
// 그 프로젝트의 **모든** 경로가 같은 이유로 unknown 이 되고, 그때 경로를 셋씩 나열하면
// 화면은 **경로**를 지목하는데 실제 고장은 **레포**다. 운영자가 좌표를 못 받고
// 엉뚱한 것을 고치러 간다.
//
// 원인이 갈릴 때만 경로별로 낸다 — 그때는 경로가 진짜 판별자다.
// 사유를 하나도 못 받았으면(관측 계층이 안 채웠으면) 옛 거동인 경로 나열로 떨어진다.
// 지어내지 않는다 — 사유 없음을 "원인 불명"이라 쓰면 그것도 거짓말이다.
func unknownDetail(paths []string, reason map[string]string) string {
	if len(reason) == 0 {
		return strings.Join(clipList(paths, 3), ", ")
	}
	shared, uniform := "", true
	for _, p := range paths {
		r := reason[p]
		if r == "" {
			uniform = false
			break
		}
		if shared == "" {
			shared = r
		} else if shared != r {
			uniform = false
			break
		}
	}
	if uniform && shared != "" {
		if len(paths) == 1 {
			return paths[0] + " (" + shared + ")"
		}
		return fmt.Sprintf("%d개 전부 같은 이유다 — %s", len(paths), shared)
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if r := reason[p]; r != "" {
			out = append(out, p+" ("+r+")")
			continue
		}
		out = append(out, p)
	}
	return strings.Join(clipList(out, 3), ", ")
}

// unreadableSuffix 는 못 연 프로젝트를 문장 끝에 붙인다.
// 숨기면 "지목이 유일하다"가 실제보다 강해 보인다 — 못 본 프로젝트가 같은 경로를
// 갖고 있었을 수 있고, 그러면 유일 지목이 아니라 모호였다.
func unreadableSuffix(un []string) string {
	if len(un) == 0 {
		return ""
	}
	s := append([]string(nil), un...)
	sort.Strings(s)
	return fmt.Sprintf(" (등록 프로젝트 %d개를 못 읽었다: %s — 지목이 그만큼 약하다)",
		len(s), strings.Join(s, ", "))
}
