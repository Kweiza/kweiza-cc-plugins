package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// ─────────────────────────────────────────────────────────────────────────────
// 선행을 끊는다 — `after-dropped-dep` 의 처방을 집행 가능하게 만드는 유일한 쓰기
// ─────────────────────────────────────────────────────────────────────────────
//
// judge.AfterSatisfied 는 선행 항목이 폐기되면 "기다려도 안 풀린다. **선행을 고쳐라**"를 낸다.
// 그런데 그 명령을 집행할 동사가 하나도 없었다 — add·claim·finish·move·note·land·alloc·snapshot
// 어느 것도 item_after 행을 못 지운다. 처방이 있는데 수단이 없으면 항목은 영구히 굶고,
// 출력에는 "고쳐라"만 계속 뜬다.
//
// ★ 여기서 지키는 진짜 축은 **역인덱스 정합**이다. item_dependents 는 pick 순위의 1축이라
// (judge.Eligible 의 Dependents), 행만 지우고 n 을 안 줄이면 아무도 안 기대는 항목이
// 영영 "남이 기다린다"로 상위에 뜬다. 그리고 그 틀림은 **오류를 안 낸다** — 조용하다.

// 선행 하나를 끊으면 행이 사라지고 역인덱스가 그만큼 준다.
func TestRemoveAfterDropsTheRowAndDecrementsTheReverseIndex(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")
	mustItem(t, s, "p", "dep")
	mustItem(t, s, "p", "waiter")

	if err := s.Tx(ctx, func(tx *Tx) error {
		return tx.AddAfter("p", "waiter", model.After{Item: "dep"})
	}); err != nil {
		t.Fatalf("선행 등록 실패: %v", err)
	}
	// ── 대조 전제: 끊기 전에 정말 걸려 있는가. 없으면 이 시험은 "안 깨졌다"가 아니라 아무것도 안 본 것이다.
	if n, err := s.Dependents(ctx, "p", "dep"); err != nil || n != 1 {
		t.Fatalf("대조 전제가 깨졌다 — 역인덱스가 %d 다(err=%v). 1 이어야 본다", n, err)
	}

	if err := s.RemoveAfter(ctx, "p", "waiter", model.After{Item: "dep"}, ""); err != nil {
		t.Fatalf("선행 절단 실패: %v", err)
	}

	if n := countIn(t, s, "item_after", "p", "waiter"); n != 0 {
		t.Errorf("item_after 에 %d행 남았다 — 끊었다는데 안 끊겼다", n)
	}
	n, err := s.Dependents(ctx, "p", "dep")
	if err != nil {
		t.Fatalf("역인덱스 조회 실패: %v", err)
	}
	if n != 0 {
		t.Errorf("역인덱스가 %d 다 — 이제 아무도 안 기대는데 pick 은 %d명이 기다린다고 읽는다", n, n)
	}
}

// 없는 선행을 끊으라고 하면 **거절한다.** 조용한 0건 성공이 아니다.
//
// 이 축이 없으면 오타 난 dep 이름이 성공으로 답한다. 사람은 처방을 집행했다고 믿고 떠나고,
// 항목은 그대로 after-dropped-dep 로 굶는다 — 그리고 다음 세션이 같은 조사를 처음부터 다시 한다.
func TestRemoveAfterRefusesWhenNoSuchDependencyIsAttached(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")
	mustItem(t, s, "p", "dep")
	mustItem(t, s, "p", "waiter")

	if err := s.Tx(ctx, func(tx *Tx) error {
		return tx.AddAfter("p", "waiter", model.After{Item: "dep"})
	}); err != nil {
		t.Fatalf("선행 등록 실패: %v", err)
	}

	err := s.RemoveAfter(ctx, "p", "waiter", model.After{Item: "dpe"}, "") // 오타
	if err == nil {
		t.Fatal("없는 선행을 끊었다는데 성공했다 — 사람은 고쳤다고 믿고 떠난다")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("없음이 아닌 오류로 나갔다 — 표면이 404 로 접지 못한다: %v", err)
	}
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("좌표 없는 오류다 — 무엇이 없었는지 표면이 못 조립한다: %v", err)
	}
	if nf.Kind != NFItemAfter {
		t.Errorf("종류가 %q 다 — %q 여야 항목 부재와 안 뭉개진다", nf.Kind, NFItemAfter)
	}

	// ── 걸려 있던 진짜 선행은 그대로여야 한다. 거절이 부작용을 남기면 더 나쁘다.
	if n := countIn(t, s, "item_after", "p", "waiter"); n != 1 {
		t.Errorf("거절했는데 item_after 가 %d행이다 — 엉뚱한 행을 건드렸다", n)
	}
	if n, _ := s.Dependents(ctx, "p", "dep"); n != 1 {
		t.Errorf("거절했는데 역인덱스가 %d 다 — 못 찾은 채로 감소시켰다", n)
	}
}

