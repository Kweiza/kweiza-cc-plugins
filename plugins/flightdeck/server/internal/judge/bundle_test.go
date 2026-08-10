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

		// ★ 아래 둘은 normPath 가 components(paths.go)에 얹혀 있다는 사실을 이 축의
		// 좌표계로 못박는다. "표기 흔들림은 흡수한다" 한 줄은 `.`·중복 슬래시만 보므로
		// 정규화 규칙을 앞뒤로 흔드는 변이 둘이 그 줄을 통과한다(실측: 둘 다 전 스위트 초록).
		//
		// 공백: components 의 TrimSpace 를 지우면 `fd add --paths "a.go, b.go"` 처럼
		// 사람이 쉼표 뒤에 띄어 적은 토큰이 자기 자신과도 안 맞아 경로 축이 통째로
		// 죽는다 — 오류가 아니라 '겹침 없음'으로 나와 정상 응답과 구분이 안 된다.
		{"앞뒤 공백은 흡수한다 — 안 그러면 사람이 띄어 적은 토큰의 경로 축이 조용히 죽는다",
			[]string{" a.go "}, []string{"a.go"}, []string{" a.go "}},
		// ".." : components 가 **일부러** 안 걷어낸다(paths.go 주석). 걷어내면
		// `../a.go` 와 `a.go` 가 같은 경로가 되어, 서로 다른 디렉토리를 가리키는 두 항목이
		// 경로 축으로 붙는다. 등록 목록의 ".." 는 입력 오류이고, 조용히 정규화하면 안 보인다.
		{"'..' 는 정규화하지 않는다 — 접으면 다른 디렉토리끼리 같은 경로가 된다",
			[]string{"../a.go"}, []string{"a.go"}, nil},

		// ★ 그리고 정규형은 **성분 경계를 지운다.** normPath 의 구분자를 "" 로 바꿔도
		// 위 줄들이 전부 통과했다(실측) — `.`·공백·`..` 은 경계가 안 흔들리는 입력이라서다.
		// 경계가 뭉개지면 서로 다른 두 경로가 같은 토큰이 되고, 그 순간 둘이다:
		// 링크가 "같은 경로"라는 **거짓 근거**를 화면에 적고, bundlePaths 가 둘 중 하나를
		// 중복으로 접어 **그 경로의 겹침 판정을 통째로 건너뛴다** — 남과 부딪히는지를
		// 안 본 채로 부딪히지 않는다고 말하는 것이 이 도구가 없애려는 침묵 그 자체다.
		{"성분 경계는 안 뭉갠다", []string{"a/bc"}, []string{"ab/c"}, nil},

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

