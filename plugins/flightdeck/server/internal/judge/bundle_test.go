package judge

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// SamePaths 는 PathsOverlap 과 **일부러 다르다**.
// PathsOverlap 의 소비자는 "남의 세션과 부딪히나"라 넓게 잡는 것이 옳고,
// 이 함수의 소비자는 "함께 할 일인가"라 넓게 잡으면 큐가 통째로 한 묶음이 된다.
// 아래 표의 조상 관계 줄들이 그 차이를 못박는 자리다.
func TestSamePathsCountsOnlyExactTokens(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want []string
	}{
		{"같은 파일", []string{"a/b.go"}, []string{"a/b.go"}, []string{"a/b.go"}},
		{"표기 흔들림은 흡수한다", []string{"a/b/"}, []string{"./a//b"}, []string{"a/b/"}},

		// ── 실측 결함 그대로. 이 셋이 nil 이어야 한다 ──
		{"조상 디렉토리는 안 센다",
			[]string{"plugins/flightdeck/server/cmd/fd"},
			[]string{"plugins/flightdeck/server/cmd/fd/hook.go"}, nil},
		{"자손 파일도 안 센다",
			[]string{"plugins/flightdeck/server/cmd/fd/client.go"},
			[]string{"plugins/flightdeck/server/cmd/fd"}, nil},
		{"디렉토리끼리 조상 관계도 안 센다",
			[]string{"internal/store"}, []string{"internal/store/session.go"}, nil},

		{"무관", []string{"a/b.go"}, []string{"c/d.go"}, nil},
		{"여럿 중 하나만 일치",
			[]string{"x.go", "shared.go", "y.go"},
			[]string{"shared.go", "z.go"}, []string{"shared.go"}},
		{"중복은 한 번만",
			[]string{"s.go", "./s.go"}, []string{"s.go"}, []string{"s.go"}},
		{"빈 토큰은 무시한다", []string{"", "  ", "a.go"}, []string{"a.go", ""}, []string{"a.go"}},
		{"양쪽 다 비면", nil, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SamePaths(c.a, c.b)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("SamePaths(%v, %v) = %v, 원하는 값 %v", c.a, c.b, got, c.want)
			}
		})
	}
}

// 순수 함수는 인자를 안 고친다. 고치면 시험이 보는 것과 호출자가 보는 것이 갈라진다.
func TestSamePathsDoesNotMutateInput(t *testing.T) {
	a := []string{"a.go", "b.go"}
	b := []string{"b.go"}
	SamePaths(a, b)
	if !reflect.DeepEqual(a, []string{"a.go", "b.go"}) {
		t.Fatalf("입력 a 가 바뀌었다: %v", a)
	}
}

// afterItem·afterSHA 는 시험용 선행이다.
func afterItem(id string) model.After { return model.After{Item: id} }
func afterSHA(s string) model.After   { return model.After{SHA: s} }

func cand(id string, minutes int, paths []string, after ...model.After) Candidate {
	it := openItem(id, minutes, paths...)
	it.After = after
	return Candidate{Item: it}
}

func axesOf(l *Link) []BundleAxis {
	if l == nil {
		return nil
	}
	return l.Axes
}

func TestLinkOfAxes(t *testing.T) {
	sib := SiblingIndex{
		"a": {"J1", "J2"},
		"b": {"J2"},
		"c": {"J9"},
	}
	cases := []struct {
		name       string
		lead, othr Candidate
		want       []BundleAxis
	}{
		{"형제 단독으로 성립한다",
			cand("a", 0, nil), cand("b", 1, nil),
			[]BundleAxis{AxisSibling}},

		{"같은 선행 단독으로 성립한다",
			cand("c", 0, nil, afterSHA("47421b4")), cand("d", 1, nil, afterSHA("47421b4")),
			[]BundleAxis{AxisAfter}},

		// ★ 이 줄이 결합 규칙의 전부다.
		{"경로만 같으면 성립하지 않는다",
			cand("c", 0, []string{"x.go"}), cand("d", 1, []string{"x.go"}),
			nil},

		{"경로는 이미 선 링크를 보강한다",
			cand("a", 0, []string{"x.go"}), cand("b", 1, []string{"x.go"}),
			[]BundleAxis{AxisSibling, AxisPaths}},

		{"축 셋이 전부 맞으면 셋 다 나온다",
			cand("a", 0, []string{"x.go"}, afterSHA("47421b4")),
			cand("b", 1, []string{"x.go"}, afterSHA("47421b4")),
			[]BundleAxis{AxisSibling, AxisAfter, AxisPaths}},

		{"선행이 하나라도 다르면 그 축은 안 선다",
			cand("c", 0, nil, afterSHA("47421b4")), cand("d", 1, nil, afterSHA("f7ff0a7")),
			nil},

		{"선행 순서가 달라도 같은 집합이면 성립한다",
			cand("c", 0, nil, afterSHA("47421b4"), afterItem("z")),
			cand("d", 1, nil, afterItem("z"), afterSHA("47421b4")),
			[]BundleAxis{AxisAfter}},

		{"선행이 양쪽 다 없으면 그 축은 안 선다 — 빈 집합끼리 같다고 세지 않는다",
			cand("c", 0, nil), cand("d", 1, nil),
			nil},

		{"무관",
			cand("c", 0, []string{"x.go"}), cand("e", 1, []string{"y.go"}),
			nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := axesOf(LinkOf(tc.lead, tc.othr, sib))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("축이 %v 인데 %v 를 원한다", got, tc.want)
			}
		})
	}
}

