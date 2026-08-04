package window

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func k1() Key { return Key{MachineID: "m1", ClaudePID: 42, Started: "1000"} }

func TestPlantCreatesWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	b, err := Plant(dir, k1(), "/w", "cc-old", at("2026-08-05T10:00:00Z"))
	if err != nil {
		t.Fatalf("Plant: %v", err)
	}
	if b.CCSessionID != "cc-old" || b.Worktree != "/w" {
		t.Fatalf("심은 값이 안 들어갔다: %+v", b)
	}
	if _, err := os.Stat(filepath.Join(dir, k1().FileName())); err != nil {
		t.Fatalf("파일이 안 생겼다: %v", err)
	}
}

// ★ 이 설계에서 가장 중요한 회귀 시험이다(설계 개정 ②).
// MCP 는 대화 중간에 재기동된다 — 실측: pid 643548 이 부모 claude 보다 6.6시간 늦게 떴다.
// 그때 통째로 덮으면 훅이 방금 고친 {새 cc, 카드A} 가 그 프로세스의 낡은 env cc 로 되돌아가고,
// 이어서 ensureSession 이 그 낡은 값을 집어 **이 기능이 고치려는 버그를 그대로 재현한다.**
func TestPlantNeverOverwritesTheHooksIdentity(t *testing.T) {
	dir := t.TempDir()
	if _, err := Plant(dir, k1(), "/w", "cc-old", at("2026-08-05T10:00:00Z")); err != nil {
		t.Fatalf("Plant: %v", err)
	}
	if _, err := SaveIdentity(dir, k1(), "cc-new", "card-A", at("2026-08-05T11:00:00Z")); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	// MCP 재기동 — 자기 낡은 env cc 로 다시 심는다.
	got, err := Plant(dir, k1(), "/w", "cc-old", at("2026-08-05T12:00:00Z"))
	if err != nil {
		t.Fatalf("두 번째 Plant: %v", err)
	}
	if got.CCSessionID != "cc-new" {
		t.Fatalf("재기동 심기가 cc 를 덮었다: %q — 버그가 되살아난다", got.CCSessionID)
	}
	if got.SessionID != "card-A" {
		t.Fatalf("재기동 심기가 session_id 를 덮었다: %q", got.SessionID)
	}
}

func TestPlantRefusesAnIncompleteKey(t *testing.T) {
	dir := t.TempDir()
	// ★ 설계 개정 ① — 정체가 반쪽이면 파일을 만들지 않는다.
	// Cursor 가 띄운 MCP 는 부모가 claude 가 아니고 CLAUDE_* 가 하나도 없다.
	// 거기서 심으면 어떤 훅도 영영 못 맞추는 pid 로 키가 잡힌다.
	if _, err := Plant(dir, Key{MachineID: "m1", ClaudePID: 0, Started: "1000"}, "/w", "cc", at("2026-08-05T10:00:00Z")); err == nil {
		t.Fatal("pid 가 없는 좌표인데 Plant 가 통과했다")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("거절했는데 파일이 %d개 생겼다", len(ents))
	}
}

func TestPlantRefusesAnEmptyCC(t *testing.T) {
	dir := t.TempDir()
	if _, err := Plant(dir, k1(), "/w", "", at("2026-08-05T10:00:00Z")); err == nil {
		t.Fatal("cc 가 빈데 Plant 가 통과했다")
	}
}

func TestLoadReportsAbsenceDistinctly(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir, k1()); err == nil {
		t.Fatal("없는 비콘인데 Load 가 오류를 안 냈다")
	}
}

func TestSaveIdentityKeepsCoordinates(t *testing.T) {
	dir := t.TempDir()
	if _, err := Plant(dir, k1(), "/w", "cc-old", at("2026-08-05T10:00:00Z")); err != nil {
		t.Fatalf("Plant: %v", err)
	}
	b, err := SaveIdentity(dir, k1(), "cc-new", "card-A", at("2026-08-05T11:00:00Z"))
	if err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}
	if b.Worktree != "/w" || b.ClaudePID != 42 || b.ClaudeStarted != "1000" {
		t.Fatalf("좌표가 지워졌다: %+v", b)
	}
}