// 같은 선행이 여러 행이면 **전부** 끊고 역인덱스를 그만큼 줄인다.
//
// item_after 에는 UNIQUE 가 없다 — 같은 (item, dep) 이 두 번 들어갈 수 있고 AddAfter 가 안 막는다.
// 그때 행은 다 지우면서 n 은 1만 줄이면 역인덱스에 **거짓이 남는데 오류가 없다.**
// 그것이 pick 순위의 1축이라, 조용히 틀린 추천이 계속 나간다.
func TestRemoveAfterDropsEveryDuplicateRowAndKeepsTheIndexInStep(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")
	mustItem(t, s, "p", "dep")
	mustItem(t, s, "p", "waiter")

	if err := s.Tx(ctx, func(tx *Tx) error {
		if err := tx.AddAfter("p", "waiter", model.After{Item: "dep"}); err != nil {
			return err
		}
		return tx.AddAfter("p", "waiter", model.After{Item: "dep"})
	}); err != nil {
		t.Fatalf("선행 두 번 등록 실패: %v", err)
	}
	// ★ 대조 전제는 **행 수**로 잡는다(2026-08-21). Dependents 가 역인덱스에서 파생으로
	// 바뀌면서 `COUNT(DISTINCT item_id)` 가 됐다 — 같은 항목이 두 번 걸어도 **기다리는
	// 항목은 하나**라 이제 1을 낸다. 그 수를 전제로 쓰면 이 시험이 재는 것(중복 행을 전부
	// 지우는가)과 다른 축을 재게 된다. 원장 실측(2026-08-21): 중복 (item,dep) 쌍 0건이라
	// 이 전환의 실물 영향은 0이다.
	if n := countIn(t, s, "item_after", "p", "waiter"); n != 2 {
		t.Fatalf("대조 전제가 깨졌다 — item_after 에 %d행이다. 2 여야 이 시험이 성립한다", n)
	}
	if n, _ := s.Dependents(ctx, "p", "dep"); n != 1 {
		t.Fatalf("중복 행이 종속 수를 %d 로 부풀렸다 — 기대는 **항목**은 waiter 하나다", n)
	}

	if err := s.RemoveAfter(ctx, "p", "waiter", model.After{Item: "dep"}, ""); err != nil {
		t.Fatalf("선행 절단 실패: %v", err)
	}

	if n := countIn(t, s, "item_after", "p", "waiter"); n != 0 {
		t.Errorf("item_after 에 %d행 남았다 — 한 행만 지웠다", n)
	}
	if n, _ := s.Dependents(ctx, "p", "dep"); n != 0 {
		t.Errorf("종속 수가 %d 다 — 행은 다 지웠는데 수가 남았다. 조용한 거짓이다", n)
	}
}

