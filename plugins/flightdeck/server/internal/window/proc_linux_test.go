//go:build linux

package window

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// ★ Alive 는 **실제 OS 씰**이다. Prune 의 "지울까" 판정이 이 함수 하나라
// 여기서 뒤집히면 결과가 비대칭이다: 참을 거짓으로 읽으면 멀쩡한 창의 비콘을 지워
// 그 창이 다음 /clear 에서 표류를 못 고치고, 거짓을 참으로 읽으면 파일이 쌓일 뿐이다.
// 그래서 두 방향을 다 잰다 — 한쪽만 재면 "항상 true" 라는 구현도 초록이다.
func TestAliveSeesThisProcessAndNotAFreeOne(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Fatal("이 프로세스를 죽었다고 본다 — Prune 이 살아 있는 창의 비콘을 지운다")
	}
	free := freePID(t)
	if Alive(free) {
		t.Fatalf("pid %d 는 프로세스 표에 없는데 살아 있다고 본다 — "+
			"죽은 창의 비콘이 영영 안 지워진다", free)
	}
	// 0 과 음수는 kill(2) 에서 **프로세스 그룹**을 뜻한다. 비콘의 pid 축으로는 좌표가 아니므로
	// 그 값이 흘러 들어와도 "살아 있다"로 읽으면 안 된다.
	for _, pid := range []int{0, -1} {
		if Alive(pid) {
			t.Fatalf("pid %d 를 살아 있다고 본다 — 그것은 프로세스가 아니라 그룹 지시자다", pid)
		}
	}
}

// freePID 는 프로세스 표에 없는 pid 하나다. pid_max 위는 커널이 ESRCH 로 답한다.
func freePID(t *testing.T) int {
	t.Helper()
	raw, err := os.ReadFile("/proc/sys/kernel/pid_max")
	if err != nil {
		t.Fatalf("pid_max 를 못 읽었다: %v", err)
	}
	max, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("pid_max 가 수가 아니다(%q): %v", raw, err)
	}
	return max + 1
}
