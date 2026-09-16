package store

import (
	"fmt"

	"github.com/kweiza/flightdeck/internal/model"
)

// ─────────────────────────────────────────────────────────────────────────────
// 항목 본문을 고치는 **유일한 자리**
// ─────────────────────────────────────────────────────────────────────────────
//
// ★ 이 파일 밖에서 `UPDATE item SET title|body|paths` 를 치면 관문이 빨개진다
// (`item_body_single_writer_test.go`). 그 관문이 함께 재는 것이 하나 더 있다:
// **이 UPDATE 와 `INSERT INTO item_revision` 이 같은 함수 안에 있어야 한다.**
// 이력 없는 수정 경로가 조용히 생기는 것이 item_revision 의 값을 통째로 무효로
// 만드는 유일한 길이다.
//
// ★ 원장도 **여기서** 남긴다(item.amend). before 를 아는 것은 같은 트랜잭션 안에서
// 읽은 쪽뿐이고, API 로 올려 보내면 원장의 정확성이 응답 왕복에 의존하게 된다 —
// SetLabels·RemoveAfter 가 같은 이유로 같은 자리에 있다.

// AmendPatch 는 고칠 축이다. **nil 은 "안 건드린다"** 이고, 빈 값을 가리키는
// 포인터는 "빈 값으로 바꿔라"다. 둘을 가르려고 포인터로 받는다.
type AmendPatch struct {
	Title  *string
	Body   *string
	Paths  *[]string
	Reason string
}

// AmendRecord 는 고친 결과다.
//
// Changed 는 **요청한 것이 아니라 실제로 값이 달라진 축**이다. 같은 값 재지정은
// 거절하지 않지만, 그때 화면이 "고쳤다"고만 말하면 사람은 안 바뀐 것을 바뀐 줄 안다
// (LabelResult 의 Added·Removed 와 같은 규율).
type AmendRecord struct {
	Rev     int
	Before  model.Item
	After   model.Item
	Changed []string
}

// AmendItem 은 항목의 title·body·paths 를 제자리에서 고치고 옛 값을 개정 이력에 쌓는다.
//
// ★ **종료 상태를 안 본다.** label 은 끝난 항목을 거절하지만(꼬리표가 뜻을 갖는 굶김
// 축이 열린 항목만 본다) 본문은 다르다 — 닫힌 항목의 틀린 본문은 note 로도 못 닿고,
// 그 도달 불가가 이 표면을 연 근거 중 하나다(설계 §2 ⓒ).
func (t *Tx) AmendItem(project, itemID string, p AmendPatch, sessionID string) (AmendRecord, error) {
	var out AmendRecord

	before, err := t.GetItem(project, itemID)
	if err != nil {
		return out, err
	}
	out.Before = before

	after := before
	if p.Title != nil {
		after.Title = *p.Title
	}
	if p.Body != nil {
		after.Body = *p.Body
	}
	if p.Paths != nil {
		after.Paths = *p.Paths
	}
	out.After = after
	out.Changed = amendChanged(before, after)

	// ★ 개정 행을 **UPDATE 앞에** 넣는다. 순서가 뒤집히면 옛 값을 읽을 자리가 이미
	//   사라져 있다 — before 를 변수에 들고 있더라도, 실패 시 어느 쪽이 남는지가
	//   순서로 결정된다. 같은 트랜잭션이라 둘 다 커밋되거나 둘 다 안 된다.
	rev, err := t.nextItemRev(project, itemID)
	if err != nil {
		return out, err
	}
	out.Rev = rev

	beforePathsJSON, err := marshalStrings(before.Paths)
	if err != nil {
		return out, fmt.Errorf("개정 이력 paths 직렬화 실패(id=%q): %w", clip(itemID, 64), err)
	}
	// 세션을 못 얻어도 개정은 남긴다 — 빈 문자열을 그대로 넣으면 session(id) FK 가
	// "" 라는 없는 키를 찾다 실패하고, 그 실패는 "값을 안 준 것"과 구분되지 않는다
	// (nullStr 이 도처에서 같은 일을 한다).
	if _, err := t.tx.ExecContext(t.ctx,
		`INSERT INTO item_revision(project, item_id, rev, at, session_id, title, body, paths, reason)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		project, itemID, rev, fmtTime(nowStamp()), nullStr(sessionID),
		before.Title, before.Body, beforePathsJSON, p.Reason); err != nil {
		return out, fmt.Errorf("개정 이력 적재 실패(project=%q id=%q rev=%d): %w",
			clip(project, 64), clip(itemID, 64), rev, err)
	}

	afterPathsJSON, err := marshalStrings(after.Paths)
	if err != nil {
		return out, fmt.Errorf("항목 paths 직렬화 실패(id=%q): %w", clip(itemID, 64), err)
	}
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE item SET title = ?, body = ?, paths = ? WHERE project = ? AND id = ?`,
		after.Title, after.Body, afterPathsJSON, project, itemID)
	if err != nil {
		return out, fmt.Errorf("항목 본문 갱신 실패(project=%q id=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	if err := affectedOne(res, NFItem, project, itemID); err != nil {
		return out, err
	}

	t.LogEvent("item.amend", project, sessionID, map[string]any{
		"item":    clip(itemID, 100),
		"rev":     rev,
		"changed": out.Changed,
		"reason":  clip(p.Reason, 200),
	})
	return out, nil
}

// nextItemRev 는 이 항목의 다음 개정 번호다.
//
// 트랜잭션 안에서 뽑는다 — `_txlock=immediate` 가 두 세션의 동시 amend 사이를 닫는다.
// 그래도 새면 PK 가 제약 위반으로 터진다(조용한 덮어쓰기가 아니다).
func (t *Tx) nextItemRev(project, itemID string) (int, error) {
	var n int
	if err := t.tx.QueryRowContext(t.ctx,
		`SELECT COALESCE(MAX(rev), 0) FROM item_revision WHERE project = ? AND item_id = ?`,
		project, itemID).Scan(&n); err != nil {
		return 0, fmt.Errorf("개정 번호 조회 실패(project=%q id=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	return n + 1, nil
}

// amendChanged 는 실제로 값이 달라진 축이다. 순수 함수이고 순서는 title·body·paths 로 고정한다.
func amendChanged(before, after model.Item) []string {
	out := make([]string, 0, 3)
	if before.Title != after.Title {
		out = append(out, "title")
	}
	if before.Body != after.Body {
		out = append(out, "body")
	}
	if !sameStrings(before.Paths, after.Paths) {
		out = append(out, "paths")
	}
	return out
}

// sameStrings 는 두 문자열 슬라이스가 **순서까지** 같은지 본다.
//
// 집합이 아니라 순서를 보는 이유: paths 는 선언이라 순서가 사람이 쓴 그대로 남고,
// 순서만 바꾼 수정도 화면에서는 바뀐 것으로 보이는 편이 옳다.
//
// 같은 몸통이 landing_resource_test.go 에 `equalStrings` 로 있었다. 그쪽은 시험 파일이라
// 운영 코드가 못 부르므로 이쪽으로 옮기고 그쪽 호출을 이 이름으로 바꿨다 — 같은 판정을
// 두 벌 두면 반드시 표류한다.
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
