package store

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// 항목 본문(title·body·paths)을 고치는 자리는 **하나뿐이다**
// ─────────────────────────────────────────────────────────────────────────────
//
// 2026-09-16 이전 이 파일은 "그런 자리가 **없다**"를 지켰다. DESIGN §11 이 그 부재를
// 판정으로 적었고 이 관문이 그 문장을 지켰다. 그 판정이 열렸다(설계 정본:
// docs/superpowers/specs/2026-09-16-item-amend-surface-design.md).
//
// **그래서 이 파일을 지우지 않았다.** §11 이 경고한 실패는 "본문을 못 고치는 것"이 아니라
// **"표면마다 무엇을 고칠 수 있나가 갈리는 것"**이고, 그 경고는 표면이 열린 뒤에 오히려
// 더 유효하다. 지키는 명제만 바꾼다:
//
//   전: title·body 를 무는 `UPDATE item SET` 은 0건이다
//   후: 그 UPDATE 는 store/amend.go 의 AmendItem 한 곳이고, **같은 함수 안에
//       `INSERT INTO item_revision` 이 있다**
//
// 둘째 절이 핵심이다. 이력 없는 수정 경로가 조용히 생기는 것이 item_revision 의 값을
// 통째로 무효로 만드는 유일한 길이고, 그 경로는 UPDATE 하나만 세는 그물에 안 걸린다.
//
// paths 가 감시 목록에 들어온 것도 이때다 — 이제 수정 축이고, 무엇보다 **겹침 판정의
// 입력**이라 몰래 고쳐지면 남의 화면이 조용히 움직인다.
//
// 방향은 여전히 양쪽이다:
//   ① 허용된 자리 밖에서 본문을 고치면        → 빨간불
//   ② §11 에서 이 표면의 이름이 사라지면      → 관문의 좌표가 밀린 것이므로 빨간불
//
// 같은 규율의 선례: signal_is_not_history_test.go(전수 walk) ·
// close_declaration_doc_test.go(DESIGN 앵커) · schema_table_count_test.go(선언 표 수) ·
// migrate_guard_test.go(파괴적 조작).

// amendWriterFile 은 본문을 고쳐도 되는 **유일한** 파일이다.
const amendWriterFile = "plugins/flightdeck/server/internal/store/amend.go"

// itemSetClauseRe 는 `UPDATE item SET <절>` 의 SET 절을 통째로 집는다.
//
// `item\s+SET` 이 표 이름 경계를 대신한다 — `UPDATE item_dependents SET` ·
// `UPDATE item_after SET` 은 item 뒤에 공백이 아니라 `_` 가 와서 안 걸린다.
// RE2 에는 lookahead 가 없으므로 이 모양이 경계를 표현하는 방법이다.
//
// (?is) 인 이유: SQL 이 백틱 문자열 안에서 줄을 넘기고, 대소문자가 섞일 수 있다.
// 비탐욕 + WHERE 종결이라 한 문장을 넘어 삼키지 않는다.
var itemSetClauseRe = regexp.MustCompile(`(?is)UPDATE\s+item\s+SET\s+(.*?)\s+WHERE`)

// itemUpdateHeadRe 는 SET 절 종결을 안 보고 **UPDATE 자체의 수**를 센다.
//
// 왜 따로 세나: 위 정규식은 WHERE 로 끊으므로 WHERE 없는 전체 갱신을 아예 못 본다.
// 지금 레포에 0건이지만, 생기면 그것이 가장 위험한 쓰기인데 그물이 **침묵한다.**
// 머릿수와 절 수가 어긋나면 그 차이가 곧 "못 본 UPDATE"다 — RE2 에는 부정 lookahead 가
// 없어서, 두 번 세어 빼는 것이 그 부재를 표현하는 방법이다.
var itemUpdateHeadRe = regexp.MustCompile(`(?is)UPDATE\s+item\s+SET\s+`)

// itemInsertRe 는 정본이다 — 생성이 title·body 를 쓰는 유일한 자리.
var itemInsertRe = regexp.MustCompile(`(?is)INSERT\s+INTO\s+item\s*\(`)

// itemBodyColumns 는 "본문"이다. 이 셋을 허용된 자리 밖에서 UPDATE 하면 §11 이 거짓이 된다.
var itemBodyColumns = []string{"title", "body", "paths"}

