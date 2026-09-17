package judge

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 워크트리가 사라진 카드 판정의 **조건 하나씩**을 잠근다.
//
// ★ 왜 순수 함수 시험이 따로 있나. 실물 저장소 시험(service/gone_worktree_test.go)에서는
// 조건 여럿이 서로를 가린다 — 살아 있는 카드는 디렉토리가 실제로 있어서, 목록 비교가 틀려도
// 그 뒤의 서버 lstat 이 막는다. 그러면 목록 비교를 망가뜨려도 초록이다. 여기서는 관측을 값으로
// 넣으므로 조건 하나만 틀어 그 조건이 잡는지 볼 수 있다.

const (
	goneMain = "/srv/repo"
	goneTree = "/srv/repo/.flightdeck/worktrees/fd-x"
)

// goneFacts 는 **닫히는** 기준 관측이다. 각 시험이 한 축만 틀어 안 닫히는지 본다.
func goneFacts() GoneWorktreeFacts {
	return GoneWorktreeFacts{
		Worktree:     goneTree,
		ListOK:       true,
		Listed:       []string{goneMain, "/srv/repo/.flightdeck/worktrees/other"},
		TreePresence: PathAbsent,
		HomePresence: PathPresent,
	}
}

func TestWorktreeGoneWhenGitAndDiskBothLackIt(t *testing.T) {
	v := WorktreeGone(goneFacts())
	if !v.Gone {
		t.Fatalf("기준 관측(목록에 없고 · 관례 자리 · 담는 워크트리 보임 · 루트 없음)이 안 닫힌다: %s", v.Reason)
	}
	if v.Tree != goneTree || v.Home != goneMain {
		t.Fatalf("잰 자리가 틀렸다: tree=%q home=%q (기대 %q · %q)", v.Tree, v.Home, goneTree, goneMain)
	}
}

// ★ 가장 중요한 조건이다. 목록을 못 읽었으면 **목록에 무엇이 들어 있든** 부재의 근거가 아니다 —
// 호출부가 실패 시 옛 목록이나 주 저장소 하나를 채워 넘기는 판이 와도 여기서 멈춰야 한다.
func TestWorktreeNotGoneWhenTheListWasNotRead(t *testing.T) {
	f := goneFacts()
	f.ListOK = false
	v := WorktreeGone(f)
	if v.Gone {
		t.Fatal("git worktree list 를 못 읽었는데 닫는다 — 마운트 밖·권한·일시 오류가 전부 삭제로 읽힌다")
	}
	if !strings.Contains(v.Reason, "못 읽었다") {
		t.Fatalf("사유가 '못 읽었다'를 말하지 않는다: %s", v.Reason)
	}
}

// 주 체크아웃과 그 하위 디렉토리는 관례 자리가 아니라 판정 대상이 못 된다.
// MCP 가 cwd 로 워크트리를 정한 카드(mcpsrv 의 cwd 폴백)는 주 체크아웃의 하위 디렉토리에 설 수
// 있고, 브랜치 전환 한 번에 그 디렉토리가 사라질 수 있다 — 그 카드는 살아서 주 체크아웃에 있다.
func TestWorktreeNotGoneInsideTheMainCheckout(t *testing.T) {
	for _, wt := range []string{goneMain + "/plugins/flightdeck/server", goneMain} {
		f := goneFacts()
		f.Worktree = wt
		v := WorktreeGone(f)
		if v.Gone {
			t.Fatalf("주 체크아웃 좌표 %q 를 닫는다 — 살아 있는 주 트리 카드가 보드에서 사라진다", wt)
		}
	}
}

// 경로 꼴이 달라도 git 이 같은 워크트리로 기록하고 있으면 안 닫는다.
// 서버가 그 꼴로는 못 보는 배치(컨테이너 안에서 호스트의 대소문자·끝 슬래시 꼴)를 흉내 내려고
// 루트를 Absent 로 둔다 — lstat 층을 빼고 목록 비교만 남긴다.
//
// ★ 변이로 잰 사실(2026-09-17): 대소문자 꼴은 **목록 비교의 대소문자 무시 하나**가 막는다
// (EqualFold 를 == 로 바꾸면 이 시험이 빨갛다). 끝 슬래시·점 성분 꼴은 둘이 막는다 — 카드 경로
// 비교의 Clean 을 지워도 관례 루트를 되읽는 자리(conventionRoots)가 이미 Clean 해서 "담는 루트가
// 목록에 있다"로 걸린다. 그래서 그 Clean 하나만 지우는 변이는 초록이다.
func TestWorktreeNotGoneWhenGitListsTheSameTreeSpelledDifferently(t *testing.T) {
	for _, spelled := range []string{
		goneTree + "/",
		"/srv/repo/.flightdeck/worktrees/./fd-x",
		"/srv/repo/.flightdeck/worktrees/FD-X",
	} {
		f := goneFacts()
		f.Listed = append(f.Listed, goneTree)
		f.Worktree = spelled
		if v := WorktreeGone(f); v.Gone {
			t.Fatalf("git 목록에 %q 가 있는데 %q 꼴의 카드를 닫는다 — 같은 경로를 다르다고 봤다", goneTree, spelled)
		}
	}
}

