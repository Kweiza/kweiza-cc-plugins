package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// 열화(L1) — 서버 미도달일 때 명령마다 무엇을 하는가.
//
// ★ 이 파일의 판정은 전부 순수 함수다. 본문에 흩어지면 시험이 사본을 단정하게 되고,
// 그러면 "선점은 오프라인에서 안 된다"는 이 설계의 불변식이 조용히 샌다(설계 §7).

// OfflineMode 는 서버 미도달일 때 그 명령이 실제로 무엇을 하는가다.
//
// 넷을 가른다. 뭉개면 "캐시를 냈다"와 "쌓아 뒀다"와 "그냥 버렸다"가 구분되지 않아
// 세션이 안 된 일을 된 줄 안다.
type OfflineMode string

const (
	// OfflineCache — 마지막 성공 응답을 **낡음 배너와 함께** 낸다. 침묵하지 않는다.
	OfflineCache OfflineMode = "cache"
	// OfflineOutbox — 아웃박스에 쌓고 종료코드 0. 재연결 시 멱등 재생한다.
	OfflineOutbox OfflineMode = "outbox"
	// OfflineDrop — 버린다. 다시 만들면 되는 사실이라 재생 기구를 만들지 않는다.
	OfflineDrop OfflineMode = "drop"
	// OfflineRefuse — 거절한다. 배타는 서버만 보장할 수 있다.
	OfflineRefuse OfflineMode = "refuse"
)

// OfflineVerdict 는 명령 하나의 열화 판정이다.
//
// 불리언이 아니라 **사유**를 담는다. "안 된다"만 알면 기다리면 되는 것인지
// 영영 안 되는 것인지 구분되지 않고, 그러면 세션이 같은 호출을 반복한다.
type OfflineVerdict struct {
	Mode   OfflineMode
	Reason string // 항상 채운다
}

// 랜딩 레인의 열화 명령 이름 — **리터럴로 흩뿌리지 않는다.**
//
// ★ JudgeOffline 은 표 밖 명령을 무조건 거절하므로 한 글자만 어긋나면 그 가지가 통째로
// 죽는다. 그런데 그 죽음은 조용하다: 거절 사유가 "정책이 정의돼 있지 않다"로 바뀔 뿐이라
// 서버가 살아 있는 동안에는 아무도 못 본다. 상수로 두면 오타가 컴파일 오류가 된다.
//
// ★ 값이 service.RefusedError 의 What 와 **글자 그대로 같은 것은 우연이 아니다.**
// mcpbackend.apiError 가 서버 문구에서 "<what> 거절: " 접두를 떼어 이중 표기를 막는데,
// 그 접두는 service 가 이 이름으로 조립한 것이다. 여기서 이름을 바꾸면 접두가 안 떼여
// 도구 응답이 "land report 거절: land report 거절: …" 가 된다.
const (
	// CmdLandAcquire 는 줄 서기 · 내 자리 재확인이다(mode=acquire). fd lane wait 의
	// 취득도 이 이름으로 온다 — wait 전용 쓰기 명령은 없다(조회는 ReadFresh 라 이 표 밖이다).
	CmdLandAcquire = "land"
	// CmdLandReport 는 보고+반납이다(mode=report).
	CmdLandReport = "land report"
	// CmdLandLeave 는 줄에서 스스로 빠지는 것이다(mode=leave).
	CmdLandLeave = "land leave"
	// CmdLaneRelease 는 사람의 회수다.
	CmdLaneRelease = "lane release"
	// CmdClaimRelease 는 사람이 죽은 세션의 선점을 회수하는 것이다.
	CmdClaimRelease = "claim release"
	// CmdProjectRemove 는 잔해 프로젝트를 지우는 것이다(`fd project rm`).
	//
	// ★ 값이 cmd/fd/project.go 의 a.cli.Write 호출부와 글자 그대로 같아야 한다 — 어긋나면
	// 아래 JudgeOffline·outbox.go 의 IdempotencyStable 이 이 상수로 잡아 둔 갈래를 안 타고
	// default(최종 리뷰 Important-3)로 조용히 떨어진다.
	CmdProjectRemove = "project-remove"
	// CmdMove 는 항목을 다른 프로젝트로 옮기는 것이다(`fd move`).
	//
	// ★ 값이 cmd/fd/cmds.go 의 runMove 안 a.cli.Write 호출부와 글자 그대로 같아야 한다 —
	// CmdProjectRemove 와 같은 이유다. write_cmd_table_coverage_test.go 가 cmd/fd 전체를
	// go/ast 로 훑어 이 어긋남을 기계로 잡는다(2026-08-11, project-remove 하나만 표에
	// 명시였던 자리에서 move·after cut·prescriptions 셋이 더 빠져 있던 것을 그 시험이
	// 처음으로 드러냈다).
	CmdMove = "move"
	// CmdAfterCut 는 걸린 선행 하나를 끊는 것이다(`fd after cut`).
	//
	// ★ 값이 cmd/fd/cmds.go 의 runAfterCut 안 a.cli.Write 호출부와 글자 그대로 같아야
	// 한다 — 위 CmdMove 와 같은 이유·같은 시험이 지킨다.
	CmdAfterCut = "after cut"
	// CmdLabel 은 이미 있는 항목의 꼬리표를 고치는 것이다(`fd label`).
	//
	// ★ 리터럴 대신 상수를 쓰는 이유는 CmdMove 와 같다 — offline.go·outbox.go 가
	// 이 이름으로 명시 갈래를 잡고, write_cmd_table_coverage_test.go 가 그것을
	// 기계로 지킨다.
	CmdLabel = "label"
	// CmdPrescriptions 는 Stop 훅이 이번 턴의 처방을 받아 오는 내부 호출이다 — 사람이
	// 셸에 치는 서브명령이 아니라 cmd/fd/hook.go 의 hookStop 만 부른다.
	//
	// ★ 값이 hookStop 안 a.cli.Write 호출부와 글자 그대로 같아야 한다 — 위 CmdMove 와
	// 같은 이유·같은 시험이 지킨다.
	CmdPrescriptions = "prescriptions"
)