// setColumnRe 는 SET 절 조각 하나에서 대입 대상 컬럼 이름을 뽑는다.
var setColumnRe = regexp.MustCompile(`^\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*=`)

// itemBodyGuardRoot 는 이 시험이 훑는 레포 루트다(store 에서 다섯 단계 위).
//
// 좌표가 밀리면 이 시험은 아무것도 안 보면서 초록이 된다. 못박아 둔다 —
// signal_is_not_history_test.go 가 같은 이유로 같은 것을 한다.
func itemBodyGuardRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("레포 루트를 못 찾았다: %v", err)
	}
	for _, must := range []string{
		"plugins/flightdeck/DESIGN.md",
		"plugins/flightdeck/server/go.mod",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(must))); err != nil {
			t.Fatalf("레포 루트(%s)에 %s 가 없다 — 이 시험의 좌표가 틀렸다: %v", root, must, err)
		}
	}
	return root
}

// itemBodyGuardInScope 는 훑을 파일인지 답한다.
//
// 범위는 **plugins/flightdeck 아래의 살아 있는 .go 와 .sql** 이다. store 만 보지 않는
// 이유는, 다른 패키지가 DB 핸들을 얻어 직접 SQL 을 쓰기 시작하면 store 만 보는 그물은
// 그것을 영영 못 보기 때문이다. `.sql` 을 넣는 이유는 백필 마이그레이션이 실재하기
// 때문이다(005·006 이 footprint 를 그렇게 고친다) — 같은 수단이 item 으로 향하면
// Go 코드를 한 줄도 안 거치고 본문이 바뀐다.
//
// 시험 파일은 뺀다 — 규약을 설명하려면 위반 문장을 인용해야 한다(이 파일이 그렇다).
func itemBodyGuardInScope(rel string) bool {
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, "plugins/flightdeck/") {
		return false
	}
	base := filepath.Base(rel)
	if strings.HasSuffix(base, "_test.go") {
		return false
	}
	return strings.HasSuffix(base, ".go") || strings.HasSuffix(base, ".sql")
}

// itemBodyExecutableSQL 은 파일 하나에서 **실행될 수 있는 SQL 텍스트만** 뽑는다.
//
// ★ 좌표계가 이 함수다. 처음 판은 파일 본문을 통째로 훑었고, 그래서
// `web/actions.go` 의 **주석 안 SQL 인용**을 위반으로 잡았다 —
// "ForceReleaseClaim 은 `UPDATE item SET state='open' … AND state='claimed'` 를 친다"는
// 설명문이다. 막아야 할 것은 실행되는 SQL 이지 그것을 설명하는 산문이 아니다.
// 산문까지 잡으면 이 관문은 **문서를 정확히 쓰는 사람을 벌한다.**
//
//   - .go 는 go/parser 로 파싱해 **문자열 리터럴만** 모은다. 주석은 AST 노드가 아니라
//     자동으로 빠지고, 이스케이프도 strconv 가 푼다.
//   - .sql 은 `--` 라인 주석만 걷어낸다.
//
// 리터럴을 개행으로 잇는 이유: 조각들이 붙어 우연히 `UPDATE item SET` 을 만들어 내는
// 가짜 매치를 막는다.
func itemBodyExecutableSQL(t *testing.T, path, rel string, src []byte) string {
	t.Helper()

	if strings.HasSuffix(rel, ".sql") {
		var b strings.Builder
		for _, ln := range strings.Split(string(src), "\n") {
			if i := strings.Index(ln, "--"); i >= 0 {
				ln = ln[:i]
			}
			b.WriteString(ln)
			b.WriteString("\n")
		}
		return b.String()
	}

	fset := token.NewFileSet()
	// 주석은 안 받는다(ParseComments 없음) — 위 ★ 가 그 이유다.
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		// 빌드되는 레포라 여기 오면 안 된다. 조용히 건너뛰면 그 파일이 영영 안 보인다.
		t.Fatalf("%s 를 파싱 못 했다 — 이 그물이 그 파일을 안 보게 된다: %v", rel, err)
	}
	var b strings.Builder
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, uerr := strconv.Unquote(lit.Value)
		if uerr != nil {
			s = lit.Value // 못 풀면 원문으로 본다 — 안 보는 것보다 낫다
		}
		b.WriteString(s)
		b.WriteString("\n")
		return true
	})
	return b.String()
}

