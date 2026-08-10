package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// project · machine — 설정 계층.
//
// 둘 다 다른 표의 FK 대상이라 **먼저 있어야 한다**. 없으면 세션 등록이 FK 위반으로 죽고,
// 그 오류가 곧 "프로젝트를 먼저 등록하라"는 안내가 된다(앱에서 미리 조회해 판정하지 않는다).

// ─────────────────────────────────────────────────────────────────────────────
// project
// ─────────────────────────────────────────────────────────────────────────────

// projectCols 는 프로젝트 조회의 컬럼 목록이다.
// judgmentCols·sessionCols 와 같은 이유로 상수다 — 목록을 손으로 다시 적으면
// 순서가 어긋나는 순간 Scan 이 조용히 엉뚱한 값을 채운다(전부 문자열이라 타입 오류도 안 난다).
const projectCols = `id, path, remote_url, default_branch, config, config_from_sha, created_at, pinned_at, archived_at`

// machineCols 는 머신 조회의 컬럼 목록이다.
const machineCols = `id, hostname, first_seen, last_seen`

// UpsertProject 는 프로젝트를 등록하거나 갱신한다.
//
// created_at 은 첫 등록 시각을 보존한다 — 재등록이 나이를 0으로 되돌리면
// "언제부터 있던 프로젝트인가"가 사라진다.
//
// ★ pinned_at·archived_at 도 같은 이유로 **갱신 목록 밖**이다. 이 함수는 세션이 열릴 때마다
// 돌고(service/session.go 의 자동 등록), 목록에 넣으면 훅이 세션을 열 때마다 사람이 고른
// 표시 축이 날아간다. 그 손실은 어느 화면에도 안 뜬다 — 다음에 볼 때 그냥 안 켜져 있을 뿐이다.
// 그 축을 쓰는 문은 SetProjectView 하나뿐이다.
func (t *Tx) UpsertProject(p model.Project) error {
	if p.ID == "" {
		return errors.New("프로젝트 id 가 비었다")
	}
	if p.DefaultBranch == "" {
		p.DefaultBranch = "main"
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = nowStamp()
	}
	_, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO project(id, path, remote_url, default_branch, config, config_from_sha, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  path            = excluded.path,
		  remote_url      = excluded.remote_url,
		  default_branch  = excluded.default_branch,
		  config          = excluded.config,
		  config_from_sha = excluded.config_from_sha`,
		p.ID, p.Path, nullStr(p.RemoteURL), p.DefaultBranch,
		nullStr(p.Config), nullStr(p.ConfigFromSHA), fmtTime(p.CreatedAt))
	if err != nil {
		return fmt.Errorf("프로젝트 upsert 실패(id=%q): %w", clip(p.ID, 64), err)
	}
	return nil
}

// UpsertProject 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) UpsertProject(ctx context.Context, p model.Project) error {
	return s.Tx(ctx, func(t *Tx) error { return t.UpsertProject(p) })
}

// scanProject 는 projectCols 순서의 한 행을 읽는다.
//
// ★ ListProjects 와 getProject 가 이것을 공유한다. Scan 목록이 두 벌이면 컬럼을 더할 때
// 한쪽만 고쳐지고, 전부 문자열이라 타입 오류도 안 난다 — projectCols 주석이 경고하는 실패다.
func scanProject(sc interface{ Scan(...any) error }) (model.Project, error) {
	var p model.Project
	var remote, config, fromSHA, pinned, archived sql.NullString
	var created string
	if err := sc.Scan(&p.ID, &p.Path, &remote, &p.DefaultBranch, &config, &fromSHA,
		&created, &pinned, &archived); err != nil {
		return model.Project{}, err
	}
	p.RemoteURL, p.Config, p.ConfigFromSHA = str(remote), str(config), str(fromSHA)
	var err error
	if p.CreatedAt, err = parseTime(created); err != nil {
		return model.Project{}, err
	}
	// ★ store.go 의 parseNullTime 은 *time.Time 을 낸다(landing.go·item.go 의 포인터
	//   필드가 그것을 그대로 받는다). PinnedAt·ArchivedAt 은 포인터가 아니라 값 필드라
	//   nil 을 제로값으로 편다 — 그 자체가 이미 "NULL == 아님"이다.
	pinnedAt, err := parseNullTime(pinned)
	if err != nil {
		return model.Project{}, err
	}
	if pinnedAt != nil {
		p.PinnedAt = *pinnedAt
	}
	archivedAt, err := parseNullTime(archived)
	if err != nil {
		return model.Project{}, err
	}
	if archivedAt != nil {
		p.ArchivedAt = *archivedAt
	}
	return p, nil
}

func getProject(ctx context.Context, q dbtx, id string) (model.Project, error) {
	row := q.QueryRowContext(ctx, `
		SELECT `+projectCols+`
		FROM project WHERE id = ?`, id)
	p, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return p, notFound(NFProject, "", id)
	}
	if err != nil {
		return p, fmt.Errorf("프로젝트 조회 실패(id=%q): %w", clip(id, 64), err)
	}
	return p, nil
}

// GetProject 는 프로젝트 하나를 읽는다. 없으면 ErrNotFound 를 감싼 오류다.
func (s *Store) GetProject(ctx context.Context, id string) (model.Project, error) {
	return getProject(ctx, s.db, id)
}

// GetProject 는 트랜잭션 안에서 읽는다.
func (t *Tx) GetProject(id string) (model.Project, error) {
	return getProject(t.ctx, t.tx, id)
}

// ListProjects 는 전부를 id 순으로 낸다. 프로젝트 수는 사람이 등록한 만큼이라 페이징이 없다.
func (s *Store) ListProjects(ctx context.Context) ([]model.Project, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+projectCols+`
		FROM project ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("프로젝트 목록 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []model.Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("프로젝트 행 해석 실패: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("프로젝트 목록 순회 실패: %w", err)
	}
	return out, nil
}

// SetProjectView 는 프로젝트의 표시 축(핀·보관)을 정한다. 제로값은 NULL 이다.
//
// ★ 이 축은 표시 계층이라 사유를 안 받는다. 이 화면에서 사유가 필수인 셋(선점 회수 ·
// 항목 폐기 · 줄 회수)은 전부 남의 일을 뺏거나 되돌릴 수 없는 것인데, 핀과 보관은 둘 다
// 아니다 — 내 판이고 클릭 하나로 돌아온다. 되짚을 거리는 시각과 event 가 남긴다.
//
// ★ 이 UPDATE 는 두 축을 **통째로** 덮어쓴다 — "핀만 바꾼다"는 계약이 아니다. 호출자가
// archived 자리에 제로값을 넘기면 기존 보관이 있어도 조용히 풀린다(TestSetProjectViewOverwritesBothAxesTogether
// 가 그 동작을 의도로 못박아 둔다). 한 축만 바꾸고 싶으면 이 함수를 부르기 전에 같은 Tx
// 안에서 GetProject 로 다른 축의 현재 값을 읽어 그대로 함께 실어야 한다 — 그러지 않으면
// UpsertProject 에 대해 이미 막은 것과 같은 모양의 손실이 난다(핀 토글 처리기가
// `SetProjectView(ctx, id, time.Now(), time.Time{})` 라고만 쓰면 보관이 날아간다).
func (t *Tx) SetProjectView(id string, pinned, archived time.Time) error {
	res, err := t.tx.ExecContext(t.ctx, `
		UPDATE project SET pinned_at = ?, archived_at = ? WHERE id = ?`,
		nullTime(pinned), nullTime(archived), id)
	if err != nil {
		return fmt.Errorf("프로젝트 표시 축 갱신 실패(id=%q): %w", clip(id, 64), err)
	}
	// ★ UPDATE 는 0행이어도 성공한다. 확인하지 않으면 프로젝트 id 오타가 조용히 성공하고,
	//   화면은 "눌렀는데 아무 일도 안 일어난다"가 된다 — 그 증상에서 원인이 안 보인다.
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("프로젝트 표시 축 갱신 결과 확인 실패(id=%q): %w", clip(id, 64), err)
	}
	// ★ ErrNotFound 를 fmt.Errorf 로 직접 감싸지 않는다. getProject(project.go 위쪽)가
	//   쓰는 notFound(NFProject, ...) 와 같은 길로 보내야 internal/api 의 errors.As(*NotFoundError)
	//   가 좌표·처방을 붙일 수 있다 — sentinel 로 새면 일반 404 문구로 접혀 "무엇이
	//   없었는지"가 응답에서 사라진다(notfound.go 의 그 타입 주석이 이 실패를 이미 적어 뒀다).
	if n == 0 {
		return notFound(NFProject, "", id)
	}
	return nil
}