// sha 선행도 끊긴다 — 그리고 역인덱스는 **안 건드린다.**
//
// after-bad-ref(rc=128, 오타이거나 지워진 커밋)도 기다려서 안 풀리는 축이라 같은 동사가 필요하다.
// 역인덱스는 dep_item 축에만 있으므로 sha 를 끊고 n 을 줄이면 **엉뚱한 항목의 순위**가 내려간다.
func TestRemoveAfterCutsAShaDepAndLeavesTheReverseIndexAlone(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")
	mustItem(t, s, "p", "dep")
	mustItem(t, s, "p", "waiter")

	if err := s.Tx(ctx, func(tx *Tx) error {
		if err := tx.AddAfter("p", "waiter", model.After{Item: "dep"}); err != nil {
			return err
		}
		return tx.AddAfter("p", "waiter", model.After{SHA: "0f19bf3"})
	}); err != nil {
		t.Fatalf("선행 등록 실패: %v", err)
	}

	if err := s.RemoveAfter(ctx, "p", "waiter", model.After{SHA: "0f19bf3"}, ""); err != nil {
		t.Fatalf("sha 선행 절단 실패: %v", err)
	}

	it, err := s.GetItem(ctx, "p", "waiter")
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if len(it.After) != 1 || it.After[0].Item != "dep" {
		t.Errorf("남은 선행이 %+v 다 — item 축 하나만 남아야 한다", it.After)
	}
	if n, _ := s.Dependents(ctx, "p", "dep"); n != 1 {
		t.Errorf("역인덱스가 %d 다 — sha 를 끊었는데 항목 축 수를 건드렸다", n)
	}
}

// 같은 선행을 든 **다른 항목**은 안 건드린다.
//
// WHERE 에서 item_id 가 빠지면 dep 하나를 끊는 명령이 그 dep 을 기다리던 모두를 푼다.
// 역인덱스는 지운 행 수만큼 줄어 겉보기 정합이 맞으므로, 이 시험이 없으면 아무 데서도 안 걸린다.
func TestRemoveAfterLeavesTheSameDepOnOtherItemsIntact(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")
	mustItem(t, s, "p", "dep")
	mustItem(t, s, "p", "waiter-a")
	mustItem(t, s, "p", "waiter-b")

	if err := s.Tx(ctx, func(tx *Tx) error {
		if err := tx.AddAfter("p", "waiter-a", model.After{Item: "dep"}); err != nil {
			return err
		}
		return tx.AddAfter("p", "waiter-b", model.After{Item: "dep"})
	}); err != nil {
		t.Fatalf("선행 등록 실패: %v", err)
	}

	if err := s.RemoveAfter(ctx, "p", "waiter-a", model.After{Item: "dep"}, ""); err != nil {
		t.Fatalf("선행 절단 실패: %v", err)
	}

	if n := countIn(t, s, "item_after", "p", "waiter-b"); n != 1 {
		t.Errorf("waiter-b 의 선행이 %d행이다 — 남의 대기까지 풀렸다", n)
	}
	if n, _ := s.Dependents(ctx, "p", "dep"); n != 1 {
		t.Errorf("역인덱스가 %d 다 — waiter-b 가 아직 기대는데 1 이 아니다", n)
	}
}

