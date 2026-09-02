package mcpsrv

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/service"
)

// 죽은 워크트리 하나가 내는 실패 둘을 **실물 그대로** 만든다.
//
// ★ 문자열을 손으로 짧게 쓰지 않는다. 이 항목의 증상은 경로가 길고 원인 안에 또 한 번
// 들어간다는 데서 나오고(gitreader 의 `미커밋 … 관측 실패(%s): %w` + CommandError 의
// `git <args>: status <n>: <stderr>`), 짧은 가짜로는 접기 판정이 실물에서 성립하는지를
// 못 본다. 2026-08-12 실측으로 두 명령의 stderr 가 **글자 그대로 같다**는 것을 확인했다 —
// 다른 것은 하위 명령(`status --porcelain -z` vs `diff --numstat …`)뿐이다.
func deadWorktreeFailures(sess, path string) (service.DerivedFailure, service.DerivedFailure) {
	stderr := fmt.Sprintf("status 128: fatal: cannot change to '%s': No such file or directory", path)
	paths := service.DerivedFailure{
		Axis: "uncommitted:" + sess,
		Detail: fmt.Sprintf("미커밋 경로 관측 실패(%s): git -C %s status --porcelain -z: %s",
			path, path, stderr),
	}
	delta := service.DerivedFailure{
		Axis: "uncommitted-delta:" + sess,
		Detail: fmt.Sprintf("미커밋 규모 관측 실패(%s): git -C %s diff --numstat -z --no-renames HEAD --: %s",
			path, path, stderr),
	}
	return paths, delta
}

const deadPath = "/home/u/repo/.flightdeck/worktrees/fd-lane-turn-machinery-is-dead-remove-it"

// TestDeadWorktreeFoldsIntoOneLineButKeepsTheAxisCount 는 이 항목의 본체다.
//
// 원인이 하나(그 디렉토리가 없다)인데 화면이 같은 말을 두 번 한다. 접되 **축 수는 안 줄인다** —
// 머리줄의 N 은 "몇 개를 못 봤나"이고 그것이 줄어들면 부재가 조용해진다.
func TestDeadWorktreeFoldsIntoOneLineButKeepsTheAxisCount(t *testing.T) {
	a, b := deadWorktreeFailures("01KZT30TAP", deadPath)
	got := RenderPick(service.PickResult{
		Mode: service.PickNone, Reason: "적격 0건이다",
		Derived: service.Derived{Failures: []service.DerivedFailure{a, b}},
	}, t0)

	if !strings.Contains(got, "못 읽은 파생 2축:") {
		t.Fatalf("축 수가 2 가 아니다 — 접기가 '몇 개를 못 봤나'를 먹었다:\n%s", got)
	}
	if n := countAxisRows(got); n != 1 {
		t.Fatalf("축 줄이 %d개다 — 원인이 하나인데 화면이 두 번 말한다(접혀서 1이어야 한다):\n%s", n, got)
	}
	// 접힌 줄이 두 축을 **둘 다** 이름으로 말해야 한다. 한쪽만 남으면 "규모만 죽었다"와
	// 구분이 사라진다.
	if !sameLine(got, "uncommitted", "delta") {
		t.Fatalf("접힌 줄이 두 축을 다 말하지 않는다:\n%s", got)
	}
	if !sameLine(got, "uncommitted", deadPath) {
		t.Fatalf("접힌 줄에 경로가 없다 — 어느 워크트리인지 사라졌다:\n%s", got)
	}
	// 공통 원인은 살아야 한다. 접기가 원인을 버리면 부재와 0 을 가르는 축이 무의미해진다.
	if !sameLine(got, "uncommitted", "No such file or directory") {
		t.Fatalf("접힌 줄이 공통 원인을 안 나른다:\n%s", got)
	}
}