// SetProjectView 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) SetProjectView(ctx context.Context, id string, pinned, archived time.Time) error {
	return s.Tx(ctx, func(t *Tx) error { return t.SetProjectView(id, pinned, archived) })
}

// projectRefTables 는 project(id) 를 참조하거나 project 컬럼으로 프로젝트에 묶이는 표 중
// **삭제할 때 사람이 직접 다뤄야 하는 것**이다. **삭제 순서이기도 하다 — 자식부터 부모
// 순이다**, 그리고 이 말은 문자 그대로다: 목록의 i 번째 표를 지우는 시점에 그 뒤(i+1 이후)
// 어느 표에도 아직 이 프로젝트의 session 을 가리키는 행이 남아 있으면 안 된다 — session
// 삭제(6번째)가 그 행 때문에 FK 위반으로 막힌다.
//
// ★ landing_queue · claim · resource_hold · job 넷은 그래서 **session 보다 앞**에 있다.
// 넷 다 session_id 로 session(id) 를 CASCADE 없이 참조한다(schema.sql: landing_queue —
// 증분 003, claim·resource_hold·job — 기본 스키마). 처음엔 landing_queue 만 옮기고 나머지
// 셋은 session 뒤에 그대로 뒀었다 — 이 파일이 스스로 못박은 "자식부터 부모 순"을 스스로
// 어긴 상태였다. claim 이 특히 잘 걸린다: 항목을 한 번이라도 선점한 프로젝트면 claim 행이
// 항상 있어서 landing_queue 보다 훨씬 흔한 경로다.
//
// ★ claim 은 예전엔 이 목록에 아예 없었다("item CASCADE 로 함께 사라지니 뺐다"는 근거였다).
// **그 근거는 절반만 참이었다.** claim 의 (project, item_id) → item(project, id) FK 는
// 정말 ON DELETE CASCADE 라 item 을 지우면(9번째) 자동으로 함께 사라지지만, claim 이
// 따로 갖는 session_id → session(id) FK 는 별개이고 CASCADE 가 아니다. item 삭제보다
// session 삭제가 이 목록에서 훨씬 앞이므로, 그 시점엔 item 도 claim 도 아직 그대로다 —
// item CASCADE 하나로는 session 삭제를 못 지킨다. 그래서 claim 을 목록에 새로 넣었다
// (부수 효과: ProjectRefCounts 가 이제 선점 이력 행 수도 사람에게 보여준다 — 전에는
// 그 축이 안 보였다).
//
// item_after 는 여전히 이 목록에 없다. (project, item_id) 로 item(project, id) 만 보고
// session 을 안 본다 — item 삭제 CASCADE 하나로 충분하고, claim 과 달리 session 삭제
// 순서에 안 걸린다.
//
// ★ 뒤의 둘(item_dependents · pick_eval)은 FK 가 아니라 컬럼으로만 묶인다. FK 가 안 우니
// 안 지워도 삭제는 성공하고, 그래서 더 위험하다 — 조용히 고아 행이 남는다.
//
// ★ judgment 는 여기 있지만 **지우지 않는다**. judgment_no_delete 트리거가 원리적으로
// 막는다(schema.sql). 그래서 RemoveProject 는 판단이 하나라도 있으면 거절한다.
//
// ★ event 는 여기 없다. event.project 는 FK 가 아니라 그냥 컬럼이고(schema.sql 의 그 자리),
// 프로젝트가 사라져도 남는다 — 그것이 옳다. "이런 프로젝트가 있었고 언제 지워졌다"가
// 원장에 남는 유일한 길이다. (ProjectRefCounts 는 이 표를 별도 질의로 여전히 센다 —
// 안 세는 것이 아니라 삭제 순서 목록에 안 둘 뿐이다.)
//
// ★ 이 목록이 project 컬럼을 가진 표 전부를 실제로 덮는지는 사람이 손으로 대조하지
// 않는다 — TestProjectRefTablesCoverEveryProjectColumn(project_ref_counts_test.go)이
// 살아 있는 DB 의 스키마(sqlite_master + PRAGMA table_info)를 읽어 기계로 대조한다.
// landing_queue 가 이 목록에서 처음에 빠졌던 것도 schema.sql 을 정규식으로 읽는 방식으로는
// 못 잡았을 결함이다(그 표는 증분에만 있다) — 그 시험이 실제 DB 를 보는 이유가 그것이다.
var projectRefTables = []string{
	"session_workspace",
	"landing_queue",
	"claim",
	"resource_hold",
	"job",
	"session",
	"ref_state",
	"change_set",
	"item",
	"judgment",
	"snapshot",
	"counter",
	"item_dependents",
	"pick_eval",
}

