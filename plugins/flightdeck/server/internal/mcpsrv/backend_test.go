package mcpsrv

import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 이 파일의 소비자 좌표계는 **에이전트가 읽는 응답 문자열**과 **isError 불리언**이다.
// 판정을 순수 함수로 빼 둔 자리라 시험이 그 함수를 직접 부른다 — 본문에 흩어 두면
// 시험이 사본을 단정하게 되고, 그러면 변이가 조용히 샌다.

func TestDegradedIsErrorSeparatesDoneFromNotDone(t *testing.T) {
	// "쌓아 뒀다"를 실패로 내면 세션이 판단이 사라진 줄 알고 다시 쓴다.
	// "안 했다"를 성공으로 내면 남의 항목 위에서 일한다. 둘 다 이 도구가 없애려는 사고다.
	for _, c := range []struct {
		mode DegradedMode
		want bool
	}{
		{DegradedCache, false},
		{DegradedOutbox, false},
		{DegradedDrop, false},
		{DegradedRefuse, true},
		{DegradedMode("무엇인지 모르는 처방"), true}, // ★ 모르는 것은 성공으로 접지 않는다
		{DegradedMode(""), true},
	} {
		if got := DegradedIsError(c.mode); got != c.want {
			t.Errorf("DegradedIsError(%q)=%v, want %v", c.mode, got, c.want)
		}
	}
}

func TestDegradedUsableIsOnlyCache(t *testing.T) {
	// 값과 **함께** 오는 처방은 cache 하나뿐이다. 나머지에 값이 있다고 믿으면
	// 빈 구조체가 "세션 0건"·"항목 없음"이라는 사실인 척한다.
	if !DegradedUsable(DegradedCache) {
		t.Error("cache 가 쓸 수 없는 값으로 판정됐다 — 캐시된 보드는 낡았지만 쓸 수 있는 값이다")
	}
	for _, m := range []DegradedMode{DegradedOutbox, DegradedDrop, DegradedRefuse, "새 처방"} {
		if DegradedUsable(m) {
			t.Errorf("%q 가 값을 써도 되는 것으로 판정됐다", m)
		}
	}
}

func TestRenderDegradedSaysWhatItDidAndWhy(t *testing.T) {
	got := RenderDegraded(&Degraded{
		What: "note", Mode: DegradedOutbox,
		Reason: "판단은 원리적으로 파생 불가한 유일한 자산이다",
		Banner: "⚠ 조정 서버 미도달(http://x, 마지막 접속 14:02 · 37분 전).",
	})
	for _, want := range []string{
		"note", "조정 서버 미도달",
		"아웃박스에 쌓았다", "아직 서버에 없어 다른 세션은 못 본다",
		"판단은 원리적으로 파생 불가한 유일한 자산이다",
		"마지막 접속 14:02",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("열화 응답에 %q 가 없다:\n%s", want, got)
		}
	}

	// 배너가 없어도 무엇을 했고 왜인지는 남는다 — 배너는 보조 축이다.
	bare := RenderDegraded(&Degraded{What: "pick", Mode: DegradedRefuse, Reason: "배타는 서버만 보장한다"})
	if !strings.Contains(bare, "하지 않았다") || !strings.Contains(bare, "배타는 서버만 보장한다") {
		t.Errorf("배너 없는 열화 응답이 반쪽이다:\n%s", bare)
	}

	// 모르는 처방을 조용히 그럴듯한 말로 덮지 않는다.
	unknown := RenderDegraded(&Degraded{What: "board", Mode: "새 처방", Reason: "사유"})
	if !strings.Contains(unknown, "모른다") {
		t.Errorf("모르는 처방인데 무엇을 했다고 단정한다:\n%s", unknown)
	}
}

func TestFilterNotesDropsOwnAndKeepsNewestFirst(t *testing.T) {
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	all := []model.Judgment{
		{ID: "a", SessionID: "me", At: base.Add(3 * time.Minute), Kind: model.JudgmentAsk},
		{ID: "b", SessionID: "other", At: base.Add(1 * time.Minute), Kind: model.JudgmentAsk},
		{ID: "c", SessionID: "other", At: base.Add(2 * time.Minute), Kind: model.JudgmentBlocked},
		{ID: "d", SessionID: "third", At: base, Kind: model.JudgmentBlocked},
	}
	got := FilterNotes(all, "me", 2)
	if len(got) != 2 {
		t.Fatalf("상한 2인데 %d건이다: %+v", len(got), got)
	}
	if got[0].ID != "c" || got[1].ID != "b" {
		t.Fatalf("최신순이 아니다: %s %s", got[0].ID, got[1].ID)
	}
	for _, j := range got {
		if j.SessionID == "me" {
			t.Fatal("자기가 쓴 판단이 알림으로 돌아왔다 — 자기 노트는 알림이 아니다")
		}
	}

	// self 가 없으면 뺄 좌표가 없다. 조용히 아무거나 빼지 않는다.
	if n := len(FilterNotes(all, "", 0)); n != 4 {
		t.Fatalf("self 없이 %d건이 남았다 — 뺄 좌표가 없으면 아무것도 안 뺀다", n)
	}
}
