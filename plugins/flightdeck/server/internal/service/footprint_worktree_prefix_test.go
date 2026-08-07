package service

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 발자국의 **워크트리 접두** 축 — 포함 축이 통과시키는 트리 밖 경로.
//
// ★ 왜 포함 축만으로 부족한가. `filepath.Rel` 은 **파일시스템 포함**을 재는데, 링크
// 워크트리는 `<repo>/.flightdeck/worktrees/<id>` 라 저장소 루트의 **물리적 자손**이다.
// 그래서 카드의 worktree 가 주 저장소 루트일 때 within=true 가 나오고, rel 이
// `.flightdeck/worktrees/<id>/…` 접두를 인 채 발자국이 된다. 반면 git 은 그 트리를
// 자기 것으로 안 본다(`.git/info/exclude` 에 `.flightdeck/`). 두 포함 개념이 갈리는
// 그 틈이 이 축이다.
//
// ★ 접두 경로는 **비교 가능한 척하며 오답을 만든다** — 절대경로가 아니라
// judge.comparablePath 를 통과하고(grounded=true), pathRelated 는 성분 0번부터 맞추므로
// `.flightdeck` vs `plugins` 에서 즉시 갈려 **원리적으로 어떤 선언 경로와도 안 겹친다.**
// 절대경로보다 나쁘다 — 그쪽은 최소한 근거에서 빠진다.
//
// 실측(2026-08-07 원장): 접두 행 107건(observed 1274 중 8.4%), 일자별 18/44/35/10 로
// 지금도 유입 중. 그 행을 인용한 처방 19건은 **전부 `outside:` 키**다.