// ProjectRefCounts 는 이 프로젝트에 묶인 행 수를 표별로 센다.
// 지우기 전에 무엇이 함께 갈지 보여주는 자리다.
//
// ★ "event" 와 "judgment_foreign" 은 projectRefTables 에 없는 **합성 키**다 — 표 이름이
// 아니라서 RemoveProject 의 삭제 루프(projectRefTables 를 그대로 도는)는 이 둘을 안 본다.
// event 는 지우지 않아서(위 주석), judgment_foreign 은 아예 이 프로젝트가 소유하지 않는
// 남의 판단이라 지울 권한이 없어서(아래 주석) 그렇다.
func (s *Store) ProjectRefCounts(ctx context.Context, id string) (map[string]int, error) {
	out := make(map[string]int, len(projectRefTables)+2)
	for _, tbl := range projectRefTables {
		var n int
		// ★ 표 이름은 위 projectRefTables 상수에서만 온다(외부 입력이 아니다) — 그래서
		// 문자열 결합으로 SQL 을 짓는 것이 안전하다. tbl 이 사용자 입력이었다면 이 자리가
		// SQL 인젝션 통로였을 것이다.
		if err := s.db.QueryRowContext(ctx,
			`SELECT count(*) FROM `+tbl+` WHERE project = ?`, id).Scan(&n); err != nil {
			return nil, fmt.Errorf("행 수 조회 실패(table=%s, project=%q): %w", tbl, clip(id, 64), err)
		}
		out[tbl] = n
	}
	var ev int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM event WHERE project = ?`, id).Scan(&ev); err != nil {
		return nil, fmt.Errorf("이벤트 수 조회 실패(project=%q): %w", clip(id, 64), err)
	}
	out["event"] = ev // 세기만 한다 — 안 지운다

	// ★ judgment.session_id 는 session(id) 를 CASCADE 없이 참조하고 judgment.project 와는
	// **독립 컬럼**이다(schema.sql:230-231) — 그래서 다른 프로젝트의 판단이 이 프로젝트의
	// 세션을 가리킬 수 있다. 실물 경로: service.NoteInput 은 Project 와 SessionID 를 각자
	// 받고(finish.go) 서로 같은 프로젝트인지 검증하지 않는다 — 세션은 맞게 골랐는데
	// --project 를 오타 내거나 다른 값으로 주면 이 모양의 행이 생긴다.
	//
	// 위 루프의 "judgment" 카운트(project = id)는 이 판단들을 못 잡는다 — project 컬럼이
	// 다르기 때문이다. 그런데 이 판단은 삭제 금지 트리거(judgment_no_delete)가 걸려 있어
	// RemoveProject 도 못 지운다 — 그 판단이 하나라도 남아 있으면 session 삭제(여섯 번째
	// 단계)가 FK 위반으로 죽는다. 그래서 별도 질의로 센다: 이 축을 안 세면
	// JudgeProjectRemoval 이 이 경로를 못 보고 통과시키고, 사람은 드라이버 원문 그대로의
	// FK 오류("FOREIGN KEY constraint failed")만 받는다 — 무엇이 막았는지 아무도 모른다.
	var foreignJudgment int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM judgment
		WHERE session_id IN (SELECT id FROM session WHERE project = ?)
		  AND (project IS NULL OR project <> ?)`, id, id).Scan(&foreignJudgment); err != nil {
		return nil, fmt.Errorf("교차 프로젝트 판단 수 조회 실패(project=%q): %w", clip(id, 64), err)
	}
	out["judgment_foreign"] = foreignJudgment
	return out, nil
}

