package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 아웃박스 — 오프라인에서 쌓이는 **판단·노트만**. 재연결 시 멱등 재생한다.
//
// 왜 이것만인가: 판단은 원리적으로 재생성 불가한 유일한 자산이다(설계 §7).
// 잃어도 다시 만들면 되는 것에 재생 기구를 만들지 않는다 — 재생 기구는
// 충돌 판정을 요구하고, 그러면 복잡도가 곱해진다.

// OutboxEntry 는 아직 못 보낸 쓰기 하나다.
type OutboxEntry struct {
	Key  string          `json:"key"` // Idempotency-Key. 이 값이 중복 재생을 막는 유일한 축이다
	At   time.Time       `json:"at"`
	Path string          `json:"path"`
	Body json.RawMessage `json:"body"`

	// Tries 는 **서버에 닿았는데** 실패한 횟수다. 없으면 0 이다(옛 파일과 호환된다).
	//
	// 미도달은 안 센다 — 못 보낸 것은 이 줄이 나쁘다는 증거가 아니다.
	//
	// ★ 이 필드가 없어서 **영구히 막힌 줄이 큐를 영구히 막았다.** 실측(2026-08-04):
	// 서버가 살아 있는데 8/3 12:43 판단 하나가 재생되지 않은 채 남아 있었고,
	// 아웃박스 파일 mtime 이 그날 10:31 로 갱신돼 있었다 — keep() 은 Replay 만 부르므로
	// **재생은 돌았고 전송이 거절당했다**는 뜻이다. 거절은 몇 번을 다시 보내도 같은 답인데
	// Replay 는 그 자리에서 멈추고 뒤엣것을 통째로 남겼다.
	Tries int `json:"tries,omitempty"`
}

// RejectedEntry 는 **영구 거절로 판정돼 격리된** 줄이다.
//
// ★ 버리지 않는다. 판단은 재생성 불가한 유일한 자산이라(설계 §7·§9 "조용히 버리는 것이
// 하나도 없어야 한다") 큐에서 빼되 파일로 옮긴다. 큐를 비우는 것과 기록을 없애는 것은 다르다.
type RejectedEntry struct {
	Entry  OutboxEntry `json:"entry"`
	Reason string      `json:"reason"` // 왜 영구로 판정했나. **항상 채운다**
	At     time.Time   `json:"at"`     // 격리한 시각
}

// maxReplayTries 는 분류가 안 되는 실패를 몇 번까지 참는가다.
//
// ★ 상태코드만으로 가르면 안 되는 이유가 실측에 있다. 세션이 사라진 뒤의 판단 재생처럼
// 하류에서 FK 로 깨지는 실패는 서버가 **500 으로 낸다**(ClassifyError 의 기본 가지).
// 500 은 정의상 "일시 장애일 수 있다"라 재시도가 옳은데, 그 줄은 영원히 500 이다.
// 그래서 시도 횟수라는 둘째 축을 둔다 — 분류가 못 가르는 것을 시간이 가른다.
//
// 5인 이유: 진짜 서버 장애 한 번(재기동·배포)은 명령 몇 번 안에 끝나고, 그보다 오래
// 실패하는 줄은 장애가 아니라 그 줄의 문제다. 크게 잡으면 큐가 그만큼 오래 막힌다.
const maxReplayTries = 5

// ReplayVerdict 는 실패 하나를 어떻게 다룰지다. 사유는 항상 채운다.
type ReplayVerdict struct {
	Permanent bool
	Reason    string
}

// JudgeReplayFailure 는 재생 실패 하나를 분류한다. 순수 함수다.
//
// 축 둘을 본다. **상태코드**(같은 답이 반복되는가)와 **시도 횟수**(분류가 못 가르는데
// 계속 실패하는가). 하나만으로는 부족하다 — 위 maxReplayTries 주석의 500 사례가 그 증거다.
//
// ★ 모르는 실패는 **영구로 접지 않는다.** 판단은 재생성 불가하므로 의심스러우면 남긴다.
// 다만 남기는 것이 영원이 되지 않게 시도 횟수가 상한을 준다.
func JudgeReplayFailure(err error, tries int) ReplayVerdict {
	if err == nil {
		return ReplayVerdict{}
	}
	// ★ **미도달은 횟수에 안 센다. 순서가 여기서 중요하다.**
	// 서버가 꺼져 있는 동안 명령을 다섯 번 돌리는 것은 완전히 정상이고, 그 다섯 번은
	// 그 줄에 대해 **아무것도 말해 주지 않는다** — 못 보낸 것이지 거절당한 것이 아니다.
	// 횟수를 먼저 보면 오프라인이 길다는 이유만으로 멀쩡한 판단이 격리된다.
	// 앞선 판에서 실제로 그렇게 썼고, 훅의 L1 시험이 그것을 잡았다.
	if Unreachable(err, 0) {
		return ReplayVerdict{Reason: "서버에 못 닿았다 — 다음 재생에서 다시 보낸다(미도달은 횟수에 안 센다)"}
	}
	// 여기부터는 **서버에 닿았는데 실패한** 것이다. 그때만 횟수가 뜻을 갖는다.
	if tries+1 >= maxReplayTries {
		return ReplayVerdict{Permanent: true, Reason: fmt.Sprintf(
			"서버에 닿았는데 %d번 연속 실패했다 — 일시 장애로 보기에는 너무 오래다. 마지막 오류: %s",
			tries+1, clip(err.Error(), 300))}
	}
	var ae *APIError
	if errors.As(err, &ae) {
		switch {
		case ae.Status == http.StatusRequestTimeout || ae.Status == http.StatusTooManyRequests:
			return ReplayVerdict{Reason: fmt.Sprintf("서버가 %d 로 되물렀다 — 다시 보낸다", ae.Status)}
		case ae.Status >= 400 && ae.Status < 500:
			return ReplayVerdict{Permanent: true, Reason: fmt.Sprintf(
				"서버가 %d 로 거절했다 — 같은 요청은 몇 번을 보내도 같은 답이다: %s",
				ae.Status, clip(ae.Message, 300))}
		}
	}
	return ReplayVerdict{Reason: "분류할 수 없는 실패 — 판단을 잃지 않으려고 남긴다: " +
		clip(err.Error(), 200)}
}

