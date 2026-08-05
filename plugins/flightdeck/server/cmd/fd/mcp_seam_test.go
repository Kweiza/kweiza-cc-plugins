package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/mcpsrv"
	"github.com/kweiza/flightdeck/internal/model"
)

// 이 파일이 닫는 것은 **이음매**다 — `fd mcp` 의 도구가 만든 변화가 알림 축까지 나가는가.
//
// 앞선 판에서 MCP 는 service 계층을 직접 받아 **로컬 SQLite 에 직접 썼다.** SSE 허브는
// internal/api 의 server 안에 있으므로 그 쓰기는 발행 지점을 지나가지 않았고,
// 도구 일곱이 에이전트의 유일한 쓰기 표면이라 실제 조정 트래픽의 대부분이
// 알림에서 통째로 사라졌다 — 그리고 그 상태에서 mcpsrv 시험은 전부 초록이었다.
// 그쪽 시험은 **자기 store 에 무엇이 써졌나**를 보고, SSE 시험은 REST 로 이벤트를 만들어 본다.
// 두 반쪽을 각자 고정하는 시험은 그 사이의 틈을 원리적으로 못 본다.
//
// 그래서 여기서는 **운영과 같은 배선**(newApp → newMCPBackend → mcpsrv.New)으로 도구를 부르고,
// 같은 서버의 /events 구독에 프레임이 오는지 단정한다.

// mcpRig 는 하네스가 띄운 실물 서버에 붙는 `fd mcp` 한 벌이다.
type mcpRig struct {
	srv     *mcpsrv.Server
	project string
	dir     string // CLAUDE_PROJECT_DIR = 워크트리
	env     map[string]string
}

// newMCPRig 는 `fd mcp` 를 운영과 같은 순서로 조립한다.
//
// ★ 가짜 백엔드를 끼우지 않는다. 이 시험이 막으려는 결함이 바로 "배선이 딴 데를 본다" 이고,
// 배선을 시험이 대신 만들면 그 축을 원리적으로 못 본다.
func newMCPRig(t *testing.T, h *harness, ccSession string) *mcpRig {
	t.Helper()
	// mcpsrv 의 프로젝트 좌표는 CLAUDE_PROJECT_DIR 의 마지막 성분이다(설계 §13).
	// 하네스의 프로젝트와 같은 이름의 디렉토리를 만들어 좌표를 맞춘다.
	dir := filepath.Join(filepath.Dir(h.state), h.project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("프로젝트 디렉토리 생성 실패: %v", err)
	}
	env := map[string]string{}
	for k, v := range h.env {
		env[k] = v
	}
	env["CLAUDE_CODE_SESSION_ID"] = ccSession
	env["CLAUDE_PROJECT_DIR"] = dir

	app := newApp(envOf(env), quietLogger(), dir, strings.NewReader(""))
	srv := mcpsrv.New(newMCPBackend(app), quietLogger(),
		mcpsrv.WithEnv(envOf(env)),
		mcpsrv.WithCwd(dir, nil),
		mcpsrv.WithHostname("mcp-test-host", nil),
	)
	// 대조 전제: 이 프로세스가 자기 정체를 관측했는가. 반쪽이면 도구가 거절되므로
	// 아래 단정이 "이음매가 끊겼다"가 아니라 "정체가 없다"로 초록/빨강을 낸다.
	if id := srv.Identity(); len(id.Missing) > 0 {
		t.Fatalf("전제가 깨졌다 — MCP 정체가 반쪽이다(결손 축 %v)", id.MissingAxes())
	}
	if got := srv.Identity().ProjectID; got != h.project {
		t.Fatalf("전제가 깨졌다 — MCP 가 본 프로젝트가 %q라 하네스의 %q와 다르다", got, h.project)
	}
	return &mcpRig{srv: srv, project: h.project, dir: dir, env: env}
}

// mcpFrame 은 MCP 응답 프레임 하나다(소비자 좌표계 — JSON-RPC).
type mcpFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    string `json:"data"`
	} `json:"error"`
}

func mcpCall(name string, args map[string]any) string {
	b, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// mcpServe 는 프레임들을 넣고 나온 응답을 돌려준다.
func mcpServe(t *testing.T, r *mcpRig, lines ...string) []mcpFrame {
	t.Helper()
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.srv.Serve(ctx, strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatalf("MCP Serve 가 오류로 끝났다: %v", err)
	}
	var frames []mcpFrame
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	for {
		var f mcpFrame
		if err := dec.Decode(&f); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("응답 프레임을 못 읽었다: %v\n원문:\n%s", err, out.String())
		}
		frames = append(frames, f)
	}
	return frames
}

