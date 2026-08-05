package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/kweiza/flightdeck/internal/api"
	"github.com/kweiza/flightdeck/internal/buildinfo"
)

// ExeID 는 실행 파일 하나의 정체다. 순수 값이다.
//
// ★ **OK 가 먼저다.** false 면 나머지 필드는 값이 아니라 빈칸이다.
// 관측 못 한 것을 0 으로 접으면 "둘 다 0이니 같다"가 되고, 그 순간 이 축의 판별력이 사라진다.
//
// Dev·Ino 는 유닉스 고유이지만, ExeID 자체는 모든 플랫폼에서 같은 형태를 갖는다.
// 단지 exeIDOfPath 만 플랫폼 고유이다.
type ExeID struct {
	OK        bool
	Dev, Ino  uint64
	Size      int64
	MtimeNano int64
}

// Same 은 두 관측이 같은 파일을 가리키는지다. 순수 함수다.
// **한쪽이라도 관측 안 됐으면 같지 않다** — 모르는 것은 같은 것이 아니다.
func (e ExeID) Same(o ExeID) bool {
	if !e.OK || !o.OK {
		return false
	}
	return e.Dev == o.Dev && e.Ino == o.Ino && e.Size == o.Size && e.MtimeNano == o.MtimeNano
}

func (e ExeID) String() string {
	if !e.OK {
		return "관측 안 됨"
	}
	return fmt.Sprintf("ino=%d size=%d mtime=%d", e.Ino, e.Size, e.MtimeNano)
}

// Action 은 감시기가 이번 회차에 할 일이다.
type Action int

const (
	ActNothing Action = iota // 아무것도 안 한다
	ActVerify                // 후보다 — 자식으로 검증한다
	ActExec                  // 검증 통과 — 드레인 후 exec
	ActRefuse                // 검증 실패 — 그대로 산다
)

func (a Action) String() string {
	switch a {
	case ActVerify:
		return "verify"
	case ActExec:
		return "exec"
	case ActRefuse:
		return "refuse"
	default:
		return "nothing"
	}
}

// Decide 는 이번 회차에 무엇을 할지 정한다. 순수 함수다.
//
// **ActNothing 또는 ActVerify 만 낸다.** 검증 결과(ActExec·ActRefuse)는 이 함수가 모른다 —
// 그것은 자식 프로세스를 돌려 봐야 아는 사실이고, 순수 함수에 부수효과를 들이면
// 이 판정을 시험이 못 준다.
func Decide(start, now, lastFailed ExeID, statErr error) (Action, string) {
	if statErr != nil {
		// 교체가 아니라 삭제·권한 문제다. exec 할 대상이 없는데 가면 서버가 사라진다.
		return ActNothing, fmt.Sprintf("실행 파일을 못 쟀다: %v", statErr)
	}
	if !now.OK {
		return ActNothing, "실행 파일을 못 쟀다(사유 없음)"
	}
	if now.Same(start) {
		return ActNothing, "그대로다"
	}
	if now.Same(lastFailed) {
		return ActNothing, "이미 검증에 실패한 판이다 — 파일이 또 바뀌면 다시 본다"
	}
	return ActVerify, fmt.Sprintf("실행 파일이 교체됐다: %s → %s", start, now)
}

// exeIDOfPath 와 selfWatchSupported 는 플랫폼별 구현을 제공한다.
// selfwatch_unix.go 와 selfwatch_other.go 를 본다.