// TestOnlyOneAxisFailedStaysSplit — 한쪽만 실패한 경우는 **반드시 갈라서** 낸다.
//
// ★ 이것이 접기의 존재 이유를 지킨다. `UncommittedDelta` 를 `UncommittedPaths` 옆에
// 별개 호출로 세운 것은 "규모를 못 읽어도 경로 축이 산다"를 위해서다
// (gitreader.go 의 UncommittedDelta 독스트링 ②). 한쪽만 실패한 것을 접으면
// "규모만 죽었다"와 "둘 다 죽었다"가 같은 화면이 되어 그 분리가 화면에서 무의미해진다.
func TestOnlyOneAxisFailedStaysSplit(t *testing.T) {
	_, delta := deadWorktreeFailures("01KZT30TAP", deadPath)
	got := RenderPick(service.PickResult{
		Mode: service.PickNone, Reason: "적격 0건이다",
		Derived: service.Derived{Failures: []service.DerivedFailure{delta}},
	}, t0)

	if !sameLine(got, "uncommitted-delta:01KZT30TAP", "미커밋 규모 관측 실패") {
		t.Fatalf("한쪽만 실패한 축이 제 이름과 원인으로 안 나온다:\n%s", got)
	}
	if strings.Contains(got, "둘 다") {
		t.Fatalf("한쪽만 실패했는데 '둘 다'로 말한다 — 규모만 죽은 것과 둘 다 죽은 것이 같은 화면이 됐다:\n%s", got)
	}
}

// TestDifferentSessionsDoNotFold — 세션이 다르면 쌍이 아니다.
func TestDifferentSessionsDoNotFold(t *testing.T) {
	a, _ := deadWorktreeFailures("01KZAAA", "/home/u/repo/.flightdeck/worktrees/fd-a")
	_, b := deadWorktreeFailures("01KZBBB", "/home/u/repo/.flightdeck/worktrees/fd-b")
	got := RenderPick(service.PickResult{
		Mode: service.PickNone, Reason: "적격 0건이다",
		Derived: service.Derived{Failures: []service.DerivedFailure{a, b}},
	}, t0)

	if n := countAxisRows(got); n != 2 {
		t.Fatalf("세션이 다른 두 실패가 %d줄로 뭉쳤다 — 한쪽 경로가 화면에서 사라진다:\n%s", n, got)
	}
}

// twinWith 는 경로와 원인 꼬리를 **따로** 지정해 쌍을 만든다.
//
// ★ 왜 필요한가(2026-08-12 변이 실측). 접기는 조건 둘을 본다 — 경로 동일성과 원인 꼬리
// 동일성. 그런데 실물에서는 stderr 안에 경로가 들어 있어 **경로가 다르면 꼬리도 자동으로
// 다르다.** 그 실물로만 시험을 짜면 두 조건이 서로를 가려 **어느 쪽도 단독으로 시험에 안
// 닿는다** — 실제로 각 검사를 하나씩 무력화하는 변이를 넣었을 때 전 스위트가 초록이었다.
// 그래서 축을 갈라 겨눈다: 아래 둘은 한 번에 조건 하나씩만 어긴다.
func twinWith(sess, path, tail string) (service.DerivedFailure, service.DerivedFailure) {
	paths := service.DerivedFailure{
		Axis:   "uncommitted:" + sess,
		Detail: fmt.Sprintf("미커밋 경로 관측 실패(%s): git -C %s status --porcelain -z: %s", path, path, tail),
	}
	delta := service.DerivedFailure{
		Axis:   "uncommitted-delta:" + sess,
		Detail: fmt.Sprintf("미커밋 규모 관측 실패(%s): git -C %s diff --numstat -z: %s", path, path, tail),
	}
	return paths, delta
}

func renderTwo(t *testing.T, a, b service.DerivedFailure) string {
	t.Helper()
	return RenderPick(service.PickResult{
		Mode: service.PickNone, Reason: "적격 0건이다",
		Derived: service.Derived{Failures: []service.DerivedFailure{a, b}},
	}, t0)
}

