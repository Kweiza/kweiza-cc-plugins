// Package mcpsrv 는 flightdeck 의 stdio MCP 서버다.
//
// JSON-RPC 2.0 을 표준 라이브러리로 직접 구현한다(SDK 없음). 프레이밍은 "줄 하나 = JSON 하나"다.
//
// 이 계층이 지키는 것 넷:
//
//  1. **세션 인자를 만들지 않는다.** 정체는 기동 시 환경(CLAUDE_CODE_SESSION_ID·
//     CLAUDE_PROJECT_DIR)과 cwd 에서 온다(설계 §13 실측). 파생 가능한 값에 파라미터를
//     두면 틀린 값이 들어오고, 그 틀린 값은 검사로 막히지 않는다 — 우회할 필드가 있으면 우회된다.
//  2. **못 읽으면 조용히 익명으로 진행하지 않는다.** 결손 축을 이름으로 배너에 싣고
//     세션 귀속이 필요한 도구(pick·note·add·finish)를 거절한다.
//  3. **규율은 도구 설명이 아니라 응답에 싣는다.** 세션 시작에 실리는 것은 도구 이름과
//     300자 instructions 뿐이고, 무엇을 적어야 하는지는 finish 를 body 없이 부른
//     **그 자리에서** 온다(설계 §6).
//  4. **도구 실패는 프로토콜 오류가 아니다.** isError 내용으로 낸다 — 에이전트가 읽고 고칠 수
//     있어야 하기 때문이다. JSON-RPC error 는 프레임 자체가 깨진 경우뿐이다.
//
// 로그는 **반드시 stderr 로** 보낸다. stdout 은 프레임 채널이라 로그 한 줄이 섞이면
// 클라이언트가 그 자리에서 파스 오류를 낸다.
package mcpsrv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// ★ 이 서버는 DB 를 열지 않는다. 조정 서버에 붙는 통로는 Backend 하나뿐이고,
// 운영 배선은 그 REST 구현이다(backend.go 의 주석에 왜인지 있다).

// maxFrameBytes 는 한 프레임의 상한이다. 판단 본문이 크므로 넉넉하게 두되,
// 상한 자체는 있어야 한다 — 없으면 깨진 스트림 하나가 메모리를 통째로 먹는다.
const maxFrameBytes = 4 << 20 // 4MiB

// tailNoteLimit 은 꼬리에 싣는 알림 수다. 넘치면 board 로 간다.
const tailNoteLimit = 3

// Server 는 stdio MCP 서버 하나다.
type Server struct {
	be  Backend
	log *slog.Logger
	now func() time.Time

	id Identity

	mu        sync.Mutex
	sessionID string // 게으르게 연다. 도구를 한 번도 안 부르면 세션 행도 안 생긴다
}

// Option 은 Server 의 선택 설정이다. 시험이 환경을 인자로 주기 위한 자리다 —
// os.Getenv 를 본문에 박으면 시험이 전역 환경을 흔들어야 하고, 그러면 병렬 시험이 서로를 깬다.
type Option func(*builder)

type builder struct {
	projectID   string
	projectPath string
	machineID   string
	getenv      func(string) (string, bool)
	cwd         string
	cwdErr      error
	hostname    string
	hostErr     error
	now         func() time.Time
}

// WithEnv 는 환경 조회를 바꾼다. nil 은 무시한다.
func WithEnv(get func(string) (string, bool)) Option {
	return func(b *builder) {
		if get != nil {
			b.getenv = get
		}
	}
}

// WithCwd 는 cwd 관측을 바꾼다.
func WithCwd(cwd string, err error) Option {
	return func(b *builder) { b.cwd, b.cwdErr = cwd, err }
}

// WithHostname 은 hostname 관측을 바꾼다.
func WithHostname(h string, err error) Option {
	return func(b *builder) { b.hostname, b.hostErr = h, err }
}

// WithClock 은 시계를 바꾼다. nil 은 무시한다.
func WithClock(f func() time.Time) Option {
	return func(b *builder) {
		if f != nil {
			b.now = f
		}
	}
}

