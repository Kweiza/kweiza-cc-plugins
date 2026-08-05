package service

import (
	"reflect"
	"sort"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

func card(id, cc, wt string, self bool, paths ...string) SessionCard {
	return SessionCard{
		View: model.SessionView{
			Session: model.Session{ID: id, CCSessionID: cc, Worktree: wt},
			Paths:   paths,
		},
		IsSelf: self,
	}
}

func TestFoldConversations(t *testing.T) {
	got := FoldConversations([]SessionCard{
		card("s1", "cc-a", "/repo", false, "a.go", "b.go"),
		card("s2", "cc-a", "/repo/sub", false, "b.go", "c.go"),
		card("s3", "cc-b", "/other", false, "d.go"),
	})
	if len(got) != 2 {
		t.Fatalf("묶음 %d개, 원하는 것 2개: %+v", len(got), got)
	}
	var a *Conversation
	for i := range got {
		if got[i].CCSessionID == "cc-a" {
			a = &got[i]
		}
	}
	if a == nil {
		t.Fatal("cc-a 묶음이 없다")
	}
	if len(a.Cards) != 2 {
		t.Errorf("cc-a 카드 %d장, 원하는 것 2장", len(a.Cards))
	}
	if a.Worktrees != 2 {
		t.Errorf("cc-a 워크트리 %d개, 원하는 것 2개", a.Worktrees)
	}
	// 합집합 건수다 — b.go 가 둘 다에 있으므로 4가 아니라 3이다.
	if a.PathCount != 3 {
		t.Errorf("cc-a 경로 %d개, 원하는 것 3개(합집합)", a.PathCount)
	}
}

func TestFoldConversationsNeverFoldsEmptyCC(t *testing.T) {
	got := FoldConversations([]SessionCard{
		card("s1", "", "/repo", false),
		card("s2", "", "/other", false),
	})
	if len(got) != 2 {
		t.Fatalf("묶음 %d개, 원하는 것 2개 — 빈 cc 는 절대 안 접는다: %+v", len(got), got)
	}
}

func TestFoldConversationsLiftsIsSelf(t *testing.T) {
	got := FoldConversations([]SessionCard{
		card("s1", "cc-a", "/repo", false),
		card("s2", "cc-a", "/repo/sub", true),
	})
	if len(got) != 1 {
		t.Fatalf("묶음 %d개, 원하는 것 1개", len(got))
	}
	if !got[0].IsSelf {
		t.Error("형제 중 하나라도 나면 묶음이 * 를 받아야 한다")
	}
}

// 대조 단정 — Sessions 를 안 건드리는 것이 이 설계의 계약이다.
// dashboard.json 소비자(이 항목의 재측정 명령을 포함)가 그것에 기대고 있다.
func TestFoldDoesNotMutateCards(t *testing.T) {
	in := []SessionCard{
		card("s1", "cc-a", "/repo", false, "a.go"),
		card("s2", "cc-a", "/repo/sub", false, "b.go"),
	}
	before := len(in)
	_ = FoldConversations(in)
	if len(in) != before {
		t.Fatalf("입력이 %d장에서 %d장으로 바뀌었다", before, len(in))
	}
	if in[0].View.Session.ID != "s1" || in[1].View.Session.ID != "s2" {
		t.Fatal("입력 카드의 순서·내용이 바뀌었다")
	}
}

// Worktrees 는 카드 수가 아니라 **서로 다른 워크트리 수**다.
// 브리프의 다른 케이스는 카드 2장·워크트리 2개라 둘이 우연히 같아, 이 시험이
// 없으면 `Worktrees = len(Cards)` 로 바꿔도 스위트가 초록이다(검토가 뮤테이션으로 확인).
func TestFoldCountsDistinctWorktreesNotCards(t *testing.T) {
	got := FoldConversations([]SessionCard{
		card("s1", "cc-a", "/repo", false, "a.go"),
		card("s2", "cc-a", "/repo", false, "b.go"), // 같은 워크트리의 다른 창
		card("s3", "cc-a", "/repo/sub", false, "c.go"),
	})
	if len(got) != 1 {
		t.Fatalf("묶음 %d개, 원하는 것 1개", len(got))
	}
	if len(got[0].Cards) != 3 {
		t.Fatalf("카드 %d장, 원하는 것 3장", len(got[0].Cards))
	}
	if got[0].Worktrees != 2 {
		t.Fatalf("워크트리 %d개, 원하는 것 2개 — 카드 수(3)와 달라야 한다", got[0].Worktrees)
	}
}

// 묶음의 Paths 를 만져도 입력이 안 바뀐다 — 얕은 복사면 여기서 원본이 오염된다.
// 이 계약이 없으면 Task 4 의 렌더가 표시용으로 한 번 정렬하는 것만으로
// BoardView.Sessions 가 조용히 바뀐다(검토가 실측으로 재현했다).
func TestFoldDeepCopiesPaths(t *testing.T) {
	in := []SessionCard{card("s1", "cc-a", "/repo", false, "c.go", "a.go", "b.go")}
	got := FoldConversations(in)
	sort.Strings(got[0].Cards[0].View.Paths)
	if want := []string{"c.go", "a.go", "b.go"}; !reflect.DeepEqual(in[0].View.Paths, want) {
		t.Fatalf("묶음의 Paths 를 정렬했더니 입력이 %v 로 바뀌었다. 원하는 것 %v — "+
			"Sessions 를 안 바꾼다는 계약이 이 층위에서 뚫려 있다", in[0].View.Paths, want)
	}
}

// ★ 이 시험은 **운영 진입점(Board)을 그대로 탄다.** FoldConversations 를 직접 부르는
//
//	시험만 있으면 Board 안의 배선 한 줄을 지워도 스위트가 초록이다.
func TestBoardFillsConversations(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-1", "트랙2")

	view, err := s.Board(ctx(), "p", BoardOptions{Self: sess.Session.ID})
	if err != nil {
		t.Fatalf("Board: %v", err)
	}
	if len(view.Sessions) == 0 {
		t.Fatal("세션 카드가 없다 — 이 시험이 아무것도 안 재고 있다")
	}
	if len(view.Conversations) == 0 {
		t.Fatal("Conversations 가 비었다 — Board 가 FoldConversations 를 안 부른다")
	}
	// 카드 전부가 묶음 어딘가에 정확히 한 번 들어가야 한다.
	seen := map[string]int{}
	for _, cv := range view.Conversations {
		for _, k := range cv.Cards {
			seen[k.View.Session.ID]++
		}
	}
	if len(seen) != len(view.Sessions) {
		t.Fatalf("묶음에 담긴 카드 %d장, Sessions %d장 — 접기가 카드를 잃거나 더했다",
			len(seen), len(view.Sessions))
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("카드 %s 가 묶음에 %d번 나온다", id, n)
		}
	}
}
