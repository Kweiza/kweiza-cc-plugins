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

// rankConversations 는 묶음의 IsSelf 를 본다 — 카드 id 비교만으로는 부족하다.
// 형제 카드가 갈려 있으면 내 카드 id 는 다른 묶음에 있을 수 있고, 그때 IsSelf 가
// 유일한 근거다.
//
// ★ 브리프 원안(대화 둘: 내 것 vs 남의 것)은 `case c.IsSelf: return 0` 을 지워도
// 초록으로 남는다 — 실제로 되돌려 확인했다. 이유: selfPaths(③ 경로 겹침 판정의
// 근거)가 IsSelf 묶음 자기 자신의 경로로 채워지므로, 그 묶음은 rank 0(IsSelf)이
// 없어도 rank 2(자기 경로와 자기 경로가 겹친다)로 떨어질 뿐이다. 남의 묶음 하나만
// 있는 원안에서는 rank 0 이든 rank 2 든 "내가 첫째"라는 결과가 똑같이 나와 결과로는
// 구분이 안 된다. 그래서 rank 1(사건이 붙은 형제)을 하나 더 둔다 — rank 0 이 죽어
// rank 2 로 떨어지면 rank 1 인 형제가 앞으로 와서 got[0]이 바뀐다. 이렇게 고쳐야
// `case c.IsSelf` 분기를 실제로 시험이 실행한다(원안 유지 시 어떤 시험도 실행하지
// 않았다는 것이 검토가 지적한 결함이다).
func TestRankConversationsHonorsIsSelfWithoutMatchingID(t *testing.T) {
	now := time.Now()
	mine := convCard("s-mine", "cc-me", "/repo", "a.go")
	noted := convCard("s-noted", "cc-noted", "/noted", "z.go")
	other := convCard("s-other", "cc-other", "/other", "b.go")
	v := service.BoardView{
		Project: model.Project{ID: "p"}, At: now, Window: 2 * time.Hour,
		Sessions: []service.SessionCard{other, noted, mine},
		Asks: []model.Judgment{
			{ID: "j1", SessionID: "s-noted", At: now, Title: "사건 붙은 형제"},
		},
	}
	v.Conversations = service.FoldConversations(v.Sessions)
	// IsSelf 를 묶음에만 세우고 self 문자열은 **비운다** — 카드 id 비교가 못 잡는 상태.
	for i := range v.Conversations {
		if v.Conversations[i].CCSessionID == "cc-me" {
			v.Conversations[i].IsSelf = true
		}
	}
	got := rankConversations(v, "", now)
	if len(got) != 3 {
		t.Fatalf("묶음 %d개, 원하는 것 3개", len(got))
	}
	if got[0].CCSessionID != "cc-me" {
		t.Fatalf("내 묶음이 첫째가 아니다(%q) — IsSelf 분기가 안 돈다"+
			"(사건 붙은 형제(cc-noted, rank 1)에 밀렸다면 case c.IsSelf 가 죽어 "+
			"내 묶음이 경로 겹침(rank 2)으로 떨어진 것이다)", got[0].CCSessionID)
	}
}

// 같은 등급 안에서는 마지막 신호가 최근인 묶음이 먼저다.
// lastSignalOfConversation 을 제로값으로 바꿔도 초록이던 자리다(검토 확인).
func TestRankConversationsOrdersByLatestSignal(t *testing.T) {
	now := time.Now()
	old := convCard("s-old", "cc-old", "/a", "a.go")
	old.View.Signals = map[model.SignalKind]time.Time{model.SignalPrompt: now.Add(-2 * time.Hour)}
	fresh := convCard("s-fresh", "cc-fresh", "/b", "b.go")
	fresh.View.Signals = map[model.SignalKind]time.Time{model.SignalPrompt: now.Add(-1 * time.Minute)}

	v := service.BoardView{
		Project: model.Project{ID: "p"}, At: now, Window: 4 * time.Hour,
		Sessions: []service.SessionCard{old, fresh}, // 일부러 옛것을 먼저 넣는다
	}
	v.Conversations = service.FoldConversations(v.Sessions)
	got := rankConversations(v, "", now)
	if len(got) != 2 {
		t.Fatalf("묶음 %d개, 원하는 것 2개", len(got))
	}
	if got[0].CCSessionID != "cc-fresh" {
		t.Fatalf("첫째가 %q — 신호가 최근인 묶음이 먼저여야 한다", got[0].CCSessionID)
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

// 카드 루프가 Conversations 를 근거로 삼으므로, 그 둘이 갈리면 머리줄과 몸통이
// 서로 모순인 문서가 나간다. **조용히 나가면 안 된다.**
func TestRenderBoardSaysWhenConversationsAreMissing(t *testing.T) {
	now := time.Now()
	cards := []service.SessionCard{
		convCard("s1", "cc-a", "/repo", "a.go"),
		convCard("s2", "cc-b", "/other", "b.go"),
	}
	// Conversations 를 **일부러 안 채운다** — 배선이 빠진 상태의 재현이다.
	v := service.BoardView{
		Project: model.Project{ID: "p"}, At: now, Window: 2 * time.Hour,
		Sessions: cards,
	}
	out := RenderBoard(v, BoardRenderOptions{Now: now})
	if !strings.Contains(out, "대화 묶음이 비었다") {
		t.Fatalf("카드 2장이 있는데 묶음이 비었다는 사실을 화면이 안 말한다:\n%s", out)
	}
	// 대조 — 정상 입력에서는 그 경고가 뜨면 안 된다(항상 뜨면 배경이 된다).
	v.Conversations = service.FoldConversations(cards)
	if out := RenderBoard(v, BoardRenderOptions{Now: now}); strings.Contains(out, "대화 묶음이 비었다") {
		t.Fatalf("묶음이 정상인데 경고가 떴다:\n%s", out)
	}
}