// New 는 서버를 만들고 **그 자리에서 정체를 관측한다**(기동 시 1회).
//
// 매 호출마다 다시 읽지 않는 이유: 한 프로세스는 한 세션의 것이고(claude 가 세션마다 띄운다),
// 중간에 바뀌었다면 그것은 정체가 흔들린 것이라 조용히 따라가면 안 되는 사건이다.
//
// ★ **그리고 다시 읽어 봐야 같은 값이다**(2026-08-04 실측 — drift.go·drift_test.go).
// claude 는 세션 id 를 스스로 만들어 MCP 서버에 **기동 시 주입**하고, 프로세스의 environ 은
// 그 뒤 바뀌지 않는다. /clear·compact·재개로 대화의 cc 가 갈려도 이 프로세스는 옛 값을
// 영원히 든다 — 훅은 매번 새 프로세스라 새 값을 본다. 그래서 카드가 두 장 뜬다.
//
// **"도구 호출마다 cc 를 다시 읽어 따라간다"는 처방을 넣지 마라 — 아무것도 안 하는 코드다.**
// 이 프로세스가 할 수 있는 정직한 일은 갈렸다는 사실을 보드에서 이름으로 말하는 것뿐이고,
// 그것이 DriftedTwins·RenderDrift 다. 따라가려면 훅이 새 값을 실어 오는 별도 통로가 필요하다.
func New(be Backend, log *slog.Logger, opts ...Option) *Server {
	if log == nil {
		log = slog.Default()
	}
	// ★ 여기서 service.name 을 덧칠하지 않는다.
	//
	// 진입점이 이미 그 키를 걸어 두므로 여기서 또 걸면 JSON 한 줄에 같은 키가 **두 값으로** 실린다.
	// 중복 키 처리는 파서마다 달라(마지막이 이기기도, 첫째가 이기기도, 배열로 접기도)
	// 수집기 판올림 한 번에 "MCP 프로세스가 무엇을 했나"가 통째로 사라질 수 있다.
	// 이름은 프로세스 진입점(runMCP)이 정한다 — 로거를 **새로 만들어서**.

	cwd, cwdErr := os.Getwd()
	host, hostErr := os.Hostname()
	b := &builder{
		getenv:   os.LookupEnv,
		cwd:      cwd,
		cwdErr:   cwdErr,
		hostname: host,
		hostErr:  hostErr,
		now:      func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(b)
	}
	id := ResolveIdentity(b.getenv, b.cwd, b.cwdErr, b.hostname, b.hostErr)
	// ★ 프로젝트 좌표는 **주입이 이긴다.**
	//
	// 이 패키지가 스스로 푸는 규칙(경로의 마지막 성분)은 워크트리에서 틀린다:
	// `.claude/worktrees/track2` 에서 띄운 세션이 프로젝트를 `track2` 로 보고,
	// 그러면 **워크트리마다 유령 프로젝트가 생긴다**. 실물로 재현했다 —
	// 같은 워크트리에서 CLI 는 `kweiza-cc-plugins`, MCP 는 `wt-probe` 를 봤다.
	// 워크트리로 일하는 것이 이 제품의 핵심 흐름이라 그 자리에서 바로 깨지는 규칙이다.
	//
	// 옳은 규칙은 `git rev-parse --git-common-dir` 로 주 저장소를 찾는 것인데,
	// 그것은 이미 진입점(cmd/fd)이 푼다. **같은 판정을 두 자리에 두지 않는다** —
	// 이 레포는 알림 축에서 그것을 한 번 겪었고 한쪽만 고쳐 조용히 어긋났다.
	//
	// 주입이 없으면(이 패키지를 단독으로 쓰는 경우) 옛 규칙으로 떨어지되 **그 사실을 남긴다.**
	if b.projectID != "" {
		id.ProjectID, id.ProjectPath = b.projectID, b.projectPath
	} else if id.ProjectID != "" {
		id.Warnings = append(id.Warnings,
			"프로젝트 좌표를 경로의 마지막 성분으로 정했다 — 워크트리에서는 주 저장소와 다를 수 있다")
	}

	// 머신 id 도 **주입이 이긴다.** 프로젝트 축과 같은 규율이고 같은 사고를 겪었다.
	//
	// 주입이 없으면 옛 규칙(hostname)으로 떨어지되 **그 사실을 남긴다** — 침묵하면
	// "주입이 끊겼다"와 "원래 그렇다"가 구분되지 않고, 그 침묵이 이번 결함을 오래 살렸다.
	if b.machineID != "" {
		id.MachineID = b.machineID
	} else if id.MachineID != "" {
		id.Warnings = append(id.Warnings,
			"머신 id 를 hostname 으로 정했다 — 진입점이 보관하는 안정 id 와 달라 세션이 갈린다")
	}

	s := &Server{be: be, log: log, now: b.now, id: id}
	if len(id.Missing) > 0 {
		// 기동 로그에도 남긴다. 배너는 도구를 부른 세션만 보고,
		// 아무도 안 부르면 "왜 조정이 안 되나"에 답할 자리가 로그뿐이다.
		s.log.WarnContext(context.Background(), "세션 정체가 반쪽이다",
			"reason", strings.Join(id.MissingAxes(), ","),
			"project", id.ProjectID, "worktree", clip(id.Worktree, 200))
	} else {
		s.log.InfoContext(context.Background(), "MCP 서버 정체 관측",
			"project", id.ProjectID, "worktree", clip(id.Worktree, 200),
			"cc_session", clip(id.CCSessionID, 64))
	}
	return s
}

// Identity 는 이 서버가 관측한 정체다(진단·시험용).
func (s *Server) Identity() Identity { return s.id }

// Run 은 stdio 위에서 MCP 서버를 돌린다. EOF 면 nil 을 낸다(정상 종료).
func Run(ctx context.Context, be Backend, in io.Reader, out io.Writer, log *slog.Logger) error {
	return New(be, log).Serve(ctx, in, out)
}

// frame 은 읽어 들인 줄 하나다. 오류도 값으로 나른다 —
// 읽기 고루틴에서 던지면 ctx 취소와 경합한다.
type frame struct {
	line []byte
	err  error
}