// JudgeOffline 은 서버 미도달일 때 이 명령을 어떻게 처리할지 정한다. 순수 함수다.
//
// 축은 하나다: **잃으면 다시 만들 수 없는가.**
//   - 판단·노트 → 재생성 불가 → 아웃박스(설계 §7 "아웃박스를 판단·노트로만 좁힌 이유")
//   - 읽기 → 낡아도 값이 있다 → 캐시 + 배너
//   - 신호 → 다음 훅이 다시 만든다 → 버린다
//   - 선점 → **배타는 서버만 보장할 수 있다** → 거절. 오프라인 획득을 허용하면 배타가 거짓이 된다
//
// 표에 없는 명령은 **거절한다.** 기본값을 캐시나 아웃박스로 두면 새 명령이 생길 때마다
// 아무도 정하지 않은 열화 정책이 조용히 붙는다.
func JudgeOffline(cmd string) OfflineVerdict {
	switch strings.TrimSpace(cmd) {
	case "note":
		return OfflineVerdict{OfflineOutbox,
			"판단은 원리적으로 파생 불가한 유일한 자산이다 — 아웃박스에 쌓고 재연결 시 멱등 재생한다"}
	case "status", "board", "next", "doctor":
		return OfflineVerdict{OfflineCache,
			"읽기다 — 마지막 성공 응답을 낡음 배너와 함께 낸다. 침묵하면 낡은 값이 현재 사실인 척한다"}
	case "beat":
		return OfflineVerdict{OfflineDrop,
			"신호는 다음 훅이 다시 만든다 — 잃어도 다시 만들면 되는 것에 재생 기구를 만들지 않는다"}
	case "pick", "claim":
		return OfflineVerdict{OfflineRefuse,
			"선점은 오프라인에서 안 된다 — 배타는 서버만 보장할 수 있고, " +
				"오프라인 획득을 허용하면 배타가 거짓이 된다"}
	case "open":
		return OfflineVerdict{OfflineCache,
			"세션 열기는 서버 발급 id 가 필요하다 — 캐시된 마지막 세션으로 배너만 내고 등록은 재연결 때 한다"}
	case "add":
		return OfflineVerdict{OfflineRefuse,
			"항목 id 는 전역 유일해야 하고 그 유일성은 서버만 보장한다 — " +
				"오프라인에서 만들면 두 세션이 같은 브랜치 이름을 쓴다"}
	case "finish":
		return OfflineVerdict{OfflineRefuse,
			"마무리는 판단·후속·종료·자원 반납을 한 트랜잭션으로 한다 — " +
				"오프라인에서 반쪽만 쌓으면 그 원자성이 거짓이 된다. 판단만 남기려면 note 를 써라"}
	case "alloc":
		return OfflineVerdict{OfflineRefuse,
			"발번은 원자 카운터다 — 오프라인에서 발급하면 두 세션이 같은 번호를 쓴다(락이 원리적으로 못 막는 자리다)"}
	case CmdProjectRemove:
		// ★ 표 밖으로 떨어뜨려 default 를 태우지 않는다(최종 리뷰 Important-3). 서버 미도달일
		//   때 default 로 빠지면 사유가 "명령의 열화 정책이 정의돼 있지 않다"가 되는데, 그
		//   문구는 "이 명령은 설계가 안 됐다 = 서버 결함"으로 읽힌다 — 하필 서버가 죽은
		//   머신은 정확히 사람이 잔해(죽은 프로젝트)를 치우려 드는 순간이라 이 오독이
		//   제일 나쁘게 걸린다. 동작(거절)은 default 와 같지만 사유가 다르다.
		return OfflineVerdict{OfflineRefuse,
			"삭제 판정은 원장의 지금 상태(항목·판단·다른 프로젝트의 판단 수)를 실시간으로 세어 " +
				"내린다 — 되돌릴 수 없는 삭제를 오프라인의 낡은 셈으로 실행하면 그 사이 새로 " +
				"생긴 항목·판단을 못 보고 지울 위험이 있다. 서버가 돌아오면 다시 실행하라"}
	case CmdMove:
		// ★ CmdProjectRemove 와 같은 결의 판단이다 — 표 밖으로 떨어지면 사유가 "정의돼
		//   있지 않다"가 되어 설계 결함처럼 읽힌다. 동작(거절)은 default 와 같지만
		//   사유가 다르다.
		return OfflineVerdict{OfflineRefuse,
			"옮길 대상 프로젝트가 실제로 등록돼 있는지는 서버만 안다(자동 생성을 안 하므로 " +
				"없는 프로젝트로는 거절한다 — move_seam_test.go 의 " +
				"TestMoveToUnknownProjectIsRefused). 아웃박스에 쌓아 재연결 시 재생하면 그 " +
				"사이 대상 프로젝트가 지워졌거나(project rm) 이 항목이 이미 다른 곳으로 " +
				"옮겨졌을 수 있는데도 낡은 요청을 그대로 실행해 엉뚱한 곳으로 보낸다"}
	case CmdAfterCut:
		return OfflineVerdict{OfflineRefuse,
			"선행을 끊는 판정은 지금 그 항목에 실제로 걸려 있는 선행이 무엇인지(item·job·sha " +
				"축)를 원장에서 실시간으로 확인해야 한다 — 아웃박스에 쌓아 재생하면 그 사이 " +
				"같은 선행이 다른 경로로 이미 사라졌거나 바뀌었을 수 있는데도 낡은 요청을 " +
				"그대로 실행해 엉뚱한 관계를 끊거나 이미 없는 관계를 끊으려 든다. 되돌릴 수 " +
				"없는 관계 편집이라 서버가 돌아오면 지금 상태를 보고 다시 실행하라"}
	case CmdLabel:
		// ★ CmdAfterCut 과 같은 결이다 — 표 밖으로 떨어지면 사유가 "정의돼 있지
		//   않다"가 되어 설계 결함처럼 읽힌다. 동작(거절)은 default 와 같지만 사유가 다르다.
		return OfflineVerdict{OfflineRefuse,
			"꼬리표 더하기·빼기는 지금 그 항목에 무엇이 붙어 있는지를 원장에서 실시간으로 " +
				"읽어 계산한다 — 아웃박스에 쌓아 재생하면 그 사이 다른 경로로 꼬리표가 " +
				"바뀌었어도 낡은 요청이 그대로 덮는다. 서버가 돌아오면 지금 상태를 보고 다시 실행하라"}
	case CmdPrescriptions:
		// ★ 사람이 치는 명령이 아니라 Stop 훅(hook.go 의 hookStop)의 내부 호출이다. 이
		//   거절은 훅을 안 막는다 — 훅은 fail-open 이라(hookStop 머리 주석) 경고 로그만
		//   남기고 다음 턴으로 넘어간다. 그래도 표 밖 default 로 떨어지면 그 로그 사유가
		//   "정책이 정의돼 있지 않다"로 오독된다는 점은 사람이 치는 명령과 같다.
		return OfflineVerdict{OfflineRefuse,
			"처방은 원장의 지금 상태(claim·랜딩·이벤트 억제 이력)를 읽어 판정하고 동시에 " +
				"\"보였다\"는 사실을 이벤트로 남겨 같은 처방이 반복 발화되지 않게 억제한다 " +
				"(internal/service/prescribe.go) — 오프라인에서 캐시로 답하면 이미 해소된 " +
				"처방이 억제 이력을 못 읽어 다시 뜨거나, 새로 켜진 조건을 놓쳐 정작 필요한 " +
				"처방을 못 낸다"}

	// ── 랜딩 레인 넷. 전부 거절이지만 **사유가 셋으로 갈린다.**
	//
	// ★ 사유를 "레인은 오프라인에서 안 된다" 한 줄로 뭉개면 다음 사람이 그중 하나만
	//   아웃박스로 연다 — 반납이 제일 그럴듯해 보이기 때문이다("어차피 놓을 건데
	//   나중에 보내면 되지 않나"). 그 한 줄이 남의 점유를 반납한다.
	case CmdLandAcquire:
		return OfflineVerdict{OfflineRefuse,
			"레인 취득은 오프라인에 성립할 수 없다 — 배타의 정본이 서버의 DB 제약이라 " +
				"여기서 '내 차례'를 만들면 두 세션이 동시에 랜딩한다"}
	case CmdLandReport, CmdLandLeave:
		return OfflineVerdict{OfflineRefuse,
			"레인 반납은 재생 대상이 아니다 — 재생 시점에 이미 남이 잡았을 수 있고, " +
				"그러면 남의 점유를 반납한다"}
	case CmdLaneRelease:
		return OfflineVerdict{OfflineRefuse,
			"회수는 사람의 판단이라 재생 대상이 아니다 — 지금 무엇이 물려 있는지를 보고 내린 판정인데, " +
				"재생 시점의 레인은 그 판정이 본 레인이 아니다"}
	case CmdClaimRelease:
		return OfflineVerdict{OfflineRefuse,
			"선점 회수도 사람의 판단이라 재생 대상이 아니다 — 신호 나이를 보고 내린 판정인데, " +
				"재생 시점에는 그 세션이 되살아나 일하고 있을 수 있다(생존 오판 실측 2회가 자동 회수를 기각한 그 축이다)"}

	default:
		return OfflineVerdict{OfflineRefuse,
			fmt.Sprintf("명령 %q 의 열화 정책이 정의돼 있지 않다 — "+
				"기본값을 두면 아무도 정하지 않은 정책이 조용히 붙는다", clip(cmd, 40))}
	}
}

