package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/kweiza/flightdeck/internal/model"
)

// J 계층 — 판단·링크·전문 검색·스냅숏. 사람이 쓰는 유일한 것이고 **추가 전용**이다.
//
// ★ 이 파일에 UpdateJudgment·DeleteJudgment 가 없다. 스키마의 BEFORE UPDATE/DELETE 트리거가
// 물리적으로 막지만, 애초에 **호출부가 없어야** 한다 — 트리거를 우회할 코드가 존재하지 않으면
// 우회할 것도 없다. 정정은 새 행 + Supersedes 다.
// 기존 게시판은 같은 파일을 두 세션이 쓸 수 있어 앞 세션의 절이 통째로 덮였고,
// 저장소가 버전관리 밖이라 원문이 영구 소실됐다. 두 번 났다.

// ─────────────────────────────────────────────────────────────────────────────
// judgment
// ─────────────────────────────────────────────────────────────────────────────

// LinkTargetKinds 는 판단 링크가 가리킬 수 있는 것의 전부다.
//
// ★ **schema.sql 의 `judgment_link.target_kind` CHECK 열거와 같아야 한다.** 두 벌이 갈리면
// 한쪽은 통과시키고 다른 쪽은 CHECK 로 죽이는데, 그 죽음은 트랜잭션 안이라 **함께 있던 판단이
// 롤백된다.** 그 일치를 사람이 지키게 두지 않는다 — internal/store 의 시험이 schema.sql 을
// 읽어 이 슬라이스와 대조한다(TestLinkTargetKindsMatchSchemaCheck).
var LinkTargetKinds = []string{"item", "job", "commit", "session"}

// ValidateLink 는 판단 링크 하나가 저장 가능한지다. 순수 함수다.
//
// ★ **이 함수가 존재하는 이유는 자리다.** 열거 위반은 지금까지 `judgment_link` 의 CHECK 가
// 잡았고, 그것은 트랜잭션 **안**이다. `Finish` 는 판단·후속·종료·반납을 한 tx 로 묶으므로
// 거기서 오류가 되면 **원리적으로 파생 불가한 판단이 함께 사라진다.** 그래서 같은 판정을
// 순수 함수로 꺼내 호출부가 **tx 진입 전에** 부를 수 있게 한다(service.Finish 의 전단 관문).
// CHECK 는 그대로 최후 방어로 남는다 — 이 함수를 안 부르는 경로가 생겨도 DB 는 지킨다.
func ValidateLink(l model.JudgmentLink) error {
	if strings.TrimSpace(l.TargetKind) == "" || strings.TrimSpace(l.TargetID) == "" {
		return fmt.Errorf("판단 링크가 비었다(kind=%q id=%q)",
			clip(l.TargetKind, 32), clip(l.TargetID, 64))
	}
	for _, k := range LinkTargetKinds {
		if l.TargetKind == k {
			return nil
		}
	}
	return fmt.Errorf("판단 링크의 target_kind %q 는 열거 밖이다 — %s 중 하나여야 한다",
		clip(l.TargetKind, 32), strings.Join(LinkTargetKinds, "·"))
}

