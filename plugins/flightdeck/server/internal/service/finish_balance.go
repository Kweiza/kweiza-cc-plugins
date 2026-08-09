package service

import (
	"context"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/store"
)

// ReproWindow 는 재생산율을 재는 표본 크기다 — 최근 이만큼의 마무리를 본다.
//
// ★ 고정 상수이고 **응답에 그 수를 적는다.** "최근 20회 기준"이 없으면 전 기간 누적과
// 구분되지 않는다. AckReach 가 시각 절단 없이 프로젝트 전 역사를 누적해 겪은 것과 같은
// 실패다(fd-ack-reach-needs-time-window).
//
// 20 인 이유: 실측에서 시간당 마무리가 1~11건이라 20 은 대략 반나절~하루의 창이다.
// 더 짧으면 한 세션의 묶음 마무리 하나가 지표를 통째로 흔들고, 더 길면 방금 바뀐
// 거동이 안 보인다.
const ReproWindow = 20

// QueueBalance 는 "이 마무리가 큐를 늘렸나 줄였나"에 답하는 값이다.
//
// ★ 왜 이것이 있나. 실측(kweiza-cc-plugins · event 원장): finish 88건이 followups 61건
// (0.69/finish)과 독립 add 53건(0.60/finish)을 낳아 **R = 1.30** 이다. 사이클 1회
// (pickup→작업→finish)마다 큐가 +0.29 이고, 88사이클 × 0.29 ≈ +25 가 실제 잔량과 맞는다.
// 즉 **pickup 을 더 돌려서는 큐가 안 준다** — 줄이는 것은 finish 인데 그 finish 가 평균
// 1.29건을 다시 넣는다.
//
// ★ **그 1.30 에는 날짜가 없다 — 2026-08-06 무렵의 전 기간 값이다**(원장의 88번째 kweiza
// 마무리가 id 13002 · 08-06T00:40Z. 재현 확인 2026-08-10 01:58 KST · mode=ro). 지금 다시 재면
// 최근 20 창은 0.80 이다. 이 수는 이 축의 **동기**이지 §10 반증 기한의 기준선이 아니고, 기한의
// 정본과 그 기한을 읽을 때의 오차는 DESIGN §10 이 적는다(store.QueueReproduction 의 doc 도
// 같은 것을 가리킨다). **그 오차를 여기서 세지 않는다** — 세는 순간 읽는 사람이 그 수만큼
// 세고 멈추는데, §10 의 그 열거는 네 번 늘었다.
//
// 그런데 유입을 **막는** 것은 답이 아니다. 묵은 항목의 전제를 재검증했더니 전부 살아 있는
// 결함이었다 — 진짜 결함을 항목화하지 말라고 하면 그 결함이 유실된다. 그래서 관문이 아니라
// 계기를 만든다. 판단은 사람이 한다.
type QueueBalance struct {
	Closed int `json:"closed"` // 이 호출이 닫은 항목(항상 1)
	Added  int `json:"added"`  // 이 호출이 만든 후속
	Open   int `json:"open"`   // 이 마무리 **직후** 열린 항목 수
	// Starved 는 judge.StarvationAge 를 넘긴 열린 항목 수다. Oldest 는 그중 최고령의 나이.
	Starved int           `json:"starved"`
	Oldest  time.Duration `json:"oldest"`
	// Repro 는 최근 ReproWindow 회 마무리의 원자료다. **비율을 여기 담지 않는다** —
	// R=0 과 그 밖의 것이 같은 값으로 접히면 안 되고, 그 구분은 Rate 와 렌더가 한다.
	//
	// ★ **포인터다.** nil = 이 응답에 원자료가 없다 · 채워졌는데 Finishes==0 = 표본이 0회다.
	// 앞 판은 값 타입이라 집계 실패가 제로값으로 남았고, 그 제로값이 "표본 0"과 구별되지
	// 않아 화면이 "R 은 못 쟀다(**최근 마무리 표본 0**)"로 **원인을 단정**했다 — 마무리가
	// 20회 쌓여 있어도 같은 문장이 나갔다.
	//
	// 채널은 DESIGN §5 의 **B**(필드를 nil 로)다. 그 표가 B 를 고르는 조건이 "빈 값이 그
	// 자체로 뜻을 가질 때"인데 여기가 정확히 그렇다 — `Finishes==0` 은 **참일 수 있는
	// 사실**(이 창에 마무리가 없었다)이라 그것을 부재의 표현으로 쓰면 안 된다. 그래서
	// 부재는 필드 밖(포인터)으로 뺀다. `StillHeld`·`QueueBalance`(finish.go)·`QueueOpen`
	// (pick.go)이 같은 계약이고, 이 축만 값 타입이라 예외였다.
	//
	// ★ 그러나 **wire 위의 존재/부재는 성공/실패의 대리값이 아니다.** 이 축을 값 타입으로
	// 내던 서버 판(0.10~0.12)은 집계가 실패해도 제로값을 실어 보낸다 — 그것을 받은 새
	// 클라이언트는 non-nil 로 읽는다. 그래서 화면 문장이 원인을 단정하면 안 되고
	// (mcpsrv/render.go 의 ★), 그 사실을 repro_wire_test.go 가 못박아 둔다.
	Repro       *store.Reproduction `json:"repro,omitempty"`
	ReproWindow int                 `json:"repro_window"`
}