// 사유가 없으면 "왜 이게 들어왔나"에 답할 수 없고, 답 못 하는 자동 선택은
// 두 번째 세션부터 무시된다.
func TestLinkOfCarriesWhy(t *testing.T) {
	sib := SiblingIndex{"a": {"J2"}, "b": {"J2"}}
	l := LinkOf(cand("a", 0, []string{"x.go"}, afterSHA("47421b4")),
		cand("b", 1, []string{"x.go"}, afterSHA("47421b4")), sib)
	if l == nil {
		t.Fatal("링크가 nil 이다")
	}
	for _, want := range []string{"J2", "47421b4", "x.go"} {
		if !strings.Contains(l.Detail, want) {
			t.Fatalf("근거에 %q 가 없다: %q", want, l.Detail)
		}
	}
	if l.Item != "b" {
		t.Fatalf("Link.Item 이 %q 다 — 이웃 id 여야 한다", l.Item)
	}
}

// 공유 판단이 여럿이면 어느 것을 적을지가 흔들려선 안 된다.
// 흔들리면 같은 입력에 다른 응답이 나오고, 그러면 재개가 재출력이 아니게 된다.
func TestLinkOfPicksSharedJudgmentDeterministically(t *testing.T) {
	sib := SiblingIndex{"a": {"J1", "J2", "J3"}, "b": {"J3", "J2", "J1"}}
	first := LinkOf(cand("a", 0, nil), cand("b", 1, nil), sib).Detail
	for i := 0; i < 50; i++ {
		if got := LinkOf(cand("a", 0, nil), cand("b", 1, nil), sib).Detail; got != first {
			t.Fatalf("같은 입력에 다른 근거가 나왔다: %q vs %q", first, got)
		}
	}
	if !strings.Contains(first, "J1") {
		t.Fatalf("공유 판단 중 사전순 첫째(J1)를 안 골랐다: %q", first)
	}
}

func memberIDs(b *Bundle) []string {
	if b == nil {
		return nil
	}
	out := make([]string, 0, len(b.Members))
	for _, m := range b.Members {
		out = append(out, m.Item.ID)
	}
	return out
}

// 이웃이 없으면 원소 1개짜리 묶음이다 — 단독은 특수 경우가 아니라 상위집합의 밑변이다.
func TestEligibleBundleSoloWhenNoNeighbor(t *testing.T) {
	in := EligibleInput{Self: "S1", Candidates: []Candidate{
		cand("solo", 0, []string{"a.go"}),
	}}
	b, rej := EligibleBundle(in, SiblingIndex{})
	if b == nil {
		t.Fatalf("적격이 있는데 묶음이 nil 이다 (탈락 %v)", rej)
	}
	if b.Lead.Item.ID != "solo" || len(b.Members) != 0 {
		t.Fatalf("단독이어야 하는데 선두 %q 구성원 %v", b.Lead.Item.ID, memberIDs(b))
	}
}