// AddJudgment 는 판단 하나와 그 링크를 저장한다. ID 가 비어 있으면 발급한다.
//
// 반환은 저장된 판단이다(발급된 ID 를 호출부가 알아야 링크를 걸 수 있다).
func (t *Tx) AddJudgment(j model.Judgment) (model.Judgment, error) {
	if strings.TrimSpace(j.Body) == "" {
		// 스키마 CHECK(body <> '') 가 최후 방어이지 1차 방어가 아니다.
		// 공백만 든 본문은 CHECK 를 통과하는데, 그건 판단이 아니다.
		return j, errors.New("판단 본문이 비었다 — 무엇을 왜 그렇게 했는지가 이 표의 존재 이유다")
	}
	if j.ID == "" {
		j.ID = NewID()
	}
	if j.At.IsZero() {
		j.At = nowStamp()
	}
	if _, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO judgment(id, project, session_id, at, kind, title, body, supersedes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, nullStr(j.Project), nullStr(j.SessionID), fmtTime(j.At),
		string(j.Kind), nullStr(j.Title), j.Body, nullStr(j.Supersedes)); err != nil {
		// ★ 등록 안 된 프로젝트로 판단을 쓰면 여기서 FK 위반이 난다. 항목 id 중복과 **같은 형태**다 —
		//   호출자가 고칠 거리인데 500 으로 나가면 "서버가 고장났다"로 읽힌다.
		return j, writeErr(err, writeTarget{
			Target: TargetJudgment, Project: j.Project, ID: j.ID,
			RefHint: fmt.Sprintf("프로젝트 %s · 세션 %s · supersedes %s",
				clip(j.Project, 64), clip(j.SessionID, 64), clip(j.Supersedes, 64)),
		}, "판단 저장 실패(id=%q kind=%q session=%q)",
			clip(j.ID, 64), clip(string(j.Kind), 32), clip(j.SessionID, 64))
	}
	for _, l := range j.Links {
		if err := ValidateLink(l); err != nil {
			return j, err
		}
		// ★ 빈 TargetProject 는 NULL 로 들어간다 — 빈 문자열과 NULL 이 갈리면
		//   COALESCE 가 빈 문자열을 "값이 있다"로 읽어 어느 프로젝트와도 안 맞는
		//   링크가 된다. 그게 정확히 이 컬럼이 없애려던 죽은 링크의 모양이다.
		if _, err := t.tx.ExecContext(t.ctx,
			`INSERT INTO judgment_link(judgment_id, target_kind, target_id, target_project)
			 VALUES (?, ?, ?, ?)`,
			j.ID, l.TargetKind, l.TargetID, nullStr(l.TargetProject)); err != nil {
			return j, writeErr(err, writeTarget{
				Target: TargetJudgmentLink, Project: j.Project, ID: j.ID,
				RefHint: fmt.Sprintf("판단 %s", clip(j.ID, 64)),
			}, "판단 링크 저장 실패(judgment=%q target=%s/%s)",
				clip(j.ID, 64), clip(l.TargetKind, 32), clip(l.TargetID, 64))
		}
	}
	return j, nil
}

// AddJudgment 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) AddJudgment(ctx context.Context, j model.Judgment) (model.Judgment, error) {
	var out model.Judgment
	err := s.Tx(ctx, func(t *Tx) error {
		var e error
		out, e = t.AddJudgment(j)
		return e
	})
	return out, err
}

const judgmentCols = `id, project, session_id, at, kind, title, body, supersedes`

func scanJudgment(sc interface{ Scan(...any) error }) (model.Judgment, error) {
	var j model.Judgment
	var project, session, title, supersedes sql.NullString
	var at, kind string
	if err := sc.Scan(&j.ID, &project, &session, &at, &kind, &title, &j.Body, &supersedes); err != nil {
		return j, err
	}
	j.Project, j.SessionID, j.Title, j.Supersedes = str(project), str(session), str(title), str(supersedes)
	j.Kind = model.JudgmentKind(kind)
	var err error
	if j.At, err = parseTime(at); err != nil {
		return j, err
	}
	return j, nil
}

func linksOf(ctx context.Context, q dbtx, judgmentID string) ([]model.JudgmentLink, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT target_kind, target_id, target_project FROM judgment_link
		  WHERE judgment_id = ? ORDER BY target_kind, target_id`,
		judgmentID)
	if err != nil {
		return nil, fmt.Errorf("판단 링크 조회 실패(judgment=%q): %w", clip(judgmentID, 64), err)
	}
	defer rows.Close()

	var out []model.JudgmentLink
	for rows.Next() {
		var l model.JudgmentLink
		var project sql.NullString
		if err := rows.Scan(&l.TargetKind, &l.TargetID, &project); err != nil {
			return nil, fmt.Errorf("판단 링크 행 해석 실패: %w", err)
		}
		l.TargetProject = str(project)
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("판단 링크 순회 실패: %w", err)
	}
	return out, nil
}

// GetJudgment 는 판단 하나를 링크째로 읽는다.
func (s *Store) GetJudgment(ctx context.Context, id string) (model.Judgment, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+judgmentCols+` FROM judgment WHERE id = ?`, id)
	j, err := scanJudgment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return j, notFound(NFJudgment, "", id)
	}
	if err != nil {
		return j, fmt.Errorf("판단 조회 실패(id=%q): %w", clip(id, 64), err)
	}
	if j.Links, err = linksOf(ctx, s.db, id); err != nil {
		return j, err
	}
	return j, nil
}

// ListJudgmentsBySession 은 한 세션이 남긴 판단을 시간순으로 낸다.
func (s *Store) ListJudgmentsBySession(ctx context.Context, sessionID string) ([]model.Judgment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+judgmentCols+` FROM judgment WHERE session_id = ? ORDER BY at, id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("세션 판단 조회 실패(session_id=%q): %w", clip(sessionID, 64), err)
	}
	out, err := collectJudgments(rows)
	if err != nil {
		return nil, err
	}
	return s.fillLinks(ctx, out)
}

