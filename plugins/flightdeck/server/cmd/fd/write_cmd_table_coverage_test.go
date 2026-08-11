package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// 이 시험이 잡으려는 것 — offline.go 의 JudgeOffline(열화)과 outbox.go 의
// IdempotencyStable(멱등 키 안정성)은 **명령 이름으로 갈리는 switch** 다. 새 쓰기 명령이
// 그 표에 안 오르면 둘 다 `default` 로 떨어지는데, 그 기본값이 하필 **안전한 방향**
// (거절 · 새 키)이라 아무도 안 아프고 그래서 아무도 못 본다. CmdProjectRemove 주석이
// 이미 이 함정을 이름으로 적어 뒀다("표 밖으로 떨어뜨려 default 를 태우지 않는다") —
// 이 시험은 그 규율을 손이 아니라 기계로 지킨다.
//
// ★★ 정본을 무엇으로 삼았나 — main.go 의 서브명령 문자열이 아니라
// `a.cli.Write(ctx, <cmd>, …)` 호출의 **명령 인자**다. 둘은 다른 문자열일 수 있다:
// `fd project rm` 은 main.go 에서 `"project"` 로 갈리지만, 두 표가 실제로 보는 값은
// project.go 의 `a.cli.Write(ctx, CmdProjectRemove, …)`(`"project-remove"`)다. 두 표의
// 소비자는 오직 `Client.Write`(client.go)뿐이고, 그 함수가 보는 것은 `cmd` 인자 그
// 자체이지 사람이 셸에 친 서브명령 이름이 아니다 — main.go 를 정본으로 삼으면 이 어긋남
// 자체를 놓친다.
//
// ★★ 정규식을 안 쓴 이유 — internal/store/project_ref_counts_test.go 의
// TestProjectRefTablesCoverEveryProjectColumn 이 이미 같은 결의 선례다: landing_queue 가
// projectRefTables 목록에서 처음에 빠졌던 결함이 정확히 "파일을 정규식/눈으로 훑는" 방식의
// 산물이었고, 그 시험은 살아 있는 DB 스키마를 직접 읽어 그 함정을 없앴다. 이 파일의 사정은
// 더 나쁘다: 명령 이름이 리터럴로 한 줄에 있는 경우(`"note"`)도 있지만, 지역변수를 거치거나
// (`runLand` 의 `cmd`는 분기마다 `CmdLandAcquire`·`CmdLandReport`·`CmdLandLeave` 로 갈린다)
// 함수 파라미터를 두 겹 거쳐 오는 경우(`mcpBackend.write` 의 `cmd` → `mcpBackend.land` 의
// `cmd` → `Land`/`LandReport`/`LandLeave` 의 리터럴)도 있다. 정규식은 값이 어디서 왔는지
// 모르니 이런 경우를 통째로 놓치거나(문자열이 그 줄에 없다) 엉뚱한 것을 줍는다. AST 는
// 대입문·파라미터를 그대로 따라갈 수 있어 값을 잃지 않는다.
//
// ★★ 못 따라가는 표현식은 **조용히 건너뛰지 않고 그 자리에서 실패한다**(resolve 참고).
// 이 시험의 신뢰는 "명령 이름을 전부 봤다"는 전제 위에 있다 — 하나라도 못 풀고 넘어가면
// 그 명령은 조사 대상에서 빠져 항상 "문제 없음"처럼 보인다. 결함을 통과로 착각하는 것이
// 이 시험이 막으려는 바로 그 부류라, 여기서 같은 실수를 반복하지 않는다.
//
// 대조는 양방향이다:
//   - 정본(Write 로 나가는 명령)이 두 표에 전부 있는가 — **누락, 이것이 주 목적**이다.
//   - 두 표에 있는 이름이 전부 정본에서 나오는가 — **유령**(오타·죽은 갈래)을 잡는다.
//     정당한 예외(Write 를 안 타지만 다른 경로로 실제 쓰이는 이름)는 아래
//     judgeOfflineGhostExceptions·idempotencyStableGhostExceptions 에 근거와 함께만 둔다.

