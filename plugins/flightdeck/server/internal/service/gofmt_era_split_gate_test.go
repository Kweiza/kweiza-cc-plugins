package service

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// gofmt 판 갈림 관문 — 옆의 gofmt_gate_test.go 가 못 보는 축
// ─────────────────────────────────────────────────────────────────────────────
//
// gofmt_gate_test.go 는 **이 머신의 툴체인**이 정본이라고 보는 형식을 강제한다.
// 그런데 그 정본이 Go 판마다 다르다(2026-09-02 실측):
//
//	맥    go1.25.6 darwin/arm64 : gofmt -l . → 0건
//	리눅스 go1.27.1 linux/amd64  : gofmt -l . → internal/mcpsrv/render_failures_fold_test.go
//
// 그리고 **양방향으로 밀어낸다** — 1.27 이 만든 형식을 1.25.6 에 주면 되돌린다.
// 즉 "최신으로 맞춘다"가 성립하지 않는다. 08-12 에 관문 다섯 줄을 전부 초록으로
// 통과해 랜딩한 파일이, 08-29~09-02 리눅스 툴체인 교체만으로 **소급해서** 위반이 됐고
// 그 사이 나간 8판이 전부 리눅스에서 빨갛다. 아무도 커밋을 잘못하지 않았다.
//
// 원인은 go/printer.indentList 하나다. 1.27 이 CL 752220(f142be8f, Fixes #7195,
// 2014년 제기·12년 묵혀 1.27 마일스톤에서 닫힘)으로 "다중행 원소 셈"에서 복합
// 리터럴을 빼기 시작했다. 릴리스 노트는 1.26·1.27 둘 다 gofmt 를 **한 글자도 안 적었다**
// (grep 0건) — 다음 판에서 또 조용히 갈릴 것으로 봐야 한다.
//
// 이 관문이 무는 것: 다중값 `return` 중, 두 규칙의 답이 **갈리는** 자리.
// 무는 대상을 이보다 넓히면 안 된다 — "다중행 복합 리터럴이 낀 다중값 return" 으로
// 잡으면 이 모듈에서 53건 중 51건이 오탐이다(멀쩡한 코드를 막는다). 좁히면 안 된다 —
// "다중행 복합 리터럴 둘 이상" 으로 잡으면 litAndCall 꼴을 통째로 놓친다.
//
// indentList 는 **ReturnStmt 에서만** 불린다(2026-09-02, 양 판 nodes.go 에서 호출부가
// 하나뿐임을 확인: 1.25 는 1426행, 1.27 은 1443행, 둘 다 `case *ast.ReturnStmt:` 안).
// 그래서 함수 인자·대입·var 선언은 이 축에 안 걸린다(붙박이 notAReturn 이 그것을 잡는다).

// ── go/printer 이식 ─────────────────────────────────────────────────────────
// 아래 셋은 go/printer/nodes.go 에서 **그대로** 옮긴 것이다. 손으로 요약하지 않았다 —
// 요약하면 경계가 어긋나고, 경계가 어긋난 관문은 오탐으로 죽거나 누락으로 장식이 된다.
// 이식이 실물과 맞는지는 TestIndentListPortMatchesTheRunningGofmt 가 매 실행 재검증한다.

func stripParensAlways(x ast.Expr) ast.Expr {
	if x, ok := x.(*ast.ParenExpr); ok {
		return stripParensAlways(x.X)
	}
	return x
}

// isCompositeLitLike 는 go1.27 이 새로 들인 술어다(1.26 이하에는 없다).
func isCompositeLitLike(x ast.Expr) bool {
	switch x := stripParensAlways(x).(type) {
	case *ast.CompositeLit:
		return true
	case *ast.UnaryExpr:
		_, ok := stripParensAlways(x.X).(*ast.CompositeLit)
		return x.Op == token.AND && ok
	}
	return false
}