// defaultSelfWatchInterval 은 실행 파일을 다시 재는 주기다.
//
// ★ **이 상수가 통제하는 것은 갱신 반응 시간이 아니다.** 그 전제는 실측으로 반증됐다:
// 런처가 실행 파일을 다시 만드는 것은 새 세션이 `bin/fd` 를 부를 때이고, 그 상류 지연이
// 사슬을 지배한다. 이 주기는 최악 사슬의 1% 미만이라 0으로 줄여도 사람이 체감하는
// 반응 시간은 안 줄어든다. **틀린 주석은 근거 없는 주석보다 나쁘다** — 다음 사람이
// 이 값을 줄여 반응을 개선하려 들고, 아무 일도 안 일어난다.
//
// 이 상수가 실제로 통제하는 것은 **스큐 창**이다: 새 클라이언트가 옛 서버를 부르는 구간.
// 그 창의 비용에는 이 저장소가 이미 이름을 붙였다(§7 의 버전 스큐 배너).
//
// **실측(2026-08-05, 정본 DB `event` 표 9733행 · 46.24시간 · 3.51건/분).**
// 길이 편향을 보정한 "창 안에 스큐 호출이 0건일 확률":
//
//	창 10s → 80.5% · 15s → 75.5% · **30s → 66.7%** · 60s → 58.6% · 120s → 51.9% · 300s → 44.4%
//
// (읽기는 `event` 표에 한 행도 안 남으므로 이 표는 **낙관적**이다 — 실제 호출률은 더 높다.)
//
// **꺾임이 없다.** 상한은 selfUpdateSkewCeiling 이 잡고, 하한은 사실상 없다 —
// stat 1회가 773ns 라 1초 주기여도 CPU 0.00008% 이고, 정상 회차는 로그를 한 줄도 안 낸다.
// 즉 1s~120s 가 전부 평탄하다. 그 안에서 30초를 고른 것에 데이터가 주는 근거는 없고,
// **없는 꺾임을 지어내지 않는다**(fd-live-window-baseline 이 남긴 형식이다).
// 붙드는 것은 값이 아니라 selfUpdateReactionBudget 이 상한 아래라는 사실이다.
const defaultSelfWatchInterval = 30 * time.Second

// selfVerifyTimeout 은 자식 selfcheck 하나에 주는 시간이다.
//
// 실측 10회 전부 <10ms(15MB DB). 상한이 1500배 큰 것은 의도다 — 콜드 DB·큰 증분에서
// 초 단위로 늘 수 있다. 그 여유가 예산에 실리는 것은 selfUpdateReactionBudget 이 말한다.
const selfVerifyTimeout = 15 * time.Second

// selfUpdateReactionBudget 은 실행 파일 교체부터 새 프로세스가 뜨기까지, **서버 몫**의
// 최악 시간이다. 자유 상수가 아니라 **파생**이다 — 상한이 선언된 항의 합이다.
//
//	탐지(티커 대기)        ≤ defaultSelfWatchInterval
//	검증(자식 selfcheck)   ≤ selfVerifyTimeout
//	드레인(인플라이트 마무리) ≤ api.ShutdownGrace
//
// ★ **exec + 기동은 항으로 안 넣는다.** 실측 13~17ms 인데 선언된 상한이 없고, 넣으려면
// 근거 없는 상한 하나를 새로 만들어야 한다 — 이 상수가 없애려는 것이 정확히 그것이다.
// 예산은 "상한이 선언된 항의 합"이지 "일어날 수 있는 모든 일의 합"이 아니다.
//
// ★ **되풀이 실패는 안 덮는다.** 검증 중 파일이 또 바뀌면(TOCTOU, ActRefuse) 사슬이
// 통째로 한 판 더 돈다. 이 예산은 **한 판의 최악**이고, 그 사실을 여기 적어 둔다.
//
// ★ 이 식이 있는 이유는 숫자가 아니라 **결합을 보이게 하는 것**이다. api.ShutdownGrace 를
// 늘리는 사람이 자기가 자기 갱신 반응 시간을 늘린다는 것을 여기서 본다. 그 결합을 말하는
// 자리가 이것 말고 없다.
const selfUpdateReactionBudget = defaultSelfWatchInterval + selfVerifyTimeout + api.ShutdownGrace

// selfUpdateSkewCeiling 은 스큐 창의 상한이다. **이쪽이 실측에서 나온 값이다.**
//
// 기준은 §10 이 이미 명시한 실패 모양이다 — "경고가 상시 점등돼 판별력을 잃는다".
// 갱신의 **과반**이 스큐 호출 0건이어야 배너가 그 판별력을 갖고, 위 도착 분포에서
// 그 조건은 창 ≤ 약 120초다(120s 에서 51.9%, 300s 에서 44.4%).
//
// TestSelfUpdateBudgetFitsSkewCeiling 이 selfUpdateReactionBudget ≤ 이 값을 붙든다.
// 산문에만 있는 만료 조건은 아무도 안 본다 — 상한을 넘기는 커밋이 빨간불을 받아야 한다.
const selfUpdateSkewCeiling = 120 * time.Second