// 전이하지 않는다. A–B, B–C 인데 A–C 가 무관하면 A 선두 묶음에 C 가 없어야 한다.
// 전이를 허용하면 넓은 토큰 하나가 큐의 3분의 2를 한 묶음으로 만든다(설계 §0.1).
//
// ★ EligibleBundle 전체가 아니라 bundleAround 를 선두별로 직접 부른다.
// 이 셋을 EligibleBundle 에 통째로 넣으면 B(두 판단 J1·J2 에 걸친 진짜 허브 —
// A 와도 직접 이어지고 C 와도 직접 이어진다)가 묶음 2건으로 A·C 의 묶음 1건보다
// 커서 키②로 이긴다. 의존자·나이를 아무리 바꿔도 못 뒤집는다 — B 의 묶음은
// A 의 묶음(A+B)의 상위집합에 C 까지 얹은 것이라 의존자 합·크기가 항상
// A 이상이다. 그러면 승자를 기준으로 "구성원 1건"을 단정하는 순간 이 시험은
// "전이 안 함"이 아니라 "허브가 커서 이겼다"(키②가 의도한 그대로 —
// TestEligibleBundlePrefersBiggerBundleOverOlderSolo 가 이미 지킨다)를 재검사하게 되어
// 정작 지키려던 것을 못 지킨다. 그래서 각 선두의 방사형 결과를 직접 본다.
func TestEligibleBundleDoesNotTransit(t *testing.T) {
	sib := SiblingIndex{"A": {"J1"}, "B": {"J1", "J2"}, "C": {"J2"}}
	fit := []Candidate{cand("A", 0, nil), cand("B", 1, nil), cand("C", 2, nil)}

	a := bundleAround(fit[0], fit, sib)
	if got := memberIDs(&a); len(got) != 1 || got[0] != "B" {
		t.Fatalf("A 선두 묶음이 B 를 거쳐 C 로 전이했다 — 구성원 %v", got)
	}
	c := bundleAround(fit[2], fit, sib)
	if got := memberIDs(&c); len(got) != 1 || got[0] != "B" {
		t.Fatalf("C 선두 묶음이 B 를 거쳐 A 로 전이했다 — 구성원 %v", got)
	}
}

// 정렬 키 ②. 이게 없으면 최고령 단독이 항상 이겨 묶음이 영원히 발화하지 않는다.
func TestEligibleBundlePrefersBiggerBundleOverOlderSolo(t *testing.T) {
	sib := SiblingIndex{"y1": {"J1"}, "y2": {"J1"}}
	in := EligibleInput{Self: "S1", Candidates: []Candidate{
		cand("x-oldest-solo", 0, nil), // 가장 오래됨. 이웃 없음
		cand("y1", 10, nil),
		cand("y2", 11, nil),
	}}
	b, _ := EligibleBundle(in, sib)
	if len(b.Members) != 1 {
		t.Fatalf("묶음(2건)이 최고령 단독을 못 이겼다 — 선두 %q 구성원 %v",
			b.Lead.Item.ID, memberIDs(b))
	}
}

// 정렬 키 ①은 여전히 ②보다 앞이다.
func TestEligibleBundleDependentsBeatSize(t *testing.T) {
	sib := SiblingIndex{"y1": {"J1"}, "y2": {"J1"}}
	heavy := cand("heavy-solo", 0, nil)
	heavy.Dependents = 5
	in := EligibleInput{Self: "S1", Candidates: []Candidate{
		heavy, cand("y1", 10, nil), cand("y2", 11, nil),
	}}
	b, _ := EligibleBundle(in, sib)
	if b.Lead.Item.ID != "heavy-solo" {
		t.Fatalf("의존자 합이 크기보다 앞이어야 한다 — 선두가 %q 다", b.Lead.Item.ID)
	}
}

// 키 ①②③이 전부 동점이면 ④(선두 id 사전순)가 브랜치 이름을 정한다.
// 실측의 형제 3건이 정확히 이 경우다 — 생성 시각이 마이크로초까지 같다.
//
// 여기서 겸사겸사 Links·Members 의 문서화된 불변식 둘도 확인한다:
// 길이가 같아야 하고(Links 는 Members 와 같은 순서·같은 길이), Members 는
// fit(=lessCandidate 로 이미 정렬된 목록)의 순서를 물려받아야 한다. 셋 다 동점이라
// lessCandidate 의 동점 처리(id 사전순)로 fit = [a-first, m-mid, z-last] 가 되므로
// 선두 a-first 의 구성원은 정확히 이 순서(m-mid, z-last)로 나와야 한다.
func TestEligibleBundleTieBreaksByLeadID(t *testing.T) {
	sib := SiblingIndex{"m-mid": {"J1"}, "a-first": {"J1"}, "z-last": {"J1"}}
	in := EligibleInput{Self: "S1", Candidates: []Candidate{
		cand("m-mid", 0, nil), cand("z-last", 0, nil), cand("a-first", 0, nil),
	}}
	b, _ := EligibleBundle(in, sib)
	if b.Lead.Item.ID != "a-first" {
		t.Fatalf("동점에서 선두 id 사전순이어야 한다 — 선두가 %q 다", b.Lead.Item.ID)
	}
	if got := memberIDs(b); len(got) != 2 || got[0] != "m-mid" || got[1] != "z-last" {
		t.Fatalf("Members 가 fit 의 사전순을 안 물려받았다 — 구성원 %v, 기대 [m-mid z-last]", got)
	}
	if len(b.Links) != len(b.Members) {
		t.Fatalf("Links 와 Members 의 길이가 다르다 — Links %d건, Members %d건", len(b.Links), len(b.Members))
	}
}

