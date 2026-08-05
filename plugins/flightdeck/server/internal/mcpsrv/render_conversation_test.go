package mcpsrv

import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

func convCard(id, cc, wt string, paths ...string) service.SessionCard {
	return service.SessionCard{
		View: model.SessionView{
			Session: model.Session{ID: id, CCSessionID: cc, Worktree: wt, State: "active"},
			Paths:   paths,
		},
	}
}

func TestRenderBoardHeadCountsConversations(t *testing.T) {
	now := time.Now()
	cards := []service.SessionCard{
		convCard("s1", "cc-a", "/repo", "a.go"),
		convCard("s2", "cc-a", "/repo/sub", "b.go"),
		convCard("s3", "cc-b", "/other", "c.go"),
	}
	v := service.BoardView{
		Project: model.Project{ID: "p"}, At: now, Window: 2 * time.Hour,
		Sessions: cards, Conversations: service.FoldConversations(cards),
	}
	out := RenderBoard(v, BoardRenderOptions{Now: now})
	if !strings.Contains(out, "대화 2개(카드 3장)") {
		t.Fatalf("머리줄이 대화 수를 안 낸다:\n%s", out)
	}
	if strings.Contains(out, "살아 있는 세션 3건") {
		t.Fatalf("옛 머리줄이 남아 있다 — 3.2배로 부풀린 그 수다:\n%s", out)
	}
}

func TestRenderBoardShowsSplitBanner(t *testing.T) {
	now := time.Now()
	v := service.BoardView{
		Project: model.Project{ID: "p"}, At: now, Window: 2 * time.Hour,
		Splits: []judge.SplitReport{{
			CCSessionID: "cc-a", MachineID: "m", Root: "/repo",
			Recorded: []string{"/repo", "/repo/sub"}, SessionIDs: []string{"s1", "s2"},
		}},
	}
	out := RenderBoard(v, BoardRenderOptions{Now: now})
	if !strings.Contains(out, "상하위 경로로 갈렸다") {
		t.Fatalf("갈림 배너가 없다:\n%s", out)
	}
	if !strings.Contains(out, "정규화") {
		t.Fatalf("배너가 원인을 안 말한다 — 증상만 내면 다음 사람이 또 조사한다:\n%s", out)
	}
}

// 대조 단정 — 갈림이 없으면 배너가 **없어야** 한다.
// 항상 찍으면 배너가 배경이 되고, 배경은 아무도 안 읽는다.
func TestRenderBoardSilentWhenNoSplit(t *testing.T) {
	now := time.Now()
	v := service.BoardView{Project: model.Project{ID: "p"}, At: now, Window: 2 * time.Hour}
	out := RenderBoard(v, BoardRenderOptions{Now: now})
	if strings.Contains(out, "상하위 경로로 갈렸다") {
		t.Fatalf("갈림이 없는데 배너가 떴다:\n%s", out)
	}
}

func TestConversationCardFoldsByDefaultAndExpandsInDetail(t *testing.T) {
	now := time.Now()
	cards := []service.SessionCard{
		convCard("s1", "cc-a", "/repo", "a.go"),
		convCard("s2", "cc-a", "/repo/sub", "b.go"),
	}
	v := service.BoardView{
		Project: model.Project{ID: "p"}, At: now, Window: 2 * time.Hour,
		Sessions: cards, Conversations: service.FoldConversations(cards),
	}
	brief := RenderBoard(v, BoardRenderOptions{Now: now})
	if strings.Contains(brief, "/repo/sub") {
		t.Fatalf("기본 보드가 워크트리를 전개했다 — 예산이 그만큼 준다:\n%s", brief)
	}
	if !strings.Contains(brief, "카드 2장") {
		t.Fatalf("기본 보드가 카드 수를 안 낸다:\n%s", brief)
	}
	detail := RenderBoard(v, BoardRenderOptions{Now: now, Detail: true})
	if !strings.Contains(detail, "/repo/sub") {
		t.Fatalf("detail 이 워크트리별로 안 전개했다:\n%s", detail)
	}
}