// printerIndentsList 는 go/printer 의 (*printer).indentList 다.
// skipCompositeLit=false 가 go1.26 이하 규칙, true 가 go1.27 이상 규칙 —
// 두 판의 차이는 아래 `&& !(skipCompositeLit && ...)` 한 조각뿐이다.
func printerIndentsList(fset *token.FileSet, list []ast.Expr, skipCompositeLit bool) bool {
	lineFor := func(p token.Pos) int { return fset.Position(p).Line }
	if len(list) >= 2 {
		b := lineFor(list[0].Pos())
		e := lineFor(list[len(list)-1].End())
		if 0 < b && b < e {
			// 결과 목록이 여러 줄에 걸친다
			n := 0 // 다중행 원소 수
			line := b
			for _, x := range list {
				xb := lineFor(x.Pos())
				xe := lineFor(x.End())
				if line < xb {
					// x 가 앞 원소가 끝난 줄보다 뒤에서 시작한다
					return true
				}
				if xb < xe && !(skipCompositeLit && isCompositeLitLike(x)) {
					n++
				}
				line = xe
			}
			return n > 1
		}
	}
	return false
}

// splitsBetweenEras 는 이 return 이 두 규칙 사이에서 갈리는지다.
func splitsBetweenEras(fset *token.FileSet, rs *ast.ReturnStmt) bool {
	return printerIndentsList(fset, rs.Results, false) != printerIndentsList(fset, rs.Results, true)
}

// ── 붙박이: 이식이 실물 gofmt 와 맞는지를 매 실행 재검증한다 ─────────────────
//
// ★ 이것이 없으면 위 이식은 근거가 안 따라오는 수다 —
//   "근거가 안 따라오는 수를 관문에 들이면 그것은 관문이 아니라 장식이다"(repo_hooks_test.go).
//
// 붙박이 원문은 전부 **go1.27 정규형으로 못박아** 뒀다(2026-09-02, go1.27.0 gofmt 로 생성).
// 그래서 실행 툴체인이:
//   · 1.27 규칙이면 → 갈리는 붙박이도 format.Source 가 **안 바꾼다**
//   · 1.26 이하 규칙이면 → 갈리는 붙박이만 **바꾼다**(덧들여쓰기를 넣는다)
// 안 갈리는 붙박이는 어느 규칙에서도 정규형이라 양쪽 다 안 바꾼다.
//
// 그리고 붙박이들의 표가 **한 규칙으로 일관되지 않으면** 여기서 죽는다 — go1.28 이
// 또 조용히 규칙을 바꾸면 "몰래 통과"가 아니라 "우리가 아는 두 규칙 어느 쪽도 아니다"로
// 빨개진다. 이 관문이 스스로 늙는 것을 아는 유일한 자리다.

type eraFixture struct {
	name string
	why  string
	src  string
}

var eraFixtures = []eraFixture{
	{
		name: "twoLits",
		why:  "#7195 원문 꼴. 이 저장소가 실제로 밟은 것(render_failures_fold_test.go).",
		src: `package p

func f() (T, T) {
	return T{
		A: 1,
	}, T{
		A: 2,
	}
}
`,
	},
	{
		name: "ptrLits",
		why:  "포인터를 벗겨도 속이 복합 리터럴이면 1.27 은 셈에서 뺀다.",
		src: `package p

func f() (*T, *T) {
	return &T{
		A: 1,
	}, &T{
		A: 2,
	}
}
`,
	},
	{
		name: "parenLits",
		why:  "괄호도 벗긴다(stripParensAlways).",
		src: `package p

func f() (T, T) {
	return (T{
		A: 1,
	}), (T{
		A: 2,
	})
}
`,
	},
	{
		name: "litAndCall",
		why:  "복합 리터럴이 **하나만** 있어도 갈린다 — 2 였던 n 이 1 로 떨어진다.",
		src: `package p

func f() (T, string) {
	return T{
		A: 1,
	}, g(
		"x",
	)
}
`,
	},
	{
		name: "oneMultiline",
		why:  "다중행 결과가 하나뿐이면 두 규칙이 같다. 이 저장소의 나머지 51자리가 이 꼴이다.",
		src: `package p

func f() (error, T) {
	return nil, T{
		A: 1,
	}
}
`,
	},
	{
		name: "twoCalls",
		why:  "복합 리터럴이 없으면 셈이 안 줄어 두 규칙이 같이 들여쓴다.",
		src: `package p

func f() (string, string) {
	return g(
			"x",
		), g(
			"y",
		)
}
`,
	},
	{
		name: "nestedLits",
		why:  "호출 **안**에 든 리터럴은 안 벗긴다 — 두 규칙이 같다.",
		src: `package p

func f() (T, T) {
	return id(T{
			A: 1,
		}), id(T{
			A: 2,
		})
}
`,
	},
	{
		name: "newlineSeparated",
		why:  "결과 사이에 줄바꿈이 있으면 첫 갈래(line < xb)가 먼저 참이라 두 규칙이 같다.",
		src: `package p

func f() (T, T) {
	return T{
			A: 1,
		},
		T{
			A: 2,
		}
}
`,
	},
	{
		name: "oneLine",
		why:  "다중행이 아니면 애초에 셈에 안 든다.",
		src: `package p

func f() (T, T) {
	return T{A: 1}, T{A: 2}
}
`,
	},
	{
		name: "notAReturn",
		why:  "indentList 는 ReturnStmt 에서만 불린다 — 대입은 안 갈린다.",
		src: `package p

func f() {
	a, b := T{
		A: 1,
	}, T{
		A: 2,
	}
	_, _ = a, b
}
`,
	},
	{
		name: "singleResult",
		why:  "결과가 하나면 len(list) >= 2 에서 막힌다.",
		src: `package p

func f() T {
	return T{
		A: 1,
	}
}
`,
	},
	{
		name: "threeWithTwoCalls",
		why:  "리터럴을 빼도 n 이 2 로 남으면 안 갈린다.",
		src: `package p

func f() (string, string, T) {
	return g(
			"x",
		), g(
			"y",
		), T{
			A: 1,
		}
}
`,
	},
}

