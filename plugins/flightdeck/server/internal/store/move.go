package store

import (
	"context"
	"errors"
	"fmt"
)

// MoveVerdict 는 항목을 다른 프로젝트로 옮길 수 있는지의 판정이다.
//
// JudgeClaim 과 같은 모양으로 **사유를 반드시 담는다**. 불리언 실패로 접으면
// 호출자가 무엇을 고쳐야 하는지 모르고, 그러면 우회하거나 포기한다.
type MoveVerdict struct {
	OK     bool
	Reason string
}

// JudgeMove 는 이동 가능 여부를 판정한다. 순수 함수다.
//
//   - itemFound:   원 항목이 존재하는가
//   - from, to:    현재 프로젝트 · 대상 프로젝트
//   - holder:      현재 점유자의 session_id. 빈 문자열이면 미선점
//   - targetFound: 대상 프로젝트가 원장에 있는가
//
// ★ 종료 상태(done·dropped)는 **막지 않는다.** 끝난 항목도 어느 프로젝트의 이력인지가
// 맞아야 하고, 오히려 그것이 이 명령이 필요한 이유의 절반이다(잘못 등록된 채 끝난 항목).
func JudgeMove(itemFound bool, from, to string, holder string, targetFound bool) MoveVerdict {
	switch {
	case to == "":
		return MoveVerdict{Reason: "대상 프로젝트가 비었다"}

	case !itemFound:
		return MoveVerdict{Reason: "그런 항목이 없다"}

	case from == to:
		// 실패로 두는 이유: 성공으로 접으면 "옮겼다"는 출력이 나가는데 아무 일도 안 일어난다.
		// 오타로 같은 이름을 준 사람은 그 출력을 보고 옮겨졌다고 믿는다.
		return MoveVerdict{Reason: fmt.Sprintf("이미 프로젝트 %s 다", clip(to, 64))}

	case !targetFound:
		// ★ 없는 프로젝트를 **만들지 않는다.** 자동 생성이 정확히 유령 프로젝트를 만든 경로다
		// (워크트리 이름이 프로젝트가 되어 워크트리마다 프로젝트가 생겼다 — 실물로 재현됐다).
		// 여기서 만들어 주면 오타 하나가 새 프로젝트를 낳고, 그 항목은 다시 실종된다.
		return MoveVerdict{Reason: fmt.Sprintf(
			"대상 프로젝트 %s 가 원장에 없다 — 자동으로 만들지 않는다(오타 하나가 유령 프로젝트를 낳는다)",
			clip(to, 64))}

	case holder != "":
		// 선점은 세션에 걸리고 세션은 프로젝트에 걸린다. 쥔 채로 옮기면
		// 그 선점이 **남의 프로젝트를 가리키는 세션**의 것이 된다.
		return MoveVerdict{Reason: fmt.Sprintf(
			"세션 %s 가 선점하고 있다 — 먼저 반납해라(release 또는 finish)", clip(holder, 40))}

	default:
		return MoveVerdict{OK: true, Reason: fmt.Sprintf("%s → %s 로 옮길 수 있다", clip(from, 64), clip(to, 64))}
	}
}

// MoveRefusedError 는 판정이 거절했을 때의 오류다. 사유 전문을 담는다.
type MoveRefusedError struct {
	Project string
	ItemID  string
	To      string
	Reason  string
}

func (e *MoveRefusedError) Error() string {
	return fmt.Sprintf("항목 %s 를 %s 로 옮길 수 없다: %s",
		clip(e.ItemID, 64), clip(e.To, 64), e.Reason)
}

