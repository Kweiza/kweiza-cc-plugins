package store

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
)

// 006 은 워크트리 접두 발자국을 전부 지우고 그 밖의 상대경로는 한 톨도 안 건드린다.
//
// ★ 이 시험의 어려운 절반은 "지웠다"가 아니라 **"안 지울 것을 안 지웠다"** 다. 표본 넷이
// 그 축이고(아래 keep=true), 그것이 없으면 `DELETE FROM footprint WHERE path LIKE '%worktrees%'`
// 같은 과녁 넓은 술어도 이 시험을 통과한다. 실측상 그 오답의 대가가 크다 — observed 발자국
// 1274건 중 1099건(86%)이 워크트리 안에서 도는 세션의 것이라, 절대경로에 그 문자열이
// 보인다고 지우면 발자국 축이 통째로 죽는다.
//
// ★ 전제를 **결과를 읽기 전에** 단정한다. 옛 상태가 안 만들어졌으면 아래 단정은 공허하다.
func TestMigration006DeletesWorktreePrefixedFootprints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	s, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("Open 실패: %v", err)
	}
	seed(t, s, "P")
	sess := mustSession(t, s, "P", "cc1")

	type fp struct {
		path   string
		origin string
		keep   bool
		why    string
	}
	samples := []fp{
		{".flightdeck/worktrees/fd-x/plugins/a.go", "observed", false,
			"이 결함의 실물 — 카드 worktree 가 주 저장소 루트일 때 Beat 가 만든 모양이다"},
		{".claude/worktrees/y/b.go", "observed", false,
			"하네스가 만드는 자리도 같은 관례다 — judge.conventionRoots 가 둘을 함께 안다"},
		{"plugins/.flightdeck/worktrees/z/c.go", "observed", false,
			"중첩 배치. 성분 삼중이 경로 **중간**에 있어도 트리 밖이다 — 술어가 관문보다 좁으면 여기서 갈린다"},
		{".flightdeck/worktrees/fd-x/d.go", "claimed", false,
			"origin 으로 안 가른다. 오늘 접두 행은 전부 observed 지만 그것은 사실이지 계약이 아니다"},

		{"plugins/flightdeck/server/x.go", "observed", true,
			"평범한 저장소 상대 경로 — 이 행이 이 시험의 핵심이다"},
		{".flightdeck/other/x.go", "observed", true,
			"`.flightdeck` 이어도 `worktrees` 가 아니면 워크트리 루트가 아니다"},
		{"worktrees/plain/x.go", "observed", true,
			"`worktrees` 만으로는 아니다 — 앞에 `.flightdeck`/`.claude` 가 와야 성분 삼중이다"},
		{"keep/a_b%c d.go", "observed", true,
			"경로의 _ 와 %% 는 와일드카드가 아니다"},
	}
	for i, f := range samples {
		at := fmt.Sprintf("2026-08-01T00:00:%02d.000000Z", i)
		if _, err := s.db.Exec(
			`INSERT INTO footprint(session_id, path, origin, first_at, last_at) VALUES (?,?,?,?,?)`,
			sess.ID, f.path, f.origin, at, at); err != nil {
			t.Fatalf("표본 심기 실패(%s): %v", f.path, err)
		}
	}

	// ★ 리터럴 5다. SchemaVersion-1 이 아니다 — 이 시험이 재현하는 옛 상태는 "006 직전"이고
	//   그 사실은 006 파일에 고정돼 있어 뒤에 무슨 증분이 더 붙든 안 변한다.
	const prev = 5
	if _, err := s.db.Exec(
		fmt.Sprintf(`DELETE FROM schema_version WHERE version > %d`, prev)); err != nil {
		t.Fatalf("옛 DB 구성 실패: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("닫기 실패: %v", err)
	}

	// ── 전제 확인 ──
	raw, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("raw 열기 실패: %v", err)
	}
	var v, planted int
	if err := raw.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
		t.Fatalf("전제 확인 실패: %v", err)
	}
	if err := raw.QueryRow(`SELECT count(*) FROM footprint`).Scan(&planted); err != nil {
		t.Fatalf("전제 확인 실패: %v", err)
	}
	raw.Close()
	if v != prev {
		t.Fatalf("전제가 성립하지 않았다: schema_version=%d — 이 상태로는 아래 단정이 무의미하다", v)
	}
	if planted != len(samples) {
		t.Fatalf("전제가 성립하지 않았다: 표본이 %d건이다, want %d — 심기가 실패했다면 '지웠다'는 단정이 공허하다",
			planted, len(samples))
	}

	// ── 판올림 ──
	s2, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("판올림 Open 실패: %v", err)
	}
	defer s2.Close()

	var left int
	if err := s2.db.QueryRow(
		`SELECT count(*) FROM footprint
		  WHERE path LIKE '.flightdeck/worktrees/%' OR path LIKE '.claude/worktrees/%'
		     OR path LIKE '%/.flightdeck/worktrees/%' OR path LIKE '%/.claude/worktrees/%'`).Scan(&left); err != nil {
		t.Fatalf("판올림 뒤 조회 실패: %v", err)
	}
	if left != 0 {
		t.Errorf("접두 발자국이 %d건 남았다 — 006 이 겨냥한 것이 정확히 그 행들이다", left)
	}

	want := 0
	for _, f := range samples {
		if !f.keep {
			continue
		}
		want++
		var n int
		if err := s2.db.QueryRow(
			`SELECT count(*) FROM footprint WHERE session_id=? AND path=? AND origin=?`,
			sess.ID, f.path, f.origin).Scan(&n); err != nil {
			t.Fatalf("조회 실패(%s): %v", f.path, err)
		}
		if n != 1 {
			t.Errorf("남아야 할 행이 %d건이다 (%s, origin=%s) — %s", n, f.path, f.origin, f.why)
		}
	}

	var total int
	if err := s2.db.QueryRow(`SELECT count(*) FROM footprint`).Scan(&total); err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if total != want {
		t.Errorf("발자국이 %d건 남았다, want %d — 관문이 너무 넓으면 여기서 잡힌다", total, want)
	}
}
