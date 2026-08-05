package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/kweiza/flightdeck/internal/api"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
	"github.com/kweiza/flightdeck/internal/web"
)

// `fd serve` — **배선만 한다.**
//
// REST 는 internal/api, 화면은 internal/web, 조합은 internal/service 다.
// 여기서 핸들러를 다시 쓰면 같은 판정이 두 벌이 되고, 두 벌은 반드시 표류한다(설계 원칙 ③).
// 이 파일이 하는 일은 셋뿐이다: DB 를 열고 · 두 표면을 한 주소에 붙이고 · 종료를 다룬다.

// DefaultDBPath 는 DB 파일 자리를 고른다. 순수 함수다.
//
// 컨테이너에는 /data 볼륨이 마운트돼 있고(설계 §2), 로컬에서는 홈 아래를 쓴다.
// 마지막 폴백(임시 디렉토리)은 값은 나오지만 재부팅하면 사라지므로 호출부가 그 사실을 로그에 남긴다.
func DefaultDBPath(get func(string) (string, bool), home string, dataDirExists bool) string {
	if v, ok := get("FD_DB"); ok && strings.TrimSpace(v) != "" {
		return filepath.Clean(v)
	}
	if dataDirExists {
		return "/data/fd.db"
	}
	if strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".flightdeck", "fd.db")
	}
	return filepath.Join(os.TempDir(), "flightdeck", "fd.db")
}

// PortAdvice 는 기동 실패 원인을 **처방이 붙은 한 줄**로 옮긴다. 순수 함수다.
//
// 설계 §7 은 "포트 선점 → 기동 시 확인 후 **사유를 남기고 종료**"를 요구한다.
// 사유만 남기면 컨테이너가 재시작 루프에 빠진 채 로그에 같은 줄만 쌓인다 —
// 무엇을 하면 되는지가 같은 줄에 있어야 한다.
func PortAdvice(addr string, err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "address already in use"):
		return fmt.Sprintf("%s 를 이미 다른 프로세스가 쓰고 있다 — "+
			"`ss -ltnp 'sport = :%s'` 로 점유자를 확인해 끄거나 --addr 로 다른 포트를 줘라",
			addr, portOf(addr))
	case strings.Contains(msg, "permission denied"):
		return fmt.Sprintf("%s 를 열 권한이 없다 — 1024 미만 포트는 특권이 필요하다. 7420 같은 상위 포트를 써라", addr)
	case strings.Contains(msg, "cannot assign requested address"):
		return fmt.Sprintf("%s 의 주소를 이 호스트에 붙일 수 없다 — 컨테이너 안이라면 0.0.0.0 으로 열어야 한다", addr)
	default:
		return fmt.Sprintf("%s 를 열지 못했다: %s", addr, clip(err.Error(), 300))
	}
}

func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 && i+1 < len(addr) {
		return addr[i+1:]
	}
	return addr
}

// buildHandler 는 REST 와 화면을 한 주소에 붙인다.
//
// 순서가 계약이다: /api/v1/ · /healthz · /metrics · /events 는 REST 가 받고,
// 나머지 전부는 화면이 받는다(화면이 자기 404 를 낸다).
// ★ 조립이 한 자리다. 화면은 api.Options.Fallback 으로 들어가 **같은 게이트 사슬**을 탄다.
//
// 앞선 판은 바깥 mux 에서 화면을 붙였고, 그래서 대시보드의 쓰기 둘이 인증·한도·멱등·
// 패닉복구·액세스로그·지표를 전부 우회했다(토큰을 켠 배포에서 비루프백 무인증 폐기가
// 실제로 성공했다). 조립을 두 자리로 나누면 한쪽만 잠긴다.
// serveAPIOptions 는 serve 가 api 표면에 넘길 옵션을 만든다. 순수 함수다.
//
// ★ 순수로 뽑아 둔 이유가 이 항목 자체다. 조립이 serve 본문 안에만 있으면 축 하나가
// 빠져도(예: 컨테이너 판정을 안 넘겨도) 전 스위트가 초록이고, 어긋남은 운영에서만 보인다.
// 정확히 그 모양이었다 — /healthz 는 "루프백은 통과한다"고 광고하는데 배선상 아무도
// 통과하지 못했고, 아무 시험도 그것을 안 잡았다.
func serveAPIOptions(token string, ratePerMinute int, log *slog.Logger, inContainer bool,
	selfUpdate func() api.SelfUpdateStatus) api.Options {
	return api.Options{
		Token:         token,
		RatePerMinute: ratePerMinute,
		Log:           log,
		InContainer:   inContainer,
		SelfUpdate:    selfUpdate,
	}
}

