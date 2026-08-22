package mcpsrv

import (
	"encoding/json"
	"strings"
	"testing"
)

// 2026-08-22 실측: 도구 호출 계층이 인자 하나를 **옆 인자에 통째로 삼켜** 보낸 자국이
// 원장에 39건 있다. 원장 전수(모든 테이블·모든 텍스트 열)에서 자리는 정확히 둘이다:
//
//	item.close_reason  20건 — 삼킨 것은 title (finish)
//	judgment.body      19건 — 삼킨 것은 item_id 18 · followups 1 (note·finish)
//
// 모양은 하나다. 모델이 `<parameter name="close_reason">…</close_reason>` 로,
// 즉 `</parameter>` 가 아니라 **자기 이름으로** 닫는다. 파서는 다음 `</parameter>` 까지
// 훑으므로 그 사이의 `<parameter name="title">…` 블록이 값에 그대로 들어온다.
//
// 피해는 조용하다. 삼켜진 인자는 **아예 안 온 것**이 되므로:
//   - title 이 삼켜지면 판단 제목이 빈다(오염 20건 전부 그랬다. 대조군 243건 중 4건).
//   - item_id 가 삼켜지면 판단이 **어느 항목에도 안 걸린다**(19건 중 18건이 그랬다) —
//     followups 관문이 지키려는 바로 그 자산(판단↔항목 링크)이 같은 방식으로 죽는다.
//
// fd 는 이 오염을 **만들지 않는다**. 재현으로 갈랐다: CLI 플래그는 `--close-reason`
// (하이픈)이라 밑줄 태그가 원리적으로 안 나오고, decodeArgs 는 엄격 JSON 역직렬화라
// 정상 짝을 그대로 갈라 넣는다. 오염은 JSON 이 되기 **전**에 이미 끝나 있다.
// 그래서 서버가 할 수 있는 것은 저장 시점 검출·거절뿐이다.
func TestArgThatClosesItsOwnParameterTagIsRefused(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantOK    bool
		wantWords []string // 사유에 반드시 들어갈 낱말들
	}{
		{
			// 원장 20건의 모양 그대로다.
			name: "close_reason 이 자기 태그로 닫고 title 을 삼켰다",
			raw: `{"item_id":"x","outcome":"dropped","body":"b",` +
				`"close_reason":"사유다.</close_reason>\n<parameter name=\"title\">삼켜진 제목"}`,
			wantOK:    false,
			wantWords: []string{"close_reason", "title"},
		},
		{
			// 원장 18건의 모양 그대로다. 이쪽이 잃는 것은 판단↔항목 링크다.
			name:      "body 가 자기 태그로 닫고 item_id 를 삼켰다",
			raw:       `{"kind":"ask","body":"본문이다.</body>\n<parameter name=\"item_id\">e2e-sa-owners-roundtrip"}`,
			wantOK:    false,
			wantWords: []string{"body", "item_id"},
		},
		{
			name:   "정상 짝은 통과한다",
			raw:    `{"item_id":"x","outcome":"dropped","body":"b","close_reason":"사유","title":"제목"}`,
			wantOK: true,
		},
		{
			// ★ 이 관문이 벽이 되는 자리를 막는다. 이 저장소의 판단 본문은 이 결함 자체를
			//   서술한다 — 그 본문에는 `</close_reason>` 도 `<parameter name=` 도 들어간다.
			//   **자기 이름**으로 닫혔는지를 보는 이유가 이것이다(body 안의 close_reason 태그는
			//   삼킴이 아니라 인용이다). followups 관문이 최상위 키만 보는 것과 같은 규율이다.
			name: "이 결함을 서술하는 본문은 통과한다",
			raw: `{"kind":"decision","body":"오염 모양은 </close_reason> 뒤에 ` +
				`<parameter name=\"title\"> 가 이어 붙은 것이다"}`,
			wantOK: true,
		},
		{
			// `</body>` 는 산문에도 나온다(원장 판단 본문 77건·항목 본문 21건이 그렇다).
			// 뒤에 파라미터 블록이 이어지는 것까지 봐야 삼킴이다.
			name:   "자기 태그가 있어도 파라미터 블록이 안 이어지면 통과한다",
			raw:    `{"kind":"decision","body":"HTML 이 </body> 로 끝난다는 이야기다"}`,
			wantOK: true,
		},
		{
			// ★ encoding/json 은 필드명을 대소문자 무시로 맞춘다. 정확 일치만 보면
			//   이 갈래가 조용히 통과한다 — hasFollowupsKey 가 같은 이유로 EqualFold 를 쓴다.
			name: "대소문자가 다른 키도 거절한다",
			raw: `{"item_id":"x","outcome":"dropped","body":"b",` +
				`"Close_Reason":"사유.</close_reason>\n<parameter name=\"title\">제목"}`,
			wantOK:    false,
			wantWords: []string{"title"},
		},
		{
			name:   "인자 없는 호출도 통과한다",
			raw:    ``,
			wantOK: true,
		},
		{
			name:   "해석 안 되는 payload 는 통과시킨다 — 판정은 decodeArgs 가 낸다",
			raw:    `{"item_id":`,
			wantOK: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := judgeArgSwallowed(json.RawMessage(tc.raw))
			if ok != tc.wantOK {
				t.Fatalf("ok=%v 를 기대했는데 %v 다 (사유: %s)", tc.wantOK, ok, reason)
			}
			if ok && reason != "" {
				t.Fatalf("통과인데 사유가 붙었다: %s", reason)
			}
			for _, w := range tc.wantWords {
				if !strings.Contains(reason, w) {
					t.Fatalf("사유에 %q 가 없다 — 무엇이 무엇을 삼켰는지 못 읽는다:\n%s", w, reason)
				}
			}
		})
	}
}

// 인자 둘이 동시에 삼켰을 때 사유가 **매번 같아야** 한다.
//
// Go 의 map 순회는 무작위다. 정렬 없이 첫 적중을 내면 같은 호출이 부를 때마다 다른
// 인자를 지목하고, 그러면 두 번 거절당한 세션이 서로 다른 처방을 받는다 —
// "같은 실패가 같은 문장을 낸다"가 이 저장소의 응답 규율이다(설계 §12).
func TestSwallowRefusalNamesTheSameArgEveryTime(t *testing.T) {
	raw := json.RawMessage(`{` +
		`"body":"본문.</body>\n<parameter name=\"item_id\">i1",` +
		`"close_reason":"사유.</close_reason>\n<parameter name=\"title\">t1"}`)

	_, first := judgeArgSwallowed(raw)
	if first == "" {
		t.Fatal("삼킴 둘인데 통과했다")
	}
	for i := 0; i < 200; i++ {
		if _, got := judgeArgSwallowed(raw); got != first {
			t.Fatalf("사유가 호출마다 다르다 — map 순회 순서가 새고 있다\n첫째: %s\n%d회차: %s", first, i, got)
		}
	}
	// 정렬 순서상 body 가 close_reason 보다 앞이다. 무엇이 먼저인지가 아니라
	// **고정돼 있는지**가 이 시험의 주장이다.
	if !strings.Contains(first, "body") {
		t.Fatalf("정렬 첫째(body)를 안 지목했다: %s", first)
	}
}