// Serve 는 프레임을 읽어 처리한다.
//
// 읽기를 고루틴으로 뺀 이유는 ctx 취소다. io.Reader 는 중간에 깨울 수단이 없어
// 본문에서 그대로 읽으면 취소가 EOF 까지 안 먹는다.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false) // 한국어 응답에 < 가 섞이면 사람이 못 읽는다

	frames := make(chan frame)
	go func() {
		defer close(frames)
		r := bufio.NewReaderSize(in, 64<<10)
		for {
			line, err := readLine(r, maxFrameBytes)
			if len(line) > 0 || err == nil {
				select {
				case frames <- frame{line: line}:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					select {
					case frames <- frame{err: err}:
					case <-ctx.Done():
					}
				}
				return
			}
		}
	}()

	s.log.InfoContext(ctx, "MCP stdio 서버 기동", "mode", "stdio", "count", len(tools))
	for {
		select {
		case <-ctx.Done():
			s.log.InfoContext(ctx, "MCP stdio 서버 종료", "reason", ctx.Err().Error())
			return ctx.Err()
		case f, ok := <-frames:
			if !ok {
				s.log.InfoContext(ctx, "MCP stdio 서버 종료", "reason", "EOF")
				return nil
			}
			if f.err != nil {
				s.log.ErrorContext(ctx, "프레임 읽기 실패", "error", f.err.Error())
				return fmt.Errorf("stdin 읽기 실패: %w", f.err)
			}
			if len(strings.TrimSpace(string(f.line))) == 0 {
				continue // 빈 줄은 프레임이 아니다
			}
			resp, respond := s.handle(ctx, f.line)
			if !respond {
				continue
			}
			if err := enc.Encode(resp); err != nil {
				s.log.ErrorContext(ctx, "응답 쓰기 실패", "error", err.Error())
				return fmt.Errorf("stdout 쓰기 실패: %w", err)
			}
		}
	}
}

// readLine 은 줄 하나를 읽는다. bufio.Scanner 를 안 쓰는 이유는 기본 상한(64KiB)이
// 판단 본문에 너무 작고, 넘쳤을 때 **조용히 끊기기** 때문이다.
func readLine(r *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > limit {
			return nil, fmt.Errorf("프레임이 상한 %d바이트를 넘었다", limit)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return buf, err
	}
}

// handle 은 프레임 하나를 처리한다. respond=false 면 알림이라 응답을 내지 않는다.
func (s *Server) handle(ctx context.Context, line []byte) (rpcResponse, bool) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		// ★ 죽지 않는다. 깨진 프레임 하나로 프로세스가 죽으면
		//   그 세션의 조정이 통째로 끊긴다.
		s.log.ErrorContext(ctx, "프레임 파스 실패",
			"error", err.Error(), "count", len(line))
		return errResponse(nullID, CodeParseError, "JSON 파스 실패", err.Error()), true
	}
	if req.JSONRPC != "2.0" || strings.TrimSpace(req.Method) == "" {
		s.log.ErrorContext(ctx, "규약 위반 프레임",
			"error", fmt.Sprintf("jsonrpc=%q method=%q", clip(req.JSONRPC, 32), clip(req.Method, 64)))
		if req.isNotification() {
			return rpcResponse{}, false
		}
		return errResponse(req.ID, CodeInvalidRequest,
			"jsonrpc 는 \"2.0\" 이어야 하고 method 가 있어야 한다",
			fmt.Sprintf("받은 값: jsonrpc=%q method=%q", clip(req.JSONRPC, 32), clip(req.Method, 64))), true
	}

	switch req.Method {
	case "initialize":
		return okResponse(req.ID, s.initialize(ctx, req.Params)), true

	case "ping":
		return okResponse(req.ID, map[string]any{}), true

	case "tools/list":
		return okResponse(req.ID, map[string]any{"tools": Tools()}), true

	case "tools/call":
		if req.isNotification() {
			// 도구 호출을 알림으로 보내면 결과를 받을 자리가 없다. 실행하지 않는다 —
			// 실행하면 응답 없는 쓰기가 되고, 그러면 원장에 주인 없는 행이 생긴다.
			s.log.WarnContext(ctx, "알림으로 온 도구 호출을 실행하지 않았다", "reason", "id 가 없다")
			return rpcResponse{}, false
		}
		return s.toolsCall(ctx, req.ID, req.Params), true

	default:
		if req.isNotification() {
			// notifications/initialized 등. 응답을 내면 규약 위반이다.
			s.log.InfoContext(ctx, "알림 수신", "mode", clip(req.Method, 64))
			return rpcResponse{}, false
		}
		return errResponse(req.ID, CodeMethodNotFound,
			fmt.Sprintf("메서드 %q 를 모른다", clip(req.Method, 64)),
			"이 서버가 아는 것: initialize · notifications/initialized · ping · tools/list · tools/call"), true
	}
}

