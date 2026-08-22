package mcpsrv

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// 인자 삼킴 관문 — 도구 호출 계층이 인자 하나를 **옆 인자에 통째로 삼켜** 보낸 것을 막는다.
//
// ## 무엇이 관측됐나 (2026-08-22 원장 전수)
//
// 모든 테이블·모든 텍스트 열을 훑어 자리는 정확히 둘, 39건이다:
//
//	item.close_reason  20건 — 삼킨 것은 title    (폐기 133건의 15%)
//	judgment.body      19건 — 삼킨 것은 item_id 18 · followups 1
//
// 값의 모양이 하나다: `…사유다.</close_reason>\n<parameter name="title">삼켜진 제목`.
// 모델이 `</parameter>` 가 아니라 **자기 인자 이름으로** 닫으면, 파서는 다음
// `</parameter>` 까지 훑으므로 그 사이의 파라미터 블록이 값 안으로 들어온다.
//
// ## 피해는 조용하다 — 삼켜진 인자는 "안 온 것"이 된다
//
//   - title 이 삼켜지면 판단 제목이 빈다. 오염 20건 **전부** 그랬다(대조군 243건 중 4건).
//   - item_id 가 삼켜지면 판단이 **어느 항목에도 안 걸린다.** 19건 중 18건이 그랬고,
//     잃은 id 는 삼켜진 텍스트 안에 그대로 남아 있다. followups 관문이 지키려는 그 자산
//     (판단↔항목 링크)이 옆 축에서 같은 방식으로 죽고 있었다.
//
// ## 왜 fd 가 고칠 수 있는 것이 검출뿐인가 (계층 셋을 재현으로 갈랐다)
//
//	⑴ CLI      탈락 — 플래그는 `--close-reason`(하이픈)이고 `--title` 이 따로다.
//	                  밑줄 태그 `</close_reason>` 는 원리적으로 안 나온다.
//	⑵ MCP 디코드 탈락 — decodeArgs 는 DisallowUnknownFields 를 쓴 엄격 역직렬화다.
//	                  정상 짝을 그대로 갈라 넣고, 삼킨 값도 그대로 통과시킨다. 만들지 못한다.
//	⑶ 도구 호출  확정 — 오염은 JSON 이 되기 **전**에 이미 끝나 있다. fd 밖이다.
//
// 그래서 이 관문은 원인을 고치지 않는다. 조용한 통과를 시끄러운 거절로 바꿀 뿐이다 —
// 그리고 그것으로 충분하다: 세션이 한 번 다시 부르면 값이 온전히 들어온다.
//
// ## 왜 이 경계인가
//
// 인자 **이름**을 알 수 있는 자리가 여기 하나다. 판정의 축이 "값이 자기 이름의 닫는
// 태그를 물고 있는가"라 이름 없이는 성립하지 않는다. 안쪽 홉(cmd/fd/wire.go → REST)은
// 필드로 이미 갈라져 있어 어느 이름으로 왔는지를 못 본다. judgeFollowupsArrived 가
// 같은 이유로 같은 자리에 있다.
//
// ★ **거절이 통과보다 싸다.** 정상 호출자는 한 번 다시 부르면 되고, 반대쪽 대가는
// 되돌릴 수 없다 — finish 는 한 트랜잭션이라 닫히고 나면 제목도 링크도 못 산다.

