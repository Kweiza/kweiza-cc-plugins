package store

import (
	"fmt"
	"regexp"
	"slices"
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
// ★ 반환은 사람이 읽는 라벨이 아니라 **op 상수**다. 예외 표가 라벨 문자열에 결합돼 있으면
// 라벨을 한 글자만 고쳐도 예외가 조용히 안 맞게 되고, 그때 방어가 사라진 것을 아무도 모른다.
// 라벨은 String() 으로 내려 표시 전용이 된다.
type op int

const (
	opDropTable op = iota
	opDropColumn
	opRename
	opDeleteFrom
	opUpdateSet
	opInsertSelect
)

// String 은 표시 전용이다. **어떤 판정의 축도 아니다** — 축이 되는 순간 위 ★ 가 무너진다.
func (o op) String() string {
	switch o {
	case opDropTable:
		return "DROP TABLE (표 삭제)"
	case opDropColumn:
		return "DROP COLUMN (컬럼 삭제)"
	case opRename:
		return "RENAME (표·컬럼 개명 — 표 재작성 관용구의 자취)"
	case opDeleteFrom:
		return "DELETE FROM (행 삭제)"
	case opUpdateSet:
		return "UPDATE … SET (데이터 이행)"
	case opInsertSelect:
		return "INSERT … SELECT (데이터 이행)"
	}
	return fmt.Sprintf("op(%d) — 이름 없는 조작. 상수를 늘렸으면 String 도 늘려라", int(o))
}

func destructiveOps(sql string) []op {
	s := strings.ToUpper(stripSQLComments(sql))

	// ★ UPDATE 를 낱말 하나로 찾으면 안 된다. 이 스키마의 외래 키는 `ON UPDATE NO ACTION`
	// 을 쓰고, 그러면 **제약을 적은 증분이 전부 데이터 이행으로 걸린다.**
	// 그래서 `UPDATE <표> SET` 모양일 때만 센다.
	checks := []struct {
		op op
		re *regexp.Regexp
	}{
		{opDropTable, regexp.MustCompile(`\bDROP\s+TABLE\b`)},
		{opDropColumn, regexp.MustCompile(`\bDROP\s+COLUMN\b`)},
		{opRename, regexp.MustCompile(`\bRENAME\s+(TO|COLUMN)\b`)},
		{opDeleteFrom, regexp.MustCompile(`\bDELETE\s+FROM\b`)},
		{opUpdateSet, regexp.MustCompile(`\bUPDATE\s+[A-Z_][A-Z0-9_]*\s+SET\b`)},
		{opInsertSelect, regexp.MustCompile(`\bINSERT\s+INTO\b[\s\S]*?\bSELECT\b`)},
	}

	var found []op
	for _, c := range checks {
		if c.re.MatchString(s) {
			found = append(found, c.op)
		}
	}
	return found
}

// opLabels 는 표시용 라벨을 낸다. 오류 메시지 전용이다.
func opLabels(ops []op) []string {
	out := make([]string, 0, len(ops))
	for _, o := range ops {
		out = append(out, o.String())
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// 예외
// ─────────────────────────────────────────────────────────────────────────────

// exemption 은 증분 하나가 여는 예외다.
//
// ★ 사유가 값의 일부다. 위 시험(TestBundledMigrationsAreAdditive)의 갈래 (b) 는
// "그 증분이 **왜** 되돌릴 수 있는지를 적고 예외로 둔다" 이지 "예외 목록에 올린다" 가 아니다.
// 사유를 선택 항목으로 두면 목록은 자라고 근거는 안 자란다.
type exemption struct {
	ops []op
	why string
}

// destructiveExempt 는 증분 번호 → 그 증분이 여는 예외다.
//
// ★ 여기 오르는 것은 **그 증분에 한정**된다. 조작 단위 허용이 아니라 증분 단위 허용이다 —
// "DELETE FROM 은 이제 괜찮다" 가 아니라 "005 의 DELETE FROM 은 이런 이유로 괜찮다" 여야
// 다음 증분이 같은 판정을 다시 받는다.
var destructiveExempt = map[int]exemption{
	// 005 · 절대경로 발자국 삭제.
	5: {
		ops: []op{opDeleteFrom},
		why: "footprint 는 D(파생) 계층이고 참조하는 표가 없다. 지우는 행은 judge.comparablePath 가 " +
			"이미 처방 축에서 배제한 절대경로뿐이며, 그 좌표계로는 관문(Beat·Pick)이 쓰는 " +
			"session.worktree 기준으로 상대화할 수 있는 행이 0건이라 살릴 방법 자체가 없다. " +
			"판올림 전 VACUUM INTO 백업이 자동으로 뜨고 RollbackHint 가 복구 절차를 낸다. " +
			"근거 전문은 005 머리말과 설계 §7 에 있다.",
	},
	// 006 · 워크트리 접두 발자국 삭제.
	//
	// ★ 005 의 사유를 복사하면 안 된다. 저쪽 ⒝ 는 "읽는 쪽이 이미 배제한 행"인데
	// 이 행들에는 그 문장이 **거짓**이다 — 접두 경로는 절대경로가 아니라 comparablePath 를
	// 통과하고, 그래서 배제되는 대신 거짓 증거로 인용된다.
	6: {
		ops: []op{opDeleteFrom},
		why: "footprint 는 D(파생) 계층이고 참조하는 표가 없다. 지우는 행은 judge.comparablePath 를 " +
			"통과해 grounded=true 로 세어지면서 pathRelated 에서는 성분 0번부터 갈려 100% 안 덮인 " +
			"것으로 인용되는 행이다 — 배제된 것이 아니라 **읽혀서 거짓 증거가 된다**(실측: 인용 처방 " +
			"22건, 전부 outside: 키). 접두를 벗겨 살리면 형제 워크트리의 같은 이름 파일이 한 문자열로 " +
			"합쳐져 4530e3c 의 판단과 DESIGN §3 이 없앤 조상 트리 상속이 되살아난다. 유입은 같은 " +
			"회차의 관문(service/session.go 의 judge.CarriesWorktreePrefix)이 막았고, 생산 문이 " +
			"Beat 하나뿐이라 그것으로 0이 된다. 판올림 전 VACUUM INTO 백업이 자동으로 뜬다. " +
			"근거 전문은 006 머리말에 있다.",
	},
}

// neverExempt 는 예외로도 못 여는 조작이다. 데이터가 아니라 **구조**가 사라지는 것들이다.
//
// ★ 이 목록이 필요한 이유는 실측이다. 이 파일의 옛 주석은 "영구 표가 사라지는 쪽은 다른
// 시험이 본다 — TestFreshInstallAndUpgradeProduceTheSameSchema 와 TestDeclaredTablesMatchDesign"
// 이라고 적어 뒀는데, 증분에 `DROP TABLE` 을 넣어 실제로 돌려 보니 **둘 다 통과했다.**
// 전자는 신규 설치와 판올림이 양쪽 다 그 증분을 돌아 스키마가 똑같이 줄기 때문이고,
// 후자는 증분 텍스트의 CREATE TABLE 만 세기 때문이다. 즉 그 안전망은 없었다.
// 없는 안전망을 믿는 대신 여기서 막는다.
var neverExempt = []op{opDropTable, opDropColumn, opRename}

// exemptReason 은 증분 to 가 조작 o 를 써도 되는지와 그 사유를 낸다. 순수 함수다.
//
// ★ 표를 인자로 받는다. 전역을 직접 읽으면 이 판정을 시험할 때 전역을 바꿔치기해야 하고,
// 그렇게 얽힌 시험은 실행 순서에 따라 빨개진다.
func exemptReason(table map[int]exemption, to int, o op) (why string, ok bool) {
	for _, banned := range neverExempt {
		if o == banned {
			return "", false
		}
	}
	ex, found := table[to]
	if !found || strings.TrimSpace(ex.why) == "" {
		return "", false
	}
	for _, allowed := range ex.ops {
		if o == allowed {
			return ex.why, true
		}
	}
	return "", false
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

// ★ 이 시험과 아래 TestNeverExemptOpsAreActuallyDetected 가 왜 둘 다 필요한가:
// **op 와 정규식의 대응이 어긋나면 예외가 엉뚱한 조작을 면제한다.** len(got)>0 만 보면
// checks 표에서 복붙 실수로 {opDeleteFrom, `\bDROP\s+TABLE\b`} 가 돼도 "파괴적이다"는
// 여전히 참이라 이 시험이 안 잡는다 — 그러면 증분 5 안의 DROP TABLE 이 opDeleteFrom 으로
// 보고돼 destructiveExempt[5] 에 면제되고, neverExempt 가 막아야 할 DROP TABLE 이 조용히
// 빠져나간다. want=true 케이스는 그래서 **찾은 op 집합**까지 wantOps 로 대조한다.
func TestDestructiveOpsNamesWhatItFound(t *testing.T) {
	cases := []struct {
		name    string
		sql     string
		want    bool
		wantOps []op // want=true 일 때만 채운다. 순서 무관 집합 비교(sameOpSet)다.
	}{
		// ── 가산 — 걸리면 안 된다 ────────────────────────────────────────────
		{"표 생성", "CREATE TABLE t (a TEXT);", false, nil},
		{"인덱스 생성", "CREATE INDEX t_by_a ON t(a);", false, nil},
		{"컬럼 추가", "ALTER TABLE t ADD COLUMN b TEXT;", false, nil},
		{"트리거 생성", "CREATE TRIGGER g BEFORE DELETE ON t BEGIN SELECT 1; END;", false, nil},
		{"씨앗 삽입", "INSERT INTO t (a) VALUES ('x');", false, nil},
		{"인덱스 삭제는 파괴가 아니다", "DROP INDEX t_by_a;", false, nil},

		// ★ 헛걸림 회귀 — 이 스키마의 외래 키가 실제로 쓰는 구문이다.
		//   낱말 UPDATE 로 찾으면 제약을 적은 증분이 전부 데이터 이행으로 걸린다.
		{"ON UPDATE NO ACTION 은 이행이 아니다",
			"CREATE TABLE c (p TEXT REFERENCES t(a) ON UPDATE NO ACTION ON DELETE CASCADE);", false, nil},

		// ★ 주석 안의 낱말로 걸리면 안 된다 — 설명이 길수록 잘 걸리는 거꾸로 된 시험이 된다.
		{"줄 주석 속 낱말", "-- 옛 판은 DROP TABLE 을 썼다. 지금은 안 쓴다.\nCREATE TABLE t (a TEXT);", false, nil},
		{"블록 주석 속 낱말", "/* DELETE FROM t 를 하지 않는 이유 */ CREATE TABLE t (a TEXT);", false, nil},

		// ── 파괴적 — 반드시 걸려야 하고, **그 op 로** 걸려야 한다 ──────────────
		{"표 삭제", "DROP TABLE t;", true, []op{opDropTable}},
		{"컬럼 삭제", "ALTER TABLE t DROP COLUMN b;", true, []op{opDropColumn}},
		{"표 개명", "ALTER TABLE t RENAME TO t_old;", true, []op{opRename}},
		{"컬럼 개명", "ALTER TABLE t RENAME COLUMN a TO z;", true, []op{opRename}},
		{"행 삭제", "DELETE FROM t WHERE a = 'x';", true, []op{opDeleteFrom}},
		{"데이터 이행(UPDATE)", "UPDATE t SET a = 'y';", true, []op{opUpdateSet}},
		{"데이터 이행(INSERT SELECT)", "INSERT INTO t2 (a) SELECT a FROM t;", true, []op{opInsertSelect}},
		// ★ 이 SQL 은 opDropTable · opRename · opInsertSelect 셋을 동시에 건다. 아래
		// wantOps 는 checks 표 순서(destructiveOps 가 append 하는 순서)와 우연히 같지만,
		// 대조는 sameOpSet 으로 **순서 무관 집합**으로 한다 — 그 우연에 기대지 않는다.
		{"표 재작성 관용구", "CREATE TABLE t_new (a INTEGER);\nINSERT INTO t_new SELECT a FROM t;\nDROP TABLE t;\nALTER TABLE t_new RENAME TO t;",
			true, []op{opDropTable, opRename, opInsertSelect}},
	}

	for _, c := range cases {
		got := destructiveOps(c.sql)
		if (len(got) > 0) != c.want {
			t.Errorf("%s: 파괴적=%v 여야 하는데 %v 다 (찾은 것: %v)\nSQL: %s",
				c.name, c.want, len(got) > 0, got, c.sql)
			continue
		}
		if c.want && !sameOpSet(got, c.wantOps) {
			t.Errorf("%s: 찾은 op 가 %v 여야 하는데 %v 다 — op↔정규식 대응이 어긋났을 수 있다\nSQL: %s",
				c.name, c.wantOps, got, c.sql)
		}
	}
}

// sameOpSet 은 두 op 슬라이스가 **순서 무관하게** 같은 다중집합인지 본다.
func sameOpSet(a, b []op) bool {
	if len(a) != len(b) {
		return false
	}
	count := map[op]int{}
	for _, o := range a {
		count[o]++
	}
	for _, o := range b {
		count[o]--
	}
	for _, n := range count {
		if n != 0 {
			return false
		}
	}
	return true
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
		// 예외에 오른 조작은 사유와 함께 통과시킨다. 나머지는 그대로 만료 통지다.
		var unexcused []op
		for _, o := range destructiveOps(m.SQL) {
			if _, ok := exemptReason(destructiveExempt, m.To, o); ok {
				continue
			}
			unexcused = append(unexcused, o)
		}
		if len(unexcused) > 0 {
			t.Errorf("증분 %d(%s) 에 예외 없는 파괴적 조작이 있다: %s\n\n"+
				"이것은 시험의 결함이 아니라 **판단의 만료 통지**다.\n"+
				"설계 §7 과 store.go 의 마이그레이션 절은 적용을 fd serve 기동 경로(store.Open)\n"+
				"안에 남겨 두기로 했고, 그 근거는 \"증분이 순수 가산이라 실질 위험이 낮다\"\n"+
				"였다. 파괴적 증분이 들어온 지금 그 근거가 사라졌다.\n\n"+
				"둘 중 하나를 하고 그 근거를 §7 과 store.go 주석에 함께 적어라:\n"+
				"  (a) 적용을 기동에서 분리한다 — fd migrate [--to N] / fd migrate --rollback.\n"+
				"      §7 이 이 순간을 위해 미리 이름 붙여 둔 처방이다. 그 명령은 아직 없다.\n"+
				"  (b) 이 증분이 왜 되돌릴 수 있는지를 destructiveExempt 에 **사유와 함께** 올린다.\n"+
				"      사유가 비면 예외로 안 쳐 준다. neverExempt 에 오른 조작은 (b) 로도 못 연다.\n\n"+
				"어느 쪽이든 **문서와 코드를 갈린 채로 두지 마라.**",
				m.To, m.Name, strings.Join(opLabels(unexcused), ", "))
		}
	}
}

// 예외 기제 자체를 시험한다.
//
// ★ 이 시험이 없으면 기제의 분기가 **한 번도 안 돈다.** 지금 예외에 오른 증분은 하나뿐이고
// 그 하나는 금지 목록에 안 걸리므로, 거절 경로는 시험이 직접 돌지 않으면 영영 안 돈다 —
// 그러면 "막는다고 적혀 있는데 실은 안 막는" 상태가 조용히 생긴다.
//
// ★ 표를 **인자로 받는다.** 전역 destructiveExempt 를 시험이 바꿔치기하면 그 시험은
// 다른 시험과 순서로 얽히고, 얽힌 시험은 언젠가 이유 없이 빨개진다.
func TestExemptionMechanism(t *testing.T) {
	ok5 := map[int]exemption{5: {ops: []op{opDeleteFrom}, why: "되돌릴 수 있고 읽는 쪽이 이미 배제한 행이다"}}

	cases := []struct {
		name   string
		table  map[int]exemption
		to     int
		o      op
		wantOK bool
	}{
		{"등록된 조작은 통과한다", ok5, 5, opDeleteFrom, true},
		{"등록 안 된 조작은 못 지나간다", ok5, 5, opUpdateSet, false},
		{"다른 증분으로 새지 않는다", ok5, 6, opDeleteFrom, false},
		{"표에 없는 증분은 예외가 없다", map[int]exemption{}, 5, opDeleteFrom, false},

		// ★ 사유가 없으면 예외가 아니다. 예외를 여는 값은 "허용 목록에 있다" 가 아니라
		//   "왜 안전한지가 적혀 있다" 다 — 근거 없는 예외는 다음 사람이 판정할 수 없다.
		{"사유가 비면 예외가 아니다",
			map[int]exemption{5: {ops: []op{opDeleteFrom}, why: ""}}, 5, opDeleteFrom, false},
		{"사유가 공백뿐이어도 예외가 아니다",
			map[int]exemption{5: {ops: []op{opDeleteFrom}, why: "   \n\t"}}, 5, opDeleteFrom, false},

		// ★ 예외로도 못 여는 것들. 데이터가 아니라 **구조**가 사라지는 조작이다.
		{"DROP TABLE 은 예외에 올려도 거절한다",
			map[int]exemption{5: {ops: []op{opDropTable}, why: "사유는 있다"}}, 5, opDropTable, false},
		{"DROP COLUMN 은 예외에 올려도 거절한다",
			map[int]exemption{5: {ops: []op{opDropColumn}, why: "사유는 있다"}}, 5, opDropColumn, false},
		{"RENAME 은 예외에 올려도 거절한다",
			map[int]exemption{5: {ops: []op{opRename}, why: "사유는 있다"}}, 5, opRename, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			why, ok := exemptReason(c.table, c.to, c.o)
			if ok != c.wantOK {
				t.Fatalf("예외 판정이 %v 여야 하는데 %v 다 (증분 %d, 조작 %s, 사유 %q)",
					c.wantOK, ok, c.to, c.o, why)
			}
			if ok && strings.TrimSpace(why) == "" {
				t.Fatalf("통과시키면서 사유가 비었다 — 사유 없는 예외는 이 기제가 막으라고 있는 바로 그것이다 (증분 %d, 조작 %s)",
					c.to, c.o)
			}
			if !ok && why != "" {
				t.Fatalf("거절하면서 사유를 냈다 %q — 거절의 사유를 통과의 사유 자리로 흘리면 호출부가 둘을 못 가른다", why)
			}
		})
	}
}

// neverExempt 에 오른 것이 destructiveOps 가 **그 op 로** 실제로 찾는 조작인지 확인한다.
//
// ★ 이 대조가 없으면 금지 목록이 **없는 조작을 금지**할 수 있다. 그러면 목록은 있는데
// 아무것도 안 막고, 그 사실은 아무 데도 안 보인다.
//
// ★ len(found)==0 만 보던 전판은 이 구멍을 못 잡았다. checks 표에서 opDropTable 에
// `\bDELETE\s+FROM\b` 같은 엉뚱한 정규식이 결합돼도, 그 SQL 이 다른 이유로 뭔가는
// 찾히면(예: 같은 문장에 RENAME 이 섞이면) len(found)>0 이라 통과한다 — **금지하려는 그
// op 자체**가 걸렸는지는 안 본 것이다. 그래서 slices.Contains(found, banned) 로
// 대상 op 가 정확히 그 자리에 있는지를 본다(TestDestructiveOpsNamesWhatItFound 의
// wantOps 대조와 같은 이유 — op 와 정규식의 대응이 어긋나면 예외가 엉뚱한 조작을 면제한다).
func TestNeverExemptOpsAreActuallyDetected(t *testing.T) {
	samples := map[op]string{
		opDropTable:  "DROP TABLE t;",
		opDropColumn: "ALTER TABLE t DROP COLUMN b;",
		opRename:     "ALTER TABLE t RENAME TO t_old;",
	}
	for _, banned := range neverExempt {
		sql, has := samples[banned]
		if !has {
			t.Fatalf("금지 목록의 %s 에 대응하는 표본이 이 시험에 없다 — 목록을 늘렸으면 표본도 늘려라", banned)
		}
		found := destructiveOps(sql)
		if !slices.Contains(found, banned) {
			t.Fatalf("금지 목록에 %s 가 있는데 destructiveOps 가 %q 에서 **그 조작으로는** 못 찾는다"+
				"(찾은 것: %v) — op↔정규식 대응이 어긋나 금지 목록이 없는 조작을 막고 있을 수 있다",
				banned, sql, found)
		}
	}
}