// cmdSite 는 명령 이름 하나가 나온 자리다(오류 메시지가 어디를 고칠지 가리키게 한다).
type cmdSite struct {
	cmd  string
	file string
	line int
}

func (s cmdSite) String() string { return fmt.Sprintf("%s:%d", s.file, s.line) }

// cmdFacts 는 cmd/fd 소스를 한 번 훑어 얻은 세 사실이다.
type cmdFacts struct {
	// write 는 **정본**이다 — a.cli.Write(또는 그 자리를 대신하는 b.app.cli.Write) 로
	// 실제 나간 명령 이름 → 그 이름이 나온 자리 전부.
	write map[string][]cmdSite
	// judgeOffline · idempotencyStable 은 각 표의 case 값 → 그 case 가 나온 자리.
	judgeOffline      map[string]cmdSite
	idempotencyStable map[string]cmdSite
}

// cmdFDSourceDir 는 이 시험 파일이 사는 디렉토리다 — cmd/fd 자신. 시험이 어느 cwd 에서
// 돌든(go test ./... 든 이 디렉토리에서 직접이든) 같은 소스를 보게 한다.
func cmdFDSourceDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("이 시험 파일 자신의 경로를 못 얻었다 — runtime.Caller 실패")
	}
	return filepath.Dir(file)
}