// DetectLag 는 실행 파일이 바뀐 시각과 그것을 관측한 시각의 차다. 순수 함수다.
//
// 예산의 첫 항(탐지)을 **사후에 잴 수 있는 유일한 자리**다. 성공한 자기 갱신은 exec 로
// 프로세스가 갈아치워져 원장에 한 행도 안 남으므로(selfUpdateStatus 의 "성공은 여기 안
// 남는다"), 교체를 본 그 순간의 로그가 아니면 이 값을 나중에 만들 길이 없다.
//
// ★ mtime 이 미래면(시계 어긋남·NFS) **0 으로 접지 않고 못 쟀다고 답한다.** 접으면
// "즉시 봤다"와 "잴 수 없다"가 같은 값이 되고, 그 둘은 뜻이 반대다.
func DetectLag(now time.Time, id ExeID) (time.Duration, bool) {
	if !id.OK {
		return 0, false
	}
	d := now.Sub(time.Unix(0, id.MtimeNano))
	if d < 0 {
		return 0, false
	}
	return d, true
}

// selfUpdateStatus 는 자동 갱신 축의 현재 상태다.
//
// ★ **Watching 이 먼저다.** 감시기가 안 떴는데 나머지가 비어 있으면 "아직 갱신이
// 없었다"로 읽힌다 — 그것은 "안 보고 있다"와 전혀 다르다. buildinfo.Coord 의 Known 이
// 같은 규율이고, 그 규율을 안 지켜 화면에 빈칸이 찍힌 적이 있다.
//
// **성공은 여기 안 남는다.** 성공하면 프로세스가 갈아치워져 새 프로세스는 그 사실을
// 모른다. 남는 것은 build 좌표가 바뀐 것뿐이고 그것으로 충분하다.
type selfUpdateStatus struct {
	Watching bool
	Reason   string    // Watching=false 일 때 왜 안 보는지
	LastAt   time.Time // 시도가 없었으면 제로값
	From, To string
	Outcome  string // "refused" | "failed"
	Detail   string

	// Stalled 는 **보고는 있는데 지금 실행 파일을 못 재고 있다**는 사실이다.
	//
	// ★ 이 자리가 없으면 stat 실패가 어느 화면에도 안 뜬다. 바이너리가 지워졌거나
	// 마운트가 사라지면 감시기는 그 갈래에 영원히 머무는데, /healthz 는 watching=true
	// 만 말하고 `fd doctor` 는 "보는 중 — 아직 교체를 못 봤다"라고 찍는다. 서버는 옛
	// 코드로 계속 살면서 화면은 따라오고 있다고 말한다 — 이 축이 없애려던 실패 모양 그대로다.
	//
	// **다시 재지면 비운다.** 지나간 고장을 현재형으로 말하면 화면이 반대 방향으로 거짓말한다.
	Stalled string
}

// selfWatcher 는 실행 파일 교체를 감시하고, 검증을 거쳐 스스로 재기동한다.
//
// ★ 이 구조체는 **플랫폼 중립**이다. 갈리는 것은 execSelf 하나뿐이다
// (selfwatch_unix.go · selfwatch_other.go). 구조체를 빌드 태그로 복제하면
// 필드 집합이 갈리고, 태그 없는 시험 파일이 다른 플랫폼에서만 컴파일에 실패한다
// (Task 2 리뷰가 그 결함을 실제로 잡았다 — `GOOS=windows go vet` 이 그것을 봤고
// `go build` 는 `_test.go` 를 건너뛰어 못 봤다).
type selfWatcher struct {
	log      *slog.Logger
	dbPath   string
	exePath  string
	start    ExeID
	lastFail ExeID
	watching bool
	reason   string
	interval time.Duration

	// stallLogged 는 마지막으로 로그에 찍은 "못 잼" 사유다. 30초 티커가 같은 줄을
	// 하루 2880번 쌓으면 그 로그는 아무도 안 읽는다 — 사유가 **바뀔 때만** 다시 찍는다.
	// step 하나에서만 읽고 쓴다(감시 goroutine 전용)이라 mu 밖이다.
	stallLogged string

	// ★ shutdownRequested 는 **신호 컨텍스트**(SIGTERM/Ctrl-C)로 종료 의사를 묻는다.
	// step 에 들어오는 ctx 는 감시기 전용 watchCtx 라서 SIGTERM 으로 안 끊긴다 —
	// 그것으로 물으면 이 검사는 운영 경로에서 영원히 거짓이다. serveCtx 로 물어도 안 된다:
	// 그쪽은 감시기 **자신의 드레인**으로도 끊겨서 "사람이 껐다"와 안 갈린다.
	// nil 이면 "종료 의사 없음"이다(감시기를 단독으로 쓰는 시험·다른 진입점).
	shutdownRequested func() bool

	mu     sync.Mutex
	status selfUpdateStatus

	// 주입 자리 — 시험이 프로세스를 안 죽이고 단언한다.
	stat     func(string) (ExeID, error)
	verify   func(ctx context.Context, exe, db string) (buildLine string, err error)
	execSelf func(exe string, argv, env []string) error
}

