// Package api 는 flightdeck 의 **정본 표면**이다 — REST + SSE.
//
// 설계 원칙 ③: "정합성 경로는 REST, MCP 는 그 위의 얇은 껍데기. 둘 다 같은 순수 함수를 부른다."
// 그래서 이 패키지에는 조합 로직이 없다. 하는 일은 넷뿐이다:
//
//  1. HTTP 를 service 계층의 인자로 옮기고 결과를 JSON 으로 되옮긴다
//  2. 게이트를 지킨다 — 인증 · 멱등 · 한도 · 패닉
//  3. 관측을 남긴다 — 액세스 로그 1줄 · /metrics · SSE
//  4. **오류에서 내부 좌표를 걷어낸다**(원인 전문은 로그로, 고칠 거리는 응답으로)
//
// 판정은 전부 순수 함수(JudgeAuth·JudgeIdempotencyKey·JudgeIdemMatch·
// JudgePersistIdempotency·ClassifyError·ConflictAdvice·RoutePattern·RenderMetrics·
// EncodeSSE)에 있고 시험이 그 함수를 직접 부른다.
// 판정이 핸들러 본문에 흩어지면 시험이 그 로직의 **사본**을 단정하게 되고 변이가 조용히 샌다.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// Options 는 서버 표면의 설정이다.
type Options struct {
	// Token 은 Bearer 토큰이다. **비면 인증이 꺼진다** — 그 사실을 /healthz 가 알린다.
	Token string
	// RequireTokenOnLoopback 은 루프백 면제를 끈다. 기본값(false)이 설계의 기본 동작이다.
	RequireTokenOnLoopback bool
	// Heartbeat 는 SSE 주석 줄 간격이다. 0 이면 20초.
	Heartbeat time.Duration
	// SSEBuffer 는 구독자당 버퍼 이벤트 수다. 0 이면 32.
	SSEBuffer int
	// RatePerMinute 는 원격 주소당 분당 허용 요청 수다. **0 이면 한도가 없다.**
	RatePerMinute int
	// Burst 는 한도의 순간 허용치다. 0 이면 RatePerMinute.
	Burst int
	// MaxBodyBytes 는 요청 본문 상한이다. 0 이면 1MiB.
	MaxBodyBytes int64
	// IdempotencyTTL·IdempotencyMax 는 멱등 표의 보존 구간과 최대 항목 수다.
	IdempotencyTTL time.Duration
	IdempotencyMax int
	// Log 는 구조화 로거다. nil 이면 slog.Default().
	Log *slog.Logger
	// Clock 은 시계다. nil 이면 time.Now().UTC.
	Clock func() time.Time

	// Fallback 은 REST 라우트에 안 걸린 요청을 받을 핸들러다(보통 대시보드).
	// nil 이면 JSON 404 를 낸다.
	//
	// ★ 이 옵션이 있는 이유는 화면을 **게이트 사슬 안**에 두기 위해서다.
	// 앞선 판은 바깥 mux 에서 `mux.Handle("/", webH)` 로 붙였고, 그래서 대시보드의
	// 쓰기 둘(선점 회수·항목 폐기)이 인증·한도·멱등·패닉복구·액세스로그·지표를
	// 전부 안 탔다. 토큰을 켠 배포에서 REST 는 401 을 내는데 화면 쪽은
	// 비루프백 무인증 POST 가 303 으로 통과하고 **항목이 실제로 폐기됐다**(실물 재현).
	//
	// 이 제품이 막으려는 것이 "남의 작업을 통째로 집는 사고"인데 그 사고를
	// 아무나 원격에서 낼 수 있었다. 조립을 두 자리로 나누면 한쪽만 잠기고,
	// 그 비대칭은 잠겨 있다고 믿게 만들어서 안 잠근 것보다 나쁘다.
	Fallback http.Handler
}

