package judge

import (
	"fmt"
	"sort"

	"github.com/kweiza/flightdeck/internal/model"
)

// 탈락 사유 코드. pick_eval.rejected 의 reason_code 로 그대로 저장된다.
//
// 선행 조건(after-*)의 사유 코드는 **AfterSatisfied 가 낸 것을 그대로 나른다**.
// 여기서 `after-blocked` 같은 상위 코드로 접으면 "기다리면 풀림"과 "영영 안 풀림"이
// 분포에서 한 칸이 되고, 그 순간 이 표가 무엇을 고쳐야 하는지 말하지 못한다.
const (
	RejectClaimed       = "claimed"         // 남이 선점했다
	RejectClaimedBySelf = "claimed-by-self" // 내가 이미 선점했다 — 새로 집을 것이 아니라 재개 경로다
	RejectResourceHeld  = "resource-held"   // 필요한 자원을 남이 쥐고 있다
	RejectClosed        = "closed"          // open 이 아니다

	// RejectNotTop 은 **거르는 축이 아니다.** 적격이었으나 추천 1건에 못 든 항목이다.
	// 원장을 완결시키기 위해 둔다 — 이것이 없으면 적격 후보가 여럿일 때
	// 추천되지 않은 나머지가 pick_eval 어디에도 안 남아 조용히 사라진다.
	// 불변식: 모든 후보는 picked 이거나 rejected 에 최소 한 줄이 있다.
	// 탈락 사유 분포를 셀 때는 이 코드를 빼고 센다.
	RejectNotTop = "not-top"
)

// Candidate 는 큐 항목 하나와, 그 항목에 대해 서버가 이미 알고 있는 파생 사실이다.
type Candidate struct {
	Item       model.Item
	ClaimedBy  string // 선점한 세션 id. 빈 문자열이면 미선점
	Dependents int    // item_dependents 의 역인덱스 값. 많을수록 이걸 풀어야 남이 움직인다

	// Needs 는 이 항목을 하려면 잡아야 하는 자원이다(.flightdeck.yaml 의 resources).
	// model.Item 에 이 축이 없어 여기에 둔다 — labels 로 대신할 수 없다.
	// labels 는 표시 전용이고 **어떤 배제 판정에도 안 쓴다**(설계 §5).
	Needs []string

	// Overlaps 는 **탈락 사유가 아니다.** 이 항목의 경로가 살아 있는 다른 세션과 겹친다는
	// 사실을 알리기만 한다(설계 §5: "거르지 않고 알린다").
	//
	// 실측상 실제 텍스트 충돌은 8일에 1건이라 배제는 과잉이다. 반대로 침묵하면
	// "겹침 없음"과 "이 축을 아예 안 본다"가 구분되지 않는다 — 그 구분이 안 되는 도구는
	// 두 번째 세션부터 무시된다.
	//
	// Eligible 이 돌려주는 picked 에만 채워진다(입력으로 준 값은 무시하고 다시 계산한다).
	Overlaps []Overlap
}

// Overlap 은 상대 세션 하나와 겹친 경로 쌍들이다.
// 세션 id 만으로는 "무엇이 겹쳤나"를 말할 수 없어 쌍을 함께 나른다.
type Overlap struct {
	SessionID string      // 상대 세션 id
	Label     string      // 표시 전용. 판정에 안 쓴다
	Pairs     [][2]string // [0]=이 항목의 경로, [1]=상대 세션의 경로
}

// LiveSession 은 지금 살아 있는 세션의 경로 발자국이다.
// Paths 는 footprint ∪ change_set — 착수 직후 구간은 브랜치 diff 가 정의상 비어 있어
// footprint 가 그 구간을 덮는다.
type LiveSession struct {
	ID    string
	Label string
	Paths []string
	// CCSessionID 는 이 카드가 속한 **대화**의 id 다.
	//
	// ★ 카드 id 로는 "이게 나인가"를 못 가른다. 정체가 3중키(머신·워크트리·cc)라
	// 한 대화가 카드 여러 장이 될 수 있고(cc 표류·워크트리 갈림), 그때 카드 id 는 다르지만
	// 대화는 같다. 그 상태에서 카드 id 만 비교하면 **세션이 자기 자신과 조율하라는 처방**이 뜬다.
	// 실측(2026-08-05): overlap 발화 32건 중 5건이 그것이었다.
	//
	// 비어 있을 수 있다(관측이 실패한 카드). 빈 값끼리는 **같다고 보지 않는다** —
	// 못 읽은 둘을 같은 대화로 접으면 진짜 겹침이 조용히 사라진다.
	CCSessionID string
}

