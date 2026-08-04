package window

import "testing"

func TestKeyFileNameIsStableAndPathSafe(t *testing.T) {
	k := Key{MachineID: "m-abc", ClaudePID: 3980399, Started: "544443873"}
	if got, want := k.FileName(), "m-abc-3980399-544443873.json"; got != want {
		t.Fatalf("FileName() = %q, want %q", got, want)
	}
}

// ★ scrub 함수는 전사 함수여야 한다. 여러 다른 입력이 같은 파일명으로 붕괴하면
// 다른 머신이 한 파일을 공유하게 되고 비콘이 침묵하게 덮어진다.
func TestKeyFileNameIsInjective(t *testing.T) {
	machineIDs := []string{"host-1", "host/1", "host\\1", "host 1", "host.1"}
	const pid = 42
	const started = "100"

	fileNames := make(map[string]string)
	for _, id := range machineIDs {
		k := Key{MachineID: id, ClaudePID: pid, Started: started}
		fn := k.FileName()
		if prev, exists := fileNames[fn]; exists {
			t.Fatalf("collision: %q and %q both map to %q", prev, id, fn)
		}
		fileNames[fn] = id
	}
}

// ★ machine id 는 hostname 에서 오므로 경로 구분자가 들어올 수 있다.
// 그대로 파일명에 쓰면 디렉토리를 벗어난다.
func TestKeyFileNameScrubsSeparators(t *testing.T) {
	k := Key{MachineID: "a/../b c", ClaudePID: 7, Started: "9"}
	got := k.FileName()
	for _, bad := range []string{"/", "\\", ".."} {
		if contains(got, bad) {
			t.Fatalf("FileName() = %q, 경계 문자 %q 가 남았다", got, bad)
		}
	}
}

func TestEncodeDecodeRoundTrips(t *testing.T) {
	in := Beacon{
		ClaudePID: 3980399, ClaudeStarted: "544443873", MachineID: "m-abc",
		Worktree: "/home/aaron/w", CCSessionID: "cc-new", SessionID: "01ABC",
		UpdatedAt: "2026-08-05T10:00:00Z",
	}
	raw, err := Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out != in {
		t.Fatalf("round trip 이 값을 바꿨다\n got %+v\nwant %+v", out, in)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := Decode([]byte("not json")); err == nil {
		t.Fatal("깨진 JSON 인데 Decode 가 오류를 안 냈다")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