// mcpText 는 tools/call 응답에서 에이전트가 읽는 본문과 isError 를 꺼낸다.
func mcpText(t *testing.T, f mcpFrame) (string, bool) {
	t.Helper()
	if f.Error != nil {
		t.Fatalf("도구 실패가 프로토콜 오류로 나왔다(code=%d %s)", f.Error.Code, f.Error.Message)
	}
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(f.Result, &r); err != nil {
		t.Fatalf("tools/call 결과를 못 읽었다: %v\n%s", err, f.Result)
	}
	if len(r.Content) != 1 || r.Content[0].Type != "text" {
		t.Fatalf("content 가 text 블록 1개가 아니다: %s", f.Result)
	}
	return r.Content[0].Text, r.IsError
}

// ─────────────────────────────────────────────────────────────────────────────
// ① MCP 도구로 쓴 것이 SSE 로 새어 나오는가 — 이 작업의 존재 이유
// ─────────────────────────────────────────────────────────────────────────────

func TestMCPToolWriteReachesTheEventStream(t *testing.T) {
	h := newHarness(t)
	rig := newMCPRig(t, h, "cc-mcp-sse")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", h.srv.URL+"/events", nil)
	if err != nil {
		t.Fatalf("구독 요청 생성 실패: %v", err)
	}
	res, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("구독 실패: %v", err)
	}
	defer res.Body.Close()

	// ★ 대조 전제 ①: 구독이 **실제로 열렸는가.** 서버는 성립하자마자 `: connected` 를 낸다.
	//   이 줄을 안 보고 진행하면 "프레임이 안 온다"가 "구독이 안 열렸다"와 구분되지 않는다.
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	if !sc.Scan() {
		t.Fatalf("전제가 깨졌다 — 구독의 첫 줄이 안 왔다: %v", sc.Err())
	}
	if first := sc.Text(); !strings.HasPrefix(first, ":") {
		t.Fatalf("전제가 깨졌다 — 첫 줄이 구독 성립 주석이 아니다: %q", first)
	}

	// ★ 대조 전제 ②: 도구 호출이 **실제로 성공했는가.**
	//   거절당한 호출에 프레임이 안 오는 것은 당연하고, 그것을 이 시험의 결론으로 삼으면 안 된다.
	frames := mcpServe(t, rig, mcpCall("note", map[string]any{
		"kind": "ask", "title": "이음매 시험", "body": "contracts/ 는 지금 내가 잡고 있다",
	}))
	if len(frames) != 1 {
		t.Fatalf("응답이 %d개다", len(frames))
	}
	text, isErr := mcpText(t, frames[0])
	if isErr {
		t.Fatalf("전제가 깨졌다 — note 도구가 실패했다:\n%s", text)
	}
	if !strings.Contains(text, "저장했다") {
		t.Fatalf("전제가 깨졌다 — note 응답이 저장을 확인하지 않는다:\n%s", text)
	}
	// 서버가 **실제로** 갖게 됐는가. "보냈다"가 아니라 "저장됐다"가 단정의 좌표계다.
	if js := h.judgments(model.JudgmentAsk); len(js) != 1 {
		t.Fatalf("전제가 깨졌다 — 서버의 ask 판단이 %d건이다", len(js))
	}

	// ── 이음매 단정: 그 쓰기가 알림 축에 떴는가 ──
	kinds := readSSEKinds(t, sc, 4)
	if !containsStr(kinds, "judgment.note") {
		t.Errorf("MCP 의 note 가 알림에 안 떴다 — 받은 kind %v.\n"+
			"이 경로가 조용하면 '아무 일도 없다'와 '이 경로는 알림을 안 낸다'가 구분되지 않는다.", kinds)
	}
}