// IdempotencyStable 은 이 명령의 멱등 키를 **본문으로 고정할지** 정한다. 순수 함수다.
//
// 축은 하나다: **같은 응답을 다시 받아도 되는가.**
//
//   - 고정(true) — 같은 본문의 재시도는 한 건이다. 아웃박스 재생이 이 축에 기댄다.
//   - 새로(false) — 응답에 "지금"이 실려 있어 재사용하면 낡은 답이 나간다.
//
// ★ alloc 을 고정하면 **두 호출이 같은 번호를 받는다** — 이 도구가 없애려는 바로 그 사고다.
// 그래서 이 판정을 불리언 하나로 두지 않고 사유를 함께 돌려준다: 다음 명령이 늘 때
// "왜 이쪽인가"를 물을 자리가 있어야 한다.
func IdempotencyStable(cmd string) (bool, string) {
	switch strings.TrimSpace(cmd) {
	case "note", "finish", "add":
		return true, "같은 본문의 재시도는 한 건이다 — 아웃박스 재생과 훅 재시도가 이 축에 기댄다"
	case "alloc":
		return false, "고정하면 두 호출이 같은 번호를 받는다 — 발번의 존재 이유가 사라진다"
	case "open":
		return false, "응답에 지금 상태(신규 여부·선점 목록)가 실려 있어 고정하면 낡은 답이 재생된다"
	case "beat":
		return false, "신호는 시각이 값이다 — 고정하면 두 번째 신호부터 서버에 안 닿는다"
	case "pick", "claim":
		return false, "선점 결과는 지금 상태다 — 고정하면 남이 반납한 뒤에도 옛 거절이 재생된다"
	case CmdLandAcquire, CmdLandReport, CmdLandLeave, CmdLaneRelease, CmdClaimRelease, CmdClaimLeave:
		// ★ 기본 가지도 false 라 동작은 같지만, 사유가 다르다. 기본 문구는 "모르는 명령이라"라고
		//   말하는데 이 다섯은 아는 명령이다 — 그대로 두면 다음 사람이 "표에 없으니 넣어야겠다"
		//   하고 위쪽(고정) 목록에 넣는다. 고정하면 대기 중인 세션이 land 를 다시 부를 때
		//   **첫 응답("너는 3번째다")이 영원히 재생돼** 차례가 왔는데도 오지 않은 것으로 보이고,
		//   선점 회수는 재실행이 옛 "회수했다"를 재생해 그 사이 새로 잡힌 선점이 안 풀린 채
		//   성공으로 보인다.
		return false, "응답이 지금 상태다(내 자리·점유자) — 선점과 같은 처지라 고정하면 낡은 답이 재생된다"
	case CmdProjectRemove:
		// ★ 이 가지도 기본값(false)과 동작은 같지만 사유를 명시한다 — 위 다섯과 같은 이유다
		//   (최종 리뷰 Important-3). 고정하면 위험한 구체적 경로: 프로젝트에 항목이 남아
		//   거절된 뒤(응답에 그 시점의 item·judgment·judgment_foreign 수가 실린다) 사람이
		//   항목을 치우고 **같은 project·같은 reason 으로 다시** 부르면, 본문이 똑같으므로
		//   고정 키가 그때와 같은 값을 낸다 — 서버는 새로 세지 않고 옛 "항목이 있어 거절"
		//   응답을 그대로 재생하고, 지금은 지울 수 있는 프로젝트가 영원히 거절된 것처럼 보인다.
		return false, "응답이 지금 상태다(그 순간의 항목·판단·교차 판단 수) — 고정하면 " +
			"뒷사람이 항목을 치우고 같은 본문으로 다시 불러도 옛 거절이 재생된다"
	case CmdMove:
		// ★ 이 가지도 기본값(false)과 동작은 같지만 사유를 명시한다 — CmdProjectRemove 와
		//   같은 이유(최종 리뷰 Important-3의 규율을 그대로 잇는다). 고정하면 위험한
		//   구체적 경로: 항목 X 를 Y 로 옮긴 뒤(성공) 그 항목이 다른 경로로 원래 자리로
		//   되돌아가고, 같은 세션이 **같은 본문**(같은 item·같은 --project)으로 move 를
		//   다시 부르면 — 고정 키가 그때와 같은 값을 내 서버는 실제로 옮기지 않고 옛
		//   성공 응답을 그대로 재생한다. 화면은 "옮겼다"고 말하는데 항목은 실제로는
		//   그대로다.
		return false, "응답이 지금 상태다(그 순간의 From·To·CrossRefs) — 고정하면 항목이 " +
			"그 사이 도로 옮겨진 뒤 같은 본문으로 다시 불러도 실제 이동 없이 옛 성공 " +
			"응답이 재생된다"
	case CmdAfterCut:
		// ★ 위 CmdMove 와 같은 위험이다. 같은 (item, dep축, 값) 으로 다시 부르면 — 예를
		//   들어 같은 선행이 다른 경로로 재등록된 뒤 — 고정 키가 실제 재실행 없이 첫
		//   "끊었다" 응답을 재생한다. 되돌릴 수 없는 관계 편집에 낡은 성공을 재생하는
		//   쪽이 alloc 이 고정을 피하는 이유(두 호출이 같은 번호를 받는다)와 같은 결이다.
		return false, "응답이 지금 상태다(그 순간에 끊은 것·남은 선행) — 고정하면 선행이 " +
			"다른 경로로 재등록된 뒤 같은 본문으로 다시 불러도 실제로는 안 끊긴 채 옛 " +
			"성공 응답이 재생된다"
	case CmdLabel:
		// ★ CmdMove·CmdAfterCut 과 같은 위험이다. 꼬리표를 달았다가 다른 경로로 도로
		//   뗀 뒤 **같은 본문**으로 다시 부르면, 고정 키가 그때와 같은 값을 내 서버는
		//   실제로 쓰지 않고 옛 성공 응답을 재생한다. 화면은 "더했다"고 말하는데
		//   항목에는 안 붙어 있다.
		return false, "응답이 지금 상태다(그 순간의 before·after·실제 변화분) — 고정하면 " +
			"꼬리표가 그 사이 도로 바뀐 뒤 같은 본문으로 다시 불러도 실제 쓰기 없이 " +
			"옛 성공 응답이 재생된다"
	case CmdPrescriptions:
		// ★ 이 가지는 false 를 고른 근거가 위 셋과 다르다 — "지금 상태가 실려서"가 아니라
		//   **본문과 경로가 애초에 세션 내내 불변**이라서다. hook.go 의 hookStop 은 매
		//   턴마다 `a.cli.Write(ctx, CmdPrescriptions, ".../prescriptions", struct{}{})`
		//   를 부르는데 body 는 항상 빈 구조체이고 path 는 세션 id 로 고정된다
		//   (KeyFor 의 키 = session + path + body). **고정하면 그 세션의 모든 턴이 같은
		//   키를 얻어, 둘째 턴부터는 서버에 새로 묻지 않고 첫 턴의 처방을 영원히
		//   재생한다** — 이 명령이 애초에 존재하는 이유(매 턴 새 처방을 받는 것)가
		//   사라진다. alloc 이 고정을 피하는 이유(두 호출이 같은 값을 받는다)보다 더
		//   구체적인 위험이다: alloc 은 "가끔"이 아니라 "이 명령은 100% 매번 같은
		//   본문이라" 첫 호출 이후 전부 재생이 된다.
		return false, "본문(빈 구조체)과 경로(세션 id 고정)가 그 세션 내내 안 바뀐다 — " +
			"고정하면 그 세션의 둘째 턴부터 서버에 새로 묻지 않고 첫 턴의 처방을 영원히 재생한다"
	default:
		return false, "모르는 명령이라 고정하지 않는다 — 고정은 '응답을 재사용하겠다'는 선언이고, " +
			"그것을 기본값으로 두면 새 명령마다 조용히 낡은 답이 나간다"
	}
}

// FreshKey 는 재사용하지 않을 멱등 키다. 순수 함수는 아니다(난수를 쓴다).
//
// 프로토콜이 모든 쓰기에 키를 요구하므로(설계 §6) 고정이 위험한 명령에도 키는 있어야 한다.
// 난수가 없으면 나노초로 대신한다 — 값이 없는 것보다 낫고, 그 경우도 재사용되지 않는다.
func FreshKey(session string) string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return IdempotencyKey(session, []byte(fmt.Sprintf("nonce-%d", time.Now().UnixNano())))
	}
	s := strings.TrimSpace(session)
	if s == "" {
		s = "nosession"
	}
	return clip(s, 64) + ":" + hex.EncodeToString(buf)
}

// IdempotencyKey 는 쓰기 하나의 **고정** 멱등 키다. 순수 함수다.
//
// 설계 §6 의 형식은 `<session>:<seq>` 다. 세션이 없을 수 있는 경로(훅이 세션 열기 전)가
// 있으므로 본문 해시를 seq 자리에 쓴다 — **같은 세션이 같은 본문을 두 번 쌓으면 한 건이다.**
// 시각을 넣지 않는 이유가 그것이다: 시각을 넣으면 재시도마다 키가 달라져 멱등이 이름뿐이 된다.
func IdempotencyKey(session string, body []byte) string {
	sum := sha256.Sum256(body)
	s := strings.TrimSpace(session)
	if s == "" {
		s = "nosession"
	}
	return clip(s, 64) + ":" + hex.EncodeToString(sum[:12])
}

// 대기열·격리 파일의 이름. 한 자리에 모은다 — 옛 자리 재생이 이 이름으로 큐를 찾으므로
// 두 자리에 흩어 두면 한쪽만 고칠 때 그 큐가 조용히 안 보이게 된다.
const (
	pendingName = "pending.jsonl"
	// pendingDirName 은 **항목당 파일** 큐의 자리다. 새 쓰기는 전부 여기로 간다.
	//
	// ★ 왜 파일 하나가 아닌가. `pending.jsonl` 판은 잠금 구간의 첫 줄이 파일 전량 읽기,
	// 끝이 파일 전량 쓰기라 **점유가 O(큐 크기)** 였다(실측 ext4: 1000건 병합 17.9ms ·
	// p95 35.4ms). 큐가 깊고 세션이 몰리면 예산 250ms 를 못 채우는 프로세스가 나오고
	// 무잠금으로 떨어져 **판단이 사라졌다**(큐 1000건·세션 30 에서 유실 10/36).
	// 예산으로는 못 닫는다 — 30s 면 유실이 0 이 되지만 훅 예산(2s·3s·10s)을 통째로 넘긴다.
	//
	// ★ **그래서 이 자리에는 잠금이 없다.** 항목당 파일이면 `Append` 는 O_EXCL 한 번이고
	// `settle` 은 내가 처리한 키의 파일만 지운다 — 남의 파일을 건드릴 수 있는 연산이 하나도
	// 없다. 겹친 재생 둘은 각자 읽고 각자 보내고(서버가 멱등 키로 접는다) 각자 지운다.
	// 잠금이 닫았던 축 셋이 여기서는 **자료구조로** 닫힌다(되살아남 300/300 · 되쓰기 삭제
	// 33/300 · 진짜 프로세스 둘의 유실 15/200 — 전부 전량 재기록이 있어야 나는 것들이다).
	pendingDirName = "pending"
	rejectedName   = "rejected.jsonl"
	// rejectedDirName 은 **격리 사건당 파일**의 자리다.
	//
	// ★ 왜 파일 하나가 아닌가. 겹친 재생 둘은 `Replay` 의 첫 `List`·`send`·거절 판정이
	// 전부 잠금 밖이라 **각자 "쓰겠다"고 결정한다.** 그래서 O_APPEND 판에서는 같은 사건이
	// 두 줄로 들어갔다(실측 300라운드×6판: 286~299/300). **잠금으로는 못 닫는다** —
	// 결정이 이미 두 번 내려졌으므로 직렬화는 순서만 정하고 개수를 안 바꾼다. 개수를 닫는
	// 유일한 길은 **중복 판정을 커널의 원자 연산으로** 만드는 것이고, 그것이 이 자리다.
	rejectedDirName = "rejected"
	// failOpenName 은 **잠금 없이 지나간 사건**의 기록이다.
	//
	// ★ 왜 파일인가. 이 사건은 오래 `o.warn` 으로만 흘렀고 그 로거는 stderr 로 간다.
	// `fd doctor` 는 파일만 읽는다 — 로그를 읽는 줄이 하나도 없다. 그리고 이 경로의 주
	// 사용자인 훅은 설계가 "정의상 조용히 죽는다"고 적어 둔 것이다. 그래서 "얼마나 자주
	// fail-open 하나"를 물을 자리가 **구조적으로** 없었고, 그 상태로 두면 "잠금을 넣었다"가
	// 거짓 안심이 된다(설계 §9).
	failOpenName = "failopen.jsonl"
)

// FailOpenEvent 는 잠금 없이 지나간 사건 하나다.
type FailOpenEvent struct {
	At     time.Time `json:"at"`
	Op     string    `json:"op"`     // 어느 갈래인가 — "bump" | "settle"(옛 형식 병합)
	Reason string    `json:"reason"` // 왜 못 잡았나. **항상 채운다**
}