// JudgeProjectRemoval 은 프로젝트를 지워도 되는지 판정한다. 순수 함수다.
//
// ★ 막는 축이 셋이고 성격이 둘로 갈린다.
//
//	⒜ 항목 — **정책**이다. 639항목짜리 프로젝트를 한 명령으로 날리는 길을 안 만든다.
//	   강제 플래그도 안 만든다: 한 번 만들면 다음 사람이 그것을 쓴다.
//	⒝ 이 프로젝트 자신의 판단 · ⒞ 다른 프로젝트가 이 프로젝트의 세션을 가리키는 판단 —
//	   **둘 다 원장이 정한 제약**이다. judgment_no_delete 트리거가 판단 삭제를 원리적으로
//	   막고, ⒝ 는 judgment.project 가, ⒞ 는 judgment.session_id → session(id) 가 각각
//	   FK 로 행을 붙잡는다(ProjectRefCounts 의 그 주석). 우회는 기각이다 —
//	   PRAGMA foreign_keys=OFF 도 트리거 드롭도 잔해 몇 건과 바꿀 값이 아니다.
//	   줄에서만 빼면 되는 경우라 화면의 보관이 그 자리를 받는다.
//
// ★ ⒞ 를 왜 pragma defer_foreign_keys(move.go 의 선례)로 안 미루는가: defer 는 **트랜잭션
// 끝까지 미룬 뒤에도 그 사이 부모·자식이 결국 같은 그림으로 맞춰지는** 경우에만 의미가
// 있다(move.go 는 item 을 옮기는 동안 FK 검사를 커밋 시점으로 미뤄서 "부모 먼저" 대
// "자식 먼저" 문제를 해소한다 — 끝나면 둘 다 새 프로젝트를 가리켜 앞뒤 상관없이 맞는다).
// 여기는 다르다: 남의 판단은 트리거 때문에 이 함수가 절대 못 건드리므로, 미뤄 봤자
// 커밋 시점에도 여전히 죽은 세션을 가리키는 채로 남는다 — 미루는 것은 실패를 뒤로
// 늦출 뿐 없애지 못한다. 그래서 여기서는 **미리 세어 거절**한다(RemoveProject 를 아예
// 안 부른다). 왜 "시도하고 잡아서 번역"(옵션 B) 을 1차 방어로 안 쓰는가: 그러면 매번
// DELETE 를 실제로 던져 봐야 알 수 있고, 사람은 --yes 를 준 뒤에야 거절을 본다 —
// 이 명령의 절반("무엇이 함께 지워질지 먼저 보여준다")이 --yes 없이도 성립해야 하는데
// 시도-후-번역은 그 절반을 지키지 못한다. 다만 2차 방어로는 남겨 둔다(RemoveProject 의
// 그 주석) — 판정과 삭제 사이의 레이스까지 사람이 읽을 사유를 받게 하기 위해서다.
func JudgeProjectRemoval(counts map[string]int) (bool, string) {
	if n := counts["item"]; n > 0 {
		return false, fmt.Sprintf("큐 항목이 %d건 있다 — 항목이 있는 프로젝트는 지우지 않는다. "+
			"줄에서만 빼려면 대시보드에서 보관하라", n)
	}
	if n := counts["judgment"]; n > 0 {
		return false, fmt.Sprintf("판단이 %d건 있다 — 판단은 원장이라 삭제 금지 트리거가 있고, "+
			"그것이 이 프로젝트 행을 붙잡는다(FK). 줄에서만 빼려면 대시보드에서 보관하라", n)
	}
	if n := counts["judgment_foreign"]; n > 0 {
		return false, fmt.Sprintf("다른 프로젝트의 판단이 %d건, 이 프로젝트의 세션을 가리키고 "+
			"있다 — judgment.session_id 는 session(id) 를 FK 로 참조하고 judgment.project 와는 "+
			"독립이라 이 프로젝트의 판단 수만으로는 안 잡힌다. 그 판단도 삭제 금지 트리거가 "+
			"걸려 있어 이쪽에서 지울 수 없고, 그대로 두면 이 프로젝트의 세션을 못 지운다(FK). "+
			"줄에서만 빼려면 대시보드에서 보관하라", n)
	}
	return true, "지울 수 있다 — 항목도 판단도 없다"
}

