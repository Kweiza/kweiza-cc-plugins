package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 종료는 되돌리지 않는다 — 그 규율은 판정 함수가 아니라 UPDATE 의 WHERE 에도 있어야 한다.
//
// JudgeClaim 이 done/dropped 를 먼저 거절하므로(item.go 의 48-58줄) 지금 이 지점에 닿는
// 호출부는 없다. 그래서 이 시험은 저장층을 **직접** 부른다 — 그것이 이 가드가 사는 이유
// 그 자체다: 판정은 상태에서 유추하는 인프로세스 추론이고, 그것이 회귀하는 날 쓰기
// 자리에 아무것도 없으면 폐기 사유가 통째로 지워지면서 오류조차 안 난다(실측했다).
func TestReopeningAClosedItemIsRefused(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	mustItem(t, s, "p", "dropped-one")
	mustItem(t, s, "p", "done-one")
	if err := s.SetItemState(ctx, "p", "dropped-one", model.ItemDropped, "중복이라 접었다"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemState(ctx, "p", "done-one", model.ItemDone, ""); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		id   string
		now  model.ItemState // 지금 상태
		want model.ItemState // 되돌리려는 상태
	}{
		{"dropped-one", model.ItemDropped, model.ItemOpen},
		{"done-one", model.ItemDone, model.ItemClaimed},
	} {
		t.Run(c.id, func(t *testing.T) {
			// 대조 전제: 지금 정말로 끝나 있고 종료 좌표가 채워져 있다.
			before, err := s.GetItem(ctx, "p", c.id)
			if err != nil {
				t.Fatal(err)
			}
			if before.State != c.now || before.ClosedAt == nil {
				t.Fatalf("전제가 깨졌다 — %s 가 %s/closed_at 으로 안 끝났다: %+v", c.id, c.now, before)
			}

			err = s.SetItemState(ctx, "p", c.id, c.want, "")
			var closed *ItemClosedError
			if !errors.As(err, &closed) {
				t.Fatalf("%s 를 %s 로 되돌리는데 %v 가 나왔다 — *ItemClosedError 여야 한다", c.id, c.want, err)
			}
			if closed.ItemID != c.id || closed.State != c.now || closed.Want != c.want {
				t.Errorf("오류가 좌표를 잘못 담았다: %+v", closed)
			}
			// 있는 항목을 "없다"로 접으면 조사가 항목 id 오타부터 의심하게 된다.
			if errors.Is(err, ErrNotFound) {
				t.Errorf("있는 항목의 종료 되돌리기가 없음(404)으로 접혔다: %v", err)
			}

			after, err := s.GetItem(ctx, "p", c.id)
			if err != nil {
				t.Fatal(err)
			}
			if after.State != before.State {
				t.Errorf("%s 의 상태가 %s 로 되살아났다", c.id, after.State)
			}
			if after.CloseReason != before.CloseReason {
				t.Errorf("%s 의 종료 사유가 %q → %q 로 지워졌다", c.id, before.CloseReason, after.CloseReason)
			}
			if after.ClosedAt == nil {
				t.Errorf("%s 의 closed_at 이 지워졌다 — 언제 끝났는지가 사라진다", c.id)
			}
		})
	}
}

// 대조 — 가드가 정상 갈래를 막지 않는다.
//
// 특히 **claimed → claimed** 다. JudgeClaim 에는 "미선점인데 상태가 claimed"인 갈래가
// 있고(item.go 의 69-73줄) 그 갈래는 이미 claimed 인 행에 claimed 를 다시 쓴다.
// 가드가 그 행을 0행으로 떨어뜨리면 앞선 반납이 상태를 못 되돌린 항목을 아무도 다시
// 못 집는다 — 가드가 낼 수 있는 유일한 진짜 회귀가 여기다.
func TestStateGuardLeavesTheClaimPathIntact(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	s1 := mustSession(t, s, "p", "cc-A")
	s2 := mustSession(t, s, "p", "cc-B")
	mustItem(t, s, "p", "x")

	if _, err := s.ClaimItem(ctx, "p", "x", s1.ID); err != nil {
		t.Fatal(err)
	}
	if it, _ := s.GetItem(ctx, "p", "x"); it.State != model.ItemClaimed {
		t.Fatalf("전제가 깨졌다 — 선점이 상태를 안 옮겼다: %s", it.State)
	}

	// 선점만 반납하고 항목 상태는 claimed 로 남긴다 — JudgeClaim 이 흔적으로 읽는 그
	// 어긋남을 손으로 만든다. ReleaseClaim 은 상태까지 되돌리므로 여기서 못 쓴다.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE claim SET released_at = ? WHERE project = 'p' AND item_id = 'x'`,
		fmtTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	if it, _ := s.GetItem(ctx, "p", "x"); it.State != model.ItemClaimed {
		t.Fatalf("전제가 깨졌다 — 항목이 claimed 로 안 남았다: %s", it.State)
	}

	if _, err := s.ClaimItem(ctx, "p", "x", s2.ID); err != nil {
		t.Fatalf("점유자 없는 claimed 항목을 다시 못 집었다: %v — 가드가 정상 갈래를 막았다", err)
	}
	if it, _ := s.GetItem(ctx, "p", "x"); it.State != model.ItemClaimed {
		t.Errorf("재선점 뒤 상태가 %s 다", it.State)
	}
}

// 가드를 걸면 0행의 사유가 둘이 된다. 없는 항목은 그대로 없음(404)이어야 한다 —
// item_notfound_test.go 가 종료 갈래에 대해 잠근 것과 같은 규율을 열림 갈래에도 건다.
func TestReopeningAMissingItemIsStillNotFound(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")

	err := s.SetItemState(ctx, "p", "없는항목", model.ItemOpen, "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("없는 항목에 %v 를 냈다 — ErrNotFound 여야 한다", err)
	}
	var nf *NotFoundError
	if !errors.As(err, &nf) || nf.Kind != NFItem {
		t.Errorf("좌표 없는 없음이다: %v", err)
	}
	var closed *ItemClosedError
	if errors.As(err, &closed) {
		t.Errorf("없는 항목이 '이미 끝났다'로 접혔다: %v", err)
	}
}
