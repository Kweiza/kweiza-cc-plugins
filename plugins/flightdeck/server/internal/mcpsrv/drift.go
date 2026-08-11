package mcpsrv

import "strings"

// cc_session_id 표류 — **수리는 훅이 한다. 여기는 그 수리가 안 닿은 자리를 말한다.**
//
// ★ 밑에 깔린 사실은 실측이고 지금도 참이다(2026-08-04, drift_test.go 머리에 방법과 값이 있다):
// claude 는 세션 id 를 스스로 만들고 MCP 서버에 **기동 시 1회 주입**한다. 그 뒤
// /clear·compact·재개로 대화의 cc 가 갈려도 **이미 뜬 프로세스의 environ 은 안 바뀐다.**
// 훅은 매번 새 프로세스라 새 값을 보고, MCP 는 옛 값을 영원히 든다 —
// 그래서 같은 좌표에 카드가 두 장 뜬다.
//
// **이 문단을 지우지 마라.** 이것이 없으면 다음 사람이 "도구 호출마다 environ 을 다시 읽어
// 따라가면 되지 않나"로 단순화한다 — 앞선 판단이 그 처방을 실측으로 한 번 죽였다.
// 구현하면 초록인 채로 **아무 일도 안 하는 코드**가 되고, 그 침묵이 이 항목을 두 번 죽였다.
// 비콘이라는 우회로가 존재하는 이유가 통째로 이 제약이다.
//
// ★★ **통로는 이제 있다 — `internal/window` 다.** 이 프로세스가 새 cc 를 알아낼 수단은
// 여전히 없지만, 훅과 MCP 가 **같은 claude 프로세스의 자손**이라는 공유 안정키가 있다
// (설계 조사 ④: claude pid 는 cc 가 갈리는 전환을 넘어 살아남는다). 그 pid 로 키를 잡은
// 비콘 파일이 두 프로세스를 잇는다:
//
//	MCP  — 기동 때 비콘을 **심는다**(병합이다: 정체 두 필드는 훅의 자리라 안 덮는다).
//	       첫 ensureSession 에서 비콘의 **cc 만** 우선해 카드를 연다.
//	훅   — 자기 조상 사슬을 걸어 그 비콘을 **찾고**, cc 가 갈렸으면 카드를 **rekey 한 뒤에**
//	       OpenSession 한다. 그 순서가 이 설계의 핵심이다(뒤바꾸면 3중키 UNIQUE 에 걸린다).
//
// 즉 표류는 대개 **다음 SessionStart 에 조용히 고쳐진다.** 재기동은 필요 없다.
//
// ★★ **그래서 이 파일의 목적이 바뀌었다.** DriftedTwins·RenderDrift 는 더 이상 "이 계층이
// 할 수 있는 유일한 정직한 일"이 아니다. 수리가 있는 지금, 이 둘이 답하는 것은 둘뿐이다:
//
//  1. **수리가 안 닿은 자리** — 비콘이 없거나(Cursor 처럼 claude 가 아닌 부모, 비리눅스,
//     MCP 미기동) 계보 대조가 실패했거나 rekey 가 거절당한 경우. 그 사유를 이름으로 말하지
//     않으면 사람이 원인에 도달할 길이 없다(beaconMiss → RenderDrift 의 why).
//  2. **영영 안 합쳐질 카드** — 같은 워크트리에 열린 **다른 창**. 이 머신에 그런 창이 다섯이고
//     (설계 개정 ③) 그것들은 합치면 **안 된다.** 카드가 여러 장인 이유를 사람에게 말해 주는
//     자리가 이것 하나뿐이라 지운 것이 아니라, 단정형을 뺀 채로 남겨 뒀다.
//
// 그래서 문구는 두 갈래를 모두 참으로 말해야 하고(RenderDrift 주석), 기준점은 이 프로세스가
// 관측한 정체가 아니라 **실제로 연 카드**여야 한다(DriftedTwins 주석) — 수리가 성공하면
// 그 둘은 정상적으로 갈리기 때문이다.
//
// 이 계층의 규율은 그대로다(mcpsrv.go 머리 ②: "못 읽으면 조용히 익명으로 진행하지 않는다").
// 바뀐 것은 할 말이 "고칠 수 없다"에서 "여기까지는 고쳐졌고 이것이 안 됐다"가 된 것이다.

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