// RemoveProject 는 프로젝트와 거기 묶인 행 전부를 지운다.
//
// ★ 판정은 부르는 쪽이 한다(JudgeProjectRemoval). 여기서 다시 세면 판정이 두 벌이 되고,
// 두 벌은 반드시 표류한다.
//
// ★ event 는 안 지운다 — projectRefTables 의 그 주석대로다.
func (s *Store) RemoveProject(ctx context.Context, id string) error {
	return s.Tx(ctx, func(t *Tx) error {
		// 지운다는 사실을 **먼저** 예약한다. 예약 이벤트는 롤백 뒤에도 흘러서
		// 아래가 통째로 죽어도 "무엇을 지우려 했나"가 원장에 남는다.
		t.LogEvent("project.remove", id, "", map[string]any{"project": clip(id, 64)})

		for _, tbl := range projectRefTables {
			if tbl == "judgment" {
				// 판단은 트리거가 막는다. 여기 오면 판정이 먼저 거절했어야 한다 —
				// 0건이면 DELETE 가 무해하므로 그대로 두면 조용히 통과하고,
				// 0건이 아니면 트리거가 정확한 말로 죽인다. 건너뛰지 않는 이유가 그것이다.
				continue
			}
			if _, err := t.tx.ExecContext(t.ctx,
				`DELETE FROM `+tbl+` WHERE project = ?`, id); err != nil {
				if ferr := removalFKMessage(tbl, id, err); ferr != nil {
					return ferr
				}
				return fmt.Errorf("행 삭제 실패(table=%s, project=%q): %w", tbl, clip(id, 64), err)
			}
		}
		res, err := t.tx.ExecContext(t.ctx, `DELETE FROM project WHERE id = ?`, id)
		if err != nil {
			if ferr := removalFKMessage("project", id, err); ferr != nil {
				return ferr
			}
			return fmt.Errorf("프로젝트 삭제 실패(id=%q): %w", clip(id, 64), err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("프로젝트 삭제 결과 확인 실패(id=%q): %w", clip(id, 64), err)
		}
		if n == 0 {
			// ★ fmt.Errorf(...ErrNotFound...) 로 직접 감싸지 않는다 — SetProjectView 의 그
			// 주석과 같은 이유다. notFound 로 보내야 internal/api 의 errors.As(*NotFoundError)
			// 가 좌표·처방을 붙일 수 있다.
			return notFound(NFProject, "", id)
		}
		return nil
	})
}

// removalFKMessage 는 RemoveProject 의 **2차 방어**다. 삭제 도중 FK 위반이 나면 드라이버
// 원문 대신 사람이 읽을 사유를 낸다. FK 위반이 아니면 nil 이다(호출부가 원래 오류를 그대로
// fmt.Errorf 로 감싼다).
//
// ★ 1차 방어는 JudgeProjectRemoval 이다(judgment·judgment_foreign 카운트로 미리 거절) —
// 정상 경로는 여기 절대 안 온다. 이 함수가 잡는 것은 **판정과 삭제 사이의 레이스**뿐이다:
// ProjectRefCounts 로 센 시점과 이 DELETE 사이에 다른 세션이 이 프로젝트의 세션을 가리키는
// 새 판단을 남기면(극히 드묾 — 사람이 --yes 를 눌러 부르는 단발 명령이라 창이 밀리초 단위다)
// 여기로 온다. 그때도 "FOREIGN KEY constraint failed" 원문만 올리면 무엇이 막았는지
// 아무도 모른다 — 그래서 한 번 더 번역한다.
func removalFKMessage(tbl, id string, err error) error {
	if JudgeConstraintCode(ConstraintCode(err)).Kind != ConflictMissingRef {
		return nil
	}
	return fmt.Errorf(
		"표 %s 를 지우는 중 참조 무결성 위반이 났다 — 세었을 때는 없던 참조가 그 사이 생겼을 "+
			"수 있다(대개 다른 프로젝트의 판단이 이 프로젝트의 세션을 새로 가리켰다). "+
			"`fd project rm` 을 다시 실행해 사유를 다시 확인하라(table=%s, project=%q): %w",
		tbl, tbl, clip(id, 64), err)
}

// nullTime 은 제로값을 NULL 로 낸다.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return fmtTime(t)
}