// MoveItem 은 항목과 **그 항목을 (project, item_id) 로 가리키는 모든 행**을 함께 옮긴다.
//
// ★ 항목 행만 옮기면 안 된다. item_after·claim 은 item(project, id) 에 복합 FK 를 걸고
// (ON UPDATE 는 NO ACTION 이다) job 은 FK 없이 같은 두 칼럼을 들고 있다.
//
// ★ item_dependents 는 2026-08-23 에 증분 011 이 **표째 걷었다**. 앞 회차에는 값만 비우고
// 목록에 남겼는데(그때는 표가 살아 있어 빼면 대조 시험이 빨개졌다), 이제는 반대로 남기면
// 빨개진다 — 그 대조가 살아 있는 스키마를 본다.
func (t *Tx) MoveItem(project, itemID, toProject, sessionID string) error {
	if _, err := t.tx.ExecContext(t.ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return fmt.Errorf("FK 검사 지연 설정 실패: %w", err)
	}
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE item SET project = ? WHERE project = ? AND id = ?`, toProject, project, itemID)
	if err != nil {
		return fmt.Errorf("항목 이동 실패(project=%q id=%q to=%q): %w",
			clip(project, 64), clip(itemID, 64), clip(toProject, 64), err)
	}
	if err := affectedOne(res, NFItem, project, itemID); err != nil {
		return err
	}
	// 딸린 표 넷. 이름을 여기 한 자리에 모아 둔다 — 표가 늘면 이 목록도 늘려야 하고,
	// 흩어 놓으면 늘리는 사람이 전부를 못 찾는다.
	for _, tbl := range []string{"item_after", "claim", "job"} {
		if _, err := t.tx.ExecContext(t.ctx,
			fmt.Sprintf(`UPDATE %s SET project = ? WHERE project = ? AND item_id = ?`, tbl),
			toProject, project, itemID); err != nil {
			return fmt.Errorf("딸린 행 이동 실패(table=%s project=%q id=%q): %w",
				tbl, clip(project, 64), clip(itemID, 64), err)
		}
	}
	// ★ 원장에 남긴다. publish(SSE)는 지금 보고 있는 사람에게만 가고 사라진다 —
	// 이 명령이 존재하는 이유가 "왜 이 항목이 여기 있나"에 답할 자리가 없어서였는데,
	// 옮긴 사실 자체를 안 남기면 같은 공백을 새로 만든다.
	//
	// **대상 프로젝트에 적는다.** 그 질문은 항목이 지금 있는 자리에서 나오기 때문이다.
	// 떠난 쪽의 사정은 payload 의 from 이 답한다.
	t.LogEvent("item.move", toProject, sessionID, map[string]any{
		"item": clip(itemID, 100), "from": clip(project, 64), "to": clip(toProject, 64),
	})

	return nil
}

// RewriteDepProject 는 이 항목을 가리키던 선행 참조를 **새 프로젝트로 다시 쓴다**(증분 015).
//
// ★ **이 함수가 있기 전에는 그 관계가 죽었다.** 옛 프로젝트에 남은 항목이 옮겨 간 항목을
// dep_item 으로 가리키면 그 참조는 같은 프로젝트 안에서만 해석돼, 이동 직후부터
// 「그 항목이 큐에 없다」(after-unknown)가 됐다. 막지 않고 수만 세어 알렸던 것이
// MoveResult.CrossRefs 이고, 이제 그 수는 **다시 쓴 건수**가 된다.
//
// ★ **dep_project 가 비어 있던 행만 고친다.** 이미 다른 프로젝트를 명시한 행은 이 항목을
// 가리키는 것이 아니다 — 같은 id 를 가진 남의 항목을 가리킨다(항목 id 는 프로젝트마다
// 독립이다). 그것까지 고치면 이 이동이 무관한 관계를 조용히 옮긴다.
//
// ★ **자기 자신은 안 고친다.** 옮겨 간 항목이 스스로를 선행으로 가리키는 행(스키마가
// 막지 않는다)은 이동과 함께 새 프로젝트로 따라갔으므로 이미 자기 프로젝트다.
func (t *Tx) RewriteDepProject(fromProject, itemID, toProject string) (int, error) {
	res, err := t.tx.ExecContext(t.ctx, `
		UPDATE item_after SET dep_project = ?
		WHERE dep_item = ? AND project = ? AND dep_project IS NULL AND item_id <> ?`,
		toProject, itemID, fromProject, itemID)
	if err != nil {
		return 0, fmt.Errorf("선행 참조 다시 쓰기 실패(project=%q id=%q → %q): %w",
			clip(fromProject, 64), clip(itemID, 64), clip(toProject, 64), err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("선행 참조 다시 쓰기 결과 확인 실패(project=%q id=%q): %w",
			clip(fromProject, 64), clip(itemID, 64), err)
	}
	return int(n), nil
}

// ★ **DependentsAcross 는 걷었다(증분 015).** 그 함수는 "옮기면 몇 건이 표현 불가가
// 되나"를 셌는데, 이제 그 관계는 표현 가능하고 RewriteDepProject 가 **다시 쓴다**.
// 세기만 하는 함수를 남겨 두면 다음 사람이 그 수를 "끊긴 건수"로 읽고, 실제로는
// 아무것도 안 끊긴 이동에 경고를 붙인다 — 그 오독이 이 축의 유일한 위험이다.

// MoveItem 은 판정 → 이동을 한 트랜잭션으로 감싼 것이다.
//
// 판정에 필요한 상태(항목 존재·점유자·대상 프로젝트 존재)를 **같은 트랜잭션 안에서** 읽는다.
// 밖에서 읽어 넘기면 그 사이 남이 선점해도 이 이동이 통과한다.
func (s *Store) MoveItem(ctx context.Context, project, itemID, toProject, sessionID string) (crossRefs int, err error) {
	err = s.Tx(ctx, func(t *Tx) error {
		_, gerr := t.GetItem(project, itemID)
		found := gerr == nil
		if gerr != nil && !errors.Is(gerr, ErrNotFound) {
			return gerr
		}
		// ★ claimRow 는 **반납된 선점도 낸다**(이력이 자산이라 그렇게 설계돼 있다).
		// ReleasedAt 을 안 보면 한 번이라도 잡혔던 항목은 영영 못 옮긴다 —
		// 그리고 옮겨야 할 항목은 대부분 그 모양이다(잡아서 일하다 잘못 등록된 것을 알았으므로).
		var holder string
		if found {
			if c, cerr := t.claimRow(project, itemID); cerr == nil {
				if c.ReleasedAt == nil {
					holder = c.SessionID
				}
			} else if !errors.Is(cerr, ErrNotFound) {
				return cerr
			}
		}
		targetFound, terr := t.projectExists(toProject)
		if terr != nil {
			return terr
		}
		if v := JudgeMove(found, project, toProject, holder, targetFound); !v.OK {
			return &MoveRefusedError{Project: project, ItemID: itemID, To: toProject, Reason: v.Reason}
		}
		if err := t.MoveItem(project, itemID, toProject, sessionID); err != nil {
			return err
		}
		// ★ **이동 뒤에 다시 쓴다.** 순서가 뒤바뀌면 아직 옛 프로젝트에 있는 항목을
		//   가리키는 참조에 새 프로젝트를 적게 되고, 그 사이 이동이 거절되면 원장에
		//   가리킬 곳 없는 참조가 남는다. 같은 트랜잭션이라 둘은 함께 커밋되거나 함께
		//   롤백된다.
		n, derr := t.RewriteDepProject(project, itemID, toProject)
		if derr != nil {
			return derr
		}
		crossRefs = n
		return nil
	})
	return crossRefs, err
}

// projectExists 는 대상 프로젝트가 원장에 있는지다.
func (t *Tx) projectExists(id string) (bool, error) {
	var n int
	if err := t.tx.QueryRowContext(t.ctx,
		`SELECT COUNT(*) FROM project WHERE id = ?`, id).Scan(&n); err != nil {
		return false, fmt.Errorf("프로젝트 조회 실패(id=%q): %w", clip(id, 64), err)
	}
	return n > 0, nil
}
