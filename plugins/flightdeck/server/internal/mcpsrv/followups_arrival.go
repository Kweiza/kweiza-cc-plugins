package mcpsrv

import (
	"encoding/json"
	"strings"
)

// 후속 도착 관문 — `finish` 가 followups 를 **조용히** 떨어뜨리는 것을 막는다.
//
// ## 왜 이 관문이 있나 (2026-08-11 사고)
//
// 한 세션이 `finish` 를 followups 둘과 함께 불렀는데 응답이 **오류 없이 `후속 0건`** 을 냈고
// 항목은 닫혔다(원장 `item.finish`: `count:0, linked:0, tx:committed`). `finish` 는 한
// 트랜잭션이라 다시 못 부르므로, 판단↔후속 링크가 그 자리에서 **영영** 죽었다. 그 링크가
// `followups` 가 존재하는 유일한 이유다 — 나중에 `add` 로 넣으면 다음 세션의 `pick` 이
// "이 일이 왜 나왔나"를 그 항목과 함께 못 낸다.
//
// ## 실측으로 좁힌 원인 (가설 하나는 반증됐다)
//
// 사고 항목은 "followups 가 **문자열**로 도착했는데 거절이 아니라 무시로 접혔다"를 첫 가설로
// 세웠다. **거짓이다.** `decodeArgs` 는 `DisallowUnknownFields` 를 쓰고 타입 불일치에 오류를
// 낸다. 아홉 모양을 실측했더니 **조용히 0건으로 통과하는 것은 셋뿐**이었다:
//
//	"followups": []      ← 키는 왔는데 비었다
//	"followups": null    ← 키는 왔는데 비었다
//	키 자체가 없음         ← 원리적으로 못 가른다
//
// 나머지 전부(문자열 · 이름 오타 · 배열 아님 · 원소가 문자열 · 원소에 없는 필드)는 시끄럽게
// 실패한다. 즉 **역직렬화는 빈틈이 없었고, 잃은 값은 서버에 애초에 안 실려 왔다.**
//
// ## 그래서 이 관문이 막는 것과 못 막는 것
//
// 앞의 둘은 막는다 — 키를 실어 놓고 내용이 0건인 것은 뜻이 없거나 **전송 계층이 값을 흘린
// 자국**이다. 셋째(키 없음)는 서버가 "안 보냈다"와 "보냈는데 유실됐다"를 **원리적으로** 못
// 가른다. 그 갈래는 관문이 아니라 **문구**가 진다(render.go 의 0건 줄이 되돌릴 수 없다는
// 사실과 복구 경로를 말한다).
//
// ## 왜 바깥 경계인가
//
// 키가 왔는지를 신뢰성 있게 볼 수 있는 자리가 여기 하나다. 안쪽 홉인 `cmd/fd/wire.go` 의
// `finishReq.Followups` 가 `json:"followups,omitempty"` 라 **빈 목록을 키째 지운다** —
// REST 핸들러는 "안 보냈다"와 "비워 보냈다"를 이미 구분할 수 없다.
//
// ★ **거절이 닫기보다 싸다.** 정상 호출자가 치르는 대가는 키를 빼고 한 번 다시 부르는
// 것뿐이고, 반대쪽 대가는 되돌릴 수 없는 링크 소실이다. `service.Rekey` 가 빈 cc 를 store
// 보다 앞에서 미리 접는 것과 같은 자리다.

// judgeFollowupsArrived 는 followups 인자가 **왔는데 하나도 안 남았는지** 판정한다. 순수 함수다.
//
// 불리언이 아니라 사유를 함께 낸다 — "안 됐다"만 알면 무엇을 어떻게 고쳐야 하는지 알 수 없고,
// 이 거절을 받는 쪽은 **되돌릴 수 없는 자리 앞에 서 있다.**
func judgeFollowupsArrived(raw json.RawMessage, decoded int) (ok bool, reason string) {
	if decoded > 0 {
		return true, ""
	}
	if !hasFollowupsKey(raw) {
		// 키가 없다 = 후속이 정말 없는 마무리다. 흔하고 정상이다 —
		// 여기서 막으면 관문이 아니라 벽이 된다(judgeMissingFollowups 의 규율과 같다).
		return true, ""
	}
	return false, "followups 를 실었는데 서버에 0건으로 도착했다 — 빈 배열이거나 null 이다.\n" +
		"  **항목을 닫지 않았다.** finish 는 한 트랜잭션이라 다시 못 부르고, " +
		"그대로 닫혔다면 판단↔후속 링크를 영영 못 산다.\n" +
		"  → 후속이 있으면 followups 에 내용을 담아 다시 불러라.\n" +
		"  → 정말 없으면 followups 키를 **빼고** 불러라.\n" +
		"  → 이미 만들어 둔 항목을 잇는 것이면 id 만 담아라. 그것도 안 되면 " +
		"add 로 만든 뒤 note(kind='handoff', item_id=…) 로 판단을 그 항목에 직접 걸어라."
}

// hasFollowupsKey 는 원본 인자에 followups 키가 있었는지 본다. 순수 함수다.
//
// ★ 최상위 키만 본다. 문자열 검색(`strings.Contains`)으로 하면 본문에 그 낱말이 들어간
// 마무리가 전부 걸린다 — 이 저장소의 판단 본문은 이 관문 자체를 서술하기도 한다.
//
// ★★ **대소문자를 무시하고 맞춘다.** `encoding/json` 이 필드명을 대소문자 무시로 맞추므로
// `{"Followups": []}` 는 `DisallowUnknownFields` 에도 안 걸리고 `a.Followups` 를 0건으로
// 채운다. 여기서 정확히 일치만 보면 그 호출은 "키 없음"으로 접혀 **조용히 통과한다** —
// 이 관문이 존재하는 바로 그 사고 모양이다. 실측으로 확인한 갈래다(`Followups` · `FOLLOWUPS` ·
// `followUps` 셋 다 decodeArgs 를 통과한다).
func hasFollowupsKey(raw json.RawMessage) bool {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return false
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		// 여기까지 왔으면 decodeArgs 가 이미 통과시킨 payload 다. 해석이 안 되면
		// **없다고 본다** — 있다고 접으면 정상 호출을 거절하게 된다.
		return false
	}
	for k := range top {
		if strings.EqualFold(k, "followups") {
			return true
		}
	}
	return false
}