// extractCmdFacts 는 cmd/fd 의 **비시험** .go 전부를 go/ast 로 파싱해 세 집합을 뽑는다.
// 시험 파일을 뺀다 — 이 시험이 자기 자신의 리터럴·case 를 정본으로 섞어 세면 순환이다.
func extractCmdFacts(t *testing.T) cmdFacts {
	t.Helper()
	dir := cmdFDSourceDir(t)
	entries, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("cmd/fd 소스 목록 조회 실패: %v", err)
	}

	fset := token.NewFileSet()
	var files []*ast.File
	consts := map[string]string{}
	funcsByName := map[string][]*ast.FuncDecl{}

	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		af, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("%s 파싱 실패: %v", path, perr)
		}
		files = append(files, af)

		for _, decl := range af.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if d.Tok != token.CONST {
					continue
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						lit, ok := vs.Values[i].(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							continue
						}
						v, uerr := strconv.Unquote(lit.Value)
						if uerr != nil {
							t.Fatalf("%s 의 const %s 문자열을 못 읽었다: %v", path, name.Name, uerr)
						}
						// ★ 같은 이름이 다른 값으로 두 번 정의되면(플랫폼별 빌드 태그
						// 파일이 갈라 정의하는 경우가 그 예다) 이름만 보는 이 시험은
						// 어느 쪽인지 모른다 — 조용히 하나를 택하지 않고 실패한다.
						if old, dup := consts[name.Name]; dup && old != v {
							t.Fatalf("상수 %s 가 %q 와 %q 두 값으로 정의돼 있다(%s) — "+
								"이 시험은 이름만으로 상수를 찾아 어느 값인지 못 가른다",
								name.Name, old, v, path)
						}
						consts[name.Name] = v
					}
				}
			case *ast.FuncDecl:
				funcsByName[d.Name.Name] = append(funcsByName[d.Name.Name], d)
			}
		}
	}

	type callSite struct {
		call *ast.CallExpr
		enc  *ast.FuncDecl
	}
	var allCalls []callSite
	for _, af := range files {
		for _, decl := range af.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if ce, ok := n.(*ast.CallExpr); ok {
					allCalls = append(allCalls, callSite{ce, fd})
				}
				return true
			})
		}
	}

	calleeName := func(ce *ast.CallExpr) string {
		switch fn := ce.Fun.(type) {
		case *ast.SelectorExpr:
			return fn.Sel.Name
		case *ast.Ident:
			return fn.Name
		}
		return ""
	}

	funcParams := func(fd *ast.FuncDecl) []string {
		var out []string
		for _, field := range fd.Type.Params.List {
			if len(field.Names) == 0 {
				out = append(out, "_") // 이름 없는 파라미터 — 우리가 찾는 cmd 는 늘 이름이 있다
				continue
			}
			for _, n := range field.Names {
				out = append(out, n.Name)
			}
		}
		return out
	}

	// resolve 는 표현식 하나가 낼 수 있는 문자열 값 **전부**를 뽑는다. 재귀는 셋만 안다:
	//   리터럴 · 최상위 문자열 상수 · (지역변수 대입 또는 함수 파라미터를 거쳐) 그 둘로 접히는 것.
	//
	// ★ 지역변수: cmds.go 의 runLand 는 `cmd, req := CmdLandAcquire, …` 로 시작해 분기마다
	// `cmd = CmdLandReport` 식으로 **다시 대입**한다. 그래서 대입문 전부(정의 `:=`·재대입 `=`)를
	// 훑어 **가능한 값 전부**를 모은다 — 순서(제어 흐름)는 안 보고 집합만 본다. 이 시험이
	// 궁금한 것은 "이 cmd 가 표에 있는가"이지 "지금 실행에서 어느 값이 되는가"가 아니므로
	// 집합으로 충분하다.
	//
	// ★ 함수 파라미터: mcpbackend.go 의 `(b *mcpBackend) write(ctx, cmd, path, body)` 는
	// `b.app.cli.Write(ctx, cmd, …)` 를 부르는데 그 `cmd` 는 이 함수 **자신의 파라미터**다.
	// 값을 알려면 `write` 를 부르는 자리(`b.write(ctx, "beat", …)` 등, 그리고 `land` 를 거쳐
	// 또 한 겹 간접인 것도 있다)를 전부 찾아 그 인자를 같은 방식으로 푼다. 함수 이름만으로
	// 호출부를 맞춘다(go/types 없이) — 그래서 아래에서 동명 함수가 하나뿐인지 먼저 확인한다.
	//
	// ★ 그 밖의 형태(함수 호출 결과·필드 접근 등)를 만나면 **조용히 건너뛰지 않고 실패한다.**
	var resolve func(expr ast.Expr, enc *ast.FuncDecl, depth int) []string
	resolve = func(expr ast.Expr, enc *ast.FuncDecl, depth int) []string {
		if depth > 12 {
			t.Fatalf("%s: 표현식 해석이 %d 단계를 넘었다 — 순환이거나 이 시험이 못 따라가는 간접호출이다",
				fset.Position(expr.Pos()), depth)
		}
		switch e := expr.(type) {
		case *ast.BasicLit:
			if e.Kind == token.STRING {
				v, uerr := strconv.Unquote(e.Value)
				if uerr != nil {
					t.Fatalf("%s: 문자열 리터럴을 못 읽었다: %v", fset.Position(e.Pos()), uerr)
				}
				return []string{v}
			}
		case *ast.Ident:
			if v, ok := consts[e.Name]; ok {
				return []string{v}
			}
			if enc != nil && enc.Body != nil {
				var vals []string
				found := false
				ast.Inspect(enc.Body, func(n ast.Node) bool {
					as, ok := n.(*ast.AssignStmt)
					if !ok {
						return true
					}
					for i, lhs := range as.Lhs {
						id, ok := lhs.(*ast.Ident)
						if !ok || id.Name != e.Name {
							continue
						}
						var rhs ast.Expr
						switch {
						case i < len(as.Rhs):
							rhs = as.Rhs[i]
						case len(as.Rhs) == 1:
							rhs = as.Rhs[0]
						default:
							t.Fatalf("%s: %s 대입의 좌우 개수가 안 맞아 못 따라간다(%d 개 좌변, %d 개 우변)",
								fset.Position(as.Pos()), e.Name, len(as.Lhs), len(as.Rhs))
						}
						found = true
						vals = append(vals, resolve(rhs, enc, depth+1)...)
					}
					return true
				})
				if found {
					return vals
				}
				for i, p := range funcParams(enc) {
					if p != e.Name {
						continue
					}
					decls := funcsByName[enc.Name.Name]
					if len(decls) != 1 {
						t.Fatalf("%s 라는 이름의 함수가 %d개다 — 파라미터 %s 를 채우는 호출부를 "+
							"이름만으로 못 가른다(이 시험은 go/types 없이 이름으로 맞춘다)",
							enc.Name.Name, len(decls), e.Name)
					}
					var out []string
					gotCall := false
					for _, ac := range allCalls {
						if calleeName(ac.call) != enc.Name.Name {
							continue
						}
						if i >= len(ac.call.Args) {
							continue
						}
						gotCall = true
						out = append(out, resolve(ac.call.Args[i], ac.enc, depth+1)...)
					}
					if !gotCall {
						t.Fatalf("%s 의 파라미터 %s 를 채우는 호출부를 하나도 못 찾았다",
							enc.Name.Name, e.Name)
					}
					return out
				}
			}
			t.Fatalf("%s: 식별자 %q 를 리터럴/상수/지역변수/파라미터 어느 것으로도 못 풀었다 — "+
				"이 시험의 resolve 가 못 따라가는 새 형태다", fset.Position(e.Pos()), e.Name)
		}
		t.Fatalf("%s: %T 형태의 표현식은 이 시험이 못 따라간다 — 리터럴이나 식별자가 아니다",
			fset.Position(expr.Pos()), expr)
		return nil
	}

	facts := cmdFacts{
		write:             map[string][]cmdSite{},
		judgeOffline:      map[string]cmdSite{},
		idempotencyStable: map[string]cmdSite{},
	}

	// ── 정본: a.cli.Write / b.app.cli.Write 로 나간 명령 ───────────────────────
	for _, ac := range allCalls {
		sel, ok := ac.call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Write" {
			continue
		}
		// ★ 리시버의 마지막 셀렉터가 "cli" 여야 진짜 Client.Write 다. Sel.Name 만 보면
		// ledger.Write(migrate.go·ledgerbackup.go, 전혀 다른 타입의 동명 메서드)도 걸린다 —
		// 정규식과 같은 실수를 AST 에서 반복하지 않으려고 리시버 모양까지 확인한다.
		recv, ok := sel.X.(*ast.SelectorExpr)
		if !ok || recv.Sel.Name != "cli" {
			continue
		}
		if len(ac.call.Args) < 2 {
			t.Fatalf("%s: Client.Write 호출인데 인자가 %d개다 — 시그니처(ctx, cmd, path, body)가 바뀌었다",
				fset.Position(ac.call.Pos()), len(ac.call.Args))
		}
		pos := fset.Position(ac.call.Pos())
		for _, v := range resolve(ac.call.Args[1], ac.enc, 0) {
			facts.write[v] = append(facts.write[v], cmdSite{cmd: v, file: filepath.Base(pos.Filename), line: pos.Line})
		}
	}
	if len(facts.write) == 0 {
		t.Fatal("Client.Write 호출을 하나도 못 찾았다 — 리시버·셀렉터 판정이 깨졌거나 파일 목록이 비었다")
	}

	// ── 두 표의 case 값 ─────────────────────────────────────────────────────
	extractSwitchCases := func(funcName string) map[string]cmdSite {
		decls := funcsByName[funcName]
		if len(decls) != 1 {
			t.Fatalf("%s 함수가 %d개다 — 정확히 하나여야 이 표를 안전하게 읽는다", funcName, len(decls))
		}
		fd := decls[0]
		out := map[string]cmdSite{}
		sawSwitch := false
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			sawSwitch = true
			for _, stmt := range sw.Body.List {
				cc, ok := stmt.(*ast.CaseClause)
				if !ok {
					t.Fatalf("%s 의 switch 안에 CaseClause 가 아닌 문장이 있다: %T", funcName, stmt)
				}
				if cc.List == nil {
					continue // default 갈래 — 값이 없다
				}
				for _, e := range cc.List {
					pos := fset.Position(e.Pos())
					for _, v := range resolve(e, fd, 0) {
						out[v] = cmdSite{cmd: v, file: filepath.Base(pos.Filename), line: pos.Line}
					}
				}
			}
			return false // 이 switch 는 다 봤다 — 중첩 순회로 이중 계산하지 않는다
		})
		if !sawSwitch {
			t.Fatalf("%s 안에서 switch 문을 못 찾았다 — 함수 모양이 바뀌었다", funcName)
		}
		return out
	}

	facts.judgeOffline = extractSwitchCases("JudgeOffline")
	facts.idempotencyStable = extractSwitchCases("IdempotencyStable")

	return facts
}