// shuttingDown 은 이미 종료 의사가 왔는가다.
func (w *selfWatcher) shuttingDown() bool {
	return w.shutdownRequested != nil && w.shutdownRequested()
}

// containerVerdict 는 이 프로세스가 컨테이너 안인가다. 순수 함수다.
//
// ★ 컨테이너에서는 감시를 **아예 안 켠다.** 이미지는 불변이라 실행 파일이 영원히 안 바뀌고,
// 그 상태로 "보는 중"이라고 말하면 읽는 쪽은 따라오고 있다고 믿는다. 침묵보다 나쁘다 —
// 틀린 안심을 준다. 컨테이너의 갱신은 `docker compose up -d --build` 로 사람이 한다.
//
// /data 를 신호로 쓰는 것은 이 저장소의 기존 관용구다(DefaultDBPath 가 같은 축을 본다).
func containerVerdict(hasDockerEnv, hasDataDir bool) (bool, string) {
	switch {
	case hasDockerEnv:
		return true, "이 서버는 컨테이너다(/.dockerenv) — 자기 이미지를 다시 만들 수 없어 자기 갱신을 안 한다. " +
			"`docker compose up -d --build` 가 갱신 경로다"
	case hasDataDir:
		return true, "이 서버는 컨테이너로 보인다(/data 볼륨) — 자기 갱신을 안 한다. " +
			"`docker compose up -d --build` 가 갱신 경로다"
	}
	return false, ""
}

// detectContainer 는 파일시스템 신호를 읽어 containerVerdict 에 넘긴다.
//
// ★ 판정의 주인은 하나다. 이 축을 두 번째로 필요로 한 자리(인증 표면이 "루프백 면제가
// 왜 안 닿는가"를 말할 때)가 생겼을 때 os.Stat 두 줄을 복사하면, 신호가 하나 늘어날 때
// 한쪽만 고쳐지고 두 화면이 서로 다른 말을 하게 된다.
func detectContainer() (bool, string) {
	_, dockerErr := os.Stat("/.dockerenv")
	_, dataErr := os.Stat("/data")
	return containerVerdict(dockerErr == nil, dataErr == nil)
}

// newSelfWatcher 는 감시기를 만든다. **기준값을 여기서 정한다.**
func newSelfWatcher(log *slog.Logger, dbPath string) *selfWatcher {
	w := &selfWatcher{
		log: log, dbPath: dbPath, interval: defaultSelfWatchInterval,
		stat: exeIDOfPath, verify: verifyWithSelfcheck, execSelf: execSelf,
	}
	if !selfWatchSupported() {
		w.reason = "이 플랫폼은 자기 재기동을 지원하지 않는다(syscall.Exec 부재)"
		return w
	}
	if isContainer, why := detectContainer(); isContainer {
		w.reason = why
		return w
	}
	exe, err := os.Executable()
	if err != nil {
		w.reason = fmt.Sprintf("실행 파일 자리를 못 읽었다: %v", err)
		return w
	}
	w.exePath = exe

	// ★ 기준값을 /proc/self/exe 에서 읽는다. 그 자리는 **지금 도는 이미지**를 가리키고,
	// 파일이 이미 교체된 뒤라면 경로를 stat 한 값과 다르다. 경로만 재면
	// "이미 낡은 채로 시작한 서버"가 자기를 최신으로 기억해 영원히 트리거하지 않는다.
	if id, err := w.stat("/proc/self/exe"); err == nil {
		w.start = id
	} else if id, err := w.stat(exe); err == nil {
		w.start = id
		log.Warn("/proc/self/exe 를 못 읽어 경로로 기준을 잡는다 — "+
			"이미 교체된 채 시작한 경우를 이 프로세스는 못 본다", "error", err.Error())
	} else {
		w.reason = fmt.Sprintf("실행 파일을 못 쟀다: %v", err)
		return w
	}
	w.watching = true
	return w
}

