package judge

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 묶음 판정 — pick 이 함께 갈 항목을 고르는 자리.
//
// 이 파일의 함수는 전부 순수 함수다. 그리고 **기존 판정을 하나도 안 고친다** —
// Eligible·PathsOverlap·lessCandidate 는 다른 질문에 답하고 있고,
// 같은 함수를 두 질문에 쓰면 한쪽을 고칠 때 다른 쪽이 조용히 바뀐다.

// SamePaths 는 두 경로 집합에서 **정확히 같은** 토큰을 낸다. 순수 함수다.
//
// ★ PathsOverlap 을 안 쓰는 것이 이 함수의 존재 이유 전부다.
// PathsOverlap 은 조상 디렉토리도 겹침으로 센다(paths.go:27). 그 규칙은 그 함수의
// 소비자("남의 세션과 부딪히나")에게는 옳지만 여기서는 무너진다 —
// 실측에서 `plugins/flightdeck/server/cmd/fd` 를 디렉토리 통째로 선언한 항목 하나가
// 열린 16건 중 10건을 한 묶음으로 끌어왔다(설계 §0.1).
//
// 돌려주는 표기는 **a 쪽 원문**이다. 정규화된 문자열을 돌려주면 화면에 뜨는 경로가
// 항목이 선언한 것과 달라져, 사람이 "내가 적은 그 줄"을 못 찾는다.
func SamePaths(a, b []string) []string {
	norm := make(map[string]bool, len(b))
	for _, y := range b {
		if n := normPath(y); n != "" {
			norm[n] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, x := range a {
		n := normPath(x)
		if n == "" || seen[n] || !norm[n] {
			continue
		}
		seen[n] = true
		out = append(out, x)
	}
	return out
}

// normPath 는 경로를 비교용 정규형으로 만든다.
// components 를 그대로 쓴다 — 성분 규칙이 두 벌이 되면 두 축이 조용히 표류한다.
func normPath(p string) string { return strings.Join(components(p), "/") }

// BundleAxis 는 두 항목이 왜 함께 갈 만한가다.
type BundleAxis string

const (
	AxisSibling BundleAxis = "sibling" // 같은 판단에 함께 매달렸다
	AxisAfter   BundleAxis = "after"   // 같은 선행을 기다렸다 / 선행이 선두다
	AxisPaths   BundleAxis = "paths"   // 선언 경로가 정확히 같다 — 보강 전용
)

// Link 는 선두와 이웃 하나 사이의 관계 **전부**다.
//
// ★ 축을 뭉개지 않는다. 뭉개면 "셋 다 맞는 쌍"과 "형제이기만 한 쌍"이 화면에서 같아지고,
// 그러면 사람이 추천을 신뢰할지 판단할 근거를 잃는다.
type Link struct {
	Item   string       // 이웃 항목 id
	Axes   []BundleAxis // 고정 순서: sibling → after → paths
	Detail string       // 무엇이 근거인가 — 판단 id · 선행 좌표 · 겹친 경로
}

// SiblingIndex 는 항목 id → 그 항목에 걸린 판단 id 목록이다.
//
// ★ 슬라이스이고 **사전순으로 정렬돼 있어야 한다**(조립은 service 가 한다).
// 맵으로 두면 공유 판단이 여럿일 때 어느 것이 근거로 찍힐지가 순회 순서에 달리고,
// 그러면 같은 입력에 다른 응답이 나온다 — 재개가 재출력이 아니게 되는 그 결함이다.
type SiblingIndex map[string][]string

// shared 는 두 항목이 함께 매달린 판단 중 **사전순 첫째**를 낸다.
func (x SiblingIndex) shared(a, b string) (string, bool) {
	bs := make(map[string]bool, len(x[b]))
	for _, j := range x[b] {
		bs[j] = true
	}
	for _, j := range x[a] { // a 의 목록이 사전순이므로 결과가 고정된다
		if bs[j] {
			return j, true
		}
	}
	return "", false
}

// afterKey 는 선행 집합의 정규형이다. 순서에 안 흔들린다.
// 빈 문자열은 "선행이 없다"이고, 그것끼리는 같다고 세지 않는다 —
// 선행 없는 항목이 큐의 다수라 그걸 축으로 세면 전부가 서로 묶인다.
func afterKey(as []model.After) string {
	parts := make([]string, 0, len(as))
	for _, a := range as {
		switch {
		case a.Item != "":
			parts = append(parts, "item:"+a.Item)
		case a.SHA != "":
			parts = append(parts, "sha:"+a.SHA)
		case a.Job != "":
			parts = append(parts, "job:"+a.Job)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// LinkOf 는 선두와 이웃 하나의 관계를 낸다. 무관하면 nil 이다.
//
// ★ 결합 규칙: **링크는 (형제 ∨ 같은 선행) 일 때만 선다.**
// 경로 일치는 이미 선 링크의 근거에 덧붙을 뿐 링크를 만들지 못한다.
// 경로 단독을 허용하면 DESIGN.md 처럼 모두가 만지는 파일 하나가 큐를 통째로 묶는다
// (실측: 그 파일 하나로 4건이 서로 묶였다 — 설계 §0.1).
func LinkOf(lead, other Candidate, sib SiblingIndex) *Link {
	l := Link{Item: other.Item.ID}
	var why []string

	if j, ok := sib.shared(lead.Item.ID, other.Item.ID); ok {
		l.Axes = append(l.Axes, AxisSibling)
		why = append(why, "판단 "+j+" 가 둘을 함께 가리킨다")
	}
	if k := afterKey(lead.Item.After); k != "" && k == afterKey(other.Item.After) {
		l.Axes = append(l.Axes, AxisAfter)
		why = append(why, "선행이 같다("+k+")")
	}
	if len(l.Axes) == 0 {
		return nil // 경로는 보강 전용이다 — 여기서 끝낸다
	}
	if same := SamePaths(lead.Item.Paths, other.Item.Paths); len(same) > 0 {
		l.Axes = append(l.Axes, AxisPaths)
		why = append(why, "같은 경로 "+strings.Join(same, ", "))
	}
	l.Detail = strings.Join(why, " · ")
	return &l
}

// Bundle 은 pick 한 번이 제안하는 집합이다. **저장되지 않는다** —
// 테이블도 id 도 상태도 없다. 저장하면 개념이 하나 늘고,
// 그 순간 "묶음이 깨졌다"·"묶음을 해체한다" 같은 상태 전이가 따라온다.
type Bundle struct {
	Lead    Candidate
	Members []Candidate // 선두 제외. lessCandidate 로 정렬
	Links   []Link      // Members 와 같은 순서·같은 길이
	// Dependents 는 구성원 전부의 합이다. 이걸 풀어야 남이 움직이는 정도.
	Dependents int
	// Oldest 는 가장 오래된 구성원의 생성 시각이다.
	Oldest time.Time
	// Starved 는 이 묶음의 최고령이 StarvationAge 를 넘겼다는 사실이다.
	//
	// EligibleInput.Now 가 zero 면 **언제나 false** 다 — 판정을 안 돌린 것과
	// "안 굶었다"가 같은 값으로 접히지만, 여기서는 그 접힘이 안전한 쪽이다
	// (안 굶은 것으로 보면 기존 순서가 그대로 산다).
	Starved bool
	// Reason 은 네 키의 **실제 값**이다. 감추면 "왜 하필 이 브랜치 이름인가"에
	// 답할 수 없고, 답 못 하는 자동 선택은 두 번째 세션부터 무시된다.
	Reason string
}

// StarvationAge 는 묶음 크기를 이기기 시작하는 나이다.
//
// ★ 임의의 값이 아니다. 리드타임 실측(kweiza-cc-plugins · done 81건 · created→closed)이
// 정한다:
//
//	중앙값 3.4h · 평균 6.7h · p90 16.3h · 최대 42.2h
//
// 24h 는 p90(16.3h) 바깥이라 **정상 작업이 안 걸린다.** 이 값을 p90 아래로 내리면
// 평시 항목이 줄줄이 기아로 판정돼 묶음 기능이 사실상 죽는다 — 그때는 기아 축이
// 예외가 아니라 새 기본값이 되고, 이 상수를 넣은 이유가 사라진다.
//
// "하루가 지나도 아무도 안 집었다"가 사람에게 설명 가능한 문장이라는 것도 값의
// 일부다. 원장이 낸 순위를 사람이 못 읽으면 두 번째 세션부터 무시된다.
const StarvationAge = 24 * time.Hour

// EligibleBundle 은 Eligible 위에 얹는다.
//
// 적격 후보 **각각을 선두로** 놓고 방사형으로 이웃을 붙인 뒤 §2.4 의 키 넷으로 정렬해
// 1순위를 낸다. **전이하지 않는다** — 이웃의 이웃은 안 들어온다.
//
// Eligible 을 안 고치고 그 위에 얹는 이유는, 시험이 단일 추천 규칙을 독립으로
// 계속 부를 수 있어야 하기 때문이다. 묶음 판정이 그 규칙의 사본을 만들면
// 두 규칙이 조용히 표류한다.
func EligibleBundle(in EligibleInput, sib SiblingIndex) (*Bundle, []model.Rejection) {
	var fit []Candidate
	byID := make(map[string]Candidate, len(in.Candidates))
	rejByItem := make(map[string][]model.Rejection, len(in.Candidates))
	order := make([]string, 0, len(in.Candidates))

	// ★ 이 아래 세 맵·슬라이스는 in.Candidates 안에서 Item.ID 가 유일하다고 가정한다
	// (store 의 item.id 는 기본키다). 어겨지면 조용히 죽지 않고 뚜렷하게 어긋난다 —
	// order 에는 그 id 가 두 번 남지만 rejByItem[id] 는 마지막에 쓴 값으로 **덮인다**
	// (append 가 아니라 대입). flatten 은 order 를 그대로 두 번 훑으므로 나중 후보의
	// 사유를 두 번 내고 먼저 후보의 사유는 아예 안 낸다 — 사라지진 않지만(뭔가는
	// 원장에 남는다) 누구 것인지 틀린다. id 아닌 별도 키(예: 입력 인덱스)로 바꾸면
	// 이 가정이 필요 없어지지만, byID·picked·absorbable 이 전부 id 로 서로를 찾는
	// 지금 구조를 바꾸는 비용이 이 가정 하나를 문서화하는 비용보다 크다.
	for _, c := range in.Candidates {
		byID[c.Item.ID] = c
		order = append(order, c.Item.ID)
		rs := rejectionsFor(c, in)
		if len(rs) == 0 {
			fit = append(fit, c)
			continue
		}
		rejByItem[c.Item.ID] = rs
	}
	if len(fit) == 0 {
		return nil, flatten(order, rejByItem)
	}
	sort.SliceStable(fit, func(i, j int) bool { return lessCandidate(fit[i], fit[j]) })

	// 흡수 후보: 탈락 사유가 **전부** after-unmet-item 인 항목만.
	// 하나라도 다른 코드(폐기·미조회·오타·sha·job 등)가 섞이면 흡수하지 않는다 —
	// "기다리면 풀린다" 가 아닌 것을 충족으로 접는 순간 이 판정의 존재 이유가 무너진다.
	absorbable := map[string]Candidate{}
	for id, rs := range rejByItem {
		all := true
		for _, r := range rs {
			if r.Reason != AfterUnmetItem {
				all = false
				break
			}
		}
		if all && len(rs) > 0 {
			absorbable[id] = byID[id]
		}
	}

	bundles := make([]Bundle, 0, len(fit))
	for _, lead := range fit {
		b := bundleAround(lead, fit, absorbable, sib)
		// 기아 판정은 여기서만 한다 — bundleAround 는 시각을 안 받는 순수 조립이다.
		// Now 가 zero 면 안 돌린다(EligibleInput.Now 주석 참고).
		if !in.Now.IsZero() {
			if age := in.Now.Sub(b.Oldest); age >= StarvationAge {
				b.Starved = true
				b.Reason += fmt.Sprintf(" · ★기아 %s 경과(임계 %s) — 묶음 크기보다 먼저 본다",
					age.Round(time.Minute), StarvationAge)
			}
		}
		bundles = append(bundles, b)
	}
	sort.SliceStable(bundles, func(i, j int) bool { return lessBundle(bundles[i], bundles[j]) })

	best := bundles[0]
	best.Lead.Overlaps = OverlapsWithLive(bundlePaths(best), in.Live, in.Self, in.SelfCC)

	// 적격이었으나 이 묶음에 못 든 것도 원장에 남긴다. 안 남기면
	// pick_eval 어디에도 없어 "왜 저것이 아니라 이것인가"에 답할 수 없다.
	picked := map[string]bool{best.Lead.Item.ID: true}
	for _, m := range best.Members {
		picked[m.Item.ID] = true
		delete(rejByItem, m.Item.ID) // 흡수됐으면 원장에서 뺀다 — picked 이므로. 두 번 세면 불변식이 깨진다.
	}
	for _, c := range fit {
		if picked[c.Item.ID] {
			continue
		}
		rejByItem[c.Item.ID] = append(rejByItem[c.Item.ID], model.Rejection{
			Item: c.Item.ID, Reason: RejectNotTop,
			Detail: fmt.Sprintf("적격이지만 추천 묶음에 없다(추천 선두는 %s, 묶음 %d건)",
				best.Lead.Item.ID, len(best.Members)+1)})
	}
	return &best, flatten(order, rejByItem)
}

// flatten 은 항목별 사유를 **입력 순서대로** 편다.
// 맵을 그대로 순회하면 같은 입력에 사유 순서가 흔들리고,
// 그러면 pick_eval 로 쌓인 분포를 시점 간에 비교할 수 없다.
func flatten(order []string, byItem map[string][]model.Rejection) []model.Rejection {
	var out []model.Rejection
	for _, id := range order {
		out = append(out, byItem[id]...)
	}
	return out
}

// bundleAround 는 선두 하나를 중심으로 직접 이웃만 모은다.
// absorbable 은 EligibleBundle 이 미리 걸러 둔 "흡수 대상"이다 —
// 사유가 전부 after-unmet-item 인 탈락 항목만 여기 들어온다.
func bundleAround(lead Candidate, fit []Candidate, absorbable map[string]Candidate, sib SiblingIndex) Bundle {
	b := Bundle{Lead: lead, Dependents: lead.Dependents, Oldest: lead.Item.CreatedAt}
	add := func(c Candidate, l Link) {
		b.Members = append(b.Members, c)
		b.Links = append(b.Links, l)
		b.Dependents += c.Dependents
		if c.Item.CreatedAt.Before(b.Oldest) {
			b.Oldest = c.Item.CreatedAt
		}
	}
	for _, c := range fit {
		if c.Item.ID == lead.Item.ID {
			continue
		}
		if l := LinkOf(lead, c, sib); l != nil {
			add(c, *l)
		}
	}
	// 흡수 — 이 항목의 **선행 전체**(충족 여부와 무관하게)가 선두 하나만 가리켜야 한다.
	//
	// ★ 이것은 "미충족 선행만 전부 선두면 된다"보다 **좁다.** 이미 충족된 sha:cafe
	// 하나와 미충족 item:B-lead 하나를 같이 가진 항목은, 미충족분만 보면 흡수
	// 대상이지만 여기서는 흡수하지 않는다(TestEligibleBundleDoesNotAbsorbWhenA
	// SatisfiedPrerequisiteIsAlsoPresent 가 못박는다). 충족 여부를 다시 매기려면
	// 이 함수가 AfterFacts 를 받아야 하는데, 그러면 AfterSatisfied 의 사본이 여기
	// 하나 더 생기고 순수 판정 표면이 넓어진다. 실측(2026-08-05, 열린 큐)에서 sha
	// 선행과 item 선행을 동시에 가진 open 항목이 **0건**이라 이 좁힘의 비용은 지금
	// 0이다 — 넓히는 건 그 실측이 바뀌었을 때 다시 잴 결정이지, 조용히 넓힐 일이 아니다.
	for _, c := range sortedCands(absorbable) {
		if blockedOnlyBy(c, lead.Item.ID) {
			add(c, Link{Item: c.Item.ID, Axes: []BundleAxis{AxisAfter},
				Detail: fmt.Sprintf("선행 %s 를 같은 묶음이 함께 한다 — 랜딩을 안 기다린다", lead.Item.ID)})
		}
	}
	// fit 이 이미 lessCandidate 로 정렬돼 있어 Members·Links 도 그 순서를 물려받는다.
	// 흡수된 구성원은 sortedCands(=id 사전순)로 뒤에 덧붙는다.
	//
	// ★ 시각은 마이크로초까지 찍는다. 분 단위로 자르면 실측의 형제들처럼
	// 생성 시각이 초 단위로만 다른 두 묶음이 ③으로 갈렸는데도 Reason 문자열은
	// 똑같이 나온다 — Reason 은 "왜 이것이고 저것이 아닌가"에 답하는 원장인데,
	// 정작 갈림의 근거였던 값이 텍스트에서 안 보이면 그 답을 못 한다.
	b.Reason = fmt.Sprintf("의존자 합 %d · 묶음 %d건 · 최고령 %s · 선두 %s",
		b.Dependents, len(b.Members)+1, b.Oldest.UTC().Format("2006-01-02 15:04:05.000000"), lead.Item.ID)
	return b
}

// blockedOnlyBy 는 이 항목의 선행 **전체**가 정확히 그 항목 하나뿐인지 본다
// (충족된 선행이 섞여 있어도 마찬가지다 — 충족 여부는 안 본다. 위 bundleAround 의
// 흡수 주석 참고). 항목 선행이 여럿이면 전부 묶음 안이어야 하는데, 방사형 묶음의
// 구성원은 선두와만 직접 이어지므로 "전부"가 성립하는 경우가 곧 "하나뿐"이다.
func blockedOnlyBy(c Candidate, leadID string) bool {
	n := 0
	for _, a := range c.Item.After {
		if a.Item == "" {
			return false // sha·job 선행이 섞여 있으면 흡수하지 않는다
		}
		if a.Item != leadID {
			return false
		}
		n++
	}
	return n > 0
}

// sortedCands 는 맵을 id 사전순으로 편다. 맵 순회는 순서가 흔들린다.
func sortedCands(m map[string]Candidate) []Candidate {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Candidate, 0, len(ids))
	for _, id := range ids {
		out = append(out, m[id])
	}
	return out
}

// lessBundle 은 추천 순서다. 조정할 상수가 하나도 없다.
//
//	① 의존자 수 합 ↓ — 이걸 풀어야 남이 움직이는 정도
//	② 묶음 크기   ↓ — 한 번에 더 많이 푸는 쪽이 이긴다
//	③ 최고령      ↑ — 오래 방치된 것을 먼저
//	④ 선두 id     사전순 — 동점 처리. 없으면 같은 입력에 다른 답이 나온다
//
// ★ ②가 없으면 이 기능이 **발화하지 않는다.** 실측에서 열린 16건 전부 의존자 0이라
// ①이 상수이고, 그 상태에서 ③이 실질 1차 키가 되는데 최고령이 단독이었다(설계 §0.2).
// ★★ ①과 ② 사이에 **기아**가 들어간다. 그 이유는 위 ②의 근거가 그대로 뒤집힌
// 자리이기 때문이다 — ①이 상수라 ②가 실질 1차 키가 되는데, 묶음은
// (형제 판단 ∨ 같은 선행)으로만 서므로(LinkOf) **형제가 없는 항목은 묶음 크기가
// 영구히 1**이다. ③(최고령)은 ②가 동점일 때만 발화하니 구제가 안 된다.
//
// 실측(kweiza-cc-plugins · 판단 01KZAW342JAC6EAW8C31RCXXK0):
//
//	열린 26건의 의존자 = {0: 26}
//	7.5h 이상 묵은 17건 — 전부 형제 0(단독)
//	6.5h 이하 8건       — 전부 형제 있음      ← 경계가 하나뿐이다
//
// 그리고 followups 로 만든 항목은 같은 판단에 FK 로 걸려 **자동으로 서로 형제**라,
// 새 유입이 들어올 때마다 기존 단독 전부를 추월한다. 큐가 FIFO 로 보이면서 실제로는
// LIFO 로 돈다(store/item.go 가 ORDER BY created_at 으로 정확히 주는데도 그렇다 —
// 그 순서를 이 함수가 덮는다).
//
// ★ 기아 영역 **안에서는 ②를 안 본다.** 다시 넣으면 굶은 단독이 굶은 묶음에 밀리는,
// 똑같은 함정이 그 안에서 재현된다. 예외 상태에서 방어 가능한 규칙은
// "가장 오래 굶은 것부터" 하나뿐이다.
func lessBundle(a, b Bundle) bool {
	if a.Dependents != b.Dependents {
		return a.Dependents > b.Dependents
	}
	if a.Starved != b.Starved {
		return a.Starved
	}
	if a.Starved { // 둘 다 굶었다 — 묶음 크기를 건너뛰고 최고령순으로만 푼다
		if !a.Oldest.Equal(b.Oldest) {
			return a.Oldest.Before(b.Oldest)
		}
		return a.Lead.Item.ID < b.Lead.Item.ID
	}
	if len(a.Members) != len(b.Members) {
		return len(a.Members) > len(b.Members)
	}
	if !a.Oldest.Equal(b.Oldest) {
		return a.Oldest.Before(b.Oldest)
	}
	return a.Lead.Item.ID < b.Lead.Item.ID
}

// bundlePaths 는 묶음 전체가 만지는 경로다.
// 겹침("남과 부딪히나")은 묶음 단위 질문이므로 합집합으로 본다.
func bundlePaths(b Bundle) []string {
	seen := map[string]bool{}
	var out []string
	add := func(ps []string) {
		for _, p := range ps {
			if n := normPath(p); n != "" && !seen[n] {
				seen[n] = true
				out = append(out, p)
			}
		}
	}
	add(b.Lead.Item.Paths)
	for _, m := range b.Members {
		add(m.Item.Paths)
	}
	return out
}

// UnaccountedIDs 는 **요청한 id 중 응답이 설명하지 못한 것**이다. 순수 함수다.
//
// ★ 이 함수가 없으면 무엇이 깨지나. 서버는 독립 컨테이너인데 플러그인은 자동
// 갱신된다 — item_ids 를 **모르는 서버**(cae53bd 판)에 신 클라이언트가 묶음을 보내면
// 그 서버는 경로의 선두 하나만 집고 나머지 필드를 조용히 무시한 채 200 을 낸다.
// api_version 은 양쪽 다 "1" 이라 SkewBanner 도 안 뜬다. 그러면 `fd pick a b c` 가
// 종료코드 0 으로 a 만 찍고 끝나고, b·c 는 **선점되지도, 이름이 불리지도 않는다.**
// 선점이 존재하는 이유가 정확히 그 상황을 막는 것이다 — 세션 서른이 도는 판에서
// "쥐었다고 믿는데 안 쥔" 항목은 두 세션이 같은 파일을 동시에 고치는 사고가 된다.
//
// 판정은 한 줄이다: 요청 집합 − 응답이 설명한 집합. 비교는 공백을 다듬은 문자열
// 동등성이다(항목 id 는 ValidateItemID 가 [A-Za-z0-9._/-] 로 좁혀 둔 값이라
// 대소문자 접기 같은 추가 규칙을 넣으면 서로 다른 두 id 가 같은 것으로 접힌다).
//
// 순서는 **요청 순서**를 지킨다 — 사람이 명령줄에 적은 순서 그대로 불러야
// 어느 인자가 빠졌는지를 눈으로 짚을 수 있다.
func UnaccountedIDs(requested, accounted []string) []string {
	seen := make(map[string]bool, len(accounted))
	for _, id := range accounted {
		if id = strings.TrimSpace(id); id != "" {
			seen[id] = true
		}
	}
	var out []string
	dup := map[string]bool{}
	for _, id := range requested {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] || dup[id] {
			continue
		}
		dup[id] = true
		out = append(out, id)
	}
	return out
}
