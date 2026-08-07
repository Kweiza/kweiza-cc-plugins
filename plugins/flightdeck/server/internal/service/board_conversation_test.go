package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/gitreader"
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

func TestSplitCardsOfCarriesTriple(t *testing.T) {
	in := []SessionCard{card("s1", "cc-a", "/repo", false)}
	in[0].View.Session.MachineID = "m1"
	got := splitCardsOf(in)
	if len(got) != 1 {
		t.Fatalf("%d건, 원하는 것 1건", len(got))
	}
	if got[0].SessionID != "s1" || got[0].MachineID != "m1" ||
		got[0].Worktree != "/repo" || got[0].CCSessionID != "cc-a" {
		t.Fatalf("3중키가 안 실렸다: %+v", got[0])
	}
}

// ★ 운영 진입점(Board)을 그대로 탄다. splitCardsOf 만 시험하면 배선 한 줄을 지워도
//
//	스위트가 초록이다 — Task 2 가 그 사고를 실제로 냈다.
func TestBoardFillsSplits(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	// 같은 (머신, cc)로 카드 둘을 연다: 하나는 트리 루트, 하나는 그 하위 디렉토리.
	// openSession 은 MachineID 를 "m1"으로 고정해 낸다(helper_test.go) — 두 카드가
	// 같은 머신으로 잡힌다. 정규화가 돌았다면 둘 다 루트로 적혔을 모양이라 갈림
	// 보고가 하나 나와야 한다.
	sub := filepath.Join(repo, "sub")
	openSession(t, s, "p", repo, repo, "cc-1", "트랙2")
	openSession(t, s, "p", repo, sub, "cc-1", "트랙2")

	view, err := s.Board(ctx(), "p", BoardOptions{})
	if err != nil {
		t.Fatalf("Board: %v", err)
	}
	if len(view.Sessions) < 2 {
		t.Fatalf("카드 %d장 — 이 시험이 아무것도 안 재고 있다", len(view.Sessions))
	}
	if len(view.Splits) == 0 {
		t.Fatal("Splits 가 비었다 — Board 가 DetectUnnormalizedSplit 을 안 부르거나 루트를 안 넘긴다")
	}
}

// hasFailure 는 파생 실패 목록에 그 축이 있는지다.
func hasFailure(v BoardView, axis string) bool {
	for _, f := range v.Failures {
		if f.Axis == axis {
			return true
		}
	}
	return false
}

// worktreesFailReader 는 축 하나(Worktrees)만 실패하는 리더다. degrade_test.go 의
// flakyReader 와 같은 기법(GitReader 를 embed 하고 필요한 메서드만 오버라이드)을
// 쓰되, 그 파일이 안 덮는 축(워크트리 목록 자체)을 덮는다. degrade_test.go 는
// 읽기만 하고 고치지 않는다.
type worktreesFailReader struct {
	GitReader
}

var errWorktreesUnreadable = errors.New("주입된 실패: 워크트리 목록을 못 읽는다")

func (w worktreesFailReader) Worktrees(ctx context.Context) ([]gitreader.Worktree, error) {
	return nil, errWorktreesUnreadable
}

