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
	// Reason 은 네 키의 **실제 값**이다. 감추면 "왜 하필 이 브랜치 이름인가"에
	// 답할 수 없고, 답 못 하는 자동 선택은 두 번째 세션부터 무시된다.
	Reason string
}

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
	var rejected []model.Rejection
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

	bundles := make([]Bundle, 0, len(fit))
	for _, lead := range fit {
		bundles = append(bundles, bundleAround(lead, fit, sib))
	}
	sort.SliceStable(bundles, func(i, j int) bool { return lessBundle(bundles[i], bundles[j]) })

	best := bundles[0]
	best.Lead.Overlaps = OverlapsWithLive(bundlePaths(best), in.Live, in.Self)

	// 적격이었으나 이 묶음에 못 든 것도 원장에 남긴다. 안 남기면
	// pick_eval 어디에도 없어 "왜 저것이 아니라 이것인가"에 답할 수 없다.
	picked := map[string]bool{best.Lead.Item.ID: true}
	for _, m := range best.Members {
		picked[m.Item.ID] = true
	}
	for _, c := range fit {
		if picked[c.Item.ID] {
			continue
		}
		rejected = append(rejected, model.Rejection{Item: c.Item.ID, Reason: RejectNotTop,
			Detail: fmt.Sprintf("적격이지만 추천 묶음에 없다(추천 선두는 %s, 묶음 %d건)",
				best.Lead.Item.ID, len(best.Members)+1)})
	}
	return &best, rejected
}

// bundleAround 는 선두 하나를 중심으로 직접 이웃만 모은다.
func bundleAround(lead Candidate, fit []Candidate, sib SiblingIndex) Bundle {
	b := Bundle{Lead: lead, Dependents: lead.Dependents, Oldest: lead.Item.CreatedAt}
	for _, c := range fit {
		if c.Item.ID == lead.Item.ID {
			continue
		}
		l := LinkOf(lead, c, sib)
		if l == nil {
			continue
		}
		b.Members = append(b.Members, c)
		b.Links = append(b.Links, *l)
		b.Dependents += c.Dependents
		if c.Item.CreatedAt.Before(b.Oldest) {
			b.Oldest = c.Item.CreatedAt
		}
	}
	// fit 이 이미 lessCandidate 로 정렬돼 있어 Members·Links 도 그 순서를 물려받는다.
	//
	// ★ 시각은 마이크로초까지 찍는다. 분 단위로 자르면 실측의 형제들처럼
	// 생성 시각이 초 단위로만 다른 두 묶음이 ③으로 갈렸는데도 Reason 문자열은
	// 똑같이 나온다 — Reason 은 "왜 이것이고 저것이 아닌가"에 답하는 원장인데,
	// 정작 갈림의 근거였던 값이 텍스트에서 안 보이면 그 답을 못 한다.
	b.Reason = fmt.Sprintf("의존자 합 %d · 묶음 %d건 · 최고령 %s · 선두 %s",
		b.Dependents, len(b.Members)+1, b.Oldest.UTC().Format("2006-01-02 15:04:05.000000"), lead.Item.ID)
	return b
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
func lessBundle(a, b Bundle) bool {
	if a.Dependents != b.Dependents {
		return a.Dependents > b.Dependents
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