// FailOpenReason 은 잠금 없이 지나간 사유다. 순수 함수다.
//
// ★ **빈 문자열이면 기록하지 않는다.** 잠금 기구가 없는 플랫폼에서는 모든 호출이
// fail-open 인데, 그것은 사건이 아니라 **상수**다. 사건으로 세면 `fd` 호출마다 한 줄씩
// 파일이 자라고 그 수는 "얼마나 자주 경합하나"를 하나도 안 말한다. 그 사실은 doctor 가
// 한 번 말할 것이지 계수기가 셀 것이 아니다(설계 §13 — 안 잰 축을 잰 척하지 않는다).
//
// ★ 예산 초과와 기구 실패를 **가른다.** 뭉치면 처방이 갈린다: 예산 초과는 "경합이 세다"라
// 큐를 쪼개야 하고, 기구 실패는 "자리를 못 열었다"라 권한·경로 문제다.
func FailOpenReason(supported bool, err error) string {
	if !supported {
		return "" // 기구가 없다. 사건이 아니라 상수다
	}
	if err != nil {
		return "잠금 기구가 실패했다: " + clip(err.Error(), 200)
	}
	return "예산 안에 못 잡았다 — 큐가 깊거나 세션이 몰렸다"
}

// Outbox 는 디렉토리 하나의 대기열이다. 파일 하나에 JSONL 로 쌓는다.
//
// ★ 예전에는 상태 디렉토리 아래였고, 그 자리가 채널마다 갈려서 셸에서 쌓인 판단을
// 훅·MCP 가 영영 못 보내는 결함이 있었다(OutboxPath 주석에 판정 전문이 있다).
// 지금은 새 쓰기가 고정 자리로 가고, 옛 자리는 **같은 타입의 값을 하나씩 만들어**
// 재생이 함께 돈다(Client.Legacy). 큐 하나가 이 값 하나다.
type Outbox struct {
	dir    string // 대기열·격리 파일이 있는 디렉토리
	source string // 왜 이 자리인가. fd doctor 가 찍는다 — machineSrc 가 선례다
	// now 는 격리 시각을 찍는 시계다. 시험이 갈아 끼울 자리이기도 하다.
	now func() time.Time
	// log 는 **잠금을 못 잡아 무잠금으로 떨어졌다**를 말할 자리다. 없어도 된다(nil 안전).
	//
	// ★ 이 필드가 없으면 fail-open 이 침묵이 된다. 무잠금 갈래는 오늘과 같은 동작이라
	// 나빠지지는 않지만, 그것이 **얼마나 자주 일어나는지**를 아무도 못 보는 상태로 두면
	// "잠금을 넣었다"가 거짓 안심이 된다(설계 §9 — 조용히 버리는 것이 하나도 없어야 한다).
	log *slog.Logger
}

// withLogger 는 잠금 경고를 받을 로거를 꽂는다. Client 가 큐를 만든 직후에 부른다.
func (o *Outbox) withLogger(l *slog.Logger) *Outbox {
	o.log = l
	return o
}

// warn 은 로거가 없어도 안전하다.
func (o *Outbox) warn(msg string, args ...any) {
	if o.log == nil {
		return
	}
	o.log.Warn(msg, args...)
}

func newOutbox(get func(string) (string, bool), home string) *Outbox {
	dir, src := OutboxPath(get, home)
	o := newOutboxAt(dir)
	o.source = src
	return o
}

// newOutboxAt 은 자리를 직접 주는 생성자다. 옛 자리 큐(Client.Legacy)와 시험이 쓴다.
func newOutboxAt(dir string) *Outbox {
	return &Outbox{
		dir:    dir,
		source: "직접 지정",
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// Dir·Source 는 fd doctor 가 "어디를, 왜"를 찍기 위한 자리다.
func (o *Outbox) Dir() string    { return o.dir }
func (o *Outbox) Source() string { return o.source }

// pendingPath·rejectedPath 는 두 파일의 자리다. 같은 디렉토리에 둔다 —
// 같은 축의 같은 자산이고, 격리는 제 큐 옆에 남아야 '어디서 온 것인가'가 안 사라진다.
func (o *Outbox) pendingPath() string  { return filepath.Join(o.dir, pendingName) }
func (o *Outbox) pendingDir() string   { return filepath.Join(o.dir, pendingDirName) }
func (o *Outbox) rejectedPath() string { return filepath.Join(o.dir, rejectedName) }
func (o *Outbox) rejectedDir() string  { return filepath.Join(o.dir, rejectedDirName) }

// entryFileName 은 키 하나가 갖는 파일 이름이다. 순수 함수다.
//
// ★ **이름은 키만으로 결정된다.** 항목 본문은 `<정렬가능한시각>-<key>.json` 을 제안하면서
// 동시에 O_EXCL 이 중복 키 검사를 커널의 원자 연산으로 만든다고 적었는데, **둘은 같이
// 성립하지 않는다**: 같은 키를 두 번 쌓는 유일한 경로가 훅 재시도이고 그때 `At` 은
// `c.Now()` 라 다르다. 이름에 시각이 들어가면 같은 키가 다른 파일을 얻어 O_EXCL 이
// 아무것도 안 막는다. **순서는 이름이 아니라 값(`At`)이 정한다** — List 참조.
//
// ★ 키를 그대로 안 쓰는 이유는 별개다. 키 형식이 `<session>:<hash>` 인데
// **windows 는 파일명에 `:` 를 못 쓴다.** 치환은 서로 다른 키가 같은 이름을 얻는 길을
// 열므로(그러면 남의 판단이 사라진다) 해시로 접는다. 충돌은 sha256 앞 16바이트다.
//
// ★ 빈 키는 **빈 이름을 낸다** — 호출자가 그것을 보고 거절한다. 빈 키끼리는 서로
// 별칭이라(settle 이 같은 이유로 그 줄을 절대 안 지운다) 한 이름에 접으면 서로 다른
// 판단이 서로를 덮어쓴다.
func entryFileName(key string) string {
	if strings.TrimSpace(key) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16]) + ".json"
}

// entryPath 는 키 하나가 사는 파일의 전체 경로다. 빈 키면 빈 문자열이다.
func (o *Outbox) entryPath(key string) string {
	name := entryFileName(key)
	if name == "" {
		return ""
	}
	return filepath.Join(o.pendingDir(), name)
}
func (o *Outbox) failOpenPath() string { return filepath.Join(o.dir, failOpenName) }

// recordFailOpen 은 잠금 없이 지나간 사건 하나를 남긴다.
//
// ★ **잠그지 않는다. 잠글 수도 없다** — 여기는 잠금을 못 잡아서 온 자리다.
// 그리고 잠글 이유도 없다: 격리 파일과 성질이 반대라, 겹친 프로세스 둘이 각자 못 잡았으면
// 그것은 **두 사건**이다. 여기서 중복을 제거하면 세려던 것을 지운다. O_APPEND 가
// 정확히 맞는 원시 연산이다.
//
// ★ **실패해도 호출자를 안 막는다.** 이 함수는 관측이고, 관측이 아웃박스 경로를 깨면
// 재생성 불가한 자산을 계수기 때문에 잃는다. 다만 조용히 넘어가지도 않는다 — 경고로 낸다.
func (o *Outbox) recordFailOpen(op string, supported bool, lockErr error) {
	reason := FailOpenReason(supported, lockErr)
	if reason == "" {
		return
	}
	buf, err := json.Marshal(FailOpenEvent{At: o.stamp(), Op: op, Reason: reason})
	if err == nil {
		// ★ 자리를 먼저 만든다. 여기가 큐보다 먼저 도는 갈래가 있다 — 잠금을 못 잡은 첫
		// 호출이 아직 아무것도 안 쓴 큐일 수 있고, 그러면 디렉토리가 없어 ENOENT 로
		// **조용히** 실패한다(경고 로거는 대개 안 꽂혀 있다). appendLocked·quarantine 이
		// 같은 이유로 같은 줄을 갖고 있다.
		err = os.MkdirAll(o.dir, 0o755)
	}
	if err == nil {
		var f *os.File
		f, err = os.OpenFile(o.failOpenPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, err = f.Write(append(buf, '\n'))
			f.Close()
		}
	}
	if err != nil {
		o.warn("잠금 실패를 기록하지 못했다 — 이 사건은 어디에도 안 남는다",
			"dir", o.dir, "op", op, "error", err.Error())
	}
}

// FailOpens 는 이 큐에서 잠금 없이 지나간 사건 전부다. 파일이 없으면 빈 목록이다.
func (o *Outbox) FailOpens() ([]FailOpenEvent, error) {
	b, err := os.ReadFile(o.failOpenPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("잠금 실패 기록을 못 읽었다: %w", err)
	}
	var out []FailOpenEvent
	for i, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e FailOpenEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			// 읽은 데까지와 함께 올린다 — 이 파일도 조용히 버리지 않는다(설계 §9).
			return out, fmt.Errorf("잠금 실패 기록 %d번째 줄 해석 실패: %w", i+1, err)
		}
		out = append(out, e)
	}
	return out, nil
}

// stamp 는 지금이다. 시계가 안 꽂혔어도 값을 낸다.
func (o *Outbox) stamp() time.Time {
	if o.now == nil {
		return time.Now().UTC()
	}
	return o.now()
}

