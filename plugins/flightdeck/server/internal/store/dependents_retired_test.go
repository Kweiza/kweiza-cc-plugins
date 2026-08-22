package store

import (
	"context"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// ─────────────────────────────────────────────────────────────────────────────
// item_dependents 는 죽은 표다 — 이 파일이 그것을 죽은 채로 붙잡는다
// ─────────────────────────────────────────────────────────────────────────────
//
// 2026-08-21 개정으로 Store.Dependents 가 item_after 파생 질의가 되면서 이 표를 **읽는
// 문이 0**이 됐고, 2026-08-22 에 **쓰는 문도 0**이 됐다(bumpDependents 와 그 호출 셋 제거,
// 증분 010 이 남은 143행 삭제).
//
// ★ 왜 시험이 필요한가: 되살아나는 것은 오류를 안 낸다. 누가 addAfter 에 +1 을 되돌려 놓아도
// 아무 데도 안 빨개진다 — 읽는 쪽이 없으니 값이 갈려도 아무도 못 본다. 그리고 그 갈린 값을
// 사람이 sqlite3 으로 직접 읽는다(이 레포의 실측 문화). 그것이 항목
// fd-item-dependents-table-has-no-readers 가 겨냥한 오독이고, 여기가 그 자리를 막는다.

// itemDependentsAll 은 표 전체를 (project, item_id, n) 로 낸다.
//
// ★ 표가 없으면 **이름으로** 말한다. 언젠가 표까지 걷는 판이 오면 그때 빨개지는 것이 옳고,
// 그때 읽을 문장은 "쿼리 실패"가 아니라 "이 시험도 함께 걷어라"여야 한다.
func itemDependentsAll(t *testing.T, s *Store) map[[2]string]int {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT project, item_id, n FROM item_dependents ORDER BY project, item_id`)
	if err != nil {
		t.Fatalf("item_dependents 를 못 읽었다: %v\n"+
			"표를 DROP 했다면 이 시험도 함께 걷어라 — 지킬 대상이 사라진 것이지 결함이 아니다.\n"+
			"(그 길은 `fd migrate` 신설이 먼저다 — migrate_guard_test.go 의 neverExempt)", err)
	}
	defer rows.Close()
	out := map[[2]string]int{}
	for rows.Next() {
		var p, id string
		var n int
		if err := rows.Scan(&p, &id, &n); err != nil {
			t.Fatalf("item_dependents 행 해석 실패: %v", err)
		}
		out[[2]string{p, id}] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("item_dependents 순회 실패: %v", err)
	}
	return out
}

// TestItemDependentsStaysRetired 는 걷어낸 쓰기 셋이 되살아나지 않는지를 본다.
//
// ★ **감시 행(sentinel)이 이 시험의 그물이다.** 빈 표에 대고 "행이 안 생겼다"만 보면
// 증가 경로(INSERT … ON CONFLICT)만 잡히고 감소 경로(UPDATE)는 원리적으로 안 잡힌다 —
// 만질 행이 없어 0행에 도는 UPDATE 는 아무 흔적도 안 남기기 때문이다. 옛 코드의 주석이
// 스스로 그 비대칭을 적어 뒀다("이 가드는 지금 시험으로 못 잡는다 — 빼도 초록이다").
// 그래서 표에 행 하나를 **일부러 심고**, 걷어낸 셋이 전부 그 행을 건드리도록 배치한다:
//
//	addAfter(+1)    → sentinel 의 n 이 오른다
//	RemoveAfter(-n) → sentinel 의 n 이 내린다
//	DeleteItem(-1)  → sentinel 의 n 이 내린다
//	DeleteItem 의 `DELETE FROM item_dependents` → **지운 항목 자신의 행**이 사라진다
//
// ★ 마지막 것 때문에 탐침이 **둘**이다. 처음 판은 sentinel 하나만 두고 "셋을 다 잡는다"고
// 적었는데, 변이를 실제로 넣어 보니 그 DELETE 만은 **안 잡혔다** — 지울 행이 표에 없으면
// 0행에 도는 DELETE 는 아무 흔적도 안 남기기 때문이다(감소 경로를 잡으려고 탐침을 심은 것과
// 정확히 같은 이유다). 그래서 지워질 항목 자신의 이름으로 두 번째 탐침을 심는다.
//
// 넷 중 **하나만** 되살아나도 마지막 대조가 빨개진다.
//
// ★ 심은 행은 현실의 재현이 아니다(증분 010 뒤 운영 DB 는 0행이다). 그물을 조이려고 둔
// 탐침이고, 그 사실을 여기 적어 둔다 — 다음 사람이 이 행을 "남아 있어야 하는 값"으로 읽지
// 않게 하려는 것이다.
func TestItemDependentsStaysRetired(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")
	seed(t, s, "q") // MoveItem 의 목적지

	mustItem(t, s, "p", "sentinel")
	mustItem(t, s, "p", "doomed") // ④ 에서 지운다 — 그 항목 자신의 탐침을 갖는다

	// ── 탐침 둘을 심는다 ──
	// sentinel 은 **증감**을 잡고(누가 n 을 만지면 값이 갈린다),
	// doomed 는 **삭제**를 잡는다(지운 항목의 행을 치우는 문이 되살아나면 행이 사라진다).
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO item_dependents(project, item_id, n)
		VALUES ('p', 'sentinel', 9), ('p', 'doomed', 4)`); err != nil {
		t.Fatalf("탐침 심기 실패: %v", err)
	}
	want := map[[2]string]int{{"p", "sentinel"}: 9, {"p", "doomed"}: 4}

	// ── 대조 전제: 증분 010 이 돈 DB 에 탐침 둘뿐인가 ──
	// 이것이 깨지면 아래 단정은 "안 늘었다"가 아니라 아무것도 안 본 것이다.
	if got := itemDependentsAll(t, s); len(got) != 2 ||
		got[[2]string{"p", "sentinel"}] != 9 || got[[2]string{"p", "doomed"}] != 4 {
		t.Fatalf("대조 전제가 깨졌다 — 표가 %v 다. 탐침 두 행이어야 이 시험이 성립한다", got)
	}

	// ── 걷어낸 쓰기 셋의 경로를 전부 태운다 ──

	// ① AddItem 이 선행을 함께 넣는 경로(내부 addAfter). 옛 코드면 sentinel 이 10 이 된다.
	if err := s.AddItem(ctx, model.Item{
		Project: "p", ID: "waiter-a", Title: "waiter-a", Body: "본문",
		After: []model.After{{Item: "sentinel"}},
	}); err != nil {
		t.Fatalf("선행을 단 항목 등록 실패: %v", err)
	}

	// ② AddAfter — 이미 있는 항목에 선행을 더한다. 옛 코드면 또 오른다.
	mustItem(t, s, "p", "waiter-b")
	if err := s.Tx(ctx, func(tx *Tx) error {
		return tx.AddAfter("p", "waiter-b", model.After{Item: "sentinel"})
	}); err != nil {
		t.Fatalf("선행 등록 실패: %v", err)
	}

	// ③ RemoveAfter — 옛 코드면 지운 행 수만큼 내린다.
	if err := s.RemoveAfter(ctx, "p", "waiter-b", model.After{Item: "sentinel"}, ""); err != nil {
		t.Fatalf("선행 절단 실패: %v", err)
	}

	// ④ DeleteItem — 옛 코드면 둘을 한꺼번에 한다: 걸려 있던 선행만큼 sentinel 을 -1 하고,
	//    지운 항목 자신의 행(doomed)을 표에서 치운다. 탐침 둘이 각각 그 하나씩을 본다.
	if err := s.Tx(ctx, func(tx *Tx) error {
		return tx.AddAfter("p", "doomed", model.After{Item: "sentinel"})
	}); err != nil {
		t.Fatalf("선행 등록 실패: %v", err)
	}
	if err := s.DeleteItem(ctx, "p", "doomed"); err != nil {
		t.Fatalf("항목 삭제 실패: %v", err)
	}

	// ⑤ MoveItem — 이 표를 아직 **읽지 않고 옮기기만 하는** 유일한 살아 있는 문이다
	//    (move.go 의 딸린 표 목록). 죽은 표라도 (project, item_id) 를 들고 있으므로 목록에
	//    남는 것이 맞고, 그 문이 행을 되살리지 않는다는 것을 여기서 함께 본다.
	if err := s.Tx(ctx, func(tx *Tx) error {
		return tx.MoveItem("p", "waiter-b", "q", "")
	}); err != nil {
		t.Fatalf("항목 이동 실패: %v", err)
	}

	// ── 전제 확인: 위 경로들이 실제로 뭔가를 했는가 ──
	// 안 했으면 아래 대조는 통과하지만 아무것도 안 지킨 것이다.
	if n := countIn(t, s, "item_after", "q", "waiter-b"); n != 0 {
		t.Fatalf("전제가 깨졌다 — 옮긴 waiter-b 의 선행이 %d행이다. ③에서 끊었으므로 0이어야 한다", n)
	}
	var edges int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM item_after WHERE dep_item = 'sentinel'`).Scan(&edges); err != nil {
		t.Fatalf("전제 확인 실패: %v", err)
	}
	if edges != 1 {
		t.Fatalf("전제가 깨졌다 — sentinel 로 가는 간선이 %d행이다. ①의 waiter-a 하나만 남아야 한다\n"+
			"(②는 ③에서 끊었고, ④의 doomed 는 삭제되며 CASCADE 로 사라진다)", edges)
	}

	// ── 본 단정 ──
	got := itemDependentsAll(t, s)
	if len(got) != len(want) {
		t.Fatalf("item_dependents 가 %v 다 — 기대 %v.\n"+
			"이 표는 **죽었다**(읽는 문 0 · 쓰는 문 0 · 증분 010 이 값을 비웠다).\n"+
			"행이 늘었다면 누가 유지 코드를 되살린 것이다 — 그 값은 파생 질의(Store.Dependents)와\n"+
			"곧 갈리고, **갈려도 오류가 안 난다.** 그것이 이 표를 죽인 이유 자체다.\n"+
			"수가 필요하면 item_after 에서 파생으로 세라.", got, want)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("탐침 %v 의 n 이 %d 다 — 심은 값 %d 그대로여야 한다.\n"+
				"누가 증감 코드를 되살렸다(증가면 addAfter, 감소면 RemoveAfter·DeleteItem).", k, got[k], w)
		}
	}
}
