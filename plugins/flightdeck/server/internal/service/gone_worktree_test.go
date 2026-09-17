package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/gitreader"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 워크트리가 사라진 카드를 서버가 닫는다 — **실물 저장소**로 잰다(설계 §4 셋째 닫힘 경로).
//
// ★ 조건 하나씩의 잠금은 judge/gone_worktree_test.go 에 있다. 여기서는 조건 여럿이 서로를
// 가리므로(살아 있는 카드는 대개 목록에 있어서 목록 비교가 먼저 막고, 목록 비교가 틀려도 뒤의
// lstat 이 막는다) **배선과 결말**을 잰다 — 닫혔나 · 원장에 남았나 · 같은 응답이 모순되지 않나 · 되살아나나.

// newRepoWithConventionWorktree 는 저장소 하나와 **관례 자리**의 워크트리 하나를 만든다.
//
// ★ newRepoWithWorktree(board_test.go)를 쓰면 안 된다 — 그 헬퍼는 저장소 밖 `wt-<브랜치>` 를
// 만들어 관례 자리가 아니고, 그러면 이 파일의 시험이 "근거가 없어 안 닫는다" 갈래만 재면서
// 초록이 난다. flightdeck 이 실제로 워크트리를 두는 자리가 `.flightdeck/worktrees/<항목 id>` 다.
func newRepoWithConventionWorktree(t *testing.T, name string) (repo, wt string) {
	t.Helper()
	repo = newRepo(t)
	// 주 트리의 status 에 미추적으로 안 뜨게 한다 — 주 카드의 미커밋 축이 흐려지지 않게.
	writeFile(t, repo, filepath.Join(".git", "info", "exclude"), ".flightdeck/\n")
	wt = filepath.Join(repo, ".flightdeck", "worktrees", name)
	runGit(t, repo, "worktree", "add", "-q", "-b", name, wt)
	return repo, wt
}

// removeWorktree 는 랜딩 뒤 정리를 그대로 흉내 낸다 — 디렉토리와 git 기록이 **함께** 사라진다.
func removeWorktree(t *testing.T, repo, wt string) {
	t.Helper()
	runGit(t, repo, "worktree", "remove", "--force", wt)
	if _, err := os.Lstat(wt); !os.IsNotExist(err) {
		t.Fatalf("전제가 깨졌다 — 워크트리 디렉토리가 남아 있다: %v", err)
	}
}

func sessionState(t *testing.T, st *store.Store, id string) model.Session {
	t.Helper()
	got, err := st.GetSession(ctx(), id)
	if err != nil {
		t.Fatalf("세션 조회 실패(%s): %v", id, err)
	}
	return got
}

func cardIDs(v BoardView) []string {
	ids := make([]string, 0, len(v.Sessions))
	for _, c := range v.Sessions {
		ids = append(ids, c.View.Session.ID)
	}
	return ids
}

// gone 은 가장 흔한 배치다: 주 트리 카드 하나 + 워크트리 카드 하나, 그리고 워크트리를 지운다.
func gone(t *testing.T) (s *Service, st *store.Store, repo, wt, mainID, ghostID string) {
	t.Helper()
	s, st = newSvc(t)
	repo, wt = newRepoWithConventionWorktree(t, "fd-x")
	mainID = openSession(t, s, "p", repo, repo, "cc-main", "주 트리").Session.ID
	ghostID = openSession(t, s, "p", repo, wt, "cc-wt", "워크트리").Session.ID
	removeWorktree(t, repo, wt)
	return
}

func TestBoardClosesCardWhoseWorktreeWasRemoved(t *testing.T) {
	s, st, _, wt, mainID, ghostID := gone(t)

	view, err := s.Board(ctx(), "p", BoardOptions{Self: mainID})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	if got := sessionState(t, st, ghostID); got.State != model.SessionDone {
		t.Fatalf("워크트리가 git 목록과 디스크에서 사라졌는데 카드가 %q 다 — 유령이 남는다", got.State)
	}
	if contains(cardIDs(view), ghostID) {
		t.Fatalf("닫힌 카드가 같은 보드의 카드 목록에 남았다: %v", cardIDs(view))
	}
	if !contains(cardIDs(view), mainID) {
		t.Fatalf("주 트리 카드가 사라졌다: %v", cardIDs(view))
	}
	if len(view.ClosedGoneWorktree) != 1 || view.ClosedGoneWorktree[0].SessionID != ghostID ||
		view.ClosedGoneWorktree[0].Worktree != wt {
		t.Fatalf("이 조회가 닫은 카드를 응답이 안 말한다: %+v", view.ClosedGoneWorktree)
	}
}