// ─────────────────────────────────────────────────────────────────────────────
// 유령 예외 — Write 를 안 타지만 표에 있는 이름
// ─────────────────────────────────────────────────────────────────────────────
//
// ghostException 은 internal/store/migrate_guard_test.go 의 exemption 과 같은 어법이다.
// 그 파일의 주석이 못박아 둔 대로 "사유가 값의 일부다 … 사유를 선택 항목으로 두면 목록은
// 자라고 근거는 안 자란다" — 그래서 reason 을 구조체의 유일한 필드로 두되 **필수**로
// 만든다(아래 반대 방향 시험이 빈 문자열을 거절한다).
//
// ★ 근거 없는 예외는 "정당하다"고 적지 않는다. "claim" 이 그 경계 사례다: 살아 있는
// 호출부를 못 찾았지만(아래 항목 참고), 그렇다고 표에서 지우지도 않는다 — 지우는 쪽이
// 두는 쪽보다 위험하다(내가 못 찾은 호출부가 하나라도 있으면 default 로 떨어지는데,
// 그것이 이 시험 전체가 막으려는 상태다). 그래서 reason 은 "근거를 못 찾았다"는 사실
// 자체를 정직하게 적는다 — "정당화됐다"는 거짓 안심을 주지 않는다.
type ghostException struct {
	reason string
}