// 카드가 살아 있는 워크트리의 하위 디렉토리면 그 디렉토리가 없어도 안 닫는다.
func TestWorktreeNotGoneWhenItsTreeIsStillListed(t *testing.T) {
	f := goneFacts()
	f.Listed = append(f.Listed, goneTree)
	f.Worktree = goneTree + "/plugins/flightdeck"
	if v := WorktreeGone(f); v.Gone {
		t.Fatalf("담는 워크트리 %q 가 목록에 있는데 그 하위 좌표를 닫는다: %s", goneTree, v.Reason)
	}
}

// 관례 자리여도 이 저장소의 목록 워크트리 안이 아니면 근거가 없다 — 다른 저장소이거나 이
// 서버가 못 읽는 파일시스템의 경로다(실측 2026-09-17: `/Users/…/staydesk` 로 등록된 프로젝트에
// `/home/kweiza/staydesk…` 카드 228장이 **이 머신의 머신 id 로** 섞여 있다 — 머신 축으로는 못 가른다).
func TestWorktreeNotGoneOutsideEveryListedTree(t *testing.T) {
	f := goneFacts()
	f.Worktree = "/home/other/staydesk/.claude/worktrees/channex"
	if v := WorktreeGone(f); v.Gone {
		t.Fatalf("이 저장소 목록 밖의 관례 좌표를 닫는다 — 남의 저장소 목록과 대조한 것이다: %s", v.Reason)
	}
	// 문자열 접두로 담는다고 보면 안 된다 — /srv/repo 는 /srv/repo2 를 안 담는다.
	f.Worktree = "/srv/repo2/.flightdeck/worktrees/fd-x"
	if v := WorktreeGone(f); v.Gone {
		t.Fatalf("/srv/repo 가 /srv/repo2 를 담는다고 봤다 — 접두 비교다: %s", v.Reason)
	}
}

// 담는 워크트리가 서버에서 안 보이면(컨테이너 마운트 밖) 루트가 없다는 관측도 믿을 수 없다.
func TestWorktreeNotGoneWhenTheHomeIsNotVisible(t *testing.T) {
	for _, p := range []PathPresence{PathAbsent, PathUnknown} {
		f := goneFacts()
		f.HomePresence = p
		if v := WorktreeGone(f); v.Gone {
			t.Fatalf("담는 워크트리가 서버에서 %v 인데 닫는다 — 마운트 밖이면 그 아래 전부가 없다고 나온다", p)
		}
	}
}

// 루트가 서버에 아직 있거나 못 쟀으면 안 닫는다.
func TestWorktreeNotGoneWhenTheTreeIsPresentOrUnmeasured(t *testing.T) {
	for _, p := range []PathPresence{PathPresent, PathUnknown} {
		f := goneFacts()
		f.TreePresence = p
		if v := WorktreeGone(f); v.Gone {
			t.Fatalf("루트가 서버에서 %v 인데 닫는다 — '못 쟀다'나 '있다'는 없다는 근거가 아니다", p)
		}
	}
}

// 앞 다섯 조건이 다 서면 후보이고, 그 후보가 잴 자리(tree·home)를 WorktreeGone 과 **같은 값**으로
// 낸다. 서비스는 이 값만 stat 하므로 두 자리가 어긋나면 잰 경로와 판정한 경로가 달라진다.
func TestWorktreeGoneCandidateNamesTheSameCoordsAsTheVerdict(t *testing.T) {
	f := goneFacts()
	c := WorktreeGoneCandidate(f.Worktree, f.ListOK, f.Listed)
	if !c.OK || c.Tree != goneTree || c.Home != goneMain {
		t.Fatalf("후보 판정이 틀렸다: %+v (기대 tree=%q home=%q)", c, goneTree, goneMain)
	}
	if v := WorktreeGone(f); v.Tree != c.Tree || v.Home != c.Home {
		t.Fatalf("후보와 판정이 다른 자리를 말한다: 후보 %+v · 판정 %+v", c, v)
	}
	// 목록에 있으면 후보가 아니다 — 서비스는 여기서 멈추고 stat 을 안 한다(평시 비용 0).
	if c := WorktreeGoneCandidate(goneMain, true, f.Listed); c.OK {
		t.Fatalf("목록에 있는 주 워크트리가 후보로 나왔다 — 살아 있는 카드마다 stat 을 한다: %+v", c)
	}
}

