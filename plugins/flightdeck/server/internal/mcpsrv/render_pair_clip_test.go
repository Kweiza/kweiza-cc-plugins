package mcpsrv

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// 이 항목의 원래 증상 — 소비자 좌표계(꼬리 문자열)로 단정한다(설계 §12).
//
// 한 세션과 겹친 경로가 6개고 **제일 큰 개정이 알파벳상 다섯째**에 있으면, 정렬 전에는
// 보이는 넷이 전부 작은 것이었다. 머리줄은 "상대 규모 큰 순"이라 말하는데 그 세션이 왜
// 맨 위인지 화면에서 확인할 수 없었다 — 화면이 말 안 한 주장을 하는 상태다.
func TestTailShowsTheBiggestPairEvenWhenItSortsLateByName(t *testing.T) {
	// judge 를 거쳐 만든다 — 정렬이 원천에 있다는 사실까지 함께 잠근다.
	live := []judge.LiveSession{{
		ID: "01KZP9EFAAAAAAAAAAAAAAAAAA", CCSessionID: "cc-other",
		Paths: []string{"a.go", "b.go", "c.go", "d.go", "zzz-big.go", "f.go"},
		Delta: map[string]model.LineDelta{
			"a.go": {Added: 1}, "b.go": {Added: 1}, "c.go": {Added: 1},
			"d.go": {Added: 1}, "f.go": {Added: 1},
			"zzz-big.go": {Added: 300, Removed: 12}, // 알파벳상 뒤인데 제일 크다
		},
	}}
	overlaps := judge.OverlapsWithLive(
		[]string{"a.go", "b.go", "c.go", "d.go", "zzz-big.go", "f.go"},
		live, "me", "cc-me")

	out := RenderTail(TailInput{NotesObserved: true, OverlapsObserved: true, Overlaps: overlaps})

	if !strings.Contains(out, "zzz-big.go↔zzz-big.go(+300/-12)") {
		t.Fatalf("제일 큰 쌍이 화면에 없다 — 머리줄이 '상대 규모 큰 순'이라 말하는데 "+
			"그 근거가 안 보인다:\n%s", out)
	}
	// 그리고 잘린 쪽은 작은 것들이다.
	if !strings.Contains(out, "+2") {
		t.Errorf("쌍 절단 표기가 없다(겹친 경로 6개, 상한 4):\n%s", out)
	}
}

// 못 읽은 쌍은 쌍 층에서도 맨 앞이다 — 절단에 제일 먼저 걸리면 안 된다.
// 세션 층과 **같은 규칙**이라는 것을 화면에서 확인한다.
func TestTailKeepsUnknownSizedPairFromBeingClippedAway(t *testing.T) {
	live := []judge.LiveSession{{
		ID: "01KZPBB3AAAAAAAAAAAAAAAAAA", CCSessionID: "cc-other",
		Paths: []string{"a.go", "b.go", "c.go", "d.go", "e.go", "zz-unknown.go"},
		Delta: map[string]model.LineDelta{
			"a.go": {Added: 9}, "b.go": {Added: 8}, "c.go": {Added: 7},
			"d.go": {Added: 6}, "e.go": {Added: 5},
			// zz-unknown.go 는 키가 없다 — 못 읽었다
		},
	}}
	overlaps := judge.OverlapsWithLive(
		[]string{"a.go", "b.go", "c.go", "d.go", "e.go", "zz-unknown.go"},
		live, "me", "cc-me")

	out := RenderTail(TailInput{NotesObserved: true, OverlapsObserved: true, Overlaps: overlaps})

	if !strings.Contains(out, "zz-unknown.go↔zz-unknown.go(규모?)") {
		t.Fatalf("못 읽은 쌍이 절단에 걸려 사라졌다 — 아래로 밀면 제일 먼저 사라지는 것이 "+
			"못 잰 것이 된다:\n%s", out)
	}
}
