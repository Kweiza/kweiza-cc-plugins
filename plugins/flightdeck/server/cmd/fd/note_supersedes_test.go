package main

import (
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// `fd note --supersedes` 의 **CLI 이음매**를 지킨다.
//
// ★ 이 축은 REST 와 MCP 에는 있었고 CLI 에만 없었다. 고칠 수단이 있는데 한 표면에서만
// 못 부르는 것이 --item-project 가 없어서 "거절이 막힌 길을 가리켰던" 것과 같은 모양이다.
//
// 단정의 좌표계는 label_seam_test.go 와 같다 — **서버가 실제로 갖게 된 판단**이지,
// CLI 가 보낸 요청을 손으로 다시 단정하지 않는다. 이 하네스에는 요청 본문을 가로채는
// 가짜 서버가 없고(harness_test.go), 실물 서버 왕복이 wire.go 의 필드 이름이
// noteReq·internal/api 사이에서 어긋나는 결함까지 함께 잡는다.
func TestNoteCLICarriesSupersedesToStoredJudgment(t *testing.T) {
	h := newHarness(t)

	if code, out := h.run("", "note", "--kind", "decision", "--body", "옛 판단이다"); code != 0 {
		t.Fatalf("전제 구성 실패 — 첫 note 가 %d 로 끝났다:\n%s", code, out)
	}
	before := h.judgments(model.JudgmentDecision)
	if len(before) != 1 {
		t.Fatalf("전제가 깨졌다 — 판단이 %d건이다(1건이어야 한다)", len(before))
	}
	oldID := before[0].ID

	code, out := h.run("", "note", "--kind", "decision", "--body", "정정한다", "--supersedes", oldID)
	if code != 0 {
		t.Fatalf("note 가 %d 로 끝났다:\n%s", code, out)
	}

	after := h.judgments(model.JudgmentDecision)
	if len(after) != 2 {
		t.Fatalf("판단이 %d건이다(2건이어야 한다 — 정정은 새 행이지 덮어쓰기가 아니다):\n%s", len(after), out)
	}

	var newRow, oldRow *model.Judgment
	for i := range after {
		switch after[i].ID {
		case oldID:
			oldRow = &after[i]
		default:
			newRow = &after[i]
		}
	}
	if newRow == nil {
		t.Fatal("정정 행을 못 찾았다")
	}
	if newRow.Body != "정정한다" {
		t.Errorf("새 행의 본문이 %q다", newRow.Body)
	}
	if newRow.Supersedes != oldID {
		t.Errorf("supersedes 가 %q다 — %q(옛 행)여야 한다. 서버로 안 넘어갔을 수 있다", newRow.Supersedes, oldID)
	}
	// 옛 행은 그대로 남는다 — 덮어쓰기가 아니다.
	if oldRow == nil || oldRow.Body != "옛 판단이다" {
		t.Error("옛 행이 사라졌거나 바뀌었다 — 정정은 새 행 + supersedes 이지 덮어쓰기가 아니다")
	}
}