// ★ 방금 닫은 카드가 **같은 응답**에 파생 실패로 뜨면 그 응답은 스스로 모순된다 —
// 카드는 없는데 그 카드의 미커밋 축을 못 읽었다고 말한다. 닫기가 파생 앞에 있어야 하는 이유다.
func TestBoardThatClosesACardCarriesNoDeriveFailureForIt(t *testing.T) {
	s, _, _, _, mainID, ghostID := gone(t)

	view, err := s.Board(ctx(), "p", BoardOptions{Self: mainID})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	for _, f := range view.Failures {
		if strings.Contains(f.Axis, ghostID) {
			t.Fatalf("방금 닫힌 카드가 같은 응답에 파생 실패로 떴다: %s — %s", f.Axis, f.Detail)
		}
	}
	if view.Freshness.Stale {
		t.Fatalf("닫힌 카드 말고는 읽을 수 있는 저장소인데 낡음으로 나왔다: %+v", view.Failures)
	}
}

// ★ 원장에 남는다 — 사람이 나중에 "왜 이 카드가 닫혔지"를 물을 수 있어야 한다. 그리고
// `fd close`·SessionEnd 가 남기는 `session.state` 와 **다른 kind** 여야 둘이 갈린다.
func TestBoardRecordsTheGoneWorktreeCloseInTheLedger(t *testing.T) {
	s, st, repo, wt, mainID, ghostID := gone(t)

	if _, err := s.Board(ctx(), "p", BoardOptions{Self: mainID}); err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	evs, err := st.ListSessionEvents(ctx(), ghostID, "", time.Time{})
	if err != nil {
		t.Fatalf("이벤트 조회 실패: %v", err)
	}
	var found []model.Event
	for _, e := range evs {
		switch e.Kind {
		case store.EventSessionCloseWorktreeGone:
			found = append(found, e)
		case "session.state":
			t.Fatalf("서버의 자동 닫기가 사람의 닫기와 같은 kind(session.state)로 남았다: %s", e.Payload)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s 이벤트가 %d건이다 — 정확히 1건이어야 한다(전체 %+v)", store.EventSessionCloseWorktreeGone, len(found), evs)
	}
	if found[0].Kind != "session.close.worktree_gone" || found[0].Project != "p" {
		t.Fatalf("원장 좌표가 틀렸다: kind=%q project=%q", found[0].Kind, found[0].Project)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(found[0].Payload), &p); err != nil {
		t.Fatalf("payload 해석 실패(%s): %v", found[0].Payload, err)
	}
	want := map[string]string{
		"worktree": wt, "tree": wt, "home": repo, "repo": repo, "tx": store.TxCommitted,
	}
	for k, v := range want {
		if p[k] != v {
			t.Fatalf("payload[%q] = %v, 기대 %q — 무엇을 왜 닫았는지 원장이 못 말한다\n%s", k, p[k], v, found[0].Payload)
		}
	}
	if r, _ := p["reason"].(string); !strings.Contains(r, "git worktree list 에 없고") {
		t.Fatalf("사유가 근거(목록 부재)를 말하지 않는다: %q", r)
	}
}

// ★★ 가장 중요한 갈래다. 목록을 못 읽었으면 워크트리가 **정말로 지워졌어도** 안 닫는다 —
// 서버 쪽에서 "못 읽었다"와 "지워졌다"는 구별되지 않고, 컨테이너 서버의 마운트 밖·권한·일시
// 오류가 전부 앞의 모양이다(오늘 실측의 사유 문구가 이미 "FD_REPOS 마운트 밖일 수…"를 말한다).
func TestBoardKeepsGoneWorktreeCardWhenTheListIsUnreadable(t *testing.T) {
	t.Run("리더가 목록을 못 읽는다", func(t *testing.T) {
		s, st, _, _, mainID, ghostID := gone(t)
		broken := New(st, nil, WithGitFactory(func(repoPath string) GitReader {
			return worktreesFailReader{GitReader: gitreader.New(repoPath)}
		}))
		_ = s

		view, err := broken.Board(ctx(), "p", BoardOptions{Self: mainID})
		if err != nil {
			t.Fatalf("보드 실패: %v", err)
		}
		assertGhostKept(t, st, view, ghostID)
		if !hasFailure(view, "worktrees") {
			t.Fatalf("목록을 못 읽었다는 사실이 파생 실패에 없다: %+v", view.Failures)
		}
	})
	t.Run("실물 git 이 저장소를 못 연다", func(t *testing.T) {
		s, st, repo, _, mainID, ghostID := gone(t)
		// .git 을 치운다 — `git worktree list` 가 "not a git repository" 로 실패한다.
		if err := os.Rename(filepath.Join(repo, ".git"), filepath.Join(repo, ".git-away")); err != nil {
			t.Fatalf(".git 치우기 실패: %v", err)
		}
		view, err := s.Board(ctx(), "p", BoardOptions{Self: mainID})
		if err != nil {
			t.Fatalf("보드 실패: %v", err)
		}
		assertGhostKept(t, st, view, ghostID)
	})
}

func assertGhostKept(t *testing.T, st *store.Store, view BoardView, ghostID string) {
	t.Helper()
	if got := sessionState(t, st, ghostID); got.State != model.SessionActive {
		t.Fatalf("목록을 못 읽었는데 카드를 %q 로 닫았다 — 못 읽은 것을 부재로 읽었다", got.State)
	}
	if !contains(cardIDs(view), ghostID) {
		t.Fatalf("목록을 못 읽었는데 카드가 보드에서 빠졌다: %v", cardIDs(view))
	}
	if len(view.ClosedGoneWorktree) != 0 {
		t.Fatalf("목록을 못 읽었는데 닫았다고 말한다: %+v", view.ClosedGoneWorktree)
	}
}

// 주 체크아웃의 카드는 닫지 않는다 — 주 트리 자체도, **그 하위 디렉토리에 선 카드도.**
// 하위 디렉토리 카드는 MCP 의 cwd 폴백이 만들고(mcpsrv 의 워크트리 결정), 브랜치 전환·git rm
// 한 번에 그 디렉토리가 사라질 수 있다. 그 카드는 살아서 주 체크아웃에 있다.
func TestBoardNeverClosesMainCheckoutCards(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	sub := filepath.Join(repo, "docs", "notes")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("하위 디렉토리 생성 실패: %v", err)
	}
	mainID := openSession(t, s, "p", repo, repo, "cc-main", "주 트리").Session.ID
	subID := openSession(t, s, "p", repo, sub, "cc-sub", "주 트리 하위").Session.ID
	if err := os.RemoveAll(filepath.Join(repo, "docs")); err != nil {
		t.Fatalf("하위 디렉토리 지우기 실패: %v", err)
	}

	// Self 를 안 준다 — 요청자 보호가 주 트리 보호를 가리지 않게.
	if _, err := s.Board(ctx(), "p", BoardOptions{}); err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	for id, what := range map[string]string{mainID: "주 트리", subID: "주 트리 하위 디렉토리(지워짐)"} {
		if got := sessionState(t, st, id); got.State != model.SessionActive {
			t.Fatalf("%s 카드를 %q 로 닫았다 — 주 체크아웃은 워크트리 자리가 아니다", what, got.State)
		}
	}
}

