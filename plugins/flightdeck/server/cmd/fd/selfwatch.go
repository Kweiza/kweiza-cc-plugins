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
// ★ **근거 있는 값이 아니다.** 갱신은 사람이 `plugin update` 를 누른 뒤에만 오고,
// 그때 이 정도 안에 따라오면 충분하다는 판단이다. 근거를 만들 수 있으면 그때 고친다
// (fd-live-window-baseline 이 같은 종류의 부채다).
const defaultSelfWatchInterval = 30 * time.Second

// selfVerifyTimeout 은 자식 selfcheck 하나에 주는 시간이다.
const selfVerifyTimeout = 15 * time.Second

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

	mu     sync.Mutex
	status selfUpdateStatus

	// 주입 자리 — 시험이 프로세스를 안 죽이고 단언한다.
	stat     func(string) (ExeID, error)
	verify   func(ctx context.Context, exe, db string) (buildLine string, err error)
	execSelf func(exe string, argv, env []string) error
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
	_, dockerErr := os.Stat("/.dockerenv")
	_, dataErr := os.Stat("/data")
	if isContainer, why := containerVerdict(dockerErr == nil, dataErr == nil); isContainer {
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

// Run 은 감시 루프다. ctx 가 끝나면 돌아온다.
//
// drain 은 "서버를 정상 종료시키고 그것이 끝날 때까지 기다린다"이다.
// exec 는 프로세스 이미지를 갈아치우므로 **인플라이트 요청이 통째로 끊기기 전에** 불러야 한다.
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
	if act != ActVerify {
		return act
	}
	w.log.Info("실행 파일이 교체됐다 — 검증한다", "reason", clip(why, 300))

	vctx, cancel := context.WithTimeout(ctx, selfVerifyTimeout)
	defer cancel()
	buildLine, err := w.verify(vctx, w.exePath, w.dbPath)

	from := buildinfo.Short(buildinfo.Self())
	if err != nil {
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
	// drain() 의 <-served 가 이미 다른 경로(운영자 SIGTERM)로 풀려 있어 안 막히기 때문이다.
	// 창은 이 검사와 drain() 사이 몇 줄이다 — 없애지는 못해도 verify 시간(≤selfVerifyTimeout)
	// 전체에서 몇 줄로 좁힌다.
	if ctx.Err() != nil {
		w.log.Info("검증 중 종료 요청이 와 재기동을 접는다", "exe", clip(w.exePath, 200))
		return ActNothing
	}

	// ★ TOCTOU. stat(248행)과 여기 사이(≤selfVerifyTimeout) 파일이 또 바뀌었으면 방금
	// 검증한 것은 지금 파일이 아니다 — 드레인 없이 물러난다. 다음 회차가 새 판을 본다.
	again, againErr := w.stat(w.exePath)
	if againErr != nil || !again.Same(now) {
		w.log.Warn("검증 중 실행 파일이 또 바뀌었다 — 이번 판은 건너뛴다", "exe", clip(w.exePath, 200))
		return ActRefuse
	}

	w.log.Info("검증 통과 — 드레인 후 재기동한다",
		"from", clip(from, 120), "to", clip(buildLine, 120))
	drain()
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
