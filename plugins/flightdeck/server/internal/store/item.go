package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// Q 계층 — 항목·의존·역인덱스·선점.

// ─────────────────────────────────────────────────────────────────────────────
// 선점 판정 — 순수 함수
// ─────────────────────────────────────────────────────────────────────────────

// ClaimVerdict 는 선점 요청의 판정이다.
//
// 불리언이 아니라 **사유**를 담는다. 사유가 없으면 "이미 남이 잡았다"와 "항목이 이미 끝났다"와
// "이 축을 아예 안 본다"가 전부 같은 false 로 접힌다.
// Reason 은 성공일 때도 채운다 — 왜 통과했는지가 없으면 통과가 검증 불가가 된다.
type ClaimVerdict struct {
	OK     bool
	Resume bool   // 이미 자기 것이다. 새로 잡는 것이 아니라 맥락 재출력 경로다
	Reason string // 항상 채운다
}

// JudgeClaim 은 선점 가능 여부를 판정한다. 순수 함수다.
//
//   - itemFound:  항목이 존재하는가
//   - itemState:  그 항목의 상태
//   - holder:     현재 점유자의 session_id. 빈 문자열이면 미선점
//   - requester:  잡으려는 세션
//
// ★ holder 를 사유에 담는 것이 이 함수의 핵심이다. "실패"만 돌려주면
// 호출자는 누구에게 물어야 하는지 모르고, 그러면 결국 추측으로 남의 작업을 집는다.
func JudgeClaim(itemFound bool, itemState model.ItemState, holder, requester string) ClaimVerdict {
	switch {
	case requester == "":
		return ClaimVerdict{Reason: "선점 요청자 session_id 가 비었다"}

	case !itemFound:
		return ClaimVerdict{Reason: "그런 항목이 없다"}

	// ★ 종료 상태를 점유자 축보다 **먼저** 본다.
	//
	// 뒤에 두면 끝난 항목에 살아 있는 자기 선점이 붙어 있을 때 "재개해라"가 나간다 —
	// 실제 사실은 "이 항목은 끝났다"인데 정반대의 처방을 낸다.
	// 지금은 SetItemState 가 종료와 함께 선점을 반납하므로 그 상태가 잘 안 생기지만,
	// 그 반납이 회귀해도 이 순서가 오답을 막는다(방어는 두 겹이어야 한다).
	case itemState == model.ItemDone:
		return ClaimVerdict{Reason: "이미 끝난 항목이다"}

	case itemState == model.ItemDropped:
		return ClaimVerdict{Reason: "폐기된 항목이다"}

	case holder != "" && holder == requester:
		// 재개 경로. 컨텍스트가 날아가 같은 워크트리로 돌아온 세션이 여기 온다 —
		// 거절하면 핸드오프가 가장 절실한 순간에 맥락을 되찾을 길이 없어진다.
		return ClaimVerdict{OK: true, Resume: true,
			Reason: "이미 이 세션의 선점이다 — 맥락을 다시 낸다"}

	case holder != "":
		return ClaimVerdict{Reason: fmt.Sprintf("세션 %s 가 이미 선점하고 있다", holder)}

	case itemState == model.ItemClaimed:
		// 미선점인데 상태가 claimed = 반납이 상태를 못 되돌린 것. 조용히 통과시키면
		// 그 불일치가 영영 안 보인다. 잡게는 해 주되 사유에 남긴다.
		return ClaimVerdict{OK: true,
			Reason: "선점 가능하다(항목 상태가 claimed 인데 점유자가 없다 — 앞선 반납이 상태를 못 되돌린 흔적이다)"}

	default:
		return ClaimVerdict{OK: true, Reason: "선점 가능하다"}
	}
}

// ClaimHeldError 는 남이 잡고 있는 항목을 잡으려 했을 때의 오류다.
// **점유자를 담는다** — 불리언 실패로 접으면 누구에게 물어야 하는지 알 수 없다.
type ClaimHeldError struct {
	Project string
	ItemID  string
	Holder  string // 점유 중인 session_id
	At      time.Time
	Reason  string // JudgeClaim 이 낸 사유 전문
}

func (e *ClaimHeldError) Error() string {
	return fmt.Sprintf("항목 %s/%s 선점 거절: %s (점유 시각 %s)",
		e.Project, e.ItemID, e.Reason, e.At.Format(time.RFC3339))
}

// ClaimRefusedError 는 점유자 때문이 아닌 사유로 선점이 거절된 것이다
// (없는 항목·끝난 항목·폐기된 항목). Holder 축과 섞지 않는다 — 처방이 다르다.
type ClaimRefusedError struct {
	Project string
	ItemID  string
	Reason  string
}

