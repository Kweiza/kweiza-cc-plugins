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

// splitBanner 는 보고 수가 아니라 **서로 다른 cc 수**를 센다.
// 픽스처가 보고를 늘 1건만 넣으면 두 수가 같아서 이 로직이 무시험이 된다
// (검토가 뮤테이션으로 확인 — len(reports) 로 바꿔도 초록이었다).
func TestSplitBannerCountsConversationsNotReports(t *testing.T) {
	now := time.Now()
	v := service.BoardView{
		Project: model.Project{ID: "p"}, At: now, Window: 2 * time.Hour,
		// 같은 대화(cc-a)가 트리 둘에서 각각 안 접힌 모양 — 보고는 2건, 대화는 1개다.
		Splits: []judge.SplitReport{
			{CCSessionID: "cc-a", MachineID: "m", Root: "/repo",
				Recorded: []string{"/repo", "/repo/sub"}, SessionIDs: []string{"s1", "s2"}},
			{CCSessionID: "cc-a", MachineID: "m", Root: "/other",
				Recorded: []string{"/other", "/other/sub"}, SessionIDs: []string{"s3", "s4"}},
		},
	}
	out := RenderBoard(v, BoardRenderOptions{Now: now})
	if !strings.Contains(out, "대화 1개") {
		t.Fatalf("보고 2건이 전부 같은 cc 인데 배너가 대화 1개라고 안 한다 — "+
			"보고 수를 그대로 세면 이 배너가 고치려던 부풀림을 스스로 저지른다:\n%s", out)
	}
	if strings.Contains(out, "대화 2개의 카드가 상하위") {
		t.Fatalf("배너가 보고 수(2)를 대화 수로 냈다:\n%s", out)
	}
}

// TestRenderBoardDetailShowsAckReach 는 boardDetailFoot 이 v.AckReach 를 detail
// 꼬리에 내는지 잠근다(§10, prescribe_reach.go). 이 한 줄이 없어도 store·service
// 시험은 전부 초록이다 — 값이 정확히 계산되고 BoardView 까지 배선돼도, 화면에 내는
// 마지막 한 줄이 없으면 이 지표는 아무에게도 안 보인다.
//
// ★ 문장을 **통째로** 대조한다("26"·"4" 가 어딘가에 있는지만 보던 앞선 판은
// `printf` 인자 순서를 뒤바꿔도(예: `r.Reachable, r.Emitted, r.Acked`) 숫자 집합이
// 같아 안 잡혔다 — 검토가 뮤테이션으로 확인한 결함이다. 세 필드도 26/4/3 으로 서로
// 다르게 둬 순서가 바뀌면 문장 자체가 달라지게 한다.
func TestRenderBoardDetailShowsAckReach(t *testing.T) {
	now := time.Now()
	cards := []service.SessionCard{convCard("s1", "cc-a", "/repo", "a.go")}
	v := service.BoardView{
		Project: model.Project{ID: "p"}, At: now, Window: 2 * time.Hour,
		Sessions: cards,
		AckReach: &service.AckReach{Emitted: 26, Reachable: 4, Acked: 3},
	}
	got := RenderBoard(v, BoardRenderOptions{Now: now, Detail: true})
	want := "확인율 — 발화 카드 26 · 그중 ack 이 닿을 수 있는 카드 4 · 실제 ack 3 " +
		"(두 수가 크게 다르면 그 차이가 카드 갈림이다)"
	if !strings.Contains(got, want) {
		t.Fatalf("확인율 줄이 기대 문장과 다르다.\nwant 포함: %q\ngot:\n%s", want, got)
	}

	// 대조 ② — Emitted==0 이면(처방이 아예 안 떴으면) 지표 자체가 의미 없으므로
	// 아무것도 안 찍는다(`r.Emitted > 0` 가드). "want: 0" 이 "안 나왔다"가 아니라
	// "판정을 못 했다"로 새는 것을 막는 자리라 반드시 함께 잠근다.
	v.AckReach = &service.AckReach{}
	zeroGot := RenderBoard(v, BoardRenderOptions{Now: now, Detail: true})
	if strings.Contains(zeroGot, "확인율") {
		t.Fatalf("발화 0건(Emitted==0)인데 확인율 줄이 찍혔다:\n%s", zeroGot)
	}
}
