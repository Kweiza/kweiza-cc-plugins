package mcpsrv

import (
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 쓸 수 있는데 아무 데도 안 보이는 종류는 거짓 초록이다.
// note(kind='now') 는 저장은 되고 보드 어디에도 안 나온다 — 도구가 그것을 제안하면 안 된다.
func TestNoteKindEnumHasNoInvisibleKinds(t *testing.T) {
	var noteTool Tool
	for _, tl := range Tools() {
		if tl.Name == "note" {
			noteTool = tl
		}
	}
	props := noteTool.InputSchema["properties"].(map[string]any)
	kind := props["kind"].(map[string]any)
	for _, v := range kind["enum"].([]any) {
		if v == "now" {
			t.Fatal("note.kind 에 'now' 가 있다 — 보드가 안 읽는 종류다")
		}
	}
}

// 반대로 model 상수는 남아 있어야 한다. 레거시 임포터가 생산하고 DB 에 이미 행이 있다.
func TestJudgmentNowConstantSurvives(t *testing.T) {
	if model.JudgmentNow != "now" {
		t.Fatal("레거시 임포터가 쓰는 상수를 지웠다 — DB 에 있는 행을 못 읽게 된다")
	}
}

// pick 에 item_ids 를 더해도 도구 수는 늘지 않는다 — 세션 시작 컨텍스트 예산이
// 그 이유다(설계 §6). ★ 8개인 이유: 랜딩 순서 큐 설계가 6→7 로(land), 항목 꼬리표
// 표면이 7→8 로(label) 눌러 잡았다(tools.go 상단 주석 참고) — 이 시험이 다른 수를
// 단정하면 그 사실과 어긋나 원래 목적(도구 수 상한)과 무관하게 FAIL 한다.
func TestPickGainsItemIDsWithoutGrowingToolCount(t *testing.T) {
	if got := len(Tools()); got != 8 {
		t.Fatalf("도구가 %d개다 — 8개여야 한다(land·label 포함)", got)
	}
	var pick *Tool
	for i := range tools {
		if tools[i].Name == "pick" {
			pick = &tools[i]
		}
	}
	if pick == nil {
		t.Fatal("pick 도구가 없다")
	}
	props := pick.InputSchema["properties"].(map[string]any)
	if _, ok := props["item_ids"]; !ok {
		t.Fatalf("pick 에 item_ids 가 없다: %v", props)
	}
	if _, ok := props["item_id"]; !ok {
		t.Fatal("item_id 가 사라졌다 — 단독 선점·재개 경로가 깨진다")
	}
}

// 반납도 **같은 값을 치르고** pick 에 얹혔다 — 9번째 도구가 아니라 인자 하나다.
//
// ★ tools.go 머리주석이 "더는 늘리지 않는다"를 두 번 못박는다. 세션 시작에 실리는 것은
// 도구 이름과 설명뿐이고 그 예산이 도구 수를 눌러 잡는다(설계 §6). 반납을 9번째 도구로
// 내면 그 규율을 근거 없이 깨는 것이고, 반납은 선점과 **같은 축**이라 얹을 자리가 이미 있었다.
//
// 이름이 leave 인 것도 임의가 아니다 — land 가 이미 leave(자기 이탈) / release(3자 회수, 거절)
// 로 두 축을 갈라 뒀다. pick 의 회수 축은 steal_reason(거절)이므로 남은 칸이 leave 다.
func TestPickGainsLeaveWithoutGrowingToolCount(t *testing.T) {
	if got := len(Tools()); got != 8 {
		t.Fatalf("도구가 %d개다 — 반납은 인자로 얹혔으므로 8개 그대로여야 한다", got)
	}
	var pick *Tool
	for i := range tools {
		if tools[i].Name == "pick" {
			pick = &tools[i]
		}
	}
	if pick == nil {
		t.Fatal("pick 도구가 없다")
	}
	props := pick.InputSchema["properties"].(map[string]any)
	if _, ok := props["leave"]; !ok {
		t.Fatalf("pick 에 leave 가 없다 — MCP 만 도는 세션의 유일한 반납 경로다: %v", props)
	}
	// 회수 축은 그대로 잠겨 있어야 한다. 반납이 생겼다고 회수가 열리면
	// "이 서버는 회수하지 않는다"는 판정이 조용히 뒤집힌다.
	if _, ok := props["steal_reason"]; !ok {
		t.Fatal("steal_reason 이 사라졌다 — 회수 거절이 표면에서 사라지면 잠금의 뜻이 흐려진다")
	}
}
