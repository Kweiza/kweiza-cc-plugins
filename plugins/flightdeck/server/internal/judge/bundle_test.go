package judge

import (
	"reflect"
	"strings"
	"testing"

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
