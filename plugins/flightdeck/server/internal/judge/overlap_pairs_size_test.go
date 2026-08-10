package judge

import (
	"reflect"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 겹침 **세션**은 상대 규모 큰 순으로 서는데(SortOverlapsBySize) 한 세션 **안의 경로쌍**은
// 안 그랬다. 화면은 앞 4개만 내므로(mcpsrv.render 의 쌍 절단), 제일 큰 개정이 알파벳상
// 다섯째 경로에 있으면 그 세션이 맨 위에 서는데 보이는 넷은 전부 작은 것이 된다 —
// 머리줄이 "상대 규모 큰 순"이라 말하는데 화면이 그것을 못 보여 준다.
//
// ★ **규칙은 세션 층과 같은 하나다.** 못 읽은 것 먼저(+∞) · 그다음 증감합 내림차순 ·
// 동점은 경로 이름. 두 층에 다른 규칙을 두면 "큰 순"이라는 한 문장이 두 뜻이 된다.
func TestSortPairsBySizePutsUnknownFirstThenBiggest(t *testing.T) {
	o := Overlap{
		SessionID: "s-1",
		Pairs: [][2]string{
			{"a.go", "a.go"}, // +2
			{"b.go", "b.go"}, // 못 읽었다
			{"c.go", "c.go"}, // +300
			{"d.go", "d.go"}, // +5/-5 = 10
			{"e.go", "e.go"}, // +1
		},
		TheirDelta: map[string]model.LineDelta{
			"a.go": {Added: 2},
			"c.go": {Added: 300},
			"d.go": {Added: 5, Removed: 5},
			"e.go": {Added: 1},
		},
	}
	SortPairsBySize(&o)

	var got []string
	for _, p := range o.Pairs {
		got = append(got, p[1])
	}
	want := []string{"b.go", "c.go", "d.go", "a.go", "e.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("순서 %q 를 기대했는데 %q 다", want, got)
	}
}

// 동점은 경로 이름으로 가른다 — 결정적이어야 시험이 순서를 단정할 수 있고,
// 같은 입력에 같은 화면이 나와야 사람이 순서 변화를 신호로 읽는다.
func TestSortPairsBySizeIsDeterministicOnTies(t *testing.T) {
	mk := func() Overlap {
		return Overlap{
			Pairs: [][2]string{{"z.go", "z.go"}, {"a.go", "a.go"}, {"m.go", "m.go"}},
			TheirDelta: map[string]model.LineDelta{
				"z.go": {Added: 7}, "a.go": {Added: 7}, "m.go": {Added: 7},
			},
		}
	}
	o := mk()
	SortPairsBySize(&o)
	var got []string
	for _, p := range o.Pairs {
		got = append(got, p[1])
	}
	if want := []string{"a.go", "m.go", "z.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("동점은 경로 이름 오름차순이어야 한다: %q", got)
	}
}

// ★ **OverlapsWithLive 가 이미 세워서 낸다.** 호출부가 따로 정렬을 부르게 두면
// 그 호출을 빠뜨린 표면이 생기고, 그것이 곧 "판정이 두 자리"다.
func TestOverlapsWithLiveReturnsPairsAlreadySorted(t *testing.T) {
	live := []LiveSession{{
		ID: "s-1", CCSessionID: "cc-other",
		Paths: []string{"a.go", "z.go"},
		Delta: map[string]model.LineDelta{"a.go": {Added: 1}, "z.go": {Added: 99}},
	}}
	got := OverlapsWithLive([]string{"a.go", "z.go"}, live, "me", "cc-me")
	if len(got) != 1 {
		t.Fatalf("겹침 1건을 기대했는데 %d건이다", len(got))
	}
	if got[0].Pairs[0][1] != "z.go" {
		t.Errorf("큰 것(z.go +99)이 앞에 와야 한다: %v", got[0].Pairs)
	}
}

// ★★ **처방은 이 정렬에 안 걸린다 — 그것이 이 설계가 성립하는 조건이다.**
// judge/prescribe.go 의 overlapPrescriptions 는 Overlap.Pairs 를 안 읽고
// OverlapPairs 를 자기가 다시 부른다. 그래서 여기서 순서를 바꿔도 처방이 지목하는
// 경로("X 를 만졌는데 세션 Y 도 Z 를 잡고 있다")는 안 바뀐다 — 처방 경로는 git 을
// 안 돌아 규모를 원리적으로 모르므로(설계 §6), 걸렸다면 두 표면의 순서가 갈렸을 것이다.
func TestOverlapPairsIsUntouchedByPrescriptions(t *testing.T) {
	// OverlapPairs 자체는 정렬하지 않는다 — 입력 순서를 그대로 낸다.
	pairs := OverlapPairs([]string{"z.go", "a.go"}, []string{"a.go", "z.go"})
	if len(pairs) != 2 {
		t.Fatalf("쌍 2건을 기대했는데 %d건이다: %v", len(pairs), pairs)
	}
	if pairs[0][0] != "z.go" {
		t.Errorf("OverlapPairs 가 입력 순서를 안 지킨다: %v — "+
			"처방이 지목하는 경로가 이 함수의 첫 쌍이라 여기서 순서를 바꾸면 처방 문구가 바뀐다", pairs)
	}
}