const (
	eraOld = "go1.26 이하 규칙(복합 리터럴도 다중행으로 센다)"
	eraNew = "go1.27 이상 규칙(복합 리터럴을 다중행 셈에서 뺀다)"
)

func TestIndentListPortMatchesTheRunningGofmt(t *testing.T) {
	votes := map[string][]string{}
	for _, fx := range eraFixtures {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, fx.name+".go", fx.src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("붙박이 %s 가 안 파싱된다: %v", fx.name, err)
		}
		old, neo := false, false
		ast.Inspect(file, func(n ast.Node) bool {
			rs, ok := n.(*ast.ReturnStmt)
			if !ok {
				return true
			}
			old = old || printerIndentsList(fset, rs.Results, false)
			neo = neo || printerIndentsList(fset, rs.Results, true)
			return true
		})

		got, ferr := format.Source([]byte(fx.src))
		if ferr != nil {
			t.Fatalf("붙박이 %s 를 format.Source 가 거절한다: %v", fx.name, ferr)
		}
		changed := !bytes.Equal(got, []byte(fx.src))
		if changed && strings.Join(strings.Fields(string(got)), " ") != strings.Join(strings.Fields(fx.src), " ") {
			t.Fatalf("붙박이 %s 가 **들여쓰기 말고 다른 이유로** 안 정규형이다 — 이 붙박이는 판 축을 못 잰다:\n%s", fx.name, got)
		}

		if old == neo {
			// 두 규칙이 같다고 본 자리. 원문은 어느 규칙에서도 정규형이어야 한다.
			if changed {
				t.Errorf("붙박이 %s: 이식은 두 규칙이 같다고 봤는데(%v) 실물 gofmt(%s)가 바꿨다 — 이식이 틀렸다.\n%s\n실물:\n%s",
					fx.name, old, runtime.Version(), fx.src, got)
			}
			continue
		}
		if changed {
			votes[eraOld] = append(votes[eraOld], fx.name)
		} else {
			votes[eraNew] = append(votes[eraNew], fx.name)
		}
	}

	// 갈리는 붙박이가 없으면 위 표는 아무것도 안 잰다 — 이식에서 두 규칙을 같게
	// 만드는 변이가 여기서 죽는다. 2026-09-02 실측 4개(twoLits·ptrLits·parenLits·litAndCall).
	if n := len(votes[eraOld]) + len(votes[eraNew]); n != 4 {
		t.Fatalf("두 규칙이 갈리는 붙박이가 %d개다(기대 4) — 이식이 두 규칙을 뭉갰거나 붙박이가 밀렸다: %v", n, votes)
	}
	if len(votes) != 1 {
		t.Fatalf("실행 툴체인 %s 가 우리가 아는 두 규칙 어느 쪽으로도 일관되지 않는다 — go/printer 가 또 바뀌었다.\n"+
			"이식(printerIndentsList)을 실물에 다시 맞춰라. 표: %v", runtime.Version(), votes)
	}
	for era, names := range votes {
		t.Logf("실행 툴체인 %s = %s (갈리는 붙박이 %d개로 판정: %v)", runtime.Version(), era, len(names), names)
	}
}