// ★ "침묵하지 않는다"가 이 과제의 핵심 설계다. 루트를 못 읽었다는 사실이 화면에
//
//	안 남으면 "갈림 없음"과 "판정을 못 했다"가 같아진다 — 이 저장소가 반복해서
//	겪은 실패 모양이다.
func TestBoardNotesWhenWorktreeRootsAreUnreadable(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	// 프로젝트를 등록만 한다(경로가 있어야 git 리더가 만들어진다) — 세션은
	// 카드 축(3b, "어느 트리에도 못 붙인 카드")과 완전히 갈라 두기 위해 있어도
	// 창 밖으로 밀어 낸다. 두 축이 같은 시험에서 함께 걸리면 각각을 따로
	// 지웠을 때 어느 쪽이 잡았는지 구분이 안 된다.
	openSession(t, s, "p", repo, repo, "cc-1", "트랙2")

	broken := New(st, nil, WithGitFactory(func(repoPath string) GitReader {
		return worktreesFailReader{GitReader: gitreader.New(repoPath)}
	}))

	// 창을 1나노초로 좁혀 세션 카드를 0장으로 만든다(board_test.go 의
	// TestBoardCarriesSignalsWithoutJudgingLiveness 가 이미 쓰는 패턴).
	view, err := broken.Board(ctx(), "p", BoardOptions{Window: 1})
	if err != nil {
		t.Fatalf("Board: %v", err) // 파생이 실패해도 보드는 응답을 낸다
	}
	if len(view.Sessions) != 0 {
		t.Fatalf("이 시험은 카드 0장을 전제한다 — 아니면 3b 축과 안 갈린다: %d건", len(view.Sessions))
	}
	if !hasFailure(view, "split-detect") {
		t.Fatalf("루트를 못 읽었는데 split-detect 축이 Failures 에 없다 — "+
			"화면이 '갈림 없음'과 '판정 못 함'을 구분하지 못한다\nFailures: %+v", view.Failures)
	}
}