// judgeOfflineGhostExceptions·idempotencyStableGhostExceptions 는 각 표의 case 중
// Write 로 안 나가는 이름과 그 근거다. 아래 반대 방향 시험이 "이 이름이 지금도 그
// 표에 있는지"를 마저 재서, 표가 바뀌어 예외가 낡는 것도 잡는다
// (project_ref_counts_test.go 의 knownProjectRefTables 와 같은 규율).
var judgeOfflineGhostExceptions = map[string]ghostException{
	"status": {"읽기 명령이다 — a.cli.Read 로 나가고 Read 는 JudgeOffline 을 아예 안 본다" +
		"(client.go 의 Read 주석: \"이 함수는 JudgeOffline 을 한 번도 안 본다\"). 이 표 자체가 " +
		"\"명령마다 무엇을 하는가\"를 전체 표면(쓰기+읽기+신호)에 대해 적은 것이라(offline.go " +
		"머리 주석의 넷째 축 \"읽기 → 캐시 + 배너\") 읽기 넷이 함께 실려 있다."},
	"board": {"읽기다(위 status 와 같은 사정). 다만 이쪽은 실제로 닿는 자리가 있다 — " +
		"mcpbackend.go 의 (b *mcpBackend) read 가 b.read(ctx, \"board\", \"board\", path) 로 " +
		"불릴 때 캐시 열화 사유를 JudgeOffline(cmd).Reason 에서 그대로 재사용한다(mcpbackend.go:123)."},
	"next": {"읽기다. mcpbackend.go 의 Pick 이 인자 없는 pick(추천, 서버 쪽 표면은 " +
		"GET /items/next)을 b.read(ctx, \"pick\", \"next\", path) 로 보내 cmd=\"next\" 로 " +
		"이 표의 사유를 재사용한다."},
	"doctor": {"읽기다(위 status 와 같은 사정) — 지금은 board·next 처럼 직접 재사용하는 자리는 " +
		"없지만 같은 범주(OfflineCache 처방)라 이 표에서 읽기 넷을 따로 취급하지 않는다."},
	"open": {"OpenSession 오프라인 경로(app.go 의 openSession)는 a.cli.Write 표준 경로를 안 타고 " +
		"자체 캐시 로직을 쓴다 — app.go 의 Rekey 주석이 그 이유를 설명한다(\"Write 는 모르는 " +
		"명령을 거절하므로 세션류는 애초에 그 경로를 안 쓴다\"). 다만 사유 문구는 " +
		"mcpbackend.go:210 의 JudgeOffline(\"open\").Reason 이 직접 불러 재사용한다."},
	// ★ 근거를 못 찾았다. offline.go·outbox.go 둘 다 `case "pick", "claim":` 로 "claim" 을
	// "pick" 과 같이 묶어 두지만, "claim" 을 cmd 값으로 넘기는 자리를 다음 중 어디에도 못
	// 찾았다: Write 호출(리터럴은 늘 "pick" — cmds.go:350, mcpbackend.go 의 Pick),
	// JudgeOffline·IdempotencyStable 의 직접 호출(mcpbackend.go:123,210 은 "board"·
	// "next"·"open" 만 쓴다), mcpBackend.read 의 cmd 인자(마찬가지로 board·next 만).
	// `git log -S'"claim"' -- cmd/fd/offline.go` 로 보면 이 쌍은 **최초 커밋**(e06d758,
	// 서비스 계층·REST·MCP·대시보드·CLI 를 통째로 얹은 커밋)부터 있었고 그 이후 손댄 적이
	// 없다 — 그 시점의 cmds.go 도 이미 `Write(ctx, "pick", …)` 만 썼다(같은 커밋에서
	// 직접 확인). main.go 의 `case "claim":` 은 다른 이름공간이다 — `fd claim release`
	// 서브명령 dispatch 이고 그 쓰기의 실제 cmd 값은 CmdClaimRelease(`"claim release"`)로
	// 이미 정본에 있다. "pick" 이 REST 표면에서 `.../claim` 경로를 친다는 사실
	// (cmds.go:350) 을 보면 애초에 cmd 이름을 REST 경로 이름("claim")으로 쓸 계획이
	// 있었을 가능성은 있으나 확증하지 못했다 — 추측을 근거로 적지 않는다.
	//
	// 지우지 않는 이유: 표에서 이름을 빼는 쪽이 두는 쪽보다 위험하다. 내가 못 찾은 살아
	// 있는 호출부가 하나라도 있으면 그 명령이 default 로 조용히 떨어지고, 그것이 이
	// 시험 전체가 막으려는 상태다.
	"claim": {"근거를 못 찾았다 — Write 호출에도, JudgeOffline 직접 호출에도, " +
		"mcpBackend.read 의 cmd 인자에도 \"claim\" 을 쓰는 자리가 없다(git log -S 로 " +
		"최초 커밋까지 확인했다). main.go 의 `case \"claim\":` 은 다른 이름공간(`fd claim " +
		"release` 서브명령 dispatch, 실제 cmd 값은 CmdClaimRelease)이라 이 표의 \"claim\" " +
		"을 정당화하지 않는다. 그래도 지우지 않는다 — 없는 것을 두는 쪽이 있는 것을 " +
		"지우는 쪽보다 안전하다(못 찾은 호출부가 실은 있다면, 지우는 순간 그 명령이 " +
		"default 로 떨어진다)."},
}

