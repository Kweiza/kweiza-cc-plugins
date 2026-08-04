package window

import (
	"errors"
	"os"
	"testing"
)

// fakeTable 은 가짜 프로세스 표다. 시험이 진짜 /proc 을 안 건드리게 하는 씰이다.
func fakeTable(m map[int]int) func(int) (int, error) {
	return func(pid int) (int, error) {
		pp, ok := m[pid]
		if !ok {
			return 0, errors.New("그런 pid 가 없다")
		}
		return pp, nil
	}
}

func TestAncestorsWalksToTheTop(t *testing.T) {
	// 훅 → sh → claude → bash → tmux → init
	table := fakeTable(map[int]int{100: 200, 200: 300, 300: 400, 400: 500, 500: 1, 1: 0})
	got := Ancestors(100, table, 16)
	want := []int{100, 200, 300, 400, 500}
	if len(got) != len(want) {
		t.Fatalf("Ancestors = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Ancestors = %v, want %v", got, want)
		}
	}
}

func TestAncestorsStopsAtMaxDepth(t *testing.T) {
	table := fakeTable(map[int]int{1: 1}) // ★ 자기 자신이 부모인 표 — 무한 순회 유발
	got := Ancestors(1, table, 4)
	if len(got) > 4 {
		t.Fatalf("깊이 제한을 안 지켰다: %v", got)
	}
}

func TestAncestorsStopsWhenTheTableBreaks(t *testing.T) {
	table := fakeTable(map[int]int{7: 8}) // 8 은 표에 없다
	got := Ancestors(7, table, 16)
	if len(got) != 2 || got[0] != 7 || got[1] != 8 {
		t.Fatalf("Ancestors = %v, want [7 8] — 읽히는 데까지는 내야 한다", got)
	}
}

// ★ 이 시험은 실제 OS 씰을 친다. 리눅스에서는 자기 부모를 읽어야 하고,
// 다른 플랫폼에서는 **오류여야 한다**(빈 값이 아니라).
func TestProcSealAgreesWithTheRuntime(t *testing.T) {
	pp, err := PPidOf(os.Getpid())
	if errors.Is(err, ErrUnsupported) {
		t.Skip("이 플랫폼은 계보를 못 읽는다 — 오류를 냈으므로 계약은 지켜졌다")
	}
	if err != nil {
		t.Fatalf("PPidOf: %v", err)
	}
	if pp != os.Getppid() {
		t.Fatalf("PPidOf(self) = %d, os.Getppid() = %d", pp, os.Getppid())
	}
	st, err := StartedOf(os.Getpid())
	if err != nil {
		t.Fatalf("StartedOf: %v", err)
	}
	if st == "" {
		t.Fatal("StartedOf 가 빈 문자열을 냈다 — 빈 값은 모든 비콘을 통과시킨다")
	}
}