// 세 축 중 정확히 하나가 아니면 거절한다 — DELETE 를 쏘기 전에.
//
// 빈 After 를 그대로 SQL 에 넣으면 `dep_item IS NULL AND dep_job IS NULL AND dep_sha IS NULL` 이 되고,
// 그건 스키마 CHECK 상 존재할 수 없는 행이라 0건이 나온다. 즉 "없다"로 답하는데 진짜 사유는
// **입력이 틀린 것**이다 — 사람은 dep 이름을 의심하러 간다.
func TestRemoveAfterRefusesAMalformedDepBeforeTouchingTheTable(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")
	mustItem(t, s, "p", "waiter")

	err := s.RemoveAfter(ctx, "p", "waiter", model.After{}, "")
	if err == nil {
		t.Fatal("빈 선행이 통과했다")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("입력 오류가 '없다'로 나갔다 — 사람이 dep 이름을 의심하러 간다: %v", err)
	}
	if !strings.Contains(err.Error(), "정확히 하나") {
		t.Errorf("무엇이 틀렸는지 안 적혔다: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 누가 나에게 기대고 있나 — 폐기 관문이 **이름을 낼** 수 있어야 한다
// ─────────────────────────────────────────────────────────────────────────────

// DependentItems 는 아직 살아 있는 종속만 **이름으로** 낸다.
//
// 수(Dependents)만으로는 관문이 "3건이 막힌다"까지밖에 못 말하고, 그러면 무엇을 손봐야 하는지
// 다시 조사해야 한다 — 이 레포의 거절 문구 규율이 전부 이름을 내는 쪽이다
// (refuseIneligibleFollowup · judgeMissingFollowups).
//
// ★ **done·dropped 는 안 센다.** 끝난 항목은 이 선행이 폐기돼도 아무것도 안 잃는다.
// 그것까지 세면 오래된 프로젝트일수록 거짓 거절이 늘어 관문이 벽이 된다.
func TestDependentItemsNamesOnlyTheLivingOnes(t *testing.T) {
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

	got, err := s.DependentItems(ctx, "p", "dep")
	if err != nil {
		t.Fatalf("종속 조회 실패: %v", err)
	}
	// 선점된 것도 **살아 있는 일감이다** — judge.AfterSatisfied 가 open·claimed 를 똑같이
	// after-unmet-item("기다리면 풀린다")으로 보므로, 선행이 폐기되면 둘 다 영구히 굶는다.
	want := map[string]bool{"w-open": true, "w-claimed": true}
	if len(got) != len(want) {
		t.Fatalf("종속이 %v 다 — 살아 있는 둘만 나와야 한다", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("끝난 항목 %s 가 섞였다 — 거짓 거절이 된다", id)
		}
	}
}

// 끊은 사실이 **원장에** 남는다. SSE 발행으로 대신할 수 없다.
//
// 선행 절단은 되돌리는 코드가 없는 파괴적 쓰기다. 그런데 항목 본문은 만들어진 시점의 사진이라
// 무엇이 걸려 있었는지는 **끊는 순간 사라진다** — 원장에 안 남기면 "이 항목이 왜 지금 적격인가"에
// 답할 자리가 어디에도 없다. item.move 가 같은 이유로 원장을 쓴다(그쪽은 SSE 만 부르다 걸렸다).
func TestRemoveAfterLeavesTheCutInTheLedger(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")
	mustItem(t, s, "p", "dep")
	mustItem(t, s, "p", "waiter")
	sess := mustSession(t, s, "p", "cc-1")

	if err := s.Tx(ctx, func(tx *Tx) error {
		return tx.AddAfter("p", "waiter", model.After{Item: "dep"})
	}); err != nil {
		t.Fatalf("선행 등록 실패: %v", err)
	}
	if err := s.RemoveAfter(ctx, "p", "waiter", model.After{Item: "dep"}, sess.ID); err != nil {
		t.Fatalf("선행 절단 실패: %v", err)
	}

	var payload string
	err := s.db.QueryRowContext(ctx,
		`SELECT payload FROM event WHERE kind = 'item.after.cut' ORDER BY id DESC LIMIT 1`).Scan(&payload)
	if err != nil {
		t.Fatalf("원장에 item.after.cut 이 없다 — 무엇이 걸려 있었는지가 통째로 사라졌다: %v", err)
	}
	// 끊긴 관계의 **양쪽**과 축이 다 있어야 재구성된다. 하나만 있으면 "무엇이 사라졌나"에 못 답한다.
	for _, want := range []string{"waiter", "dep"} {
		if !strings.Contains(payload, want) {
			t.Errorf("원장 payload 에 %q 가 없다: %s", want, payload)
		}
	}
}