// 정렬 키 ④(선두 id 사전순)를 lessBundle 을 직접 불러 확인한다.
//
// ★ EligibleBundle 을 통해서 확인하면 안 되는 이유: bundles 슬라이스는
// lessCandidate 로 이미 사전순 정렬된 fit 에서 만들어지고, lessCandidate 자신의
// 동점 처리도 id 사전순이라 sort.SliceStable 이 그 순서를 그대로 지킨다.
// 그러면 ④(lessBundle 의 마지막 줄)를 통째로 `return false` 로 바꿔 지워도
// 답이 안 바뀐다 — 이 시험은 사실 ④가 아니라 lessCandidate 가 SliceStable 을
// 거쳐 새어나온 결과를 재확인하고 있을 뿐이다(TestEligibleBundleTieBreaksByLeadID
// 가 그 함정이다). 그래서 손으로 만든 Bundle 둘로 lessBundle 을 직접 부른다.
func TestLessBundleTieBreaksByLeadID(t *testing.T) {
	// ①②③을 전부 동점으로 고정한다: Dependents 같음, Members 길이 같음, Oldest 같음.
	mk := func(leadID string) Bundle {
		return Bundle{
			Lead:       cand(leadID, 0, nil),
			Dependents: 3,
			Members:    []Candidate{cand("m", 1, nil)},
			Oldest:     t0,
		}
	}
	a, z := mk("a-first"), mk("z-last")
	if !lessBundle(a, z) {
		t.Fatalf("①②③ 동점인데 id 사전순(a-first < z-last)으로 안 이겼다")
	}
	if lessBundle(z, a) {
		t.Fatalf("역방향이 대칭이 아니다 — z-last 가 a-first 를 이겼다")
	}
}

// 정렬 키 ③(최고령 ↑)을 lessBundle 을 직접 불러 확인한다.
//
// ★ EligibleBundle 을 통해서 확인하면 같은 함정에 빠진다: lessCandidate 의
// **둘째** 키도 생성 시각 오름차순이라, fit 이 이미 나이순으로 정렬돼 있고
// bundles 순서가 그 순서를 물려받아 ③을 지워도 우연히 같은 답이 나올 수 있다.
// 그래서 선두 id 를 일부러 "기대와 반대" 로 배정한다 — older 의 선두 id 를
// newer 의 선두 id 보다 사전순으로 **뒤**에 둔다. ③이 사라져 ④(id)로
// 새면 승자가 뒤집혀서 잡힌다.
func TestLessBundlePrefersOlder(t *testing.T) {
	older := Bundle{Lead: cand("z-old", 0, nil), Dependents: 1, Members: []Candidate{cand("m", 0, nil)}, Oldest: t0}
	newer := Bundle{Lead: cand("a-new", 0, nil), Dependents: 1, Members: []Candidate{cand("m", 0, nil)}, Oldest: t0.Add(time.Hour)}
	if !lessBundle(older, newer) {
		t.Fatalf("①② 동점인데 최고령(older)이 안 이겼다")
	}
	if lessBundle(newer, older) {
		t.Fatalf("역방향이 대칭이 아니다 — 더 최근인데 이겼다")
	}
}

// Reason 의 최고령 표기가 분 단위로 잘리면, 실측 형제들처럼 초·마이크로초
// 단위로만 다른 두 최고령이 ③으로 서로 다른 승자를 냈는데도 Reason 문자열은
// 똑같이 찍힌다 — "왜 이것이고 저것이 아닌가"에 답해야 하는 원장이
// 정작 그 답의 근거를 감추는 셈이다.
func TestBundleAroundReasonDistinguishesCloseOldest(t *testing.T) {
	mk := func(offset time.Duration) Bundle {
		it := openItem("solo", 0)
		it.CreatedAt = t0.Add(offset)
		lead := Candidate{Item: it}
		return bundleAround(lead, []Candidate{lead}, SiblingIndex{})
	}
	a := mk(0)
	b := mk(30 * time.Second)
	if a.Reason == b.Reason {
		t.Fatalf("최고령이 30초 다른데 Reason 문자열이 같다(분 단위 표기라 구분이 안 된다): %q", a.Reason)
	}
}