// judgmentsPath 는 아웃박스가 재생할 수 있는 **유일한 경로**다.
// 값이 cmds.go·mcpbackend.go 의 note 경로와 같아야 한다 — 어긋나면 오프라인 note 가
// 쌓이지 않고 거절되는데, 그 거절은 시끄럽다(degrade_test.go 가 그 자리에서 빨강을 낸다).
const judgmentsPath = "/api/v1/judgments"

// OutboxEligible 은 이 명령이 아웃박스에 들어가도 되는지 본다. 순수 함수다.
//
// ★ 적격 집합이 {note} 하나인 것이 설계다 — 판단만이 원리적으로 파생 불가하다(설계 §7).
// 여기를 넓히려면 이 함수와 그 시험을 **함께** 고쳐야 한다. JudgeOffline 한 자리만
// 고쳐서 새 명령이 아웃박스로 새는 경로를 이 함수가 막는다.
//
// 왜 둘째 방어가 필요한가: JudgeOffline 의 아웃박스 가지는 `case "note":` 한 줄이라,
// 거기에 낱말 하나를 더 끼워 넣는 것이 물리적으로 가장 쉬운 수정이다. 그런데 아웃박스는
// **재연결 시점에 재생**된다 — 그때 세상은 달라져 있다. 레인 취득이 그 자리에 들어가면
// 남이 랜딩 중인 레인을 5분 뒤에 뺏고, 반납이 들어가면 남의 점유를 반납한다.
// "잃으면 다시 만들 수 없는가"만 보고 넓히면 이 축이 안 보인다.
//
// 경로까지 보는 이유: 명령 이름은 클라이언트가 붙이는 라벨이라 같은 이름으로 다른 표면을
// 칠 수 있다. 아웃박스에 실제로 쌓이는 것은 (키, 경로, 본문)이고 재생이 치는 것도 그 경로다.
func OutboxEligible(cmd, path string) (bool, string) {
	c := strings.TrimSpace(cmd)
	p := strings.TrimSpace(path)
	if c != "note" {
		return false, fmt.Sprintf("아웃박스 적격 명령은 note 하나인데 %q 다 — "+
			"판단만이 원리적으로 파생 불가하고, 나머지는 재생 시점에 세상이 달라져 있다",
			clip(c, 40))
	}
	if p != judgmentsPath {
		return false, fmt.Sprintf("적격 경로는 %s 하나인데 %q 다 — "+
			"명령 이름은 클라이언트가 붙이는 라벨이고, 재생이 실제로 치는 것은 이 경로다",
			judgmentsPath, clip(p, 120))
	}
	return true, "판단은 원리적으로 파생 불가한 유일한 자산이다 — 쌓아 두고 멱등 재생한다"
}

