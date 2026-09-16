package store

import (
	"context"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

func strp(s string) *string       { return &s }
func pathsp(v []string) *[]string { return &v }

// amendFixture 는 항목 하나와 **실재하는 세션 하나**가 있는 저장소를 연다.
//
// 열기 헬퍼는 이 패키지의 기존 이름 `newStore`·`seed` 를 쓴다(브리프가 가정한
// `openTestStore`·`seedProjectAndItem` 은 이 패키지에 없다). 기존 `mustItem` 도 안 쓴다 —
// 그것은 제목을 항목 id 로 넣는데, 이 시험들이 재는 것이 **고치기 직전의 값**이라
// 제목과 id 가 같으면 "옛 값을 담았나"와 "id 를 담았나"가 구분되지 않는다.
//
// ★ 세션을 실제로 연다. `item_revision.session_id` 는 `session(id)` 를 참조하고 이 DB 는
// `_pragma=foreign_keys(1)` 로 열리므로, 아무 문자열이나 넣으면 FK 위반으로 죽는다 —
// 그 죽음은 amend 의 결함처럼 보이지만 실은 시험의 좌표가 틀린 것이다.
func amendFixture(t *testing.T) (*Store, context.Context, string) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p1")
	if err := s.AddItem(ctx, model.Item{
		Project: "p1", ID: "i1", Title: "원래 제목", Body: "원래 본문",
		Paths: []string{"services/"},
	}); err != nil {
		t.Fatalf("항목 준비 실패: %v", err)
	}
	return s, ctx, mustSession(t, s, "p1", "cc1").ID
}

// TestAmendItemWritesPreviousValueToRevision 은 이 표의 존재 이유를 잰다 —
// 개정 행에 들어가는 것이 **새 값이 아니라 옛 값**인가.
func TestAmendItemWritesPreviousValueToRevision(t *testing.T) {
	st, ctx, sess := amendFixture(t)

	var rec AmendRecord
	err := st.Tx(ctx, func(tx *Tx) error {
		var e error
		rec, e = tx.AmendItem("p1", "i1", AmendPatch{
			Title: strp("고친 제목"), Reason: "오타",
		}, sess)
		return e
	})
	if err != nil {
		t.Fatalf("고치지 못했다: %v", err)
	}
	if rec.Rev != 1 {
		t.Errorf("첫 개정의 rev 가 %d다 — 1이어야 한다", rec.Rev)
	}
	if rec.After.Title != "고친 제목" {
		t.Errorf("항목 제목이 %q다 — 고친 값이어야 한다", rec.After.Title)
	}

	// ★ 이 단정이 이 시험의 전부다. 개정 행은 옛 값을 담아야 한다.
	var gotTitle, gotReason string
	row := st.db.QueryRowContext(ctx,
		`SELECT title, reason FROM item_revision WHERE project=? AND item_id=? AND rev=1`, "p1", "i1")
	if err := row.Scan(&gotTitle, &gotReason); err != nil {
		t.Fatalf("개정 행을 못 읽었다: %v", err)
	}
	if gotTitle != "원래 제목" {
		t.Errorf("개정 행의 제목이 %q다 — **고치기 직전의 값**(\"원래 제목\")이어야 한다.\n"+
			"새 값을 쌓으면 현재가 두 곳에 생기고 둘이 갈리는 날 아무도 못 본다", gotTitle)
	}
	if gotReason != "오타" {
		t.Errorf("사유가 %q다 — 요청한 것이어야 한다", gotReason)
	}
}

// TestAmendItemLeavesOmittedAxesAlone 은 안 준 축이 안 변하는지 본다.
func TestAmendItemLeavesOmittedAxesAlone(t *testing.T) {
	st, ctx, sess := amendFixture(t)

	var rec AmendRecord
	err := st.Tx(ctx, func(tx *Tx) error {
		var e error
		rec, e = tx.AmendItem("p1", "i1", AmendPatch{Title: strp("새 제목"), Reason: "r"}, sess)
		return e
	})
	if err != nil {
		t.Fatalf("고치지 못했다: %v", err)
	}
	if rec.After.Body != rec.Before.Body {
		t.Errorf("본문이 %q → %q 로 변했다 — 안 준 축은 안 건드려야 한다", rec.Before.Body, rec.After.Body)
	}
	if len(rec.Changed) != 1 || rec.Changed[0] != "title" {
		t.Errorf("Changed 가 %v다 — [title] 이어야 한다", rec.Changed)
	}
}