func (s *Server) initialize(ctx context.Context, params json.RawMessage) initializeResult {
	var p initializeParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			// 규약상 initialize 의 params 는 필수지만, 못 읽었다고 끊지 않는다 —
			// 협상 실패는 기본값으로 이어갈 수 있고, 사실은 로그에 남는다.
			s.log.WarnContext(ctx, "initialize params 를 못 읽었다", "error", err.Error())
		}
	}
	proto := NegotiateProtocol(p.ProtocolVersion)
	s.log.InfoContext(ctx, "initialize",
		"mode", proto, "value", clip(p.ClientInfo.Name+"/"+p.ClientInfo.Version, 80))
	return initializeResult{
		ProtocolVersion: proto,
		Capabilities:    capabilities{Tools: toolsCapability{ListChanged: false}},
		ServerInfo:      serverInfo{Name: ServerName, Version: ServerVersion},
		Instructions:    Instructions,
	}
}

func (s *Server) toolsCall(ctx context.Context, id json.RawMessage, params json.RawMessage) rpcResponse {
	var p callParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errResponse(id, CodeInvalidParams, "tools/call params 를 못 읽었다", err.Error())
	}
	if !KnownTool(p.Name) {
		// 이름이 틀린 것은 프레임 수준의 오류다. 그리고 **무엇이 있는지 함께 낸다** —
		// 목록 없이 거절하면 호출자가 같은 실수를 반복한다.
		return errResponse(id, CodeInvalidParams,
			fmt.Sprintf("도구 %q 가 없다", clip(p.Name, 64)),
			"이 서버의 도구: "+strings.Join(ToolNames(), " · "))
	}
	res := s.callTool(ctx, p.Name, p.Arguments)
	return okResponse(id, res)
}

// callTool 은 도구 하나를 실행한다. **실패도 여기서 내용으로 낸다**(isError).
func (s *Server) callTool(ctx context.Context, name string, args json.RawMessage) toolResult {
	start := s.now()

	ok, reason := GateTool(name, s.id)
	if !ok {
		s.log.WarnContext(ctx, "도구 거절",
			"mode", name, "reason", reason, "project", s.id.ProjectID)
		return textResult(s.withTail(ctx, RenderRefusal(name, reason,
			"환경 축은 `fd doctor` 가 실제로 잰다. 값을 지어내지 않는다."), tailOpts{}), true)
	}

	// 세션 신호. 도구 호출 자체가 "살아 있다"는 사실이다(설계 §4의 mcp 신호).
	sessionID := ""
	if s.canAttribute() {
		var err error
		if sessionID, err = s.ensureSession(ctx); err != nil {
			s.log.ErrorContext(ctx, "세션 열기 실패", "mode", name, "error", err.Error())
			return textResult(s.withTail(ctx, s.errText(name, err), tailOpts{}), true)
		}
		if err := s.be.Beat(ctx, sessionID, model.SignalMCP, nil); err != nil {
			// 신호 실패는 도구 실패가 아니다. 삼키지 않고 로그에 남기되 진행한다 —
			// 조정의 본체(누가 무엇을 집었나)는 이 신호 없이도 성립한다.
			s.log.WarnContext(ctx, "mcp 신호 기록 실패",
				"session_id", clip(sessionID, 64), "error", err.Error())
		}
	}

	var res toolResult
	switch name {
	case "board":
		res = s.toolBoard(ctx, sessionID, args)
	case "pick":
		res = s.toolPick(ctx, sessionID, args)
	case "note":
		res = s.toolNote(ctx, sessionID, args)
	case "add":
		res = s.toolAdd(ctx, sessionID, args)
	case "finish":
		res = s.toolFinish(ctx, sessionID, args)
	case "alloc":
		res = s.toolAlloc(ctx, sessionID, args)
	default:
		// KnownTool 을 통과했는데 여기 오면 도구 표와 디스패치가 어긋난 것이다.
		res = textResult(fmt.Sprintf("도구 %q 가 표에는 있는데 디스패치에 없다 — 서버 결함이다", clip(name, 64)), true)
	}

	s.log.InfoContext(ctx, "도구 호출",
		"mode", name, "session_id", clip(sessionID, 64), "project", s.id.ProjectID,
		"status", map[bool]string{true: "error", false: "ok"}[res.IsError],
		"duration", s.now().Sub(start).Seconds())
	return res
}

// canAttribute 는 이 프로세스가 세션 행을 만들 수 있는지다.
func (s *Server) canAttribute() bool {
	return s.id.CCSessionID != "" && s.id.ProjectID != "" && filepath.IsAbs(s.id.Worktree)
}

