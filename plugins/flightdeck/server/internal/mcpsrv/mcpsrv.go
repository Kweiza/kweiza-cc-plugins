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
//     세션 귀속이 필요한 도구(pick·note·add·finish·land·label)를 거절한다.
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
	"github.com/kweiza/flightdeck/internal/window"
)

// ★ 이 서버는 DB 를 열지 않는다. 조정 서버에 붙는 통로는 Backend 하나뿐이고,
// 운영 배선은 그 REST 구현이다(backend.go 의 주석에 왜인지 있다).

// maxFrameBytes 는 한 프레임의 상한이다. 판단 본문이 크므로 넉넉하게 두되,
// 상한 자체는 있어야 한다 — 없으면 깨진 스트림 하나가 메모리를 통째로 먹는다.
const maxFrameBytes = 4 << 20 // 4MiB

// TailNoteLimit 은 꼬리에 싣는 알림 수다. 넘치면 board 로 간다.
const TailNoteLimit = 3

// Server 는 stdio MCP 서버 하나다.
type Server struct {
	be  Backend
	log *slog.Logger
	now func() time.Time

	id Identity

	beaconDir string // 창 비콘을 둘 디렉토리. 빈 값이면 심지 않는다(WithBeaconDir 참고)

	mu        sync.Mutex
	sessionID string // 게으르게 연다. 도구를 한 번도 안 부르면 세션 행도 안 생긴다
	openedCC  string // 그 카드를 **실제로 연** cc. 비콘이 있으면 s.id.CCSessionID 와 다르다
}

// Option 은 Server 의 선택 설정이다. 시험이 환경을 인자로 주기 위한 자리다 —
// os.Getenv 를 본문에 박으면 시험이 전역 환경을 흔들어야 하고, 그러면 병렬 시험이 서로를 깬다.
type Option func(*builder)

type builder struct {
	projectID   string
	projectPath string
	// projectSet 은 WithProject 가 **불렸는가**다. 값이 비었는가와 다른 질문이다 —
	// 진입점이 "좌표를 못 풀었다"고 말하는 유일한 방법이 빈 값을 주는 것이라,
	// 이 축이 없으면 그 말이 "아무 말도 안 했다"와 같게 접힌다(WithProject 주석).
	projectSet bool
	worktree   string
	machineID  string
	getenv     func(string) (string, bool)
	cwd        string
	cwdErr     error
	hostname   string
	hostErr    error
	now        func() time.Time
	beaconDir  string
	harness    string
}