// ─────────────────────────────────────────────────────────────────────────────
// machine
// ─────────────────────────────────────────────────────────────────────────────

// UpsertMachine 은 머신을 등록하고 last_seen 을 갱신한다.
//
// first_seen 은 보존한다 — 그 값이 "이 머신을 언제부터 봤나"이고,
// 갱신해 버리면 last_seen 과 같은 값이 되어 컬럼 하나가 통째로 의미를 잃는다.
func (t *Tx) UpsertMachine(m model.Machine) error {
	if m.ID == "" {
		return errors.New("머신 id 가 비었다")
	}
	now := m.LastSeen
	if now.IsZero() {
		now = nowStamp()
	}
	first := m.FirstSeen
	if first.IsZero() {
		first = now
	}
	_, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO machine(id, hostname, first_seen, last_seen)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  hostname  = excluded.hostname,
		  last_seen = excluded.last_seen`,
		m.ID, m.Hostname, fmtTime(first), fmtTime(now))
	if err != nil {
		return fmt.Errorf("머신 upsert 실패(id=%q): %w", clip(m.ID, 64), err)
	}
	return nil
}

// UpsertMachine 은 단발 트랜잭션으로 감싼 것이다.
func (s *Store) UpsertMachine(ctx context.Context, m model.Machine) error {
	return s.Tx(ctx, func(t *Tx) error { return t.UpsertMachine(m) })
}

// GetMachine 은 머신 하나를 읽는다.
func (s *Store) GetMachine(ctx context.Context, id string) (model.Machine, error) {
	var m model.Machine
	var first, last string
	err := s.db.QueryRowContext(ctx,
		`SELECT `+machineCols+` FROM machine WHERE id = ?`, id).
		Scan(&m.ID, &m.Hostname, &first, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return m, notFound(NFMachine, "", id)
	}
	if err != nil {
		return m, fmt.Errorf("머신 조회 실패(id=%q): %w", clip(id, 64), err)
	}
	if m.FirstSeen, err = parseTime(first); err != nil {
		return m, err
	}
	if m.LastSeen, err = parseTime(last); err != nil {
		return m, err
	}
	return m, nil
}