// Append 는 쓰기 하나를 쌓는다. **같은 키가 이미 있으면 쌓지 않는다** —
// 훅은 실패하면 재시도되므로, 그대로 두면 한 판단이 여러 줄이 된다.
//
// ★ 그 중복 검사는 List→검사→O_APPEND 라 **그 사이에 남이 끼면 서로를 못 본다.**
// 그래서 프로세스 간 잠금 아래에서 돈다(outbox_lock.go).
//
// ★ **피해는 중복이 아니라 삭제였다.** 이 주석은 오래 "한 판단이 여러 줄이 된다"만
// 적어 뒀는데, 재현해 보니 더 무거운 것이 있었다: 재생이 도는 동안 Append 한 줄은
// 재생의 스냅숏에 없어서 keep() 의 전량 재기록에 통째로 지워진다. 격리에도 안 남고,
// Append 를 부른 쪽은 err=nil 을 받아 "쌓았다"를 찍는다. 멱등 키는 그것을 못 막는다 —
// 막을 줄 자체가 파일에서 사라지기 때문이다.
//
// 잠금을 못 잡으면 **무잠금으로 진행한다**(fail-open). 훅 예산을 넘기며 기다리는 것보다
// 오늘과 같은 상태로 떨어지는 쪽이 낫다 — 다만 조용히 떨어지지는 않는다.
// ★ **이 함수는 잠금을 안 쥔다.** 옛 판은 `List→검사→O_APPEND` 라 그 사이에 남이 끼면
// 서로를 못 봤고, 그래서 프로세스 간 잠금 아래에서 돌았다. 항목당 파일에서는 중복 검사가
// **파일 이름의 존재**이고 그 판정을 커널이 원자적으로 한다 — 검사와 쓰기 사이에 창이 없다.
//
// ★ **왜 `O_CREATE|O_EXCL` 직접 쓰기가 아니라 tmp + `Link` 인가.** 항목 본문이 적은
// O_EXCL 은 *이름*은 원자적으로 잡지만 **내용은 아니다**: 자리를 잡은 뒤 Write 까지의
// 사이에 남의 `List` 가 그 파일을 읽으면 0바이트 또는 반쪽 JSON 을 본다. 그 자리에서
// 해석 실패를 오류로 올리면 **정상 동시성이 매번 빨간 줄을 내고**, 조용히 건너뛰면
// 재생성 불가한 자산을 버리는 것이 된다(설계 §9) — 둘 다 못 쓴다. `Link` 는 **내용이
// 완성된 파일에 이름을 붙이는** 연산이라 두 성질을 함께 준다: 보이는 순간 이미 온전하고,
// 이름이 이미 있으면 EEXIST 다.
//
// ★ **Link 가 안 되는 파일 시스템에 폴백을 안 짓는다.** 안 재본 플랫폼을 위해 두 경로를
// 두면 그 경로가 시험 없이 산다. 실패하면 오류로 말한다 — 조용히 다른 길로 가지 않는다.
func (o *Outbox) Append(e OutboxEntry) error {
	path := o.entryPath(e.Key)
	if path == "" {
		// ★ 옛 판은 빈 키도 쌓았고, `List` 의 `x.Key == e.Key` 가 빈 키끼리 매칭돼서
		// **둘째 빈 키가 조용히 안 쌓였다.** 빈 키는 서로 별칭이라(settle 이 같은 이유로
		// 그 줄을 절대 안 지운다) 그 동작은 서로 다른 판단 하나를 소리 없이 버리는 것이었다.
		// 여기서는 거절한다 — 작성기는 빈 키를 안 만들고(FreshKey·IdempotencyKey 가
		// nosession 을 채운다) 그래도 온다면 그것은 고쳐야 할 호출부다.
		return errors.New("멱등 키가 빈 판단은 안 쌓는다 — 빈 키끼리 서로 별칭이 되어 " +
			"한쪽이 다른 쪽을 지운다. 키는 FreshKey·IdempotencyKey 가 만든다")
	}
	// ★ 옛 형식에 이미 있으면 안 쌓는다. 이 자리를 빼면 형식 전환 중인 큐에서 같은 판단이
	// 두 벌이 된다 — JSONL 쪽 한 줄과 항목 파일 하나. 둘 다 보내지지만 서버가 멱등으로
	// 접으므로 원장은 멀쩡하고, **큐 잔량이 실제보다 커 보인다**(doctor 가 거짓말한다).
	legacy, err := readEntries(o.pendingPath())
	if err != nil {
		return err
	}
	for _, x := range legacy {
		if x.Key == e.Key {
			return nil // 이미 쌓여 있다. 조용히 넘어가도 되는 유일한 경우다
		}
	}
	buf, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("아웃박스 직렬화 실패: %w", err)
	}
	if err := os.MkdirAll(o.pendingDir(), 0o755); err != nil {
		return fmt.Errorf("아웃박스 디렉토리 생성 실패: %w", err)
	}
	// ★ tmp 이름은 `.json` 으로 안 끝난다 — readEntryDir 가 그 접미사로 거른다.
	//   이름이 프로세스마다 다른 이유는 keep 의 tmp 와 같다(고정 이름이면 둘이 같은
	//   파일에 쓰고 서로의 바이트가 섞인다).
	tmp := filepath.Join(o.pendingDir(), ".tmp-"+tmpNonce())
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("아웃박스 기록 실패: %w", err)
	}
	// ★ 성공하든 실패하든 tmp 는 치운다. Link 가 성공해도 tmp 는 같은 inode 를 가리키는
	//   둘째 이름으로 남아, 안 치우면 큐 자리에 잔해가 하나씩 쌓인다.
	defer os.Remove(tmp)
	if err := os.Link(tmp, path); err != nil {
		if os.IsExist(err) {
			return nil // 이미 쌓여 있다
		}
		return fmt.Errorf("아웃박스 항목 자리를 못 잡았다(%s): %w", filepath.Base(path), err)
	}
	return nil
}

// List 는 대기 중인 전부를 순서대로 낸다. 파일이 없으면 빈 목록이다(오류가 아니다).
//
// ★ **두 형식을 다 읽는다** — 항목당 파일(`pending/`)과 옛 `pending.jsonl`. 그래서
// 형식 전환에 마이그레이션 코드가 없다: 쓰기는 전부 새 형식으로 가고, 옛 파일이 있는
// 자리는 재생이 **보내서** 비우며 그때 JSONL 이 사라진다. 옛 채널 자리(`Client.Legacy`)도
// 같은 코드로 돌아서 형식 플래그가 필요 없다. 옮기지 않는다는 설계 판정(§7)도 그대로다 —
// 읽는 자리가 둘일 뿐 판단은 제자리에서 전송으로 나간다.
//
// ★ **순서는 이름이 아니라 `At` 이 정하고, 정렬은 안정 정렬이다.**
// 옛 판의 순서는 append 도착 순(= 잠금 획득 순)이었지 `At` 순이 아니었다. 새 판은
// `At` 순이고, 그쪽이 "판단은 시간축이 의미다"(Replay 주석)에 가깝다 — 서버가 판단
// 시각을 자기 수신 시각으로 채우므로(`store/judgment.go`) **큐 순서가 곧 원장 순서**다.
// 안정 정렬인 이유: `At` 이 동률일 때 입력 순서(항목 파일은 이름순, 그다음 JSONL 은
// 파일 내 순서)가 그대로 남아야 결과가 결정적이다. 시험 하네스가 모든 줄에 같은 `At` 을
// 심는데(`entry()` 는 `time.Unix(0,0)`), 불안정 정렬이면 거기서 순서가 흔들린다.
//
// ★ 이 함수는 **잠금을 안 쥔다.** 겹친 재생이 방금 지운 파일은 ENOENT 로 건너뛴다 —
// 그것은 오류가 아니라 "남이 이미 처리했다"이고, 이 자료구조에서 정상 갈래다.
func (o *Outbox) List() ([]OutboxEntry, error) {
	out, err := readEntryDir(o.pendingDir())
	legacy, lerr := readEntries(o.pendingPath())
	if n := fillMissingKeys(legacy); n > 0 {
		o.warn("키 없는 줄에 본문 기준 키를 부여했다 — 다음 재생부터 정상 판단으로 나간다",
			"dir", o.dir, "건수", n)
	}
	out = append(out, legacy...)
	// ★ 오류가 나도 읽은 데까지는 정렬해서 낸다 — 이 파일은 재생성 불가한 자산이라
	// 부분 결과를 버리지 않는다(설계 §9). readEntries 가 같은 규율을 갖고 있다.
	sortEntriesByAt(out)
	if err != nil {
		return out, err
	}
	return out, lerr
}

// sortEntriesByAt 은 `At` 오름차순 **안정** 정렬이다. 순수 함수는 아니다(제자리 정렬).
func sortEntriesByAt(es []OutboxEntry) {
	sort.SliceStable(es, func(i, j int) bool { return es[i].At.Before(es[j].At) })
}

// readEntryDir 는 항목당 파일 큐 하나를 읽는다. 디렉토리가 없으면 빈 목록이다.
//
// ★ `os.ReadDir` 는 이름순으로 낸다(문서 보증). 그래서 `At` 동률일 때의 입력 순서가
// 결정적이고, 위 안정 정렬이 그것을 보존한다.
//
// ★ **읽는 중 사라진 파일은 오류가 아니다.** 겹친 재생이 그 사이 보내고 지웠다는 뜻이고,
// 그 판단은 잃은 것이 아니라 이미 갔다. 이것을 오류로 올리면 정상 동시성이 매번 빨간
// 줄을 낸다 — 그리고 그 빨간 줄은 진짜 결함을 가린다.
//
// ★ 깨진 파일은 **조용히 버리지 않는다**(readEntries 와 같은 규율, 설계 §9).
func readEntryDir(dir string) ([]OutboxEntry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("아웃박스 항목 디렉토리를 못 읽었다(%s): %w", dir, err)
	}
	out := make([]OutboxEntry, 0, len(des))
	for _, de := range des {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, de.Name()))
		if err != nil {
			if os.IsNotExist(err) {
				continue // 겹친 재생이 방금 보내고 지웠다
			}
			return out, fmt.Errorf("아웃박스 항목 %s 를 못 읽었다: %w", de.Name(), err)
		}
		var e OutboxEntry
		if err := json.Unmarshal(b, &e); err != nil {
			return out, fmt.Errorf("아웃박스 항목 %s 를 해석하지 못했다: %w", de.Name(), err)
		}
		out = append(out, e)
	}
	return out, nil
}

