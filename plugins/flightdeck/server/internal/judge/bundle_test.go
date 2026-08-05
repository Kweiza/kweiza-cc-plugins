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

		// ★ 아래 둘이 "첫 일치에서 끊는" 변이를 죽이는 자리다. 위의 한 개짜리
		// 케이스만으로는 루프가 첫 일치에서 return 해도 전부 통과한다.
		{"여럿이 일치하면 전부 낸다",
			[]string{"x.go", "shared1.go", "y.go", "shared2.go"},
			[]string{"shared2.go", "shared1.go", "z.go"},
			[]string{"shared1.go", "shared2.go"}},
		{"순서는 a 쪽을 따른다 — 화면의 경로가 항목이 선언한 순서여야 사람이 그 줄을 찾는다",
			[]string{"z.go", "m.go", "a.go"},
			[]string{"a.go", "m.go", "z.go"},
			[]string{"z.go", "m.go", "a.go"}},
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

// 겹친 경로가 여럿이면 근거 줄이 **전부** 불러야 한다.
//
// ★ 위의 SamePaths 표와 따로 두는 이유: 사람이 실제로 읽는 것은 이 한 줄이고,
// 여기서 경로가 하나만 나오면 "이 둘이 어디서 만나나"를 화면만 보고는 못 짚는다.
// SamePaths 가 첫 일치에서 끊기는 변이는 표에서도 이 줄에서도 죽어야 한다.
func TestLinkOfCarriesEverySharedPath(t *testing.T) {
	sib := SiblingIndex{"a": {"J9"}, "b": {"J9"}}
	l := LinkOf(cand("a", 0, []string{"p/one.go", "p/two.go", "p/only-a.go"}),
		cand("b", 1, []string{"p/two.go", "p/one.go"}), sib)
	if l == nil {
		t.Fatal("링크가 nil 이다")
	}
	for _, want := range []string{"p/one.go", "p/two.go"} {
		if !strings.Contains(l.Detail, want) {
			t.Fatalf("근거가 겹친 경로 %q 를 안 불렀다: %q", want, l.Detail)
		}
	}
	if strings.Contains(l.Detail, "p/only-a.go") {
		t.Fatalf("겹치지 않은 경로가 근거에 실렸다: %q", l.Detail)
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

	a := bundleAround(fit[0], fit, map[string]Candidate{}, sib)
	if got := memberIDs(&a); len(got) != 1 || got[0] != "B" {
		t.Fatalf("A 선두 묶음이 B 를 거쳐 C 로 전이했다 — 구성원 %v", got)
	}
	c := bundleAround(fit[2], fit, map[string]Candidate{}, sib)
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
		return bundleAround(lead, []Candidate{lead}, map[string]Candidate{}, SiblingIndex{})
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

// 묶음 겹침도 **형제 카드**를 건너뛴다 — 세션이 자기 자신과 조율하라는 경고를 막는 자리다.
//
// ★ 이 시험이 없으면 EligibleBundle 의 SelfCC 인자를 통째로 "" 로 바꿔도
// 전 스위트가 초록이다(실측). 그 상태에서 나는 것이 정확히 이 레포가 방금 고친
// 거짓 양성이다 — 카드 id 는 다르고 대화는 같은 형제가 남으로 보고된다.
// self(id 일치)와 형제(cc 일치)를 한 시험에 같이 두는 이유는, id 비교만 남기고
// cc 비교를 지우는 변이가 self 줄만 보는 시험은 통과하기 때문이다.
func TestEligibleBundleOverlapsSkipSiblingCardOfSameConversation(t *testing.T) {
	sib := SiblingIndex{"lead": {"J1"}, "mem": {"J1"}}
	in := EligibleInput{
		Self: "S1", SelfCC: "CC-1",
		Candidates: []Candidate{
			cand("lead", 0, []string{"lead-only.go"}),
			cand("mem", 1, []string{"member-only.go"}),
		},
		Live: []LiveSession{
			// 카드 id 는 다르지만 대화가 같다 — 형제다. 겹침이 아니다.
			{ID: "S9", CCSessionID: "CC-1", Paths: []string{"lead-only.go", "member-only.go"}},
			// 자기 카드. 이것도 겹침이 아니다.
			{ID: "S1", CCSessionID: "CC-1", Paths: []string{"lead-only.go"}},
		},
	}
	b, _ := EligibleBundle(in, sib)
	for _, o := range b.Lead.Overlaps {
		t.Fatalf("자기 대화의 카드 %q 가 겹침으로 보고됐다 — 세션이 자기 자신과 조율하게 된다", o.SessionID)
	}
}

// 형제를 거르는 것이 겹침 축을 통째로 끄는 것이어서는 안 된다.
// 위 시험만 있으면 OverlapsWithLive 호출을 아예 지우는 변이가 통과한다.
func TestEligibleBundleOverlapsStillCatchOtherConversation(t *testing.T) {
	sib := SiblingIndex{"lead": {"J1"}, "mem": {"J1"}}
	in := EligibleInput{
		Self: "S1", SelfCC: "CC-1",
		Candidates: []Candidate{
			cand("lead", 0, []string{"lead-only.go"}),
			cand("mem", 1, []string{"member-only.go"}),
		},
		Live: []LiveSession{
			{ID: "S9", CCSessionID: "CC-1", Paths: []string{"lead-only.go"}},
			{ID: "S8", CCSessionID: "CC-2", Paths: []string{"member-only.go"}},
		},
	}
	b, _ := EligibleBundle(in, sib)
	if len(b.Lead.Overlaps) != 1 || b.Lead.Overlaps[0].SessionID != "S8" {
		t.Fatalf("남의 대화(S8)와의 겹침이 안 잡혔다: %+v", b.Lead.Overlaps)
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

// 흡수의 유일한 경우. A 의 선행이 B 이고 B 가 선두면 한 브랜치에서 B→A 로 하면
// 랜딩을 안 기다린다.
func TestEligibleBundleAbsorbsBlockedItemWhenBlockerIsLead(t *testing.T) {
	blocker := cand("B-blocker", 0, nil)
	blocked := cand("A-blocked", 1, nil, afterItem("B-blocker"))
	in := EligibleInput{
		Self:       "S1",
		Candidates: []Candidate{blocker, blocked},
		Facts:      AfterFacts{ItemStates: map[string]model.ItemState{"B-blocker": model.ItemOpen}},
	}
	b, rej := EligibleBundle(in, SiblingIndex{})
	if b == nil {
		t.Fatalf("묶음이 nil 이다 (탈락 %v)", rej)
	}
	if b.Lead.Item.ID != "B-blocker" {
		t.Fatalf("선행이 선두여야 한다 — 선두가 %q 다", b.Lead.Item.ID)
	}
	if !contains(memberIDs(b), "A-blocked") {
		t.Fatalf("막힌 항목이 흡수되지 않았다 — 구성원 %v", memberIDs(b))
	}
	// 흡수된 항목은 picked 이므로 원장에서 빠져야 한다. 두 번 세면 불변식이 깨진다.
	if itemIDs(rej)["A-blocked"] {
		t.Fatalf("흡수된 항목이 탈락 원장에도 있다: %v", rej)
	}
	// 그런데 왜 들어왔는지는 남아야 한다.
	for i, m := range b.Members {
		if m.Item.ID == "A-blocked" && !strings.Contains(b.Links[i].Detail, "B-blocker") {
			t.Fatalf("흡수 근거에 선행 좌표가 없다: %q", b.Links[i].Detail)
		}
	}
}

// 선행이 묶음 밖이면 흡수하지 않는다. 밖의 것을 기다려야 하는 사실이 안 바뀐다.
func TestEligibleBundleDoesNotAbsorbWhenBlockerIsNotInBundle(t *testing.T) {
	in := EligibleInput{
		Self: "S1",
		Candidates: []Candidate{
			cand("A-blocked", 0, nil, afterItem("outside")),
			cand("Z-unrelated", 1, nil),
		},
		Facts: AfterFacts{ItemStates: map[string]model.ItemState{"outside": model.ItemOpen}},
	}
	b, rej := EligibleBundle(in, SiblingIndex{})
	if contains(memberIDs(b), "A-blocked") {
		t.Fatalf("묶음 밖 선행인데 흡수했다 — 구성원 %v", memberIDs(b))
	}
	if !contains(codesFor(rej, "A-blocked"), AfterUnmetItem) {
		t.Fatalf("막힌 항목의 사유가 원장에 없다: %v", codesFor(rej, "A-blocked"))
	}
}

// 선행이 둘인데 하나만 묶음 안이면 흡수하지 않는다.
func TestEligibleBundleNeedsEveryBlockerInBundle(t *testing.T) {
	in := EligibleInput{
		Self: "S1",
		Candidates: []Candidate{
			cand("B-blocker", 0, nil),
			cand("A-blocked", 1, nil, afterItem("B-blocker"), afterItem("outside")),
		},
		Facts: AfterFacts{ItemStates: map[string]model.ItemState{
			"B-blocker": model.ItemOpen, "outside": model.ItemOpen,
		}},
	}
	b, _ := EligibleBundle(in, SiblingIndex{})
	if contains(memberIDs(b), "A-blocked") {
		t.Fatalf("선행 하나가 묶음 밖인데 흡수했다 — 구성원 %v", memberIDs(b))
	}
}

// 사유가 둘 이상 섞이면 — 하나는 after-unmet-item, 하나는 다른 축(자원 점유) —
// 흡수하지 않는다. "사유 중 **하나라도** after-unmet-item"으로 완화하면, 자원을
// 남이 쥔 항목도 선행만 선두에 있으면 흡수돼 그 resource-held 줄이 원장에서
// 지워진다 — 선점 세션은 자원이 막힌 줄 모르고 그 항목을 받는다.
// (모든 기존 흡수 시험은 사유가 하나뿐인 항목만 쓰므로 any 와 every 를 못 가른다.)
func TestEligibleBundleDoesNotAbsorbWhenAnyRejectionIsNotUnmetItem(t *testing.T) {
	blocker := cand("B-lead", 0, nil)
	blocked := cand("A-blocked", 1, nil, afterItem("B-lead"))
	blocked.Needs = []string{"db"}
	in := EligibleInput{
		Self:          "S1",
		Candidates:    []Candidate{blocker, blocked},
		Facts:         AfterFacts{ItemStates: map[string]model.ItemState{"B-lead": model.ItemOpen}},
		HeldResources: map[string]string{"db": "S2"},
	}
	b, rej := EligibleBundle(in, SiblingIndex{})
	if contains(memberIDs(b), "A-blocked") {
		t.Fatalf("사유가 섞였는데(after-unmet-item + resource-held) 흡수했다 — 구성원 %v", memberIDs(b))
	}
	codes := codesFor(rej, "A-blocked")
	if !contains(codes, AfterUnmetItem) || !contains(codes, RejectResourceHeld) {
		t.Fatalf("사유 두 줄이 원장에 다 있어야 한다: %v", codes)
	}
}

// ★ 흡수 규칙의 좁힘을 못박는다. 이 항목의 선행은 둘이다 — 이미 충족된 sha:cafe000 과
// 미충족 item:B-lead. **미충족분만** 보면 흡수 대상(선두가 유일한 미충족 선행)이지만,
// 이 구현은 흡수하지 않는다 — blockedOnlyBy 가 충족 여부와 무관하게 After 전체를 본다.
// "미충족 선행만 전부 묶음 안이면 된다"로 넓히려면 판정 함수가 AfterFacts 를 받아야
// 하고, 그건 조용히 할 일이 아니라 결정으로 해야 한다 — 이 시험이 그 결정 없이
// 조용히 넓어지는 것을 막는다(design.md §2.3 이 같은 근거를 문서에 싣는다).
func TestEligibleBundleDoesNotAbsorbWhenASatisfiedPrerequisiteIsAlsoPresent(t *testing.T) {
	blocker := cand("B-lead", 0, nil)
	blocked := cand("A-blocked", 1, nil, afterItem("B-lead"), afterSHA("cafe000"))
	in := EligibleInput{
		Self:       "S1",
		Candidates: []Candidate{blocker, blocked},
		Facts: AfterFacts{
			ItemStates:  map[string]model.ItemState{"B-lead": model.ItemOpen},
			SHAAncestry: map[string]AncestryResult{"cafe000": AncestryYes}, // 이미 충족
		},
	}
	b, rej := EligibleBundle(in, SiblingIndex{})
	if contains(memberIDs(b), "A-blocked") {
		t.Fatalf("충족된 선행이 섞였는데 흡수했다(미충족분만 보면 선두뿐인데도) — 구성원 %v", memberIDs(b))
	}
	if !contains(codesFor(rej, "A-blocked"), AfterUnmetItem) {
		t.Fatalf("미충족 사유(after-unmet-item)가 원장에 없다: %v", codesFor(rej, "A-blocked"))
	}
}

// ★ 이 시험이 이 태스크의 핵심이다.
// 아홉 코드 중 흡수 가능한 것은 after-unmet-item 하나뿐이다.
// 나머지를 흡수하면 "모르는 것"이나 "영영 안 풀리는 것"이 충족으로 접힌다.
// 여섯 코드를 코드별로 못박는다: after-dropped-dep · after-unknown · after-bad-state
// (item 축) · after-bad-ref · after-unmet-sha · after-failed-job(sha·job 축).
// after-unmet-job 은 after-unmet-sha·after-failed-job 과 같은 "잡 축" 판정 경로를
// 공유하고, after-malformed 는 스키마 CHECK 를 우회한 입력에서만 나와 이 시험의
// 정상 입력 구성으로는 만들 이유가 약해 뺐다.
//
// ★ 이 여섯 줄이 전부 **같은 이유로** 안 흡수되는 것은 아니다. 처음 셋(item 축)은
// 이 파일 아래의 "코드 필터"(all==after-unmet-item) 하나로 막힌다 — 그 필터를
// 무력화하면(대입 조건을 늘 참으로 바꾸면) 처음 셋만 붉어진다. 나머지 셋(sha·job 축)은
// 애초에 item 이 아닌 선행이라 blockedOnlyBy 의 구조적 가드(비-item 선행은 무조건
// 거른다)가 코드 필터와 **무관하게** 이중으로 막는다 — 코드 필터만 무력화해선 안
// 붉어진다. 그 가드 자체도 이중이다: blockedOnlyBy 의 `a.Item == ""` 줄과
// `a.Item != leadID` 줄은 leadID 가 항상 비어 있지 않다는 store 불변식 때문에
// 비-item 선행에 대해 서로를 완전히 대체한다 — 둘 다 무력화해야만 sha·job 세
// 줄이 붉어진다("아직 조상이 아닌 sha 선행" 행으로 실측 확인, task-4-report.md
// Fix round 2/5). 그래도 이 셋을 남긴 이유는, 코드가 실제로
// after-bad-ref·after-unmet-sha·after-failed-job 인지(예를 들어 항상
// after-unknown 으로 새지 않는지)를 원장에서 직접 확인하는 값어치가 있어서다 —
// 방어가 겹친다고 사유 코드 자체가 맞는지 안 볼 이유는 없다.
func TestEligibleBundleAbsorbsOnlyUnmetItem(t *testing.T) {
	cases := []struct {
		name  string
		after model.After
		facts AfterFacts
		code  string
	}{
		{"폐기된 선행은 흡수 불가 — 영영 안 풀린다",
			afterItem("B-blocker"),
			AfterFacts{ItemStates: map[string]model.ItemState{"B-blocker": model.ItemDropped}},
			AfterDroppedDep},
		{"조회 못 한 선행은 흡수 불가 — 판정 자체를 안 했다",
			afterItem("B-blocker"),
			AfterFacts{ItemStates: map[string]model.ItemState{}}, // 키 부재 = after-unknown
			AfterUnknown},
		{"열거에 없는 항목 상태는 흡수 불가 — 스키마와 코드가 어긋난 정합성 결함이다",
			afterItem("B-blocker"),
			AfterFacts{ItemStates: map[string]model.ItemState{"B-blocker": model.ItemState("bogus")}},
			AfterBadState},
		{"오타 ref 인 sha 선행은 흡수 불가 — 오타이거나 지워진 커밋이다",
			afterSHA("deadbee"),
			AfterFacts{SHAAncestry: map[string]AncestryResult{"deadbee": AncestryBadRef}},
			AfterBadRef},
		{"아직 조상이 아닌 sha 선행은 흡수 불가 — 이 세션이 만들 수 없는 사실을 기다린다",
			afterSHA("deadbee"),
			AfterFacts{SHAAncestry: map[string]AncestryResult{"deadbee": AncestryNo}},
			AfterUnmetSHA},
		{"실패한 잡 선행은 흡수 불가 — 재실행 없이는 안 풀린다",
			model.After{Job: "ci-1"},
			AfterFacts{JobStates: map[string]string{"ci-1": "fail"}},
			AfterFailedJob},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := EligibleInput{
				Self: "S1",
				Candidates: []Candidate{
					cand("B-blocker", 0, nil),
					cand("A-blocked", 1, nil, tc.after),
				},
				Facts: tc.facts,
			}
			b, rej := EligibleBundle(in, SiblingIndex{})
			if contains(memberIDs(b), "A-blocked") {
				t.Fatalf("%s 인데 흡수했다 — 구성원 %v", tc.code, memberIDs(b))
			}
			if !contains(codesFor(rej, "A-blocked"), tc.code) {
				t.Fatalf("사유 코드 %q 가 원장에 없다: %v", tc.code, codesFor(rej, "A-blocked"))
			}
		})
	}
}

// flatten 은 항목별 사유를 입력 순서대로 편다. 맵을 그대로 순회하면
// 같은 입력에도 사유 순서가 흔들리고, 그러면 pick_eval 로 쌓인 분포를
// 시점 간에 비교할 수 없다 — 이 패키지가 이미 지키는 결정성 규율
// (TestLinkOfPicksSharedJudgmentDeterministically)을 flatten 에도 적용한다.
func TestFlattenPreservesCandidateInputOrder(t *testing.T) {
	order := []string{"z-item", "a-item", "m-item"}
	byItem := map[string][]model.Rejection{
		"z-item": {{Item: "z-item", Reason: "r1"}},
		"a-item": {{Item: "a-item", Reason: "r2"}},
		"m-item": {{Item: "m-item", Reason: "r3"}},
	}
	first := flatten(order, byItem)
	for i := 0; i < 50; i++ {
		got := flatten(order, byItem)
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("같은 입력에 다른 순서가 나왔다(%d회차): %v vs %v", i, got, first)
		}
	}
	var gotOrder []string
	for _, r := range first {
		gotOrder = append(gotOrder, r.Item)
	}
	want := []string{"z-item", "a-item", "m-item"}
	if !reflect.DeepEqual(gotOrder, want) {
		t.Fatalf("flatten 이 입력 순서를 안 지켰다 — %v, 기대 %v", gotOrder, want)
	}
}

// sortedCands 는 흡수 후보 맵을 id 사전순으로 편다. 맵 순회를 그대로 쓰면
// 같은 입력에서도 흡수 링크의 부착 순서가 흔들린다.
func TestSortedCandsWalksIDOrderDeterministically(t *testing.T) {
	m := map[string]Candidate{
		"z-item": cand("z-item", 0, nil),
		"a-item": cand("a-item", 1, nil),
		"m-item": cand("m-item", 2, nil),
	}
	idsOf := func(cs []Candidate) []string {
		out := make([]string, 0, len(cs))
		for _, c := range cs {
			out = append(out, c.Item.ID)
		}
		return out
	}
	first := idsOf(sortedCands(m))
	for i := 0; i < 50; i++ {
		if got := idsOf(sortedCands(m)); !reflect.DeepEqual(got, first) {
			t.Fatalf("같은 입력에 다른 순서가 나왔다(%d회차): %v vs %v", i, got, first)
		}
	}
	want := []string{"a-item", "m-item", "z-item"}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("sortedCands 가 id 사전순이 아니다 — %v, 기대 %v", first, want)
	}
}