// 중첩 배치에서는 **가장 안쪽** 관례 루트를 잰다 — 워크트리 안에서 하네스가 자기 워크트리를
// 만든 경우다(judge 의 conventionRoots 가 전부를 내는 이유와 같은 배치).
//
// 바깥 것을 고르면 둘이 다 틀린다: 안쪽 트리가 지워졌는데 바깥이 목록에 있으면 "살아 있는
// 워크트리의 하위 디렉토리"로 읽혀 영영 안 닫히고, 반대로 바깥이 지워졌는데 안쪽이 살아 있으면
// 살아 있는 트리를 부재로 잰다.
func TestWorktreeGoneMeasuresTheInnermostConventionRoot(t *testing.T) {
	outer := "/srv/repo/.flightdeck/worktrees/fd-x"
	inner := outer + "/.claude/worktrees/sub"
	f := goneFacts()
	f.Worktree = inner
	f.Listed = []string{goneMain, outer} // 바깥 트리는 살아 있다

	c := WorktreeGoneCandidate(f.Worktree, f.ListOK, f.Listed)
	if c.Tree != inner || c.Home != outer {
		t.Fatalf("중첩 배치에서 잰 자리가 틀렸다: tree=%q home=%q (기대 %q · %q)", c.Tree, c.Home, inner, outer)
	}
	if v := WorktreeGone(f); !v.Gone {
		t.Fatalf("안쪽 트리가 목록에 없고 서버에도 없는데 안 닫는다: %s", v.Reason)
	}
}

// ── 닫아도 되는 카드인가 ────────────────────────────────────────────────

func okGuards() GoneCardGuards {
	return GoneCardGuards{State: model.SessionActive}
}

func TestMayCloseGoneCardBaseline(t *testing.T) {
	if ok, why := MayCloseGoneCard(okGuards()); !ok {
		t.Fatalf("active · 선점 0 · mcp 신호 없음 · 요청자 아님인데 안 닫는다: %s", why)
	}
}

func TestMayCloseGoneCardLeavesBlockedAndPaused(t *testing.T) {
	for _, st := range []model.SessionState{model.SessionBlocked, model.SessionPaused, model.SessionDone} {
		g := okGuards()
		g.State = st
		if ok, _ := MayCloseGoneCard(g); ok {
			t.Fatalf("state=%s 인 카드를 닫는다 — 사람이 쓴 상태다(설계 §4)", st)
		}
	}
}

func TestMayCloseGoneCardLeavesClaimHolders(t *testing.T) {
	g := okGuards()
	g.Claims = 1
	if ok, _ := MayCloseGoneCard(g); ok {
		t.Fatal("선점을 든 카드를 닫는다 — ListLive 에서 빠져 그 선점이 아무에게도 안 보인다")
	}
}

// MCP 카드는 닫으면 되살릴 길이 없다 — ensureSession 은 프로세스당 한 번만 열고, 도구마다 찍는
// mcp 신호(Tx.Beat)는 state 를 안 건드리며, 그 세션의 훅은 다른 3중키다. 그 사이 그 세션이
// pick 하면 done 카드가 선점을 쥔다.
func TestMayCloseGoneCardLeavesMCPCards(t *testing.T) {
	g := okGuards()
	g.HasMCPSignal = true
	ok, why := MayCloseGoneCard(g)
	if ok {
		t.Fatal("mcp 신호가 있는 카드를 닫는다 — MCP 는 그 카드를 다시 안 열어 done 카드가 선점을 쥘 수 있다")
	}
	if !strings.Contains(why, "mcp") {
		t.Fatalf("사유가 mcp 카드라는 사실을 말하지 않는다: %s", why)
	}
}

func TestMayCloseGoneCardLeavesTheCaller(t *testing.T) {
	g := okGuards()
	g.IsSelf = true
	if ok, _ := MayCloseGoneCard(g); ok {
		t.Fatal("지금 요청을 보낸 카드를 닫는다 — 부르고 있다는 것이 살아 있다는 관측이다")
	}
}

// 카드당 한 번이다. 그리고 원장을 못 읽었으면 한도를 지킨다는 근거가 없으므로 안 닫는다.
func TestPriorAutoCloseAllowsOnlyOncePerCard(t *testing.T) {
	if ok, why := PriorAutoCloseAllows(PriorAutoCloseNone); !ok {
		t.Fatalf("앞선 자동 닫기가 없는데 막는다: %s", why)
	}
	for _, p := range []PriorAutoClose{PriorAutoCloseSeen, PriorAutoCloseUnknown} {
		if ok, _ := PriorAutoCloseAllows(p); ok {
			t.Fatalf("앞선 자동 닫기 관측이 %v 인데 또 닫는다 — 되살아난 카드와 파생이 열림·닫힘을 오간다", p)
		}
	}
}