func TestBoardKeepsGoneWorktreeCardHoldingAClaim(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithConventionWorktree(t, "fd-x")
	mainID := openSession(t, s, "p", repo, repo, "cc-main", "주 트리").Session.ID
	ghostID := openSession(t, s, "p", repo, wt, "cc-wt", "워크트리").Session.ID
	addItem(t, s, "p", "fd-x", nil, nil)
	if _, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: ghostID, ItemID: "fd-x"}); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	removeWorktree(t, repo, wt)

	view, err := s.Board(ctx(), "p", BoardOptions{Self: mainID})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	if got := sessionState(t, st, ghostID); got.State != model.SessionActive {
		t.Fatalf("선점을 든 카드를 %q 로 닫았다 — ListLive 에서 빠져 fd-x 를 누가 쥐었는지 아무도 못 본다", got.State)
	}
	if !contains(cardIDs(view), ghostID) {
		t.Fatalf("선점을 든 카드가 보드에서 빠졌다: %v", cardIDs(view))
	}
}

func TestBoardKeepsGoneWorktreeCardMarkedBlocked(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithConventionWorktree(t, "fd-x")
	mainID := openSession(t, s, "p", repo, repo, "cc-main", "주 트리").Session.ID
	ghostID := openSession(t, s, "p", repo, wt, "cc-wt", "워크트리").Session.ID
	if err := s.SetState(ctx(), ghostID, model.SessionBlocked, "스테이징이 물렸다"); err != nil {
		t.Fatalf("막힘 표시 실패: %v", err)
	}
	removeWorktree(t, repo, wt)

	if _, err := s.Board(ctx(), "p", BoardOptions{Self: mainID}); err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	got := sessionState(t, st, ghostID)
	if got.State != model.SessionBlocked || got.BlockedWhy != "스테이징이 물렸다" {
		t.Fatalf("blocked 카드가 바뀌었다: state=%q why=%q — 사람이 사유와 함께 남긴 판단이다", got.State, got.BlockedWhy)
	}
}