// TestBeatDropsAWorktreePrefixedPath 는 관문을 잠근다.
//
// 규율은 포함 축 기존 규약 그대로다 — **버리되 남긴다.** 거절하지 않는 이유는 훅이 죽으면
// 생존 신호가 끊기기 때문이고, 조용히 지우지 않는 이유는 그것이 이 함수가 없애려는
// 침묵 그 자체이기 때문이다.
func TestBeatDropsAWorktreePrefixedPath(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	// 카드의 기준 트리가 **주 저장소 루트**다. 이것이 접두가 생기는 유일한 배치다.
	sess := openSession(t, s, "p", repo, repo, "cc-prefix", "접두축")

	inside := filepath.Join(repo, "plugins", "flightdeck", "server", "cmd", "fd", "hook.go")
	// 같은 저장소 안의 링크 워크트리. 좌표계도 포함 축도 **통과한다** — 물리적 자손이라서다.
	prefixed := filepath.Join(repo, ".flightdeck", "worktrees", "fd-x",
		"plugins", "flightdeck", "server", "cmd", "fd", "hook.go")

	if err := s.Beat(ctx(), sess.Session.ID, model.SignalTool, []string{inside, prefixed}); err != nil {
		t.Fatalf("접두 경로 때문에 신호가 죽었다 — 거절이 아니라 버리는 축이다: %v", err)
	}

	// ── ① 신호는 산다.
	sig, err := st.Signals(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("신호 조회 실패: %v", err)
	}
	if _, ok := sig[model.SignalTool]; !ok {
		t.Fatalf("tool 신호가 안 남았다: %v", sig)
	}

	// ── ② 접두 없는 경로만 발자국이 된다.
	fps, err := st.FootprintPaths(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("발자국 조회 실패: %v", err)
	}
	want := filepath.ToSlash(filepath.Join("plugins", "flightdeck", "server", "cmd", "fd", "hook.go"))
	if len(fps) != 1 || fps[0] != want {
		t.Fatalf("발자국 = %v, 기대 [%s]\n"+
			"접두 경로가 발자국에 들어가면 selfsame 파일이 한 카드 안에서 두 문자열로 산다 — "+
			"claimed 축은 repo-relative, observed 축은 접두. 그 둘이 원리적으로 안 겹쳐 "+
			"outside 처방이 100%% 발화한다(실측 19건)", fps, want)
	}

	// ── ③ 조용히 사라지지 않는다.
	evs, err := st.ListSessionEvents(ctx(), sess.Session.ID, "session.beat", time.Time{})
	if err != nil {
		t.Fatalf("이벤트 조회 실패: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("session.beat 이벤트가 %d건이다, want 1: %+v", len(evs), evs)
	}
	payload := evs[0].Payload
	if !strings.Contains(payload, `"outside":1`) {
		t.Errorf("원장에 버린 건수가 안 남았다: %s", payload)
	}
	if !strings.Contains(payload, `"count":1`) {
		t.Errorf("원장의 count 가 실제로 Touch 한 수와 다르다: %s", payload)
	}
	if !strings.Contains(payload, "worktrees") {
		t.Errorf("버린 경로가 원장에 안 남았다 — 지워진 발자국은 아무 데도 안 나타난다: %s", payload)
	}
}

// TestBeatKeepsPathsWhenTheCardIsTheWorktree 는 **대조 짝**이다 — 관문이 너무 넓어지면
// 이것이 빨개진다.
//
// ★ 이 짝이 없으면 "경로에 worktrees 가 보이면 버린다"로 새는 수정이 초록불이 난다.
// 그러면 워크트리 안에서 도는 세션의 발자국이 통째로 죽는다 — 실측상 observed 1274건 중
// 1099건(86%)이 그런 세션의 것이다. 판정의 전부는 **접두가 rel 에 남았는가**이지
// 절대경로에 그 문자열이 있는가가 아니다.
func TestBeatKeepsPathsWhenTheCardIsTheWorktree(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	wt := filepath.Join(repo, ".flightdeck", "worktrees", "fd-x")
	// 카드의 기준 트리가 **그 워크트리 자체**다 — 접두가 root 에 먹혀 rel 이 깨끗하다.
	sess := openSession(t, s, "p", repo, wt, "cc-inwt", "워크트리카드")

	p := filepath.Join(wt, "plugins", "flightdeck", "server", "cmd", "fd", "hook.go")
	if err := s.Beat(ctx(), sess.Session.ID, model.SignalTool, []string{p}); err != nil {
		t.Fatalf("beat 실패: %v", err)
	}

	fps, err := st.FootprintPaths(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("발자국 조회 실패: %v", err)
	}
	want := filepath.ToSlash(filepath.Join("plugins", "flightdeck", "server", "cmd", "fd", "hook.go"))
	if len(fps) != 1 || fps[0] != want {
		t.Fatalf("발자국 = %v, 기대 [%s]\n"+
			"워크트리 카드의 정상 발자국이 죽었다 — 관문이 rel 이 아니라 절대경로를 보고 있다. "+
			"그러면 실측상 발자국의 86%%가 사라진다", fps, want)
	}
}

// TestBeatDropsANestedWorktreePrefix 는 중첩 배치를 본다 —
// 워크트리 안에서 하네스가 자기 워크트리를 만든 경우다.
//
// judge 의 관례 스캔이 **전부** 내는 이유가 이것이다(첫 매치에서 멈추면 안쪽 트리가
// 목록에 안 들어간다). 오늘 원장에 이 배치는 0건이지만, 워크트리 안에서 도는 세션에
// 하네스가 `.claude/worktrees/` 를 만들면 바로 생긴다.
func TestBeatDropsANestedWorktreePrefix(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	wt := filepath.Join(repo, ".flightdeck", "worktrees", "fd-x")
	sess := openSession(t, s, "p", repo, wt, "cc-nested", "중첩")

	ok := filepath.Join(wt, "plugins", "x.go")
	nested := filepath.Join(wt, ".claude", "worktrees", "sub", "plugins", "x.go")

	if err := s.Beat(ctx(), sess.Session.ID, model.SignalTool, []string{ok, nested}); err != nil {
		t.Fatalf("beat 실패: %v", err)
	}
	fps, err := st.FootprintPaths(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("발자국 조회 실패: %v", err)
	}
	want := filepath.ToSlash(filepath.Join("plugins", "x.go"))
	if len(fps) != 1 || fps[0] != want {
		t.Fatalf("발자국 = %v, 기대 [%s] — 중첩 워크트리 접두가 안 걸렸다", fps, want)
	}
}