// readSSEKinds 는 프레임 max 개를 읽어 data 안의 kind 를 모은다.
//
// **kind 로 거른다** — 프레임에 `event:` 줄이 없는 것이 이 서버의 계약이고(EncodeSSE),
// 그 계약을 여기서 다시 흉내 내면 사본을 단정하게 된다.
func readSSEKinds(t *testing.T, sc *bufio.Scanner, max int) []string {
	t.Helper()
	var kinds []string
	for len(kinds) < max && sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("SSE data 를 못 읽었다(%q): %v", line, err)
		}
		kinds = append(kinds, ev.Kind)
		if ev.Kind == "judgment.note" {
			break
		}
	}
	return kinds
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// ② 서버 미도달 — 도구 일곱이 각각 무엇을 하는가. **조용히 성공하지 않는다**
//
// ★ 표는 도구 일곱을 다 덮지만 **갈래를 다 덮지는 않는다.** pick 은 인자에 따라 두 축이라
//   두 행이고, land 는 인자 없는 취득 한 갈래만 여기 있다 — 보고·이탈·회수의 사유는
//   land_seam_test.go 의 TestLandDegradeReasonsAreDistinct 가 순수 함수 쪽에서 가른다.
//   여기서 재는 것은 그 판정이 **MCP 응답까지 건너오는가**이지 판정 자체가 아니다.
//
// ★ **그 "다 덮는다"를 잠그는 시험은 없다.** 아래 표를 mcpsrv.ToolNames() 와 대조하는 자리가
//   이 패키지에 한 군데도 없다(cmd/fd 전체에 ToolNames·KnownTool·Tools 호출이 0건이다) —
//   실측: alloc 행 하나를 지워도 이 시험이 초록으로 통과한다. 도구가 여덟이 되면
//   mcpsrv 의 TestToolTableIsSeven 은 빨개지지만 그 빨간불은 **다른 패키지의 다른 사실**을
//   말하고, 그것을 고치는 사람을 이 표로 데려오는 문장은 없다. 그래서 여덟째 도구의
//   열화 처방은 조용히 안 덮인 채로 남을 수 있다(api/errors.go 의 "그 수를 잠그는 시험은
//   없다", DESIGN.md 의 "이 수를 잠그는 시험은 없다"와 같은 부류의 결손이다).
// ─────────────────────────────────────────────────────────────────────────────

