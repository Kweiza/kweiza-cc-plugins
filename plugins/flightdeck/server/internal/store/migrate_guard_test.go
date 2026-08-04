package store

import (
	"regexp"
	"strings"
	"testing"
)

// 이 파일이 지키는 것은 **설계 §7 의 판단이 뒤집히는 순간**이다.
//
// §7 은 "나쁜 스키마 변경으로 크래시루프"의 처방을 셋으로 적었다(one-shot 분리 · 백업 · 롤백 명령).
// 지금 있는 것은 백업뿐이고, 나머지 둘을 **일부러 안 만들었다** — 근거는 store.go 의
// 마이그레이션 절 주석에 있다: 증분이 한 단이고 순수 가산이라 실질 위험이 낮은 반면,
// 적용을 기동에서 떼면 fail-open 훅 4종이 "스키마가 안 올라간 DB" 를 만나는 새 경로가 생긴다.
//
// 그 판단에는 **만료 조건이 명시돼 있다**:
//
//	"이 판단이 뒤집히는 조건: 증분이 파괴적(컬럼 삭제·타입 변경·데이터 이행)이 되는 순간.
//	 그때는 fd migrate [--to N] / fd migrate --rollback 으로 적용을 기동에서 분리한다."
//
// ★ **그런데 그 순간을 보는 코드가 없었다.** 조건은 문서와 주석에만 있고, 파괴적 증분이
// 들어오는 날 아무도 알려 주지 않는다 — 만료된 판단 위에서 계속 도는 것이 이 항목이
// 겨냥한 위험("다음 세션이 '설계대로 돼 있다'고 믿는 것")의 정확한 모양이다.
// 이 파일이 그 자리를 메운다.

// destructiveOps 는 증분 SQL 하나에서 **파괴적 조작**을 찾는다. 순수 함수다.
//
// 파괴적의 정의는 설계 §7 이 준 셋이다 — 컬럼 삭제 · 타입 변경 · 데이터 이행.
// SQLite 에는 타입 변경 구문이 없어 표를 다시 만들어 옮기는 관용구로 하므로,
// 그 관용구의 자취(RENAME · INSERT…SELECT · DROP TABLE)를 함께 본다.
//
// ★ 주석을 먼저 걷어낸다. 이 레포의 증분 파일은 본문보다 주석이 길고, 그 주석은
// 무엇을 **안 하는지**를 설명하느라 파괴적 낱말을 자주 쓴다 — 걷어내지 않으면
// 설명이 잘 붙은 증분일수록 더 잘 걸리는, 정확히 거꾸로 된 시험이 된다.
//
// ★ DROP INDEX 는 여기 없다. 데이터가 사라지지 않고 다시 만들면 되는 조작이라
// §7 의 셋 어디에도 안 들어간다. 헛걸림이 늘면 이런 시험은 결국 꺼진다.
func destructiveOps(sql string) []string {
	s := strings.ToUpper(stripSQLComments(sql))

	// ★ UPDATE 를 낱말 하나로 찾으면 안 된다. 이 스키마의 외래 키는 `ON UPDATE NO ACTION`
	// 을 쓰고, 그러면 **제약을 적은 증분이 전부 데이터 이행으로 걸린다.**
	// 그래서 `UPDATE <표> SET` 모양일 때만 센다.
	checks := []struct {
		op string
		re *regexp.Regexp
	}{
		{"DROP TABLE (표 삭제)", regexp.MustCompile(`\bDROP\s+TABLE\b`)},
		{"DROP COLUMN (컬럼 삭제)", regexp.MustCompile(`\bDROP\s+COLUMN\b`)},
		{"RENAME (표·컬럼 개명 — 표 재작성 관용구의 자취)", regexp.MustCompile(`\bRENAME\s+(TO|COLUMN)\b`)},
		{"DELETE FROM (행 삭제)", regexp.MustCompile(`\bDELETE\s+FROM\b`)},
		{"UPDATE … SET (데이터 이행)", regexp.MustCompile(`\bUPDATE\s+[A-Z_][A-Z0-9_]*\s+SET\b`)},
		{"INSERT … SELECT (데이터 이행)", regexp.MustCompile(`\bINSERT\s+INTO\b[\s\S]*?\bSELECT\b`)},
	}

	var found []string
	for _, c := range checks {
		if c.re.MatchString(s) {
			found = append(found, c.op)
		}
	}
	return found
}

// stripSQLComments 는 `--` 줄 주석과 `/* */` 블록 주석을 걷어낸다. 순수 함수다.
func stripSQLComments(sql string) string {
	var b strings.Builder
	b.Grow(len(sql))
	for i := 0; i < len(sql); {
		switch {
		case strings.HasPrefix(sql[i:], "--"):
			j := strings.IndexByte(sql[i:], '\n')
			if j < 0 {
				return b.String()
			}
			b.WriteByte('\n') // 줄바꿈은 남긴다 — 낱말이 붙어 버리면 없던 구문이 생긴다
			i += j + 1
		case strings.HasPrefix(sql[i:], "/*"):
			j := strings.Index(sql[i+2:], "*/")
			if j < 0 {
				return b.String()
			}
			b.WriteByte(' ')
			i += 2 + j + 2
		default:
			b.WriteByte(sql[i])
			i++
		}
	}
	return b.String()
}

// ─────────────────────────────────────────────────────────────────────────────

