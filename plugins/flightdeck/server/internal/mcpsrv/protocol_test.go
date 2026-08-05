package mcpsrv

import (
	"strings"
	"testing"
)

// 이 파일의 소비자 좌표계는 **호스트가 보는 값**이다 —
// initialize 응답의 protocolVersion 문자열과 세션 시작에 실리는 instructions.

func TestNegotiateProtocol(t *testing.T) {
	cases := []struct {
		name, requested, want string
	}{
		{"아는 최신", "2025-06-18", "2025-06-18"},
		{"아는 구판", "2025-03-26", "2025-03-26"},
		{"아는 최구판", "2024-11-05", "2024-11-05"},
		{"모르는 미래판", "2099-01-01", DefaultProtocolVersion},
		{"빈 값", "", DefaultProtocolVersion},

		// ── 표 밖 케이스 ── 규약에 없는 모양이 실제로 온다.
		{"앞뒤 공백", "  2025-06-18  ", "2025-06-18"},
		{"대소문자 섞인 쓰레기", "LATEST", DefaultProtocolVersion},
		{"버전이 아니라 문장", "protocolVersion", DefaultProtocolVersion},
		{"개행이 섞임", "2025-06-18\n", "2025-06-18"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NegotiateProtocol(c.requested); got != c.want {
				t.Fatalf("NegotiateProtocol(%q) = %q, 기대 %q", c.requested, got, c.want)
			}
		})
	}
}

// TestInstructionsBudget 은 설계 §6 의 "instructions 300자" 예산을 지킨다.
//
// 이 예산이 도구를 일곱으로 눌러 잡은 이유이고, 문구가 자라면 세션 시작 컨텍스트가 자란다.
// 그 일곱을 잠그는 것은 바로 아래 TestToolTableIsSeven 이다(이름·순서까지 못박는다).
func TestInstructionsBudget(t *testing.T) {
	n := len([]rune(Instructions))
	if n > InstructionsLimit {
		t.Fatalf("instructions 가 %d자다 — 상한 %d자", n, InstructionsLimit)
	}
	// 규율 산문이 여기 들어오는 것이 정확히 막으려는 것이다.
	for _, banned := range []string{"핸드오프", "무엇을 적어야", "① "} {
		if strings.Contains(Instructions, banned) {
			t.Fatalf("instructions 에 규율 산문 %q 가 들어왔다 — 그것은 응답 꼬리 몫이다", banned)
		}
	}
	// 설계 §6 이 못박은 세 줄이 그대로 있어야 한다.
	for _, want := range []string{
		"작업은 `pick`, 판단은 `note`, 끝나면 `finish`. 락은 없다.",
		"head·branch·sha·랜딩 이력은 서버가 git 에서 읽으므로 적지 마라.",
		"겹침·선점·미확인 결과는 응답 꼬리에 온다.",
	} {
		if !strings.Contains(Instructions, want) {
			t.Fatalf("instructions 에 설계 §6 의 줄이 없다: %q", want)
		}
	}
}

func TestToolTableIsSeven(t *testing.T) {
	got := ToolNames()
	want := []string{"board", "pick", "note", "add", "finish", "alloc", "land"}
	if len(got) != len(want) {
		t.Fatalf("도구가 %d개다(%v) — 랜딩 순서 큐가 land 를 더해 7개다", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("도구 %d번이 %q다 — 기대 %q", i, got[i], want[i])
		}
	}
	if KnownTool("status") {
		t.Fatal("KnownTool 이 표에 없는 이름을 참으로 봤다")
	}
	// 설명이 길면 세션 시작 컨텍스트가 자란다. 규율 산문은 응답 꼬리 몫이다.
	for _, tl := range Tools() {
		if n := len([]rune(tl.Description)); n > 90 {
			t.Fatalf("도구 %s 의 설명이 %d자다 — 90자 안으로", tl.Name, n)
		}
		if tl.InputSchema == nil || tl.InputSchema["type"] != "object" {
			t.Fatalf("도구 %s 의 inputSchema 가 object 가 아니다", tl.Name)
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		name, in string
		wantMin  int
		wantMax  int
	}{
		{"빈 문자열", "", 0, 0},
		{"ASCII 열 자", "abcdefghij", 3, 3},
		{"한글 열 자", "가나다라마바사아자차", 15, 15},
		{"섞임", "가나다abcd", 6, 6},

		// ── 표 밖 케이스 ──
		{"개행만", "\n\n\n", 1, 1},
		{"이모지(서로게이트 아님, 룬 1개)", "🚀", 2, 2},
		{"긴 ASCII 는 한글보다 싸다", strings.Repeat("a", 100), 30, 30},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EstimateTokens(c.in)
			if got < c.wantMin || got > c.wantMax {
				t.Fatalf("EstimateTokens(%q) = %d, 기대 %d..%d", clip(c.in, 20), got, c.wantMin, c.wantMax)
			}
		})
	}
	// 단조성 — 문자를 더하면 값이 줄지 않는다. 상한 어림의 최소 조건이다.
	if EstimateTokens("가나") < EstimateTokens("가") {
		t.Fatal("EstimateTokens 가 단조가 아니다")
	}
}
