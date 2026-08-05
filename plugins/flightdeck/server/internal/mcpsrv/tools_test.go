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
// 그 이유다(설계 §6). ★ 7개인 이유: 랜딩 순서 큐 설계가 이 브랜치가 main 에서
// 갈라진 뒤 land 도구 하나를 더해 6→7 로 눌러 잡았다(tools.go 상단 주석 참고) —
// 이 시험이 6을 단정하면 그 사실과 어긋나 원래 목적(도구 수 상한)과 무관하게 FAIL 한다.
func TestPickGainsItemIDsWithoutGrowingToolCount(t *testing.T) {
	if got := len(Tools()); got != 7 {
		t.Fatalf("도구가 %d개다 — 7개여야 한다(land 포함)", got)
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
