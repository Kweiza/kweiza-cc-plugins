package mcpsrv

import "strings"

// cc_session_id 표류 — 알아채기(따라가기가 **아니다**).
//
// ★ 사실관계는 실측이다(2026-08-04, drift_test.go 머리에 방법과 값이 있다):
// claude 는 세션 id 를 스스로 만들고 MCP 서버에 **기동 시 1회 주입**한다. 그 뒤
// /clear·compact·재개로 대화의 cc 가 갈려도 **이미 뜬 MCP 프로세스의 environ 은 안 바뀐다.**
// 훅은 매번 새 프로세스라 새 값을 보고, MCP 는 옛 값을 계속 쓴다 —
// 그래서 같은 좌표에 카드가 두 장 뜬다.
//
// ★ **따라갈 수단이 이 프로세스 안에 없다.** environ 을 다시 읽어도 같은 값이고,
// 새 값을 가진 것은 훅뿐이다. 그러니 여기서 할 수 있는 정직한 일은 하나다:
// 갈렸다는 사실을 **보드에서 이름으로 말하는 것.** 이 계층의 규율이 그렇다
// (mcpsrv.go 머리 ②: "못 읽으면 조용히 익명으로 진행하지 않는다").
//
// 새 통로(훅이 현재 cc 를 상태 디렉토리에 적고 MCP 가 그것을 읽는 길)는 여기 없다.
// 그것은 동시에 열린 창 여럿을 어떤 키로 가를지가 먼저 정해져야 하는 별개 문제라
// 이 항목에서 끊었다 — 근거는 판단에 있다.

// LiveIdentity 는 살아 있는 세션 하나의 좌표다. 표류 판정에 필요한 것만 담는다.
type LiveIdentity struct {
	SessionID   string
	MachineID   string
	Worktree    string
	CCSessionID string
}

// CoordinateTwin 은 나와 **같은 (machine, worktree)** 인데 cc 가 갈린 세션이다.
type CoordinateTwin struct {
	SessionID   string
	CCSessionID string
}

// DriftedTwins 는 표류한 쌍둥이를 찾는다. 순수 함수다.
//
// mine 이 Identity 가 아니라 LiveIdentity 인 것이 요점이다 — 기준점은 **이 프로세스가
// 관측한 정체**가 아니라 **실제로 연 카드**여야 한다. 그 둘은 이 기능이 도는 동안 정상적으로
// 갈린다(ensureSession 이 비콘의 cc 로 여는 것이 이 기능의 본체다).
//
// ★ 축을 (machine, worktree) 로 **고정**하고 cc 만 본다. 워크트리가 다르면 카드가 둘인 것이
// 옳으므로(워크트리로 일하는 것이 이 제품의 정상 흐름이다) 그것까지 표류로 부르면
// 경고가 상시가 되고, 상시 경고는 읽히지 않는다.
//
// ★★ **자기 카드는 session id 로도 뺀다.** cc 축만으로 빼면 자기 자신이 쌍둥이로 잡힌다:
// mine.CCSessionID 는 카드를 연 시점의 값인데, 그 뒤 /clear 가 한 번 더 오면 훅이 같은
// 카드의 cc 를 또 바꾼다. 그러면 같은 id 의 카드가 "다른 cc" 로 보인다. id 는 rekey 를
// 건너 보존되므로(설계 제약 ⑥) 그 축만이 안정적이다.
//
// ★ 어느 한쪽 cc 가 비면 세지 않는다. 빈 값을 "다르다"로 접으면 정체가 반쪽인 세션 하나가
// 살아 있는 세션 전부를 표류로 고발한다.
func DriftedTwins(mine LiveIdentity, live []LiveIdentity) []CoordinateTwin {
	if mine.CCSessionID == "" || mine.MachineID == "" || mine.Worktree == "" {
		return nil
	}
	var out []CoordinateTwin
	for _, l := range live {
		if mine.SessionID != "" && l.SessionID == mine.SessionID {
			continue
		}
		if l.CCSessionID == "" || l.CCSessionID == mine.CCSessionID {
			continue
		}
		if l.MachineID != mine.MachineID || l.Worktree != mine.Worktree {
			continue
		}
		out = append(out, CoordinateTwin{SessionID: l.SessionID, CCSessionID: l.CCSessionID})
	}
	return out
}

// RenderDrift 는 표류 하나를 사람이 읽는 문구로 만든다. 순수 함수다.
//
// 표류가 없으면 **빈 문자열**이다 — 매 board 마다 빈 절이 붙으면 예산이 토큰인 화면이 상한다.
//
// ★ why 는 **수리가 왜 안 됐나**다. 이제 표류는 훅이 비콘으로 고친다 — 훅이 조상 사슬을
// 밟아 이 MCP 프로세스의 비콘을 찾고 카드를 rekey 한다. 그래도 여기 문구가 뜬다면 그 수리가
// 어딘가에서 멈춘 것이고, 그 자리를 이름으로 말하지 않으면 사람이 원인에 도달할 길이 없다 —
// "재기동해라"는 더 이상 맞는 조언이 아니다(재기동 없이도 다음 SessionStart 에 고쳐진다).
//
// ★★ **맺음말이 합쳐진다고 단정하지 않는다.** 자기 카드를 id 로 뺀 뒤(DriftedTwins)
// 여기 남는 것은 둘 중 하나다: 같은 워크트리에 열린 **다른 창**(claude 부모가 다른 별개
// 대화 — 이 머신에 다섯이 있다, 설계 개정 ③)이거나, 진짜로 멈춘 수리다. 전자에게는
// 수리가 영영 안 오고 **안 오는 것이 옳다.** 단정형은 그 경우에 오지 않을 약속을 하고,
// 그 약속을 믿고 기다리면 "왜 아직 두 장이냐"에 아무도 답을 못 한다.
// 그래서 두 갈래를 그대로 말하고, 어느 쪽인지는 사람이 가른다 — 이 함수는 못 가른다.
func RenderDrift(twins []CoordinateTwin, mineCC, why string) string {
	if len(twins) == 0 {
		return ""
	}
	s := "⚠ 이 워크트리에 cc_session_id 가 갈린 세션이 " +
		itoa(len(twins)) + "건 더 있다 — 카드가 여러 장인 이유가 이것이다.\n" +
		"  이 MCP 프로세스가 카드를 연 값: " + clip(mineCC, 64) + "\n"
	for _, t := range twins {
		s += "  갈린 카드: " + clip(t.SessionID, 64) + " · cc=" + clip(t.CCSessionID, 64) + "\n"
	}
	s += "  같은 창의 카드라면 훅이 다음 SessionStart 에 합친다. " +
		"같은 워크트리에 열린 **다른 창**이라면 안 합쳐진다 — 그쪽이 맞다."
	if strings.TrimSpace(why) != "" {
		s += " 이 프로세스가 아는 사유: " + clip(why, 200)
	}
	return s
}

// itoa 는 작은 수 하나를 적는다. strconv 를 위해 import 를 늘리지 않는다.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