// fillMissingKeys 는 키가 빈 줄에 **본문 기준 키를 부여한다.** 채운 개수를 낸다.
//
// ★★ **빼지 않고 늘지도 않게 하는 길이 이것이다.** 앞선 판은 빈 키 줄을 큐에 남기고
// 재생 대상에도 뒀다: 빼면 그 판단을 조용히 버리는 것이라(설계 §9) 뺄 수 없었고, 두면
// `Replay` 가 매번 다시 보내 매번 400 을 받았다 — **헛 전송이 fd 호출마다 반복되고 큐
// 잔량이 영영 0 이 안 됐다**(doctor 가 "대기 N건"을 계속 찍어 사람은 판단이 안 나갔다고 읽는다).
//
// ★ **빈 키의 문제는 정확히 '별칭'이었다.** 빈 키끼리 서로 같아 보여서, 키로 매칭하는
// 모든 자리(병합·중복 검사)가 서로 다른 판단을 하나로 뭉갰다. 그래서 `settle` 이 그 줄을
// 절대 안 지웠고 `TallyRejected` 가 그 줄을 절대 안 접었다. **본문 해시 키는 그 별칭을
// 없앤다** — 서로 다른 판단은 서로 다른 키를 얻고, 같은 판단은 같은 키를 얻는다
// (`IdempotencyKey` 의 원래 계약 그대로: "같은 세션이 같은 본문을 두 번 쌓으면 한 건이다").
// 별칭이 사라지면 그 줄을 특별 취급할 이유가 통째로 없어지고, **그 판단은 원장으로 간다.**
//
// ★ 세션을 빈 문자열로 넘긴다. 빈 키 줄은 어느 세션의 것인지 모르고, `IdempotencyKey` 가
// 그 자리에 `nosession` 을 채운다 — 없는 세션을 지어내는 것보다 모른다고 적는 편이 맞다.
// 경로+본문을 해시하는 조합은 `Client.key`(client.go)가 온라인 경로에서 쓰는 것과 같다.
//
// ★ **파일을 여기서 안 고친다.** 되쓰기는 재생이 병합할 때 일어나고(settleLegacy → keep),
// 그때 채운 키가 파일에 앉는다. 읽기가 파일을 고치면 `List` 한 번에 디스크가 움직인다.
func fillMissingKeys(es []OutboxEntry) int {
	n := 0
	for i := range es {
		if strings.TrimSpace(es[i].Key) != "" {
			continue
		}
		es[i].Key = IdempotencyKey("", append([]byte(es[i].Path+"\n"), es[i].Body...))
		n++
	}
	return n
}

// readEntries 는 JSONL 대기열 파일 하나를 읽는다.
//
// ★ 깨진 줄을 **조용히 버리지 않는다.** 이 파일은 재생성 불가한 자산이므로
// 해석 실패는 **읽은 데까지와 함께** 오류로 올려 사람이 보게 한다(설계 §9).
func readEntries(path string) ([]OutboxEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("아웃박스 읽기 실패: %w", err)
	}
	defer f.Close()

	var out []OutboxEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // 판단 본문은 길 수 있다
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var e OutboxEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return out, fmt.Errorf("아웃박스 %d번째 줄을 해석하지 못했다: %w", line, err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("아웃박스 주사 실패: %w", err)
	}
	return out, nil
}

// settle 은 재생 한 번의 결과를 큐에 반영한다. 남은 건수를 낸다.
//
// ★ **스냅숏을 되쓰지 않는다.** 옛 구현은 재생을 시작할 때 뜬 스냅숏에서 처리한 것을 뺀
// `left` 를 통째로 되썼다. 그 사이 남이 Append 한 줄은 스냅숏에 없으므로 그 되쓰기에
// **삭제되고**(실측 33/300), 남이 이미 지운 줄은 스냅숏에 남아 있으므로 **되살아났다**
// (실측 300/300). 항목당 파일에서는 **되쓸 전체가 아예 없어서** 이 두 갈래가 원리적으로
// 사라진다 — 내가 만지는 것은 내가 처리한 키의 파일뿐이다. 옛 JSONL 쪽은 여전히 전량
// 재기록이라 거기서는 "다시 읽고 내 것만 빼는" 병합이 그대로 필요하다(settleLegacy).
//
// ★ Tries 는 **지금 파일값에 +1** 이다. 스냅숏값+1 로 쓰면 겹친 재생 둘이 같은 값에서
// 출발해 같은 값을 써서 시도 하나가 사라진다(실측 299/300). 시도는 가산이라야 맞다.
//
// ★ **자리가 둘이라 처리도 둘이다.** 항목당 파일 쪽은 내가 처리한 키의 파일만 지우면
// 끝이고 **잠금이 필요 없다** — 남의 파일을 건드릴 수 있는 연산이 없기 때문이다.
// 옛 JSONL 쪽은 전량 재기록이라 아래 병합과 잠금이 그대로 필요하다. **JSONL 이 없으면
// 그 경로를 아예 안 탄다** — 그래서 전환이 끝난 자리에서는 이 함수에 fail-open 이
// 원리적으로 안 난다.
//
// ★ 남은 건수는 **처리 뒤에 다시 센다.** 옛 판은 병합 안에서 셌는데, 이제 자리가 둘이라
// 한쪽만 보고 세면 틀린다. 세다 실패하면 스냅숏값을 낸다(0 을 내면 "다 나갔다"로 읽힌다).
func (o *Outbox) settle(done map[string]bool, bumpedKey string, snapshotLeft int) (int, error) {
	var firstErr error
	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// ── 항목당 파일 ──────────────────────────────────────────────────────────
	for key := range done {
		if key == "" {
			continue // 빈 키는 파일이 없다. 아래 JSONL 쪽에서 다룬다
		}
		// ★ ENOENT 는 오류가 아니다 — 겹친 재생이 이미 지웠다는 뜻이고, 그 판단은
		// 잃은 것이 아니라 이미 갔다. 옛 판에서 이 상황은 "되살아나는 줄"이었다.
		if err := os.Remove(o.entryPath(key)); err != nil && !os.IsNotExist(err) {
			keep(fmt.Errorf("보낸 판단의 자리를 못 치웠다(%s): %w", clip(key, 40), err))
		}
	}
	if bumpedKey != "" && !done[bumpedKey] {
		keep(o.bumpTries(bumpedKey))
	}

	// ── 옛 JSONL ─────────────────────────────────────────────────────────────
	if err := o.settleLegacy(done, bumpedKey); err != nil {
		keep(err)
	}

	cur, err := o.List()
	if err != nil {
		keep(err)
		return snapshotLeft, firstErr
	}
	return len(cur), firstErr
}

// bumpTries 는 항목 파일 하나의 시도 횟수를 1 올린다.
//
// ★ **이 자리에만 잠금이 남는다. 그리고 그것이 이 항목이 원래 하려던 것이다.**
// 처음에는 여기도 잠금 없이 두고 "겹치면 시도 하나를 덜 세는 정도"라고 적었다.
// **실측이 그것을 반증했다: 300라운드에서 268 이 Tries<2 였다.** 읽고-고쳐-쓰기는
// 거의 항상 진다 — 두 프로세스가 같은 값(0)을 읽고 같은 값(1)을 쓴다. 그대로 두면
// `TestConcurrentReplaysCountEveryTry` 가 지키던 계약이 통째로 깨지고, 그 계약이
// 지키는 것은 **영구히 실패할 줄이 큐를 얼마나 오래 막는가**다(maxReplayTries 는
// 상태코드가 못 가르는 것을 가르는 유일한 둘째 축이다).
//
// ★ **여기 잠금은 예산 문제를 안 되살린다.** 옛 판이 못 쓰게 된 이유는 잠금 자체가
// 아니라 **점유가 O(큐 크기)** 였던 것이다(파일 전량 읽기 + 전량 쓰기 = 1000건 17.9ms).
// 이 함수의 점유는 **파일 하나**라 O(1) 이고 큐 깊이와 무관하다 — 큐가 1000건이든
// 10건이든 같은 수십 µs 다. 그래서 세션이 몰려도 예산 250ms 를 못 채울 이유가 없다.
// 항목 본문이 말한 "잠금 구간을 O(1) 로 만든다"의 정확한 실현이 이 자리다.
//
// ★ 파일이 없으면 아무것도 안 한다 — 남이 이미 보냈거나 격리했다는 뜻이다.
func (o *Outbox) bumpTries(key string) error {
	locked, err := withQueueLock(o.dir, queueLockBudget, func() error { return o.bumpTriesLocked(key) })
	if locked {
		return err
	}
	o.warn("큐 잠금 없이 시도 횟수를 올린다 — 겹친 재생이 있으면 시도 하나가 사라진다",
		"dir", o.dir, "supported", queueLockSupported, "error", errText(err))
	o.recordFailOpen("bump", queueLockSupported, err)
	return o.bumpTriesLocked(key)
}

