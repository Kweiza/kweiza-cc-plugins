package service

import (
	"os/exec"
	"strings"
	"testing"
)

// 소비자 좌표계는 **셸이다.** 문자열을 단정하지 말고 실제로 실행해서 확인한다.
func TestSetupCommandsSurviveHostilePaths(t *testing.T) {
	for _, p := range []string{
		"/tmp/evil dir; touch /tmp/pwned_flightdeck",
		"/tmp/a'b",
		"/tmp/back`tick`",
		"/tmp/dollar$HOME",
		"/tmp/nl\nline",
	} {
		cmds := SetupCommands(p, "main", "t1-item")
		if len(cmds) == 0 {
			t.Fatalf("경로 %q 에 명령이 안 나왔다", p)
		}
		// `cd <인용된경로>` 대신 `printf %s <인용된경로>` 로 바꿔 실제 셸에 먹인다.
		// 셸이 무엇을 하나의 인자로 보는지가 정확히 우리가 지키려는 것이다.
		arg := strings.TrimPrefix(cmds[0], "cd ")
		out, err := exec.Command("sh", "-c", "printf %s "+arg).Output()
		if err != nil {
			t.Fatalf("경로 %q: 셸이 거부했다: %v", p, err)
		}
		if string(out) != p {
			t.Errorf("경로 %q 가 셸에서 %q 로 갈라졌다", p, string(out))
		}
	}
}