// ensureSession 은 세션을 한 번만 연다. 같은 3중키면 같은 세션이므로 재호출도 안전하지만,
// 매번 열면 git 파생이 도구 호출마다 돌아 첫 명령이 느려진다 — 그 느림이 기존 도구의 병목 3위였다.
func (s *Server) ensureSession(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID != "" {
		return s.sessionID, nil
	}
	res, err := s.be.OpenSession(ctx, service.OpenSessionInput{
		Project:     s.id.ProjectID,
		ProjectPath: s.id.ProjectPath,
		MachineID:   s.id.MachineID,
		Hostname:    s.id.Hostname,
		Worktree:    s.id.Worktree,
		CCSessionID: s.id.CCSessionID,
	})
	// 서버가 죽었어도 이 머신에 캐시된 마지막 세션이 있으면 그 좌표로 진행한다 —
	// 여기서 끊으면 아웃박스에 쌓아야 할 판단까지 함께 죽는다(설계 §7 L1 의 open 처방).
	// **조용히 쓰지 않는다**: 그 세션이 지금 서버에 없을 수도 있다는 사실을 로그에 남기고,
	// 이 호출의 실제 열화 결과는 도구 응답 본문이 배너로 나른다.
	if deg, ok := AsDegraded(err); ok && DegradedUsable(deg.Mode) && res.Session.ID != "" {
		s.log.WarnContext(ctx, "캐시된 세션 좌표로 진행한다",
			"session_id", clip(res.Session.ID, 64), "mode", string(deg.Mode), "reason", deg.Reason)
		err = nil
	}
	if err != nil {
		return "", err
	}
	s.sessionID = res.Session.ID
	s.log.InfoContext(ctx, "세션 귀속",
		"session_id", res.Session.ID, "project", res.Project.ID,
		"created", res.Created, "branch", res.Branch)
	return s.sessionID, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 도구
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) toolBoard(ctx context.Context, sessionID string, raw json.RawMessage) toolResult {
	var a boardArgs
	if err := decodeArgs(raw, &a); err != nil {
		return textResult(s.withTail(ctx, s.errText("board", err), tailOpts{}), true)
	}
	// ★ IncludeNotes 는 detail 과 무관하게 항상 참이다. 꼬리의 알림 축이 이 응답에서 오고,
	//   따로 부르면 같은 보드를 두 번 파생하게 된다(git 을 두 번 훑는다).
	//   기본(brief) 표시에는 이 필드가 안 실리므로 출력은 그대로다 — boardBriefFoot 이 그 증거다.
	view, err := s.be.Board(ctx, s.id.ProjectID, service.BoardOptions{
		Self:         sessionID,
		IncludeQueue: true,
		IncludeNotes: true,
	})
	notice := ""
	if deg, ok := AsDegraded(err); ok && DegradedUsable(deg.Mode) {
		notice, err = RenderDegraded(deg), nil
	}
	if err != nil {
		if r, ok := s.degradedResult(ctx, "board", err); ok {
			return r
		}
		return textResult(s.withTail(ctx, s.errText("board", err), tailOpts{}), true)
	}

	// 겹침은 여기서 **실제로** 읽었다. 내 경로 대 남의 경로다.
	var mine []string
	for _, c := range view.Sessions {
		if c.View.Session.ID == sessionID {
			mine = c.View.Paths
		}
	}
	// cc_session_id 표류를 여기서 본다 — 보드가 그 증상(카드 여러 장)이 실제로 보이는 자리다.
	// 새 파생도 새 왕복도 없다: 살아 있는 세션의 좌표는 이 응답에 이미 실려 있다.
	// 꼬리가 아니라 notice 에 싣는 이유는 꼬리가 **모든 도구 응답**에 붙기 때문이다 —
	// 그러려면 도구 호출마다 보드를 파생해야 하고, 그 비용은 이미 한 번 문제가 됐다
	// (RecentNotes 주석). 증상이 보이는 자리에서만 말한다.
	if d := RenderDrift(DriftedTwins(s.id, liveIdentitiesOf(view.Sessions)), s.id.CCSessionID); d != "" {
		if notice != "" {
			notice += "\n"
		}
		notice += d
	}

	notes := append(append([]model.Judgment(nil), view.Asks...), view.Blocked...)
	tail := s.tail(ctx, tailOpts{
		overlaps: judge.OverlapsWithLive(mine, liveOf(view.Sessions), sessionID),
		observed: true,
		notes:    notes,
		haveNote: true,
	})
	if sessionID == "" {
		tail = s.tail(ctx, tailOpts{
			observed:     false,
			overlapsNote: "내 세션이 없어 '내 경로'가 없다 — 겹침을 계산할 좌표가 없다",
			notes:        notes,
			haveNote:     true,
		})
	}
	return textResult(RenderBoard(view, BoardRenderOptions{
		Self: sessionID, Detail: a.Detail, Now: s.now(), Tail: tail, Notice: notice,
	}), false)
}

// liveIdentitiesOf 는 표류 판정에 필요한 좌표만 뽑는다.
func liveIdentitiesOf(cards []service.SessionCard) []LiveIdentity {
	out := make([]LiveIdentity, 0, len(cards))
	for _, c := range cards {
		out = append(out, LiveIdentity{
			SessionID:   c.View.Session.ID,
			MachineID:   c.View.Session.MachineID,
			Worktree:    c.View.Session.Worktree,
			CCSessionID: c.View.Session.CCSessionID,
		})
	}
	return out
}

func liveOf(cards []service.SessionCard) []judge.LiveSession {
	out := make([]judge.LiveSession, 0, len(cards))
	for _, c := range cards {
		out = append(out, judge.LiveSession{
			ID: c.View.Session.ID, Label: c.View.Session.Label, Paths: c.View.Paths,
		})
	}
	return out
}