// itemBodyOffenders 는 SET 절 하나에서 본문 컬럼을 대입한 것을 골라낸다.
func itemBodyOffenders(setClause string) []string {
	var hit []string
	for _, frag := range strings.Split(setClause, ",") {
		m := setColumnRe.FindStringSubmatch(frag)
		if m == nil {
			continue
		}
		col := strings.ToLower(m[1])
		for _, bad := range itemBodyColumns {
			if col == bad {
				hit = append(hit, col)
			}
		}
	}
	return hit
}

// TestItemBodyHasSingleWriter 는 살아 있는 SQL 전수에서 item 표의 본문을 고치는 문장을
// 찾아, 그것이 **허용된 한 자리뿐인지** 잰다. 전에는 같은 walk 로 "0건"을 쟀다.
func TestItemBodyHasSingleWriter(t *testing.T) {
	root := itemBodyGuardRoot(t)

	var offenders []string
	var scanned, inserts, updates, writerHits int

	werr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// 워크트리·빌드 산출물로 들어가면 같은 파일을 여러 번 세고 느려진다.
			switch d.Name() {
			case ".git", ".flightdeck", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		if !itemBodyGuardInScope(rel) {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		scanned++
		sql := itemBodyExecutableSQL(t, p, filepath.ToSlash(rel), b)
		inserts += len(itemInsertRe.FindAllString(sql, -1))

		clauses := itemSetClauseRe.FindAllStringSubmatch(sql, -1)
		updates += len(clauses)
		for _, m := range clauses {
			cols := itemBodyOffenders(m[1])
			if len(cols) == 0 {
				continue
			}
			rels := filepath.ToSlash(rel)
			if rels == amendWriterFile {
				writerHits += len(cols)
				continue
			}
			for _, col := range cols {
				offenders = append(offenders, fmt.Sprintf("%s  UPDATE item SET … %s = …", rels, col))
			}
		}
		// WHERE 로 안 끝나는 UPDATE 는 위 정규식이 통째로 못 본다 — 컬럼을 읽을 수
		// 없으니 본문 여부도 판정 못 한다. 못 본다는 사실 자체를 위반으로 낸다.
		if heads := len(itemUpdateHeadRe.FindAllString(sql, -1)); heads > len(clauses) {
			offenders = append(offenders, fmt.Sprintf(
				"%s  WHERE 로 안 끝나는 `UPDATE item SET` 이 %d건 — 이 그물이 컬럼을 못 읽는다",
				filepath.ToSlash(rel), heads-len(clauses)))
		}
		return nil
	})
	if werr != nil {
		t.Fatalf("전수 훑기가 실패했다: %v", werr)
	}

	// 눈을 뜨고 있는지 본다. 좌표나 그물이 밀리면 offenders 0 은 "깨끗하다"가 아니라
	// "아무것도 안 봤다"인데, 둘은 화면에서 구분되지 않는다.
	if scanned == 0 {
		t.Fatalf("범위 안 파일을 한 개도 못 읽었다 — 이 시험이 아무것도 안 보고 있다")
	}
	if inserts == 0 {
		t.Fatalf("`INSERT INTO item(` 을 한 건도 못 봤다(파일 %d개를 훑었다) — 그물이나 좌표가 밀렸다. "+
			"store/item.go 의 AddItem 이 걸려야 정상이다", scanned)
	}
	if updates == 0 {
		t.Fatalf("`UPDATE item SET` 을 한 건도 못 봤다(파일 %d개를 훑었다) — 그물이 죽었다. "+
			"state·landed_ref 계열이 걸려야 정상이다", scanned)
	}
	if writerHits == 0 {
		t.Fatalf("허용된 자리(%s)에서 본문 UPDATE 를 한 건도 못 봤다(파일 %d개를 훑었다) — "+
			"그물이나 좌표가 밀렸다. 이 상태의 offenders 0 은 '깨끗하다'가 아니라 '아무것도 안 봤다'다",
			amendWriterFile, scanned)
	}

	if len(offenders) > 0 {
		t.Errorf("허용된 자리 밖에서 항목 본문을 고치는 SQL 이 %d 곳이다(파일 %d개, UPDATE %d건을 훑었다):\n  %s\n\n"+
			"이 저장소에서 item 의 title·body·paths 를 고치는 자리는 %s 의 AmendItem 하나다 — "+
			"거기서만 개정 이력(item_revision)이 함께 쌓이기 때문이다.\n"+
			"새 수정 경로가 정말로 필요하면 그 함수를 부르게 하고, 표면을 늘리는 것이라면 "+
			"**DESIGN §11 을 함께 고쳐라** — 안 고치면 설계가 조용히 거짓이 된다.",
			len(offenders), scanned, updates, strings.Join(offenders, "\n  "), amendWriterFile)
	}
}

