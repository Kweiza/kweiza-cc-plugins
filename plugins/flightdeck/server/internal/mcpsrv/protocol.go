package mcpsrv

import (
	"encoding/json"
	"strings"
)

// JSON-RPC 2.0 · MCP 프레임 — 표준 라이브러리만 쓴다.
//
// SDK 를 넣지 않는 이유는 의존성 최소주의만이 아니다. 이 계층이 하는 일은
// "줄 하나 = JSON 하나"를 읽고 쓰는 것뿐이고, 그 정도의 코드를 남의 판올림에 묶으면
// 프로토콜 버전이 바뀌는 날 우리가 못 고치는 자리가 생긴다.

// 서버 정체. tools/list 의 전체 이름(mcp__plugin_flightdeck_fd__<도구>)은 호스트가 만든다 —
// 우리는 짧은 이름만 낸다.
const (
	ServerName    = "flightdeck"
	ServerVersion = "0.1.0"
)

// Instructions 는 세션 시작에 컨텍스트로 실리는 서버 안내다. **설계 §6 의 문구 그대로**다.
//
// ★ 여기에 규율 산문을 넣지 않는다. 세션 시작에는 도구 이름과 이 문자열만 실리고,
// 그 예산이 도구를 7개로 눌러 잡은 이유다(설계 §6). 규율은 **필요할 때, 그 자리에서**
// 응답에 싣는다 — finish 를 body 없이 부르면 그때 무엇을 적어야 하는지가 온다.
const Instructions = "작업은 `pick`, 판단은 `note`, 끝나면 `finish`. 락은 없다.\n" +
	"head·branch·sha·랜딩 이력은 서버가 git 에서 읽으므로 적지 마라.\n" +
	"겹침·선점·미확인 결과는 응답 꼬리에 온다."

// InstructionsLimit 은 Instructions 의 상한이다(설계 §6: "서버 instructions 는 300자 고정").
// 시험이 이 상수로 단정한다 — 문구가 자라면 그 자리에서 빨간불이 난다.
const InstructionsLimit = 300

// DefaultProtocolVersion 은 클라이언트가 모르는 버전을 요청했을 때 우리가 답하는 버전이다.
const DefaultProtocolVersion = "2025-06-18"

// supportedProtocols 는 우리가 아는 프로토콜 버전이다.
//
// 프레이밍(줄 단위 JSON)과 이 서버가 쓰는 메서드 다섯은 이 셋에서 같다.
var supportedProtocols = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// NegotiateProtocol 은 클라이언트가 요청한 버전에 답할 버전을 고른다. 순수 함수다.
//
// 아는 버전이면 **그대로 되돌려준다**(클라이언트가 그 버전으로 말하겠다는 뜻이므로).
// 모르는 버전이면 우리 기본값을 낸다 — 클라이언트가 그걸 보고 끊을지 이어갈지 정한다.
// 조용히 클라이언트 문자열을 그대로 반향하면 우리가 모르는 규약을 안다고 거짓말하는 것이 된다.
func NegotiateProtocol(requested string) string {
	r := strings.TrimSpace(requested)
	for _, v := range supportedProtocols {
		if r == v {
			return v
		}
	}
	return DefaultProtocolVersion
}

// ─────────────────────────────────────────────────────────────────────────────
// 프레임
// ─────────────────────────────────────────────────────────────────────────────

// rpcRequest 는 들어오는 프레임 하나다.
//
// ID 를 json.RawMessage 로 두는 이유: 규약이 문자열·숫자를 모두 허용하므로
// 타입을 정하면 한쪽이 깨진다. 받은 것을 **그대로** 되돌려준다.
// 필드 자체가 없으면 알림(notification)이고 응답을 내지 않는다.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (r rpcRequest) isNotification() bool { return len(r.ID) == 0 }

// rpcError 는 프로토콜 오류다. **도구 실패는 여기로 오지 않는다** —
// 도구가 실패했다는 것은 에이전트가 읽고 고칠 수 있는 결과이므로 isError 내용으로 간다.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// JSON-RPC 2.0 표준 오류 코드.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

var nullID = json.RawMessage("null")

func okResponse(id json.RawMessage, result any) rpcResponse {
	if len(id) == 0 {
		id = nullID
	}
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errResponse(id json.RawMessage, code int, msg, data string) rpcResponse {
	if len(id) == 0 {
		id = nullID
	}
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg, Data: clip(data, 800)}}
}

// ─────────────────────────────────────────────────────────────────────────────
// initialize
// ─────────────────────────────────────────────────────────────────────────────

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
	ClientInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type capabilities struct {
	Tools toolsCapability `json:"tools"`
}

type initializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    capabilities `json:"capabilities"`
	ServerInfo      serverInfo   `json:"serverInfo"`
	Instructions    string       `json:"instructions"`
}

// ─────────────────────────────────────────────────────────────────────────────
// tools/call
// ─────────────────────────────────────────────────────────────────────────────

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolResult 는 도구 한 번의 결과다.
//
// IsError 에 omitempty 를 두지 않는다 — 거짓을 생략하면 "성공"과 "이 서버는 이 축을
// 안 낸다"가 클라이언트 쪽에서 같은 모양이 된다.
type toolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError"`
}

func textResult(s string, isErr bool) toolResult {
	return toolResult{Content: []contentBlock{{Type: "text", Text: s}}, IsError: isErr}
}

// clip 은 외부에서 온 문자열을 자르고 제어문자를 걷어낸다.
// 로그 주입과 무한장 오류 메시지를 막는다(service.clip 과 같은 규율 — 그쪽은 비공개다).
func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}