// TestNoMultiValueReturnSplitsBetweenGofmtEras 가 본체다.
//
// 고치는 법(셋 다 2026-09-02 에 go1.25.6·go1.27.0·go1.27.1 세 판에서 바이트 동일함을
// 실측했다). 어느 판을 이기게 할지 **고르지 않는** 것이 요점이다:
//
//	① 리터럴을 지역 변수로 뽑고 `return a, b` — 권장. 구조가 자명하고 되돌림이 어렵다.
//	② 결과 사이에 줄바꿈을 넣는다(`}, \n\t\tT{` → 첫 갈래가 먼저 참이 된다) — 가장 작다.
//	   다만 다음 사람이 줄을 붙이면 조용히 되살아난다.
//	③ 리터럴을 한 줄로 접는다 — 짧을 때만.
//
// `gofmt -w` 로 때우지 마라. 그것은 **이 머신의 판**에 맞추는 것이라, 다른 판의
// 머신에서 그대로 빨개진다(그리고 그쪽에서 되돌리면 여기가 빨개진다).
func TestNoMultiValueReturnSplitsBetweenGofmtEras(t *testing.T) {
	root := filepath.Join("..", "..") // internal/service → 모듈 루트(server/)
	// 루트 계산이 밀렸는지는 **수로 재지 않는다** — internal/service 하나만 훑어도
	// 파일 82개·다중값 return 240개가 나와서(2026-09-02 실측) 어떤 수 문턱도 샌다.
	// 모듈 루트인지를 go.mod 로 직접 못박는다.
	if _, serr := os.Stat(filepath.Join(root, "go.mod")); serr != nil {
		t.Fatalf("순회 루트 %q 에 go.mod 가 없다 — 이 관문의 좌표가 밀렸다: %v", root, serr)
	}
	walked, multi := 0, 0
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		walked++
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if perr != nil {
			// 문법이 깨진 파일은 이 관문의 축이 아니다 — 컴파일이 먼저 죽는다.
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			rs, ok := n.(*ast.ReturnStmt)
			if !ok || len(rs.Results) < 2 {
				return true
			}
			multi++
			if splitsBetweenEras(fset, rs) {
				offenders = append(offenders, fmt.Sprintf("  %s:%d", path, fset.Position(rs.Return).Line))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("모듈 순회 실패: %v", err)
	}
	// 0건 순회는 "깨끗하다"가 아니라 "아무것도 안 봤다"다 — 옆 gofmt 관문과 같은 가드.
	if walked < 50 {
		t.Fatalf("순회한 .go 파일이 %d개뿐이다 — 루트 계산이 밀렸다(2026-09-02 실측 477)", walked)
	}
	// 파일은 다 읽었는데 return 을 0개 봤다면 훑기가 죽은 것이다. 파일 수 가드만으로는
	// 이 눈멂이 안 잡힌다 — 이 관문이 실제로 세는 것은 파일이 아니라 다중값 return 이다.
	if multi < 200 {
		t.Fatalf("다중값 return 을 %d개밖에 못 봤다 — AST 훑기가 밀렸다(2026-09-02 실측 1442)", multi)
	}
	if len(offenders) > 0 {
		t.Fatalf("gofmt 판마다 답이 갈리는 다중값 return %d자리 — 이 자리는 go1.26 이하와 go1.27 이상이\n"+
			"**서로 다른 정본**을 주장한다(둘 다 gofmt -w 로는 못 닫는다. 위 독스트링의 ①②③ 중 하나로 고쳐라).\n"+
			"이 시험을 돌린 툴체인: %s / 파일 %d개·다중값 return %d개를 봤다.\n%s",
			len(offenders), runtime.Version(), walked, multi, strings.Join(offenders, "\n"))
	}
}
