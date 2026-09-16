package store

import (
	"context"
	"strings"
	"testing"
)

// 이 파일이 재는 것 — **쌓은 개정을 되읽는 경로**다.
//
// `item_revision` 은 2026-09-16 에 생기고 하루 동안 읽는 경로가 레포 전체에 0건이었다.
// 그 상태에서는 "쌓았다"와 "쌓았는데 못 읽는다"가 화면에서 구분되지 않는다 —
// RenderAmend 가 `개정 N` 이라는 좌표를 약속하는데 그 좌표를 열 문이 없었기 때문이다.

// amendTwice 는 같은 항목을 두 번 고쳐 개정 사슬을 만든다.
func amendTwice(t *testing.T, st *Store, sess string) {
	t.Helper()
	ctx := context.Background()
	if err := st.Tx(ctx, func(tx *Tx) error {
		_, e := tx.AmendItem("p1", "i1", AmendPatch{
			Title: strp("두 번째 제목"), Reason: "제목 오타",
		}, sess)
		return e
	}); err != nil {
		t.Fatalf("첫 개정 실패: %v", err)
	}
	if err := st.Tx(ctx, func(tx *Tx) error {
		_, e := tx.AmendItem("p1", "i1", AmendPatch{
			Body: strp("두 번째 본문"), Paths: pathsp([]string{"internal/", "cmd/"}),
			Reason: "리네임 추종",
		}, sess)
		return e
	}); err != nil {
		t.Fatalf("둘째 개정 실패: %v", err)
	}
}

// TestItemRevisionsAreOrderedByRevAndCarryTheReason 은 되읽기의 뼈대를 못박는다:
// **rev 오름차순**이고 각 행이 사유를 그대로 나른다.
//
// 사유가 이 표의 존재 이유다(migrations/016 의 CHECK(reason <> ”)) — 순서와 사유
// 둘 중 하나라도 없으면 되짚는 사람이 "무엇을 왜 고쳤나"를 못 잇는다.
func TestItemRevisionsAreOrderedByRevAndCarryTheReason(t *testing.T) {
	st, ctx, sess := amendFixture(t)
	amendTwice(t, st, sess)

	revs, err := st.ItemRevisions(ctx, "p1", "i1")
	if err != nil {
		t.Fatalf("개정 이력을 못 읽었다: %v", err)
	}
	if len(revs) != 2 {
		t.Fatalf("개정이 %d건이다 — 2건이어야 한다: %+v", len(revs), revs)
	}
	if revs[0].Rev != 1 || revs[1].Rev != 2 {
		t.Fatalf("rev 순서가 %d·%d 다 — 1·2(오름차순)여야 한다", revs[0].Rev, revs[1].Rev)
	}
	if revs[0].Reason != "제목 오타" || revs[1].Reason != "리네임 추종" {
		t.Fatalf("사유가 %q·%q 다 — 쌓을 때 준 값 그대로여야 한다", revs[0].Reason, revs[1].Reason)
	}
	// ★ 행이 담는 것은 **고치기 직전의 값**이다. rev 1 은 amendFixture 의 원래 제목을,
	//   rev 2 는 첫 개정이 넣은 제목을 담아야 한다.
	if revs[0].Title != "원래 제목" {
		t.Errorf("rev 1 의 제목이 %q다 — 고치기 직전의 값(원래 제목)이어야 한다", revs[0].Title)
	}
	if revs[1].Title != "두 번째 제목" {
		t.Errorf("rev 2 의 제목이 %q다 — 첫 개정이 넣은 값이어야 한다", revs[1].Title)
	}
	if revs[0].SessionID != sess {
		t.Errorf("rev 1 의 세션이 %q다 — %q 여야 한다", revs[0].SessionID, sess)
	}
	if revs[0].At.IsZero() {
		t.Error("rev 1 의 시각이 0값이다 — 언제 고쳤나가 사라졌다")
	}
}

// TestMarkItemRevisionChangesReconstructsChangedAxes 는 "무엇이 바뀌었나"를 사슬로
// 복원하는 축을 잰다.
//
// ★ 이 값은 표에 없다 — item_revision 은 옛 값만 담는다. 그래서 rev N 의 다음 상태는
// rev N+1 이 담은 옛 값이고, 마지막 rev 의 다음 상태는 **지금 값**이다. 이 복원이
// 틀리면 화면이 "제목을 고쳤다"를 "본문을 고쳤다"로 말한다.
func TestMarkItemRevisionChangesReconstructsChangedAxes(t *testing.T) {
	st, ctx, sess := amendFixture(t)
	amendTwice(t, st, sess)

	revs, err := st.ItemRevisions(ctx, "p1", "i1")
	if err != nil {
		t.Fatalf("개정 이력을 못 읽었다: %v", err)
	}
	cur, err := st.GetItem(ctx, "p1", "i1")
	if err != nil {
		t.Fatalf("지금 값을 못 읽었다: %v", err)
	}
	MarkItemRevisionChanges(revs, cur)

	// rev 1: 원래 제목 → 두 번째 제목. title 하나만이다.
	if got := joinAxes(revs[0].Changed); got != "title" {
		t.Errorf("rev 1 이 바꾼 축이 %q다 — title 하나여야 한다(제목만 고쳤다)", got)
	}
	// rev 2: 본문과 경로를 함께 고쳤다.
	if got := joinAxes(revs[1].Changed); got != "body·paths" {
		t.Errorf("rev 2 가 바꾼 축이 %q다 — body·paths 여야 한다", got)
	}
}

// TestItemRevisionsOnUntouchedItemIsEmptyNotError 는 **0 과 오류를 가른다.**
//
// 한 번도 안 고친 항목은 정상이다. 여기서 오류를 내면 호출부(service.ShowItem)가
// 그것을 파생 실패로 고백하게 되고, 화면은 "개정 이력을 못 읽었다"는 거짓을 낸다 —
// 0 과 못 잼을 가르려고 만든 축이 그 자리에서 뒤집힌다.
func TestItemRevisionsOnUntouchedItemIsEmptyNotError(t *testing.T) {
	st, ctx, _ := amendFixture(t)

	revs, err := st.ItemRevisions(ctx, "p1", "i1")
	if err != nil {
		t.Fatalf("안 고친 항목의 개정 이력이 오류다 — 0건은 정상이다: %v", err)
	}
	if len(revs) != 0 {
		t.Fatalf("개정이 %d건이다 — 안 고쳤으니 0건이어야 한다: %+v", len(revs), revs)
	}
}

// joinAxes 는 축 목록을 한 문자열로 만든다(시험 단정용).
func joinAxes(axes []string) string { return strings.Join(axes, "·") }
