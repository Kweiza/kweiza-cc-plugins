package store

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 010 은 item_dependents 를 통째로 비우고 **item_after 는 한 행도 안 건드린다.**
//
// ★ 지우는 시험에서 어려운 것은 "지웠다" 가 아니라 **"안 지울 것을 안 지웠다"** 다
// (005 시험의 그 규율). 여기서 살릴 것은 같은 표의 다른 행이 아니라 **다른 표**다 —
// item_after 가 정본이고, 010 의 되돌리기 근거 전부가 "그 표가 그대로 있다"에 걸려 있다.
// 그래서 이 시험은 item_after 를 증분 전후로 통째로 대조한다.
//
// ★ 전제를 **결과를 읽기 전에** 단정한다. 옛 상태가 실제로 안 만들어졌으면 아래 단정은
// 아무것도 안 지킨다.
func TestMigration010EmptiesItemDependentsAndKeepsItemAfter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	// ★ 9판에서 출발한다 — 11판에는 item_dependents 가 없다(증분 011). 그리고 9판 DB 는
	//   Open 이 거절하므로(적용 분리 뒤의 규약) 관문 없는 openRaw 로 연다. 이 시험의 목적이
	//   **중간 판을 보는 것**이라 관문이 목적에 맞지 않는다.
	mustMigrateTo(t, path, 9)
	s, err := openRaw(path, log)
	if err != nil {
		t.Fatalf("열기 실패: %v", err)
	}
	seed(t, s, "P")
	mustItem(t, s, "P", "dep")
	mustItem(t, s, "P", "waiter-open")
	mustItem(t, s, "P", "waiter-done")
	mustItem(t, s, "P", "drained") // 간선이 하나도 없는 항목 — n=0 행의 주인이다

	// 간선 둘을 건다. 하나는 열린 항목, 하나는 닫힌 항목에서 온다 —
	// 그 비대칭이 "표가 답하던 질문"과 "읽는 쪽이 묻던 질문"을 갈라놓는 그 지점이다.
	// ★ **AddAfter 를 안 쓴다 — 손으로 심는다.** 이 시험은 스키마 9판 DB 를 여는데,
	//   그 판에는 dep_project 칼럼이 없다(증분 015 가 만든다). AddAfter 는 **지금 코드**라
	//   그 칼럼에 쓰고, 9판 DB 에서는 "no such column" 으로 죽는다.
	//
	//   운영에서는 그 조합이 불가능하다 — Open 이 판 불일치를 거절한다. 이 시험만
	//   openRaw 로 그 관문을 우회하므로(위 ★), 옛 판의 모양은 **그 판의 SQL 로** 심는
	//   것이 맞다. 여기서 AddAfter 를 쓰면 이 픽스처가 앞으로 증분마다 깨진다.
	for _, w := range []string{"waiter-open", "waiter-done"} {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO item_after(project, item_id, dep_item) VALUES (?, ?, ?)`,
			"P", w, "dep"); err != nil {
			t.Fatalf("선행 등록 실패(%s): %v", w, err)
		}
	}
	if err := s.SetItemState(ctx, "P", "waiter-done", model.ItemDone, "끝"); err != nil {
		t.Fatalf("항목 종료 실패: %v", err)
	}

	// ── 옛 상태(증분 010 직전)를 재현한다 ──
	// 쓰기 셋은 이미 걷혔으므로 생성 경로가 없다. 손으로 심되 **실측이 말한 모양**으로 심는다:
	// n 은 살아 있는 종속 수가 아니라 **간선 수**다(2026-08-22 전수 143행, 예외 0).
	//
	// ★ n=0 행도 함께 심는다. 운영 원장에 실제로 둘 있었고(143행 중 2행), 그 둘이 아래 ③의
	// **한계를 드러내는 유일한 갈래**다 — 되돌리기 질의는 간선에서 GROUP BY 하므로 간선이
	// 0인 행은 만들 수 없다. 안 심으면 ③이 "전부 복원된다"로 읽히는데 그것은 거짓이다.
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO item_dependents(project, item_id, n)
		VALUES ('P', 'dep', 2), ('P', 'drained', 0)`); err != nil {
		t.Fatalf("옛 상태 심기 실패: %v", err)
	}

	// 그 갈림을 여기서 못박는다 — 이것이 표를 죽인 이유고, 되돌리기 질의가 무엇을 되살리는지의 정의다.
	//
	// ★ **Store.Dependents 를 안 쓴다 — 그 판의 SQL 로 센다.** 이 DB 는 곧 9판으로
	//   되돌려지는데(아래 prev), 지금 코드의 그 함수는 dep_project 를 읽는다(증분 015).
	//   9판에는 그 칼럼이 없어 "no such column" 으로 죽는다. 위 선행 심기가 같은 이유로
	//   손 SQL 인 것과 같은 판정이다: 옛 판의 모양은 그 판의 SQL 로 재고, 그래야 이
	//   픽스처가 앞으로 증분마다 안 깨진다.
	var live int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT a.item_id) FROM item_after a
		JOIN item i ON i.project = a.project AND i.id = a.item_id
		WHERE a.project = 'P' AND a.dep_item = 'dep' AND i.state IN ('open','claimed')`).
		Scan(&live); err != nil || live != 1 {
		t.Fatalf("전제가 깨졌다 — 파생 종속 수가 %d 다(err=%v). 살아 있는 것은 waiter-open 하나라 1 이어야 한다", live, err)
	}

	before := dumpItemAfter(t, s)
	if len(before) != 2 {
		t.Fatalf("전제가 깨졌다 — item_after 가 %d행이다. 2행이어야 이 시험이 성립한다", len(before))
	}

	// ★ 리터럴 9 다. SchemaVersion-1 이 아니다.
	//   재현하려는 옛 상태는 "010 직전", 즉 9 다. 그 사실은 010 파일에 고정돼 있고
	//   뒤에 무슨 증분이 더 붙든 안 변한다(migrate_test.go 와 005 시험이 같은 함정을 못박아 뒀다).
	//
	//   010 은 구조를 하나도 안 만들므로 dropNonIdempotentColumns 류의 되돌림이 필요 없다 —
	//   되돌릴 것은 schema_version 행뿐이다.
	const prev = 9
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM schema_version WHERE version > ?`, prev); err != nil {
		t.Fatalf("옛 DB 구성 실패: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("닫기 실패: %v", err)
	}

	// ── 전제 확인(raw) ──
	raw, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("raw 열기 실패: %v", err)
	}
	var v, rows int
	if err := raw.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
		t.Fatalf("전제 확인 실패: %v", err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM item_dependents`).Scan(&rows); err != nil {
		t.Fatalf("전제 확인 실패: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("raw 닫기 실패: %v", err)
	}
	if v != prev {
		t.Fatalf("전제가 깨졌다 — 옛 DB 의 버전이 %d 다. %d 여야 010 이 돈다", v, prev)
	}
	if rows != 2 {
		t.Fatalf("전제가 깨졌다 — 비우기 전 item_dependents 가 %d행이다. 2행이어야 이 시험이 본다", rows)
	}

	// ── 증분을 태운다 ──
	//
	// ★ **10판에서 멈춘다.** 증분 011 이 item_dependents 를 표째 걷었으므로, 최신까지 올리면
	//   010 이 무엇을 했는지 볼 자리 자체가 사라진다. 그리고 10판 DB 는 Open 이 거절하므로
	//   (적용이 기동에서 분리된 뒤의 규약) 관문 없는 openRaw 로 연다 — 중간 판을 보는 것이
	//   이 시험의 목적이고, 그 목적에는 관문이 맞지 않는다.
	mustMigrateTo(t, path, 10)
	s2, err := openRaw(path, log)
	if err != nil {
		t.Fatalf("재열기(마이그레이션) 실패: %v", err)
	}
	defer func() {
		if err := s2.Close(); err != nil {
			t.Fatalf("닫기 실패: %v", err)
		}
	}()

	// ── ① 지웠는가 ──
	if got := itemDependentsAll(t, s2); len(got) != 0 {
		t.Errorf("증분 뒤에도 item_dependents 가 %v 다 — 010 이 안 돌았거나 조건이 붙었다", got)
	}

	// ── ② 안 지울 것을 안 지웠는가 ──
	// 이 축이 없으면 `DELETE FROM item_after` 를 섞은 증분도 이 시험을 통과한다.
	after := dumpItemAfter(t, s2)
	if len(after) != len(before) {
		t.Fatalf("item_after 가 %d행이다 — 증분 전 %d행. 010 은 이 표를 한 행도 안 건드려야 한다.\n"+
			"이 표가 정본이고, 010 의 되돌리기 근거 전부가 그것이 그대로라는 데 걸려 있다.", len(after), len(before))
	}
	for i := range before {
		if after[i] != before[i] {
			t.Errorf("item_after %d번 행이 %v 다 — 증분 전 %v", i, after[i], before[i])
		}
	}

	// ── ③ 머리말이 적은 되돌리기가 실제로 되돌리는가 ──
	// destructiveExempt[10] 의 사유가 이 질의에 걸려 있다. 그 주장을 문장으로만 두면 아무도
	// 검산하지 않는다 — 여기서 실제로 돌려 심은 값이 그대로 나오는지 본다.
	//
	// ★ 그리고 **안 돌아오는 것까지 못박는다.** 복원되는 것은 행 집합이 아니라 함수다:
	// 간선이 있는 행은 값까지 살아나고, n=0 행은 **안 살아난다**(간선이 없으니 GROUP BY 가
	// 행을 못 만든다). 그 둘은 읽는 쪽에게 같은 값이었으므로 잃는 것이 없지만, 그 사실을
	// 시험이 안 재면 머리말의 한 문장이 검산 없는 주장으로 남는다.
	if _, err := s2.db.ExecContext(ctx, `
		INSERT INTO item_dependents(project, item_id, n)
		SELECT project, dep_item, COUNT(*) FROM item_after
		 WHERE dep_item IS NOT NULL GROUP BY project, dep_item`); err != nil {
		t.Fatalf("머리말이 적은 되돌리기 질의가 실패했다: %v", err)
	}
	restored := itemDependentsAll(t, s2)
	want := map[[2]string]int{{"P", "dep"}: 2} // drained(n=0)는 여기 없다 — 아래 단정이 그것을 못박는다
	if _, back := restored[[2]string{"P", "drained"}]; back {
		t.Errorf("n=0 이던 drained 행이 되살아났다 — 되돌리기 질의는 간선에서 GROUP BY 하므로\n" +
			"간선이 0인 행은 만들 수 없다. 살아났다면 질의가 바뀐 것이고, 머리말 ⒝ 의\n" +
			"\"복원되는 것은 행 집합이 아니라 함수다\" 문단도 함께 고쳐야 한다.")
	}
	if len(restored) != len(want) {
		t.Fatalf("되돌린 표가 %v 다 — 기대 %v", restored, want)
	}
	for k, w := range want {
		if restored[k] != w {
			t.Errorf("되돌린 %v 의 n 이 %d 다 — 증분 전 심은 값은 %d 였다.\n"+
				"머리말 ⒝ 와 destructiveExempt[10] 의 사유가 이 등식에 걸려 있다.", k, restored[k], w)
		}
	}
}

// dumpItemAfter 는 item_after 전체를 결정적 순서로 낸다. 증분 전후 대조용이다.
func dumpItemAfter(t *testing.T, s *Store) [][5]string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT project, item_id,
		       COALESCE(dep_item, ''), COALESCE(dep_job, ''), COALESCE(dep_sha, '')
		  FROM item_after ORDER BY project, item_id, dep_item, dep_job, dep_sha`)
	if err != nil {
		t.Fatalf("item_after 조회 실패: %v", err)
	}
	defer rows.Close()
	var out [][5]string
	for rows.Next() {
		var r [5]string
		if err := rows.Scan(&r[0], &r[1], &r[2], &r[3], &r[4]); err != nil {
			t.Fatalf("item_after 행 해석 실패: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("item_after 순회 실패: %v", err)
	}
	return out
}

// itemDependentsAll 은 표 전체를 (project, item_id, n) 로 낸다.
//
// ★ 이 헬퍼는 2026-08-23 에 dependents_retired_test.go 에서 옮겨 왔다. 그 파일은 "표가
// 되살아나지 않는지"를 지켰는데, 증분 011 이 표를 걷으면서 지킬 대상이 사라져 함께 걷었다
// (그 파일이 스스로 "표를 DROP 했다면 이 시험도 함께 걷어라"고 적어 뒀다). 읽는 헬퍼만
// 이 파일로 남는다 — 10판 시점을 보는 이 시험이 유일한 사용처다.
func itemDependentsAll(t *testing.T, s *Store) map[[2]string]int {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT project, item_id, n FROM item_dependents ORDER BY project, item_id`)
	if err != nil {
		t.Fatalf("item_dependents 를 못 읽었다: %v\n"+
			"이 시험은 **10판 시점**을 본다 — 11판에는 이 표가 없다(증분 011).", err)
	}
	defer rows.Close()
	out := map[[2]string]int{}
	for rows.Next() {
		var p, id string
		var n int
		if err := rows.Scan(&p, &id, &n); err != nil {
			t.Fatalf("item_dependents 행 해석 실패: %v", err)
		}
		out[[2]string{p, id}] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("item_dependents 순회 실패: %v", err)
	}
	return out
}