func (o *Outbox) bumpTriesLocked(key string) error {
	path := o.entryPath(key)
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("시도 횟수를 못 읽었다(%s): %w", clip(key, 40), err)
	}
	var e OutboxEntry
	if err := json.Unmarshal(b, &e); err != nil {
		return fmt.Errorf("시도 횟수를 올릴 항목을 해석하지 못했다(%s): %w", clip(key, 40), err)
	}
	e.Tries++
	buf, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("시도 횟수 직렬화 실패(%s): %w", clip(key, 40), err)
	}
	// ★ 제자리 O_TRUNC 로 쓰지 마라 — 쓰는 도중 남의 List 가 반쪽을 읽는다.
	//   Append 와 같은 이유이고, 여기서는 **덮어쓰기가 맞으므로** Link 가 아니라 Rename 이다.
	tmp := path + "." + tmpNonce() + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("시도 횟수 기록 실패(%s): %w", clip(key, 40), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("시도 횟수 교체 실패(%s): %w", clip(key, 40), err)
	}
	return nil
}

// settleLegacy 는 옛 `pending.jsonl` 하나에 재생 결과를 반영한다.
//
// ★ **파일이 없으면 잠금도 안 잡는다.** 전환이 끝난 자리에서 이 함수는 곧장 돌아간다.
func (o *Outbox) settleLegacy(done map[string]bool, bumpedKey string) error {
	if _, err := os.Stat(o.pendingPath()); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("옛 큐 파일을 못 봤다: %w", err)
	}
	merge := func() error {
		// ★ `o.List()` 를 쓰면 **항목당 파일까지 JSONL 로 되쓴다** — 같은 판단이 두 자리에
		// 생기고, 그 뒤 항목 파일이 지워져도 JSONL 쪽 사본이 남아 영원히 재생된다.
		// 이 함수가 되쓰는 것은 자기 파일뿐이다.
		cur, err := readEntries(o.pendingPath())
		if err != nil {
			return err
		}
		// ★ **읽자마자 채운다. `List` 와 같은 규칙이어야 한다** — 여기서 안 채우면 done 이
		// 들고 온 (채워진) 키와 이 파일의 (빈) 키가 안 맞아 보낸 줄이 안 지워지고, 그 줄은
		// 다음 재생에서 다시 나간다. 두 자리가 같은 키를 봐야 병합이 성립한다.
		//
		// ★ 채운 뒤 `keep` 이 되쓰므로 **파일이 이 시점에 갱신된다** — 그다음부터 이 자리는
		// 아무것도 안 채운다. 읽기가 아니라 병합이 고치는 이유가 그것이다.
		fillMissingKeys(cur)
		out := make([]OutboxEntry, 0, len(cur))
		for _, e := range cur {
			// ★ 앞선 판은 여기에 **빈 키 줄 특별 취급**이 있었다: 지우지도 빼지도 않고
			// 큐에 남기고 경고를 냈다. 빈 키끼리 서로 별칭이라 키로 매칭하면 하나가
			// 배달될 때 나머지가 함께 사라지기 때문이었다. **fillMissingKeys 가 그 별칭을
			// 없애서 특별 취급할 이유가 사라졌다** — 이제 빈 키 줄은 자기 키를 갖고 아래
			// 일반 경로로 간다. 그 줄이 겪던 무한 반복 전송도 함께 끝난다.
			if done[e.Key] {
				continue // 보냈거나 격리했다
			}
			if bumpedKey != "" && e.Key == bumpedKey {
				e.Tries++
			}
			out = append(out, e)
		}
		return o.keep(out)
	}
	locked, err := withQueueLock(o.dir, queueLockBudget, merge)
	if locked {
		return err
	}
	// 잠금을 못 잡아도 **병합으로** 처리한다. 스냅숏 되쓰기로 되돌아가지 않는다 —
	// 잠금 없이도 다시 읽고 빼는 쪽이 창을 훨씬 좁힌다(오늘보다 나쁘지 않다).
	o.warn("큐 잠금 없이 재생 결과를 반영한다 — 겹친 재생이 있으면 판단이 사라질 수 있다",
		"dir", o.dir, "supported", queueLockSupported, "error", errText(err))
	o.recordFailOpen("settle", queueLockSupported, err)
	return merge()
}

// errText 는 nil 오류를 빈 문자열로 낸다. 로그 인자에서 <nil> 을 안 보이게 한다.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// tmpNonce 는 임시 파일 이름에 붙일 조각이다. **pid 만으로는 부족하다** —
// 한 프로세스 안 두 고루틴도 같은 tmp 를 다툴 수 있고, 프레임 루프를 병렬화하는 날
// pid-only 판은 그 자리에서 조용히 무력해진다.
//
// 난수를 못 얻으면 나노초로 대신한다(FreshKey 가 같은 자리에서 같은 선택을 한다).
func tmpNonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d-%x", os.Getpid(), time.Now().UnixNano())
	}
	return fmt.Sprintf("%d-%s", os.Getpid(), hex.EncodeToString(b[:]))
}

// keep 은 **옛 `pending.jsonl` 에** 남길 항목만 다시 쓴다(원자 교체).
//
// ★ **새 쓰기는 이 함수를 안 탄다.** 남은 호출자는 settleLegacy 하나이고, 그것은 옛 파일이
// 실제로 있을 때만 돈다. 그래서 이 함수는 **전환이 끝나면 아무도 안 부르는 코드**다 —
// 지우지 않는 이유는 실물 머신에 아직 그 파일이 남아 있을 수 있어서이지, 새 경로가
// 이것을 쓰기 때문이 아니다. 여기에 새 기능을 얹지 마라.
//
// ★ 비면 파일을 **지운다.** 그래야 다음 `List` 가 옛 형식을 아예 안 보고, `Append` 의
// 옛 형식 중복 검사도 빈 읽기로 끝난다 — 전환이 스스로 완결되는 자리가 여기다.
func (o *Outbox) keep(entries []OutboxEntry) error {
	if len(entries) == 0 {
		if err := os.Remove(o.pendingPath()); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("아웃박스 비우기 실패: %w", err)
		}
		return nil
	}
	var b strings.Builder
	for _, e := range entries {
		buf, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("아웃박스 직렬화 실패: %w", err)
		}
		b.Write(buf)
		b.WriteByte('\n')
	}
	// ★ tmp 이름은 **프로세스마다 다르다.** 고정 이름(`pending.jsonl.tmp`)이면 잠금을
	// 못 잡아 떨어진 갈래 둘이 같은 tmp 에 O_TRUNC 로 쓰고, 그러면 서로의 바이트가
	// 섞인 채 rename 된다. 잠금이 있으면 거의 안 닿는 자리지만, fail-open 갈래가
	// 남아 있는 한 그 자리는 열려 있다.
	tmp := fmt.Sprintf("%s.%s.tmp", o.pendingPath(), tmpNonce())
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		// ★ 여기도 치운다. os.WriteFile 은 O_CREATE|O_TRUNC 로 **먼저 만들고** 쓰므로
		// ENOSPC·EDQUOT·EIO 로 실패해도 파일은 남는다. 이름이 유일해진 뒤로는 다음
		// 호출이 같은 이름을 재사용해 덮어 주지 않으니, 실패할 때마다 잔해가 하나씩 쌓인다.
		os.Remove(tmp)
		return fmt.Errorf("아웃박스 기록 실패: %w", err)
	}
	if err := os.Rename(tmp, o.pendingPath()); err != nil {
		os.Remove(tmp) // 이름이 유일해졌으니 실패한 tmp 는 아무도 안 치운다
		return fmt.Errorf("아웃박스 교체 실패: %w", err)
	}
	return nil
}

// ReplayResult 는 재생 한 번의 결과다. 건수와 **왜 남았는지**를 함께 낸다.
type ReplayResult struct {
	Rejected  int // 영구 거절로 판정해 격리한 건수
	Sent      int
	Remaining int
	Detail    string
}