func TestDestructiveOpsNamesWhatItFound(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want bool
	}{
		// ── 가산 — 걸리면 안 된다 ────────────────────────────────────────────
		{"표 생성", "CREATE TABLE t (a TEXT);", false},
		{"인덱스 생성", "CREATE INDEX t_by_a ON t(a);", false},
		{"컬럼 추가", "ALTER TABLE t ADD COLUMN b TEXT;", false},
		{"트리거 생성", "CREATE TRIGGER g BEFORE DELETE ON t BEGIN SELECT 1; END;", false},
		{"씨앗 삽입", "INSERT INTO t (a) VALUES ('x');", false},
		{"인덱스 삭제는 파괴가 아니다", "DROP INDEX t_by_a;", false},

		// ★ 헛걸림 회귀 — 이 스키마의 외래 키가 실제로 쓰는 구문이다.
		//   낱말 UPDATE 로 찾으면 제약을 적은 증분이 전부 데이터 이행으로 걸린다.
		{"ON UPDATE NO ACTION 은 이행이 아니다",
			"CREATE TABLE c (p TEXT REFERENCES t(a) ON UPDATE NO ACTION ON DELETE CASCADE);", false},

		// ★ 주석 안의 낱말로 걸리면 안 된다 — 설명이 길수록 잘 걸리는 거꾸로 된 시험이 된다.
		{"줄 주석 속 낱말", "-- 옛 판은 DROP TABLE 을 썼다. 지금은 안 쓴다.\nCREATE TABLE t (a TEXT);", false},
		{"블록 주석 속 낱말", "/* DELETE FROM t 를 하지 않는 이유 */ CREATE TABLE t (a TEXT);", false},

		// ── 파괴적 — 반드시 걸려야 한다 ──────────────────────────────────────
		{"표 삭제", "DROP TABLE t;", true},
		{"컬럼 삭제", "ALTER TABLE t DROP COLUMN b;", true},
		{"표 개명", "ALTER TABLE t RENAME TO t_old;", true},
		{"컬럼 개명", "ALTER TABLE t RENAME COLUMN a TO z;", true},
		{"행 삭제", "DELETE FROM t WHERE a = 'x';", true},
		{"데이터 이행(UPDATE)", "UPDATE t SET a = 'y';", true},
		{"데이터 이행(INSERT SELECT)", "INSERT INTO t2 (a) SELECT a FROM t;", true},
		{"표 재작성 관용구", "CREATE TABLE t_new (a INTEGER);\nINSERT INTO t_new SELECT a FROM t;\nDROP TABLE t;\nALTER TABLE t_new RENAME TO t;", true},
	}

	for _, c := range cases {
		got := destructiveOps(c.sql)
		if (len(got) > 0) != c.want {
			t.Errorf("%s: 파괴적=%v 여야 하는데 %v 다 (찾은 것: %v)\nSQL: %s",
				c.name, c.want, len(got) > 0, got, c.sql)
		}
	}
}

// TestBundledMigrationsAreAdditive 는 **지금 번들된 증분이 전부 순수 가산인지**를 단정한다.
//
// 이 시험이 빨개지는 것은 결함이 아니라 **판단의 만료 통지**다. 설계 §7 과 store.go 의
// 마이그레이션 절이 "증분이 파괴적이 되는 순간 적용을 기동에서 분리한다"고 적어 뒀고,
// 여기가 그 순간을 알아채는 유일한 자리다.
func TestBundledMigrationsAreAdditive(t *testing.T) {
	// ── 대조가 성립했는지 먼저 단정한다 ─────────────────────────────────────
	// 증분 목록이 비면 이 시험은 아무것도 안 보면서 통과한다.
	if len(migrations) == 0 {
		t.Fatal("전제가 깨졌다 — 번들된 증분이 0건이다. 볼 것이 없으면 지킬 것도 없다")
	}

	for _, m := range migrations {
		if strings.TrimSpace(m.SQL) == "" {
			t.Fatalf("전제가 깨졌다 — 증분 %d(%s) 의 SQL 이 비었다(//go:embed 확인)", m.To, m.Name)
		}
		if ops := destructiveOps(m.SQL); len(ops) > 0 {
			t.Errorf("증분 %d(%s) 에 파괴적 조작이 있다: %s\n\n"+
				"이것은 시험의 결함이 아니라 **판단의 만료 통지**다.\n"+
				"설계 §7 과 store.go 의 마이그레이션 절은 적용을 fd serve 기동 경로(store.Open)\n"+
				"안에 남겨 두기로 했고, 그 근거는 \"증분이 한 단이고 순수 가산이라 실질 위험이 낮다\"\n"+
				"였다. 파괴적 증분이 들어온 지금 그 근거가 사라졌다.\n\n"+
				"둘 중 하나를 하고 그 근거를 §7 과 store.go 주석에 함께 적어라:\n"+
				"  (a) 적용을 기동에서 분리한다 — fd migrate [--to N] / fd migrate --rollback.\n"+
				"      §7 이 이 순간을 위해 미리 이름 붙여 둔 처방이다.\n"+
				"  (b) 이 증분이 왜 되돌릴 수 있는지를 적고 이 시험의 예외로 둔다.\n"+
				"      (트리거 본문의 DELETE FROM 처럼 실제로 안전한 경우가 있다)\n\n"+
				"어느 쪽이든 **문서와 코드를 갈린 채로 두지 마라.**",
				m.To, m.Name, strings.Join(ops, ", "))
		}
	}
}