type EligibleInput struct {
	Self          string // 판정을 요청한 세션 id
	Candidates    []Candidate
	Live          []LiveSession
	Facts         AfterFacts
	HeldResources map[string]string // 자원 -> 점유 세션 id. 비어 있으면 아무도 안 쥠
}

// Eligible 은 지금 집을 수 있는 항목 하나를 고르고, **고르지 않은 전부의 사유**를 돌려준다.
//
// 사유가 없으면 큐는 블랙박스가 되고, 블랙박스는 두 번째 세션부터 무시된다.
// 그래서 불변식은 이것이다: **모든 후보는 picked 이거나 rejected 에 최소 한 줄이 있다.**
// 조용히 버리는 것이 하나도 없다. 한 후보가 여러 축에서 걸리면 사유도 여러 줄이 나온다
// (축을 뭉개지 않는다).
//
// 정렬은 의존자 수 많은 것 → 오래된 것 → id 사전순이다. 마지막 축은 동점 처리이고,
// 없으면 같은 입력에 다른 답이 나올 수 있다(입력 순서에 의존하게 된다).
func Eligible(in EligibleInput) (picked *Candidate, rejected []model.Rejection) {
	var fit []Candidate
	for _, c := range in.Candidates {
		rs := rejectionsFor(c, in)
		if len(rs) == 0 {
			fit = append(fit, c)
			continue
		}
		rejected = append(rejected, rs...)
	}
	if len(fit) == 0 {
		return nil, rejected
	}

	sort.SliceStable(fit, func(i, j int) bool { return lessCandidate(fit[i], fit[j]) })

	// 적격이었으나 1순위가 아닌 것도 원장에 남긴다. 안 남기면 그 후보들이
	// pick_eval 어디에도 없어 "왜 저것이 아니라 이것인가"에 답할 수 없다.
	for i, c := range fit[1:] {
		rejected = append(rejected, model.Rejection{Item: c.Item.ID, Reason: RejectNotTop,
			Detail: fmt.Sprintf("적격이지만 추천 %d순위다(추천은 %s)", i+2, fit[0].Item.ID)})
	}

	// 값 복사본을 만들어 겹침을 채운다. 입력 슬라이스를 건드리지 않는다 —
	// 순수 함수가 인자를 고치면 시험이 보는 것과 호출자가 보는 것이 갈라진다.
	best := fit[0]
	best.Overlaps = OverlapsWithLive(best.Item.Paths, in.Live, in.Self)
	return &best, rejected
}