// 경로 꼴이 달라도 같은 살아 있는 워크트리면 안 닫는다.
//
// ★ 판정 순서상 **목록 비교가 먼저** 막는다(Clean + 대소문자 무시). lstat 은 그 뒤의 층이라,
// 목록 비교만 망가뜨리면 이 시험은 초록이고(macOS 에서는 대소문자만 다른 꼴도 lstat 이 "있다"로
// 본다) 두 층을 함께 꺼야 빨갛다 — 층 하나씩은 judge 시험과 아래 lstat·prunable 시험이 잠근다.
func TestBoardKeepsLiveWorktreeCardSpelledDifferently(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithConventionWorktree(t, "fd-x")
	mainID := openSession(t, s, "p", repo, repo, "cc-main", "주 트리").Session.ID
	spelled := map[string]string{
		"끝 슬래시": wt + "/",
		"점 성분":  filepath.Join(repo, ".flightdeck", "worktrees") + "/./fd-x",
		"대소문자":  filepath.Join(repo, ".flightdeck", "worktrees", "FD-X"),
	}
	ids := map[string]string{}
	for what, p := range spelled {
		ids[what] = openSession(t, s, "p", repo, p, "cc-"+what, what).Session.ID
	}

	if _, err := s.Board(ctx(), "p", BoardOptions{Self: mainID}); err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	for what, id := range ids {
		if got := sessionState(t, st, id); got.State != model.SessionActive {
			t.Fatalf("%s 꼴(%q)로 적힌 살아 있는 워크트리 카드를 %q 로 닫았다 — 같은 경로를 다르다고 봤다",
				what, spelled[what], got.State)
		}
	}
}

// 지금 요청을 보낸 카드는 닫지 않는다 — 부르고 있다는 것이 살아 있다는 관측이다.
func TestBoardDoesNotCloseTheCallersOwnCard(t *testing.T) {
	s, st, _, _, _, ghostID := gone(t)

	view, err := s.Board(ctx(), "p", BoardOptions{Self: ghostID})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	if got := sessionState(t, st, ghostID); got.State != model.SessionActive {
		t.Fatalf("보드를 부른 카드 자신을 %q 로 닫았다", got.State)
	}
	if len(view.ClosedGoneWorktree) != 0 {
		t.Fatalf("요청자 카드를 닫았다고 말한다: %+v", view.ClosedGoneWorktree)
	}
}