// Status 는 /healthz 가 실을 값이다. 동시 호출된다.
func (w *selfWatcher) Status() selfUpdateStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.status
	s.Watching, s.Reason = w.watching, w.reason
	return s
}

func (w *selfWatcher) setStatus(f func(*selfUpdateStatus)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	f(&w.status)
}

// noteStall 은 "지금 실행 파일을 못 잰다"를 상태에 남기고 **한 번만** 로그한다.
//
// 상태는 매번 덮는다(읽는 쪽은 현재 사실을 물으므로). 로그만 사유 문자열로 접는다 —
// 30초 티커가 같은 줄을 쌓으면 그 로그는 읽히지 않는 배경이 되고, 그것은 침묵과 같다.
// 사유가 바뀌면 원인이 바뀐 것이므로 그때는 다시 찍는다.
func (w *selfWatcher) noteStall(why string) {
	w.setStatus(func(s *selfUpdateStatus) { s.Stalled = why })
	if w.stallLogged == why {
		return
	}
	w.stallLogged = why
	w.log.Warn("실행 파일을 못 재고 있다 — 교체가 와도 못 본다",
		"exe", clip(w.exePath, 200), "reason", clip(why, 300))
}

// clearStall 은 다시 재지기 시작하면 그 사실을 지운다.
// 지나간 고장을 현재형으로 남겨 두면 화면이 반대 방향으로 거짓말한다.
func (w *selfWatcher) clearStall() {
	if w.stallLogged == "" {
		return
	}
	w.stallLogged = ""
	w.setStatus(func(s *selfUpdateStatus) { s.Stalled = "" })
	w.log.Info("실행 파일을 다시 잰다 — 감시가 회복됐다", "exe", clip(w.exePath, 200))
}

// Run 은 감시 루프다. ctx 가 끝나면 돌아온다.
//
// drain 은 "인플라이트를 마무리하고, 리스너가 실제로 닫힐 때까지 기다린다"이다.
// drain 을 부르는 이유는 **둘**이다 — 도는 요청을 마치는 것과, exec 전에 포트를 확실히 놓는 것.
//
// ★ 앞선 판은 첫째를 못 했고 주석이 그 사실을 "아웃박스 + 멱등키가 덮는다"로 정당화했다.
// 그 근거는 성립하지 않았다: 절단은 미도달이 아니라 서버가 답한 500 이라 아웃박스도
// 낡음 배너도 안 탄다(아웃박스는 미도달 기구다). 지금은 api.Serve 가 요청 컨텍스트를
// 서버 수명 ctx 에서 떼어 두어 실제로 마무리한다.
func (w *selfWatcher) Run(ctx context.Context, drain func()) {
	if !w.watching {
		w.log.Info("자기 재기동 감시를 안 켠다", "reason", clip(w.reason, 200))
		return
	}
	w.log.Info("자기 재기동 감시 시작",
		"exe", clip(w.exePath, 200), "interval", w.interval.String())
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if w.step(ctx, drain) == ActExec {
				return // 여기 도달하면 exec 가 실패한 것이다. 호출부가 처리한다
			}
		}
	}
}