// TestAmendWriterAlsoWritesRevision 은 본문 UPDATE 와 개정 INSERT 가 **같은 함수** 안에
// 있는지 본다. 파일 단위로 세면 통과하는 구멍이 있다 — 같은 파일의 다른 함수가 이력
// 없이 UPDATE 만 치는 경우다.
func TestAmendWriterAlsoWritesRevision(t *testing.T) {
	root := itemBodyGuardRoot(t)
	path := filepath.Join(root, filepath.FromSlash(amendWriterFile))

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("%s 를 파싱 못 했다: %v", amendWriterFile, err)
	}

	var checked int
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		var sql strings.Builder
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			lit, ok := m.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				s = lit.Value
			}
			sql.WriteString(s)
			sql.WriteString("\n")
			return true
		})
		body := sql.String()
		if len(itemBodyGuardHits(body)) == 0 {
			return true
		}
		checked++
		if !regexp.MustCompile(`(?is)INSERT\s+INTO\s+item_revision\s*\(`).MatchString(body) {
			t.Errorf("%s 의 %s 가 항목 본문을 고치면서 개정 이력을 안 쌓는다.\n"+
				"이력 없는 수정 경로는 item_revision 의 값을 통째로 무효로 만든다 — "+
				"같은 함수 안에서 INSERT INTO item_revision 을 함께 해라",
				amendWriterFile, fn.Name.Name)
		}
		return true
	})

	if checked == 0 {
		t.Fatalf("%s 에서 본문을 고치는 함수를 한 개도 못 찾았다 — "+
			"이 시험이 아무것도 안 보고 있다(함수가 옮겨졌거나 그물이 죽었다)", amendWriterFile)
	}
}

// TestAmendSurfaceIsNamedInDesign 은 §11 이 이 표면을 이름으로 부르는지 본다.
//
// 전에는 같은 자리에서 **부재**를 물었다(TestItemBodyImmutabilityIsNamedInDesign).
// 방향 ② 는 그대로이고 무엇을 찾느냐만 바뀐다 — 부재가 아니라 열린 사실이다.
//
// 아래 문자열은 **앵커**다. 문서의 표현을 바꾸려면 이 시험도 같이 고쳐라 —
// 그 한 번의 수고가 이 관문의 전부이고, 그것이 없으면 관문이 조용히 눈이 먼다.
func TestAmendSurfaceIsNamedInDesign(t *testing.T) {
	p := filepath.Join("..", "..", "..", "DESIGN.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("DESIGN.md 를 못 읽었다(%s) — 이 시험의 좌표가 틀렸다: %v", p, err)
	}
	design := string(b)

	for _, want := range []string{
		"항목 본문(`title`·`body`) 수정", // §11 표의 그 행이 아직 거기 있는가
		"열었다 (2026-09-16)",         // 그 행의 **지위** — 이제 열렸다
		// 무엇이 그 값을 받치는가. **§11 행에만 있는 조각이어야 한다** — 맨
		// "item_revision" 은 §3 의 표 선언 목록에도 걸려서, §11 행에서 통째로 사라져도
		// 초록이 된다(앵커가 공허해지는 전형이다).
		"옛 값은 `item_revision`(증분 016, 추가 전용)에",
	} {
		if strings.Contains(design, want) {
			continue
		}
		t.Errorf("코드에 항목 본문 수정 표면이 있는데 DESIGN 에 %q 가 없다 — "+
			"§11 이 무엇이 열렸고 무엇은 여전히 안 열렸는지를 말해야 한다. "+
			"안 적으면 다음 사람이 경로를 전부 평가하고서야 답에 도달한다", want)
	}
}

