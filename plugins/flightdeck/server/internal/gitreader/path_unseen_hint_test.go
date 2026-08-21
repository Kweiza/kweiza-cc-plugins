package gitreader

import (
	"strings"
	"testing"
)

// 경로가 서버에서 안 보이면 **그 사실을 말한다** (2026-08-22).
//
// ★ 이 힌트가 없어서 난 일: 다른 머신(NAS)에서 등록 7건 중 **6건이 파생 불능**이었는데,
// 화면에는 "미판정"과 git 원문만 나왔다. 원인은 컨테이너 마운트(FD_REPOS)가 한 디렉토리뿐이라
// 나머지 저장소가 컨테이너 안에 아예 없었던 것이다. `healthz` 는 내내 ok 였고, **사람이
// 리포트를 쓰기 전까지 아무도 몰랐다.** 그 값은 갱신 때 안 주면 조용히 기본값으로 되돌아가므로
// 한 번 고쳐도 재발한다 — 그래서 증상을 만나는 자리에서 원인을 가리켜야 한다.
//
// ★ **단정하지 않는다.** 서버는 자기가 컨테이너인지, FD_REPOS 가 무엇인지 모른다.
// 아는 것은 "git 이 이 경로로 못 갔다"까지이고, 거기까지만 말하고 조건부로 가리킨다.
func TestCommandErrorPointsAtTheMountWhenThePathIsUnseen(t *testing.T) {
	ce := &CommandError{
		Args:     []string{"-C", "/home/kweiza/ccdaddy", "status"},
		ExitCode: 128,
		Stderr:   "fatal: cannot change to '/home/kweiza/ccdaddy': No such file or directory",
	}
	msg := ce.Error()
	if !strings.Contains(msg, "FD_REPOS") {
		t.Fatalf("경로를 못 본 실패인데 마운트를 안 가리킨다 — 이 문장이 없으면 원인이 안 보인다:\n%s", msg)
	}
	// 원문은 그대로 남아야 한다 — 힌트가 사실을 덮으면 안 된다.
	if !strings.Contains(msg, "No such file or directory") {
		t.Fatalf("git 원문이 사라졌다 — 힌트는 덧붙이는 것이지 대체하는 것이 아니다:\n%s", msg)
	}
}

// 무관한 실패에는 안 붙는다 — 상시 점등은 판별력이 0이다(§4).
func TestCommandErrorDoesNotGuessTheMountOnUnrelatedFailures(t *testing.T) {
	for _, stderr := range []string{
		"fatal: not a git repository (or any of the parent directories): .git",
		"error: pathspec 'nope' did not match any file(s) known to git",
		"",
	} {
		ce := &CommandError{Args: []string{"-C", "/repo", "status"}, ExitCode: 128, Stderr: stderr}
		if strings.Contains(ce.Error(), "FD_REPOS") {
			t.Fatalf("무관한 실패에 마운트 힌트가 붙었다(stderr=%q):\n%s", stderr, ce.Error())
		}
	}
}