// judgeArgSwallowed 는 최상위 문자열 인자 중 **자기 이름으로 닫히고 그 뒤에 다른 파라미터
// 블록이 딸려 온 것**이 있는지 판정한다. 순수 함수다.
//
// 불리언이 아니라 사유를 낸다 — 무엇이 무엇을 삼켰는지를 말하지 않으면 받는 쪽은
// 자기 호출의 어디를 고쳐야 하는지 알 수 없다.
func judgeArgSwallowed(raw json.RawMessage) (ok bool, reason string) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return true, "" // 인자 없는 호출은 유효하다(pick·board 가 그렇다)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		// decodeArgs 가 아직 안 본 payload 다. 여기서 판정하지 않는다 —
		// 해석 못 하는 것을 거절로 접으면 오류 문구가 이 관문의 것으로 바뀐다.
		return true, ""
	}
	// ★ 키를 정렬해서 본다. map 순회는 무작위라 인자 둘이 동시에 삼켰을 때
	//   사유가 호출마다 달라진다 — 그러면 같은 실패가 같은 문장을 못 낸다.
	keys := make([]string, 0, len(top))
	for k := range top {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		var v string
		if err := json.Unmarshal(top[k], &v); err != nil {
			continue // 문자열 인자만 본다. 배열·객체는 이 모양이 나오면 JSON 자체가 깨진다
		}
		victim, found := swallowedBy(k, v)
		if !found {
			continue
		}
		return false, fmt.Sprintf(
			"인자 %s 의 값이 자기 이름으로 닫혀 있고(`</%s>`), 그 뒤에 인자 %s 가 통째로 딸려 왔다 — "+
				"도구 호출이 `</parameter>` 대신 `</%s>` 로 닫힌 자국이다.\n"+
				"  **아무것도 쓰지 않았다.** 그대로 받으면 %s 는 **안 온 것**이 되고 %s 에는 마크업이 남는다. "+
				"원장에 그렇게 들어간 39건이 있고, 그 중 18건은 판단이 어느 항목에도 안 걸렸다.\n"+
				"  → 인자는 이름이 아니라 `</parameter>` 로 닫고 그대로 다시 불러라. 잃은 것은 아직 없다.\n"+
				"  → 이 문자열이 정말 본문의 일부라면(이 결함을 서술하는 판단이 그렇다) "+
				"`</%s>` 와 `<parameter` 사이에 공백 아닌 글자를 하나 넣어라 — 관문은 붙어 있는 것만 본다.",
			k, k, victim, k, victim, k, k)
	}
	return true, ""
}

// swallowedBy 는 값 v 안에서 인자 name 이 자기 태그로 닫히고 파라미터 블록이 이어지는지 본다.
// 이어지면 딸려 온 인자의 이름을 낸다. 순수 함수다.
//
// ★ **자기 이름으로 닫혔는지를 본다.** 값 안에 아무 `<parameter name=` 이나 있으면 거절하는
// 쪽이 잡기는 더 넓지만, 이 저장소의 판단 본문은 이 결함 자체를 서술하고 그 본문에는
// 그 문자열이 들어간다 — 넓히면 관문이 아니라 벽이 된다(hasFollowupsKey 가 최상위 키만
// 보는 것과 같은 규율이다).
//
// ★★ **닫는 태그만으로도 부족하다.** `</body>` 는 산문에 흔하다(원장 실측: 판단 본문 77건 ·
// 항목 본문 21건). 뒤에 파라미터 블록이 **붙어서** 이어지는 것까지 봐야 삼킴이다.
// 조임 술어로 재면 원장에서 39건이 잡히고, 그 39건은 느슨 술어가 잡는 것과 정확히 같다.
func swallowedBy(name, v string) (victim string, found bool) {
	close := "</" + name + ">"
	i := indexFoldASCII(v, close)
	if i < 0 {
		return "", false
	}
	rest := strings.TrimLeft(v[i+len(close):], " \t\r\n")
	const open = `<parameter name="`
	if !strings.HasPrefix(rest, open) {
		return "", false
	}
	rest = rest[len(open):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		// 이름을 못 읽어도 삼킴은 삼킴이다. 사유가 "무엇을"만 못 말한다.
		return "(이름을 못 읽었다)", true
	}
	return rest[:end], true
}

// indexFoldASCII 는 ASCII 대소문자를 무시하고 sub 의 첫 자리를 찾는다.
//
// ★ strings.ToLower 로 접어 놓고 찾으면 안 된다 — 유니코드 접기는 **길이를 바꾸는** 룬이
// 있어(U+0130 등) 돌려준 자리가 원본과 어긋난다. 이 값은 사람이 쓴 한글 산문이다.
//
// 대소문자를 무시하는 이유: encoding/json 이 필드명을 대소문자 무시로 맞추므로
// `{"Close_Reason": …}` 도 decodeArgs 를 통과해 CloseReason 을 채운다. 여기서 정확히
// 일치만 보면 그 갈래가 조용히 통과한다(hasFollowupsKey 가 EqualFold 를 쓰는 그 이유다).
func indexFoldASCII(s, sub string) int {
	if len(sub) == 0 || len(sub) > len(s) {
		return -1
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			if foldASCII(s[i+j]) != foldASCII(sub[j]) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func foldASCII(b byte) byte {
	if 'A' <= b && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}