func (s *Server) toolPick(ctx context.Context, sessionID string, raw json.RawMessage) toolResult {
	var a pickArgs
	if err := decodeArgs(raw, &a); err != nil {
		return textResult(s.withTail(ctx, s.errText("pick", err), tailOpts{}), true)
	}
	// ★ 회수는 이 서버가 하지 않는다. 설계 §4: "회수는 사람만, 사유 필수,
	//   근거를 다섯 축으로 나란히 보여준 뒤에." 인자를 조용히 무시하면
	//   에이전트가 회수됐다고 믿고 남의 작업 위에서 일한다.
	if strings.TrimSpace(a.StealReason) != "" {
		return textResult(s.withTail(ctx, RenderRefusal("pick",
			"steal_reason 이 왔지만 이 서버는 선점을 회수하지 않는다",
			"회수는 사람만 한다 — 마지막 신호 종류·나이, 발자국 경로 수, 원격 마지막 커밋 시각, "+
				"미푸시 커밋 수, 마지막 판단 다섯 축을 나란히 본 뒤에야 한다(설계 §4). "+
				"하나의 신호로 판정해 두 번 틀렸다. 지금 할 수 있는 것: note(kind=ask) 로 점유자에게 묻거나, "+
				"웹 대시보드의 '선점 회수' 버튼(사유 필수)을 쓴다."), tailOpts{}), true)
	}

	res, err := s.be.Pick(ctx, service.PickInput{
		Project: s.id.ProjectID, SessionID: sessionID, ItemID: strings.TrimSpace(a.ItemID),
	})
	// 추천(item_id 없음)은 읽기라 캐시 처방이 온다. 값을 버리지 않고 배너와 함께 낸다 —
	// 다만 **선점은 아무것도 안 됐다**는 사실을 그 배너가 말한다(선점의 처방은 거절이다).
	notice := ""
	if deg, ok := AsDegraded(err); ok && DegradedUsable(deg.Mode) {
		notice, err = RenderDegraded(deg)+"\n\n", nil
	}
	if err != nil {
		if r, ok := s.degradedResult(ctx, "pick", err); ok {
			return r
		}
		return textResult(s.withTail(ctx, s.errText("pick", err), tailOpts{}), true)
	}
	tail := s.tail(ctx, tailOpts{overlaps: res.Overlaps, observed: true})
	return textResult(notice+RenderPick(res, s.now())+"\n\n"+tail, false)
}

func (s *Server) toolNote(ctx context.Context, sessionID string, raw json.RawMessage) toolResult {
	var a noteArgs
	if err := decodeArgs(raw, &a); err != nil {
		return textResult(s.withTail(ctx, s.errText("note", err), tailOpts{}), true)
	}
	res, err := s.be.Note(ctx, service.NoteInput{
		Project: s.id.ProjectID, SessionID: sessionID,
		Kind:  model.JudgmentKind(strings.TrimSpace(a.Kind)),
		Title: a.Title, Body: a.Body, ItemID: strings.TrimSpace(a.ItemID),
		Supersedes: strings.TrimSpace(a.Supersedes),
	})
	if err != nil {
		if r, ok := s.degradedResult(ctx, "note", err); ok {
			return r
		}
		return textResult(s.withTail(ctx, s.errText("note", err), tailOpts{}), true)
	}
	return textResult(s.withTail(ctx, RenderNote(res), tailOpts{}), false)
}

func (s *Server) toolAdd(ctx context.Context, sessionID string, raw json.RawMessage) toolResult {
	var a addArgs
	if err := decodeArgs(raw, &a); err != nil {
		return textResult(s.withTail(ctx, s.errText("add", err), tailOpts{}), true)
	}
	it, err := s.be.AddItem(ctx, service.AddItemInput{
		Project: s.id.ProjectID, SessionID: sessionID,
		ID: strings.TrimSpace(a.ID), Title: a.Title, Body: a.Body,
		Paths: a.Paths, Labels: a.Labels, After: toAfter(a.After),
	})
	if err != nil {
		if r, ok := s.degradedResult(ctx, "add", err); ok {
			return r
		}
		return textResult(s.withTail(ctx, s.errText("add", err), tailOpts{}), true)
	}
	return textResult(s.withTail(ctx, RenderAdd(it), tailOpts{}), false)
}

