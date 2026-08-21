package store

import (
	"context"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// Dependents 는 **살아 있는** 종속만 센다 — 죽은 종속은 pick 순위의 1축을 부풀린다.
//
// ★ 실측이 이 시험의 근거다(원장 ~/.flightdeck/fd.db, 2026-08-21): 열린 항목 중 n>0 인
// 14건에서 3건이 역인덱스와 실제가 어긋나 있었다 — contracts-inbox-surface n=3 / 살아있는 0 ·
// repo-hostname-leak-sweep n=1 / 0 · docs-r208-connector-asset-owning-corpus n=2 / 1.
//
// 기전: item_dependents 는 addAfter(+1) · RemoveAfter(-n) · DeleteItem(-1) 에서만 움직이고,
// **기대던 항목이 done·dropped 로 닫혀도 안 준다.** 그래서 아무도 안 기다리는 항목이 영영
// "남이 기다린다"로 pick 의 1축을 이긴다. 그리고 그 틀림은 오류를 안 낸다 — 조용하다.
//
// ★ 고침의 방향은 **파생**이다(설계 §5). 닫을 때 -1 을 치는 길도 있지만 그러면 이미 부푼
// 기존 행에 마이그레이션이 필요하고, 다음에 상태 전이를 늘리는 사람이 같은 함정을 다시 밟는다.
// DependentItems 가 쓰는 것과 **같은 살아있음 정의**(open·claimed)를 한 자리에서 쓴다.
func TestDependentsCountsOnlyTheLivingOnes(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")
	for _, id := range []string{"dep", "w-open", "w-claimed", "w-done", "w-dropped"} {
		mustItem(t, s, "p", id)
	}
	if err := s.Tx(ctx, func(tx *Tx) error {
		for _, w := range []string{"w-open", "w-claimed", "w-done", "w-dropped"} {
			if err := tx.AddAfter("p", w, model.After{Item: "dep"}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("선행 등록 실패: %v", err)
	}
	// ── 대조 전제: 넷이 다 걸려 있는가. 안 걸렸으면 이 시험은 아무것도 안 본 것이다.
	if n, err := s.Dependents(ctx, "p", "dep"); err != nil || n != 4 {
		t.Fatalf("대조 전제가 깨졌다 — 넷이 걸린 상태에서 %d 다(err=%v)", n, err)
	}

	sess := mustSession(t, s, "p", "cc-1")
	if _, err := s.ClaimItem(ctx, "p", "w-claimed", sess.ID, time.Time{}); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	for _, c := range []struct {
		id    string
		state model.ItemState
	}{{"w-done", model.ItemDone}, {"w-dropped", model.ItemDropped}} {
		if err := s.Tx(ctx, func(tx *Tx) error {
			return tx.SetItemState("p", c.id, c.state, "시험 준비")
		}); err != nil {
			t.Fatalf("상태 변경 실패(%s): %v", c.id, err)
		}
	}

	n, err := s.Dependents(ctx, "p", "dep")
	if err != nil {
		t.Fatalf("종속 수 조회 실패: %v", err)
	}
	if n != 2 {
		t.Fatalf("Dependents 가 %d 다 — open·claimed 둘만 세야 한다.\n"+
			"죽은 종속을 세면 아무도 안 기다리는 항목이 pick 의 1축(judge.lessBundle 의 Dependents)에서\n"+
			"영영 이긴다. 실측으로 열린 14건 중 3건이 그 상태였다", n)
	}

	// 이름 축(DependentItems)과 **같은 수**여야 한다 — 둘이 갈리면 관문 문구와 순위가 서로 다른 세상을 본다.
	names, err := s.DependentItems(ctx, "p", "dep")
	if err != nil {
		t.Fatalf("종속 이름 조회 실패: %v", err)
	}
	if len(names) != n {
		t.Fatalf("수(%d)와 이름 수(%d)가 다르다 — 같은 살아있음 정의를 써야 한다: %v", n, len(names), names)
	}
}