func (e *ClaimRefusedError) Error() string {
	return fmt.Sprintf("항목 %s/%s 선점 거절: %s", e.Project, e.ItemID, e.Reason)
}

// ─────────────────────────────────────────────────────────────────────────────
// item
// ─────────────────────────────────────────────────────────────────────────────

// AddItem 은 항목을 만들고 선행 조건과 역인덱스를 함께 유지한다.
//
// item_dependents 는 "나에게 기대는 항목이 몇이나 되나"의 역인덱스다.
// 기존 도구는 이 질문에 답하려고 적격 항목마다 큐 전체를 grep 해 첫 명령이 51.7초 걸렸다.
func (t *Tx) AddItem(it model.Item) error {
	if it.Project == "" || it.ID == "" {
		return fmt.Errorf("항목의 project 와 id 는 필수다(project=%q id=%q)",
			clip(it.Project, 64), clip(it.ID, 64))
	}
	if it.State == "" {
		it.State = model.ItemOpen
	}
	if it.CreatedAt.IsZero() {
		it.CreatedAt = nowStamp()
	}
	pathsJSON, err := marshalStrings(it.Paths)
	if err != nil {
		return fmt.Errorf("항목 paths 직렬화 실패(id=%q): %w", clip(it.ID, 64), err)
	}
	labelsJSON, err := marshalStrings(it.Labels)
	if err != nil {
		return fmt.Errorf("항목 labels 직렬화 실패(id=%q): %w", clip(it.ID, 64), err)
	}

	if _, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO item(project, id, title, body, paths, labels, state, close_reason, landed_ref, created_at, closed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		it.Project, it.ID, it.Title, it.Body, pathsJSON, labelsJSON,
		string(it.State), nullStr(it.CloseReason), nullStr(it.LandedRef),
		fmtTime(it.CreatedAt), nullTimePtr(it.ClosedAt)); err != nil {
		// 중복 id 는 오타·재등록에서 흔한 **정상적인 거절**이다 — 타입 있는 오류로 올려야
		// 표면이 409 로 접고 처방을 낼 수 있다. 그대로 올리면 500 이 되고,
		// 500 은 멱등 표에 안 남아 재시도가 계속 하류로 들어간다.
		return writeErr(err, writeTarget{
			Target: TargetItem, Project: it.Project, ID: it.ID,
			RefHint: "프로젝트 " + clip(it.Project, 64),
		}, "항목 등록 실패(project=%q id=%q)", clip(it.Project, 64), clip(it.ID, 64))
	}

	for i, a := range it.After {
		if err := t.addAfter(it.Project, it.ID, a); err != nil {
			return fmt.Errorf("항목 %s/%s 의 %d번째 선행 조건: %w",
				clip(it.Project, 64), clip(it.ID, 64), i, err)
		}
	}
	return nil
}

// AddItem 은 단발 트랜잭션으로 감싼 것이다.
func (s *Store) AddItem(ctx context.Context, it model.Item) error {
	return s.Tx(ctx, func(t *Tx) error { return t.AddItem(it) })
}

// ValidateAfter 는 선행 조건이 정확히 한 축만 채웠는지 본다.
//
// 스키마 CHECK 가 최후 방어이지 1차 방어가 아니다. 그리고 **브랜치 이름을 담을 자리가 없다** —
// 랜딩이 끝나면 규율대로 브랜치가 삭제돼 조건이 충족되는 바로 그 순간 판정이 해석 불가가 되기 때문이다.
func ValidateAfter(a model.After) error {
	n := 0
	for _, v := range []string{a.Item, a.Job, a.SHA} {
		if v != "" {
			n++
		}
	}
	switch n {
	case 1:
		return nil
	case 0:
		return errors.New("선행 조건이 비었다 — item·job·sha 중 정확히 하나를 채워야 한다")
	default:
		return fmt.Errorf("선행 조건에 %d개 축이 채워졌다(item=%q job=%q sha=%q) — 정확히 하나여야 한다",
			n, clip(a.Item, 64), clip(a.Job, 64), clip(a.SHA, 40))
	}
}