func (s *Server) toolFinish(ctx context.Context, sessionID string, raw json.RawMessage) toolResult {
	var a finishArgs
	if err := decodeArgs(raw, &a); err != nil {
		return textResult(s.withTail(ctx, s.errText("finish", err), tailOpts{}), true)
	}
	// ★ body 누락을 **여기서 막지 않는다.** 판정은 service.JudgeFinish 하나뿐이고,
	//   그것이 무엇을 적어야 하는지(넷)를 처방으로 함께 낸다. 여기서 미리 "필수 인자 누락"
	//   으로 끊으면 그 처방이 사라지고, 이 도구가 지키려는 것이 통째로 없어진다.
	fs := make([]service.FollowupInput, 0, len(a.Followups))
	for _, f := range a.Followups {
		fs = append(fs, service.FollowupInput{
			ID: strings.TrimSpace(f.ID), Title: f.Title, Body: f.Body,
			Paths: f.Paths, Labels: f.Labels, After: toAfter(f.After),
		})
	}
	res, err := s.be.Finish(ctx, service.FinishInput{
		Project: s.id.ProjectID, SessionID: sessionID,
		ItemID:  strings.TrimSpace(a.ItemID),
		Outcome: model.ItemState(strings.TrimSpace(a.Outcome)),
		Title:   a.Title, Body: a.Body, CloseReason: a.CloseReason,
		Followups: fs,
	})
	if err != nil {
		if r, ok := s.degradedResult(ctx, "finish", err); ok {
			return r
		}
		return textResult(s.withTail(ctx, s.errText("finish", err), tailOpts{}), true)
	}
	return textResult(s.withTail(ctx, RenderFinish(res), tailOpts{}), false)
}

func (s *Server) toolAlloc(ctx context.Context, sessionID string, raw json.RawMessage) toolResult {
	var a allocArgs
	if err := decodeArgs(raw, &a); err != nil {
		return textResult(s.withTail(ctx, s.errText("alloc", err), tailOpts{}), true)
	}
	name := strings.TrimSpace(a.CounterName)
	n, err := s.be.Alloc(ctx, s.id.ProjectID, name)
	if err != nil {
		if r, ok := s.degradedResult(ctx, "alloc", err); ok {
			return r
		}
		return textResult(s.withTail(ctx, s.errText("alloc", err), tailOpts{}), true)
	}
	_ = sessionID // 발번의 원장 행은 프로젝트 귀속이다(service 가 그렇게 남긴다)
	return textResult(s.withTail(ctx, RenderAlloc(name, n), tailOpts{}), false)
}

// toAfter 는 인자의 선행 조건을 도메인 타입으로 옮긴다.
// 셋 중 정확히 하나인지의 판정은 store.ValidateAfter 가 한다 — 여기서 흉내 내지 않는다.
func toAfter(in []afterArgs) []model.After {
	if len(in) == 0 {
		return nil
	}
	out := make([]model.After, 0, len(in))
	for _, a := range in {
		out = append(out, model.After{
			Item: strings.TrimSpace(a.Item),
			Job:  strings.TrimSpace(a.Job),
			SHA:  strings.TrimSpace(a.SHA),
		})
	}
	return out
}

// decodeArgs 는 도구 인자를 읽는다. 모르는 필드는 **거절한다** —
// 조용히 무시하면 오타 난 인자가 "안 줬다"와 구분되지 않는다.
func decodeArgs(raw json.RawMessage, dst any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil // 인자 없는 호출은 유효하다(pick·board 가 그렇다)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("인자를 못 읽었다: %w", err)
	}
	return nil
}

// errText 는 오류 하나를 응답 본문으로 만든다.
//
// RefusedError 는 **처방까지** 낸다 — 그것이 이 계층이 규율을 나르는 방식이다.
// 그 밖의 오류는 원인 전문을 싣는다. 상태 코드만 남기면 무엇이 틀렸는지 영영 모른다.
func (s *Server) errText(tool string, err error) string {
	// 열화는 실패가 아니라 **다른 사실**이다. 여기까지 오는 경로가 있다(세션 열기 실패 등) —
	// 그때 "note 실패: ..." 로 뭉개면 무엇이 됐고 무엇이 안 됐는지가 사라진다.
	if deg, ok := AsDegraded(err); ok {
		return RenderDegraded(deg)
	}
	var ref *service.RefusedError
	if errors.As(err, &ref) {
		return RenderRefusal(ref.What, ref.Reason, ref.Guidance)
	}
	if errors.Is(err, store.ErrNotFound) {
		// err.Error() 에 **무엇이 없었는지**가 들어 있다(store.NotFoundError 가 좌표를 나른다).
		// 앞선 판은 REST 를 건너오면 그 좌표가 사라져 "요청한 대상이 없다"만 남았고,
		// 그래서 오타 난 항목 id 와 프로젝트 미등록이 글자 그대로 같은 화면이었다.
		return RenderRefusal(tool, "찾는 것이 없다: "+clip(err.Error(), 300),
			NotFoundGuidance(err, s.id.ProjectID))
	}
	s.log.ErrorContext(context.Background(), "도구 실패",
		"mode", tool, "project", s.id.ProjectID, "error", err.Error())
	return fmt.Sprintf("%s 실패: %s", tool, clip(err.Error(), 1200))
}

// ─────────────────────────────────────────────────────────────────────────────
// 꼬리
// ─────────────────────────────────────────────────────────────────────────────

type tailOpts struct {
	overlaps     []judge.Overlap
	observed     bool   // 이 도구가 경로 축을 **실제로** 읽었나
	overlapsNote string // 안 읽었으면 왜

	// notes·haveNote — 이 도구가 알림 축을 **이미 읽어 왔다면** 그 값이다.
	// board 가 그렇다: 같은 보드 응답에 실려 오므로 다시 부르면 조정 서버가
	// 같은 파생을 두 번 한다. haveNote 를 불리언으로 따로 두는 이유는
	// "0건을 읽어 왔다"와 "안 읽어 왔다"가 nil 슬라이스로는 구분되지 않아서다.
	notes    []model.Judgment
	haveNote bool
}

