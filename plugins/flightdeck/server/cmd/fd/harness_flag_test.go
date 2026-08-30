package main

import "testing"

// TestSplitHarnessFlag 는 전역 --harness 파싱을 문다.
//
// ★ 왜 환경변수가 아니라 인자인가: 환경은 중첩 실행에서 상속되어 거짓말한다
// (DESIGN 「14. 하네스 축」 ②의 실측). 인자는 상속되지 않는다.
func TestSplitHarnessFlag(t *testing.T) {
	cases := []struct {
		name     string
		in       []string
		harness  string
		wantRest []string
	}{
		{"공백형", []string{"hook", "session-start", "--harness", "codex"}, "codex", []string{"hook", "session-start"}},
		{"등호형", []string{"mcp", "--harness=claude"}, "claude", []string{"mcp"}},
		{"앞에 와도 된다", []string{"--harness", "codex", "hook", "stop"}, "codex", []string{"hook", "stop"}},
		{"없으면 빈 값", []string{"hook", "stop"}, "", []string{"hook", "stop"}},
		// ★ 값이 없는 --harness 로 **뒤 인자를 삼키지 않는다.** 삼키면 `fd hook --harness`
		//   가 훅 이름을 잃고 fail-open 으로 조용히 아무것도 안 한다.
		{"값이 없다", []string{"hook", "--harness"}, "", []string{"hook"}},
		{"모르는 이름도 그대로 넘긴다", []string{"hook", "--harness", "codx"}, "codx", []string{"hook"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, rest := SplitHarnessFlag(c.in)
			if got != c.harness {
				t.Fatalf("하네스 %q — %q 를 기대했다", got, c.harness)
			}
			if len(rest) != len(c.wantRest) {
				t.Fatalf("남은 인자 %v — %v 를 기대했다", rest, c.wantRest)
			}
			for i := range rest {
				if rest[i] != c.wantRest[i] {
					t.Fatalf("남은 인자 %v — %v 를 기대했다", rest, c.wantRest)
				}
			}
		})
	}
}