func (o Options) withDefaults() Options {
	if o.Heartbeat <= 0 {
		o.Heartbeat = 20 * time.Second
	}
	if o.SSEBuffer <= 0 {
		o.SSEBuffer = 32
	}
	if o.MaxBodyBytes <= 0 {
		o.MaxBodyBytes = 1 << 20
	}
	if o.IdempotencyTTL <= 0 {
		o.IdempotencyTTL = 24 * time.Hour
	}
	if o.IdempotencyMax <= 0 {
		o.IdempotencyMax = 4096
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Clock == nil {
		o.Clock = func() time.Time { return time.Now().UTC() }
	}
	return o
}

// server 는 표면 하나다. 상태는 전부 여기 모인다.
type server struct {
	svc  *service.Service
	st   *store.Store
	log  *slog.Logger
	opt  Options
	hub  *Hub
	met  *metrics
	idem *idemStore
	lim  *limiter
	now  func() time.Time

	// mux 는 라우팅 표다. 게이트가 **mux 에 닿기 전에** 되돌아 나갈 때
	// 라우트 라벨을 스스로 풀기 위해 보관한다 — 아래 resolveRoute 참조.
	mux *http.ServeMux
}

// resolveRoute 는 이 요청의 라우트 라벨을 낸다.
//
// ★ r.Pattern 은 **mux 가 매칭한 뒤에야** 채워진다. 그래서 멱등·인증처럼 mux 앞에서
// 되돌아 나가는 게이트의 응답은 라벨이 전부 `<METHOD> unmatched` 가 됐다 —
// 재생(201)·충돌(409)·키 없음(400) 셋 다.
//
// 이 레포의 로그 규율은 "route 는 메트릭 라벨과 **같은 문자열**"이다. 그 결선이 여기서 끊기면
// 아웃박스 재생 구간처럼 **정의상 재시도가 몰리는 트래픽**이 통째로 unmatched 로 뭉쳐,
// 어느 라우트가 재생되는지를 지표로 볼 수 없다.
//
// mux.Handler 는 매칭만 하고 핸들러를 부르지 않으므로 부작용이 없다.
func (s *server) resolveRoute(r *http.Request) string {
	if p := strings.TrimSpace(r.Pattern); p != "" && p != "/" {
		return RoutePattern(p, r.Method)
	}
	if s.mux != nil {
		if _, pattern := s.mux.Handler(r); pattern != "" {
			return RoutePattern(pattern, r.Method)
		}
	}
	return RoutePattern("", r.Method)
}

// NewServer 는 라우팅과 게이트를 얹은 핸들러를 만든다.
//
// 라우팅은 표준 http.ServeMux 의 메서드·패턴 문법이다(Go 1.22+). 프레임워크가 없다 —
// 라우트 패턴 문자열이 그대로 액세스 로그와 /metrics 의 라벨이 되므로,
// 라우터가 그 문자열을 돌려주는 것 자체가 이 계층이 필요로 하는 기능의 전부다.
func NewServer(svc *service.Service, opt Options) http.Handler {
	s := newServer(svc, opt)
	s.mux = s.routes()
	return s.chain(s.mux)
}

// newServer 는 상태만 만든다. 라우팅과 분리한 이유는 시험이 **같은 게이트 사슬**에
// 다른 핸들러를 끼워 넣을 수 있어야 하기 때문이다 — 패닉 복구처럼 정상 라우트로는
// 만들 수 없는 축이 있고, 그 축을 시험용 사슬로 따로 조립하면 시험이 사본을 단정하게 된다.
func newServer(svc *service.Service, opt Options) *server {
	opt = opt.withDefaults()
	// ★ service.name 을 여기서 덧칠하지 않는다. 그 필드는 **프로세스 진입점 하나**가 건다
	//   (cmd/fd 의 newLoggerNamed). 라이브러리 계층이 각자 덧칠하면 JSON 한 줄에 같은 키가
	//   여러 번 들어가고, 중복 키의 처리는 파서마다 다르다 — 수집기 판올림 한 번에
	//   "어느 프로세스가 무엇을 했나"라는 축이 조용히 사라진다.
	s := &server{
		svc:  svc,
		log:  opt.Log,
		opt:  opt,
		hub:  NewHub(opt.SSEBuffer),
		met:  newMetrics(),
		idem: newIdemStore(opt.IdempotencyTTL, opt.IdempotencyMax, opt.Clock),
		lim:  newLimiter(opt.RatePerMinute, opt.Burst, opt.Clock),
		now:  opt.Clock,
	}
	s.idem.log = opt.Log
	if svc != nil {
		s.st = svc.Store()
		// 멱등 기록의 영속 계층. Store 가 없으면(시험의 대조 조립) 메모리 전용으로 돈다.
		if s.st != nil {
			s.idem.db = s.st
		}
	}
	return s
}

// routes 는 표면 목록이다. 설계 §6 의 REST 표와 1:1 이다.
func (s *server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	// 세션 — D 계층. 사람이 branch·head·sha 를 적을 자리가 **없다**(설계 §5).
	mux.HandleFunc("POST /api/v1/sessions", s.handleOpenSession)
	mux.HandleFunc("PATCH /api/v1/sessions/{id}", s.handlePatchSession)
	mux.HandleFunc("POST /api/v1/sessions/{id}/signals", s.handleSignal)
	mux.HandleFunc("POST /api/v1/sessions/{id}/workspaces", s.handleWorkspace)
	mux.HandleFunc("POST /api/v1/footprints", s.handleFootprints)

	// 큐 — Q 계층.
	mux.HandleFunc("GET /api/v1/items/next", s.handleNextItem)
	mux.HandleFunc("POST /api/v1/items", s.handleAddItem)
	mux.HandleFunc("POST /api/v1/items/{id}/claim", s.handleClaimItem)
	mux.HandleFunc("POST /api/v1/items/{id}/finish", s.handleFinishItem)
	mux.HandleFunc("POST /api/v1/items/{id}/move", s.handleMoveItem)

	// 판단 — J 계층. **추가 전용**이라 PUT·DELETE 가 없다.
	mux.HandleFunc("POST /api/v1/judgments", s.handleAddJudgment)
	mux.HandleFunc("GET /api/v1/judgments", s.handleSearchJudgments)

	// 발번·스냅숏.
	mux.HandleFunc("POST /api/v1/counters/{name}/next", s.handleCounter)
	mux.HandleFunc("GET /api/v1/snapshots/{key}", s.handleGetSnapshot)
	mux.HandleFunc("PUT /api/v1/snapshots/{key}", s.handlePutSnapshot)

	// 화면·알림·진단.
	mux.HandleFunc("GET /api/v1/dashboard.json", s.handleDashboard)
	mux.HandleFunc("GET /api/v1/notices", s.handleNotices) // 꼬리 전용. 세션 카드 파생을 안 돈다
	mux.HandleFunc("GET /api/v1/events", s.handleEvents)
	mux.HandleFunc("GET /events", s.handleEvents) // 짧은 별칭. 화면이 이걸 문다
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /metrics", s.handleMetrics)

	// 못 맞춘 요청. Fallback 이 있으면 그쪽으로 넘기고, 없으면 JSON 404 다.
	// 기본 404 는 평문이라 클라이언트가 오류 본문을 파싱하는 경로가 두 벌이 된다.
	//
	// ★ Fallback 도 이 mux 안에 있으므로 chain() 의 게이트를 **전부** 탄다.
	// 바깥 mux 에서 붙이면 그 사슬을 통째로 우회한다.
	if s.opt.Fallback != nil {
		mux.Handle("/", s.opt.Fallback)
	} else {
		mux.HandleFunc("/", s.handleUnmatched)
	}

	return mux
}

// chain 은 게이트를 순서대로 감싼다. **순서가 계약이다.**
//
//	requestID  — 상관키를 만든다. 401·429 응답에도 실린다
//	  rateLimit — 429. 로그 줄 없음(초과 트래픽이 그대로 로그 증폭이 된다)
//	    auth    — 401. 로그 줄 없음
//	      accessLog — 여기서부터 "게이트를 통과한 요청"이다. 1건당 1줄
//	        idempotency — 쓰기 재시도를 같은 결과로 접는다
//	          recover   — 패닉을 500 으로. 액세스 로그가 그 500 을 본다
//
// recover 가 accessLog **안쪽**인 이유: 바깥에 두면 패닉 요청의 액세스 로그가
// 상태코드 0 으로 남아 "무엇이 500 을 냈나"를 지표에서 못 찾는다.
func (s *server) chain(h http.Handler) http.Handler {
	return s.withRequestID(s.withRateLimit(s.withAuth(s.withAccessLog(s.withIdempotency(s.withRecover(h))))))
}

// Serve 는 핸들러를 주소에 붙이고 ctx 가 끝날 때까지 돌린다.
//
// 포트 선점은 **사유를 남기고 종료한다**(설계 §7) — 조용히 실패하면
// 전 세션이 "서버 미도달"만 보고 원인을 모른다.
func Serve(ctx context.Context, addr string, h http.Handler, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	// service.name 은 진입점이 이미 걸어 두었다 — 여기서 다시 걸면 한 줄에 두 번 찍힌다.

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.ErrorContext(ctx, "포트를 열 수 없다 — 이미 쓰고 있는 프로세스가 있는지 확인해라",
			"route", clip(addr, 120), "error", err.Error())
		return fmt.Errorf("%s 를 열 수 없다: %w", addr, err)
	}

	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		// WriteTimeout 을 걸지 않는다 — SSE 는 수명이 정해지지 않은 응답이라
		// 상한을 걸면 정상 구독이 주기적으로 끊긴다.
		IdleTimeout:    120 * time.Second,
		BaseContext:    func(net.Listener) context.Context { return ctx },
		ErrorLog:       slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		ReadTimeout:    0,
		MaxHeaderBytes: 1 << 20,
	}

	done := make(chan error, 1)
	go func() {
		log.InfoContext(ctx, "서버 기동", "route", clip(ln.Addr().String(), 120))
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			log.ErrorContext(ctx, "서버가 멈췄다", "error", err.Error())
		}
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			log.ErrorContext(ctx, "정상 종료 실패 — 강제로 닫는다", "error", err.Error())
			if cerr := srv.Close(); cerr != nil {
				log.ErrorContext(ctx, "강제 종료도 실패했다", "error", cerr.Error())
			}
		}
		<-done
		log.InfoContext(ctx, "서버 종료")
		return nil
	}
}

// clip 은 로그·오류에 실을 외부 문자열을 자르고 제어문자를 걷어낸다.
// (service.clip 과 같은 규율이지만 그쪽은 비공개다.)
func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}