// TestItemBodyGuardActuallyCatches 는 그물이 무엇을 잡고 무엇을 통과시키는지 못박는다.
// 전수 시험은 레포가 깨끗하면 초록이라, 그물이 죽어도 그것만으로는 안 보인다.
func TestItemBodyGuardActuallyCatches(t *testing.T) {
	caught := []string{
		`UPDATE item SET body = ? WHERE project = ? AND id = ?`,
		`UPDATE item SET title = ?, body = ? WHERE project = ? AND id = ?`,
		`UPDATE item SET state = ?, title = ? WHERE project = ? AND id = ?`,
		`UPDATE item SET paths = ? WHERE project = ? AND id = ?`,
		"update item\n\t\t\tset  Body = ?\n\t\t\twhere id = ?", // 줄 넘김·대소문자 섞임
	}
	for _, s := range caught {
		if len(itemBodyGuardHits(s)) > 0 {
			continue
		}
		t.Errorf("그물이 놓쳤다: %q", s)
	}

	passed := []string{
		// 살아 있는 UPDATE 전부 — 본문이 아닌 축이다.
		`UPDATE item SET state = ?, close_reason = NULL, closed_at = NULL WHERE project = ? AND id = ?`,
		`UPDATE item SET landed_ref = ? WHERE project = ? AND id = ?`,
		`UPDATE item SET state = 'open' WHERE project = ? AND id = ? AND state = 'claimed'`,
		`UPDATE item SET project = ? WHERE project = ? AND id = ?`,
		// 다른 표다 — item 접두에 걸려서는 안 된다.
		`UPDATE item_dependents SET n = MAX(0, n + ?) WHERE project = ? AND item_id = ?`,
		`UPDATE item_after SET body = ? WHERE project = ?`,
		// 생성은 유일하게 허용된 본문 쓰기다.
		`INSERT INTO item(project, id, title, body, paths, labels, state) VALUES (?,?,?,?,?,?,?)`,
		// 판단 표의 본문은 이 축이 아니다(그쪽은 트리거가 UPDATE 자체를 막는다).
		`UPDATE judgment SET body = '덮어씀' WHERE id = ?`,
	}
	for _, s := range passed {
		if hits := itemBodyGuardHits(s); len(hits) > 0 {
			t.Errorf("그물이 정상 문장을 잡았다: %q (걸린 컬럼 %v)", s, hits)
		}
	}
}

// TestItemBodyGuardIgnoresProse 는 **주석 안의 SQL 인용을 안 잡는다**를 못박는다.
//
// 이 시험이 있는 이유: 처음 판이 정확히 그것을 잡았다(web/actions.go 의 순서 설명).
// 좌표계가 파일 본문에서 실행 SQL 로 좁혀진 것이 이 커밋의 교정이고, 그 교정이
// 되돌려지면 여기가 빨개진다.
func TestItemBodyGuardIgnoresProse(t *testing.T) {
	const src = `package p

// ⓐ ForceReleaseClaim 은 ` + "`UPDATE item SET state='open' … AND state='claimed'`" + ` 를
//   함께 친다. 이 주석은 실행되지 않는다.
//   UPDATE item SET body = ? 라고 적어도 마찬가지다.
const q = ` + "`UPDATE item SET landed_ref = ? WHERE project = ? AND id = ?`" + `
`
	dir := t.TempDir()
	p := filepath.Join(dir, "prose.go")
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatalf("임시 파일을 못 썼다: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("임시 파일을 못 읽었다: %v", err)
	}

	sql := itemBodyExecutableSQL(t, p, "prose.go", b)
	if strings.Contains(sql, "…") {
		t.Errorf("주석의 SQL 인용이 실행 SQL 로 새어 들어왔다:\n%s", sql)
	}
	if hits := itemBodyGuardHits(sql); len(hits) > 0 {
		t.Errorf("주석 안 SQL 인용을 위반으로 잡았다(걸린 컬럼 %v) — "+
			"이 관문은 실행되는 SQL 만 본다. 산문까지 잡으면 문서를 정확히 쓰는 사람을 벌한다", hits)
	}
	// 같은 파일의 **진짜** SQL 은 여전히 보여야 한다 — 안 그러면 위 초록이 공허하다.
	if !strings.Contains(sql, "UPDATE item SET landed_ref") {
		t.Errorf("실행되는 SQL 리터럴을 못 봤다 — 그물이 파일 전체를 놓쳤다:\n%s", sql)
	}
}

// itemBodyGuardHits 는 SQL 텍스트 하나가 무는 본문 컬럼들이다(전수 시험과 같은 판정이다).
func itemBodyGuardHits(sql string) []string {
	var hit []string
	for _, m := range itemSetClauseRe.FindAllStringSubmatch(sql, -1) {
		hit = append(hit, itemBodyOffenders(m[1])...)
	}
	return hit
}
