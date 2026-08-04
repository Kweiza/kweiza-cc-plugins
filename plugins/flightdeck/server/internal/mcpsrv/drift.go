package mcpsrv

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
// ★ 축을 (machine, worktree) 로 **고정**하고 cc 만 본다. 워크트리가 다르면 카드가 둘인 것이
// 옳으므로(워크트리로 일하는 것이 이 제품의 정상 흐름이다) 그것까지 표류로 부르면
// 경고가 상시가 되고, 상시 경고는 읽히지 않는다.
//
// ★ 어느 한쪽 cc 가 비면 세지 않는다. 빈 값을 "다르다"로 접으면 정체가 반쪽인 세션 하나가
// 살아 있는 세션 전부를 표류로 고발한다.
func DriftedTwins(mine Identity, live []LiveIdentity) []CoordinateTwin {
	if mine.CCSessionID == "" || mine.MachineID == "" || mine.Worktree == "" {
		return nil
	}
	var out []CoordinateTwin
	for _, l := range live {
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
func RenderDrift(twins []CoordinateTwin, mineCC string) string {
	if len(twins) == 0 {
		return ""
	}
	s := "⚠ 이 워크트리에 cc_session_id 가 갈린 세션이 " +
		itoa(len(twins)) + "건 더 있다 — 카드가 여러 장인 이유가 이것이다.\n" +
		"  이 MCP 프로세스가 든 값: " + clip(mineCC, 64) + " (기동 시 주입된 뒤 안 바뀐다)\n"
	for _, t := range twins {
		s += "  갈린 카드: " + clip(t.SessionID, 64) + " · cc=" + clip(t.CCSessionID, 64) + "\n"
	}
	s += "  /clear·compact·재개로 대화의 cc 가 갈렸는데 이 프로세스는 옛 값을 계속 쓴다. " +
		"프로세스의 환경은 기동 뒤 안 바뀌므로 이 자리에서 따라갈 방법이 없다 — " +
		"합치려면 Claude Code 를 재기동해라(MCP 서버가 새 값으로 다시 뜬다)."
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