// TestAmendItemPathsIsAnAxisToo 는 세 번째 축이 실제로 고쳐지고 Changed 의 순서가
// title·body·paths 로 고정인지 본다.
//
// ★ paths 를 따로 재는 이유: 이 축은 **겹침 판정의 입력**이라 몰래 고쳐지면 남의 화면이
// 조용히 움직인다. 관문이 감시 컬럼에 paths 를 새로 들인 것도 같은 이유다.
func TestAmendItemPathsIsAnAxisToo(t *testing.T) {
	st, ctx, sess := amendFixture(t)

	var rec AmendRecord
	err := st.Tx(ctx, func(tx *Tx) error {
		var e error
		rec, e = tx.AmendItem("p1", "i1", AmendPatch{
			Body:   strp("고친 본문"),
			Paths:  pathsp([]string{"web/", "services/"}),
			Reason: "리네임 추종",
		}, sess)
		return e
	})
	if err != nil {
		t.Fatalf("고치지 못했다: %v", err)
	}
	if len(rec.Changed) != 2 || rec.Changed[0] != "body" || rec.Changed[1] != "paths" {
		t.Errorf("Changed 가 %v다 — [body paths] 여야 한다(순서는 title·body·paths 로 고정)", rec.Changed)
	}

	it, err := st.GetItem(ctx, "p1", "i1")
	if err != nil {
		t.Fatalf("되읽기 실패: %v", err)
	}
	if len(it.Paths) != 2 || it.Paths[0] != "web/" || it.Paths[1] != "services/" {
		t.Errorf("항목 paths 가 %v다 — 준 순서 그대로여야 한다", it.Paths)
	}

	// 개정 행은 옛 paths 를 JSON 배열로 담아야 한다.
	var gotPaths string
	if err := st.db.QueryRowContext(ctx,
		`SELECT paths FROM item_revision WHERE project=? AND item_id=? AND rev=1`,
		"p1", "i1").Scan(&gotPaths); err != nil {
		t.Fatalf("개정 행을 못 읽었다: %v", err)
	}
	if gotPaths != `["services/"]` {
		t.Errorf("개정 행의 paths 가 %s다 — 고치기 직전의 값 [\"services/\"] 이어야 한다", gotPaths)
	}
}

// TestAmendItemSameValueIsNoChange 는 같은 값 재지정이 변화로 안 세지는지 본다.
//
// 거절하지는 않는다 — 다만 "고쳤다"고만 말하면 안 바뀐 것을 바뀐 줄 안다.
func TestAmendItemSameValueIsNoChange(t *testing.T) {
	st, ctx, sess := amendFixture(t)

	var rec AmendRecord
	err := st.Tx(ctx, func(tx *Tx) error {
		cur, e := tx.GetItem("p1", "i1")
		if e != nil {
			return e
		}
		rec, e = tx.AmendItem("p1", "i1", AmendPatch{Title: strp(cur.Title), Reason: "r"}, sess)
		return e
	})
	if err != nil {
		t.Fatalf("고치지 못했다: %v", err)
	}
	if len(rec.Changed) != 0 {
		t.Errorf("Changed 가 %v다 — 같은 값 재지정은 변화가 아니다", rec.Changed)
	}
}

// TestAmendItemRevStacks 는 두 번 고치면 rev 가 쌓이고 역순 복원이 원문을 내는지 본다.
func TestAmendItemRevStacks(t *testing.T) {
	st, ctx, sess := amendFixture(t)

	for i, title := range []string{"둘째", "셋째"} {
		want := i + 1
		var rec AmendRecord
		err := st.Tx(ctx, func(tx *Tx) error {
			var e error
			rec, e = tx.AmendItem("p1", "i1", AmendPatch{Title: strp(title), Reason: "r"}, sess)
			return e
		})
		if err != nil {
			t.Fatalf("%d번째 개정에서 실패했다: %v", want, err)
		}
		if rec.Rev != want {
			t.Fatalf("%d번째 개정의 rev 가 %d다", want, rec.Rev)
		}
	}

	// rev 1 이 원문을 들고 있어야 한다 — 역순 복원의 종점이다.
	var oldest string
	if err := st.db.QueryRowContext(ctx,
		`SELECT title FROM item_revision WHERE project=? AND item_id=? AND rev=1`,
		"p1", "i1").Scan(&oldest); err != nil {
		t.Fatalf("rev 1 을 못 읽었다: %v", err)
	}
	if oldest != "원래 제목" {
		t.Errorf("rev 1 의 제목이 %q다 — 원문이어야 한다", oldest)
	}
}

// TestAmendItemClosedItemIsAllowed 는 닫힌 항목도 고쳐지는지 본다.
//
// ★ label 과 **다른** 판정이다. label 은 끝난 항목을 거절한다(꼬리표가 뜻을 갖는 굶김
// 축이 열린 항목만 보기 때문이다). amend 는 거절하지 않는다 — 스펙 §2 ⓒ 가 여는 근거
// 중 하나이고, 닫힌 항목의 틀린 본문은 note 로도 못 닿는 자리다.
func TestAmendItemClosedItemIsAllowed(t *testing.T) {
	st, ctx, sess := amendFixture(t)
	if err := st.SetItemState(ctx, "p1", "i1", model.ItemDone, ""); err != nil {
		t.Fatalf("항목을 못 닫았다: %v", err)
	}

	err := st.Tx(ctx, func(tx *Tx) error {
		_, e := tx.AmendItem("p1", "i1", AmendPatch{Body: strp("닫힌 뒤 정정"), Reason: "r"}, sess)
		return e
	})
	if err != nil {
		t.Fatalf("닫힌 항목을 못 고쳤다: %v\n"+
			"amend 는 label 과 달리 종료 상태를 안 본다 — 그것이 이 표면을 연 근거 중 하나다", err)
	}
}

// TestAmendItemUnknownItem 은 없는 항목에 대한 거절이 표준 not-found 인지 본다.
func TestAmendItemUnknownItem(t *testing.T) {
	st, ctx, sess := amendFixture(t)

	err := st.Tx(ctx, func(tx *Tx) error {
		_, e := tx.AmendItem("p1", "없는거", AmendPatch{Title: strp("x"), Reason: "r"}, sess)
		return e
	})
	if err == nil {
		t.Fatal("없는 항목을 고쳤다고 답했다")
	}
	if !strings.Contains(err.Error(), "없는거") {
		t.Errorf("거절에 항목 id 가 없다: %v", err)
	}
}