// Replay 는 쌓인 것을 순서대로 보낸다.
//
// 성공한 것만 지운다. **미도달이면 그 자리에서 멈춘다** — 뒤엣것을 계속 시도하면
// 순서가 뒤집히고(판단은 시간축이 의미다), 매번 전량 재시도로 서버가 살아난 순간
// 실패 폭풍이 난다.
func (o *Outbox) Replay(ctx context.Context, send func(context.Context, OutboxEntry) error) (ReplayResult, error) {
	entries, err := o.List()
	if err != nil {
		return ReplayResult{}, err
	}
	if len(entries) == 0 {
		return ReplayResult{Detail: "대기 중인 판단이 없다"}, nil
	}
	sent, rejected := 0, 0
	stopReason, rejectReason := "", ""
	left := []OutboxEntry(nil)
	// done 은 **내가 실제로 처리한** 키다(보냈거나 격리했다). 아래 병합이 이것만 뺀다.
	// bumpedKey 는 시도 횟수를 올려야 하는 키다 — 멈추는 자리는 하나뿐이라 한 개다.
	done := map[string]bool{}
	bumpedKey := ""
	for i, e := range entries {
		err := send(ctx, e)
		if err == nil {
			sent++
			done[e.Key] = true
			continue
		}
		v := JudgeReplayFailure(err, e.Tries)
		if v.Permanent {
			// ★ 격리하고 **계속 간다.** 여기서 멈추면 영원히 실패할 줄 하나가 뒤엣것을
			// 통째로 인질로 잡는다 — 실측된 그 상태가 정확히 이것이었다.
			// 순서 보증은 깨지지 않는다: 보낸 것들 사이의 순서는 그대로이고,
			// 격리된 줄은 애초에 안 보낸다.
			e.Tries++
			if qerr := o.quarantine(RejectedEntry{Entry: e, Reason: v.Reason, At: o.stamp()}); qerr != nil {
				// 격리에 실패하면 **버리지 않는다.** 큐에 남겨 두는 쪽이 잃는 것보다 낫다.
				stopReason = fmt.Sprintf("%d번째(%s)를 격리하지 못했다: %v", i+1, clip(e.Key, 40), qerr)
				left = append(left, entries[i:]...)
				break
			}
			rejected++
			done[e.Key] = true
			if rejectReason == "" {
				rejectReason = v.Reason
			}
			continue
		}
		// 일시 실패는 **그 자리에서 멈춘다** — 뒤엣것을 계속 시도하면 순서가 뒤집히고
		// (판단은 시간축이 의미다) 매번 전량 재시도로 실패 폭풍이 난다.
		//
		// ★ 미도달은 **세지 않는다.** 못 보낸 것은 그 줄에 대한 정보가 아니다
		// (JudgeReplayFailure 의 같은 주석). 세면 오프라인이 길다는 이유만으로 격리된다.
		if !Unreachable(err, 0) {
			e.Tries++
			bumpedKey = e.Key
		}
		left = append(left, e)
		left = append(left, entries[i+1:]...)
		stopReason = fmt.Sprintf("%d번째(%s)에서 멈췄다(%d회째): %v",
			i+1, clip(e.Key, 40), e.Tries, err)
		break
	}
	remaining, err := o.settle(done, bumpedKey, len(left))
	if err != nil {
		return ReplayResult{Sent: sent, Remaining: len(left), Rejected: rejected}, err
	}
	res := ReplayResult{Sent: sent, Remaining: remaining, Rejected: rejected}
	switch {
	case res.Remaining == 0 && res.Rejected == 0:
		res.Detail = fmt.Sprintf("판단 %d건을 재생했다", sent)
	case res.Remaining == 0:
		res.Detail = fmt.Sprintf("판단 %d건 재생 · %d건은 영구 거절이라 격리했다(%s) — %s",
			sent, res.Rejected, o.rejectedPath(), rejectReason)
	default:
		// ★ 사유가 빌 수 있다. Remaining 은 이제 **병합 뒤 파일 기준**이라, 내가 멈춰서가
		// 아니라 **재생 도중 남이 쌓아서** 남는 갈래가 생겼다(옛 구현에는 없던 갈래다:
		// 거기서는 Remaining>0 이 곧 break 였다). 그때 stopReason 은 비어 있고, 그대로
		// 두면 "…남았다 — " 로 대시만 남는다. ReplayResult 는 건수와 **왜 남았는지**를
		// 함께 내겠다고 스스로 적어 둔 타입이므로 그 계약을 빈 문자열로 깨면 안 된다.
		why := stopReason
		if why == "" {
			why = "재생 중에 새로 쌓였다 — 다음 재생이 보낸다"
		}
		res.Detail = fmt.Sprintf("판단 %d건 재생 · %d건 격리 · %d건 남았다 — %s",
			sent, res.Rejected, res.Remaining, why)
	}
	return res, nil
}

// rejectedFileName 은 격리 **사건** 하나가 갖는 파일 이름이다. 순수 함수다.
//
// ★★ **판별자는 키가 아니다.** 앞선 판단(dbaf719)은 이 자리에 `rejected/<key>.json` 을
// 후속으로 제안했는데, 그것은 **그 판단이 스스로 거절한 사유에 그대로 걸린다**:
// "키는 판단의 정체성이지 **거절 사건의 정체성이 아니다** — 같은 키가 400('항목이 잠겨
// 있다') 뒤 404('항목이 사라졌다')로 재격리되는 경로가 열려 있고(`Append` 는 pending 만
// 보고 격리 이력을 안 본다), 키로 접으면 **나중 사실이 사라진다**." 키로 이름을 지으면
// 그 둘째 사건이 EEXIST 로 조용히 없어진다 — 오늘 O_APPEND 판은 두 줄로 남기는 정보다.
//
// ★ 그래서 판별자는 **격리된 항목 전체 + 사유**다. 격리 시각(`RejectedEntry.At`)만 뺀다 —
// 그것이 겹친 쌍둥이를 가르는 유일한 축이고(실측 격차 0.286ms), 정체성이 아니라 관측 시각이다.
// 같은 판단이 같은 사유로 같은 시도 횟수에서 거절된 사건은 **하나**이고, 사유나 시도가
// 다르면 **다른 사건**이다. dbaf719 의 실측이 그대로 근거다: 경합 쌍둥이는
// 사유 296/296 동일 · Tries 296/296 동일.
//
// ★ **본문까지 넣는 이유는 빈 키다.** 키 없는 줄은 서로 별칭이고 재생마다 다시 격리된다.
// 판별자에 본문이 없으면 서로 다른 빈 키 판단들이 같은 사유를 받았을 때 **한 파일로 접혀
// 사라진다** — 세는 자리를 고치려다 판단을 잃는 것이고, 그것이 §9 위반이다.
func rejectedFileName(r RejectedEntry) (string, error) {
	buf, err := json.Marshal(struct {
		Entry  OutboxEntry `json:"entry"`
		Reason string      `json:"reason"`
	}{Entry: r.Entry, Reason: r.Reason})
	if err != nil {
		return "", fmt.Errorf("격리 판별자 직렬화 실패: %w", err)
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:16]) + ".json", nil
}

// quarantine 은 영구 거절된 줄을 격리 자리로 옮긴다. **추가 전용이다.**
//
// ★ **같은 사건은 한 번만 들어간다.** 겹친 재생 둘이 같은 판단을 같은 사유로 거절하면
// 같은 이름을 얻고 둘째는 EEXIST 로 끝난다 — 그 중복이 이 자리의 축이었고, 잠금으로는
// 원리적으로 못 닫혔다(위 rejectedDirName 주석). **잠그지 않는 이유**도 같다: 잠글 것이
// 없다. 그리고 잠그면 `Append` 와 같은 `.lock` 을 O(격리 파일)만큼 점유해 **유실 확률과
// 중복을 맞바꾸는 거래**가 된다(그 파일은 비우는 경로가 없어 계속 자란다).
//
// ★ tmp + Link 인 이유는 Append 와 같다 — 이름만 원자적이면 남이 반쪽을 읽는다.
func (o *Outbox) quarantine(r RejectedEntry) error {
	name, err := rejectedFileName(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(o.rejectedDir(), 0o755); err != nil {
		return fmt.Errorf("격리 디렉토리 생성 실패: %w", err)
	}
	buf, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("격리 직렬화 실패: %w", err)
	}
	tmp := filepath.Join(o.rejectedDir(), ".tmp-"+tmpNonce())
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("격리 기록 실패: %w", err)
	}
	defer os.Remove(tmp)
	if err := os.Link(tmp, filepath.Join(o.rejectedDir(), name)); err != nil {
		if os.IsExist(err) {
			return nil // 같은 사건이 이미 있다. 겹친 재생이 만드는 그 중복이다
		}
		return fmt.Errorf("격리 자리를 못 잡았다(%s): %w", name, err)
	}
	return nil
}

// Rejected 는 격리된 것 전부다. 자리가 없으면 빈 목록이다(오류가 아니다).
//
// ★ 큐와 같이 **두 형식을 다 읽는다** — 사건당 파일(`rejected/`)과 옛 `rejected.jsonl`.
// 다만 큐와 달리 **옛 파일이 저절로 사라지지 않는다**: 격리는 추가 전용이고 비우는 경로가
// 코드에 없다(후속 `fd-rejected-and-failopen-files-have-no-retention-path`). 그래서 옛
// 줄은 그 자리에 영원히 남고 이 함수가 계속 둘을 합쳐 낸다. 그 사실을 숨기지 않는다.
//
// ★ 격리 시각 오름차순 **안정** 정렬이다. doctor 가 이 목록을 그대로 찍으므로 순서가
// 흔들리면 같은 상태가 실행마다 다르게 보인다.
func (o *Outbox) Rejected() ([]RejectedEntry, error) {
	out, err := readRejectedDir(o.rejectedDir())
	legacy, lerr := readRejected(o.rejectedPath())
	out = append(out, legacy...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	if err != nil {
		return out, err
	}
	return out, lerr
}

// readRejectedDir 는 사건당 파일 격리 자리 하나를 읽는다. 디렉토리가 없으면 빈 목록이다.
//
// ★ 깨진 파일은 **조용히 안 버린다**(설계 §9). readEntryDir·readEntries 와 같은 규율이다.
func readRejectedDir(dir string) ([]RejectedEntry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("격리 디렉토리를 못 읽었다(%s): %w", dir, err)
	}
	out := make([]RejectedEntry, 0, len(des))
	for _, de := range des {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, de.Name()))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return out, fmt.Errorf("격리 항목 %s 를 못 읽었다: %w", de.Name(), err)
		}
		var r RejectedEntry
		if err := json.Unmarshal(b, &r); err != nil {
			return out, fmt.Errorf("격리 항목 %s 를 해석하지 못했다: %w", de.Name(), err)
		}
		out = append(out, r)
	}
	return out, nil
}

