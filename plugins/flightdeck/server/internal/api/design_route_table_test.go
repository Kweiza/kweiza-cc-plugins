package api

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// 이 시험이 잡으려는 것 — DESIGN §6 의 REST 표는 **정본**이라고 쓰여 있는데, 실제로는
// 표면이 날 때마다 표를 안 고쳐서 조용히 덜 적혀 있었다. 2026-08-11 실측: 표에 적힌 경로
// 20 · 코드의 경로 27 · **표에 없는 라우트 7개**(랜딩 표면 둘, 로그인 표면 셋, 프로젝트
// 표면 둘 — 전부 나중에 난 것들이다). 반대 방향(표에 있는데 코드에 없는 유령)은 0이었다.
//
// ★ 왜 이것이 조용한가. 표가 덜 적힌 것은 **아무도 안 아프다.** 코드는 잘 돌고, 시험도
// 초록이고, 화면도 멀쩡하다. 그런데 사람은 그 표를 **판단의 근거로 쓴다** — 바로 앞
// 회차가 「새 라우트를 낼 것인가」를 이 표를 근거로 정했다(§6 이 `POST /footprints` 를
// 지우고 `/workspaces` 를 남긴 그 대체재 기준). **없는 줄은 근거가 될 수 없는데,
// 없다는 사실 자체가 안 보인다.** 손으로 한 번 더 맞추면 다음 표면이 날 때 또 어긋난다.
//
// ★★ 대조는 **양방향**이다. 누락(코드에 있는데 표에 없다)이 주 목적이지만, 유령(표에
// 있는데 코드에 없다)도 같이 잡는다 — 지워진 라우트가 표에 남아 있으면 그것도 거짓 정본이다.
// §6 이 `POST /footprints` 를 "실제로 코드에서 제거"하며 표에서도 지웠다고 적은 것이
// 그 방향의 규율이고, 여기서 기계가 그것을 잇는다.
//
// ★★ 정본의 범위는 **`internal/api` 의 mux 등록뿐이다.** `internal/web` 은 화면 표면이라
// 별개 mux 이고(`web.go` 의 `GET /{$}`·`POST /actions/…`), §6 의 표는 그것을 안 적는다 —
// 그 표의 축이 REST API 이기 때문이다. 그래서 web 을 정본에 안 넣는다. 넣으면 이 시험이
// 표에게 "화면 라우트도 적어라"를 요구하게 되는데, 그것은 이 표가 하는 말이 아니다.
//
// ★★ 여기서는 정규식을 쓴다 — `cmd/fd/write_cmd_table_coverage_test.go` 가 "정규식을 안
// 쓴 이유"를 길게 적어 뒀지만 그쪽 사정은 **Go 소스에서 값을 역추적**하는 일이었다(지역변수·
// 파라미터를 두 겹 거쳐 오는 명령 이름). 이 파일이 정규식으로 읽는 것은 **마크다운 표**이고
// 거기에는 추적할 값이 없다. 반대편(Go 소스)은 그 선례대로 AST 로 읽는다.

// routeSite 는 라우트 하나가 등록된 자리다(오류가 어디를 고칠지 가리키게 한다).
type routeSite struct {
	file string
	line int
}

func (s routeSite) String() string { return fmt.Sprintf("%s:%d", filepath.Base(s.file), s.line) }

// apiSourceDir 는 이 시험 파일이 사는 디렉토리 — internal/api 자신이다.
// 시험이 어느 cwd 에서 돌든 같은 소스를 보게 한다.
func apiSourceDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("이 시험 파일 자신의 경로를 못 얻었다 — runtime.Caller 실패")
	}
	return filepath.Dir(file)
}

// stripAPIPrefix 는 `/api/v1` 접두를 뗀다.
//
// 표는 헤더에 그 접두를 한 번 적고 각 줄에서는 생략한다. 다만 접두 **밖**의 표면도
// 표에 있으므로(`/healthz`·`/metrics`·`/events` — 그 셋은 애초에 접두가 없다)
// 무조건 떼는 것이 아니라 있을 때만 뗀다.
func stripAPIPrefix(p string) string { return strings.TrimPrefix(p, "/api/v1") }

// registeredRoutes 는 **정본**이다 — internal/api 의 비시험 .go 를 go/ast 로 훑어
// mux 에 실제로 등록된 "METHOD /path" 를 전부 모은다.
//
// ★ 메서드가 없는 패턴(`"/"` — 폴백/캐치올)은 **일부러 뺀다.** 그것은 라우트가 아니라
// 안 맞은 요청이 떨어지는 자리이고, §6 의 표가 적는 축(메서드+경로)이 아니다.
// 뺀다는 사실을 여기 적어 두지 않으면 다음 사람이 "표에 `/` 가 없다"를 결함으로 본다.
//
// ★★ 첫 인자가 문자열 리터럴이 아니면 **그 자리에서 실패한다.** 조용히 건너뛰면 그
// 라우트는 조사 대상에서 빠져 이 시험이 영영 "문제 없음"을 낸다 — 결함을 통과로 착각하는
// 것이 이 시험이 막으려는 바로 그 부류다(write_cmd_table_coverage_test.go 와 같은 규율).
func registeredRoutes(t *testing.T) map[string]routeSite {
	t.Helper()
	dir := apiSourceDir(t)
	entries, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("internal/api 소스 목록 조회 실패: %v", err)
	}

	fset := token.NewFileSet()
	out := map[string]routeSite{}
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		af, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("%s 파싱 실패: %v", path, perr)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
				return true
			}
			if len(call.Args) == 0 {
				return true
			}
			pos := fset.Position(call.Pos())
			site := routeSite{file: path, line: pos.Line}

			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Fatalf("%s: mux.%s 의 첫 인자가 문자열 리터럴이 아니다 — 이 시험은 "+
					"등록된 라우트를 전부 봤다는 전제 위에 있다. 못 읽은 자리를 남기면 "+
					"그 라우트는 조사 대상에서 빠져 이 시험이 영영 초록이다", site, sel.Sel.Name)
			}
			pattern, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				t.Fatalf("%s: 라우트 리터럴을 못 읽었다(%s): %v", site, lit.Value, uerr)
			}
			fields := strings.Fields(pattern)
			if len(fields) != 2 {
				return true // 메서드 없는 폴백(`"/"`) — 위 ★ 참고
			}
			out[fields[0]+" "+stripAPIPrefix(fields[1])] = site
			return true
		})
	}
	if len(out) == 0 {
		t.Fatal("등록된 라우트를 하나도 못 찾았다 — 이 시험의 정본이 사라졌거나 훑는 자리가 틀렸다")
	}
	return out
}