// ErrUnreachable 은 서버에 **닿지 못했다**는 표식이다. 4xx·5xx 와 다른 축이다.
var ErrUnreachable = errors.New("조정 서버 미도달")

// Unreachable 은 이 결과가 "서버 미도달"인지 판정한다. 순수 함수다.
//
// ★ HTTP 상태코드가 온 것과 아예 못 닿은 것을 가른다. 400 을 미도달로 접으면
// 잘못된 인자가 조용히 캐시 응답으로 바뀌어 **틀린 요청이 성공처럼 보인다.**
// 다만 502·503·504 는 게이트웨이가 상류에 못 닿은 것이라 미도달과 같은 처방이다.
func Unreachable(err error, status int) bool {
	if err != nil {
		if errors.Is(err, ErrUnreachable) {
			return true
		}
		var ne net.Error
		if errors.As(err, &ne) {
			return true
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			return true
		}
		return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EHOSTUNREACH)
	}
	switch status {
	case 502, 503, 504:
		return true
	}
	return false
}

// StaleBanner 는 L1 배너다(설계 §7 의 문안).
//
// 순수 함수인 이유: 이 문자열이 **소비자 좌표계 그 자체**다 — 세션이 실제로 읽는 것은
// 이 문장뿐이고, 시험은 이 함수의 출력을 단정한다.
// cachedAt 이 0값이면 "캐시조차 없다"를 명시한다. 침묵하면 빈 화면이 "아무도 없다"로 읽힌다.
func StaleBanner(now, cachedAt time.Time, serverURL string) string {
	var b strings.Builder
	b.WriteString("⚠ 조정 서버 미도달(" + clip(serverURL, 200))
	if cachedAt.IsZero() {
		b.WriteString(", 캐시 없음).\n")
	} else {
		b.WriteString(fmt.Sprintf(", 마지막 접속 %s · %s).\n",
			cachedAt.Local().Format("15:04"), humanAge(now.Sub(cachedAt))))
	}
	b.WriteString("  되는 것: 코드 작성·커밋·조사 전부. 이미 선점한 항목의 작업.\n")
	b.WriteString("  안 되는 것: 새 항목 선점 · 다른 세션의 현재 상태.\n")
	if cachedAt.IsZero() {
		b.WriteString("  이 머신에는 캐시된 스냅숏이 하나도 없다 — 누가 무엇을 집었는지 알 방법이 지금 없다.")
	} else {
		b.WriteString(fmt.Sprintf("  아래는 %s 시점의 스냅숏이다. 그 뒤 남이 무엇을 집었는지는 알 수 없다.",
			cachedAt.Local().Format("15:04")))
	}
	return b.String()
}

// SkewBanner 는 클라이언트·서버 계약 버전 스큐를 알린다. 순수 함수다.
//
// 플러그인은 자동 갱신되므로 **운영자가 아무것도 안 해도** 스큐가 발생한다(설계 §7).
// 같으면 빈 문자열이다 — 상시 점등된 경고는 판별력이 0 이기 때문이다.
func SkewBanner(clientAPI, serverAPI string) string {
	if strings.TrimSpace(serverAPI) == "" {
		return "⚠ 서버가 api_version 을 알리지 않았다 — /healthz 가 옛 형식이거나 이 주소가 flightdeck 이 아니다."
	}
	if clientAPI == serverAPI {
		return ""
	}
	return fmt.Sprintf("⚠ 계약 버전 스큐: 클라이언트 %s · 서버 %s. "+
		"플러그인은 자동 갱신되므로 이 어긋남은 운영자가 아무것도 안 해도 생긴다 — 서버를 올려라.",
		clip(clientAPI, 16), clip(serverAPI, 16))
}