// rejectionsFor 은 후보 하나가 걸리는 축을 전부 낸다. 빈 슬라이스면 적격이다.
//
// 축의 순서는 고정이다(종료 → 선점 → 선행 → 자원). 같은 입력에 같은 사유 순서여야
// pick_eval 로 쌓인 분포를 시점 간에 비교할 수 있다.
func rejectionsFor(c Candidate, in EligibleInput) []model.Rejection {
	id := c.Item.ID
	var out []model.Rejection

	// ① 종료. **선점보다 먼저 본다.**
	//
	//    앞선 판은 선점을 먼저 봤고, 그 근거("선점된 항목은 state 가 claimed 라
	//    순서를 뒤집으면 엉뚱하게 closed 가 나간다")는 state='claimed' 한 경우에만 참이었다.
	//    done·dropped 항목에 살아 있는 자기 선점이 붙어 있으면 claimed-by-self 가 먼저 나가
	//    "끝났다"가 "재개해라"로 뒤바뀌고, 그러면 §10 의 탈락 사유 분포가 거짓이 된다.
	//    그래서 종료 상태만 먼저 걸러 내고, claimed 는 아래 선점 축이 계속 맡는다.
	if c.Item.State == model.ItemDone || c.Item.State == model.ItemDropped {
		return []model.Rejection{{Item: id, Reason: RejectClosed,
			Detail: fmt.Sprintf("state=%s", c.Item.State)}}
	}

	// ② 선점. claimed 상태는 여기가 맡는다 — 위에서 걸렀으면 "closed" 라는 엉뚱한 사유가 나간다.
	if c.ClaimedBy != "" {
		if c.ClaimedBy == in.Self {
			// 남이 집은 것과 처방이 정반대다: 이건 회피가 아니라 **재개**해야 하는 항목이다.
			// 한 코드로 뭉개면 "남이 다 집었다"는 분포가 거짓이 된다.
			return []model.Rejection{{Item: id, Reason: RejectClaimedBySelf,
				Detail: "이미 내가 선점했다 — 새로 집을 것이 아니라 맥락을 다시 내면 된다"}}
		}
		return []model.Rejection{{Item: id, Reason: RejectClaimed,
			Detail: fmt.Sprintf("세션 %s 가 선점했다", c.ClaimedBy)}}
	}

	// ③ 선점 행 없이 claimed 로 남은 항목. 그 자체가 정합성 결함이므로 상태를 상세에 그대로 싣는다.
	if c.Item.State != model.ItemOpen {
		return []model.Rejection{{Item: id, Reason: RejectClosed,
			Detail: fmt.Sprintf("state=%s", c.Item.State)}}
	}

	// ④ 선행 조건. AfterSatisfied 의 사유 코드를 그대로 나른다.
	if ok, reasons := AfterSatisfied(c.Item.After, in.Facts); !ok {
		for _, r := range reasons {
			code, detail := SplitReason(r)
			out = append(out, model.Rejection{Item: id, Reason: code, Detail: detail})
		}
	}

	// ⑤ 자원. Needs 순서대로 본다 — 맵을 순회하면 같은 입력에 사유 순서가 흔들린다.
	for _, res := range c.Needs {
		holder, held := in.HeldResources[res]
		if !held || holder == "" || holder == in.Self {
			continue // 내가 쥔 자원은 나를 막지 않는다
		}
		out = append(out, model.Rejection{Item: id, Reason: RejectResourceHeld,
			Detail: fmt.Sprintf("자원 %s 를 세션 %s 가 쥐고 있다", res, holder)})
	}

	return out
}

// lessCandidate 는 추천 순서다. 의존자 수 많은 것 → 오래된 것 → id 사전순.
//
// 순수 함수로 빼 둔 이유는 시험이 정렬 규칙을 **직접** 부를 수 있게 하기 위해서다.
// 정렬이 Eligible 본문에 있으면 시험이 그 규칙의 사본을 단정하게 된다.
func lessCandidate(a, b Candidate) bool {
	if a.Dependents != b.Dependents {
		return a.Dependents > b.Dependents
	}
	if !a.Item.CreatedAt.Equal(b.Item.CreatedAt) {
		return a.Item.CreatedAt.Before(b.Item.CreatedAt)
	}
	return a.Item.ID < b.Item.ID
}

// OverlapsWithLive 는 경로 집합이 살아 있는 세션들과 겹치는 것을 전부 모은다.
//
// self 는 건너뛴다 — 자기 발자국과 겹치는 것은 알림거리가 아니다.
// 알림거리로 세면 착수 직후(자기 footprint 가 이미 그 경로를 담은 시점)마다
// 자기 자신과 겹친다는 경고가 나오고, 상시 점등된 경고는 판별력이 0이 된다.
func OverlapsWithLive(paths []string, live []LiveSession, self string) []Overlap {
	var out []Overlap
	for _, s := range live {
		if s.ID == self {
			continue
		}
		pairs := OverlapPairs(paths, s.Paths)
		if len(pairs) == 0 {
			continue
		}
		out = append(out, Overlap{SessionID: s.ID, Label: s.Label, Pairs: pairs})
	}
	return out
}
