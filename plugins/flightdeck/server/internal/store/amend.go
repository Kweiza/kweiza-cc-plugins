package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

	// ★ 사유의 **1차 방어는 여기다.** AddJudgment 가 빈 판단 본문에 대해 같은 자리에서
	// 같은 일을 하고, 그 주석이 이유를 적었다 — "스키마 CHECK 가 최후 방어이지 1차 방어가
	// 아니다." 이 표에서는 그 말이 두 겹으로 참이다:
	//
	//   ① 공백만 든 사유("   ")는 CHECK(reason <> '') 를 **통과해 그대로 저장된다.**
	//      되짚을 수 없는 개정이 추가 전용 표에 남는데, 사유를 남기는 것이 이 표의 존재 이유다.
	//   ② 정말 빈 사유가 CHECK 까지 닿으면 JudgeConstraintCode 가 CHECK 를 **일부러 안 접어**
	//      500 으로 나간다(1차 방어가 앞에 있다는 전제다). 호출자가 고칠 거리인데 등급이 틀리고,
	//      500 은 멱등 표에 안 남아 재시도가 계속 하류로 들어간다.
	//
	// 저장은 **원문 그대로** 한다(TrimSpace 한 값이 아니다) — AddJudgment 가 j.Body 에
	// 하는 것과 같다. 거절 판정에만 다듬은 값을 쓴다.
	if strings.TrimSpace(p.Reason) == "" {
		return out, errors.New("개정 사유가 비었다 — 무엇을 왜 고쳤는지가 item_revision 의 존재 이유다")
	}

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

	// 개정 번호를 먼저 뽑는다. nextItemRev 는 item_revision 만 보므로 UPDATE 와 순서가
	// 무관하고, 아래 INSERT 가 그 값을 필요로 한다 — 이 순서의 이유는 그것뿐이다.
	//
	// ★ **어느 쪽이 남는지를 순서로 정하는 것이 아니다.** INSERT 와 UPDATE 는 같은
	//   트랜잭션이고 Store.Tx 가 오류에 Rollback 하므로, 실패하면 **어느 쪽도 안 남는다.**
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
		// ★ 맨 오류로 올리면 안 된다. 여기로 오는 갈래 둘이 전부 호출자가 고칠 거리다 —
		//   등록 안 된 세션 id → FK 787(ClaimItem 이 이미 같은 형태로 접는다) ·
		//   두 세션의 동시 amend 로 rev 가 겹침 → PK 1555(nextItemRev 주석이 적은 그 갈래).
		//   접지 않으면 둘 다 500 이 되고, 500 은 멱등 표에 안 남아 재시도가 계속 하류로
		//   들어간다(constraint.go 머리말).
		//
		// ★ 대상은 **TargetItem** 이다. item_revision 용 대상을 새로 만들지 않는다 —
		//   ConflictTargets() 는 표면이 대상마다 문구를 갖는지 전수로 재는 목록이라,
		//   대상을 늘리면 그 문구를 함께 넣어야 하고 여기서 할 일이 아니다.
		return out, writeErr(err, writeTarget{
			Target: TargetItem, Project: project, ID: itemID,
			RefHint: fmt.Sprintf("항목 %s/%s · 세션 %s · 개정 %d",
				clip(project, 64), clip(itemID, 64), clip(sessionID, 64), rev),
		}, "개정 이력 적재 실패(project=%q id=%q rev=%d)",
			clip(project, 64), clip(itemID, 64), rev)
	}

	afterPathsJSON, err := marshalStrings(after.Paths)
	if err != nil {
		return out, fmt.Errorf("항목 paths 직렬화 실패(id=%q): %w", clip(itemID, 64), err)
	}
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE item SET title = ?, body = ?, paths = ? WHERE project = ? AND id = ?`,
		after.Title, after.Body, afterPathsJSON, project, itemID)
	if err != nil {
		return out, writeErr(err, writeTarget{
			Target: TargetItem, Project: project, ID: itemID,
			RefHint: fmt.Sprintf("항목 %s/%s", clip(project, 64), clip(itemID, 64)),
		}, "항목 본문 갱신 실패(project=%q id=%q)", clip(project, 64), clip(itemID, 64))
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

// ─────────────────────────────────────────────────────────────────────────────
// 개정 이력을 **읽는** 자리
// ─────────────────────────────────────────────────────────────────────────────
//
// ★ 이 표는 2026-09-16 에 생기고 2026-09-17 까지 **읽는 경로가 레포 전체에 0건**이었다.
// RenderAmend 는 "개정 3 — 옛 값은 그대로 남는다"고 좌표를 약속하는데 그 좌표를 열 문이
// 없었다. 쌓기만 하고 못 읽는 표는 복구 경로가 0인 것과 화면에서 구분되지 않는다.

// ItemRevisions 는 항목 하나의 개정 이력이다. **rev 오름차순**(오래된 것부터)이다.
//
// ★ 행이 없으면 빈 슬라이스다 — 오류가 아니다. "한 번도 안 고친 항목"은 정상이고,
// 그것을 실패로 접으면 화면이 「없다」와 「못 읽었다」를 가를 근거를 잃는다.
// 그 둘을 가르는 것은 호출부다(service.ShowItem 이 Derived 로 고백한다).
//
// ★ Changed 는 여기서 안 채운다. 그 값은 **지금 값**을 함께 봐야 정해지는데, 이 함수가
// 지금 값을 또 읽으면 호출부가 이미 읽은 것과 두 벌이 된다 — MarkItemRevisionChanges 가 채운다.
func (s *Store) ItemRevisions(ctx context.Context, project, itemID string) ([]model.ItemRevision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT rev, at, COALESCE(session_id, ''), title, body, paths, reason
		   FROM item_revision WHERE project = ? AND item_id = ? ORDER BY rev`,
		project, itemID)
	if err != nil {
		return nil, fmt.Errorf("개정 이력 조회 실패(project=%q item=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	defer rows.Close()

	out := []model.ItemRevision{}
	for rows.Next() {
		var r model.ItemRevision
		var at, pathsRaw string
		if err := rows.Scan(&r.Rev, &at, &r.SessionID, &r.Title, &r.Body, &pathsRaw, &r.Reason); err != nil {
			return nil, fmt.Errorf("개정 이력 행 해석 실패(project=%q item=%q): %w",
				clip(project, 64), clip(itemID, 64), err)
		}
		if r.At, err = parseTime(at); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(pathsRaw), &r.Paths); err != nil {
			return nil, fmt.Errorf("개정 이력 paths 해석 실패(project=%q item=%q rev=%d): %w",
				clip(project, 64), clip(itemID, 64), r.Rev, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("개정 이력 순회 실패(project=%q item=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	return out, nil
}

// MarkItemRevisionChanges 는 개정 사슬에 "이 개정이 무엇을 바꿨나"를 채운다. 순수 함수다.
//
// item_revision 은 **고치기 직전의 값**만 담으므로 "무엇이 바뀌었나"는 표에 없다.
// 사슬로 복원한다: rev N 의 다음 상태는 rev N+1 이 담은 옛 값이고, 마지막 rev 의 다음
// 상태는 지금 값(current)이다. 그래서 이 값은 **파생이지 기록이 아니다**.
//
// ★ event 표의 `item.amend` 페이로드에도 같은 값이 있지만 그것을 안 쓴다 — 그쪽은 FK 도
// NOT NULL 도 없는 관측 로그라 정본이 아니고, 행이 빠져도 아무도 안 아프다. 사슬은
// 추가 전용 표 자신에서 복원되므로 그 결손이 원리적으로 없다.
//
// ★ revs 는 **rev 오름차순**이어야 한다(ItemRevisions 가 그렇게 낸다). 역순으로 주면
// 조용히 거짓을 채운다 — 그래서 정렬을 여기서 다시 하지 않고 계약으로 둔다.
//
// ★ current 를 revs 보다 먼저 읽고 그 사이 새 개정이 들어오면 **마지막 행의 축이 한 칸
// 낡는다**(그 개정분이 마지막 행에 합쳐져 보인다). 읽기 전용 화면의 한 칸 오차라
// 트랜잭션을 열지 않는다 — `_txlock=immediate` 는 쓰기 잠금이고, 이 조회 때문에 그것을
// 잡으면 읽기 하나가 모든 쓰기를 세운다.
func MarkItemRevisionChanges(revs []model.ItemRevision, current model.Item) {
	for i := range revs {
		before := model.Item{Title: revs[i].Title, Body: revs[i].Body, Paths: revs[i].Paths}
		after := model.Item{Title: current.Title, Body: current.Body, Paths: current.Paths}
		if i+1 < len(revs) {
			after = model.Item{Title: revs[i+1].Title, Body: revs[i+1].Body, Paths: revs[i+1].Paths}
		}
		revs[i].Changed = amendChanged(before, after)
	}
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