func TestMCPToolsDegradeExplicitlyWhenServerIsDown(t *testing.T) {
	h := newHarness(t)
	rig := newMCPRig(t, h, "cc-mcp-offline")

	// ── 준비: 온라인에서 세션·보드 캐시를 채우고 항목 하나를 만든다 ──
	warm := mcpServe(t, rig,
		mcpCall("board", map[string]any{}),
		mcpCall("add", map[string]any{"id": "t9-offline", "title": "제목", "body": "본문"}),
		mcpCall("pick", map[string]any{}), // 추천은 읽기다 — 캐시가 채워져야 아래 cache 처방이 돈다
	)
	if len(warm) != 3 {
		t.Fatalf("준비 응답이 %d개다", len(warm))
	}
	for i, name := range []string{"board", "add", "pick(추천)"} {
		if txt, isErr := mcpText(t, warm[i]); isErr {
			t.Fatalf("전제가 깨졌다 — 온라인에서 %s 가 실패했다:\n%s", name, txt)
		}
	}
	judgmentsBefore := len(h.judgments(model.JudgmentAsk))

	// ── 대조 전제: 정말 미도달인가(h.down 이 그 자리에서 단정한다) ──
	h.down()

	cases := []struct {
		tool    string
		args    map[string]any
		isErr   bool   // isError 로 나와야 하는가
		wants   string // 응답에 반드시 있어야 하는 사유 조각
		didWhat string // 무엇을 했다고 말해야 하는가
	}{
		{"board", map[string]any{}, false,
			"읽기다", "캐시된 마지막 응답을 냈다"},
		{"note", map[string]any{"kind": "ask", "body": "오프라인에서 남긴 판단"}, false,
			"파생 불가한 유일한 자산", "아웃박스에 쌓았다"},
		// 같은 도구가 인자에 따라 다른 축이다: 인자 없으면 읽기(추천), 있으면 쓰기(선점).
		// 하나만 시험하면 나머지 하나의 처방이 무엇이든 초록이 난다.
		{"pick", map[string]any{}, false,
			"읽기다", "캐시된 마지막 응답을 냈다"},
		{"pick", map[string]any{"item_id": "t9-offline"}, true,
			"배타는 서버만 보장할 수 있", "하지 않았다"},
		{"add", map[string]any{"id": "t9-more", "title": "제목", "body": "본문"}, true,
			"항목 id 는 전역 유일", "하지 않았다"},
		{"finish", map[string]any{"item_id": "t9-offline", "outcome": "done", "body": "본문"}, true,
			"한 트랜잭션", "하지 않았다"},
		{"alloc", map[string]any{"counter_name": "rev"}, true,
			"원자 카운터", "하지 않았다"},
		// 인자 없는 land 는 레인 **취득**이다(mcpbackend.Land → CmdLandAcquire).
		// 사유가 "배타의 정본이 서버의 DB 제약"인 것이 이 행의 요점이다 —
		// 오프라인에서 '내 차례'를 만들면 두 세션이 동시에 랜딩한다(offline.go 의 그 갈래).
		{"land", map[string]any{}, true,
			"배타의 정본이 서버의 DB 제약", "하지 않았다"},
	}
	for _, c := range cases {
		frames := mcpServe(t, rig, mcpCall(c.tool, c.args))
		if len(frames) != 1 {
			t.Fatalf("%s: 응답이 %d개다", c.tool, len(frames))
		}
		text, isErr := mcpText(t, frames[0])
		// ★ 조용한 성공이 없다 — 표의 모든 행이 서버에 못 닿았다는 것을 **본문으로** 말한다.
		//   (수를 안 적는다: 이 표는 도구 하나가 인자에 따라 두 행이라 행 수와 도구 수가 다르고,
		//    앞선 판의 "여섯 전부"는 그 둘 중 어느 쪽도 아니게 됐다.)
		if !strings.Contains(text, "조정 서버 미도달") {
			t.Errorf("%s: 서버가 죽었는데 응답이 그 사실을 말하지 않는다:\n%s", c.tool, text)
			continue
		}
		if isErr != c.isErr {
			t.Errorf("%s: isError=%v 인데 %v 여야 한다 — "+
				"쌓아 둔 것을 실패로 내면 세션이 다시 쓰고, 안 한 것을 성공으로 내면 남의 항목 위에서 일한다:\n%s",
				c.tool, isErr, c.isErr, text)
		}
		if !strings.Contains(text, c.didWhat) {
			t.Errorf("%s: 무엇을 했는지가 응답에 없다(%q):\n%s", c.tool, c.didWhat, text)
		}
		if !strings.Contains(text, c.wants) {
			t.Errorf("%s: 왜 그 처방인지가 응답에 없다(%q):\n%s", c.tool, c.wants, text)
		}
	}

	// ★ 원장 단정 — 거절된 것은 **정말 아무 행도 안 남겼다.**
	//   응답 문자열만 보면 "거절했다고 말하면서 몰래 쓴" 경우를 못 본다.
	h.up()
	if n := len(h.judgments(model.JudgmentAsk)); n != judgmentsBefore {
		t.Errorf("오프라인 note 가 서버에 %d건 들어갔다(재생 전인데) — 아웃박스여야 한다", n-judgmentsBefore)
	}
	v, err := h.svc.Board(context.Background(), h.project, service0BoardOptions())
	if err != nil {
		t.Fatalf("보드 조회 실패: %v", err)
	}
	for _, it := range v.OpenItems {
		if it.ID == "t9-more" {
			t.Error("오프라인 add 가 거절됐는데 항목이 서버에 생겼다")
		}
		if it.ID == "t9-offline" && it.State != "open" {
			t.Errorf("오프라인 finish 가 거절됐는데 항목 상태가 %s 다", it.State)
		}
	}
}

