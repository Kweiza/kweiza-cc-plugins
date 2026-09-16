package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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
	var gotTitle, gotReason, gotSession, gotAt string
	row := st.db.QueryRowContext(ctx,
		`SELECT title, reason, COALESCE(session_id, ''), at
		   FROM item_revision WHERE project=? AND item_id=? AND rev=1`, "p1", "i1")
	if err := row.Scan(&gotTitle, &gotReason, &gotSession, &gotAt); err != nil {
		t.Fatalf("개정 행을 못 읽었다: %v", err)
	}
	if gotTitle != "원래 제목" {
		t.Errorf("개정 행의 제목이 %q다 — **고치기 직전의 값**(\"원래 제목\")이어야 한다.\n"+
			"새 값을 쌓으면 현재가 두 곳에 생기고 둘이 갈리는 날 아무도 못 본다", gotTitle)
	}
	if gotReason != "오타" {
		t.Errorf("사유가 %q다 — 요청한 것이어야 한다", gotReason)
	}

	// ★ **누가 언제 고쳤나도 잰다.** 안 재면 session_id 를 nil 로 박아도 위 단정들이
	// 전부 초록이다 — 실재하는 세션을 여는 픽스처의 비용만 치르고 그 값을 아무도 안 본다.
	// "복구 경로가 0이 아니다"는 무엇이 남았나뿐 아니라 **누구의 수정인가**에도 걸린다.
	if gotSession != sess {
		t.Errorf("개정 행의 session_id 가 %q다 — 고친 세션 %q 여야 한다", gotSession, sess)
	}
	if _, perr := time.Parse(timeLayout, gotAt); perr != nil {
		t.Errorf("개정 행의 at 이 %q라 저장 표기(timeLayout)로 안 읽힌다: %v — "+
			"폭이 흔들리면 사전순 정렬이 시간순과 어긋난다(store.go 의 timeLayout 주석)", gotAt, perr)
	}
}

// TestAmendItemWritesLedger 는 원장이 **store 계층에서** 남는지, 그리고 페이로드 키가
// 전부 실렸는지 본다.
//
// ★ 페이로드를 푼다. Kind 와 SessionID 만 보면 LogEvent 에서 changed 나 reason 이 사라져도
// 이 시험이 조용히 통과한다 — TestSetLabelsWritesLedgerWithBeforeAndAfter 가 같은 이유로
// 같은 일을 한다. 원장을 여기서 남기는 이유도 그쪽과 같다: before 를 아는 것은 같은
// 트랜잭션 안에서 읽은 쪽뿐이라, API 로 올려 보내면 원장의 정확성이 응답 왕복에 의존한다.
func TestAmendItemWritesLedger(t *testing.T) {
	st, ctx, sess := amendFixture(t)

	err := st.Tx(ctx, func(tx *Tx) error {
		_, e := tx.AmendItem("p1", "i1", AmendPatch{
			Title:  strp("고친 제목"),
			Paths:  pathsp([]string{"web/"}),
			Reason: "리네임 추종",
		}, sess)
		return e
	})
	if err != nil {
		t.Fatalf("고치지 못했다: %v", err)
	}

	evs, err := st.ListEvents(ctx, "item.amend", time.Time{}, 20)
	if err != nil {
		t.Fatalf("원장 조회 실패: %v", err)
	}
	var found bool
	for _, e := range evs {
		if e.Kind != "item.amend" {
			continue
		}
		found = true
		if e.SessionID != sess {
			t.Errorf("이벤트의 세션이 %q다 — %q 여야 한다", e.SessionID, sess)
		}
		var payload struct {
			Item    string   `json:"item"`
			Rev     int      `json:"rev"`
			Changed []string `json:"changed"`
			Reason  string   `json:"reason"`
		}
		if uerr := json.Unmarshal([]byte(e.Payload), &payload); uerr != nil {
			t.Fatalf("이벤트 페이로드를 못 읽었다: %v (원문 %q)", uerr, e.Payload)
		}
		if payload.Item != "i1" {
			t.Errorf("원장의 item 이 %q다 — i1 이어야 한다", payload.Item)
		}
		if payload.Rev != 1 {
			t.Errorf("원장의 rev 가 %d다 — 1이어야 한다", payload.Rev)
		}
		if got := strings.Join(payload.Changed, ","); got != "title,paths" {
			t.Errorf("원장의 changed 가 %q다 — title,paths 여야 한다", got)
		}
		if payload.Reason != "리네임 추종" {
			t.Errorf("원장의 reason 이 %q다 — 요청한 것이어야 한다", payload.Reason)
		}
	}
	if !found {
		t.Error("원장에 item.amend 가 없다 — 이 쓰기는 되돌리는 코드가 없고 " +
			"무엇이 있었는지가 바꾸는 순간 사라진다. 그 흔적이 통째로 빈다")
	}
}

