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

	var present, absent, unknown int
	for _, p := range in.Paths {
		switch in.Here[p] {
		case PathPresent:
			present++
		case PathAbsent:
			absent++
		default:
			unknown++
		}
	}

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
		v.Kind = KindUnknown
		v.Summary = fmt.Sprintf("%d개 중 %d개를 못 읽었다 — '없다'가 아니다. 이 축은 판정하지 않았다.",
			len(in.Paths), unknown)
	default:
		v = classifyAllAbsent(in, v, absent)
	}

	v.Summary += unreadableSuffix(in.Unreadable)
	return v
}

// classifyAllAbsent 는 "이 프로젝트에서 전부 없다"가 관측된 뒤의 세 갈래다.
func classifyAllAbsent(in ItemPathInput, v ItemPathVerdict, absent int) ItemPathVerdict {
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