// 근거 줄의 경로는 **선두가 선언한 원문·선언 순서**다.
//
// ★ SamePaths 표는 "돌려주는 표기는 a 쪽 원문"을 함수 좌표계에서만 잠근다. 그런데
// 그 규율이 화면까지 오려면 LinkOf 가 **선두를 a 로** 넘겨야 하고, 그 인자 순서는
// 표에서 안 보인다 — 실측으로 `SamePaths(other…, lead…)` 로 뒤바꿔도 전 스위트가
// 초록이었다(위 TestLinkOfCarriesEverySharedPath 는 양쪽 표기가 같아 못 가른다).
// 뒤바뀌면 선두 카드의 근거 줄에 **이웃이 적은 표기와 이웃이 적은 순서**가 뜬다.
// 사람은 선두를 보고 있는데 자기가 안 적은 줄을 읽게 되고, 그러면 "내가 적은 그 줄"을
// 못 찾는다 — SamePaths 주석이 원문 표기를 고집하는 이유가 정확히 그것이다.
//
// 그래서 두 축을 같이 본다: 표기(`./`가 붙은 선두 원문)와 순서(선두의 선언 순서).
func TestLinkOfCarriesLeadSpellingAndOrder(t *testing.T) {
	sib := SiblingIndex{"a": {"J9"}, "b": {"J9"}}
	// 같은 두 경로를 양쪽이 **다른 표기·다른 순서**로 선언했다.
	l := LinkOf(cand("a", 0, []string{"./svc/z.go", "svc//m.go"}),
		cand("b", 1, []string{"svc/m.go", "svc/z.go"}), sib)
	if l == nil {
		t.Fatal("링크가 nil 이다")
	}
	// 표기: 선두 원문 그대로여야 한다. "svc/z.go" 만 보면 "./svc/z.go" 에도 걸려
	// 뒤바뀐 변이를 못 가르므로 선두 원문 전체를 본다.
	for _, want := range []string{"./svc/z.go", "svc//m.go"} {
		if !strings.Contains(l.Detail, want) {
			t.Fatalf("근거가 선두 원문 %q 가 아니라 이웃 표기를 실었다: %q", want, l.Detail)
		}
	}
	// 순서: 선두가 선언한 순서(z 먼저, m 나중)여야 한다.
	iz, im := strings.Index(l.Detail, "./svc/z.go"), strings.Index(l.Detail, "svc//m.go")
	if iz > im {
		t.Fatalf("근거의 경로 순서가 선두 선언 순서(z→m)가 아니다: %q", l.Detail)
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
// 전이를 허용하면 넓은 토큰 하나가 큐의 3분의 2를 한 묶음으로 만든다(이 저장소 실측).
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

// 정렬 키 ①′(기아)를 lessBundle 을 직접 불러 확인한다.
//
// ★ 이 축이 없으면 큐가 FIFO 로 보이면서 실제로는 LIFO 로 돈다.
// 실측(kweiza-cc-plugins · 판단 01KZAW342JAC6EAW8C31RCXXK0):
//
//	열린 26건의 의존자 분포 = {0: 26}      ← 키 ①이 상수다
//
// ①이 상수면 ②(묶음 크기)가 실질 1차 키가 된다. 그런데 묶음은
// (형제 판단 ∨ 같은 선행)으로만 서므로(LinkOf), 형제가 없는 항목은 묶음 크기가
// **영구히 1**이다. 그 결과가 이것이었다:
//
//	7.5h 이상 묵은 17건 — 전부 형제 0(단독)
//	6.5h 이하 8건       — 전부 형제 있음
//
// 경계가 하나뿐이다. ③(최고령)이 이미 있는데도 그렇다 — ③은 ②가 동점일 때만
// 발화하기 때문이다. 그리고 followups 로 만든 항목은 같은 판단에 FK 로 걸려
// **자동으로 서로 형제**라, 새 유입이 들어올 때마다 기존 단독 전부를 추월한다.
//
// ★ 축 격리: 이 시험은 ②③④를 **전부 big 편으로** 몰아 둔다(묶음 4건 · 더 오래됨 ·
// id 사전순 앞). 기아 축 하나만 solo 편이다. 그래서 이 축을 지우면 반드시 잡힌다.
// "굶었는데 더 최근"은 부자연스러운 배치이지만, 축을 섞으면 무엇이 이겼는지
// 시험이 못 말한다.
func TestLessBundleStarvedBeatsLargerBundle(t *testing.T) {
	solo := Bundle{Lead: cand("z-starved", 0, nil), Oldest: t0.Add(72 * time.Hour), Starved: true}
	big := Bundle{
		Lead:    cand("a-fresh", 0, nil),
		Members: []Candidate{cand("m1", 0, nil), cand("m2", 0, nil), cand("m3", 0, nil)},
		Oldest:  t0,
	}
	if !lessBundle(solo, big) {
		t.Fatalf("굶은 단독이 4건 묶음에 밀렸다 — 이 축이 없으면 큐가 LIFO 로 돈다")
	}
	if lessBundle(big, solo) {
		t.Fatalf("역방향이 대칭이 아니다 — 안 굶은 묶음이 굶은 단독을 이겼다")
	}
}

// 기아 영역 안에서는 **묶음 크기를 안 본다** — 순수 최고령순이다.
//
// ★ 여기서 ②(묶음 크기)를 다시 넣으면 위 시험이 고발한 바로 그 함정이
// 기아 영역 **안에서 그대로 재현된다**: 굶은 단독이 굶은 묶음에 영구히 밀리고,
// 가장 오래 굶은 것이 영영 안 나온다. 기아는 예외 상태이고, 예외 상태에서
// 방어 가능한 규칙은 "가장 오래 굶은 것부터" 하나뿐이다.
//
// 축 격리는 위와 같은 방식이다 — ②(묶음 크기)와 ④(id)를 둘 다 newer 편에 둔다.
func TestLessBundleStarvedTiesGoToOldestNotBiggest(t *testing.T) {
	older := Bundle{Lead: cand("z-old", 0, nil), Oldest: t0, Starved: true}
	newer := Bundle{
		Lead:    cand("a-new", 0, nil),
		Members: []Candidate{cand("m1", 0, nil), cand("m2", 0, nil)},
		Oldest:  t0.Add(time.Hour),
		Starved: true,
	}
	if !lessBundle(older, newer) {
		t.Fatalf("둘 다 굶었는데 최고령이 아니라 큰 묶음이 이겼다 — 기아 영역에 같은 함정을 다시 들였다")
	}
	if lessBundle(newer, older) {
		t.Fatalf("역방향이 대칭이 아니다")
	}
}

// EligibleBundle 이 Now 로 기아를 판정하는지 — 배선이 실제로 이어져 있는지 본다.
//
// ★ lessBundle 단위 시험만 있으면 Starved 를 **아무도 안 채우는** 상태가 통과한다.
// 이 저장소가 여러 번 겪은 실패 모양이다(계산은 되는데 읽는 쪽이 0건).
func TestEligibleBundleMarksStarvation(t *testing.T) {
	// 단독 최고령 vs 형제 둘. 형제 쪽이 묶음 크기로 이기게 두고, 나이로만 뒤집는다.
	old := cand("z-old-solo", 0, nil)
	n1 := cand("a-new-1", 600, nil)
	n2 := cand("a-new-2", 600, nil)
	sib := SiblingIndex{"a-new-1": {"J1"}, "a-new-2": {"J1"}}

	// 기아 전: 형제 묶음이 이긴다(지금 거동 그대로).
	best, _ := EligibleBundle(EligibleInput{
		Candidates: []Candidate{old, n1, n2},
		Now:        t0.Add(time.Hour),
	}, sib)
	if best == nil || best.Lead.Item.ID != "a-new-1" {
		t.Fatalf("기아 전에는 형제 묶음이 선두여야 한다 — 평시 순서가 바뀌었다: %v", best)
	}

	// 기아 후: 단독 최고령이 이긴다.
	best, _ = EligibleBundle(EligibleInput{
		Candidates: []Candidate{old, n1, n2},
		Now:        t0.Add(StarvationAge + time.Minute),
	}, sib)
	if best == nil || best.Lead.Item.ID != "z-old-solo" {
		t.Fatalf("임계를 넘긴 단독이 선두가 아니다 — Starved 가 안 채워졌거나 배선이 끊겼다: %v", best)
	}
	if !best.Starved {
		t.Fatalf("선두가 굶었는데 Starved 가 false 다 — Reason 이 근거를 못 낸다")
	}
}

// 티클러는 아무리 늙어도 묶음을 기아 승격시키지 않는다 — 기한을 기다리는 항목이
// 매번 1순위로 추천되면 굶김 축의 판별력이 0이 된다(§4). 배제는 아니다: 티클러도
// 여전히 적격이고, 같은 묶음의 비티클러가 임계를 넘기면 그 나이로는 굶는다.
func TestEligibleBundleTicklerDoesNotStarve(t *testing.T) {
	tick := cand("z-tickler-old", 0, nil)
	tick.Item.Labels = []string{"tickler"}
	n1 := cand("a-new-1", 600, nil)
	n2 := cand("a-new-2", 600, nil)
	sib := SiblingIndex{"a-new-1": {"J1"}, "a-new-2": {"J1"}}

	// 티클러 단독이 임계를 넘겨도 형제 묶음이 그대로 이긴다 — 기아 승격이 없다.
	best, _ := EligibleBundle(EligibleInput{
		Candidates: []Candidate{tick, n1, n2},
		Now:        t0.Add(StarvationAge + time.Minute),
	}, sib)
	if best == nil || best.Lead.Item.ID != "a-new-1" {
		t.Fatalf("티클러의 나이가 기아 승격을 만들었다 — 기한 대기 항목이 매번 1순위가 된다: %+v", best)
	}

	// 대조: 같은 나이의 비티클러는 굶는다(기존 거동이 그대로 산다는 확인).
	old := cand("z-old-solo", 0, nil)
	best, _ = EligibleBundle(EligibleInput{
		Candidates: []Candidate{old, n1, n2},
		Now:        t0.Add(StarvationAge + time.Minute),
	}, sib)
	if best == nil || best.Lead.Item.ID != "z-old-solo" || !best.Starved {
		t.Fatalf("대조가 깨졌다 — 비티클러 기아 승격이 사라졌다: %+v", best)
	}
}

// Now 를 안 준 호출은 기아 판정을 **안 돌린다**.
//
// ★ zero time 을 그대로 쓰면 모든 항목이 "1년 넘게 굶었다"로 판정돼 묶음 기능이
// 통째로 죽는다. 관측을 못 하면 판정하지 않는다 — 이 저장소의 fail-open 규율이
// judgeMissingFollowups·followupCandidates 에서 이미 같은 모양으로 서 있다.
func TestEligibleBundleWithoutNowDoesNotStarve(t *testing.T) {
	old := cand("z-old-solo", 0, nil)
	n1 := cand("a-new-1", 600, nil)
	n2 := cand("a-new-2", 600, nil)
	sib := SiblingIndex{"a-new-1": {"J1"}, "a-new-2": {"J1"}}

	best, _ := EligibleBundle(EligibleInput{Candidates: []Candidate{old, n1, n2}}, sib)
	if best == nil {
		t.Fatalf("적격이 있는데 nil 이 나왔다")
	}
	if best.Starved {
		t.Fatalf("Now 가 zero 인데 굶었다고 판정했다 — 묶음 기능이 통째로 죽는다")
	}
	if best.Lead.Item.ID != "a-new-1" {
		t.Fatalf("Now 없는 호출의 순서가 기존과 달라졌다: %s", best.Lead.Item.ID)
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

// overlapIDs 는 겹침으로 보고된 세션 id 를 보고된 순서대로 편다.
// 집합이 아니라 **슬라이스**로 보는 이유는, 하나가 빠졌을 때 "누가 빠졌나"를
// 실패 메시지가 바로 말하게 하기 위해서다.
func overlapIDs(os []Overlap) []string {
	out := make([]string, 0, len(os))
	for _, o := range os {
		out = append(out, o.SessionID)
	}
	return out
}

// 묶음 겹침은 **자기 카드도** 건너뛴다 — cc 를 못 읽은 세션에서도 그래야 한다.
//
// ★ 위 TestEligibleBundleOverlapsSkipSiblingCardOfSameConversation 이 "self(id 일치)와
// 형제(cc 일치)를 한 시험에 같이 둔다"고 적었지만, 그 시험의 자기 카드(S1)는 SelfCC 와
// **같은 cc** 도 달고 있어 형제 규칙 하나만으로도 걸러진다. 그래서 id 비교 쪽은
// 사실상 안 물려 있었다 — 실측으로 `OverlapsWithLive(…, in.Self, …)` 의 Self 인자를
// 통째로 "" 로 바꿔도 전 스위트가 초록이었다.
//
// 그 상태에서 무엇이 깨지나: cc 관측이 실패한 세션(SelfCC="")은 형제 규칙이 안 도는데
// id 규칙마저 없으면 **자기 발자국이 자기에게 겹침으로 보고된다.** 착수 직후 footprint 가
// 이미 그 경로를 담으므로 상시 점등이고, 상시 점등된 경고는 판별력이 0이 된다.
//
// 남(S8)을 함께 두는 이유는 늘 같다 — 자기를 빼는 것이 겹침 축을 통째로 끄는 것이면 안 된다.
func TestEligibleBundleOverlapsSkipOwnCardEvenWithoutCC(t *testing.T) {
	sib := SiblingIndex{"lead": {"J1"}, "mem": {"J1"}}
	in := EligibleInput{
		Self: "S1", SelfCC: "", // 내 cc 를 못 읽었다 — 형제 규칙이 안 돈다
		Candidates: []Candidate{
			cand("lead", 0, []string{"lead-only.go"}),
			cand("mem", 1, []string{"member-only.go"}),
		},
		Live: []LiveSession{
			// 내 카드. cc 가 비어 있으므로 **id 로만** 걸러진다.
			{ID: "S1", CCSessionID: "", Paths: []string{"lead-only.go", "member-only.go"}},
			// 진짜 남. 저쪽 cc 도 비어 있다 — 빈 cc 둘은 형제가 아니다.
			{ID: "S8", CCSessionID: "", Paths: []string{"member-only.go"}},
		},
	}
	b, _ := EligibleBundle(in, sib)
	if got := overlapIDs(b.Lead.Overlaps); !reflect.DeepEqual(got, []string{"S8"}) {
		t.Fatalf("겹침이 %v 다 — 내 카드 S1 은 빠지고 남 S8 만 남아야 한다", got)
	}
}

// 묶음 경로는 **선두 ∪ 구성원 전부**다 — 셋 다 각각 확인한다.
//
// ★ 기존 두 시험(TestEligibleBundleOverlapsUseWholeBundlePaths ·
// TestEligibleBundleOverlapsCoverEveryMemberPath)은 구성원 **한 명**의 경로만
// 대조로 쓴다. 그래서 두 변이가 그 둘을 통과한다(실측: 둘 다 전 스위트 초록):
//
//	· bundlePaths 에서 `add(b.Lead.Item.Paths)` 를 지운다 → 선두 경로로 난 겹침이 사라진다.
//	  선두는 이 세션이 실제로 손댈 첫 파일이라, 가장 흔한 충돌이 통째로 조용해진다.
//	· 구성원 루프를 `b.Members[0]` 하나로 좁힌다 → 둘째 구성원부터가 안 보인다.
//	  묶음은 정의상 여럿을 함께 집는 기능이므로 이 침묵은 묶음이 클수록 커진다.
//
// 그래서 선두·첫 구성원·둘째 구성원의 경로를 **각각 한 세션씩만** 만지게 갈라 두고,
// 셋이 전부 나오는지 본다. 하나라도 빠지면 어느 쪽이 빠졌는지 메시지가 바로 말한다.
func TestEligibleBundleOverlapsCoverLeadAndEveryMember(t *testing.T) {
	sib := SiblingIndex{"a-lead": {"J1"}, "b-mem1": {"J1"}, "c-mem2": {"J1"}}
	in := EligibleInput{
		Self: "S1",
		Candidates: []Candidate{
			cand("a-lead", 0, []string{"lead-only.go"}),
			cand("b-mem1", 1, []string{"m1-only.go"}),
			cand("c-mem2", 2, []string{"m2-only.go"}),
		},
		Live: []LiveSession{
			{ID: "S-lead", Paths: []string{"lead-only.go"}},
			{ID: "S-m1", Paths: []string{"m1-only.go"}},
			{ID: "S-m2", Paths: []string{"m2-only.go"}},
		},
	}
	b, rej := EligibleBundle(in, sib)
	if b == nil {
		t.Fatalf("적격이 있는데 묶음이 nil 이다(탈락 %v)", rej)
	}
	// 전제: 셋이 정말 한 묶음인가. 아니면 아래 단정은 합집합을 증명하지 못한다.
	if b.Lead.Item.ID != "a-lead" || !reflect.DeepEqual(memberIDs(b), []string{"b-mem1", "c-mem2"}) {
		t.Fatalf("전제가 깨졌다 — 선두 %q 구성원 %v(a-lead + [b-mem1 c-mem2] 를 기대)",
			b.Lead.Item.ID, memberIDs(b))
	}
	want := []string{"S-lead", "S-m1", "S-m2"}
	if got := overlapIDs(b.Lead.Overlaps); !reflect.DeepEqual(got, want) {
		t.Fatalf("겹침이 %v 다 — %v 를 기대했다(선두 ∪ 구성원 전부의 합집합이어야 한다)", got, want)
	}
}

// 묶음 경로의 중복 제거와 원문 표기를 겹침 **쌍** 좌표계에서 못박는다.
//
// ★ 선두와 구성원이 같은 파일을 서로 다른 표기로 선언하는 것은 실제로 흔하다
// (한쪽은 `./x`, 한쪽은 `x`). 그때 두 가지가 조용히 무너진다 — 둘 다 실측으로
// 전 스위트 초록이었다:
//
//	· `!seen[n]` 을 지우면 같은 파일이 쌍 **두 줄**로 뜬다. OverlapPairs 는 원문
//	  문자열로 쌍을 접으므로 표기가 다르면 접지 못한다. 사람은 충돌이 둘이라고 읽는다.
//	· `out = append(out, p)` 를 정규형 `n` 으로 바꾸면 화면의 왼쪽 경로가 항목이 적은
//	  줄과 달라진다 — SamePaths 주석이 원문을 고집하는 이유와 같은 사고다.
//
// 그래서 쌍을 통째로 단정한다. len 만 보면 표기 변이가, 표기만 보면 중복 변이가 샌다.
func TestEligibleBundleOverlapPairsDedupeAndKeepDeclaredSpelling(t *testing.T) {
	sib := SiblingIndex{"a-lead": {"J1"}, "b-mem": {"J1"}}
	in := EligibleInput{
		Self: "S1",
		Candidates: []Candidate{
			cand("a-lead", 0, []string{"./shared/x.go"}), // 같은 파일, 다른 표기
			cand("b-mem", 1, []string{"shared//x.go"}),
		},
		Live: []LiveSession{{ID: "S9", Paths: []string{"shared/x.go"}}},
	}
	b, rej := EligibleBundle(in, sib)
	if b == nil {
		t.Fatalf("적격이 있는데 묶음이 nil 이다(탈락 %v)", rej)
	}
	if b.Lead.Item.ID != "a-lead" || len(b.Members) != 1 {
		t.Fatalf("전제가 깨졌다 — 선두 %q 구성원 %v", b.Lead.Item.ID, memberIDs(b))
	}
	if len(b.Lead.Overlaps) != 1 {
		t.Fatalf("겹친 세션이 %d건이다 — S9 하나여야 한다: %+v", len(b.Lead.Overlaps), b.Lead.Overlaps)
	}
	want := [][2]string{{"./shared/x.go", "shared/x.go"}}
	if got := b.Lead.Overlaps[0].Pairs; !reflect.DeepEqual(got, want) {
		t.Fatalf("겹친 쌍이 %v 다 — %v 를 기대했다(같은 파일은 한 줄, 왼쪽은 선두가 적은 원문)", got, want)
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

// 흡수된 구성원의 링크는 축이 정확히 [after] 다.
//
// ★ 위 시험은 흡수 링크의 Detail 만 본다. 그래서 Axes 를 [sibling] 으로 바꾸거나
// 통째로 비워도 전 스위트가 초록이었다(실측). Link 주석이 "축을 뭉개지 않는다"고
// 적어 둔 자리가 정작 흡수 경로에서만 안 물려 있었다.
//
// 무엇이 깨지나. [sibling] 이면 화면이 "같은 판단에 함께 매달렸다"고 말하는데
// 이 둘을 잇는 것은 판단이 아니라 선행이다 — 근거가 거짓이 되고, 사람은 있지도 않은
// 판단을 찾으러 간다. 비면 "셋 다 맞는 쌍"과 "이유를 모르는 쌍"이 화면에서 같아진다.
func TestEligibleBundleAbsorbedLinkCarriesAfterAxisOnly(t *testing.T) {
	in := EligibleInput{
		Self: "S1",
		Candidates: []Candidate{
			cand("B-blocker", 0, nil),
			cand("A-blocked", 1, nil, afterItem("B-blocker")),
		},
		Facts: AfterFacts{ItemStates: map[string]model.ItemState{"B-blocker": model.ItemOpen}},
	}
	b, rej := EligibleBundle(in, SiblingIndex{})
	if b == nil {
		t.Fatalf("묶음이 nil 이다(탈락 %v)", rej)
	}
	found := false
	for i, m := range b.Members {
		if m.Item.ID != "A-blocked" {
			continue
		}
		found = true
		if !reflect.DeepEqual(b.Links[i].Axes, []BundleAxis{AxisAfter}) {
			t.Fatalf("흡수 링크의 축이 %v 다 — 흡수는 선행 축(after) 하나다", b.Links[i].Axes)
		}
	}
	if !found {
		t.Fatalf("전제가 깨졌다 — 막힌 항목이 흡수되지 않았다: 구성원 %v", memberIDs(b))
	}
}

// 묶음의 Dependents·Oldest 는 **구성원까지 합친** 값이다.
//
// ★ 이 둘은 정렬 키 ①·③의 입력이자 Reason 에 찍히는 값인데, 지금까지는 구성원의
// 기여가 안 물려 있었다 — `b.Dependents += c.Dependents` 를 지우거나
// Oldest 갱신을 지워도 전 스위트가 초록이었다(실측). lessBundle 자체는
// TestLessBundlePrefersOlder 등이 손으로 만든 Bundle 로 직접 부르므로, 그 값을
// **누가 채우나**는 그 시험들이 안 본다.
//
// 무엇이 깨지나. Dependents 가 선두 것만이면 "이걸 풀어야 남이 움직이는 정도"가
// 묶음 크기와 무관해져 키 ①이 묶음을 과소평가한다. Oldest 가 선두 것만이면
// 오래 방치된 구성원을 끌고 있는 묶음이 키 ③에서 젊게 보인다.
//
// 여기서는 선두가 **더 젊고 의존자가 더 적게** 되도록 일부러 배치한다 —
// 선두 값이 그대로 새면 두 단정이 동시에 붉어진다.
func TestBundleAggregatesDependentsAndOldestFromMembers(t *testing.T) {
	lead := cand("a-lead", 5, nil) // 나중에 생성됨
	lead.Dependents = 2
	mem := cand("z-mem", 0, nil) // 더 오래됨
	mem.Dependents = 3

	sib := SiblingIndex{"a-lead": {"J1"}, "z-mem": {"J1"}}
	b, rej := EligibleBundle(EligibleInput{Self: "S1", Candidates: []Candidate{lead, mem}}, sib)
	if b == nil {
		t.Fatalf("묶음이 nil 이다(탈락 %v)", rej)
	}
	// 전제: 선두가 a-lead 인가. 합산이 사라지면 z-mem(의존자 3)이 키 ①로 이겨
	// 여기서 먼저 붉어진다 — 그것도 정당한 실패다.
	if b.Lead.Item.ID != "a-lead" || !reflect.DeepEqual(memberIDs(b), []string{"z-mem"}) {
		t.Fatalf("전제가 깨졌다 — 선두 %q 구성원 %v", b.Lead.Item.ID, memberIDs(b))
	}
	if b.Dependents != 5 {
		t.Fatalf("의존자 합이 %d 다 — 선두 2 + 구성원 3 = 5 여야 한다", b.Dependents)
	}
	if !b.Oldest.Equal(t0) {
		t.Fatalf("최고령이 %s 다 — 구성원 z-mem 의 생성 시각(%s)이어야 한다",
			b.Oldest.UTC(), t0.UTC())
	}
	if !strings.Contains(b.Reason, "의존자 합 5") {
		t.Fatalf("Reason 이 합산된 실제 값을 안 싣는다: %q", b.Reason)
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