// ListJudgmentsByKind 는 프로젝트의 특정 종류 판단을 최신순으로 낸다.
func (s *Store) ListJudgmentsByKind(ctx context.Context, project string, kind model.JudgmentKind, limit int) ([]model.Judgment, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+judgmentCols+` FROM judgment WHERE project = ? AND kind = ? ORDER BY at DESC, id DESC LIMIT ?`,
		project, string(kind), limit)
	if err != nil {
		return nil, fmt.Errorf("판단 종류별 조회 실패(project=%q kind=%q): %w",
			clip(project, 64), clip(string(kind), 32), err)
	}
	out, err := collectJudgments(rows)
	if err != nil {
		return nil, err
	}
	return s.fillLinks(ctx, out)
}

// SupersededBy 는 이 프로젝트에서 **정정당한 판단 id → 그것을 대체한 판단 id** 다.
//
// 원장은 추가 전용이라(위 트리거) 옛 행을 지우거나 표시를 켜는 경로가 없다.
// "이것은 정정됐다"는 사실은 **새 행이 거는 역참조로만** 읽힌다 — 그래서 이 질의가
// 필요하다. 옛 행 자체는 아무것도 모른다.
//
// ★ 있음/없음이 아니라 **값**을 내는 이유: 화면이 "정정됐다"까지만 말하면 반쪽이다.
// 어디로 가야 정정문을 읽는지가 없으면 사람이 옛 행을 그대로 믿거나, 대체한 행을
// 찾느라 전체를 훑는다. 거르기만 하는 호출부(service)는 존재 검사로 쓰면 된다.
//
// ★ 거르는 것은 호출부(service)의 판정이다. ListJudgmentsByKind 를 여기서 좁히지 않는
// 이유가 그것이다 — 백업(legacy/export.go)은 정정된 행까지 전수로 가져가야 한다.
//
// 한 행을 여럿이 대체하면(스키마가 막지 않는다) **가장 나중 것이 이긴다** — 정렬이
// 그래서 붙어 있다. 정정의 정정이 났을 때 화면이 첫 정정으로 보내면 한 칸 뒤처진다.
func (s *Store) SupersededBy(ctx context.Context, project string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT supersedes, id FROM judgment WHERE project = ? AND supersedes IS NOT NULL
		 ORDER BY at ASC, id ASC`,
		project)
	if err != nil {
		return nil, fmt.Errorf("정정 대상 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var old, cur string
		if err := rows.Scan(&old, &cur); err != nil {
			return nil, fmt.Errorf("정정 대상 행 해석 실패: %w", err)
		}
		out[old] = cur
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("정정 대상 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	return out, nil
}

func collectJudgments(rows *sql.Rows) ([]model.Judgment, error) {
	defer rows.Close()
	var out []model.Judgment
	for rows.Next() {
		j, err := scanJudgment(rows)
		if err != nil {
			return nil, fmt.Errorf("판단 행 해석 실패: %w", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("판단 목록 순회 실패: %w", err)
	}
	return out, nil
}

func (s *Store) fillLinks(ctx context.Context, js []model.Judgment) ([]model.Judgment, error) {
	for i := range js {
		l, err := linksOf(ctx, s.db, js[i].ID)
		if err != nil {
			return nil, err
		}
		js[i].Links = l
	}
	return js, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 전문 검색
// ─────────────────────────────────────────────────────────────────────────────

// FTSQuery 는 사람이 친 검색어를 FTS5 가 받는 안전한 질의로 바꾼다. 순수 함수다.
//
// ★ 왜 필요한가: FTS5 의 MATCH 는 자체 문법이 있어서 `-`·`"`·`*`·`(`·`:` 같은 문자가
// 들어오면 **구문 오류로 죽는다**. 사용자가 친 문자열을 그대로 넘기면
// "결과 없음"과 "질의가 깨져서 못 돌았음"이 같은 빈 목록으로 접힌다.
// 그래서 토큰마다 큰따옴표로 감싸 전부 리터럴로 만든다(내부 `"` 는 겹쳐 이스케이프).
// 토큰 사이는 FTS5 의 기본 결합(AND)에 맡긴다.
//
// 잃는 것: 사용자가 OR·NEAR·접두 검색 문법을 직접 쓸 수 없다.
// 그것이 필요해지면 **인자를 하나 늘려** 원문 통과 경로를 여는 것이 맞다 —
// 지금 문법을 반쯤 허용하면 어느 문자가 살아 있는지 아무도 모르는 상태가 된다.
func FTSQuery(raw string) string {
	var toks []string
	for _, f := range strings.Fields(raw) {
		toks = append(toks, `"`+strings.ReplaceAll(f, `"`, `""`)+`"`)
	}
	return strings.Join(toks, " ")
}

// SearchJudgments 는 판단을 전문 검색한다.
//
// project 가 비어 있으면 전 프로젝트를 본다. 결과는 FTS5 의 rank 순(관련도 높은 순)이다.
// 판단이 쌓이면 grep 이 유일한 도달 경로가 되는 것을 막는 자리다.
func (s *Store) SearchJudgments(ctx context.Context, project, query string, limit int) ([]model.Judgment, error) {
	q := FTSQuery(query)
	if q == "" {
		// 빈 질의를 MATCH 에 넘기면 구문 오류가 난다. 빈 결과와 구분되는 오류로 거절한다.
		return nil, fmt.Errorf("검색어가 비었다(원문 %q)", clip(query, 64))
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT j.id, j.project, j.session_id, j.at, j.kind, j.title, j.body, j.supersedes
		FROM judgment_fts f
		JOIN judgment j ON j.rowid = f.rowid
		WHERE judgment_fts MATCH ? AND (? = '' OR j.project = ?)
		ORDER BY rank
		LIMIT ?`, q, project, project, limit)
	if err != nil {
		// 원인 전문을 담는다 — 변환된 질의까지 실어야 "무엇이 틀렸나"에 답이 된다.
		return nil, fmt.Errorf("판단 검색 실패(원문=%q 변환=%q project=%q): %w",
			clip(query, 64), clip(q, 128), clip(project, 64), err)
	}
	out, err := collectJudgments(rows)
	if err != nil {
		return nil, err
	}
	return s.fillLinks(ctx, out)
}

// ─────────────────────────────────────────────────────────────────────────────
// snapshot
// ─────────────────────────────────────────────────────────────────────────────

// ValidateSnapshot 은 스냅숏이 저장 가능한지 판정한다. 순수 함수다.
//
// ★ method='manual' 인데 근거가 없으면 **호출 전에** 거절한다.
// 스키마 CHECK 가 최후 방어이지 1차 방어가 아니다 — DB 제약 위반 문구는
// "무엇이 왜 빠졌나"를 말하지 않고, 그러면 호출부가 사용자에게 옮길 말이 없다.
// 규율이 제약이 되는 자리다: "손으로 올리면 근거 없는 숫자가 되고, 그 순간 이 표를 아무도 못 믿는다."
func ValidateSnapshot(s model.Snapshot) error {
	switch {
	case s.Project == "":
		return errors.New("스냅숏의 project 가 비었다")
	case s.Key == "":
		return errors.New("스냅숏의 key 가 비었다")
	case s.Method != model.SnapshotCommand && s.Method != model.SnapshotManual:
		return fmt.Errorf("스냅숏 method 는 command 또는 manual 이어야 한다(받은 값 %q)",
			clip(string(s.Method), 32))
	case s.Method == model.SnapshotManual && strings.TrimSpace(s.Evidence) == "":
		return fmt.Errorf("스냅숏 %q 는 method=manual 인데 근거(evidence)가 없다 "+
			"— 손으로 올린 숫자에 근거가 없으면 그 순간 이 표를 아무도 못 믿는다", clip(s.Key, 64))
	default:
		return nil
	}
}

// PutSnapshot 은 스냅숏을 저장한다(같은 키면 덮는다 — 파생이 아니라 재계산 결과이므로).
func (t *Tx) PutSnapshot(s model.Snapshot) error {
	if err := ValidateSnapshot(s); err != nil {
		return err
	}
	if s.ComputedAt.IsZero() {
		s.ComputedAt = nowStamp()
	}
	if _, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO snapshot(project, key, value, method, evidence, input_digest, computed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project, key) DO UPDATE SET
		  value = excluded.value, method = excluded.method, evidence = excluded.evidence,
		  input_digest = excluded.input_digest, computed_at = excluded.computed_at`,
		s.Project, s.Key, s.Value, string(s.Method),
		nullStr(s.Evidence), nullStr(s.InputDigest), fmtTime(s.ComputedAt)); err != nil {
		return writeErr(err, writeTarget{
			Target: TargetSnapshot, Project: s.Project, ID: s.Key,
			RefHint: "프로젝트 " + clip(s.Project, 64),
		}, "스냅숏 저장 실패(project=%q key=%q method=%q)",
			clip(s.Project, 64), clip(s.Key, 64), clip(string(s.Method), 32))
	}
	return nil
}

// PutSnapshot 은 단발 트랜잭션으로 감싼 것이다.
func (s *Store) PutSnapshot(ctx context.Context, sn model.Snapshot) error {
	return s.Tx(ctx, func(t *Tx) error { return t.PutSnapshot(sn) })
}

// ─────────────────────────────────────────────────────────────────────────────
// 항목별 판단 링크
// ─────────────────────────────────────────────────────────────────────────────

// JudgmentLinksForItems 는 항목 id 들에 걸린 판단 id 를 한 번에 읽는다.
//
// ★ judgment_link_by_target 인덱스(schema.sql:261)는 처음부터 있었고 접근자만 없었다.
// 그래서 service/pick.go 가 종류 9개를 훑어 링크로 거르는 방식으로 우회했는데,
// 묶음 N건이면 그것이 N×9 질의가 된다.
//
// 링크가 없는 항목은 **키를 안 만든다.** 빈 슬라이스를 넣으면
// "이 항목에 판단이 없다"와 "이 항목을 안 봤다"가 같은 값이 된다.
//
// 입력이 비면 DB 를 건드리지 않고 바로 돌아간다 — 결과가 애초에 빈 맵일 게 뻔한데
// 왕복을 한 번 더 할 이유가 없어서다. (이전에는 "IN () 가 SQLite 구문 오류를 낸다"고
// 여기 적어 뒀는데 틀린 주장이었다 — 실측하니 이 저장소가 쓰는 modernc.org/sqlite 는
// `x IN ()` 을 오류 없이 "항상 거짓"으로 받아들인다. 엔진이 뭘 허용하는지에 기대는 이유가
// 아니라는 뜻이라, 다음 사람이 또 확인하지 않도록 여기 사실대로 남긴다.)
func (s *Store) JudgmentLinksForItems(ctx context.Context, project string, itemIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(itemIDs) == 0 {
		return out, nil // 결과가 뻔히 빈 맵일 왕복을 생략한다 — 엔진의 IN () 처리에 기대지 않는다
	}
	ph := make([]string, len(itemIDs))
	args := make([]any, 0, len(itemIDs)+1)
	args = append(args, project)
	for i, id := range itemIDs {
		ph[i] = "?"
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT jl.target_id, jl.judgment_id
		   FROM judgment_link jl JOIN judgment j ON j.id = jl.judgment_id
		  WHERE j.project = ? AND jl.target_kind = 'item'
		    AND jl.target_id IN (`+strings.Join(ph, ",")+`)
		  ORDER BY jl.target_id, jl.judgment_id`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("항목별 판단 링크 조회 실패(project=%q, 항목 %d건): %w",
			clip(project, 64), len(itemIDs), err)
	}
	defer rows.Close()
	for rows.Next() {
		var item, jid string
		if err := rows.Scan(&item, &jid); err != nil {
			return nil, fmt.Errorf("판단 링크 행 해석 실패: %w", err)
		}
		out[item] = append(out[item], jid) // ORDER BY 가 사전순을 보장한다
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("판단 링크 순회 실패: %w", err)
	}
	return out, nil
}

// JudgmentsForItem 은 항목 하나에 걸린 판단 **전문**이다. 최신 먼저, 동점이면 id 역순.
//
// service/pick.go 의 linkedJudgments 가 종류 9개를 훑던 자리를 대신한다.
func (s *Store) JudgmentsForItem(ctx context.Context, project, itemID string) ([]model.Judgment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+judgmentCols+`
		   FROM judgment j JOIN judgment_link jl ON jl.judgment_id = j.id
		  WHERE COALESCE(jl.target_project, j.project) = ?
		    AND jl.target_kind = 'item' AND jl.target_id = ?
		  ORDER BY j.at DESC, j.id DESC`,
		project, itemID)
	if err != nil {
		return nil, fmt.Errorf("항목의 판단 조회 실패(project=%q item=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	js, err := collectJudgments(rows)
	if err != nil {
		return nil, err
	}
	return s.fillLinks(ctx, js)
}

// GetSnapshot 은 스냅숏 하나를 읽는다.
// "낡음" 판정은 여기서 하지 않는다 — input_digest 를 현재 트리와 대조하는 것은
// git 을 읽는 계층의 몫이고, 저장 계층이 그 판정을 흉내 내면 두 벌이 된다.
//
// GetJudgment 와 같은 형태(SELECT snapshotCols + scanSnapshot)다 — 컬럼 목록과
// Scan 순서를 이 함수가 따로 손으로 적으면 snapshotCols 옆에 같은 목록이 또 생기고,
// 그 순간 이 태스크가 없애려던 이중화가 store 파일 **안에서** 되살아난다.
func (s *Store) GetSnapshot(ctx context.Context, project, key string) (model.Snapshot, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+snapshotCols+` FROM snapshot WHERE project = ? AND key = ?`, project, key)
	sn, err := scanSnapshot(row)
	if errors.Is(err, sql.ErrNoRows) {
		return sn, notFound(NFSnapshot, project, key)
	}
	if err != nil {
		return sn, fmt.Errorf("스냅숏 조회 실패(project=%q key=%q): %w",
			clip(project, 64), clip(key, 64), err)
	}
	return sn, nil
}

// snapshotCols 는 스냅숏 조회의 컬럼 목록이다.
//
// judgmentCols 와 같은 이유로 상수다 — 목록을 손으로 다시 적으면 순서가 어긋나는 순간
// Scan 이 조용히 엉뚱한 값을 채운다(전부 문자열이라 타입 오류도 안 난다).
const snapshotCols = `project, key, value, method, evidence, input_digest, computed_at`

// ListSnapshots 는 프로젝트의 스냅숏 전부를 키 순으로 낸다.
//
// 수는 사람이 넣은 만큼이라 페이징이 없다. 없는 프로젝트는 오류가 아니라 빈 목록이다 —
// GetSnapshot 과 달리 "아직 없다"와 "그런 프로젝트가 없다"를 가를 필요가 없다.
func (s *Store) ListSnapshots(ctx context.Context, project string) ([]model.Snapshot, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+snapshotCols+` FROM snapshot WHERE project = ? ORDER BY key`, project)
	if err != nil {
		return nil, fmt.Errorf("스냅숏 목록 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	defer rows.Close()

	var out []model.Snapshot
	for rows.Next() {
		sn, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("스냅숏 목록 순회 실패: %w", err)
	}
	return out, nil
}

// scanSnapshot 은 snapshotCols 순서의 한 행을 읽는다.
// scanJudgment 과 같은 면(*sql.Row 와 *sql.Rows 둘 다)을 받는다.
func scanSnapshot(sc interface{ Scan(...any) error }) (model.Snapshot, error) {
	var sn model.Snapshot
	var evidence, digest sql.NullString
	var method, at string
	if err := sc.Scan(&sn.Project, &sn.Key, &sn.Value, &method, &evidence, &digest, &at); err != nil {
		return sn, fmt.Errorf("스냅숏 행 해석 실패: %w", err)
	}
	sn.Method = model.SnapshotMethod(method)
	sn.Evidence, sn.InputDigest = str(evidence), str(digest)
	var err error
	if sn.ComputedAt, err = parseTime(at); err != nil {
		return sn, err
	}
	return sn, nil
}
