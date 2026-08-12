package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/mcpsrv"
)

// ─────────────────────────────────────────────────────────────────────────────
// 동사 하나에 렌더러 하나 — 그리고 그 짝을 세는 자리가 이 패키지에 있어야 한다
// ─────────────────────────────────────────────────────────────────────────────
//
// 이 저장소의 규율은 **MCP 도구 뒤에 있는 CLI 명령이 같은 렌더러를 쓰는 것**이다.
// 그래야 사람과 세션이 같은 답을 받는다.
//
// ★ 그 짝을 강제하는 자리가 없어서 **실제 비용이 두 번 났다**:
//
//   - `label`(2026-08-12) — 계획이 CLI 를 MCP 앞에 뒀고 그때 `RenderLabel` 이 없어서
//     구현자가 출력을 손으로 짰다. 결과: `fd label X --add tickler` 를 친 **사람이**
//     "묶음에서는 선두에 달아야 걸린다" 두 줄을 못 봤다. 세션은 봤다. 그 선두 규칙의
//     침묵이 애초에 그 표면을 만든 사고의 절반이었는데 사람 경로에서 다시 빼먹었다.
//     최종 전수 리뷰가 잡았다 — 작업 단위 리뷰는 구조적으로 못 본다(CLI 작업만 보면
//     옳고 MCP 작업만 봐도 옳다. 둘을 나란히 놔야 갈린다).
//   - `add` — 위를 고치러 와서 발견했다. `RenderAdd` 는 **어느 프로젝트에 들어갔는지** ·
//     되돌리는 명령 · **"본문은 나중에 못 고친다"** 를 내는데 `fd add` 는 한 줄만 냈다.
//     그 셋은 RenderAdd 주석이 실측 사고로 적어 둔 것이다(항목 10건 오등록, id 하나 영구 사망).
//
// `cmd/fd/mcp_seam_test.go` 가 이 결손을 **이미 예측해** 적어 뒀다:
// "아래 표를 mcpsrv.ToolNames() 와 대조하는 자리가 이 패키지에 한 군데도 없다
//  (cmd/fd 전체에 ToolNames·KnownTool·Tools 호출이 0건이다)". 이 파일이 그 0을 깬다.

// toolRenderer 는 도구 하나와 **CLI 가 그 결과를 낼 때 써야 하는 렌더러**의 짝이다.
//
// 값이 빈 문자열이면 **의도적 예외**이고, why 에 근거가 있어야 한다.
// 표는 mcpsrv.ToolNames() 와 양방향으로 대조된다 — 도구가 늘면 여기가 빨개지고,
// 여기 유령이 있어도 빨개진다.
var toolRenderer = map[string]struct {
	renderer string // mcpsrv 의 렌더러 이름. 빈 문자열이면 예외
	why      string // 예외 근거. renderer 가 비면 필수
}{
	"board":  {renderer: "RenderBoard"},
	"pick":   {renderer: "RenderPick"},
	"note":   {renderer: "RenderNote"},
	"add":    {renderer: "RenderAdd"},
	"finish": {renderer: "RenderFinish"},
	"land":   {renderer: "RenderLand"},
	"label":  {renderer: "RenderLabel"},

	// ★ 유일한 예외. `fd alloc <counter>` 는 **숫자 한 줄만** 낸다(`fmt.Fprintf(out, "%d\n", …)`).
	//   `RenderAlloc` 은 "alloc · x = 3" + 산문 한 줄을 내는데, 그것을 CLI 가 쓰면
	//   `n=$(fd alloc contract_revision)` 이 깨진다. 발번의 소비자는 사람이 아니라 셸이다.
	//   MCP 쪽은 사람(에이전트)이 읽으므로 RenderAlloc 을 그대로 쓴다 — 표면마다 소비자가
	//   다르다는 것이 이 예외의 근거이고, 그것은 "같은 답을 준다"의 위반이 아니다.
	"alloc": {why: "fd alloc 은 숫자만 낸다 — $(fd alloc x) 파이프 계약이다. 소비자가 셸이지 사람이 아니다"},
}

// cmdFDRendererCalls 는 cmd/fd 의 **살아 있는** 코드가 부르는 mcpsrv.RenderX 이름들이다.
//
// 정규식이 아니라 AST 인 이유는 write_cmd_table_coverage_test.go 와 같다 — 호출이 줄을
// 넘기거나 변수를 거치면 정규식이 놓친다. 여기서는 SelectorExpr 하나만 보면 되므로
// 그 위험이 작지만, 같은 규율을 따르는 편이 다음 사람에게 덜 놀랍다.
func cmdFDRendererCalls(t *testing.T) map[string]bool {
	t.Helper()
	found := map[string]bool{}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("cmd/fd 를 못 읽었다: %v", err)
	}
	var scanned int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatalf("%s 를 파싱 못 했다 — 이 그물이 그 파일을 안 보게 된다: %v", name, perr)
		}
		scanned++
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "mcpsrv" || !strings.HasPrefix(sel.Sel.Name, "Render") {
				return true
			}
			found[sel.Sel.Name] = true
			return true
		})
	}
	// 눈을 뜨고 있는지 본다 — 0개 파일을 훑고 "짝이 다 없다"로 빨개지면 진단이 거짓이 된다.
	if scanned == 0 {
		t.Fatal("cmd/fd 에서 .go 파일을 한 개도 안 훑었다 — 이 시험이 아무것도 안 보고 있다")
	}
	return found
}