// TestSameSessionDifferentPathsDoNotFold — **경로만** 다르면 접지 않는다(꼬리는 같게 둔다).
//
// 오늘 이 상태는 service/board.go 가 두 호출에 같은 `Session.Worktree` 를 넘기므로 안 생긴다.
// 그래도 무는 이유: 접힌 줄은 경로를 **하나만** 싣는다. 어느 날 그 전제가 깨지면 나머지 한
// 경로가 화면에서 조용히 사라지고, 조용한 소실이 이 저장소가 가장 비싸게 보는 결함이다.
//
// 꼬리를 **경로가 안 든 오류**로 잡은 것이 이 시험의 요점이다 — 그래야 경로 동일성 검사가
// 유일한 방어선이 되어 변이가 닿는다.
func TestSameSessionDifferentPathsDoNotFold(t *testing.T) {
	const sameTail = "status 128: fatal: bad object HEAD"
	a, _ := twinWith("01KZSAME", "/home/u/repo/.flightdeck/worktrees/fd-a", sameTail)
	_, b := twinWith("01KZSAME", "/home/u/repo/.flightdeck/worktrees/fd-b", sameTail)

	if n := countAxisRows(renderTwo(t, a, b)); n != 2 {
		t.Fatalf("경로가 다른데 %d줄로 접혔다 — 한쪽 워크트리가 화면에서 사라진다:\n%s",
			n, renderTwo(t, a, b))
	}
}

// TestSameSessionDifferentCausesDoNotFold — **원인만** 다르면 접지 않는다(경로는 같게 둔다).
//
// 접힌 줄은 원인도 **하나만** 싣는다. 두 축이 서로 다른 이유로 죽었는데 한 줄로 뭉치면
// 나머지 원인이 사라진다 — 예컨대 경로 축은 디렉토리 부재로, 규모 축은 타임아웃으로 죽은
// 판이 "둘 다 같은 원인"으로 보고된다.
func TestSameSessionDifferentCausesDoNotFold(t *testing.T) {
	const samePath = "/home/u/repo/.flightdeck/worktrees/fd-x"
	a, _ := twinWith("01KZSAME", samePath, "status 128: fatal: cannot change to '…': No such file or directory")
	_, b := twinWith("01KZSAME", samePath, "status -1: signal: killed: context deadline exceeded")

	if n := countAxisRows(renderTwo(t, a, b)); n != 2 {
		t.Fatalf("원인이 다른데 %d줄로 접혔다 — 한쪽 원인이 화면에서 사라진다:\n%s",
			n, renderTwo(t, a, b))
	}
}

// TestUnrelatedAxesAreUntouched — 이 접기는 uncommitted 쌍에만 건다.
func TestUnrelatedAxesAreUntouched(t *testing.T) {
	fs := []service.DerivedFailure{
		{Axis: "git-head", Detail: "fatal: not a git repository"},
		{Axis: "merge-base:feat-x", Detail: "갈래 지점을 못 읽었다"},
	}
	got := RenderPick(service.PickResult{
		Mode: service.PickNone, Reason: "적격 0건이다",
		Derived: service.Derived{Failures: fs},
	}, t0)

	if n := countAxisRows(got); n != 2 {
		t.Fatalf("관계없는 축 둘이 %d줄이 됐다:\n%s", n, got)
	}
	if !sameLine(got, "git-head", "fatal: not a git repository") {
		t.Fatalf("관계없는 축의 원인이 사라졌다:\n%s", got)
	}
}

// countAxisRows 는 파생 실패 절의 **축 줄** 수다(머리줄 `못 읽은 파생 N축:` 은 뺀다).
//
// 머리줄과 축 줄을 가르는 이유가 이 항목의 전부다 — 접기는 축 줄 수를 줄이되 머리줄의
// N 은 그대로 둬야 한다.
func countAxisRows(rendered string) int {
	n, in := 0, false
	for _, line := range strings.Split(rendered, "\n") {
		switch {
		case strings.HasPrefix(line, "못 읽은 파생 "):
			in = true
		case in && strings.HasPrefix(line, "  · "):
			n++
		case in && strings.HasPrefix(line, "  … "):
			// 상한으로 접힌 꼬리줄. 축 줄이 아니다.
		case in:
			in = false
		}
	}
	return n
}