// ★ 어느 트리에도 못 붙인 카드가 있으면 그 수를 낸다.
func TestBoardNotesUnattributedCards(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	// 등록된 저장소 **밖** 경로로 세션을 연다 — 어느 워크트리 루트에도 안 붙는다.
	// (실물 원장에도 /home/aaron·/home/aaron/infra 같은 카드가 실제로 있다)
	outside := filepath.Join(filepath.Dir(repo), "definitely-outside-any-repo")
	openSession(t, s, "p", repo, outside, "cc-1", "트랙2")

	view, err := s.Board(ctx(), "p", BoardOptions{})
	if err != nil {
		t.Fatalf("Board: %v", err)
	}
	if !hasFailure(view, "split-detect") {
		t.Fatalf("트리에 못 붙인 카드가 있는데 split-detect 축이 없다\nFailures: %+v", view.Failures)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ack 도달성(§10) — Task 5. store.AckReach 자체는 internal/store/prescribe_reach_test.go
// 가 잠근다. 여기서는 **운영 진입점(Board)** 을 그대로 타는 배선만 잠근다 —
// TestBoardFillsSplits 와 같은 이유다: 순수 조회 함수만
// 시험하면 Board() 안의 배선 한 줄을 지워도 스위트가 초록이다.
// ─────────────────────────────────────────────────────────────────────────────

// ★ 운영 진입점을 그대로 탄다. store.AckReach 를 직접 부르는 시험만 있으면
//
//	Board() 안의 `view.AckReach = &AckReach{...}` 한 줄을 지워도(조회는 여전히 돌지만
//	결과를 버려도) 이 패키지의 스위트가 초록이다 — 실측 확인됨.
//
// ★ 셋(emitted·reachable·acked)을 **서로 다른 값**으로 둔다(3/2/1). 1/1/1 이던 앞선
// 픽스처는 `AckReach{}` 리터럴의 필드가 뒤바뀌어도(예: `Emitted: re, Reachable: em`)
// 세 값이 우연히 같아 안 잡혔다 — 검토가 뮤테이션으로 확인한 결함이다.
func TestBoardWiresAckReach(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	// 셋이 발화한다 · 그중 둘(s1·s2)이 판단을 가진다 · 하나(s1)만 ack 됐다.
	s1 := openSession(t, s, "p", repo, filepath.Join(repo, "a"), "cc-1", "")
	s2 := openSession(t, s, "p", repo, filepath.Join(repo, "b"), "cc-2", "")
	s3 := openSession(t, s, "p", repo, filepath.Join(repo, "c"), "cc-3", "")

	for _, sess := range []SessionResult{s1, s2, s3} {
		st.LogEvent(ctx(), "prescribe", "p", sess.Session.ID, map[string]any{"key": "k"})
	}
	for _, sess := range []SessionResult{s1, s2} {
		if _, err := st.AddJudgment(ctx(), model.Judgment{
			Project: "p", SessionID: sess.Session.ID, Kind: model.JudgmentDecision,
			Title: "t", Body: "b",
		}); err != nil {
			t.Fatalf("AddJudgment(%s): %v", sess.Session.ID, err)
		}
	}
	st.LogEvent(ctx(), "prescribe_ack", "p", s1.Session.ID, map[string]any{"keys": []string{"k"}})

	view, err := s.Board(ctx(), "p", BoardOptions{})
	if err != nil {
		t.Fatalf("Board: %v", err)
	}
	if view.AckReach == nil {
		t.Fatal("Board 가 AckReach 를 배선하지 않았다 — view.AckReach 가 nil 이다")
	}
	want := AckCounts{Emitted: 3, Reachable: 2, Acked: 1}
	if view.AckReach.AllTime != want {
		t.Errorf("view.AckReach.AllTime = %+v, want %+v", view.AckReach.AllTime, want)
	}
	// 픽스처가 전부 방금 만든 행이라 두 구간이 같아야 한다. 갈리면 절단이 창 안까지 잘랐다.
	if view.AckReach.Recent != want {
		t.Errorf("view.AckReach.Recent = %+v, want %+v — 창 안 픽스처인데 잘렸다", view.AckReach.Recent, want)
	}
	// 구간을 **값으로** 낸다. 표면이 "어느 구간의 수인가"를 문장에 적으려면 이 필드가 있어야
	// 하고, 없으면 렌더러가 24시간을 문자열로 박게 된다(render.go 의 창 문구 규율 참조).
	if view.AckReach.Window != AckWindow {
		t.Errorf("view.AckReach.Window = %v, want %v", view.AckReach.Window, AckWindow)
	}
}

// 최근 벌이 **실제로 잘리는지**, 그리고 그 절단이 주입 시계를 타는지 잠근다.
//
// ★ 위 시험만으로는 이 축이 안 잡힌다 — 픽스처가 전부 방금 만든 행이라 두 벌이 같은 값이고,
// 그러면 Board 가 since 를 아예 안 넘겨(전 역사를 두 번 세어) 도 초록이다. 여기서는 같은
// 픽스처를 창 밖으로 보낸다.
//
// ★ 미는 쪽이 시계다. event 행의 at 은 store 가 저장 시점의 time.Now() 로 박고 event 표는
// UPDATE 가 트리거로 막혀 있어 **행을 과거로 못 민다**. 그래서 반대로 시계를 미래로 민다.
// 부수 효과로 이 시험은 Board 가 `s.now()` 대신 `time.Now()` 를 직접 부르는 변이도 잡는다 —
// 주입 시계를 안 타면 절단이 안 움직인다.
//
// ★ 창을 `AckWindow` 로 **표현하지 않는다.** `now + AckWindow + 1h` 로 밀면 절단폭이 상수와
// 함께 스케일해서, 절단이 2시간으로 좁아져도(board.go 의 `-AckWindow` 를 `-DefaultLiveWindow`
// 로 바꾸는 변이) 픽스처가 여전히 전부 창 밖이라 초록이다 — 그 변이는 화면 라벨이 "1일 0시간"
// 인 채 2시간 창의 수를 찍게 만든다. 그래서 절대 시간 두 표본으로 폭을 **양쪽에서** 조인다:
// 23시간 뒤에는 아직 창 안이고 25시간 뒤에는 창 밖이다.
func TestBoardCutsAckReachRecentByWindow(t *testing.T) {
	inWindow := AckCounts{Emitted: 3, Reachable: 2, Acked: 1}
	for _, c := range []struct {
		name       string
		ahead      time.Duration
		wantRecent AckCounts
	}{
		{"23시간 뒤 — 아직 창 안이다", 23 * time.Hour, inWindow},
		{"25시간 뒤 — 창 밖이다", 25 * time.Hour, AckCounts{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			clockAt := time.Now().Add(c.ahead).UTC()
			s, st := newSvcWithClock(t, func() time.Time { return clockAt })
			repo := newRepo(t)
			s1 := openSession(t, s, "p", repo, filepath.Join(repo, "a"), "cc-1", "")
			s2 := openSession(t, s, "p", repo, filepath.Join(repo, "b"), "cc-2", "")
			s3 := openSession(t, s, "p", repo, filepath.Join(repo, "c"), "cc-3", "")

			for _, sess := range []SessionResult{s1, s2, s3} {
				st.LogEvent(ctx(), "prescribe", "p", sess.Session.ID, map[string]any{"key": "k"})
			}
			for _, sess := range []SessionResult{s1, s2} {
				if _, err := st.AddJudgment(ctx(), model.Judgment{
					Project: "p", SessionID: sess.Session.ID, Kind: model.JudgmentDecision,
					Title: "t", Body: "b",
				}); err != nil {
					t.Fatalf("AddJudgment(%s): %v", sess.Session.ID, err)
				}
			}
			st.LogEvent(ctx(), "prescribe_ack", "p", s1.Session.ID, map[string]any{"keys": []string{"k"}})

			view, err := s.Board(ctx(), "p", BoardOptions{})
			if err != nil {
				t.Fatalf("Board: %v", err)
			}
			if view.AckReach == nil {
				t.Fatal("Board 가 AckReach 를 배선하지 않았다 — view.AckReach 가 nil 이다")
			}
			// 전 역사는 시계와 무관하다 — 절단은 최근 벌에만 걸린다.
			if view.AckReach.AllTime != inWindow {
				t.Errorf("view.AckReach.AllTime = %+v, want %+v — 절단이 전 역사 벌에까지 샜다",
					view.AckReach.AllTime, inWindow)
			}
			if view.AckReach.Recent != c.wantRecent {
				t.Errorf("view.AckReach.Recent = %+v, want %+v — Board 가 자르는 폭이 "+
					"보고하는 Window(%v)와 다르다. 화면은 라벨만 보고 수치를 그 구간으로 읽는다",
					view.AckReach.Recent, c.wantRecent, view.AckReach.Window)
			}
		})
	}
}

// ★ 확인율 조회가 실패해도 보드가 안 죽고, 그 사실이 Failures 에 "ack-reach" 축으로
// 남는지 잠근다 — TestBoardNotesWhenWorktreeRootsAreUnreadable(split-detect 축)과 같은
// 짝이다. event 표를 지워 **이 축만** 정확히 깨뜨린다 — Board() 의 다른 파생
// (GetProject·ListLive·ListOpen·ListHeld·LandingLane)은 event 표를 안 건드리므로
// 이렇게 하면 ack-reach 축 하나만 격리해서 실패시킬 수 있다.
func TestBoardNotesWhenAckReachFails(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	openSession(t, s, "p", repo, repo, "cc-1", "트랙2")

	if _, err := st.DB().ExecContext(context.Background(), `DROP TABLE event`); err != nil {
		t.Fatalf("event 표 제거 실패(시험 전제 준비): %v", err)
	}

	view, err := s.Board(ctx(), "p", BoardOptions{})
	if err != nil {
		t.Fatalf("Board: %v", err) // 파생이 실패해도 보드는 응답을 낸다
	}
	if view.AckReach != nil {
		t.Errorf("조회가 실패했는데 AckReach 가 채워졌다: %+v", view.AckReach)
	}
	if !hasFailure(view, "ack-reach") {
		t.Fatalf("event 표가 없어 확인율 조회가 실패했는데 ack-reach 축이 Failures 에 없다\n"+
			"Failures: %+v", view.Failures)
	}
}
