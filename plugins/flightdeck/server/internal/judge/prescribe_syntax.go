package judge

import "github.com/kweiza/flightdeck/internal/model"

// 처방문이 쓰는 **도구 호출 표기**. 하네스마다 다르다.
//
// ★ 왜 이 표가 필요한가. 처방은 매 턴 주입되므로, 올바른 문법을 말하면 MCP 도구 설명이
// 상주하는 것과 비슷한 효과를 낸다 — 즉 이것이 「도구 발견성」의 주된 메움수단이다.
// 반대로 없는 문법을 말하면 그 창은 도구를 영영 못 찾는다: codex 는 오늘 훅 전용이라
// MCP 표면이 **0**이고, 거기에 `land()` 라고 적으면 "그런 함수가 없다"조차 안 나온다.
// 아무 일도 안 일어난다.
//
// ★ **표로 두는 이유**: 문구는 일곱 자리인데 호출 표기는 여덟 개뿐이다. 문구마다 갈래를
// 두면 하나를 고칠 때 나머지가 조용히 낡고, 그 낡음은 그 하네스의 창에서만 보인다 —
// 즉 고친 사람 화면에서는 영원히 안 보인다.
type callSyntax struct {
	Land        string // 레인을 쥔다
	LandOK      string // 다 쓰고 반납한다
	LandLeave   string // 줄에서 빠진다(사유 포함)
	NoteAsk     string // 의도를 남긴다
	NoteDecide  string // 범위가 왜 늘었는지 남긴다
	NoteDecideW string // 무엇을 정했고 무엇을 기각했나
	Pick        string // 항목을 집는다
	AddWithPath string // 항목을 세운다 — %s 자리에 경로가 온다
	Add         string // 항목을 세운다(경로 자리 없음)
}

// syntaxFor 는 그 하네스가 실제로 부를 수 있는 문법이다. 순수 함수다.
//
// ★ **빈 값(「미상」)은 MCP 문법이다.** 우연히 정해지면 안 되는 갈래라 근거를 적는다:
// 미선언으로 오는 것은 오늘 사실상 Claude 뿐이다 — codex 훅 다섯은 전부 `--harness codex`
// 를 싣기 때문이다(codex-hooks.json). 그리고 손해가 비대칭이다: MCP 문법을 받은 쪽이
// 실은 codex 였으면 도구를 못 찾지만, CLI 문법을 받은 쪽이 Claude 였으면 있는 MCP 도구를
// 놔두고 Bash 를 쓴다 — 후자가 이 함대에서 훨씬 잦다.
func syntaxFor(harness string) callSyntax {
	if harness == model.HarnessCodex {
		return callSyntax{
			Land:        "fd land",
			LandOK:      "fd land --ok",
			LandLeave:   "fd land --leave '사유'",
			NoteAsk:     "fd note --kind ask --body '무엇을 왜 잡는가'",
			NoteDecide:  "fd note --kind decision",
			NoteDecideW: "fd note --kind decision --body '무엇을 정했고 무엇을 기각했나'",
			Pick:        "fd pick <item-id>",
			AddWithPath: "fd add --id … --title … --body … --path '%s'",
			Add:         "fd add --id … --title … --body … --path …",
		}
	}
	return callSyntax{
		Land:        "land()",
		LandOK:      "land(result='ok')",
		LandLeave:   "land(leave='사유')",
		NoteAsk:     "note(kind='ask', body='무엇을 왜 잡는가')",
		NoteDecide:  "note(kind='decision')",
		NoteDecideW: "note(kind='decision', body='무엇을 정했고 무엇을 기각했나')",
		Pick:        "pick(item_id=…)",
		AddWithPath: "add(id=…, title=…, body=…, paths=['%s'])",
		Add:         "add(id=…, title=…, body=…, paths=[…])",
	}
}
