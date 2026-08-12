package mcpsrv

// 도구 8개 — 설계 §6 표에 랜딩 순서 큐 설계(2026-08-05-landing-order-queue-design.md)가
// land 를, 항목 꼬리표 표면(2026-08-12-item-label-surface-design.md)이 label 을 더했다.
// 더는 늘리지 않는다.
//
// ★ 설명 문구를 짧게 유지하는 것이 이 파일의 규율이다. 세션 시작에 실리는 것은
// 도구 이름과 설명, 그리고 서버 instructions 뿐이고 그 예산이 도구 수를 눌러 잡는
// 이유다(설계 §6). **규율 산문은 여기 없다** — 응답 꼬리에 있다.

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
		"description": "이번에 나온 후속. 같은 호출에 넣으면 판단에 이어진다",
		"items": obj(map[string]any{
			"id":     str("항목 id — 브랜치 이름이 된다. **이미 있는 id 면 만들지 않고 잇는다**(이 세션이 이 선점 뒤 만든 열린 항목만 — 없는 id 면 새로 만드니 제목·본문이 필요하다)"),
			"title":  str("한 줄 제목. 새로 만들 때만 쓴다 — 이미 있는 id 면 안 읽는다"),
			"body":   str("무엇을 해야 하는가. 새로 만들 때만 쓴다 — 이미 있는 id 면 안 읽는다"),
			"paths":  strArr("이 항목이 건드릴 경로"),
			"labels": strArr("표시 전용 꼬리표. 'tickler' 만 굶김 축에서 빠진다"),
			"after":  afterSchema(),
		}, "id"),
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
		Name: "pick",
		Description: "인자 없으면 함께 갈 항목까지 묶어 추천하고 탈락 사유 전부. " +
			"item_id 를 주면 선점하고 맥락을 낸다. item_ids 는 묶음, leave 는 반납이다.",
		InputSchema: obj(map[string]any{
			"item_id":  str("집을 항목 id. 없으면 추천만 하고 선점하지 않는다"),
			"item_ids": strArr("함께 집을 항목 id 들. **첫째가 선두**이고 그 id 가 브랜치가 된다. item_id 와 동시에 못 준다"),
			"steal_reason": str("남의 선점을 회수하는 사유. **이 서버는 회수하지 않는다** — " +
				"주면 사유와 함께 거절한다"),
			// ★ 이름이 land 의 leave 와 같다. 같은 뜻이기 때문이다 — **자기가 빠진다**.
			//   회수(land 의 release · pick 의 steal_reason)는 둘 다 거절되는 3자 축이고,
			//   반납은 둘 다 leave 다. 이름을 갈라 두면 같은 판정이 두 이름으로 읽힌다.
			"leave": str("내 선점을 놓는 사유(필수). 항목은 open 으로 돌아가고 id·이력이 산다. " +
				"item_id 를 함께 주면 그 하나만, 안 주면 내가 쥔 전부를 놓는다"),
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
			"labels": strArr("표시 전용 꼬리표. 배제 판정에 안 쓴다 — 'tickler' 하나만 굶김 축(집계·기아 가중)에서 빠진다"),
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
	{
		Name:        "land",
		Description: "랜딩 줄에 서거나 내 차례를 본다. result 로 보고+반납, leave 로 이탈한다.",
		InputSchema: obj(map[string]any{
			"result": enumStr("보고 종류. 채우면 레인을 반납한다", "ok", "fail"),
			"detail": str("보고 사유. result=fail 이면 필수"),
			"leave":  str("채우면 이 값을 사유로 줄에서 빠진다"),
			"release": str("레인을 회수하는 사유. **이 서버는 회수하지 않는다** — " +
				"주면 사유와 함께 거절한다"),
			"resources": strArr("줄을 설 자원들. 비면 landing. 경로 자원은 path:<경로>"),
		}),
	},
	{
		Name:        "label",
		Description: "항목의 꼬리표를 더하거나 뺀다. 'tickler' 만 굶김 축에서 빠진다.",
		InputSchema: obj(map[string]any{
			"item_id": str("꼬리표를 고칠 항목 id"),
			"add":     strArr("더할 꼬리표. 'tickler' 는 기한까지 늙는 항목을 굶김 축에서 뺀다"),
			"rm":      strArr("뺄 꼬리표"),
		}, "item_id"),
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
	ItemID      string   `json:"item_id"`
	ItemIDs     []string `json:"item_ids"`
	StealReason string   `json:"steal_reason"`
	Leave       string   `json:"leave"`
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

// landArgs 는 land 세 동작(줄 서기·보고·이탈)과 거절 한 동작(회수)을 한 인자로 받는다.
//
// ★ 동작을 고르는 것은 도구 이름이 아니라 **채운 필드**다: 전부 비면 줄을 서거나 내 자리를
// 다시 묻고, Result 를 채우면 보고+반납, Leave 를 채우면 이탈이다. Release 는 채워도 되는 동작이
// 아니라 **거절 사유를 만드는 미끼**다 — pick 의 steal_reason 과 같은 자리(mcpsrv.go 참조).
//
// ★ Resources 는 **줄 서기에만** 성립한다(Task 3 의 all-or-nothing 자원 집합). 비면 서비스가
// 기존 단일 레인("landing")으로 정규화한다(service.normalizeResources) — 자원 축을 아직 안
// 보내는 옛 호출자와 이 개편 이전이 같은 동작을 그대로 받는다. Result·Leave 와 함께 오면
// mcpsrv.go 의 toolLand 가 거절한다 — 보고·이탈은 줄 행 전체에 걸리는 동작이라 자원 인자가
// 무의미하다(조용히 버리면 "r2 만 반납했다" 같은 거짓 믿음이 생긴다).
type landArgs struct {
	Result    string   `json:"result"`
	Detail    string   `json:"detail"`
	Leave     string   `json:"leave"`
	Release   string   `json:"release"`
	Resources []string `json:"resources"`
}

type labelArgs struct {
	ItemID string   `json:"item_id"`
	Add    []string `json:"add"`
	Rm     []string `json:"rm"`
}
