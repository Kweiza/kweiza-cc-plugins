package mcpsrv

// 도구 6개 — 설계 §6 표 그대로. 늘리지 않는다.
//
// ★ 설명 문구를 짧게 유지하는 것이 이 파일의 규율이다. 세션 시작에 실리는 것은
// 도구 이름과 설명, 그리고 서버 instructions 뿐이고 그 예산이 도구 수를 6개로
// 눌러 잡은 이유다(설계 §6). **규율 산문은 여기 없다** — 응답 꼬리에 있다.

// Tool 은 tools/list 가 내는 항목 하나다.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	// 추가 필드를 막는다. 열어 두면 오타 난 인자가 조용히 무시되고,
	// 그러면 "안 줬다"와 "잘못 줬다"가 구분되지 않는다.
	m["additionalProperties"] = false
	return m
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func strArr(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

func enumStr(desc string, values ...string) map[string]any {
	vs := make([]any, 0, len(values))
	for _, v := range values {
		vs = append(vs, v)
	}
	return map[string]any{"type": "string", "enum": vs, "description": desc}
}

// afterSchema 는 선행 조건 하나다. **브랜치 이름을 담을 자리가 없다** —
// 랜딩이 끝나면 브랜치가 지워져 조건이 충족되는 바로 그 순간 해석 불가가 되기 때문이다(설계 §3).
func afterSchema() map[string]any {
	return map[string]any{
		"type":        "array",
		"description": "선행 조건. 항목 하나에 item·sha·job 중 정확히 하나만 채운다(브랜치 이름은 못 쓴다)",
		"items": obj(map[string]any{
			"item": str("미랜딩 선행 항목 id"),
			"sha":  str("이미 랜딩된 커밋 sha"),
			"job":  str("선행 잡 id (Tier B)"),
		}),
	}
}

func followupSchema() map[string]any {
	return map[string]any{
		"type":        "array",
		"description": "이번에 나온 후속. 같은 호출에 넣으면 판단과 FK 로 이어진다",
		"items": obj(map[string]any{
			"id":     str("항목 id — 브랜치 이름이 된다"),
			"title":  str("한 줄 제목"),
			"body":   str("무엇을 해야 하는가"),
			"paths":  strArr("이 항목이 건드릴 경로"),
			"labels": strArr("표시 전용 꼬리표"),
			"after":  afterSchema(),
		}, "id", "title", "body"),
	}
}

// tools 는 이 서버가 내는 전부다. 순서 고정 — tools/list 가 같은 입력에 같은 답이어야 한다.
var tools = []Tool{
	{
		Name:        "board",
		Description: "살아 있는 세션·신호 나이·만지는 경로·선점·큐 요약. detail 로 확대한다.",
		InputSchema: obj(map[string]any{
			"detail": map[string]any{"type": "boolean",
				"description": "전 세션·전 경로·큐 목록·막힘/요청 판단까지 낸다(기본 false)"},
		}),
	},
	{
		Name:        "pick",
		Description: "인자 없으면 추천 1건과 탈락 사유 전부. item_id 를 주면 선점하고 맥락을 낸다.",
		InputSchema: obj(map[string]any{
			"item_id": str("집을 항목 id. 없으면 추천만 하고 선점하지 않는다"),
			"steal_reason": str("남의 선점을 회수하는 사유. **이 서버는 회수하지 않는다** — " +
				"주면 사유와 함께 거절한다"),
		}),
	},
	{
		Name:        "note",
		Description: "판단을 남긴다. 파생 불가한 유일한 자산이다.",
		InputSchema: obj(map[string]any{
			"kind": enumStr("판단 종류",
				// ★ 'now' 가 여기 없다. 저장은 되지만 보드가 안 읽어서(service/board.go 는
				// ask·blocked 만 읽는다) 쓴 세션에게만 보이고 남에게는 안 보인다 —
				// 쓸 수 있는데 안 보이는 것은 거짓 초록이다(설계 §11).
				// model.JudgmentNow 상수와 검증 목록은 남는다: 레거시 임포터가 생산하고
				// DB 에 이미 행이 있어, 지우면 그 행을 못 읽게 된다.
				"handoff", "decision", "blocked", "ask", "rejected", "not-done", "verified", "draft"),
			"body":       str("본문. 비면 거절한다"),
			"title":      str("한 줄 제목"),
			"item_id":    str("이 판단이 걸리는 큐 항목 id"),
			"supersedes": str("정정 대상 판단 id. 덮어쓰기는 없다 — 새 행이 옛 행을 가리킨다"),
		}, "kind", "body"),
	},
	{
		Name:        "add",
		Description: "큐 항목을 만든다. id 가 그대로 브랜치 이름이 된다.",
		InputSchema: obj(map[string]any{
			"id":     str("항목 id — [A-Za-z0-9._/-] 만. 브랜치·워크트리 이름으로 그대로 나간다"),
			"title":  str("한 줄 제목"),
			"body":   str("무엇을 해야 하는가. 비면 거절한다"),
			"paths":  strArr("이 항목이 건드릴 경로(겹침 판정의 축)"),
			"labels": strArr("표시 전용 꼬리표. 어떤 배제 판정에도 안 쓴다"),
			"after":  afterSchema(),
		}, "id", "title", "body"),
	},
	{
		Name:        "finish",
		Description: "항목을 끝낸다 — 판단 저장·후속 등록·종료·자원 반납이 한 호출, 한 트랜잭션이다.",
		InputSchema: obj(map[string]any{
			"item_id":      str("끝낼 항목 id"),
			"outcome":      enumStr("종료 상태", "done", "dropped"),
			"body":         str("판단 본문. 비면 무엇을 적어야 하는지를 응답으로 낸다"),
			"title":        str("한 줄 제목"),
			"close_reason": str("outcome=dropped 면 필수"),
			"followups":    followupSchema(),
		}, "item_id", "outcome", "body"),
	},
	{
		Name:        "alloc",
		Description: "논리 카운터의 다음 정수를 원자적으로 발급한다(개정 차수 등).",
		InputSchema: obj(map[string]any{
			"counter_name": str("카운터 이름"),
		}, "counter_name"),
	},
}

// Tools 는 tools/list 가 내는 목록의 사본이다.
func Tools() []Tool { return append([]Tool(nil), tools...) }

// ToolNames 는 도구 이름 목록이다(오류 메시지가 "무엇이 있나"에 답할 수 있게).
func ToolNames() []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

// KnownTool 은 이름이 이 서버의 도구인지 본다. 순수 함수다.
func KnownTool(name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// 인자 — 파생값이 하나도 없다
// ─────────────────────────────────────────────────────────────────────────────
//
// ★ session·branch·head·sha 인자가 어디에도 없다. 세션은 환경에서 오고(설계 §13)
// 나머지는 서버가 git 에서 읽는다. 인자로 두면 틀린 값이 들어오고, 그것이
// "메인 트리의 지금 HEAD"가 남의 랜딩 sha 로 박히던 결함이었다.

type boardArgs struct {
	Detail bool `json:"detail"`
}

type pickArgs struct {
	ItemID      string `json:"item_id"`
	StealReason string `json:"steal_reason"`
}

type noteArgs struct {
	Kind       string `json:"kind"`
	Body       string `json:"body"`
	Title      string `json:"title"`
	ItemID     string `json:"item_id"`
	Supersedes string `json:"supersedes"`
}

type afterArgs struct {
	Item string `json:"item"`
	SHA  string `json:"sha"`
	Job  string `json:"job"`
}

type addArgs struct {
	ID     string      `json:"id"`
	Title  string      `json:"title"`
	Body   string      `json:"body"`
	Paths  []string    `json:"paths"`
	Labels []string    `json:"labels"`
	After  []afterArgs `json:"after"`
}

type followupArgs struct {
	ID     string      `json:"id"`
	Title  string      `json:"title"`
	Body   string      `json:"body"`
	Paths  []string    `json:"paths"`
	Labels []string    `json:"labels"`
	After  []afterArgs `json:"after"`
}

type finishArgs struct {
	ItemID      string         `json:"item_id"`
	Outcome     string         `json:"outcome"`
	Body        string         `json:"body"`
	Title       string         `json:"title"`
	CloseReason string         `json:"close_reason"`
	Followups   []followupArgs `json:"followups"`
}

type allocArgs struct {
	CounterName string `json:"counter_name"`
}