func (t *Tx) addAfter(project, itemID string, a model.After) error {
	if err := ValidateAfter(a); err != nil {
		return err
	}
	if _, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO item_after(project, item_id, dep_item, dep_job, dep_sha) VALUES (?, ?, ?, ?, ?)`,
		project, itemID, nullStr(a.Item), nullStr(a.Job), nullStr(a.SHA)); err != nil {
		return writeErr(err, writeTarget{
			Target: TargetItemAfter, Project: project, ID: itemID,
			RefHint: fmt.Sprintf("항목 %s/%s", clip(project, 64), clip(itemID, 64)),
		}, "선행 조건 등록 실패(project=%q item=%q)", clip(project, 64), clip(itemID, 64))
	}
	if a.Item != "" {
		if err := t.bumpDependents(project, a.Item, +1); err != nil {
			return err
		}
	}
	return nil
}

// AddAfter 는 이미 있는 항목에 선행 조건을 추가한다.
func (t *Tx) AddAfter(project, itemID string, a model.After) error {
	return t.addAfter(project, itemID, a)
}

// bumpDependents 는 역인덱스를 delta 만큼 움직인다.
// 감소는 0 아래로 안 내려간다 — 음수 카운트는 조용히 "의존 없음"으로 읽혀 잘못된 통과를 만든다.
func (t *Tx) bumpDependents(project, depItem string, delta int) error {
	if delta >= 0 {
		_, err := t.tx.ExecContext(t.ctx, `
			INSERT INTO item_dependents(project, item_id, n) VALUES (?, ?, ?)
			ON CONFLICT(project, item_id) DO UPDATE SET n = n + ?`,
			project, depItem, delta, delta)
		if err != nil {
			return fmt.Errorf("역인덱스 증가 실패(project=%q dep=%q): %w",
				clip(project, 64), clip(depItem, 64), err)
		}
		return nil
	}
	_, err := t.tx.ExecContext(t.ctx, `
		UPDATE item_dependents SET n = MAX(0, n + ?) WHERE project = ? AND item_id = ?`,
		delta, project, depItem)
	if err != nil {
		return fmt.Errorf("역인덱스 감소 실패(project=%q dep=%q): %w",
			clip(project, 64), clip(depItem, 64), err)
	}
	return nil
}

// Dependents 는 이 항목에 기대는 항목 수다. 없으면 0.
func (s *Store) Dependents(ctx context.Context, project, itemID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT n FROM item_dependents WHERE project = ? AND item_id = ?`, project, itemID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil // 행이 없다 = 기대는 항목이 0건. 부재와 0이 같은 뜻인 유일한 자리다
	}
	if err != nil {
		return 0, fmt.Errorf("역인덱스 조회 실패(project=%q item=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	return n, nil
}

// DeleteItem 은 항목을 지우고 역인덱스를 되돌린다.
//
// 일반 경로는 이걸 안 쓴다 — 끝난 항목은 state=done, 버린 항목은 state=dropped 다(사유 필수).
// 이 함수는 잘못 넣은 항목을 되무르는 자리이고, 그때 역인덱스가 안 줄면
// 선행 항목이 영영 "누가 기대고 있다"로 남는다.
func (t *Tx) DeleteItem(project, itemID string) error {
	afters, err := afterOf(t.ctx, t.tx, project, itemID)
	if err != nil {
		return err
	}
	res, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM item WHERE project = ? AND id = ?`, project, itemID)
	if err != nil {
		return fmt.Errorf("항목 삭제 실패(project=%q id=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("항목 삭제 결과 확인 실패(project=%q id=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	if n == 0 {
		return fmt.Errorf("항목 %s/%s 가 %w", clip(project, 64), clip(itemID, 64), ErrNotFound)
	}
	// item_after 는 FK ON DELETE CASCADE 로 함께 사라진다. 역인덱스는 FK 가 없으므로 손으로 되돌린다.
	for _, a := range afters {
		if a.Item == "" {
			continue
		}
		if err := t.bumpDependents(project, a.Item, -1); err != nil {
			return err
		}
	}
	// 자기 자신을 가리키던 역인덱스 행도 지운다(이제 존재하지 않는 항목이다).
	if _, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM item_dependents WHERE project = ? AND item_id = ?`, project, itemID); err != nil {
		return fmt.Errorf("역인덱스 정리 실패(project=%q id=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	return nil
}

// DeleteItem 은 단발 트랜잭션으로 감싼 것이다.
func (s *Store) DeleteItem(ctx context.Context, project, itemID string) error {
	return s.Tx(ctx, func(t *Tx) error { return t.DeleteItem(project, itemID) })
}

const itemCols = `project, id, title, body, paths, labels, state, close_reason, landed_ref, created_at, closed_at`

func scanItem(sc interface{ Scan(...any) error }) (model.Item, error) {
	var it model.Item
	var pathsRaw, labelsRaw, state, created string
	var closeReason, landed, closed sql.NullString
	if err := sc.Scan(&it.Project, &it.ID, &it.Title, &it.Body, &pathsRaw, &labelsRaw,
		&state, &closeReason, &landed, &created, &closed); err != nil {
		return it, err
	}
	it.State = model.ItemState(state)
	it.CloseReason, it.LandedRef = str(closeReason), str(landed)
	if err := json.Unmarshal([]byte(pathsRaw), &it.Paths); err != nil {
		return it, fmt.Errorf("항목 paths 해석 실패(id=%q): %w", clip(it.ID, 64), err)
	}
	if err := json.Unmarshal([]byte(labelsRaw), &it.Labels); err != nil {
		return it, fmt.Errorf("항목 labels 해석 실패(id=%q): %w", clip(it.ID, 64), err)
	}
	var err error
	if it.CreatedAt, err = parseTime(created); err != nil {
		return it, err
	}
	if it.ClosedAt, err = parseNullTime(closed); err != nil {
		return it, err
	}
	return it, nil
}

func afterOf(ctx context.Context, q dbtx, project, itemID string) ([]model.After, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT dep_item, dep_job, dep_sha FROM item_after WHERE project = ? AND item_id = ?`,
		project, itemID)
	if err != nil {
		return nil, fmt.Errorf("선행 조건 조회 실패(project=%q item=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	defer rows.Close()

	var out []model.After
	for rows.Next() {
		var di, dj, ds sql.NullString
		if err := rows.Scan(&di, &dj, &ds); err != nil {
			return nil, fmt.Errorf("선행 조건 행 해석 실패: %w", err)
		}
		out = append(out, model.After{Item: str(di), Job: str(dj), SHA: str(ds)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("선행 조건 순회 실패: %w", err)
	}
	return out, nil
}

func getItem(ctx context.Context, q dbtx, project, itemID string) (model.Item, error) {
	row := q.QueryRowContext(ctx,
		`SELECT `+itemCols+` FROM item WHERE project = ? AND id = ?`, project, itemID)
	it, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return it, fmt.Errorf("항목 %s/%s 가 %w", clip(project, 64), clip(itemID, 64), ErrNotFound)
	}
	if err != nil {
		return it, fmt.Errorf("항목 조회 실패(project=%q id=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	if it.After, err = afterOf(ctx, q, project, itemID); err != nil {
		return it, err
	}
	return it, nil
}

// GetItem 은 항목 하나를 선행 조건까지 읽는다.
func (s *Store) GetItem(ctx context.Context, project, itemID string) (model.Item, error) {
	return getItem(ctx, s.db, project, itemID)
}

// GetItem 은 트랜잭션 안에서 읽는다.
func (t *Tx) GetItem(project, itemID string) (model.Item, error) {
	return getItem(t.ctx, t.tx, project, itemID)
}

// ListOpen 은 열린 항목을 오래된 순으로 낸다.
func (s *Store) ListOpen(ctx context.Context, project string) ([]model.Item, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+itemCols+` FROM item WHERE project = ? AND state = 'open' ORDER BY created_at, id`, project)
	if err != nil {
		return nil, fmt.Errorf("열린 항목 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	defer rows.Close()

	var out []model.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("항목 행 해석 실패: %w", err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("항목 목록 순회 실패: %w", err)
	}
	// 선행 조건은 행 순회가 끝난 뒤에 채운다 — 같은 커넥션에서 rows 를 열어 둔 채
	// 다른 질의를 던지면 드라이버에 따라 막힌다.
	for i := range out {
		if out[i].After, err = afterOf(ctx, s.db, out[i].Project, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ValidateFinish 는 종료 인자가 성립하는지 본다.
// dropped 에 사유가 없으면 나중에 왜 버렸는지 되짚을 수 없다(스키마 CHECK 와 같은 규율,
// 다만 여기가 1차 방어다 — DB 제약 위반 문구는 무엇이 빠졌는지 말하지 않는다).
func ValidateFinish(outcome model.ItemState, closeReason string) error {
	switch outcome {
	case model.ItemDone:
		return nil
	case model.ItemDropped:
		if closeReason == "" {
			return errors.New("state=dropped 에는 사유가 필수다 — 사유 없는 폐기는 나중에 되짚을 수 없다")
		}
		return nil
	default:
		return fmt.Errorf("종료 상태는 done 또는 dropped 여야 한다(받은 값 %q)", clip(string(outcome), 32))
	}
}

// SetItemState 는 항목 상태를 바꾼다. 종료 상태(done/dropped)면 closed_at 을 함께 찍는다.
func (t *Tx) SetItemState(project, itemID string, state model.ItemState, closeReason string) error {
	switch state {
	case model.ItemOpen, model.ItemClaimed:
		res, err := t.tx.ExecContext(t.ctx,
			`UPDATE item SET state = ?, close_reason = NULL, closed_at = NULL WHERE project = ? AND id = ?`,
			string(state), project, itemID)
		if err != nil {
			return fmt.Errorf("항목 상태 갱신 실패(project=%q id=%q state=%q): %w",
				clip(project, 64), clip(itemID, 64), state, err)
		}
		return affectedOne(res, "항목", project, itemID)
	case model.ItemDone, model.ItemDropped:
		if err := ValidateFinish(state, closeReason); err != nil {
			return err
		}
		now := fmtTime(time.Now())
		res, err := t.tx.ExecContext(t.ctx,
			`UPDATE item SET state = ?, close_reason = ?, closed_at = ? WHERE project = ? AND id = ?`,
			string(state), nullStr(closeReason), now, project, itemID)
		if err != nil {
			return fmt.Errorf("항목 종료 실패(project=%q id=%q state=%q): %w",
				clip(project, 64), clip(itemID, 64), state, err)
		}
		if err := affectedOne(res, "항목", project, itemID); err != nil {
			return err
		}
		// ★ 종료하면 선점도 함께 반납한다.
		//
		// 앞선 판은 이 자리를 비워 뒀고, 그래서 끝난 항목의 선점이 영구히 살아남았다.
		// 증상이 셋으로 갈라졌다 — board 가 끝난 항목을 "이 세션이 쥐고 있다"로 계속 표시하고
		// (게시판의 신호 대 소음이 단조 악화하던 기존 결함의 재현이다),
		// pick 이 거절 대신 "맥락을 다시 낸다"를 실행하며,
		// Eligible 이 그 항목을 claimed-by-self 로 분류해 **탈락 사유 분포가 거짓이 된다**.
		//
		// 반납은 여기 한 자리에 둔다. FinishItem 이 먼저 반납해도 아래는 멱등이다
		// (released_at IS NULL 조건이 이미 반납된 행을 건너뛴다).
		if _, err := t.tx.ExecContext(t.ctx,
			`UPDATE claim SET released_at = ? WHERE project = ? AND item_id = ? AND released_at IS NULL`,
			now, project, itemID); err != nil {
			return fmt.Errorf("종료 시 선점 반납 실패(project=%q id=%q): %w",
				clip(project, 64), clip(itemID, 64), err)
		}
		return nil
	default:
		return fmt.Errorf("알 수 없는 항목 상태 %q", clip(string(state), 32))
	}
}

// SetItemState 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) SetItemState(ctx context.Context, project, itemID string, state model.ItemState, closeReason string) error {
	return s.Tx(ctx, func(t *Tx) error { return t.SetItemState(project, itemID, state, closeReason) })
}

// SetLandedRef 는 랜딩된 sha 를 적는다.
//
// ★ 여기 들어가는 값은 **러너가 실제로 fast-forward 한 sha** 뿐이다.
// 기존 도구는 "메인 트리의 지금 HEAD"를 적어 남의 커밋이 이 항목의 랜딩 sha 로 박혔다(3회 관측).
// 그래서 이 함수는 sha 를 인자로만 받고, 어디서도 HEAD 를 읽지 않는다.
func (t *Tx) SetLandedRef(project, itemID, sha string) error {
	if sha == "" {
		return errors.New("랜딩 sha 가 비었다 — 파생 가능한 값을 여기서 지어내지 않는다")
	}
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE item SET landed_ref = ? WHERE project = ? AND id = ?`, sha, project, itemID)
	if err != nil {
		return fmt.Errorf("랜딩 sha 기록 실패(project=%q id=%q sha=%q): %w",
			clip(project, 64), clip(itemID, 64), clip(sha, 40), err)
	}
	// 없는 항목에 조용히 성공하면 이 함수를 만든 이유가 통째로 무너진다 —
	// "남의 커밋이 박히던 결함"을 고치려다 **아무 데도 안 박히면서 성공으로 보고**하게 된다.
	return affectedOne(res, "항목", project, itemID)
}

// SetLandedRef 는 단발 트랜잭션으로 감싼 것이다.
//
// 이 래퍼가 없어서 SetItemState·FinishItem 만 Store 수준에 있고 이것만 Tx 전용이었다.
// 같은 성격의 함수 셋 중 하나만 표면이 다르면 호출부가 우회 경로를 만든다.
func (s *Store) SetLandedRef(ctx context.Context, project, itemID, sha string) error {
	return s.Tx(ctx, func(t *Tx) error { return t.SetLandedRef(project, itemID, sha) })
}

// affectedOne 은 UPDATE 가 정확히 한 행을 건드렸는지 본다.
//
// 이 헬퍼를 둔 이유는 비대칭이 결함의 신호이기 때문이다 —
// SetSessionState·ForceReleaseClaim·ForceReleaseResource 는 RowsAffected 를 보는데
// SetItemState·SetLandedRef 는 안 봐서 **없는 항목 id 에 nil(성공)을 돌려주고 있었다.**
// 항목 id 오타 하나에 도구가 성공을 보고하고 원장에는 아무것도 안 남는다.
// 가드를 넣을 때는 같은 자원을 만지는 다른 명령을 반드시 함께 훑어야 한다.
func affectedOne(res sql.Result, what, project, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s 갱신 결과 확인 실패(project=%q id=%q): %w",
			what, clip(project, 64), clip(id, 64), err)
	}
	if n == 0 {
		return fmt.Errorf("%s %q/%q 가 %w", what, clip(project, 64), clip(id, 64), ErrNotFound)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// claim — 원자적 선점
// ─────────────────────────────────────────────────────────────────────────────

// ClaimItem 은 항목을 선점한다.
//
// ★ 원자적이다. 이 트랜잭션은 BEGIN IMMEDIATE 로 열리므로(store.go 의 Tx 주석 참조)
// "미선점 확인 → 삽입" 사이에 남이 끼어들 창이 없다.
// 이미 남의 것이면 **점유자를 담은 오류**(*ClaimHeldError)를 돌려준다 —
// 불리언 실패로 접으면 누구에게 물어야 하는지 알 수 없고, 그러면 다시 추측이 시작된다.
func (t *Tx) ClaimItem(project, itemID, sessionID string) (model.Claim, error) {
	found := true
	var state model.ItemState
	switch it, err := t.GetItem(project, itemID); {
	case err == nil:
		state = it.State
	case errors.Is(err, ErrNotFound):
		found = false
	default:
		return model.Claim{}, err
	}

	cur, curErr := t.claimRow(project, itemID)
	holder := ""
	if curErr == nil && cur.ReleasedAt == nil {
		holder = cur.SessionID
	} else if curErr != nil && !errors.Is(curErr, ErrNotFound) {
		return model.Claim{}, curErr
	}

	v := JudgeClaim(found, state, holder, sessionID)
	switch {
	case v.Resume:
		return cur, nil
	case !v.OK && holder != "":
		return model.Claim{}, &ClaimHeldError{
			Project: project, ItemID: itemID, Holder: holder, At: cur.At, Reason: v.Reason}
	case !v.OK:
		return model.Claim{}, &ClaimRefusedError{Project: project, ItemID: itemID, Reason: v.Reason}
	}

	now := nowStamp()
	// PK 가 (project, item_id) 라 **반납된 선점 행이 그대로 남아 있다**.
	// 그래서 단순 INSERT 가 아니라 upsert 여야 한다 — 재선점이 PK 위반으로 죽으면
	// 한 번 반납한 항목을 아무도 다시 못 집는다.
	if _, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO claim(project, item_id, session_id, at, released_at, force_reason)
		VALUES (?, ?, ?, ?, NULL, NULL)
		ON CONFLICT(project, item_id) DO UPDATE SET
		  session_id = excluded.session_id, at = excluded.at, released_at = NULL, force_reason = NULL`,
		project, itemID, sessionID, fmtTime(now)); err != nil {
		// 등록 안 된 session_id 로 선점하면 여기서 FK 위반이 난다 — 항목 id 중복과 같은 형태다.
		return model.Claim{}, writeErr(err, writeTarget{
			Target: TargetClaim, Project: project, ID: itemID,
			RefHint: fmt.Sprintf("항목 %s/%s · 세션 %s",
				clip(project, 64), clip(itemID, 64), clip(sessionID, 64)),
		}, "선점 기록 실패(project=%q item=%q session=%q)",
			clip(project, 64), clip(itemID, 64), clip(sessionID, 64))
	}
	if err := t.SetItemState(project, itemID, model.ItemClaimed, ""); err != nil {
		return model.Claim{}, err
	}
	return model.Claim{Project: project, ItemID: itemID, SessionID: sessionID, At: now}, nil
}

// ClaimItem 은 단발 트랜잭션으로 감싼 것이다.
func (s *Store) ClaimItem(ctx context.Context, project, itemID, sessionID string) (model.Claim, error) {
	var c model.Claim
	err := s.Tx(ctx, func(t *Tx) error {
		var e error
		c, e = t.ClaimItem(project, itemID, sessionID)
		return e
	})
	return c, err
}

func (t *Tx) claimRow(project, itemID string) (model.Claim, error) {
	return claimRow(t.ctx, t.tx, project, itemID)
}

func claimRow(ctx context.Context, q dbtx, project, itemID string) (model.Claim, error) {
	var c model.Claim
	var at string
	var released, force sql.NullString
	err := q.QueryRowContext(ctx, `
		SELECT project, item_id, session_id, at, released_at, force_reason
		FROM claim WHERE project = ? AND item_id = ?`, project, itemID).
		Scan(&c.Project, &c.ItemID, &c.SessionID, &at, &released, &force)
	if errors.Is(err, sql.ErrNoRows) {
		return c, fmt.Errorf("항목 %s/%s 의 선점이 %w", clip(project, 64), clip(itemID, 64), ErrNotFound)
	}
	if err != nil {
		return c, fmt.Errorf("선점 조회 실패(project=%q item=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	c.ForceReason = str(force)
	if c.At, err = parseTime(at); err != nil {
		return c, err
	}
	if c.ReleasedAt, err = parseNullTime(released); err != nil {
		return c, err
	}
	return c, nil
}

// GetClaim 은 항목의 선점 행을 읽는다(반납된 것도 낸다 — 이력이 자산이다).
func (s *Store) GetClaim(ctx context.Context, project, itemID string) (model.Claim, error) {
	return claimRow(ctx, s.db, project, itemID)
}

// ReleaseClaim 은 자기 선점을 반납한다.
//
// **남의 선점은 반납할 수 없다.** 자동 회수 코드 경로가 존재하지 않고, 강제 회수는
// 사유를 필수로 받는 별도 함수(ForceReleaseClaim)다 — 생존 판정의 정본이 없어
// "만료 = 죽음"이 성립하지 않기 때문이다(실측으로 두 번 틀렸다).
func (t *Tx) ReleaseClaim(project, itemID, sessionID string) error {
	cur, err := t.claimRow(project, itemID)
	if err != nil {
		return err
	}
	if cur.ReleasedAt != nil {
		return nil // 이미 반납됐다. 멱등하게 통과시킨다
	}
	if cur.SessionID != sessionID {
		return &ClaimHeldError{
			Project: project, ItemID: itemID, Holder: cur.SessionID, At: cur.At,
			Reason: fmt.Sprintf("세션 %s 의 선점이라 세션 %s 가 반납할 수 없다 "+
				"— 강제 반납은 사유를 받는 ForceReleaseClaim 이다", cur.SessionID, sessionID)}
	}
	if _, err := t.tx.ExecContext(t.ctx,
		`UPDATE claim SET released_at = ? WHERE project = ? AND item_id = ?`,
		fmtTime(time.Now()), project, itemID); err != nil {
		return fmt.Errorf("선점 반납 실패(project=%q item=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	// 열린 항목으로 되돌린다. 종료된 항목이면 FinishItem 이 이미 상태를 바꿨다.
	if _, err := t.tx.ExecContext(t.ctx,
		`UPDATE item SET state = 'open' WHERE project = ? AND id = ? AND state = 'claimed'`,
		project, itemID); err != nil {
		return fmt.Errorf("항목 상태 되돌리기 실패(project=%q item=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	return nil
}

// ReleaseClaim 은 단발 트랜잭션으로 감싼 것이다.
func (s *Store) ReleaseClaim(ctx context.Context, project, itemID, sessionID string) error {
	return s.Tx(ctx, func(t *Tx) error { return t.ReleaseClaim(project, itemID, sessionID) })
}

// ForceReleaseClaim 은 남의 선점을 회수한다. **사유가 필수다.**
//
// 회수는 사람만 한다. 자동 만료가 없는 이유는 스키마 주석에 있다 —
// 죽었다고 판정한 세션이 그 뒤 6커밋을 랜딩한 실측이 있다.
// 회수 행위 자체는 judgment(kind='decision') 으로 함께 남기는 것이 상위 계층의 몫이다.
func (t *Tx) ForceReleaseClaim(project, itemID, reason string) error {
	if reason == "" {
		return errors.New("강제 반납에는 사유가 필수다 — 사유 없는 회수는 나중에 되짚을 수 없다")
	}
	res, err := t.tx.ExecContext(t.ctx, `
		UPDATE claim SET released_at = ?, force_reason = ?
		WHERE project = ? AND item_id = ? AND released_at IS NULL`,
		fmtTime(time.Now()), reason, project, itemID)
	if err != nil {
		return fmt.Errorf("선점 강제 반납 실패(project=%q item=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("선점 강제 반납 결과 확인 실패(project=%q item=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	if n == 0 {
		return fmt.Errorf("항목 %s/%s 에 살아 있는 선점이 %w",
			clip(project, 64), clip(itemID, 64), ErrNotFound)
	}
	if _, err := t.tx.ExecContext(t.ctx,
		`UPDATE item SET state = 'open' WHERE project = ? AND id = ? AND state = 'claimed'`,
		project, itemID); err != nil {
		return fmt.Errorf("항목 상태 되돌리기 실패(project=%q item=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	return nil
}

// ForceReleaseClaim 은 단발 트랜잭션으로 감싼 것이다.
func (s *Store) ForceReleaseClaim(ctx context.Context, project, itemID, reason string) error {
	return s.Tx(ctx, func(t *Tx) error { return t.ForceReleaseClaim(project, itemID, reason) })
}

// FinishItem 은 선점 반납과 항목 종료를 **한 트랜잭션에서** 처리한다.
//
// 기존 규율은 이 순서를 산문으로 강제했고(문서 → done → add → unregister),
// 순서를 어긴 세션이 실제로 있었다. 원자화하면 검산할 순서 자체가 사라진다.
func (t *Tx) FinishItem(project, itemID, sessionID string, outcome model.ItemState, closeReason string) error {
	if err := ValidateFinish(outcome, closeReason); err != nil {
		return err
	}
	cur, err := t.claimRow(project, itemID)
	switch {
	case err == nil && cur.ReleasedAt == nil && cur.SessionID != sessionID:
		return &ClaimHeldError{
			Project: project, ItemID: itemID, Holder: cur.SessionID, At: cur.At,
			Reason: fmt.Sprintf("세션 %s 의 선점이라 세션 %s 가 끝낼 수 없다", cur.SessionID, sessionID)}
	case err == nil && cur.ReleasedAt == nil:
		if _, err := t.tx.ExecContext(t.ctx,
			`UPDATE claim SET released_at = ? WHERE project = ? AND item_id = ?`,
			fmtTime(time.Now()), project, itemID); err != nil {
			return fmt.Errorf("종료 시 선점 반납 실패(project=%q item=%q): %w",
				clip(project, 64), clip(itemID, 64), err)
		}
	case err != nil && !errors.Is(err, ErrNotFound):
		return err
	}
	return t.SetItemState(project, itemID, outcome, closeReason)
}

// FinishItem 은 단발 트랜잭션으로 감싼 것이다.
func (s *Store) FinishItem(ctx context.Context, project, itemID, sessionID string, outcome model.ItemState, closeReason string) error {
	return s.Tx(ctx, func(t *Tx) error {
		return t.FinishItem(project, itemID, sessionID, outcome, closeReason)
	})
}

// ClaimedItems 는 세션이 지금 쥐고 있는 항목 id 를 낸다.
func (s *Store) ClaimedItems(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT item_id FROM claim WHERE session_id = ? AND released_at IS NULL ORDER BY item_id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("선점 목록 조회 실패(session_id=%q): %w", clip(sessionID, 64), err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("선점 행 해석 실패: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("선점 목록 순회 실패: %w", err)
	}
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// pick_eval
// ─────────────────────────────────────────────────────────────────────────────

// RecordPickEval 은 추천 1건과 **탈락 사유 전부**를 남긴다.
// 사유가 없으면 큐는 블랙박스가 되고, 블랙박스는 두 번째 세션부터 무시된다.
func (t *Tx) RecordPickEval(e model.PickEval) error {
	rejected := e.Rejected
	if rejected == nil {
		rejected = []model.Rejection{}
	}
	buf, err := json.Marshal(rejected)
	if err != nil {
		return fmt.Errorf("탈락 사유 직렬화 실패(project=%q session=%q): %w",
			clip(e.Project, 64), clip(e.SessionID, 64), err)
	}
	if e.At.IsZero() {
		e.At = nowStamp()
	}
	if _, err := t.tx.ExecContext(t.ctx,
		`INSERT INTO pick_eval(project, session_id, at, picked, rejected) VALUES (?, ?, ?, ?, ?)`,
		e.Project, e.SessionID, fmtTime(e.At), nullStr(e.Picked), string(buf)); err != nil {
		return fmt.Errorf("추천 판정 기록 실패(project=%q session=%q): %w",
			clip(e.Project, 64), clip(e.SessionID, 64), err)
	}
	return nil
}

// RecordPickEval 은 단발 트랜잭션으로 감싼 것이다.
func (s *Store) RecordPickEval(ctx context.Context, e model.PickEval) error {
	return s.Tx(ctx, func(t *Tx) error { return t.RecordPickEval(e) })
}

// ─────────────────────────────────────────────────────────────────────────────

func marshalStrings(v []string) (string, error) {
	if v == nil {
		v = []string{}
	}
	b, err := json.Marshal(v)
	return string(b), err
}

func nullTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return fmtTime(*t)
}