// TestAmendItemWithUnknownSessionIsMissingRef 는 개정 이력의 FK 위반이 **타입 있는
// 오류**로 접히는지 본다.
//
// ★ 이 단정이 없으면 writeErr 를 맨 fmt.Errorf 로 되돌려도 아무것도 안 빨개진다.
// 접히지 않으면 표면이 500 을 내는데, 등록 안 된 세션 id 는 호출자가 고칠 거리이고
// 500 은 멱등 표에 안 남아 재시도가 계속 하류로 들어간다(constraint.go 머리말).
// 선점에 대해 TestClaimWithUnknownSessionIsMissingRef 가 이미 못박은 형태다.
func TestAmendItemWithUnknownSessionIsMissingRef(t *testing.T) {
	st, ctx, _ := amendFixture(t)

	err := st.Tx(ctx, func(tx *Tx) error {
		_, e := tx.AmendItem("p1", "i1", AmendPatch{Title: strp("x"), Reason: "r"}, "없는세션")
		return e
	})
	if err == nil {
		t.Fatal("없는 세션으로 고치는 것이 성공했다 — item_revision.session_id FK 가 안 물고 있다")
	}
	var c *ConflictError
	if !errors.As(err, &c) {
		t.Fatalf("FK 위반이 타입 있는 오류로 안 올라왔다: %T %v", err, err)
	}
	if c.Kind != ConflictMissingRef {
		t.Errorf("Kind 가 %q다 — %q 여야 한다(사유: %s)", c.Kind, ConflictMissingRef, c.Reason)
	}
	if c.Target != TargetItem {
		t.Errorf("Target 이 %q다 — %q 여야 한다", c.Target, TargetItem)
	}
	if !strings.Contains(c.RefHint, "없는세션") {
		t.Errorf("무엇을 가리켰는지가 안 실렸다: %q", c.RefHint)
	}
}

// TestAmendItemRefusesEmptyReason 은 사유의 **1차 방어**가 store 에 있는지 본다.
//
// ★ 공백만 든 사유가 핵심 갈래다. 그것은 스키마의 CHECK(reason <> ”) 를 **통과해**
// 그대로 저장된다 — 되짚을 수 없는 개정이 추가 전용 표에 남는데, 사유를 남기는 것이
// 이 표의 존재 이유다. 빈 문자열 쪽은 CHECK 가 잡기는 하지만 JudgeConstraintCode 가
// CHECK 를 일부러 안 접어 500(서버 결함)으로 나간다 — 호출자가 고칠 거리인데 등급이 틀린다.
// AddJudgment 가 빈 판단 본문에 대해 같은 자리에서 같은 일을 한다.
func TestAmendItemRefusesEmptyReason(t *testing.T) {
	for _, reason := range []string{"", "   ", "\t\n"} {
		st, ctx, sess := amendFixture(t)
		err := st.Tx(ctx, func(tx *Tx) error {
			_, e := tx.AmendItem("p1", "i1", AmendPatch{Title: strp("x"), Reason: reason}, sess)
			return e
		})
		if err == nil {
			t.Errorf("사유 %q 로 고치는 것이 성공했다 — 되짚을 수 없는 개정이 원장에 남는다", reason)
		}

		// 거절이면 아무것도 안 남아야 한다. 개정 행만 남거나 항목만 바뀌면 그게 더 나쁘다.
		var n int
		if qerr := st.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM item_revision WHERE project=? AND item_id=?`, "p1", "i1").Scan(&n); qerr != nil {
			t.Fatalf("개정 행 수를 못 셌다: %v", qerr)
		}
		if n != 0 {
			t.Errorf("사유 %q 가 거절됐는데 개정 행이 %d개 남았다", reason, n)
		}
		it, gerr := st.GetItem(ctx, "p1", "i1")
		if gerr != nil {
			t.Fatalf("되읽기 실패: %v", gerr)
		}
		if it.Title != "원래 제목" {
			t.Errorf("사유 %q 가 거절됐는데 제목이 %q로 바뀌었다", reason, it.Title)
		}
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

	// ★ 그래도 **개정 행은 남고 rev 는 오른다.** 추가 전용 표에 대한 결정이라 나중에
	// 바꾸기 어렵다 — 어느 쪽으로도 안 못박아 두면 다음 사람이 "변화 0이면 건너뛰자"를
	// 최적화로 넣고, 그 순간 "누가 언제 무엇을 시도했나"가 원장에서 사라진다.
	// 고쳤다고 말하지 않는 것(Changed 가 빈 것)과 시도를 안 남기는 것은 다른 일이다.
	if rec.Rev != 1 {
		t.Errorf("rev 가 %d다 — 변화가 없어도 개정은 쌓인다(1이어야 한다)", rec.Rev)
	}
	var n int
	if qerr := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM item_revision WHERE project=? AND item_id=? AND rev=1`,
		"p1", "i1").Scan(&n); qerr != nil {
		t.Fatalf("개정 행 수를 못 셌다: %v", qerr)
	}
	if n != 1 {
		t.Errorf("개정 행이 %d개다 — 같은 값 재지정도 1개를 남긴다", n)
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
	// ★ 센티넬로 잰다. 문구에 id 가 들었는지만 보면 아무 fmt.Errorf 나 통과하고,
	// 그러면 표면이 404 로 접을 근거가 사라진 것을 이 시험이 못 본다
	// (labels_test.go 의 TestSetLabelsRefusesMissingItem 과 같은 판정).
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("오류가 %v다 — ErrNotFound 여야 한다", err)
	}
	if !strings.Contains(err.Error(), "없는거") {
		t.Errorf("거절에 항목 id 가 없다: %v", err)
	}
}