func buildHandler(svc *service.Service, webH http.Handler, opt api.Options) api.Handler {
	opt.Fallback = webH
	return api.NewServer(svc, opt)
}

// runServe 는 `fd serve` 다.
func runServe(args []string, env func(string) (string, bool), log *slog.Logger) int {
	fs := newFlagSet("serve")
	addr := fs.String("addr", envOr(env, "FD_ADDR", ":7420"), "수신 주소")
	dbPath := fs.String("db", "", "SQLite 파일 경로(비면 자동)")
	rate := fs.Int("rate-per-minute", 0, "원격 주소당 분당 요청 상한(0 이면 무제한)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	home, _ := os.UserHomeDir()
	path := *dbPath
	if strings.TrimSpace(path) == "" {
		_, derr := os.Stat("/data")
		path = DefaultDBPath(env, home, derr == nil)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Error("DB 디렉토리를 만들지 못해 기동을 중단한다",
			"db_path", clip(path, 200), "error", err.Error())
		return 1
	}

	st, err := store.OpenWithLogger(path, log)
	if err != nil {
		log.Error("DB 를 열지 못해 기동을 중단한다", "db_path", clip(path, 200), "error", err.Error())
		return 1
	}
	// ★ 자동 갱신 경로에서는 이 defer 가 **안 돈다.** syscall.Exec 는 스택째 버린다.
	// 그래도 되는 근거: exec 뒤에도 같은 프로세스라 POSIX 락이 자기 자신과 안 부딪히고,
	// 커밋된 것은 WAL 파일에 남아 새 이미지가 그대로 이어 읽는다. 잃는 것은 이 로그 한 줄이다.
	defer func() {
		if cerr := st.Close(); cerr != nil {
			log.Error("DB 닫기 실패", "error", cerr.Error())
		}
	}()

	svc := service.New(st, log)
	token := envOr(env, "FD_TOKEN", "")
	webH := web.New(svc, web.WithLogger(log))
	// ★ watcher 를 buildHandler 보다 먼저 만든다 — api.Options.SelfUpdate 콜백이
	// 감시기의 Status() 를 물어야 하므로, 조립 시점에 감시기가 이미 있어야 한다.
	watcher := newSelfWatcher(log, path)
	inContainer, _ := detectContainer()
	handler := buildHandler(svc, webH, serveAPIOptions(token, *rate, log, inContainer,
		func() api.SelfUpdateStatus {
			st := watcher.Status()
			out := api.SelfUpdateStatus{
				Watching: st.Watching, Reason: st.Reason, Stalled: st.Stalled,
				From: st.From, To: st.To, Outcome: st.Outcome, Detail: st.Detail,
			}
			// ★ LastAt 변환: cmd/fd 는 time.Time(제로값 = 시도 없음), api 는 *time.Time
			// (nil = 시도 없음). IsZero() 로 가른다 — 값 그대로 &st.LastAt 을 넘기면
			// "시도 없음"도 유효한 시각처럼 실린다.
			if !st.LastAt.IsZero() {
				at := st.LastAt
				out.LastAt = &at
			}
			return out
		}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("기동", "route", clip(*addr, 120), "db_path", clip(path, 200),
		"api_version", service.APIVersion, "auth_required", token != "")

	return serveWithWatcher(ctx, *addr, handler, log, watcher)
}

// serveWithWatcher 는 REST 서버를 감시기와 함께 돌린다. `runServe` 가 이것을 부르는
// 얇은 껍데기다 — 실제 api.Serve·실제 goroutine 스케줄로 드레인 악수를 시험하려면
// 이 조합을 따로 부를 수 있어야 한다(serve_test.go 를 본다).
//
// ★ 감시기에게는 **자기만의 취소 손잡이**(watchCtx)를 준다. 서버 ctx 를 그대로 주면
// 드레인(= 그 ctx 취소)이 감시기 자신도 죽여서 exec 까지 못 간다.
//
// ★ 감시기 goroutine 을 **join** 한다(watchDone). drain(=close(served))이 끝났다고
// 곧바로 이 함수가 반환하면, 감시기의 exec 시도가 끝나기 전에 이 프로세스가 os.Exit
// 에 닿을 수 있다 — (a) 성공 exec 인데 그 사실이 기록되기 전에 프로세스가 죽어
// 재기동이 사람이 끈 것과 구별 안 되거나, (b) exec 실패(exit 1) 안전망이 그 실패를
// 아직 못 본 채로 통과해 버린다. <-watchDone 이 그 창을 닫는다.
//
// 유계인 근거: stopWatch() 뒤 verify 중이던 감시기는 exec.CommandContext 가 자식을
// 죽여 selfVerifyTimeout 안에 verify 가 돌아온다. drain 중이던 감시기는 served 가
// 이미 닫혀 있어 <-served 에 안 막힌다. 그 외에는 Run 이 ctx.Done() 으로 바로
// 돌아온다. 그래서 <-watchDone 은 못 매달린다.
//
// ★ 그 대가로 감시기는 **종료 의사를 스스로 알 수 없게 된다.** watchCtx 는 SIGTERM 으로
// 안 끊기고(stopWatch() 는 api.Serve 가 돌아온 **뒤에** 불린다), serveCtx 는 감시기 자신의
// 드레인으로도 끊긴다. 그래서 신호 컨텍스트인 ctx 를 그대로 읽는 술어를 따로 건네준다 —
// exec 직전 두 자리가 그것으로 묻는다.
func serveWithWatcher(ctx context.Context, addr string, h api.Handler, log *slog.Logger, w *selfWatcher) int {
	watchCtx, stopWatch := context.WithCancel(context.Background())
	defer stopWatch()
	serveCtx, drainServe := context.WithCancel(ctx)
	defer drainServe()

	w.shutdownRequested = func() bool { return ctx.Err() != nil }

	served := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		w.Run(watchCtx, func() {
			// ★ 드레인은 **인플라이트를 마무리한다.** api.Serve 가 요청 컨텍스트를 serveCtx
			// 에서 떼어 두었고(BaseContext), 여기서 그 ctx 를 취소하면 Serve 가 ① 수명이
			// 정해지지 않은 응답(SSE)에 종료를 알리고 ② srv.Shutdown 으로 남은 요청을
			// 기다린다. api.ShutdownGrace 를 넘긴 것만 끊기고 그때는 ERROR 가 뜬다.
			//
			// ★ 그 대가로 exec 가 **최대 그 유예만큼 늦는다.** 그 값이 자기 갱신 반응 시간의
			// 항이다(아래 selfUpdateReactionBudget). 실측으로는 수명이 정해진 요청의 최댓값이
			// 0.864초라 통상 그 만큼도 안 늦는다.
			drainServe()
			<-served // 리스너가 실제로 닫힐 때까지 기다린다 — 그 전에 exec 하면 포트가 겹친다
		})
	}()

	serveErr := api.Serve(serveCtx, addr, h, log)
	close(served) // 드레인 중인 감시기를 먼저 풀어 준다
	stopWatch()   // 감시 중이면 세운다. 드레인 중이면 served 가 이미 닫혀 있어 안 막힌다
	<-watchDone   // ★ exec 시도가 끝나기 전에는 이 프로세스가 돌아오지 않는다

	if serveErr != nil {
		// api.Serve 가 이미 원인 전문을 남겼다. 여기서 더하는 것은 **처방**이다.
		log.Error("서버를 띄우지 못했다", "route", clip(addr, 120),
			"error", serveErr.Error(), "reason", PortAdvice(addr, serveErr))
		return 1
	}
	// 드레인이 자동 갱신 때문이었으면 exec 가 이미 이 프로세스를 갈아치웠다.
	// 여기에 도달했다는 것은 exec 가 실패했거나 사람이 껐다는 뜻이다.
	if su := w.Status(); su.Outcome == "failed" {
		log.Error("자동 갱신이 실패해 서버가 내려간 상태다 — 재기동이 필요하다",
			"detail", clip(su.Detail, 400))
		return 1
	}
	log.Info("종료", "route", clip(addr, 120))
	return 0
}

func envOr(env func(string) (string, bool), key, def string) string {
	if v, ok := env(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}
