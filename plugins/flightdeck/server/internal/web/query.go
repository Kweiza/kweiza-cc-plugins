package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 대시보드 전용 읽기 질의.
//
// ★ 왜 여기서 SQL 을 쓰는가 — 이 네 축(탈락 사유 분포·스냅숏 목록·종료된 항목·미확인 잡)에
// **store 의 접근자가 없기 때문이다.** store 는 담당 밖이라 한 줄도 고치지 않았고,
// 그렇다고 이 축들을 빼면 설계 §6 이 요구하는 섹션 셋(③ 탈락 사유 분포 · ④ 랜딩 이력 ·
// ② 미확인 결과)이 통째로 사라진다. 없는 축을 조용히 빼는 것이 이 제품이 반복해 맞은 실패이므로
// 사실을 표면에 두고(핸드오프의 후속 항목) 여기서는 **읽기 전용 SELECT 만** 한다.
//
// 쓰기는 하나도 없다. 쓰기는 전부 store 의 트랜잭션 API 를 거친다(actions.go).

// pickEvals 는 최근 큐 판정 기록을 최신순으로 읽는다.
func pickEvals(ctx context.Context, db *sql.DB, project string, limit int) ([]model.PickEval, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT session_id, at, picked, rejected
		FROM pick_eval WHERE project = ? ORDER BY id DESC LIMIT ?`, project, limit)
	if err != nil {
		return nil, fmt.Errorf("큐 판정 기록 조회 실패(project=%q): %w", Clip(project, 64), err)
	}
	defer rows.Close()

	var out []model.PickEval
	for rows.Next() {
		var (
			e        model.PickEval
			at       string
			picked   sql.NullString
			rejected string
		)
		if err := rows.Scan(&e.SessionID, &at, &picked, &rejected); err != nil {
			return nil, fmt.Errorf("큐 판정 행 해석 실패: %w", err)
		}
		e.Project, e.Picked = project, picked.String
		if e.At, err = parseStamp(at); err != nil {
			return nil, err
		}
		// 사유 JSON 이 깨졌으면 삼키지 않는다 — 분포가 조용히 짧아지면
		// "탈락이 없었다"와 "우리가 못 읽었다"가 같은 화면이 된다.
		if rejected != "" {
			if err := json.Unmarshal([]byte(rejected), &e.Rejected); err != nil {
				return nil, fmt.Errorf("탈락 사유 해석 실패(project=%q session=%q): %w",
					Clip(project, 64), Clip(e.SessionID, 64), err)
			}
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("큐 판정 목록 순회 실패: %w", err)
	}
	return out, nil
}

// closedItems 는 종료된 항목을 최근 종료순으로 읽는다(랜딩 이력의 Tier A 분).
func closedItems(ctx context.Context, db *sql.DB, project string, limit int) ([]model.Item, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, title, state, close_reason, landed_ref, closed_at
		FROM item WHERE project = ? AND state IN ('done','dropped')
		ORDER BY closed_at DESC LIMIT ?`, project, limit)
	if err != nil {
		return nil, fmt.Errorf("종료된 항목 조회 실패(project=%q): %w", Clip(project, 64), err)
	}
	defer rows.Close()

	var out []model.Item
	for rows.Next() {
		var (
			it                        model.Item
			state                     string
			title, reason, landed, ca sql.NullString
		)
		if err := rows.Scan(&it.ID, &title, &state, &reason, &landed, &ca); err != nil {
			return nil, fmt.Errorf("종료된 항목 행 해석 실패: %w", err)
		}
		it.Project, it.State = project, model.ItemState(state)
		it.Title, it.CloseReason, it.LandedRef = title.String, reason.String, landed.String
		if ca.Valid && ca.String != "" {
			t, err := parseStamp(ca.String)
			if err != nil {
				return nil, err
			}
			it.ClosedAt = &t
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("종료된 항목 목록 순회 실패: %w", err)
	}
	return out, nil
}

// UnackedJob 은 아직 아무도 확인하지 않은 잡 결과 하나다(설계 §6 ②, Tier B).
type UnackedJob struct {
	ID       string
	Kind     string
	ItemID   string
	State    string
	FailKind string
	EndedAt  time.Time
	LogTail  string
}

// unackedJobs 는 미확인 결과를 읽는다.
//
// Tier A 에는 러너가 없어 이 표가 비어 있다. 그래도 **질의는 한다** —
// 안 보면 "결과가 없다"와 "이 화면이 그 축을 안 본다"가 구분되지 않고,
// 그 구분의 부재가 이 제품이 겨냥하는 실패 그 자체다.
func unackedJobs(ctx context.Context, db *sql.DB, project string, limit int) ([]UnackedJob, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, kind, item_id, state, fail_kind, ended_at, log_tail
		FROM job WHERE project = ? AND ack_at IS NULL AND state IN ('ok','fail','stalled','bypassed')
		ORDER BY queued_at DESC LIMIT ?`, project, limit)
	if err != nil {
		return nil, fmt.Errorf("미확인 잡 조회 실패(project=%q): %w", Clip(project, 64), err)
	}
	defer rows.Close()

	var out []UnackedJob
	for rows.Next() {
		var (
			j                           UnackedJob
			item, failKind, ended, tail sql.NullString
			kind, state                 string
		)
		if err := rows.Scan(&j.ID, &kind, &item, &state, &failKind, &ended, &tail); err != nil {
			return nil, fmt.Errorf("미확인 잡 행 해석 실패: %w", err)
		}
		j.Kind, j.State = kind, state
		j.ItemID, j.FailKind, j.LogTail = item.String, failKind.String, tail.String
		if ended.Valid && ended.String != "" {
			if j.EndedAt, err = parseStamp(ended.String); err != nil {
				return nil, err
			}
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("미확인 잡 목록 순회 실패: %w", err)
	}
	return out, nil
}

// parseStamp 는 저장 표기를 시각으로 옮긴다.
// store 의 것과 같은 표기(RFC3339·UTC·마이크로초)를 읽지만 그쪽은 비공개라 여기 한 벌 둔다.
func parseStamp(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("시각 해석 실패(%q): %w", Clip(s, 64), err)
	}
	return t.UTC(), nil
}