var idempotencyStableGhostExceptions = map[string]ghostException{
	"open": {"OpenSession 은 IdempotencyStable 도 안 탄다(KeyFor 는 Write 안에서만 불리고 " +
		"open 은 Write 를 안 쓴다 — 위 judgeOfflineGhostExceptions[\"open\"] 참고). 이 항목은 " +
		"살아 있는 호출부가 아니라, 같은 명령 표를 두 표에서 나란히 유지하는 관례를 따른 문서용 " +
		"case 다 — 그 자체가 근거이지만 실행 경로 근거는 아니라는 점을 이 문구가 명시한다."},
	"claim": {"위 judgeOfflineGhostExceptions[\"claim\"] 과 근거가 같다 — 근거를 못 찾았지만 " +
		"지우지 않는다."},
}

// TestWriteCommandsAppearInJudgeOfflineTable 은 **주 목적**이다: Client.Write 로 나가는
// 명령 전부가 JudgeOffline 에 명시 갈래를 갖는지 잰다. 없으면 default(OfflineRefuse)로
// 조용히 떨어지는데, project.go 의 CmdProjectRemove 주석이 적어 둔 대로 그 사유 문구는
// "이 명령은 설계가 안 됐다 = 서버 결함"으로 읽혀 하필 서버가 죽은 순간 사람을 오도한다.
func TestWriteCommandsAppearInJudgeOfflineTable(t *testing.T) {
	facts := extractCmdFacts(t)
	var missing []string
	for cmd, sites := range facts.write {
		if _, ok := facts.judgeOffline[cmd]; ok {
			continue
		}
		missing = append(missing, fmt.Sprintf("%q(%s)", cmd, joinSites(sites)))
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("Write 로 나가는 명령이 JudgeOffline(offline.go) 표에 없다 — 기본값으로 조용히 "+
			"OfflineRefuse 가 붙는다. 서버 미도달일 때 이 명령의 사유가 \"명령의 열화 정책이 "+
			"정의돼 있지 않다\"가 되어 설계 결함처럼 읽힌다:\n  %s", strings.Join(missing, "\n  "))
	}
}

