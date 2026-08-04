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