// RejectedTally 는 격리 파일 하나를 사람이 읽을 수 있는 수 셋으로 접는다.
//
// ★ **줄 수와 판단 수는 다른 수다.** 겹친 재생 둘은 잠금 밖에서 각자 스냅숏을 뜨고
// (Replay 의 첫 List) 각자 send 해서 각자 영구 거절 판정을 받는다. 그래서 격리는 두 번
// **결정**되고, 그 뒤에 무엇을 잠가도 append 는 두 번 난다 — 재현 실측(300라운드×6판):
// 오늘 286~299/300 중복, quarantine 을 잠금 안에 넣어도 284~296/300 으로 분포가 겹친다.
// 12판 전부에서 오차 없이 성립한 항등식이 이유를 말한다: **격리 줄 수 = 4xx 를 받은
// send 횟수.** 잠금은 순서만 정하고 개수를 안 바꾼다.
//
// 그래서 이 축의 피해는 유실이 아니라 "세는 수가 부푼다"이고, 처방이 여기 있다.
type RejectedTally struct {
	// Lines 는 파일에 있는 그대로다. **이것이 진실이고 아래 둘은 요약이다.**
	Lines int
	// Judgments 는 고유 키 수다. 키는 판단의 정체성이고(IdempotencyKey 는 세션+본문 해시라
	// 시각이 안 들어간다) **거절 사건의 정체성이 아니다** — 같은 판단이 400 으로 격리된 뒤
	// 다시 쌓여 404 로 또 거절될 수 있고(appendLocked 는 pending 만 보고 격리는 안 본다),
	// 그 둘은 서로 다른 사건이다. 그래서 이 수는 줄을 **대체하지 않고 나란히 선다.**
	Judgments int
	// Keyless 는 키가 빈 줄 수다. **하나도 안 접는다.**
	//
	// ★ settle 이 같은 이유로 빈 키 줄을 큐에서 안 지운다: 빈 키끼리 서로 별칭이라
	// 접으면 서로 다른 판단들이 한 건으로 뭉개진다. 세는 자리에서 그 방어를 되돌리면
	// doctor 가 **아래로** 거짓말한다 — 부푸는 것보다 나쁜 방향이다(설계 §9).
	Keyless int
}

// TallyRejected 는 격리 줄을 센다. 순수 함수다.
//
// ★ 판별자는 **파일 전체에 걸친 키 집합**이다. 두 가지 더 싼 판별자가 틀렸고, 둘 다
// 변이 실험에서 이 패키지를 통째로 초록으로 통과했다:
//   - **사유로 접기** — JudgeReplayFailure 의 4xx 가지는 사유를 상태코드와 서버 메시지로만
//     짓는다. 키도 경로도 안 들어가므로 **서로 다른 두 판단이 같은 400 을 받으면 사유가
//     바이트 동일**하고, 접으면 재생성 불가한 판단 둘이 한 건으로 보고된다.
//   - **이웃만 접기** — 실제 배치 경합이 만드는 파일의 다수가 흩어져 있다(2건 큐·300라운드
//     실측에서 `A B A B` 가 142~199/300). 이웃만 보면 그 다수에 안 닿는다.
func TallyRejected(rs []RejectedEntry) RejectedTally {
	t := RejectedTally{Lines: len(rs)}
	seen := make(map[string]bool, len(rs))
	for _, r := range rs {
		if r.Entry.Key == "" {
			t.Keyless++
			continue
		}
		if !seen[r.Entry.Key] {
			seen[r.Entry.Key] = true
			t.Judgments++
		}
	}
	return t
}

// Retention 은 **비우는 경로가 없는 자리들**의 크기다. 바이트다.
//
// ★★ 왜 이 수를 내는가. 격리와 fail-open 기록은 둘 다 **추가 전용이고 지우는 코드가
// 없다**(실측: `rejectedPath`·`failOpenPath` 에 닿는 `os.Remove`·`O_TRUNC` 0건).
// 그것은 설계이지 결함이 아니다 — 격리는 재생성 불가한 판단을 담고, fail-open 기록은
// 지우면 세려던 것을 지운다. **다만 상한이 없는 자리는 언젠가 문제가 된다.**
//
// ★ **회전도 상한도 안 만든다.** 이 머신 실측(2026-08-11): 고정 자리에는 아웃박스
// 디렉토리 자체가 없고, 옛 자리의 격리가 577바이트·1건이며 fail-open 기록은 없다.
// **압력이 실물로 관측된 적이 없다.** 근거 없이 회전을 만들면 "어느 시점 이후를 못 본다"는
// 새 구멍이 열리고, 그것은 이 저장소가 없애려는 종류의 침묵이다.
// 그래서 하는 것은 **그 사실을 화면에 두는 것**뿐이다 — 언젠가 커졌을 때 그 수가 거기
// 있고, 그때 근거를 갖고 판정한다(안 잰 축을 잰 척하지 않는 것과 같은 어법이다).
//
// ★ 격리는 두 형식을 **합쳐서** 잰다. 사건당 파일로 옮겼어도 옛 `rejected.jsonl` 은
// 비우는 경로가 없어 그 자리에 남는다 — 한쪽만 재면 그 잔량이 화면에서 사라진다.
type Retention struct {
	Rejected int64 // rejected.jsonl + rejected/ 전부
	FailOpen int64 // failopen.jsonl
	Err      string
}

// Retention 은 위 수를 잰다. **읽기만 한다.**
func (o *Outbox) Retention() Retention {
	var r Retention
	note := func(err error) {
		if err != nil && !os.IsNotExist(err) {
			r.Err = strings.TrimSpace(r.Err + " " + err.Error())
		}
	}
	if fi, err := os.Stat(o.rejectedPath()); err == nil {
		r.Rejected += fi.Size()
	} else {
		note(err)
	}
	n, err := dirBytes(o.rejectedDir())
	note(err)
	r.Rejected += n
	if fi, err := os.Stat(o.failOpenPath()); err == nil {
		r.FailOpen = fi.Size()
	} else {
		note(err)
	}
	return r
}

// dirBytes 는 디렉토리 하나에 든 **일반 파일**의 바이트 합이다. 없으면 0 이다.
//
// ★ 재귀하지 않는다 — 이 자리들은 평평하고, 재귀하면 남이 실수로 둔 것까지 이 수에 섞인다.
func dirBytes(dir string) (int64, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var n int64
	for _, de := range des {
		if de.IsDir() {
			continue
		}
		fi, err := de.Info()
		if err != nil {
			continue // 방금 사라졌다. 크기를 재는 자리에서 그것은 오류가 아니다
		}
		n += fi.Size()
	}
	return n, nil
}

// Leftover 는 옛 자리 하나에 아직 남아 있는 것이다.
type Leftover struct {
	Dir      string
	Pending  int // 대기열 줄 수
	Rejected int // 격리 줄 수 — 이것은 안 비워진다(보관소는 제 큐 옆에 남는다)
	// RejectedJudgments 는 그 줄들이 담은 **고유 판단 수**다(TallyRejected 참조).
	//
	// ★ Rejected 를 이 값으로 **갈아 끼우지 마라.** LegacyLeftovers 가 이 자리를 화면에
	// 낼지 말지를 `lo.Rejected == 0` 으로 정하는데, 거기를 고유 수로 바꾸면 키 없는 줄만
	// 든 옛 자리가 고유 0 이라 **디렉토리째 화면에서 사라진다** — 조용히 버리는 것이
	// 하나도 없어야 한다(설계 §9). 줄 수가 존재 판정이고 판단 수는 요약이다.
	RejectedJudgments int
	// RejectedBytes 는 그 자리의 격리가 차지한 바이트다.
	//
	// ★★ **이 필드가 없어서 화면이 어긋났다.** 「보관 자리」 줄은 `Outbox.Retention()` 을
	// 쓰는데 그것은 **고정 자리만** 잰다. 그래서 옛 채널 자리에 격리가 남아 있어도
	// `격리 0바이트` 로 나오고, **바로 아래 줄이 같은 자리를 「격리 기록 1건」이라고 말한다**
	// (실측 2026-08-11 0.16.0: 그 1건이 577바이트였다). 사람은 두 줄 중 무엇이 참인지 골라야 했다.
	//
	// ★ 합산하지 않고 **자리별로** 낸다. 이 자리들은 채널마다 갈려 있던 시절의 잔재라
	// 개수가 머신마다 다르고, 합치면 "어디를 지워야 하나"가 화면에서 사라진다 —
	// 보관소는 제 큐 옆에 남는 것이 설계이므로(§7) 그 자리 이름이 곧 처방이다.
	RejectedBytes int64
	Err           string // 셀 수 없었으면 그 사유. 비어 있을 수 있다
}

// leftover 는 이 큐에 남은 것을 **읽기만 해서** 센다.
//
// ★ 보내지 않는다. 진단이 부작용을 가지면 "찍어 봤더니 상태가 달라졌다"가 되고,
// 그러면 진단을 믿을 수 없다. 재생은 Flush 경로에서만 돈다.
func (o *Outbox) leftover() Leftover {
	lo := Leftover{Dir: o.dir}
	if es, err := o.List(); err != nil {
		lo.Err = err.Error()
	} else {
		lo.Pending = len(es)
	}
	if rs, err := o.Rejected(); err != nil {
		lo.Err = strings.TrimSpace(lo.Err + " " + err.Error())
	} else {
		tal := TallyRejected(rs)
		lo.Rejected, lo.RejectedJudgments = tal.Lines, tal.Judgments
	}
	// ★ 크기는 **같은 Retention 함수**로 잰다. 여기서 따로 세면 두 화면이 같은 파일에
	//   다른 수를 말하게 되고, 그 어긋남이 정확히 이 항목이 고치는 것이다.
	ret := o.Retention()
	lo.RejectedBytes = ret.Rejected
	if ret.Err != "" {
		lo.Err = strings.TrimSpace(lo.Err + " " + ret.Err)
	}
	return lo
}

// readRejected 는 격리 파일 하나를 읽는다. doctor 의 잔량 합산도 이것을 쓴다.
func readRejected(path string) ([]RejectedEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("격리 파일 읽기 실패: %w", err)
	}
	var out []RejectedEntry
	for i, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r RejectedEntry
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			// ★ i+1 이다. 같은 파일의 readOutbox 는 line++ 로 이미 1-based 였고
			// 여기만 range 인덱스를 그대로 실어, **한 파일 안에 두 규약이 공존했다.**
			return out, fmt.Errorf("격리 %d번째 줄 해석 실패: %w", i+1, err)
		}
		out = append(out, r)
	}
	return out, nil
}
