package store

import (
	"context"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

func TestRecordPickEvalKeepsLeadInPickedAndRestInPickedWith(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "P")

	err := s.RecordPickEval(ctx, model.PickEval{
		Project: "P", SessionID: "S1",
		Picked:     "lead-item",
		PickedWith: []string{"m1", "m2"},
	})
	if err != nil {
		t.Fatalf("기록 실패: %v", err)
	}

	var picked, with string
	row := s.db.QueryRowContext(ctx,
		`SELECT picked, COALESCE(picked_with,'') FROM pick_eval WHERE project='P'`)
	if err := row.Scan(&picked, &with); err != nil {
		t.Fatalf("읽기 실패: %v", err)
	}
	// ★ picked 는 선두를 계속 담는다. 기존 분포 질의가 안 깨지는 근거가 이 줄이다.
	if picked != "lead-item" {
		t.Fatalf("picked 가 %q 다 — 선두여야 한다", picked)
	}
	if with != `["m1","m2"]` {
		t.Fatalf("picked_with 가 %q 다", with)
	}
}

// 단독이면 picked_with 가 NULL 이다 — 빈 배열과 다르다.
// "묶을 게 없었다"와 "이 판이 그 축을 안 썼다"를 가르는 자리다.
func TestRecordPickEvalSoloLeavesPickedWithNull(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "P")
	if err := s.RecordPickEval(ctx, model.PickEval{
		Project: "P", SessionID: "S1", Picked: "solo",
	}); err != nil {
		t.Fatalf("기록 실패: %v", err)
	}
	var isNull bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT picked_with IS NULL FROM pick_eval WHERE project='P'`).Scan(&isNull); err != nil {
		t.Fatalf("읽기 실패: %v", err)
	}
	if !isNull {
		t.Fatal("단독인데 picked_with 가 NULL 이 아니다")
	}
}
