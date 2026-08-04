package window

import "testing"

func TestKeyFileNameIsStableAndPathSafe(t *testing.T) {
	k := Key{MachineID: "m-abc", ClaudePID: 3980399, Started: "544443873"}
	if got, want := k.FileName(), "m_2dabc-3980399-544443873.json"; got != want {
		t.Fatalf("FileName() = %q, want %q", got, want)
	}
}

// ★ scrub 함수는 전사 함수여야 한다. 여러 다른 입력이 같은 파일명으로 붕괴하면
// 다른 머신이 한 파일을 공유하게 되고 비콘이 침묵하게 덮어진다.
func TestKeyFileNameIsInjective(t *testing.T) {
	// 여러 충돌 벡터를 아우르는 Key 들의 표.
	// 1. MachineID 특수 문자: "host-1", "host/1", "host\1", "host 1", "host.1"
	// 2. 구분자 충돌: "a" vs "a-1"
	// 3. 음수 PID: -5 vs 5
	// 4. 빈 입력 가드: "" vs "x"
	allKeys := []Key{
		// 다양한 MachineID 입력 조합 (모두 pid=42, started="100")
		{MachineID: "host-1", ClaudePID: 42, Started: "100"},
		{MachineID: "host/1", ClaudePID: 42, Started: "100"},
		{MachineID: "host\\1", ClaudePID: 42, Started: "100"},
		{MachineID: "host 1", ClaudePID: 42, Started: "100"},
		{MachineID: "host.1", ClaudePID: 42, Started: "100"},

		// 구분자 충돌 벡터
		{MachineID: "a", ClaudePID: 1, Started: "23-5"},
		{MachineID: "a-1", ClaudePID: 23, Started: "5"},

		// 음수 PID 벡터
		{MachineID: "m", ClaudePID: -5, Started: "s"},
		{MachineID: "m", ClaudePID: 5, Started: "s"},

		// 빈 입력 가드 벡터 (MachineID)
		{MachineID: "", ClaudePID: 5, Started: "1"},
		{MachineID: "x", ClaudePID: 5, Started: "1"},

		// 빈 입력 가드 벡터 (Started)
		{MachineID: "m", ClaudePID: 7, Started: ""},
		{MachineID: "m", ClaudePID: 7, Started: "x"},
	}

	// 모든 Key 가 고유 파일명을 가져야 한다.
	fileNames := make(map[string]Key)
	for _, k := range allKeys {
		fn := k.FileName()
		if prev, exists := fileNames[fn]; exists {
			t.Fatalf("collision: %+v and %+v both map to %q", prev, k, fn)
		}
		fileNames[fn] = k
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