// designPath 는 §6 을 담은 문서다. internal/api → server → flightdeck.
func designPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(apiSourceDir(t), "..", "..", "..", "DESIGN.md")
}

// routeFence 는 표 블록을 여는 표식이다.
//
// ★ 앵커를 절 제목(`### REST …`)이 아니라 **코드펜스의 언어 태그**로 잡은 이유:
// 제목은 사람이 다듬는 자리라 자주 바뀌지만(바로 이 문서의 §7 표제가 최근에 바뀌었다),
// 펜스 태그는 렌더링에 안 보이고 사람이 손댈 이유가 없다. 마크다운은 모르는 언어 태그를
// 그냥 평문으로 내므로 화면도 그대로다.
const routeFence = "```routes"

// documentedRoutes 는 §6 표에 적힌 라우트다.
//
// 쿼리(`?q=`·`?id=<카드>`)는 라우트가 아니라 같은 라우트의 인자라 잘라 낸다 —
// 안 자르면 `GET /sessions?id=` 와 `GET /sessions` 가 다른 것으로 보인다.
// `GET|PUT /snapshots/{key}` 처럼 메서드를 묶어 적은 줄은 각각으로 편다.
func documentedRoutes(t *testing.T) map[string]int {
	t.Helper()
	p := designPath(t)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("DESIGN.md 를 못 읽었다(%s) — 이 시험의 좌표가 틀렸다: %v", p, err)
	}
	lines := strings.Split(string(raw), "\n")

	start := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == routeFence {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("DESIGN.md 에서 %s 블록을 못 찾았다 — 이 시험이 읽을 정본이 없다.\n"+
			"§6 의 REST 표를 여는 코드펜스에 그 표식을 달아라(닫는 펜스는 그대로 ```).", routeFence)
	}

	re := regexp.MustCompile(`\b(GET|POST|PUT|PATCH|DELETE)((?:\|(?:GET|POST|PUT|PATCH|DELETE))*)\s+(/\S*)`)
	out := map[string]int{}
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
			if len(out) == 0 {
				t.Fatalf("%s 블록에서 라우트를 하나도 못 읽었다(DESIGN.md:%d~%d) — "+
					"표식은 있는데 표가 비었거나 어법이 바뀌었다", routeFence, start+1, i)
			}
			return out
		}
		for _, m := range re.FindAllStringSubmatch(lines[i], -1) {
			methods := append([]string{m[1]}, strings.FieldsFunc(m[2], func(r rune) bool { return r == '|' })...)
			path := m[3]
			if q := strings.IndexByte(path, '?'); q >= 0 {
				path = path[:q]
			}
			for _, meth := range methods {
				out[meth+" "+path] = i + 1
			}
		}
	}
	t.Fatalf("%s 블록이 안 닫혔다 — DESIGN.md:%d 이후에 닫는 펜스가 없다", routeFence, start)
	return nil
}

// TestDesignRouteTableMatchesRegisteredRoutes 는 §6 의 표가 실제 라우트와 같은지 잰다.
func TestDesignRouteTableMatchesRegisteredRoutes(t *testing.T) {
	registered := registeredRoutes(t)
	documented := documentedRoutes(t)

	var missing, ghost []string
	for r, site := range registered {
		if _, ok := documented[r]; !ok {
			missing = append(missing, fmt.Sprintf("  %-42s (등록: %s)", r, site))
		}
	}
	for r, line := range documented {
		if _, ok := registered[r]; !ok {
			ghost = append(ghost, fmt.Sprintf("  %-42s (표: DESIGN.md:%d)", r, line))
		}
	}
	sort.Strings(missing)
	sort.Strings(ghost)

	if len(missing) > 0 {
		t.Errorf("표에 없는 라우트 %d건 — §6 이 정본이라고 말하는데 덜 적혔다.\n%s\n"+
			"각 줄에 **왜 있는지**를 한 마디로 붙여 표에 적어라 — 표의 나머지 줄이 이미 그 어법이다"+
			"(`← 클라이언트 0건`, `훅 전용`, `꼬리 전용`).",
			len(missing), strings.Join(missing, "\n"))
	}
	if len(ghost) > 0 {
		t.Errorf("코드에 없는데 표에 있는 라우트 %d건 — 거짓 정본이다.\n%s\n"+
			"라우트를 지웠으면 표에서도 지워라(§6 이 POST /footprints 를 그렇게 지웠다).",
			len(ghost), strings.Join(ghost, "\n"))
	}
}