// TestEveryToolIsPairedWithItsCLIRenderer 는 도구 전수와 위 표를 양방향 대조하고,
// 짝이 있는 것은 CLI 가 그 렌더러를 **실제로 부르는지**까지 본다.
func TestEveryToolIsPairedWithItsCLIRenderer(t *testing.T) {
	tools := mcpsrv.ToolNames()
	if len(tools) == 0 {
		t.Fatal("mcpsrv.ToolNames() 가 비었다 — 대조할 정본이 없다")
	}

	// ① 누락: 도구가 표에 없다. 새 도구를 더한 사람을 이 표로 데려오는 문장이 이것이다.
	for _, name := range tools {
		if _, ok := toolRenderer[name]; !ok {
			t.Errorf("도구 %q 가 toolRenderer 표에 없다 — CLI 가 그 결과를 어떤 렌더러로 내는지 "+
				"적어라. CLI 에 대응 명령이 없거나 출력 계약이 달라 예외라면 why 에 근거를 남겨라", name)
		}
	}

	// ② 유령: 표에만 있고 도구에는 없다(오타·삭제된 도구).
	known := map[string]bool{}
	for _, name := range tools {
		known[name] = true
	}
	var ghosts []string
	for name := range toolRenderer {
		if !known[name] {
			ghosts = append(ghosts, name)
		}
	}
	sort.Strings(ghosts)
	for _, g := range ghosts {
		t.Errorf("toolRenderer 표의 %q 는 도구가 아니다 — 오타이거나 지워진 도구다(지금 도구: %v)", g, tools)
	}

	// ③ 짝이 실제로 성립하는가.
	calls := cmdFDRendererCalls(t)
	for _, name := range tools {
		pair, ok := toolRenderer[name]
		if !ok {
			continue // ①이 이미 보고했다
		}
		if pair.renderer == "" {
			if strings.TrimSpace(pair.why) == "" {
				t.Errorf("도구 %q 가 예외인데 why 가 비었다 — 근거 없는 예외는 다음 사람이 "+
					"결함인지 판정할 수 없다", name)
			}
			continue
		}
		if !calls[pair.renderer] {
			t.Errorf("도구 %q 의 CLI 경로가 mcpsrv.%s 를 안 부른다 — 손으로 짠 출력이면 "+
				"사람과 세션이 다른 답을 받는다(label·add 가 정확히 그렇게 갈렸다). "+
				"렌더러를 쓰거나, 출력 계약이 정말 달라야 한다면 표에서 예외로 옮기고 why 를 적어라",
				name, pair.renderer)
		}
	}
}

// TestDegradeTableCoversEveryTool 은 mcp_seam_test.go 의 **서버 미도달 표**가 도구 전수를
// 덮는지 본다. 그 파일이 스스로 요청한 대조다:
//
//	"아래 표를 mcpsrv.ToolNames() 와 대조하는 자리가 이 패키지에 한 군데도 없다 —
//	 실측: alloc 행 하나를 지워도 이 시험이 초록으로 통과한다."
//
// 표는 시험 함수 안의 지역 슬라이스라 AST 로 뽑는다. 도구 이름 문자열을 파일 전체에서
// grep 하는 방식으로는 부족하다 — 그 이름이 주석이나 다른 호출에 있어도 통과하므로
// "행 하나를 지워도 초록"이 그대로 남는다.
func TestDegradeTableCoversEveryTool(t *testing.T) {
	const (
		file = "mcp_seam_test.go"
		fn   = "TestMCPToolsDegradeExplicitlyWhenServerIsDown"
	)
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%s 를 못 읽었다 — 이 시험의 좌표가 틀렸다: %v", file, err)
	}
	fset := token.NewFileSet()
	f, perr := parser.ParseFile(fset, filepath.Base(file), src, 0)
	if perr != nil {
		t.Fatalf("%s 를 파싱 못 했다: %v", file, perr)
	}

	covered := map[string]bool{}
	var sawTable bool
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Name == nil || fd.Name.Name != fn {
			return true
		}
		// 그 함수 안에서 `[]struct{…}{…}` 리터럴을 찾는다. 각 원소의 **첫 필드**가 도구 이름이다.
		ast.Inspect(fd, func(m ast.Node) bool {
			lit, ok := m.(*ast.CompositeLit)
			if !ok {
				return true
			}
			arr, ok := lit.Type.(*ast.ArrayType)
			if !ok {
				return true
			}
			if _, ok := arr.Elt.(*ast.StructType); !ok {
				return true
			}
			sawTable = true
			for _, el := range lit.Elts {
				row, ok := el.(*ast.CompositeLit)
				if !ok || len(row.Elts) == 0 {
					continue
				}
				bl, ok := row.Elts[0].(*ast.BasicLit)
				if !ok || bl.Kind != token.STRING {
					continue
				}
				s, uerr := strconv.Unquote(bl.Value)
				if uerr != nil {
					continue
				}
				covered[s] = true
			}
			return true
		})
		return false
	})

	// 눈을 뜨고 있는지 본다. 함수 이름이 바뀌거나 표 모양이 바뀌면 covered 가 비는데,
	// 그것을 "전부 미덮음"으로 보고하면 진단이 엉뚱한 곳을 가리킨다.
	if !sawTable {
		t.Fatalf("%s 의 %s 에서 []struct{…} 표를 못 찾았다 — 함수 이름이나 표 모양이 바뀌었다. "+
			"이 시험의 좌표를 고쳐라(그냥 두면 아무것도 안 보면서 초록이 된다)", file, fn)
	}

	for _, name := range mcpsrv.ToolNames() {
		if !covered[name] {
			t.Errorf("도구 %q 가 %s 의 서버-미도달 표에 없다 — 그 도구가 오프라인에서 무엇을 하는지 "+
				"아무도 안 재고 있다. 표에 행을 더해라(%s)", name, file, fn)
		}
	}
}