// TestWriteCommandsAppearInIdempotencyStableTable 은 같은 검사를 IdempotencyStable
// (outbox.go)에 대해 한다. 없으면 default(고정하지 않음, 즉 매번 새 키)로 떨어지는데, 이
// 명령이 사실은 고정돼야 하는 쓰기(예: 응답이 매번 같아야 하는 재시도)라면 이 기본값이
// 재시도마다 서버에 새 부작용을 일으킨다 — 방향은 안전(refuse 와 달리 무해에 가깝지만,
// alloc 처럼 고정하면 안 되는 축이 있듯 안 고정하면 안 되는 축도 있을 수 있다는 뜻이다.
func TestWriteCommandsAppearInIdempotencyStableTable(t *testing.T) {
	facts := extractCmdFacts(t)
	var missing []string
	for cmd, sites := range facts.write {
		if _, ok := facts.idempotencyStable[cmd]; ok {
			continue
		}
		missing = append(missing, fmt.Sprintf("%q(%s)", cmd, joinSites(sites)))
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("Write 로 나가는 명령이 IdempotencyStable(outbox.go) 표에 없다 — 기본값으로 "+
			"조용히 \"고정하지 않음\"이 붙는다. 이 명령이 실은 고정돼야 하는 쓰기라면 아무도 "+
			"정하지 않은 멱등 정책이 조용히 붙는다:\n  %s", strings.Join(missing, "\n  "))
	}
}

