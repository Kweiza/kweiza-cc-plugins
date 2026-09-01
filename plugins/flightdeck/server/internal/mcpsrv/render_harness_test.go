package mcpsrv

import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 보드가 카드마다 **어느 도구의 것인지**를 찍는다.
//
// ★ 이것이 이 사슬의 마지막 칸이다. 원장에 남기기만 하고 화면에 안 내면 아무것도 안 한
// 것과 같다 — 겹침 처방을 읽는 사람이 "저건 codex 다"를 알아야 조율이 되고, 그 조율이
// 이 축을 만든 이유 전부다.

func harnessCard(id, harness string) service.SessionCard {
	return service.SessionCard{
		View: model.SessionView{
			Session: model.Session{
				ID: id, CCSessionID: "cc-" + id, Worktree: "/w/" + id,
				State: "active", Harness: harness,
			},
			Claims: []string{"item-" + id},
		},
	}
}

func harnessBoard(t *testing.T, cards ...service.SessionCard) string {
	t.Helper()
	now := time.Now()
	return RenderBoard(service.BoardView{
		Project:  model.Project{ID: "p"},
		At:       now,
		Window:   2 * time.Hour,
		Sessions: cards,
	}, BoardRenderOptions{Now: now})
}

func TestBoardCardNamesTheHarness(t *testing.T) {
	out := harnessBoard(t, harnessCard("s1", "codex"))
	if !strings.Contains(out, "codex") {
		t.Fatalf("보드가 하네스를 안 찍는다:\n%s", out)
	}
}

// ★ 선언이 없으면 「미상」이라고 **말한다.** 침묵하지 않는다.
//
// 침묵하면 "claude 다"와 "안 물어봤다"가 화면에서 똑같아진다 — 그 둘을 가르는 것이
// 이 축의 전부이므로, 빈 칸으로 두면 칼럼을 판 값이 화면에서 사라진다.
func TestBoardCardSaysUnknownWhenUndeclared(t *testing.T) {
	out := harnessBoard(t, harnessCard("s1", ""))
	if !strings.Contains(out, "미상") {
		t.Fatalf("선언이 없는데 보드가 「미상」을 안 찍는다 — 침묵과 claude 가 구별되지 않는다:\n%s", out)
	}
	// 지어내지 않는다 — 미선언 카드에 claude 가 찍히면 안 된다.
	if strings.Contains(out, "claude") {
		t.Fatalf("선언이 없는데 claude 가 찍혔다 — 접으면 그것이 곧 지어내는 것이다:\n%s", out)
	}
}
