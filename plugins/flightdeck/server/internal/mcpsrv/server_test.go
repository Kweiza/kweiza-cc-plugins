package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// 이 파일의 소비자 좌표계는 **JSON-RPC 프레임과 MCP 응답 문자열**이다.
// 서버 내부 타입을 들여다보지 않고, 클라이언트가 실제로 받는 것만 단정한다.

func TestMain(m *testing.M) {
	// 시험이 도는 머신의 git 설정에서 격리한다.
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newRepo 는 커밋 하나가 있는 실물 저장소를 만든다. 이름은 프로젝트 id 가 된다.
func newRepo(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("임시 경로 해석 실패: %v", err)
	}
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("디렉토리 생성 실패: %v", err)
	}
	git := func(args ...string) {
		full := append([]string{"-C", repo,
			"-c", "user.name=fd test", "-c", "user.email=fd@test.invalid",
			"-c", "commit.gpgsign=false"}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("준비용 git %v 실패: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main", ".")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("파일 쓰기 실패: %v", err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	return repo
}

func newSvc(t *testing.T) (*service.Service, *store.Store) {
	t.Helper()
	st, err := store.OpenWithLogger(filepath.Join(t.TempDir(), "fd.db"), discard())
	if err != nil {
		t.Fatalf("DB 열기 실패: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return service.New(st, discard()), st
}

// newServer 는 환경을 인자로 주입한 서버다. 전역 환경을 흔들지 않는다.
func newServer(t *testing.T, svc *service.Service, repo string, envs map[string]string) *Server {
	t.Helper()
	return New(svc, discard(),
		WithEnv(env(envs)),
		WithCwd(repo, nil),
		WithHostname("testhost", nil),
	)
}

func fullEnv(repo string) map[string]string {
	return map[string]string{
		EnvSessionID:  "cc-session-uuid-1",
		EnvProjectDir: repo,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 프레임 왕복
// ─────────────────────────────────────────────────────────────────────────────

type outFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    string `json:"data"`
	} `json:"error"`
}

func req(id any, method string, params any) string {
	m := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		m["id"] = id
	}
	if params != nil {
		m["params"] = params
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func call(name string, args any) string {
	return req(99, "tools/call", map[string]any{"name": name, "arguments": args})
}

// serve 는 프레임들을 한 번에 넣고 나온 응답을 전부 돌려준다.
func serve(t *testing.T, srv *Server, lines ...string) []outFrame {
	t.Helper()
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := srv.Serve(ctx, in, &out); err != nil {
		t.Fatalf("Serve 가 오류로 끝났다: %v", err)
	}
	var frames []outFrame
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	for {
		var f outFrame
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

// toolText 는 tools/call 응답 하나에서 사람이 읽는 본문과 isError 를 꺼낸다.
func toolText(t *testing.T, f outFrame) (string, bool) {
	t.Helper()
	if f.Error != nil {
		t.Fatalf("도구 실패가 프로토콜 오류로 나왔다(code=%d %s) — isError 내용이어야 한다",
			f.Error.Code, f.Error.Message)
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
// ① initialize → tools/list 왕복
// ─────────────────────────────────────────────────────────────────────────────

func TestInitializeAndToolsListRoundTrip(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv,
		req(1, "initialize", map[string]any{
			"protocolVersion": "2025-06-18",
			"clientInfo":      map[string]any{"name": "claude-code", "version": "2.1.220"},
		}),
		req(nil, "notifications/initialized", nil), // 알림 — 응답이 있으면 규약 위반이다
		req(2, "tools/list", map[string]any{}),
		req(3, "ping", nil),
	)

	if len(frames) != 3 {
		t.Fatalf("응답이 %d개다 — 알림에 응답하지 않아야 하므로 3개여야 한다: %+v", len(frames), frames)
	}
	for i, f := range frames {
		if f.JSONRPC != "2.0" {
			t.Fatalf("%d번 응답의 jsonrpc 가 %q다", i, f.JSONRPC)
		}
		if f.Error != nil {
			t.Fatalf("%d번 응답이 오류다: %+v", i, f.Error)
		}
	}
	if string(frames[0].ID) != "1" || string(frames[1].ID) != "2" || string(frames[2].ID) != "3" {
		t.Fatalf("id 가 요청 순서대로 안 돌아왔다: %s %s %s",
			frames[0].ID, frames[1].ID, frames[2].ID)
	}

	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools map[string]any `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(frames[0].Result, &init); err != nil {
		t.Fatalf("initialize 결과를 못 읽었다: %v", err)
	}
	if init.ProtocolVersion != "2025-06-18" {
		t.Fatalf("협상된 프로토콜이 %q다", init.ProtocolVersion)
	}
	if init.ServerInfo.Name != ServerName || init.ServerInfo.Version == "" {
		t.Fatalf("serverInfo 가 비었다: %+v", init.ServerInfo)
	}
	if init.Capabilities.Tools == nil {
		t.Fatal("capabilities.tools 가 없다 — 도구를 내는 서버라는 사실이 안 알려진다")
	}
	if n := len([]rune(init.Instructions)); n == 0 || n > InstructionsLimit {
		t.Fatalf("instructions 가 %d자다(상한 %d)", n, InstructionsLimit)
	}

	var list struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(frames[1].Result, &list); err != nil {
		t.Fatalf("tools/list 결과를 못 읽었다: %v", err)
	}
	if len(list.Tools) != 6 {
		names := []string{}
		for _, tl := range list.Tools {
			names = append(names, tl.Name)
		}
		t.Fatalf("도구가 %d개다(%v) — 설계 §6 은 6개다", len(list.Tools), names)
	}
	for _, tl := range list.Tools {
		if tl.Description == "" || tl.InputSchema == nil {
			t.Fatalf("도구 %s 에 설명이나 스키마가 없다", tl.Name)
		}
		if strings.HasPrefix(tl.Name, "mcp__") {
			t.Fatalf("도구 이름 %q 에 접두가 붙었다 — 전체 이름은 호스트가 만든다", tl.Name)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ② finish 를 body 없이 — 무엇을 적어야 하는지가 그 자리에서 온다
// ─────────────────────────────────────────────────────────────────────────────

func TestFinishWithoutBodyCarriesWhatToWrite(t *testing.T) {
	repo := newRepo(t)
	svc, st := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv, call("finish", map[string]any{
		"item_id": "t5-iam", "outcome": "done",
	}))
	if len(frames) != 1 {
		t.Fatalf("응답이 %d개다", len(frames))
	}
	text, isErr := toolText(t, frames[0])
	if !isErr {
		t.Fatalf("body 없는 finish 가 성공으로 나왔다:\n%s", text)
	}

	// ★ 소비자 좌표계: 에이전트가 읽는 문자열에 넷이 **실제로** 실려 있는가.
	for _, want := range []string{
		"① 왜 그렇게 했나",
		"② 무엇을 기각했나",
		"③ 일부러 안 한 것",
		"④ 확인했으나 못 한 것",
		"followups",
		"의도적으로 남긴 자리를 결함으로 보고 고치러 간다",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("finish 응답에 %q 가 없다:\n%s", want, text)
		}
	}
	// 거절인데 반쪽이 써지면 안 된다.
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절당한 finish 가 판단 %d행을 남겼다", n)
	}
	if n := countRows(t, st, `SELECT count(*) FROM item`); n != 0 {
		t.Fatalf("거절당한 finish 가 항목 %d행을 남겼다", n)
	}
	// 시도 자체는 원장에 남는다 — 성공한 것만 세면 §10 이 실패를 못 본다.
	if n := countRows(t, st, `SELECT count(*) FROM event WHERE kind='item.finish'`); n != 0 {
		t.Logf("거절이 트랜잭션 앞에서 났으므로 item.finish 예약은 없다(=%d) — 판정 단계의 거절이다", n)
	}
}

// TestFinishFullPathIsAtomic 은 정상 경로가 판단·후속·종료·반납을 한 번에 하는지 본다.
func TestFinishFullPathIsAtomic(t *testing.T) {
	repo := newRepo(t)
	svc, st := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv,
		call("add", map[string]any{"id": "t5-iam", "title": "제목", "body": "본문"}),
		call("pick", map[string]any{"item_id": "t5-iam"}),
		call("finish", map[string]any{
			"item_id": "t5-iam", "outcome": "done",
			"title": "IAM 컬럼 상한 랜딩", "body": "왜 그렇게 했나 · 무엇을 기각했나",
			"followups": []map[string]any{
				{"id": "t6-next", "title": "후속 제목", "body": "후속 본문"},
			},
		}),
	)
	if len(frames) != 3 {
		t.Fatalf("응답이 %d개다", len(frames))
	}
	text, isErr := toolText(t, frames[2])
	if isErr {
		t.Fatalf("정상 finish 가 실패했다:\n%s", text)
	}
	for _, want := range []string{"t5-iam", "done", "t6-next", "한 트랜잭션"} {
		if !strings.Contains(text, want) {
			t.Fatalf("finish 응답에 %q 가 없다:\n%s", want, text)
		}
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment WHERE kind='handoff'`); n != 1 {
		t.Fatalf("핸드오프 판단이 %d행이다", n)
	}
	if n := countRows(t, st, `SELECT count(*) FROM item WHERE id='t6-next' AND state='open'`); n != 1 {
		t.Fatalf("후속이 열린 항목으로 안 들어갔다(%d행)", n)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ③ pick — 브랜치 이름과 워크트리 준비 명령
// ─────────────────────────────────────────────────────────────────────────────

func TestPickResponseHasBranchAndWorktreeCommands(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv,
		call("add", map[string]any{
			"id": "t5-iam", "title": "IAM 컬럼 상한", "body": "본문이다",
			"paths": []string{"services/console-api/"},
		}),
		call("pick", map[string]any{}),                    // 인자 없음 → 추천
		call("pick", map[string]any{"item_id": "t5-iam"}), // 지정 → 선점
		call("pick", map[string]any{"item_id": "t5-iam"}), // 다시 → 재개
	)
	if len(frames) != 4 {
		t.Fatalf("응답이 %d개다", len(frames))
	}

	rec, isErr := toolText(t, frames[1])
	if isErr {
		t.Fatalf("추천이 실패했다:\n%s", rec)
	}
	if !strings.Contains(rec, "브랜치: t5-iam") {
		t.Fatalf("추천 응답에 브랜치 이름이 없다:\n%s", rec)
	}
	wantCmd := fmt.Sprintf("git worktree add %s -b t5-iam main", service.WorktreeDir("t5-iam"))
	if !strings.Contains(rec, wantCmd) {
		t.Fatalf("추천 응답에 워크트리 준비 명령(%q)이 없다:\n%s", wantCmd, rec)
	}
	if !strings.Contains(rec, repo) {
		t.Fatalf("워크트리 명령에 프로젝트 경로가 없다:\n%s", rec)
	}
	if !strings.Contains(rec, "아직 선점하지 않았다") {
		t.Fatalf("추천이 선점으로 오해될 수 있다:\n%s", rec)
	}

	claimed, isErr := toolText(t, frames[2])
	if isErr {
		t.Fatalf("선점이 실패했다:\n%s", claimed)
	}
	if !strings.Contains(claimed, "pick · 선점했다") {
		t.Fatalf("선점했다는 사실이 응답에 없다:\n%s", claimed)
	}

	resumed, _ := toolText(t, frames[3])
	if !strings.Contains(resumed, "재개") {
		t.Fatalf("자기 선점을 다시 부른 것이 재개로 안 나온다:\n%s", resumed)
	}
}

// TestPickRefusesSteal 은 설계 §4("회수는 사람만")를 응답 문자열로 단정한다.
func TestPickRefusesSteal(t *testing.T) {
	repo := newRepo(t)
	svc, st := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv,
		call("add", map[string]any{"id": "t5-iam", "title": "제목", "body": "본문"}),
		call("pick", map[string]any{"item_id": "t5-iam", "steal_reason": "저쪽이 죽은 것 같다"}),
	)
	text, isErr := toolText(t, frames[1])
	if !isErr {
		t.Fatalf("steal_reason 이 조용히 무시됐다:\n%s", text)
	}
	if !strings.Contains(text, "회수하지 않는다") || !strings.Contains(text, "다섯 축") {
		t.Fatalf("회수 거절의 사유가 응답에 없다:\n%s", text)
	}
	// 조용히 무시하지도, 몰래 선점하지도 않았다.
	if n := countRows(t, st, `SELECT count(*) FROM claim`); n != 0 {
		t.Fatalf("거절했는데 선점 %d행이 생겼다", n)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ④ board 기본 출력이 예산 안인가
// ─────────────────────────────────────────────────────────────────────────────

func TestBoardDefaultOutputWithinBudget(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	ctx := context.Background()

	// 예산을 넘길 만큼 세션을 만든다. 라벨을 길게 두는 이유는 실제 운영에서
	// 라벨이 문장에 가깝기 때문이다(기존 게시판의 track 문자열이 그랬다).
	const n = 24
	for i := 0; i < n; i++ {
		res, err := svc.OpenSession(ctx, service.OpenSessionInput{
			Project: "repo", ProjectPath: repo, MachineID: "m1", Hostname: "h",
			Worktree: repo, CCSessionID: fmt.Sprintf("cc-%02d", i),
			Label: fmt.Sprintf("트랙 %d — 파이프라인 색인 경로 정리와 계약 개정 반영 작업", i),
		})
		if err != nil {
			t.Fatalf("세션 열기 실패: %v", err)
		}
		if err := svc.Beat(ctx, res.Session.ID, model.SignalTool, []string{
			"pipeline/indexer/", "contracts/search-index/", "services/data-api/",
		}); err != nil {
			t.Fatalf("신호 기록 실패: %v", err)
		}
	}

	srv := newServer(t, svc, repo, fullEnv(repo))
	frames := serve(t, srv,
		call("board", map[string]any{"detail": true}),
		call("board", map[string]any{}),
	)
	if len(frames) != 2 {
		t.Fatalf("응답이 %d개다", len(frames))
	}

	// ★ 대조를 먼저 단정한다. detail 출력이 예산을 안 넘으면 이 시험은
	//   자르는 경로를 통과하지 않은 채 초록을 낸다.
	detail, isErr := toolText(t, frames[0])
	if isErr {
		t.Fatalf("detail 보드가 실패했다:\n%s", detail)
	}
	if got := EstimateTokens(detail); got <= BoardTokenBudget {
		t.Fatalf("대조 불성립: detail 보드가 %d토큰이라 예산 %d 를 안 넘는다 — "+
			"세션 %d건으로는 자르는 경로가 안 돈다", got, BoardTokenBudget, n)
	}

	brief, isErr := toolText(t, frames[1])
	if isErr {
		t.Fatalf("기본 보드가 실패했다:\n%s", brief)
	}
	if got := EstimateTokens(brief); got > BoardTokenBudget {
		t.Fatalf("기본 보드가 %d토큰이다 — 상한 %d\n%s", got, BoardTokenBudget, brief)
	}
	if !strings.Contains(brief, "접었다") || !strings.Contains(brief, "detail=true") {
		t.Fatalf("잘랐다는 사실이나 전부 보는 법이 응답에 없다:\n%s", brief)
	}
	// 꼬리는 모든 응답에 온다.
	if !strings.Contains(brief, "── 꼬리 ──") {
		t.Fatalf("보드 응답에 꼬리가 없다:\n%s", brief)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ⑤ 세션 정체가 없을 때 — 조용히 진행하지 않는다
// ─────────────────────────────────────────────────────────────────────────────

func TestMissingSessionIDRefusesAndWritesNothing(t *testing.T) {
	repo := newRepo(t)
	svc, st := newSvc(t)

	// 프로젝트는 다른 세션이 이미 열어 등록해 뒀다(읽기는 되어야 한다).
	if _, err := svc.OpenSession(context.Background(), service.OpenSessionInput{
		Project: "repo", ProjectPath: repo, MachineID: "m9", Hostname: "h9",
		Worktree: repo, CCSessionID: "someone-else", Label: "남의 세션",
	}); err != nil {
		t.Fatalf("사전 세션 열기 실패: %v", err)
	}

	srv := newServer(t, svc, repo, map[string]string{EnvProjectDir: repo}) // 세션 id 가 없다

	frames := serve(t, srv,
		call("note", map[string]any{"kind": "ask", "body": "contracts/ 는 건드리지 마라"}),
		call("add", map[string]any{"id": "x-1", "title": "제목", "body": "본문"}),
		call("board", map[string]any{}),
	)
	if len(frames) != 3 {
		t.Fatalf("응답이 %d개다", len(frames))
	}

	for i, name := range []string{"note", "add"} {
		text, isErr := toolText(t, frames[i])
		if !isErr {
			t.Fatalf("%s 가 세션 없이 성공했다:\n%s", name, text)
		}
		if !strings.Contains(text, EnvSessionID) {
			t.Fatalf("%s 거절 사유가 어느 축이 없는지 안 말한다:\n%s", name, text)
		}
		if !strings.Contains(text, "지어내지 않는다") {
			t.Fatalf("%s 거절이 '지어내지 않는다'를 안 말한다:\n%s", name, text)
		}
	}

	// ★ 조용히 익명으로 진행하지 않았다는 것을 원장으로 단정한다.
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("정체 없이 판단 %d행이 써졌다", n)
	}
	if n := countRows(t, st, `SELECT count(*) FROM item`); n != 0 {
		t.Fatalf("정체 없이 항목 %d행이 써졌다", n)
	}
	if n := countRows(t, st, `SELECT count(*) FROM session WHERE cc_session_id=''`); n != 0 {
		t.Fatalf("빈 cc_session_id 로 세션 %d행이 열렸다", n)
	}

	// 읽기는 된다. 그리고 배너가 그 결과가 정체 없이 나온 값이라는 것을 알린다.
	board, isErr := toolText(t, frames[2])
	if isErr {
		t.Fatalf("정체가 반쪽이라고 읽기까지 막혔다:\n%s", board)
	}
	if !strings.Contains(board, EnvSessionID) {
		t.Fatalf("board 꼬리 배너에 결손 축 이름이 없다:\n%s", board)
	}
	if !strings.Contains(board, "안 되는 것") {
		t.Fatalf("무엇이 안 되는지가 배너에 없다:\n%s", board)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ⑥ 깨진 프레임 — 프로토콜 오류를 내고 죽지 않는다
// ─────────────────────────────────────────────────────────────────────────────

func TestMalformedFramesGetProtocolErrorsAndServerSurvives(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv,
		`{ 이것은 JSON 이 아니다`, // 파스 오류
		``,                 // 빈 줄 — 프레임이 아니다
		`{"jsonrpc":"1.0","id":11,"method":"ping"}`,                                  // 규약 위반
		`{"jsonrpc":"2.0","id":12,"method":"tools/summon"}`,                          // 없는 메서드
		`{"jsonrpc":"2.0","method":"notifications/cancelled"}`,                       // 모르는 알림 — 응답 없음
		`{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"status"}}`, // 없는 도구
		`{"jsonrpc":"2.0","id":14,"method":"ping"}`,                                  // 살아 있나
	)

	if len(frames) != 5 {
		t.Fatalf("응답이 %d개다 — 파스오류·규약위반·없는메서드·없는도구·ping 다섯이어야 한다: %+v",
			len(frames), frames)
	}

	if frames[0].Error == nil || frames[0].Error.Code != CodeParseError {
		t.Fatalf("깨진 JSON 에 파스 오류가 안 났다: %+v", frames[0])
	}
	if string(frames[0].ID) != "null" {
		t.Fatalf("파스 오류의 id 가 %s다 — 규약상 null 이어야 한다", frames[0].ID)
	}
	if frames[1].Error == nil || frames[1].Error.Code != CodeInvalidRequest {
		t.Fatalf("jsonrpc=1.0 에 규약 위반 오류가 안 났다: %+v", frames[1])
	}
	if frames[2].Error == nil || frames[2].Error.Code != CodeMethodNotFound {
		t.Fatalf("없는 메서드에 -32601 이 안 났다: %+v", frames[2])
	}
	if frames[3].Error == nil || frames[3].Error.Code != CodeInvalidParams {
		t.Fatalf("없는 도구에 -32602 가 안 났다: %+v", frames[3])
	}
	if !strings.Contains(frames[3].Error.Data, "board") {
		t.Fatalf("없는 도구 오류가 무엇이 있는지 안 알린다: %+v", frames[3].Error)
	}

	// ★ 살아남았는가 — 이것이 이 시험의 본체다.
	if frames[4].Error != nil || string(frames[4].ID) != "14" {
		t.Fatalf("깨진 프레임 뒤의 정상 요청이 처리되지 않았다: %+v", frames[4])
	}
}

// TestUnknownArgumentIsToolErrorNotProtocolError 는 경계를 단정한다:
// 인자 오타는 에이전트가 고칠 수 있으므로 isError 이고, 프레임이 깨진 것만 JSON-RPC error 다.
func TestUnknownArgumentIsToolErrorNotProtocolError(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv, call("add", map[string]any{
		"id": "x-1", "title": "제목", "body": "본문", "branch": "main", // 없는 인자
	}))
	text, isErr := toolText(t, frames[0])
	if !isErr {
		t.Fatalf("모르는 인자가 조용히 무시됐다:\n%s", text)
	}
	if !strings.Contains(text, "branch") {
		t.Fatalf("어느 인자가 문제인지 안 말한다:\n%s", text)
	}
}

// TestNoteReportsRecipients 는 §6 의 "저장 확인 + 이 노트를 받을 세션 수"를 단정한다.
func TestNoteReportsRecipients(t *testing.T) {
	repo := newRepo(t)
	svc, st := newSvc(t)
	if _, err := svc.OpenSession(context.Background(), service.OpenSessionInput{
		Project: "repo", ProjectPath: repo, MachineID: "m2", Hostname: "h2",
		Worktree: repo, CCSessionID: "other-session", Label: "남",
	}); err != nil {
		t.Fatalf("사전 세션 열기 실패: %v", err)
	}
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv, call("note", map[string]any{
		"kind": "ask", "title": "계약 건드리지 마라", "body": "contracts/ 는 지금 내가 잡고 있다",
	}))
	text, isErr := toolText(t, frames[0])
	if isErr {
		t.Fatalf("note 가 실패했다:\n%s", text)
	}
	if !strings.Contains(text, "1건이 읽는다") {
		t.Fatalf("받을 세션 수가 안 나온다:\n%s", text)
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment WHERE kind='ask'`); n != 1 {
		t.Fatalf("ask 판단이 %d행이다", n)
	}
}

// TestAllocIsAtomicAndVisible 은 발번이 값과 그 성질을 함께 내는지 본다.
func TestAllocIsAtomicAndVisible(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv,
		call("board", map[string]any{}), // 프로젝트 등록을 위해 세션을 먼저 연다
		call("alloc", map[string]any{"counter_name": "contract_revision"}),
		call("alloc", map[string]any{"counter_name": "contract_revision"}),
	)
	first, isErr := toolText(t, frames[1])
	if isErr {
		t.Fatalf("발번이 실패했다:\n%s", first)
	}
	second, _ := toolText(t, frames[2])
	if !strings.Contains(first, "contract_revision = 1") || !strings.Contains(second, "contract_revision = 2") {
		t.Fatalf("발번이 1,2 로 안 나온다:\n%s\n---\n%s", first, second)
	}
}

// countRows 는 시험이 원장을 직접 볼 때 쓴다 — "조용히 진행하지 않았다"의 유일한 증거다.
func countRows(t *testing.T, st *store.Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("행 세기 실패(%s): %v", q, err)
	}
	return n
}