// RateVerdict 는 R 을 낼 수 있는가의 **세 갈래**다. 둘로 접으면 안 되는 이유는
// QueueBalance.Repro 의 ★ 에 있다.
type RateVerdict int

const (
	// RateUnmeasured 는 집계 자체가 실패한 것이다 — 표본이 몇인지도 모른다.
	RateUnmeasured RateVerdict = iota
	// RateNoSample 은 읽었고, 이 창에 마무리가 0회인 것이다. **참일 수 있는 사실이다.**
	RateNoSample
	// RateMeasured 는 값이 있는 것이다.
	RateMeasured
)

func (v RateVerdict) String() string {
	switch v {
	case RateUnmeasured:
		return "못 쟀다"
	case RateNoSample:
		return "표본 0"
	case RateMeasured:
		return "값 있음"
	default:
		return "알 수 없음"
	}
}

// 큐 수지 — "이 마무리가 큐를 늘렸나 줄였나"를 그 자리에서 낸다.
//
// ★ 이 파일이 finish.go 와 따로 있는 이유는 finish_followups.go 와 같다: finish.go 는
// 지금 열린 항목 넷의 자리라(fd-finish-discards-committed-work-on-aux-read ·
// fd-note-beat-masquerades-as-mcp · fd-finish-cannot-link-an-existing-item-as-followup ·
// fd-item-body-immutable-is-undocumented) 새 함수를 그 안에 넣으면 남의 헝크와 부딪힌다.
// finish.go 에는 호출 한 줄만 남는다.

// queueBalance 는 이 마무리 **직후**의 큐 수지다. 못 읽으면 nil 이다.
//
// ★ **오류를 올리지 않는다.** 이 자리에 오기까지 트랜잭션은 이미 커밋됐다 — 판단이 저장되고
// 후속이 등록되고 항목이 닫혔다. 표시용 값 하나 때문에 오류를 올리면 "판단은 저장됐는데
// 오류가 났다"는 응답이 나가고 세션이 같은 finish 를 다시 부른다. 그 부류가
// fd-finish-discards-committed-work-on-aux-read 가 고발한 결함이고, 여기서 새로 만들지 않는다.
//
// nil 과 0을 가른다: nil = 못 읽었다, 채워진 값 = 읽었다. 0으로 접으면 조회가 실패한 응답이
// "큐가 안 늘었다"를 단정한다.
func (s *Service) queueBalance(ctx context.Context, project string, added int, now time.Time) *QueueBalance {
	open, err := s.st.ListOpen(ctx, project)
	if err != nil {
		s.log.WarnContext(ctx, "마무리 뒤 큐 수지 조회 실패 — 응답은 그 축을 안 낸다",
			"project", clip(project, 64), "error", err.Error())
		return nil
	}

	b := &QueueBalance{Closed: 1, Added: added, Open: len(open), ReproWindow: ReproWindow}
	for _, it := range open {
		if it.CreatedAt.IsZero() {
			continue // 관측을 못 한 것은 안 센다
		}
		// 티클러는 굶김 축(Starved·Oldest)에서 뺀다 — 기한까지 늙는 것이 정상이라
		// 넣으면 경고가 상시 점등돼 판별력이 0이 된다(§4). Open 수에는 그대로 든다.
		if judge.IsTickler(it.Labels) {
			continue
		}
		age := now.Sub(it.CreatedAt)
		if age > b.Oldest {
			b.Oldest = age
		}
		if age >= judge.StarvationAge {
			b.Starved++
		}
	}

	// ★ 재생산율은 **따로 실패한다.** 큐 상태는 읽었는데 원장 집계만 실패한 경우,
	// 그 하나 때문에 나머지를 버리면 세션은 굶은 항목 수도 못 본다.
	//
	// ★ 실패하면 Repro 를 **nil 로 둔다** — 제로값으로 두면 "표본 0"과 같아져서 화면이
	// 실패의 원인을 "마무리가 없었다"로 단정한다. 그 둘을 가르는 것이 이 포인터의 존재
	// 이유다(필드 주석의 ★).
	if r, rerr := s.st.QueueReproduction(ctx, project, ReproWindow); rerr != nil {
		s.log.WarnContext(ctx, "재생산율 집계 실패 — 응답은 그 절을 '못 쟀다'로 낸다",
			"project", clip(project, 64), "error", rerr.Error())
	} else {
		b.Repro = &r
	}
	return b
}

// Rate 는 이 표본의 재생산율과 **왜 못 내는지**다.
//
// ★ 저장 계층이 아니라 여기서 나눈다. store 는 원자료만 낸다 — 0으로 나누는 갈래를
// 저장 계층에 두면 "마무리 0건"과 "R=0"이 같은 값으로 접힌다.
//
// ★ 판정이 셋인 이유는 RateVerdict 와 QueueBalance.Repro 의 ★ 에 있다. 앞 판은 bool
// 하나였고, 그 false 가 "집계 실패"와 "표본 0"을 같은 값으로 만들었다.
func (b QueueBalance) Rate() (float64, RateVerdict) {
	if b.Repro == nil {
		return 0, RateUnmeasured
	}
	if b.Repro.Finishes == 0 {
		return 0, RateNoSample
	}
	return float64(b.Repro.Followups+b.Repro.Adds) / float64(b.Repro.Finishes), RateMeasured
}

// Delta 는 이 호출이 큐에 더한 순증이다. 음수면 큐를 줄였다.
func (b QueueBalance) Delta() int { return b.Added - b.Closed }