// TestJudgeOfflineTableHasNoUnexplainedGhostCommands 는 반대 방향이다: JudgeOffline 의
// case 값 중 Write 로 안 나가는 이름이 있는가. 오타(예: "porject-remove")나 죽은 갈래를
// 잡는다. 정당한 예외는 judgeOfflineGhostExceptions 에 근거와 함께만 둔다.
func TestJudgeOfflineTableHasNoUnexplainedGhostCommands(t *testing.T) {
	facts := extractCmdFacts(t)
	var ghosts []string
	for cmd, site := range facts.judgeOffline {
		if _, ok := facts.write[cmd]; ok {
			continue
		}
		if _, ok := judgeOfflineGhostExceptions[cmd]; ok {
			continue
		}
		ghosts = append(ghosts, fmt.Sprintf("%q(%s)", cmd, site))
	}
	sort.Strings(ghosts)
	if len(ghosts) > 0 {
		t.Errorf("JudgeOffline(offline.go) 의 case 가 Write 로 안 나가고 정당한 예외 목록에도 "+
			"없다 — 오타이거나 죽은 갈래일 수 있다. 정당하면 judgeOfflineGhostExceptions 에 "+
			"근거(실제로 이 이름을 쓰는 자리)와 함께 추가하라:\n  %s", strings.Join(ghosts, "\n  "))
	}
	// 반대 방향의 반대: 예외로 적어 둔 이름이 지금도 실제로 표에 있는가. 표가 바뀌면
	// (case 를 지우거나 이름을 바꾸면) 이 예외 목록이 유령을 가리키게 된다.
	for cmd, exc := range judgeOfflineGhostExceptions {
		if strings.TrimSpace(exc.reason) == "" {
			t.Errorf("judgeOfflineGhostExceptions 의 %q 는 사유가 비었다 — 근거 없는 예외는 "+
				"안 둔다(못 찾았으면 그렇다고 적어라)", cmd)
		}
		if _, ok := facts.judgeOffline[cmd]; !ok {
			t.Errorf("judgeOfflineGhostExceptions 의 %q(%s)가 지금 JudgeOffline 표에 없다 — 예외가 낡았다",
				cmd, exc.reason)
		}
	}
}

// TestIdempotencyStableTableHasNoUnexplainedGhostCommands 는 IdempotencyStable 에
// 대한 같은 반대 방향 검사다.
func TestIdempotencyStableTableHasNoUnexplainedGhostCommands(t *testing.T) {
	facts := extractCmdFacts(t)
	var ghosts []string
	for cmd, site := range facts.idempotencyStable {
		if _, ok := facts.write[cmd]; ok {
			continue
		}
		if _, ok := idempotencyStableGhostExceptions[cmd]; ok {
			continue
		}
		ghosts = append(ghosts, fmt.Sprintf("%q(%s)", cmd, site))
	}
	sort.Strings(ghosts)
	if len(ghosts) > 0 {
		t.Errorf("IdempotencyStable(outbox.go) 의 case 가 Write 로 안 나가고 정당한 예외 목록에도 "+
			"없다 — 오타이거나 죽은 갈래일 수 있다. 정당하면 idempotencyStableGhostExceptions 에 "+
			"근거와 함께 추가하라:\n  %s", strings.Join(ghosts, "\n  "))
	}
	for cmd, exc := range idempotencyStableGhostExceptions {
		if strings.TrimSpace(exc.reason) == "" {
			t.Errorf("idempotencyStableGhostExceptions 의 %q 는 사유가 비었다 — 근거 없는 예외는 "+
				"안 둔다(못 찾았으면 그렇다고 적어라)", cmd)
		}
		if _, ok := facts.idempotencyStable[cmd]; !ok {
			t.Errorf("idempotencyStableGhostExceptions 의 %q(%s)가 지금 IdempotencyStable 표에 없다 — 예외가 낡았다",
				cmd, exc.reason)
		}
	}
}

func joinSites(sites []cmdSite) string {
	seen := map[string]bool{}
	var out []string
	for _, s := range sites {
		str := s.String()
		if seen[str] {
			continue
		}
		seen[str] = true
		out = append(out, str)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