// ★ 되돌릴 수 있어야 한다. 그 카드로 신호가 또 오면(훅은 매 프롬프트·편집마다 OpenSession 을
// 지난다 — cmd/fd 의 beatFromHook) 같은 카드가 살아나고, **그 뒤로는 다시 자동으로 안 닫힌다.**
// 한도가 없으면 사라진 cwd 에서 신호를 보내는 세션이 신호와 보드마다 열림·닫힘을 오가며
// 지울 수 없는 event 행을 쌓는다.
func TestGoneWorktreeCardReopensOnTheNextSignalAndIsNotClosedAgain(t *testing.T) {
	s, st, repo, wt, mainID, ghostID := gone(t)

	if _, err := s.Board(ctx(), "p", BoardOptions{Self: mainID}); err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	if got := sessionState(t, st, ghostID); got.State != model.SessionDone {
		t.Fatalf("전제가 깨졌다 — 첫 보드가 안 닫았다: %q", got.State)
	}

	// 같은 3중키로 신호가 온다 — 훅이 부르는 바로 그 서비스 함수다.
	res := openSession(t, s, "p", repo, wt, "cc-wt", "워크트리")
	if res.Created || res.Session.ID != ghostID || res.Session.State != model.SessionActive {
		t.Fatalf("자동으로 닫힌 카드가 신호로 안 살아난다: created=%v id=%q state=%q",
			res.Created, res.Session.ID, res.Session.State)
	}

	view, err := s.Board(ctx(), "p", BoardOptions{Self: mainID})
	if err != nil {
		t.Fatalf("둘째 보드 실패: %v", err)
	}
	if got := sessionState(t, st, ghostID); got.State != model.SessionActive {
		t.Fatalf("되살아난 카드를 또 닫았다(%q) — 그 좌표로 신호가 왔다는 관측이 판정을 이겨야 한다", got.State)
	}
	if !contains(cardIDs(view), ghostID) || len(view.ClosedGoneWorktree) != 0 {
		t.Fatalf("되살아난 카드가 보드에 없거나 또 닫았다고 말한다: cards=%v closed=%+v",
			cardIDs(view), view.ClosedGoneWorktree)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM event WHERE kind = ? AND session_id = ?`,
		store.EventSessionCloseWorktreeGone, ghostID); n != 1 {
		t.Fatalf("자동 닫기 이벤트가 %d건이다 — 카드당 한 번이어야 한다", n)
	}
}

// 보드만이 아니다 — pick 도 같은 카드 파생을 지나므로 거기서도 닫힌다. 유령은 겹침 표에도
// 뜨고 겹침은 pick 응답 꼬리가 내기 때문이다.
func TestPickAlsoClosesGoneWorktreeCards(t *testing.T) {
	s, st, _, _, mainID, ghostID := gone(t)
	addItem(t, s, "p", "it-next", nil, nil)

	if _, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: mainID, ItemID: "it-next"}); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	if got := sessionState(t, st, ghostID); got.State != model.SessionDone {
		t.Fatalf("pick 이 같은 파생을 지났는데 유령 카드가 %q 다 — 겹침 표에 남는다", got.State)
	}
}

// ★ I-1: MCP 카드는 닫지 않는다 — 닫으면 되살릴 길이 없다.
//
// 닫히던 입력: 세션을 관례 워크트리 **안에서** 띄우면 `fd mcp` 가 그 cwd 로 카드를 연다. 그 세션이
// 랜딩하고 워크트리를 지운 뒤 main 에서 계속 일하면 훅은 main 3중키라 다른 카드이고, MCP 카드는
// 지운 자리에 남는다. 그 카드를 닫으면 ① ensureSession 은 프로세스당 한 번만 열고 ② 도구마다
// 찍는 mcp 신호는 state 를 안 건드리며 ③ 훅은 그 카드를 안 연다 — 그 사이 그 세션이 pick 하면
// done 카드가 선점을 쥔다. 여기서는 그 모양(mcp 신호가 찍힌 카드 + 지운 워크트리 + 남의 보드)을 만든다.
func TestBoardDoesNotCloseAnMCPCardWhoseWorktreeIsGone(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithConventionWorktree(t, "fd-x")
	mainID := openSession(t, s, "p", repo, repo, "cc-main", "주 트리").Session.ID
	mcpID := openSession(t, s, "p", repo, wt, "cc-mcp", "워크트리에서 띄운 세션").Session.ID
	// callTool 이 ensureSession 뒤에 찍는 바로 그 신호다(mcpsrv.go).
	if err := s.Beat(ctx(), mcpID, model.SignalMCP, nil); err != nil {
		t.Fatalf("mcp 신호 실패: %v", err)
	}
	removeWorktree(t, repo, wt)

	view, err := s.Board(ctx(), "p", BoardOptions{Self: mainID})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	if got := sessionState(t, st, mcpID); got.State != model.SessionActive {
		t.Fatalf("mcp 신호가 있는 카드를 %q 로 닫았다 — MCP 는 그 카드를 다시 안 열고, 그 세션의 pick 이 done 카드에 선점을 쥐인다", got.State)
	}
	if !contains(cardIDs(view), mcpID) || len(view.ClosedGoneWorktree) != 0 {
		t.Fatalf("MCP 카드가 보드에서 빠졌거나 닫았다고 말한다: cards=%v closed=%+v", cardIDs(view), view.ClosedGoneWorktree)
	}
}

// ★ I-3: **목록에는 없는데 디렉토리는 살아 있는** 워크트리의 카드를 안 닫는다.
//
// 이 모양에서 카드를 막는 층은 서버의 lstat 하나뿐이다. git 은 gitdir 를 못 읽는 워크트리를
// 목록에서 조용히 빼고 0으로 끝난다 — 관리 디렉토리(.git/worktrees/<이름>)가 치워졌거나
// repair·move 가 그것을 다시 쓰는 순간이 그렇다. 여기서는 관리 디렉토리만 치운다.
func TestBoardKeepsCardWhoseLiveWorktreeGitStoppedListing(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithConventionWorktree(t, "fd-x")
	mainID := openSession(t, s, "p", repo, repo, "cc-main", "주 트리").Session.ID
	liveID := openSession(t, s, "p", repo, wt, "cc-live", "살아 있는 워크트리").Session.ID
	if err := os.RemoveAll(filepath.Join(repo, ".git", "worktrees", "fd-x")); err != nil {
		t.Fatalf("관리 디렉토리 치우기 실패: %v", err)
	}
	// 전제 둘을 실물로 잰다 — 목록에서는 빠졌고, 디렉토리는 살아 있다.
	wts, err := gitreader.New(repo).Worktrees(ctx())
	if err != nil {
		t.Fatalf("목록 읽기 실패: %v", err)
	}
	for _, w := range wts {
		if filepath.Clean(w.Path) == wt {
			t.Fatalf("전제가 깨졌다 — git 이 아직 %s 를 목록에 낸다: %+v", wt, w)
		}
	}
	if _, err := os.Lstat(wt); err != nil {
		t.Fatalf("전제가 깨졌다 — 워크트리 디렉토리가 없다: %v", err)
	}

	if _, err := s.Board(ctx(), "p", BoardOptions{Self: mainID}); err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	if got := sessionState(t, st, liveID); got.State != model.SessionActive {
		t.Fatalf("디렉토리가 살아 있는 워크트리의 카드를 %q 로 닫았다 — git 목록에서 빠진 것만 보고 부재로 읽었다", got.State)
	}
}

// git 목록에 prunable 로 남은 워크트리는 **있는 것**이다 — 디렉토리가 없어도 안 닫는다.
//
// 컨테이너 서버에서 마운트 밖 워크트리가 정확히 이 모양이다(git 은 기록을 갖고 있는데 서버에서는
// 디렉토리가 안 보인다). 여기서는 `git worktree remove` 없이 디렉토리만 지워 같은 목록을 만든다.
func TestBoardKeepsCardWhoseWorktreeGitStillListsAsPrunable(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithConventionWorktree(t, "fd-x")
	mainID := openSession(t, s, "p", repo, repo, "cc-main", "주 트리").Session.ID
	ghostID := openSession(t, s, "p", repo, wt, "cc-prunable", "prunable").Session.ID
	if err := os.RemoveAll(wt); err != nil {
		t.Fatalf("워크트리 디렉토리 지우기 실패: %v", err)
	}
	wts, err := gitreader.New(repo).Worktrees(ctx())
	if err != nil {
		t.Fatalf("목록 읽기 실패: %v", err)
	}
	prunable := false
	for _, w := range wts {
		if filepath.Clean(w.Path) == wt && w.Prunable {
			prunable = true
		}
	}
	if !prunable {
		t.Fatalf("전제가 깨졌다 — git 이 %s 를 prunable 로 안 낸다: %+v", wt, wts)
	}

	if _, err := s.Board(ctx(), "p", BoardOptions{Self: mainID}); err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	if got := sessionState(t, st, ghostID); got.State != model.SessionActive {
		t.Fatalf("git 이 prunable 로 아직 기록하는 워크트리의 카드를 %q 로 닫았다 — 마운트 밖 워크트리가 전부 닫힌다", got.State)
	}
}