// step 은 한 회차다. 시험이 이것을 직접 부른다.
func (w *selfWatcher) step(ctx context.Context, drain func()) Action {
	now, statErr := w.stat(w.exePath)
	act, why := Decide(w.start, now, w.lastFail, statErr)
	if statErr != nil || !now.OK {
		// ★ **못 재는 상태를 침묵으로 두지 않는다.** 바이너리가 지워졌거나 디렉토리
		// 권한이 바뀌었거나 마운트가 사라지면 감시기는 여기에 영원히 머문다. 그 사실을
		// 아무 데도 안 남기면 서버는 옛 코드로 계속 사는데 화면은 "보는 중"이라 말한다.
		w.noteStall(why)
		return act
	}
	w.clearStall()
	if act != ActVerify {
		return act
	}
	// ★ detect_lag 는 **예산의 첫 항을 사후에 잴 수 있는 유일한 자리다**(DetectLag 참조).
	// 못 재면 필드를 안 싣는다 — 0 을 실으면 "즉시 봤다"로 읽힌다.
	if lag, ok := DetectLag(time.Now(), now); ok {
		w.log.Info("실행 파일이 교체됐다 — 검증한다",
			"reason", clip(why, 300), "detect_lag_ms", lag.Milliseconds())
	} else {
		w.log.Info("실행 파일이 교체됐다 — 검증한다",
			"reason", clip(why, 300), "detect_lag", "못 쟀다")
	}

	vctx, cancel := context.WithTimeout(ctx, selfVerifyTimeout)
	defer cancel()
	buildLine, err := w.verify(vctx, w.exePath, w.dbPath)

	from := buildinfo.Short(buildinfo.Self())
	if err != nil {
		// ★ 종료 요청 중이면 이 오류는 **후보의 잘못이 아니다.** 운영자 SIGTERM →
		// api.Serve 종료 → stopWatch() → vctx 취소 → exec.CommandContext 가 자식을
		// 죽여 "signal: killed" 가 온다. 이것을 거절로 적으면 정상 종료 로그에 남의
		// 이름이 박히고, lastFail 이 멀쩡한 판을 태워 버린다.
		if w.shuttingDown() || ctx.Err() != nil {
			w.log.Info("종료 요청 중에 검증이 끊겼다 — 거절로 적지 않는다",
				"exe", clip(w.exePath, 200), "error", clip(err.Error(), 200))
			return ActNothing
		}
		w.lastFail = now
		w.setStatus(func(s *selfUpdateStatus) {
			s.LastAt, s.From, s.To = time.Now().UTC(), from, buildLine
			s.Outcome, s.Detail = "refused", err.Error()
		})
		w.log.Warn("자동 갱신 거절 — 그대로 산다",
			"from", clip(from, 120), "reason", clip(err.Error(), 400))
		return ActRefuse
	}

	// ★ verify 중 종료 요청이 왔으면 물러난다. 안 보면 "멈추라는 요청 뒤에 되살아난다" —
	// drain() 의 <-served 가 이미 그 종료로 풀려 있어 안 막히기 때문이다.
	//
	// **묻는 대상이 ctx 가 아니다.** ctx 는 watchCtx 이고 그것은 stopWatch() 로만 끊긴다 —
	// 즉 api.Serve 가 이미 돌아온 뒤다. 운영자가 방금 SIGTERM 을 보낸 순간에는 아직
	// 멀쩡하다. 종료 의사는 신호 컨텍스트를 보는 shutdownRequested 가 안다.
	// ctx.Err() 도 함께 보는 이유는 다르다: 그쪽이 끊겼으면 리스너가 이미 없다.
	if w.shuttingDown() || ctx.Err() != nil {
		// ★ 이 갈래도 **화면까지 간다.** 사람이 보는 것은 "갱신을 넣었는데 서버가 안 바뀌었다"이고,
		// 여기서 침묵하면 /healthz 의 self_update 가 그 사유를 말하지 않는다. Outcome 이 빈 값이면
		// "아직 시도가 없었다"로 읽히는데 그것은 사실이 아니다 — 검증까지 갔다가 접은 것이다.
		w.setStatus(func(s *selfUpdateStatus) {
			s.LastAt, s.From, s.To = time.Now().UTC(), from, buildLine
			s.Outcome, s.Detail = "refused", "검증 중 종료 요청이 와 재기동을 접었다"
		})
		w.log.Info("검증 중 종료 요청이 와 재기동을 접는다", "exe", clip(w.exePath, 200))
		return ActNothing
	}

	// ★ TOCTOU. 이 회차 첫 stat 과 여기 사이(≤selfVerifyTimeout) 파일이 또 바뀌었으면
	// 방금 검증한 것은 지금 파일이 아니다 — 드레인 없이 물러난다. 다음 회차가 새 판을 본다.
	again, againErr := w.stat(w.exePath)
	if againErr != nil || !again.Same(now) {
		detail := "검증 중 실행 파일이 또 바뀌었다 — 이번 판은 건너뛴다(다음 회차가 새 판을 본다)"
		if againErr != nil {
			detail = fmt.Sprintf("검증 뒤 실행 파일을 다시 못 쟀다: %v", againErr)
		}
		// ★ 이 갈래도 **화면까지 간다.** 거절 경로 중 유일하게 조용했던 자리였다 —
		// 서버 로그를 안 보는 사람에게는 "검증까지 통과했는데 왜 안 바뀌나"가 답이 없었다.
		// lastFail 은 안 건드린다: 이 판이 나쁘다는 판정이 아니라 늦었다는 판정이다.
		w.setStatus(func(s *selfUpdateStatus) {
			s.LastAt, s.From, s.To = time.Now().UTC(), from, buildLine
			s.Outcome, s.Detail = "refused", detail
		})
		w.log.Warn("검증 중 실행 파일이 또 바뀌었다 — 이번 판은 건너뛴다",
			"exe", clip(w.exePath, 200), "reason", clip(detail, 300))
		return ActRefuse
	}

	w.log.Info("검증 통과 — 드레인 후 재기동한다",
		"from", clip(from, 120), "to", clip(buildLine, 120))
	drain()

	// ★ **여기서 한 번 더 묻는다.** 진짜 창은 위 검사 다음 몇 줄이 아니라 drain() 전체다 —
	// drain() 은 api.ShutdownGrace 만큼 매달릴 수 있고, 그 사이에 온 SIGTERM 은
	// drainServe() 를 이미 취소된 것으로 만들고 <-served 를 그 종료로 풀어 준다.
	// 그러면 아무 저항 없이 syscall.Exec 에 닿는다 — 되돌릴 수 없는 자리다.
	//
	// ★ 이 창은 드레인이 실물이 되면서 **처음으로 실재한다.** 앞선 판은 드레인이 인플라이트를
	// 즉시 잘라서 실측 7ms 였고, 그래서 이 검사는 이론이었다. 지금은 마무리를 기다리므로
	// 유예만큼 열린다 — 이 갈래를 침묵으로 두면 안 되는 이유가 그것이다.
	//
	// 여기서 ctx(watchCtx)로 다시 묻지 않는 이유가 있다: serveWithWatcher 는 close(served)
	// **직후** 정상적으로 stopWatch() 를 부른다. 그것을 종료 의사로 읽으면 멀쩡한 재기동이
	// 매번 접힌다 — 기능 자체가 죽는다.
	if w.shuttingDown() {
		w.setStatus(func(s *selfUpdateStatus) {
			s.LastAt, s.From, s.To = time.Now().UTC(), from, buildLine
			s.Outcome, s.Detail = "refused", "드레인 중 종료 요청이 와 재기동을 접었다 — 서버는 이미 내려갔다"
		})
		w.log.Info("드레인 중 종료 요청이 와 재기동을 접는다 — 서버는 이미 내려갔다",
			"exe", clip(w.exePath, 200))
		return ActNothing
	}

	if err := w.execSelf(w.exePath, os.Args, os.Environ()); err != nil {
		// 리스너는 이미 닫혔다. 되살리는 척하지 않는다 — 호출부가 비0으로 죽는다.
		w.setStatus(func(s *selfUpdateStatus) {
			s.LastAt, s.From, s.To = time.Now().UTC(), from, buildLine
			s.Outcome, s.Detail = "failed", err.Error()
		})
		w.log.Error("재기동 실패 — 리스너는 이미 닫혔다",
			"exe", clip(w.exePath, 200), "error", err.Error())
	}
	return ActExec
}

// verifyWithSelfcheck 는 새 바이너리를 자식으로 한 번 돌린다.
func verifyWithSelfcheck(ctx context.Context, exe, db string) (string, error) {
	cmd := exec.CommandContext(ctx, exe, "selfcheck", "--db", db)
	out, err := cmd.CombinedOutput()
	line := firstOutputLine(string(out))
	if err != nil {
		return "", fmt.Errorf("selfcheck 실패(%v): %s", err, clip(strings.TrimSpace(string(out)), 400))
	}
	// 계약: `fd selfcheck ok build=<좌표>`
	if b, ok := strings.CutPrefix(line, "fd selfcheck ok build="); ok {
		return strings.TrimSpace(b), nil
	}
	return "", fmt.Errorf("selfcheck 가 계약된 첫 줄을 안 냈다: %s", clip(line, 200))
}

// firstOutputLine 은 자식 프로세스 출력의 첫 줄을 뽑는다.
//
// render.go 에 이미 다른 뜻의 firstLine(title, body string) 이 있어 이름을 갈랐다 —
// 이 함수는 인자 하나짜리 "출력 뭉치에서 첫 줄"이고 그쪽은 "제목이 없으면 본문에서 뽑는다"다.
func firstOutputLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
