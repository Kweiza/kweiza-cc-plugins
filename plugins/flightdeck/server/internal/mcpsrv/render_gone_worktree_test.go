package mcpsrv

import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 서버가 이 조회에서 닫은 카드를 보드가 **말한다.**
//
// ★ 그 카드는 v.Sessions 에서 이미 빠져 있다. 이 줄이 없으면 창 밖으로 밀려난 카드와 서버가
// 닫은 카드가 화면에서 같고, 사람은 유령이 왜 사라졌는지 원장을 뒤져야 안다.
// 간단 화면과 detail 둘 다 본다 — foot 은 두 갈래가 따로 조립된다.
func TestBoardSaysWhichCardsThisCallClosedBecauseTheirWorktreeIsGone(t *testing.T) {
	now := time.Date(2026, 9, 17, 3, 0, 0, 0, time.UTC)
	v := service.BoardView{
		At:       now,
		Window:   2 * time.Hour,
		Sessions: []service.SessionCard{{View: model.SessionView{Session: model.Session{ID: "01MAINCARD000"}}}},
		ClosedGoneWorktree: []service.GoneWorktreeClosure{
			{SessionID: "01GHOSTCARD00", Worktree: "/r/.flightdeck/worktrees/fd-x"},
		},
	}
	for _, detail := range []bool{false, true} {
		got := RenderBoard(v, BoardRenderOptions{Now: now, Detail: detail})
		if !strings.Contains(got, "사라진 카드 1장을 이 조회가 닫았다") || !strings.Contains(got, ShortID("01GHOSTCARD00")) {
			t.Fatalf("detail=%v: 이 조회가 닫은 카드를 안 말한다:\n%s", detail, got)
		}
		// "다시 열린다"는 mcp 카드를 안 닫는다는 전제 위에서만 참이다 — 문장이 그 전제를 말해야 한다.
		if !strings.Contains(got, "mcp 신호가 없는 카드만 닫으므로") || !strings.Contains(got, "다시 열린다") {
			t.Fatalf("detail=%v: 되열림과 그 전제(mcp 카드는 안 닫는다)를 함께 말하지 않는다:\n%s", detail, got)
		}
		if strings.Contains(got, "죽") {
			t.Fatalf("detail=%v: 생존 판정 낱말이 들어갔다 — 설계 §4 위반:\n%s", detail, got)
		}
	}

	// 닫은 것이 없으면 줄 자체가 없다(상시 점등은 판별력 0 — 설계 §4).
	v.ClosedGoneWorktree = nil
	if got := RenderBoard(v, BoardRenderOptions{Now: now}); strings.Contains(got, "이 조회가 닫았다") {
		t.Fatalf("닫은 카드가 없는데 줄이 떴다:\n%s", got)
	}
}