// driftTwinLimit 은 배너가 **이름까지 적는** 갈린 카드 수다. 수 자체는 첫 줄이 전부 센다.
//
// ★ 왜 상한이 필요한가. 이 배너는 board 의 **고정분**에 들어간다 — 예산이 자르는 것은
// 세션 카드뿐이므로(RenderBoard), 여기 한 줄이 늘면 카드가 한 장 밀려난다. 그런데 갈린
// 카드 수는 대화가 /clear·compact·재개를 겪을 때마다 자라고 상한이 없었다.
// 실측(2026-08-05): 쌍둥이 10건일 때 이 배너 혼자 533토큰 = 예산 1200 의 44%.
//
// 카드를 밀어내면서까지 id 를 전부 적을 값어치는 없다. 사람이 이 배너로 하는 일은
// "내 카드가 갈렸구나"를 아는 것이고 그것은 첫 줄로 끝난다 — 전부가 필요하면 detail 이 낸다.
const driftTwinLimit = 3

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
func RenderDrift(twins []CoordinateTwin, mineID, mineCC, why string) string {
	if len(twins) == 0 {
		return ""
	}
	// ★ **카드 id 를 먼저 낸다.** cc 는 rekey 를 못 견딘다 — 아래 mineCC 는 이 프로세스가
	// 카드를 열 때 쓴 값이고(openedCC, 무효화 경로가 없다) 그 뒤 훅이 SessionStart 에서
	// 카드를 그 cc 에서 떼어 갈 수 있다(Rekey 는 cc 컬럼만 UPDATE 하고 id 는 보존한다).
	// 그러면 인쇄된 cc 는 **어떤 카드도 갖지 않은 값**이 되고, 그 값으로 할 수 있는 일이
	// 하나도 없다 — 실물로 그 일이 났다: `fd close -cc-session <그 값>` 이 3중키 upsert 라
	// 카드를 하나 만들어서 그것을 닫았고 실제 카드는 그대로 열려 있었다.
	// DriftedTwins 주석이 이미 "id 는 rekey 를 건너 보존되므로 그 축만이 안정적이다"를
	// 판정 근거로 쓴다. 화면도 같은 축을 내야 사람이 그 값으로 무언가 할 수 있다.
	s := "⚠ 이 워크트리에 cc_session_id 가 갈린 세션이 " +
		itoa(len(twins)) + "건 더 있다 — 카드가 여러 장인 이유가 이것이다.\n" +
		"  이 MCP 프로세스의 카드: " + clip(mineID, 64) +
		" (열 때 쓴 cc=" + clip(mineCC, 64) + " — 이 값은 rekey 로 옮겨갔을 수 있다)\n"
	for i, t := range twins {
		if i >= driftTwinLimit {
			s += "  갈린 카드 " + itoa(len(twins)-driftTwinLimit) +
				"건 더 — 수는 위 첫 줄이 전부 센 값이다\n"
			break
		}
		// ★ **id 옆에 그 id 로 할 일을 붙인다.** 안정 축을 인쇄하면서 그 축으로 일할 수단을
		// 안 주는 것이 이 저장소가 명문화한 결함이다(judge/prescribe.go 2026-08-06 개정 주석).
		// 자기 카드(위 첫 줄)에는 안 붙인다 — 이 대화가 쓰는 카드라 닫으면 발밑을 지운다.
		s += "  갈린 카드: " + clip(t.SessionID, 64) + " · cc=" + clip(t.CCSessionID, 64) +
			" — 닫으려면: fd close --session " + clip(t.SessionID, 64) + "\n"
	}
	s += "  같은 창의 카드라면 훅이 다음 SessionStart 에 합친다. " +
		"같은 워크트리에 열린 **다른 창**이라면 안 합쳐진다 — 그쪽이 맞다.\n" +
		// ★ 명령을 실은 이상 이 경고가 함께 가야 한다. 위 두 갈래는 사람만 가를 수 있는데,
		// 명령만 보이면 둘 다 닫는 쪽으로 흐른다 — 그러면 살아 있는 다른 창이 보드에서 사라지고,
		// 그 창이 든 선점은 아무에게도 안 보인다(runClose 의 선점 가드가 막는 바로 그 상태다).
		"  닫기 전에 어느 쪽인지 가려라 — 살아 있는 다른 창을 닫으면 그 창이 보드에서 사라진다."
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