// degradedResult 는 열화 오류를 도구 결과로 옮긴다. 열화가 아니면 ok=false 다.
//
// isError 판정을 여기서 하지 않고 DegradedIsError(순수 함수)에 두는 이유:
// "아웃박스에 쌓았다"를 실패로 내면 세션이 판단이 사라진 줄 알고 다시 쓰고,
// "선점 못 했다"를 성공으로 내면 남의 항목 위에서 일한다 — 둘 다 이 도구가 없애려는 사고다.
func (s *Server) degradedResult(ctx context.Context, tool string, err error) (toolResult, bool) {
	deg, ok := AsDegraded(err)
	if !ok {
		return toolResult{}, false
	}
	isErr := DegradedIsError(deg.Mode)
	s.log.WarnContext(ctx, "열화 결과를 냈다",
		"mode", tool, "reason", deg.Reason, "status", string(deg.Mode),
		"project", s.id.ProjectID)
	return textResult(s.withTail(ctx, RenderDegraded(deg), tailOpts{}), isErr), true
}

func (s *Server) withTail(ctx context.Context, body string, o tailOpts) string {
	return strings.TrimRight(body, "\n") + "\n\n" + s.tail(ctx, o)
}

// tail 은 모든 응답에 붙는 꼬리다 — 미확인 알림 · 겹침 · 정체 배너.
func (s *Server) tail(ctx context.Context, o tailOpts) string {
	in := TailInput{
		Banner:           s.id.Banner(),
		Now:              s.now(),
		Overlaps:         o.overlaps,
		OverlapsObserved: o.observed,
		OverlapsNote:     o.overlapsNote,
	}
	if o.haveNote {
		// 이 도구가 이미 읽어 온 것이다. 같은 값을 다시 부르지 않는다.
		in.Notes, in.NotesObserved = FilterNotes(o.notes, s.currentSession(), tailNoteLimit), true
		return RenderTail(in)
	}
	notes, err := s.recentNotes(ctx)
	if err != nil {
		in.NotesError = clip(err.Error(), 300)
	} else {
		in.Notes, in.NotesObserved = notes, true
	}
	return RenderTail(in)
}

// recentNotes 는 **다른** 세션이 남긴 최근 ask·blocked 다.
//
// ★ Tier A 에는 확인(ack) 원장이 없다. 그래서 "미확인"을 "최근"으로 근사하고,
// 근사했다는 사실을 꼬리 문구가 그대로 말한다 — 침묵하면 읽는 쪽이
// "확인한 것은 빠진다"고 믿게 되고, 그 믿음이 §10 의 알림 확인율을 거짓으로 만든다.
//
// 저장 계층을 직접 읽지 않는다. 앞선 판은 svc.Store() 로 SQLite 를 직접 열었는데,
// 그 통로가 있는 한 이 서버는 **서버 머신에서만** 돌 수 있었다(설계 원칙 ③ 위반).
// 지금은 Backend 하나가 유일한 통로다.
func (s *Server) recentNotes(ctx context.Context) ([]model.Judgment, error) {
	if s.id.ProjectID == "" {
		return nil, nil
	}
	all, err := s.be.RecentNotes(ctx, s.id.ProjectID, 20)
	// 캐시된 알림은 **없는 것보다 낫다.** 낡았다는 사실은 이 응답의 본문(열화 배너)이
	// 이미 말하고 있고, 여기서 오류로 접으면 서버가 죽은 순간 꼬리가 통째로 침묵한다.
	if deg, ok := AsDegraded(err); ok && DegradedUsable(deg.Mode) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return FilterNotes(all, s.currentSession(), tailNoteLimit), nil
}

func (s *Server) currentSession() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// WithProject 는 프로젝트 좌표를 **주입**한다.
//
// 진입점이 git 으로 이미 푼 값을 그대로 쓴다 — 이 패키지가 다시 풀면 규칙이 두 벌이 되고,
// 두 벌은 반드시 표류한다. 워크트리에서 실제로 그렇게 갈렸다.
func WithProject(id, path string) Option {
	return func(b *builder) { b.projectID, b.projectPath = id, path }
}

// WithMachine 은 머신 id 를 **주입**한다. WithProject 와 같은 자리, 같은 이유다.
//
// 이 패키지가 스스로 푸는 규칙(hostname)은 진입점의 규칙(상태에 보관하는 안정 id)과 다르다.
// 그래서 같은 머신이 채널마다 다른 id 를 갖고, 세션 정체 3중키의 첫 축이 갈려
// **한 Claude 세션이 보드에 카드 여러 장으로 뜬다**(실물로 재현했다 — 3장).
// 프로젝트 축이 먼저 같은 사고를 겪고 주입으로 고쳤는데 머신 축만 그 교정에서 빠져 있었다.
func WithMachine(id string) Option {
	return func(b *builder) { b.machineID = strings.TrimSpace(id) }
}
