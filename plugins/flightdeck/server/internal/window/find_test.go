package window

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func startedTable(m map[int]string) func(int) (string, error) {
	return func(pid int) (string, error) {
		s, ok := m[pid]
		if !ok {
			return "", ErrUnsupported
		}
		return s, nil
	}
}

func TestFindMatchesAnAncestorEvenThroughAShell(t *testing.T) {
	dir := t.TempDir()
	k := Key{MachineID: "m1", ClaudePID: 300, Started: "1000"}
	if _, err := Plant(dir, k, "/w", "cc-old", time.Unix(0, 0)); err != nil {
		t.Fatalf("Plant: %v", err)
	}
	// 훅(100) → sh(200) → claude(300)
	m, ok, why := Find(dir, "m1", []int{100, 200, 300}, startedTable(map[int]string{100: "x", 200: "y", 300: "1000"}))
	if !ok {
		t.Fatalf("조상 사슬에 비콘이 있는데 못 찾았다: %s", why)
	}
	if m.Beacon.CCSessionID != "cc-old" || m.Key.ClaudePID != 300 {
		t.Fatalf("엉뚱한 비콘을 찾았다: %+v", m)
	}
}

// ★ 이것이 설계에서 가장 위험했던 자리다(개정 ③).
// 같은 머신·같은 워크트리에 창이 다섯이다. 조상이 아닌 창의 비콘을 집으면
// 그 창의 카드를 이 대화의 cc 로 rekey 하게 되고, 그 창의 선점과 판단이 통째로 딴 대화에 붙는다.
func TestFindRefusesABeaconThatIsNotAnAncestor(t *testing.T) {
	dir := t.TempDir()
	other := Key{MachineID: "m1", ClaudePID: 999, Started: "1000"}
	if _, err := Plant(dir, other, "/w", "cc-other", time.Unix(0, 0)); err != nil {
		t.Fatalf("Plant: %v", err)
	}
	// 디렉토리에 비콘이 딱 하나뿐이어도 조상이 아니면 안 쓴다.
	_, ok, why := Find(dir, "m1", []int{100, 200, 300}, startedTable(map[int]string{100: "x", 200: "y", 300: "1000"}))
	if ok {
		t.Fatal("조상이 아닌 창의 비콘을 집었다 — 남의 카드를 rekey 하게 된다")
	}
	if why == "" {
		t.Fatal("못 찾은 사유가 비었다 — 폴백 문구가 무엇을 말할지 알 수 없다")
	}
}

func TestFindRefusesWhenStartTimeDisagrees(t *testing.T) {
	dir := t.TempDir()
	k := Key{MachineID: "m1", ClaudePID: 300, Started: "1000"}
	if _, err := Plant(dir, k, "/w", "cc-old", time.Unix(0, 0)); err != nil {
		t.Fatalf("Plant: %v", err)
	}
	// pid 는 같지만 시작 시각이 다르다 → pid 가 재사용된 것이다.
	_, ok, why := Find(dir, "m1", []int{300}, startedTable(map[int]string{300: "9999"}))
	if ok {
		t.Fatalf("pid 재사용인데 통과했다 (%s)", why)
	}
}

func TestFindRefusesAnotherMachine(t *testing.T) {
	dir := t.TempDir()
	k := Key{MachineID: "m1", ClaudePID: 300, Started: "1000"}
	if _, err := Plant(dir, k, "/w", "cc-old", time.Unix(0, 0)); err != nil {
		t.Fatalf("Plant: %v", err)
	}
	if _, ok, _ := Find(dir, "m2", []int{300}, startedTable(map[int]string{300: "1000"})); ok {
		t.Fatal("다른 머신의 비콘을 집었다")
	}
}

func TestPruneRemovesDeadWindowsOnly(t *testing.T) {
	dir := t.TempDir()
	live := Key{MachineID: "m1", ClaudePID: 300, Started: "1000"}
	dead := Key{MachineID: "m1", ClaudePID: 301, Started: "1000"}
	for _, k := range []Key{live, dead} {
		if _, err := Plant(dir, k, "/w", "cc", time.Unix(0, 0)); err != nil {
			t.Fatalf("Plant: %v", err)
		}
	}
	n, err := Prune(dir, func(pid int) bool { return pid == 300 })
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("Prune 이 %d개 지웠다, 1개여야 한다", n)
	}
	if _, err := os.Stat(filepath.Join(dir, live.FileName())); err != nil {
		t.Fatal("살아 있는 창의 비콘을 지웠다")
	}
}

func TestPruneOnAMissingDirIsNotAnError(t *testing.T) {
	if _, err := Prune(filepath.Join(t.TempDir(), "nope"), func(int) bool { return true }); err != nil {
		t.Fatalf("없는 디렉토리는 조용히 넘어가야 한다: %v", err)
	}
}