// 불변식: 모든 후보는 picked 이거나 rejected 에 최소 한 줄.
// 조용히 사라지는 것이 하나도 없어야 큐가 블랙박스가 안 된다.
func TestEligibleBundleLedgersEveryCandidate(t *testing.T) {
	sib := SiblingIndex{"y1": {"J1"}, "y2": {"J1"}}
	in := EligibleInput{Self: "S1", Candidates: []Candidate{
		cand("y1", 0, nil), cand("y2", 1, nil),
		cand("lonely", 5, nil),
		{Item: openItem("taken", 2), ClaimedBy: "S2"},
	}}
	b, rej := EligibleBundle(in, sib)
	inBundle := map[string]bool{b.Lead.Item.ID: true}
	for _, id := range memberIDs(b) {
		inBundle[id] = true
	}
	ledger := itemIDs(rej)
	for _, id := range []string{"y1", "y2", "lonely", "taken"} {
		if !inBundle[id] && !ledger[id] {
			t.Fatalf("후보 %q 가 묶음에도 원장에도 없다", id)
		}
		if inBundle[id] && ledger[id] {
			t.Fatalf("후보 %q 가 묶음에도 원장에도 있다 — 두 번 셌다", id)
		}
	}
	if !contains(codesFor(rej, "lonely"), RejectNotTop) {
		t.Fatalf("적격이지만 안 뽑힌 항목의 사유가 %v 다", codesFor(rej, "lonely"))
	}
	if !contains(codesFor(rej, "taken"), RejectClaimed) {
		t.Fatalf("남이 선점한 항목의 사유가 %v 다", codesFor(rej, "taken"))
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// 적격 0건이면 nil 을 내되 사유는 전부 낸다.
func TestEligibleBundleNoneKeepsEveryReason(t *testing.T) {
	in := EligibleInput{Self: "S1", Candidates: []Candidate{
		{Item: openItem("taken", 0), ClaimedBy: "S2"},
		{Item: model.Item{ID: "done", State: model.ItemDone, CreatedAt: t0}},
	}}
	b, rej := EligibleBundle(in, SiblingIndex{})
	if b != nil {
		t.Fatalf("적격이 없는데 묶음이 나왔다: %q", b.Lead.Item.ID)
	}
	if len(itemIDs(rej)) != 2 {
		t.Fatalf("탈락 원장에 2건이 있어야 한다: %v", rej)
	}
}

// 겹침은 묶음 **전체 경로**로 본다 — 남과 부딪히는지는 묶음 단위 질문이다.
func TestEligibleBundleOverlapsUseWholeBundlePaths(t *testing.T) {
	sib := SiblingIndex{"lead": {"J1"}, "mem": {"J1"}}
	in := EligibleInput{
		Self: "S1",
		Candidates: []Candidate{
			cand("lead", 0, []string{"lead-only.go"}),
			cand("mem", 1, []string{"member-only.go"}),
		},
		Live: []LiveSession{{ID: "S2", Paths: []string{"member-only.go"}}},
	}
	b, _ := EligibleBundle(in, sib)
	if len(b.Lead.Overlaps) == 0 {
		t.Fatal("구성원의 경로로 난 겹침이 안 잡혔다 — 겹침을 선두 경로로만 봤다")
	}
}

// 사유가 없으면 "왜 저것이 아니라 이것인가"에 답할 수 없다.
//
// ★ "의존자"·"묶음"·"최고령" 라벨 존재만 확인하면 부족하다 — Reason 을 통째로
// "의존자 · 묶음 · 최고령 순으로 골랐다" 같은 일반 문구로 바꿔도 라벨 세 개가
// 다 들어 있어 통과한다. 그 문구엔 실제 값도, 키④(선두)도 없다. Reason 은
// "네 키의 실제 값" 이라고 문서에 적어 뒀으니 실제 값을 확인한다.
func TestEligibleBundleCarriesReason(t *testing.T) {
	sib := SiblingIndex{"y1": {"J1"}, "y2": {"J1"}}
	in := EligibleInput{Self: "S1", Candidates: []Candidate{cand("y1", 0, nil), cand("y2", 1, nil)}}
	b, _ := EligibleBundle(in, sib)
	// 의존자 0, 묶음 2건(y1+y2), 선두 y1(동점에서 사전순으로 이긴다) — 셋 다 실측값이다.
	for _, want := range []string{"의존자 합 0", "묶음 2건", "선두 y1"} {
		if !strings.Contains(b.Reason, want) {
			t.Fatalf("사유에 실제 값 %q 가 없다: %q", want, b.Reason)
		}
	}
}