// WithHarness 는 이 프로세스가 **어느 하네스의 것인지 선언한다**(DESIGN 「14. 하네스 축」).
//
// ★ 관측이 아니라 선언이다. 환경으로는 못 가르므로(중첩 실행이 양방향으로 거짓말한다)
// 진입점이 알려 줘야 하고, 안 알려 주면 「미상」으로 남는다 — claude 로 접지 않는다.
func WithHarness(name string) Option {
	return func(b *builder) { b.harness = name }
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
	id := ResolveIdentityAs(b.harness, b.getenv, b.cwd, b.cwdErr, b.hostname, b.hostErr)
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
	//
	// ★ **가르는 것은 값이 아니라 「불렸는가」다.** 앞선 판은 `if b.projectID != ""` 였고,
	// 그것이 진입점의 고침을 한 줄에서 통째로 무효화했다 — 진입점은 git 을 못 읽으면 프로젝트
	// id 를 **일부러 비워** 보내는데(cmd/fd 의 resolveProject: "지어내지 않는다"), 그 빈 값이
	// 여기서 "주입이 아예 없다"와 같게 접혀 옛 폴백이 **같은 이름을 다시 지어냈다.**
	// 실측: WithProject("", "…/wt") → ProjectID="wt". 그리고 조용했다 — 붙는 경고가
	// "워크트리에서는 주 저장소와 다를 수 있다"라 **git 을 못 읽었다는 말을 안 한다.**
	//
	// 두 상태는 요구가 정반대다. 「안 불렸다」는 이 패키지가 스스로 풀어야 하는 자리이고,
	// 「불렸는데 비었다」는 **호출자가 모른다고 답한 것**이라 우리가 대신 답하면 안 된다.
	if b.projectSet {
		id.ProjectID, id.ProjectPath = b.projectID, b.projectPath
	} else if id.ProjectID != "" {
		id.Warnings = append(id.Warnings,
			"프로젝트 좌표를 경로의 마지막 성분으로 정했다 — 워크트리에서는 주 저장소와 다를 수 있다")
	}
	// ★ 결손 판정은 **주입을 반영한 뒤** 다시 내린다. ResolveIdentity 가 매긴 것은 폴백 기준의
	// 답이라, 주입이 그 값을 비우거나(위) 반대로 채운 경우 둘 다 낡는다. 이 축이 낡으면
	// Banner 가 "관측되지 않은 축"을 틀리게 말하고, 그 배너는 모든 도구 응답 꼬리에 붙는다.
	id.Missing = withoutAxis(id.Missing, axisProject)
	if id.ProjectID == "" {
		id.Missing = append(id.Missing, axisProject)
	}

	// 워크트리도 **주입이 이긴다.** 프로젝트 축과 같은 규율이고, 안 넣었다가 같은 사고를 겪었다.
	//
	// ★ 이 패키지가 스스로 푸는 규칙(워크트리 = 자기 cwd)은 **저장소 하위 디렉토리에서 틀린다.**
	// 53c18ba 가 훅 쪽을 `git rev-parse --show-toplevel` 로 바꿨는데 이 계층은 안 바꿨고,
	// 그래서 두 계층이 같은 창에 다른 좌표를 매겼다. 세션 카드의 키가 (머신·워크트리·cc)
	// 3중키라 그 갈림은 가드 하나가 아니라 **카드 자체를 쪼갠다.**
	//
	// 실측(2026-08-04): 같은 cc·같은 머신인데 상하위 경로로 갈린 카드쌍 60건,
	// 남의 워크트리 하위 경로에 있는 카드 80건. session.worktree 에
	// `…/kweiza-cc-plugins` 와 `…/kweiza-cc-plugins/plugins/flightdeck/server` 가 나란히 있다.
	//
	// ★ 여기서 git 을 부르지 않는 이유는 프로젝트 축과 같다 — **같은 판정을 두 자리에 두지 않는다.**
	// `--show-toplevel` 은 이미 진입점(cmd/fd 의 resolveProject)이 푼다. 이 계층은 순수하게 남는다.
	if b.worktree != "" {
		id.Worktree = b.worktree
	} else if id.Worktree != "" {
		id.Warnings = append(id.Warnings,
			"워크트리를 cwd 로 정했다 — 저장소 하위 디렉토리에서 열면 훅의 --show-toplevel 과 갈려 카드가 쪼개진다")
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

	s.beaconDir = b.beaconDir
	s.plantBeacon()

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

	// 인자 삼킴 관문 — 도구 호출 계층이 인자 하나를 옆 인자에 통째로 삼켜 보낸 것을 막는다.
	// 근거 전문(원장 39건의 실측과 계층 셋을 가른 재현)은 arg_swallow.go 에 있다.
	//
	// ★ **도구별 핸들러가 아니라 여기다.** 판정의 축이 인자 **이름**이라 도구를 안 가리고,
	//   실제로 두 도구(finish 의 close_reason · note 의 body)에서 같은 모양으로 관측됐다.
	//   핸들러마다 놓으면 아홉째 도구가 생길 때 조용히 빠진다 — 이 저장소가 이미 한 번
	//   당한 부류다(관문을 스킬 축에만 걸어 옆 축에서 재발, c38e273).
	//
	// ★★ **신호보다는 뒤, 디스패치보다는 앞이다.** 뒤인 이유: 거절당한 호출도 **살아 있는
	//   세션이 낸 것**이라 mcp 신호는 참이다. 여기서 앞당기면 마크업을 못 고치는 세션이
	//   보드에서 침묵하고 사람이 죽었다고 읽는다. GateTool 이 신호보다 앞인 것은 규율이
	//   아니라 강제다 — 그쪽은 정체가 반쪽이라 귀속할 세션 자체가 없다(canAttribute).
	//   앞인 이유: 되돌릴 수 없는 쪽(finish 는 한 트랜잭션, note 는 판단 행)보다 뒤에 서면
	//   관문이 아니라 사후 보고가 된다.
	if ok, reason := judgeArgSwallowed(args); !ok {
		s.log.WarnContext(ctx, "인자 삼킴 거절",
			"mode", name, "session_id", clip(sessionID, 64), "project", s.id.ProjectID)
		return textResult(s.withTail(ctx, RenderRefusal(name, reason, ""), tailOpts{}), true)
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
	case "land":
		res = s.toolLand(ctx, sessionID, args)
	case "label":
		res = s.toolLabel(ctx, sessionID, args)
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

// BeaconKey 는 이 프로세스가 심을 창 좌표다. 심을 수 없으면 ok=false 다.
//
// ★ 부모가 claude 라는 보장이 없다. 실측: Cursor 가 띄운 fd mcp 의 부모는 node 이고
// 조상 사슬 어디에도 claude 가 없으며 CLAUDE_* 환경이 하나도 없다. 그 자리에 심으면
// 어떤 훅도 못 맞추는 pid 로 키가 잡히므로, 정체가 온전할 때만(canAttribute) 심는다 —
// beaconDir 이 비었을 때도 마찬가지다(시험 격리, WithBeaconDir 주석 참고).
func (s *Server) BeaconKey() (window.Key, bool) {
	k, err := s.beaconKey()
	return k, err == nil
}

// beaconKey 는 BeaconKey 와 **같은 판정이되 사유를 잃지 않는다.**
//
// ★ 사유를 나르는 갈래가 따로 있는 이유: BeaconKey 의 bool 은 심기 판정에 딱 맞지만
// (심을까 말까), beaconMiss 는 그 실패를 **사람에게 이름으로 말해야** 한다. 오류를
// 삼키면 리눅스 밖의 ErrUnsupported 가 "부모가 claude 가 아니다"로 둔갑하고, 그것을 읽은
// 사람은 아무 문제 없는 자기 프로세스 계보를 뒤지게 된다 — why 가 존재하는 이유가
// 원인에 도달시키는 것이라, 틀린 원인을 대는 것은 아무 원인도 안 대는 것보다 나쁘다.
func (s *Server) beaconKey() (window.Key, error) {
	if s.beaconDir == "" {
		return window.Key{}, errors.New("이 MCP 프로세스에 비콘 디렉토리가 설정되지 않았다")
	}
	if !s.canAttribute() {
		return window.Key{}, errors.New("이 프로세스의 정체가 반쪽이라 비콘 좌표를 만들 수 없다")
	}
	ppid := os.Getppid()
	started, err := window.StartedOf(ppid)
	if err != nil {
		return window.Key{}, fmt.Errorf("부모(pid %d)의 시작 시각을 못 읽었다: %w", ppid, err)
	}
	k := window.Key{MachineID: s.id.MachineID, ClaudePID: ppid, Started: started}
	if !k.Valid() {
		return window.Key{}, fmt.Errorf("창 좌표가 반쪽이다(machine=%q pid=%d)", clip(k.MachineID, 40), k.ClaudePID)
	}
	return k, nil
}

// plantBeacon 은 이 창의 자리를 표시한다. 실패해도 서버를 막지 않는다 —
// 비콘이 없으면 훅이 폴백하고, 그 폴백이 오늘 거동이다.
func (s *Server) plantBeacon() {
	k, ok := s.BeaconKey()
	if !ok {
		return
	}
	if _, err := window.Plant(s.beaconDir, k, s.id.Worktree, s.id.CCSessionID, s.now()); err != nil {
		s.log.WarnContext(context.Background(), "창 비콘 심기 실패", "error", err.Error())
	}
}

// beaconMiss 는 이 프로세스가 비콘으로 표류를 못 짚은 사유다. RenderDrift 의 why 로 간다.
//
// ★ 훅의 사유는 여기서 알 수 없다 — 훅은 다른 프로세스다. 그래서 훅이 낼 사유를 흉내내지
// 않고 **이 서버가 실제로 아는 것**만 말한다: 비콘 디렉토리가 있는지, 이 프로세스의 비콘
// 좌표를 만들 수 있는지(BeaconKey), 그 좌표로 비콘을 읽을 수 있는지(ensureSession 이
// 이미 쓰는 판정과 같다) — 딱 그 셋이다. 셋 다 통과했다면(비콘을 읽었다면) 이 프로세스
// 쪽에서는 막힌 데가 없다는 뜻이라 사유를 비운다 — 표류는 **남의** 카드 얘기라 내 비콘이
// 멀쩡해도 남을 수 있고, 그 경우의 진짜 사유는 이 프로세스가 알 길이 없다.
func (s *Server) beaconMiss() string {
	k, err := s.beaconKey()
	if err != nil {
		return beaconMissReason(err)
	}
	if _, lerr := window.Load(s.beaconDir, k); lerr != nil {
		// ★ 앞에 "비콘을 못 읽었다:" 를 덧붙이지 않는다 — window.Load 의 오류가 이미 그 말로
		// 시작한다. 겹쳐 쓰면 사람이 진짜 원인(파일명·errno)에 닿기 전에 읽기를 멈춘다.
		return beaconMissReason(lerr)
	}
	return ""
}

// beaconMissReason 은 좌표 실패 하나를 사람이 읽는 사유로 만든다. 순수 함수다.
//
// ★ ErrUnsupported 만 갈라 낸다. 그 경우의 진짜 원인은 이 프로세스의 계보가 아니라
// **플랫폼**이고, 그것을 말해 주지 않으면 읽는 사람이 멀쩡한 부모 사슬을 뒤진다.
// 나머지는 오류가 이미 자기 말을 갖고 있으므로 덧칠하지 않는다.
func beaconMissReason(err error) string {
	if errors.Is(err, window.ErrUnsupported) {
		return "이 플랫폼에서는 프로세스 계보를 읽을 수 없다 — 비콘 통로 자체가 없다(리눅스 전용)"
	}
	return err.Error()
}

// ensureSession 은 세션을 한 번만 연다. 같은 3중키면 같은 세션이므로 재호출도 안전하지만,
// 매번 열면 git 파생이 도구 호출마다 돌아 첫 명령이 느려진다 — 그 느림이 기존 도구의 병목 3위였다.
func (s *Server) ensureSession(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID != "" {
		return s.sessionID, nil
	}

	// ★ 비콘이 있으면 cc 만 그 값을 쓴다. 기동 시 주입된 s.id.CCSessionID 는 exec 이후
	// 못 바뀌지만, 훅은 매번 새 프로세스라 /clear·compact 로 갈린 새 cc 를 비콘에 적어 둔다.
	// 여기서 그 값을 집어야 두 프로세스(훅·MCP)가 같은 3중키로 같은 카드를 연다.
	//
	// s.id 자체는 안 고친다 — s.id 는 뮤텍스 없이 여러 곳에서 읽힌다(callTool·toolBoard·
	// errText·tail 응답 등). 그 필드를 가변으로 만들면 지금 코드에 없는 경쟁이 생긴다.
	// 대신 **s.sessionID 옆에**(같은 뮤텍스 아래) 적어 둔다 — 이 둘은 한 쌍이다:
	// "어느 카드를, 어느 cc 로 열었나". 표류 판정은 그 쌍을 기준점으로 써야 한다(openedIdentity).
	cc := s.id.CCSessionID
	if k, ok := s.BeaconKey(); ok {
		if b, err := window.Load(s.beaconDir, k); err == nil && strings.TrimSpace(b.CCSessionID) != "" {
			cc = b.CCSessionID
		}
	}

	res, err := s.be.OpenSession(ctx, service.OpenSessionInput{
		Project:     s.id.ProjectID,
		ProjectPath: s.id.ProjectPath,
		MachineID:   s.id.MachineID,
		Hostname:    s.id.Hostname,
		Worktree:    s.id.Worktree,
		CCSessionID: cc,
		Harness:     s.id.Harness,
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
	s.sessionID, s.openedCC = res.Session.ID, cc
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
	//
	// why 는 twins 가 있을 때만 구한다 — beaconMiss 가 비콘 파일을 읽으므로, 표류가 없는
	// (대부분의) board 호출에서까지 그 I/O 를 낼 이유가 없다.
	//
	// ★★ 기준점은 s.id 가 **아니다.** s.id.CCSessionID 는 exec 때 주입된 뒤 안 바뀌는 값이고,
	// 카드는 비콘의 cc 로 열린다(ensureSession) — /clear 뒤에는 그 둘이 **정상적으로** 다르다.
	// s.id 로 재면 방금 수리에 성공한 자기 카드를 자기가 표류로 고발한다.
	self := s.openedIdentity(sessionID)
	twins := DriftedTwins(self, liveIdentitiesOf(view.Sessions))
	why := ""
	if len(twins) > 0 {
		why = s.beaconMiss()
	}
	if d := RenderDrift(twins, self.SessionID, self.CCSessionID, why); d != "" {
		if notice != "" {
			notice += "\n"
		}
		notice += d
	}

	notes := append(append([]model.Judgment(nil), view.Asks...), view.Blocked...)
	tail := s.tail(ctx, tailOpts{
		// self 는 위에서 이미 구한 openedIdentity 다 — 이 프로세스가 **실제로 연 카드**의
		// 좌표라, 표류 배너가 쓰는 것과 같은 기준점이다. 둘이 갈리면 배너는 "갈렸다"고
		// 하는데 겹침은 형제를 못 빼는 상태가 된다.
		overlaps: judge.OverlapsWithLive(mine, liveOf(view.Sessions), sessionID, self.CCSessionID),
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

// openedIdentity 는 표류 판정의 기준점이다 — 이 프로세스가 **실제로 연 카드**의 좌표다.
//
// ★ sessionID 를 인자로 받는 이유: 호출부(toolBoard)가 이미 그 값을 들고 있고, 그것이
// 이번 호출이 자기 카드로 삼은 값이다. 여기서 s.sessionID 를 다시 읽으면 그 사이에 바뀐
// 값을 볼 수 있어 "응답이 self 로 표시한 카드"와 "표류 판정이 자기라고 본 카드"가 갈린다.
//
// ★ 아직 카드를 안 열었으면(canAttribute 가 거짓이거나 열기가 실패한 경우) env 의 cc 로
// 떨어진다 — 오늘 거동이고, 그 경우 s.id 축이 대개 반쪽이라 DriftedTwins 가 어차피 nil 이다.
func (s *Server) openedIdentity(sessionID string) LiveIdentity {
	s.mu.Lock()
	cc := s.openedCC
	s.mu.Unlock()
	if cc == "" {
		cc = s.id.CCSessionID
	}
	return LiveIdentity{
		SessionID: sessionID, MachineID: s.id.MachineID,
		Worktree: s.id.Worktree, CCSessionID: cc,
	}
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
			// 규모도 함께 넘긴다. 이 변환이 두 자리(service.liveFor · 여기)에 있는데,
			// 한쪽만 고치면 board 와 pick 중 한쪽에서만 규모가 뜬다.
			Delta: c.View.PathDelta,
			// ★ 대화 id 를 함께 넘긴다 — 없으면 형제 카드가 남으로 보여 자기 자신과
			// 겹친다고 알린다. 판정은 judge.OverlapsWithLive 한 자리에만 있다.
			CCSessionID: c.View.Session.CCSessionID,
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
	//   근거를 여섯 축으로 나란히 보여준 뒤에." 인자를 조용히 무시하면
	//   에이전트가 회수됐다고 믿고 남의 작업 위에서 일한다.
	//
	//   ★ 여섯째(종료 선언)는 세션이 아니라 **항목**을 묻는다 — 이 항목을 닫으려다
	//   롤백된 finish 가 원장에 있나. 다섯 축이 전부 "저 세션은 떠났다"로 옳았는데도
	//   회수가 사고가 된 실측이 그 축을 낳았다(2026-08-04~05, 같은 모양 넷).
	if strings.TrimSpace(a.StealReason) != "" {
		return textResult(s.withTail(ctx, RenderRefusal("pick",
			"steal_reason 이 왔지만 이 서버는 선점을 회수하지 않는다",
			"회수는 사람만 한다 — 마지막 신호 종류·나이, 발자국 경로 수, 원격 마지막 커밋 시각, "+
				"미푸시 커밋 수, 마지막 판단, 그리고 그 항목의 종료 선언 여섯 축을 나란히 본 뒤에야 한다(설계 §4). "+
				"하나의 신호로 판정해 두 번 틀렸다. 지금 할 수 있는 것: note(kind=ask) 로 점유자에게 묻거나, "+
				"웹 대시보드의 '선점 회수' 버튼(사유 필수)을 쓴다."), tailOpts{}), true)
	}
	// ★ 반납은 **집기 앞**에서 갈린다. 뒤에 두면 leave 와 item_id 가 함께 온 호출이
	//   먼저 집고 나서 놓는 꼴이 되고, 그러면 "놓으려던 것"과 "방금 집은 것"이 같은
	//   id 로 겹쳐 원장에 선점 한 쌍이 헛으로 남는다.
	//
	//   land 의 leave 와 같은 이름인 것은 같은 판정이기 때문이다 — 자기가 빠지는 축.
	//   회수 축(steal_reason)은 위에서 이미 거절됐으므로 여기 오는 것은 전부 자기 것이다.
	if strings.TrimSpace(a.Leave) != "" {
		if len(a.ItemIDs) > 0 {
			return textResult(s.withTail(ctx, RenderRefusal("pick",
				"leave 와 item_ids 를 함께 줬다",
				"반납은 전부 아니면 하나다 — 내가 쥔 전부를 놓으려면 leave 만, "+
					"하나만 놓으려면 item_id 와 함께 줘라."), tailOpts{}), true)
		}
		res, err := s.be.LeaveClaim(ctx, service.LeaveInput{
			Project: s.id.ProjectID, SessionID: sessionID,
			ItemID: strings.TrimSpace(a.ItemID), Reason: strings.TrimSpace(a.Leave),
		})
		if err != nil {
			if r, ok := s.degradedResult(ctx, "pick", err); ok {
				return r
			}
			return textResult(s.withTail(ctx, s.errText("pick", err), tailOpts{}), true)
		}
		return textResult(s.withTail(ctx, RenderLeave(res), tailOpts{}), false)
	}
	// ★ 둘을 동시에 주면 합치거나 한쪽을 우선하지 않고 거절한다. 어느 쪽을
	//   골라도 "무엇을 집었는가"가 호출자의 의도가 아니라 서버의 임의 선택이
	//   되고, 그것이 이 도구가 지키려는 사실 그 자체를 흐린다.
	if strings.TrimSpace(a.ItemID) != "" && len(a.ItemIDs) > 0 {
		return textResult(s.withTail(ctx, RenderRefusal("pick",
			"item_id 와 item_ids 를 함께 줬다",
			"둘 중 하나만 써라 — 하나면 item_id, 묶음이면 item_ids 에 선두부터 순서대로."), tailOpts{}), true)
	}

	res, err := s.be.Pick(ctx, service.PickInput{
		Project: s.id.ProjectID, SessionID: sessionID,
		ItemID: strings.TrimSpace(a.ItemID), ItemIDs: a.ItemIDs,
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
	// ★ **보낸 것과 돌아온 것을 대조한다.** 이 자리가 요청(a.ItemIDs)과 응답(res)을
	// 둘 다 보는 유일한 지점이다 — 백엔드가 in-process 서비스든 원격 서버를 치는
	// cmd/fd 프록시든 똑같이 지난다.
	//
	// 안 하면: item_ids 를 모르는 구서버가 선두만 집고 200 을 내는데(양쪽 api_version
	// 이 "1" 이라 SkewBanner 는 안 뜬다) 이 도구는 그걸 성공으로 렌더한다. 세션은
	// 안 쥔 항목을 쥐었다고 믿고 남의 작업 위에서 일한다 — 선점이 막으려는 사고 그 자체다.
	//
	// isError=true 로 낸다. 본문은 그대로 실어 보낸다 — 선두는 실제로 집혔을 수 있고
	// 그 브랜치·워크트리 명령을 지우면 성공한 절반까지 함께 버리는 셈이 된다.
	if missing := judge.UnaccountedIDs(a.ItemIDs, res.AccountedIDs()); len(missing) > 0 {
		// 회계 경고는 **본문의 끝**이다 — 꼬리가 아니다. 그래서 본문 쪽에 이어 붙인 뒤
		// joinTail 에 넘긴다. 앞선 판은 여기서 꼬리를 손으로 이으면서 개행을 하나만
		// 적었고, 그것이 맞았던 것은 RenderBundleUnaccounted 가 개행으로 끝났기 때문이다.
		return textResult(joinTail(
			notice+RenderPick(res, s.now())+"\n\n"+RenderBundleUnaccounted(missing), tail), true)
	}
	return textResult(joinTail(notice+RenderPick(res, s.now()), tail), false)
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
		ItemProject: strings.TrimSpace(a.ItemProject),
		Supersedes:  strings.TrimSpace(a.Supersedes),
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
	//
	// ★★ **후속 도착만은 여기서 막는다 — 안쪽에서는 원리적으로 못 본다.** followups 키가
	//    왔는데 0건으로 해석됐으면 그것은 뜻이 없거나 전송 계층이 값을 흘린 자국이다.
	//    안쪽 홉(cmd/fd/wire.go)이 `omitempty` 로 빈 목록을 키째 지우므로 REST 핸들러는
	//    "안 보냈다"와 "비워 보냈다"를 이미 구분할 수 없다. 근거 전문은 followups_arrival.go 에 있다.
	//
	// ★ **처방을 안 붙인다(service.FollowupsGuidance 를 재사용하지 않는다).** 그 문구는
	//    judgeMissingFollowups 전용이고 마지막 문장이 "이 관문은 **한 번만** 막는다 —
	//    그대로 다시 불러라"다. 이 관문은 **매번** 막으므로 그 말을 따르면 무한히 거절당한다
	//    (문구 하나로 "관문이 벽이 된다"에 도달하는 경로다). 그 문구의 "위에 이름이 나온
	//    항목들"도 여기서는 가리킬 목록이 없다. 사유 자체가 이미 복구 경로 셋을 담는다.
	if ok, reason := judgeFollowupsArrived(raw, len(a.Followups)); !ok {
		return textResult(s.withTail(ctx, RenderRefusal("finish", reason, ""), tailOpts{}), true)
	}
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

// toolLand 는 랜딩 줄 하나를 다룬다 — 인자가 무엇을 채웠는지로 동작을 고른다
// (줄 서기/내 자리 · result=보고+반납 · leave=이탈). release 는 동작이 아니라 거절 사유다.
func (s *Server) toolLand(ctx context.Context, sessionID string, raw json.RawMessage) toolResult {
	var a landArgs
	if err := decodeArgs(raw, &a); err != nil {
		return textResult(s.withTail(ctx, s.errText("land", err), tailOpts{}), true)
	}

	// ★ 회수는 이 서버가 하지 않는다 — pick 의 steal_reason 거절과 **같은 판정, 같은 문장 틀**이다.
	//   한 서버가 선점 회수는 거절하고 레인 회수는 허용하면 그 거절 문구가 화면에서 거짓이 된다.
	//
	//   ★ **축 목록만 갈린다.** pick 은 여섯이고 여기는 다섯이다 — 여섯째(종료 선언)는
	//   항목에 붙는 사실인데 이쪽의 회수 대상은 줄 행이라 그 축이 존재하지 않는다(설계 §4).
	//   수를 맞추면 이 응답이 없는 축을 보라고 말하게 된다. 그 갈림은 표류가 아니라
	//   대상이 다르다는 사실이고, reclaim_axes_test.go 가 양쪽을 각각 잠근다.
	if strings.TrimSpace(a.Release) != "" {
		return textResult(s.withTail(ctx, RenderRefusal("land",
			"release 가 왔지만 이 서버는 레인을 회수하지 않는다",
			"회수는 사람만 한다 — 마지막 신호 종류·나이, 발자국 경로 수, 원격 마지막 커밋 시각, "+
				"미푸시 커밋 수, 마지막 판단 다섯 축을 나란히 본 뒤에야 한다(설계 §4). "+
				"지금 할 수 있는 것: note(kind=ask) 로 점유자에게 묻거나, "+
				"`fd lane release --row <id> --reason \"...\"` 를 쓴다."), tailOpts{}), true)
	}

	result := strings.TrimSpace(a.Result)
	leave := strings.TrimSpace(a.Leave)

	// ★ resources 는 줄 서기(acquire)에만 성립한다 — 보고·이탈은 줄 행 전체(all-or-nothing 의
	// 짝)에 걸리는 동작이라 자원 인자를 받을 자리가 없다. 조용히 버리면 세션은 "resources 로
	// 준 자원만 반납/이탈했다"고 믿는데, 실제로는 행에 묶인 자원 집합 전부가 한 번에 움직인다
	// (service.LandReport·LandLeave 가 행의 Resources 를 그대로 훑는다) — 그 믿음의 어긋남이
	// 조용한 오반납이다. 그래서 다른 두 갈래(result·leave 동시 입력, release)와 같은 자리에서
	// 거절한다.
	if (result != "" || leave != "") && len(a.Resources) > 0 {
		return textResult(s.withTail(ctx, RenderRefusal("land",
			"resources 는 줄 서기(acquire)에만 성립한다 — 보고·이탈은 네 줄 행 전체에 걸린다",
			"반납·이탈은 행의 자원 집합 전부가 한 번에 움직인다(all-or-nothing 의 짝)."), tailOpts{}), true)
	}

	var res service.LandResult
	var err error
	switch {
	case result != "" && leave != "":
		// 둘 다 채운 것은 서버가 조용히 하나를 고를 일이 아니다 — 보고와 이탈은 다른 원장 결과다.
		return textResult(s.withTail(ctx, RenderRefusal("land",
			"result 와 leave 를 함께 줬다 — 보고와 이탈은 다른 동작이다",
			"한 번에 하나만 해라: 레인을 반납하려면 result, 줄에서 완전히 빠지려면 leave."), tailOpts{}), true)
	case result != "":
		res, err = s.be.LandReport(ctx, service.LandReportInput{
			Project: s.id.ProjectID, SessionID: sessionID,
			Kind: model.LandingLeftKind(result), Detail: a.Detail,
		})
	case leave != "":
		res, err = s.be.LandLeave(ctx, service.LandLeaveInput{
			Project: s.id.ProjectID, SessionID: sessionID, Detail: leave,
		})
	default:
		res, err = s.be.Land(ctx, service.LandInput{
			Project: s.id.ProjectID, SessionID: sessionID, Resources: a.Resources,
		})
	}
	if err != nil {
		if r, ok := s.degradedResult(ctx, "land", err); ok {
			return r
		}
		return textResult(s.withTail(ctx, s.errText("land", err), tailOpts{}), true)
	}
	return textResult(s.withTail(ctx, RenderLand(res, s.now()), tailOpts{}), false)
}

// toolLabel 은 이미 있는 항목의 꼬리표를 고친다.
//
// ★ 고칠 수 있는 축은 **꼬리표 하나뿐**이다 — 일반 amend 가 아니다(설계 §11).
func (s *Server) toolLabel(ctx context.Context, sessionID string, raw json.RawMessage) toolResult {
	var a labelArgs
	if err := decodeArgs(raw, &a); err != nil {
		return textResult(s.withTail(ctx, s.errText("label", err), tailOpts{}), true)
	}
	res, err := s.be.SetLabels(ctx, service.LabelInput{
		Project: s.id.ProjectID, SessionID: sessionID,
		ItemID: strings.TrimSpace(a.ItemID), Add: a.Add, Rm: a.Rm,
	})
	if err != nil {
		if r, ok := s.degradedResult(ctx, "label", err); ok {
			return r
		}
		return textResult(s.withTail(ctx, s.errText("label", err), tailOpts{}), true)
	}
	return textResult(s.withTail(ctx, RenderLabel(res), tailOpts{}), false)
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

// withTail 은 본문에 이 호출의 꼬리를 붙인다. 조립 자체는 joinTail 한 자리가 한다 —
// 이 함수는 꼬리를 **만드는** 일(s.tail)과 붙이는 일을 잇기만 한다.
func (s *Server) withTail(ctx context.Context, body string, o tailOpts) string {
	return joinTail(body, s.tail(ctx, o))
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
		in.Notes, in.NotesObserved = FilterNotes(o.notes, s.currentSession(), TailNoteLimit), true
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
	return FilterNotes(all, s.currentSession(), TailNoteLimit), nil
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
// ★ **빈 id 를 주는 것은 "안 주는 것"과 다르다.** 진입점이 git 을 못 읽었을 때 그 사실을
// 이 계층에 전하는 방법이 그것뿐이라, 이 옵션은 값과 별개로 **불렸다는 사실**을 기록한다.
// 시그니처를 안 바꾸는 이유도 그것이다 — 호출부(cmd/fd 의 mcp.go)는 이미 옳은 값을 넘기고
// 있었고, 갈리던 것은 이 계층의 해석 하나였다.
func WithProject(id, path string) Option {
	return func(b *builder) { b.projectID, b.projectPath, b.projectSet = id, path, true }
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

// WithWorktree 는 세션의 워크트리 절대경로를 **주입**한다. 위 둘과 같은 자리, 같은 이유다.
//
// 이 패키지가 스스로 푸는 규칙(워크트리 = 자기 cwd)은 진입점의 규칙
// (`git rev-parse --show-toplevel`, 53c18ba)과 다르다. 저장소 하위 디렉토리에서 Claude Code 를
// 열면 그 둘이 갈리고, 3중키의 둘째 축이 갈리므로 **한 창이 카드 두 장으로 열린다.**
// 프로젝트 축과 머신 축이 차례로 같은 사고를 겪고 주입으로 고쳤는데 워크트리 축만 남아 있었다.
//
// 빈 값은 주입이 아니다 — 안 넣은 것과 같이 다뤄 cwd 규칙으로 떨어지고 경고를 남긴다.
// 지어낸 좌표로 카드를 여는 것보다 "cwd 로 정했다"고 말하는 쪽이 낫다.
func WithWorktree(path string) Option {
	return func(b *builder) {
		if p := strings.TrimSpace(path); p != "" {
			b.worktree = filepath.Clean(p)
		}
	}
}

// WithBeaconDir 는 창 비콘을 둘 디렉토리를 준다.
//
// ★ **이 옵션이 없으면 심지 않는다.** 여기서 기본 경로로 떨어지면 go test 가 개발자의
// 진짜 ~/.flightdeck/windows/ 에 파일을 쓴다 — cmd/fd 는 그 사고를
// TestUnpinnedEnvNeverReachesTheRealHome 으로 막지만 이 패키지에는 그런 방어가 없다.
// 경로를 고르는 판단은 window.Dir 하나가 갖고, 그것을 부르는 것은 배선(cmd/fd)의 일이다.
func WithBeaconDir(dir string) Option {
	return func(b *builder) { b.beaconDir = dir }
}