// TestMCPPickIsRefusedOfflineEvenWithoutAnyCache 는 캐시가 하나도 없는 머신에서도
// 선점이 **조용히 실패하지 않는지** 본다.
//
// 캐시가 있는 경로와 나눠 두는 이유: 캐시가 있으면 세션 좌표가 있어 거절이
// 열화 판정(JudgeOffline)에서 나고, 없으면 그 앞의 세션 열기에서 난다.
// 두 자리는 다른 코드라 하나가 조용해도 다른 하나가 초록이면 안 보인다.
func TestMCPPickIsRefusedOfflineEvenWithoutAnyCache(t *testing.T) {
	h := newHarness(t)
	rig := newMCPRig(t, h, "cc-mcp-cold")
	h.down() // 아무것도 안 한 채로 죽인다 — 캐시도 세션도 없다

	frames := mcpServe(t, rig, mcpCall("pick", map[string]any{"item_id": "t9-any"}))
	text, isErr := mcpText(t, frames[0])
	if !isErr {
		t.Fatalf("캐시도 서버도 없는데 pick 이 성공으로 나왔다:\n%s", text)
	}
	if !strings.Contains(text, "조정 서버 미도달") {
		t.Fatalf("pick 거절이 서버 미도달을 말하지 않는다:\n%s", text)
	}
	if !strings.Contains(text, "캐시된 세션도 없다") {
		t.Fatalf("무엇이 없어서 못 했는지가 응답에 없다:\n%s", text)
	}
	if n := countClaims(t, h); n != 0 {
		t.Fatalf("오프라인에서 거절했는데 선점 %d행이 생겼다", n)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ③ 배관을 갈았는데 표면이 그대로인가 — 이 전환은 표면 변경이 아니다
// ─────────────────────────────────────────────────────────────────────────────

// TestMCPRefusalWordingSurvivesTheRESTHop 은 서버가 낸 거절이 REST 를 건너오면서
// **문구를 잃지 않는지** 본다.
//
// 이 축이 위험한 이유: 거절은 서버에서 "<무엇> 거절: <사유>" 로 조립돼 상태코드와 함께 오고,
// 클라이언트가 그것을 다시 오류 타입으로 되돌린다. 그 왕복에서 처방(guidance)이 떨어지면
// finish 를 body 없이 부른 세션이 **무엇을 적어야 하는지를 못 받는다** — 그리고 그 상실은
// "거절은 됐다"는 사실에 가려 아무 시험에도 안 걸린다.
func TestMCPRefusalWordingSurvivesTheRESTHop(t *testing.T) {
	h := newHarness(t)
	rig := newMCPRig(t, h, "cc-mcp-refusal")

	frames := mcpServe(t, rig, mcpCall("finish", map[string]any{
		"item_id": "t9-none", "outcome": "done",
	}))
	text, isErr := mcpText(t, frames[0])
	if !isErr {
		t.Fatalf("body 없는 finish 가 성공으로 나왔다:\n%s", text)
	}
	for _, want := range []string{
		"finish 거절: 판단 본문(body)이 비어 있어 끝낼 수 없다",
		"① 왜 그렇게 했나",
		"② 무엇을 기각했나",
		"③ 일부러 안 한 것",
		"④ 확인했으나 못 한 것",
		"followups",
		"의도적으로 남긴 자리를 결함으로 보고 고치러 간다",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("REST 를 건너온 거절에 %q 가 없다:\n%s", want, text)
		}
	}
	// request_id 도 함께 온다 — 응답과 서버 로그를 잇는 유일한 열쇠다.
	if !strings.Contains(text, "request_id=") {
		t.Errorf("거절에 request_id 가 없다 — 신고와 서버 로그를 이을 열쇠가 사라진다:\n%s", text)
	}
}

// TestMCPDBNoticeFlagsRemoteWithoutToken 은 주소 축의 판정이다. 순수 함수라 직접 부른다.
func TestMCPDBNoticeFlagsRemoteWithoutToken(t *testing.T) {
	if got := MCPDBNotice("http://fd.example.internal:7420", ""); got == "" {
		t.Error("원격 주소인데 토큰이 없는 조합이 조용하다 — 그 쓰기는 전부 401 로 끊긴다")
	} else if !strings.Contains(got, "401") {
		t.Errorf("무엇이 일어날지가 안내에 없다: %s", got)
	}
	// 루프백은 서버가 토큰을 면제한다(설계 §6). 상시 점등된 경고는 판별력이 0 이다.
	for _, u := range []string{"", "http://127.0.0.1:7420", "http://localhost:7420", "http://[::1]:7420"} {
		if got := MCPDBNotice(u, ""); got != "" {
			t.Errorf("MCPDBNotice(%q, \"\") 가 %q 를 냈다 — 정상 조합이다", u, got)
		}
	}
	if got := MCPDBNotice("http://fd.example.internal:7420", "tok"); got != "" {
		t.Errorf("토큰이 있는 원격인데 경고가 났다: %s", got)
	}
}

func countClaims(t *testing.T, h *harness) int {
	t.Helper()
	var n int
	if err := h.st.DB().QueryRowContext(context.Background(),
		`SELECT count(*) FROM claim`).Scan(&n); err != nil {
		t.Fatalf("선점 행 세기 실패: %v", err)
	}
	return n
}
