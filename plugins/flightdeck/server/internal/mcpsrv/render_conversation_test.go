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
// 같아 안 잡혔다 — 검토가 뮤테이션으로 확인한 결함이다. 여섯 필드도 26/4/3 · 9/5/2 로
// 서로 다르게 둬 순서가 바뀌거나 두 구간이 뒤집히면 문장 자체가 달라지게 한다.
//
// ★ Window 를 일부러 **24시간이 아닌** 30시간으로 둔다. 운영값과 같은 값을 쓰면
// 렌더러가 구간을 문자열로 박아 넣어도(즉 v.AckReach.Window 를 안 읽어도) 초록이라,
// 상수를 바꾸는 날 문구만 조용히 낡는다 — render.go 의 창 문구 규율이 막으려는 것이 그것이다.
func TestRenderBoardDetailShowsAckReach(t *testing.T) {
	now := time.Now()
	cards := []service.SessionCard{convCard("s1", "cc-a", "/repo", "a.go")}
	v := service.BoardView{
		Project: model.Project{ID: "p"}, At: now, Window: 2 * time.Hour,
		Sessions: cards,
		AckReach: &service.AckReach{
			AllTime: service.AckCounts{Emitted: 26, Reachable: 4, Acked: 3},
			Recent:  service.AckCounts{Emitted: 9, Reachable: 5, Acked: 2},
			Window:  30 * time.Hour,
		},
	}
	got := RenderBoard(v, BoardRenderOptions{Now: now, Detail: true})
	// 최근 벌이 먼저다 — "지금 규율"을 묻는 쪽이 이것이고, 전 역사는 배경이다.
	wantRecent := "확인율 최근 1일 6시간 — 발화 대화 9 · 그중 ack 이 닿을 수 있는 대화 5 · 실제 ack 2 " +
		"(앞 두 수가 크게 다르면 처방을 받고 판단을 안 남긴 대화가 그만큼이다)"
	if !strings.Contains(got, wantRecent) {
		t.Fatalf("최근 구간 확인율 줄이 기대 문장과 다르다.\nwant 포함: %q\ngot:\n%s", wantRecent, got)
	}
	// ★ 전 역사 줄은 **자기가 무엇인지 말해야 한다.** 구간 라벨 없이 세 수만 있으면
	// 다음 사람이 그것을 "지금 값"으로 읽는다 — 이 항목이 고치려던 결함 자체다.
	wantAll := "확인율 전 역사 — 발화 대화 26 · 그중 ack 이 닿을 수 있는 대화 4 · 실제 ack 3 " +
		"(분모가 단조 증가한다 — 추세로만 읽어라)"
	if !strings.Contains(got, wantAll) {
		t.Fatalf("전 역사 확인율 줄이 기대 문장과 다르다.\nwant 포함: %q\ngot:\n%s", wantAll, got)
	}

	// 대조 ② — 전 역사 Emitted==0 이면(처방이 아예 안 떴으면) 지표 자체가 의미 없으므로
	// 아무것도 안 찍는다(`r.AllTime.Emitted > 0` 가드). "want: 0" 이 "안 나왔다"가 아니라
	// "판정을 못 했다"로 새는 것을 막는 자리라 반드시 함께 잠근다.
	// 최근만 0인 경우는 침묵하지 않는다 — 그것은 "이 구간에 처방이 없었다"는 사실이다.
	v.AckReach = &service.AckReach{Window: 30 * time.Hour}
	zeroGot := RenderBoard(v, BoardRenderOptions{Now: now, Detail: true})
	if strings.Contains(zeroGot, "확인율") {
		t.Fatalf("발화 0건(AllTime.Emitted==0)인데 확인율 줄이 찍혔다:\n%s", zeroGot)
	}

	// 대조 ③ — 전 역사는 있는데 **최근만 0**이면 침묵하지 않는다. 그 0 은 "이 구간에 처방이
	// 없었다"는 사실이고, 접으면 "안 나왔다"와 "못 쟀다"가 화면에서 같아진다.
	//
	// ★ 이 갈래가 없으면 게이트를 AllTime→Recent 로 바꾸는 변이가 살아남는다. 그 변이가
	// 들어가면 주말이나 배포 직후처럼 최근 처방이 0인 프로젝트에서 확인율 **두 줄이 통째로
	// 사라져**, 전 역사 수치조차 안 보인다 — 대조 ②가 잠근 것과 정반대 방향의 침묵이다.
	v.AckReach = &service.AckReach{
		AllTime: service.AckCounts{Emitted: 26, Reachable: 4, Acked: 3},
		Recent:  service.AckCounts{},
		Window:  30 * time.Hour,
	}
	quietGot := RenderBoard(v, BoardRenderOptions{Now: now, Detail: true})
	wantQuiet := "확인율 최근 1일 6시간 — 발화 대화 0 · 그중 ack 이 닿을 수 있는 대화 0 · 실제 ack 0 " +
		"(앞 두 수가 크게 다르면 처방을 받고 판단을 안 남긴 대화가 그만큼이다)"
	if !strings.Contains(quietGot, wantQuiet) {
		t.Fatalf("최근 구간 처방이 0인데 그 줄이 안 찍혔다 — 0(안 나왔다)이 침묵(못 쟀다)으로 샜다.\n"+
			"want 포함: %q\ngot:\n%s", wantQuiet, quietGot)
	}
	if !strings.Contains(quietGot, "확인율 전 역사 — 발화 대화 26") {
		t.Fatalf("최근이 0이라고 전 역사 줄까지 사라졌다:\n%s", quietGot)
	}
}
