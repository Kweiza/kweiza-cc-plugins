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
	"sync"
	"sync/atomic"
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

	// SelfUpdate 는 자동 갱신 축의 현재 상태를 낸다. nil 이면 "배선 안 됨"으로 답한다.
	//
	// ★ 콜백인 이유: 이 값은 **계속 변한다.** 조립 시점의 스냅숏을 박으면
	// /healthz 가 영원히 기동 직후 상태를 낸다.
	SelfUpdate func() SelfUpdateStatus

	// LedgerBackup 은 판단 원장 주기 백업의 현재 상태를 낸다. nil 이면 "배선 안 됨"으로 답한다.
	//
	// ★ SelfUpdate 와 같은 이유로 콜백이다 — 이 값은 회차마다 바뀐다.
	LedgerBackup func() LedgerBackupStatus

	// InContainer 는 이 서버가 컨테이너 안인가다. **배선이 판정해 준다.**
	//
	// ★ 이 표면이 쓰는 곳은 하나뿐이다 — 루프백 면제가 안 닿을 때 **그 사유를 말하는 것**.
	// 컨테이너에서는 호스트의 127.0.0.1 요청이 도커 브리지를 거쳐 172.x 로 보이고,
	// 그래서 면제가 구조적으로 안 걸린다. 그 사실을 모르면 운영자는 자기 토큰 설정을
	// 의심하며 시간을 쓴다(실제로 그렇게 됐다).
	//
	// 불리언인 이유: 사람이 읽을 문장은 표면의 몫이다. 배선이 문자열을 넘기면
	// self_update 축의 문구("자기 갱신을 안 한다")가 인증 문맥으로 새어 든다 —
	// 그 둘은 같은 신호(/.dockerenv)를 보지만 하려는 말이 다르다.
	InContainer bool

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

	// LoginScreen 은 401 을 HTML 토큰 폼으로 낼 렌더러다. nil 이면 JSON 401 로 접힌다.
	//
	// ★ Fallback 과 **같은 이유의 같은 모양**이다. 토큰을 아는 자리(여기)와 템플릿을 가진
	// 자리(internal/web)가 다른데, 화면은 게이트 사슬 안에 있어야 하므로 401 을 내는 것도
	// 이 계층이다. 콜백으로 받으면 api 가 HTML 을 알지 않고도 폼을 낼 수 있고, web 은
	// 토큰을 모르는 채로 남는다 — 토큰이 두 자리에 살면 상수시간 비교와 대소문자 규칙이
	// 두 벌이 되고 그 둘은 반드시 표류한다.
	//
	// ★ 이 콜백은 **상태코드를 스스로 쓴다**(401). 여기서 미리 쓰면 렌더러가 500 을 내야
	// 하는 경우에 헤더가 이미 나가 있다.
	LoginScreen func(w http.ResponseWriter, r *http.Request, v LoginView)
}

// LoginView 는 토큰 폼을 그리는 데 필요한 것 전부다.
//
// ★ **토큰 값을 안 담는다.** 되비추면 그 값이 HTML 에 실려 나가고, web/notFound 가
// 요청 경로에 대해 세워둔 규율("소비자가 이미 아는 것을 되비추지 않는다")이 여기서 깨진다.
type LoginView struct {
	// Error 는 직전 시도의 사유다. 비면 첫 방문이다.
	Error string
	// Next 는 로그인 뒤 돌아갈 자리다. **호출부가 이미 JudgeNext 를 통과시킨 값이다** —
	// 렌더러가 그 검증을 다시 하지 않는다. 두 자리에서 검증하면 한쪽만 고쳐진다.
	Next string
	// Action 은 이 폼의 action 이 가리켜야 할 **상대경로**다(`./login` · `../login` · …).
	// **호출부가 이미 JudgeLoginAction 으로 계산한 값이다** — 렌더러가 다시 계산하지
	// 않는다(Next 와 같은 규율). 렌더러가 계산하면 그 깊이 셈이 두 벌이 되고, 두 벌은
	// 반드시 표류한다 — 그리고 이 축의 표류는 "토큰을 정확히 쳐도 폼이 다시 뜬다"로
	// 나타나서 원인이 폼 action 이라는 것이 증상에서 안 보인다.
	Action string
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

	// draining 은 **서버가 종료 중이다**는 사실이다. Drain() 이 닫고, 수명이 정해지지
	// 않은 응답(지금은 SSE 하나)이 이것으로 빠져나온다.
	//
	// ★ 이 사실을 **요청 컨텍스트로 나르지 않는다.** 그것이 이 자리의 결함이었다 —
	// BaseContext 가 serveCtx 였을 때 "서버가 종료 중이다"와 "이 요청은 그만둬도 된다"가
	// 한 값으로 접혔고, 그래서 드레인이 마무리 대신 절단이었다. 두 사실은 채널이 다르다.
	//
	// ★ **수명이 정해지지 않은 응답을 새로 만들면 이 채널을 봐야 한다.** 안 보면 그
	// 핸들러 하나가 셧다운 유예를 통째로 쓴다 — srv.Shutdown 은 요청 컨텍스트를 안 끊으므로
	// 스트림이 스스로 나가는 다른 길이 없다. 컴파일러가 이것을 못 잡는다(Go 에는 "이 채널을
	// select 해야 한다"를 강제할 수단이 없다). 대신 유예를 넘기면 ERROR 가 뜨고, 넘기지
	// 않아도 "서버 종료" 의 drain_ms 가 그 값이 자라는 것을 말한다.
	//
	// ★ 통지는 **select 에 앉아 있는 핸들러에게만** 닿는다. w.Write 안에서 막힌 스트림
	// (수신 윈도가 찬 죽은 피어)은 이 채널로도, 요청 컨텍스트 취소로도 못 깨운다 —
	// 오늘도 그렇고 고친 뒤에도 그렇다. 그것을 푸는 것은 유예 만료 뒤의 srv.Close() 하나다.
	// 그래서 유예 초과 ERROR 는 "계약 위반"과 "죽은 피어" 둘 다를 뜻할 수 있다
	// (후속: fd-sse-write-deadline-unbounded).
	draining  chan struct{}
	drainOnce sync.Once

	// loopbackSeen 은 **루프백으로 도달한 요청이 실제로 있었는가**다.
	//
	// ★ 설정만으로는 면제가 닿는지 알 수 없다. 컨테이너 배포에서는 호스트의
	// 127.0.0.1 요청이 브리지 게이트웨이(172.x)로 보여 IsLoopback 이 false 이고,
	// 그래서 면제를 받는 클라이언트가 **하나도 없는데** 설정은 열려 있다.
	// 그 어긋남을 /healthz 와 401 처방이 말하려면 이 관측이 있어야 한다.
	//
	// 단조 증가다(한 번 참이면 안 내려간다). 나중에 도달이 끊긴 것을 잡으려면
	// 마지막 시각을 재야 하는데, 이 축이 답하려는 질문은 "닿기는 하는가"이고
	// 거기에는 한 번의 관측이면 충분하다 — 켜졌다 꺼지는 값은 배너를 깜빡이게 한다.
	loopbackSeen atomic.Bool
}

// loopbackReach 는 설정과 관측을 합쳐 면제의 실제 도달 여부를 낸다.
func (s *server) loopbackReach() LoopbackReach {
	return LoopbackReach{
		Configured:  !s.opt.RequireTokenOnLoopback,
		Observed:    s.loopbackSeen.Load(),
		InContainer: s.opt.InContainer,
	}
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

// Drain 은 수명이 정해지지 않은 응답에 종료를 알린다. 여러 번 불러도 안전하다.
//
// ★ goroutine 도 채널도 **새로 만들지 않는다** — 닫기 한 번이다. 그래서 join 할 것이
// 없고 이 호출은 못 매달린다. 통지를 받은 핸들러가 실제로 나갔는지의 판정자는
// srv.Shutdown 하나다("커넥션이 유휴가 됐는가"). 여기서 세지 않는다 — 판정자가 둘이면
// 반드시 어긋나고, 그때 어느 쪽이 정본인지 말해 주는 것이 없다.
func (s *server) Drain() { s.drainOnce.Do(func() { close(s.draining) }) }

// Handler 는 조립된 표면이다 — http.Handler 이면서 **종료를 통지받는다.**
//
// Serve 가 http.Handler 를 안 받는 이유: SSE 는 요청 컨텍스트로 안 끝난다(이 축의 전부다).
// 통지 경로를 타입 단언(`if d, ok := h.(interface{ Drain() })`)으로 두면 누가 핸들러를
// 한 겹 감싸는 순간 **조용히** 사라지고, 그 결과는 "모든 종료가 유예를 통째로 쓴다"는
// 침묵한 회귀다. 원칙 ① 그대로 — 검사가 아니라 **넘길 수 없다는 부재**로 막는다.
type Handler interface {
	http.Handler
	Drain()
}

// surface 는 게이트 사슬과 종료 통지를 한 값으로 묶는다.
//
// ★ *server 에 ServeHTTP 를 달지 않는다. 달면 **사슬을 안 탄 핸들러**가 Handler 로
// 새는 길이 생긴다 — newServer 만 부르는 시험의 결과가 그대로 Handler 를 만족하게 되고,
// 컴파일은 통과하는데 인증·멱등·한도가 전부 빠진 표면이 조립된다.
type surface struct {
	http.Handler // 게이트 사슬
	srv          *server
}

func (f surface) Drain() { f.srv.Drain() }

// NewServer 는 라우팅과 게이트를 얹은 핸들러를 만든다.
//
// 라우팅은 표준 http.ServeMux 의 메서드·패턴 문법이다(Go 1.22+). 프레임워크가 없다 —
// 라우트 패턴 문자열이 그대로 액세스 로그와 /metrics 의 라벨이 되므로,
// 라우터가 그 문자열을 돌려주는 것 자체가 이 계층이 필요로 하는 기능의 전부다.
func NewServer(svc *service.Service, opt Options) Handler {
	s := newServer(svc, opt)
	s.mux = s.routes()
	return surface{Handler: s.chain(s.mux), srv: s}
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
		svc:      svc,
		log:      opt.Log,
		opt:      opt,
		draining: make(chan struct{}),

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
	mux.HandleFunc("GET /api/v1/sessions", s.handleFindSession)
	mux.HandleFunc("PATCH /api/v1/sessions/{id}", s.handlePatchSession)
	mux.HandleFunc("POST /api/v1/sessions/{id}/signals", s.handleSignal)
	mux.HandleFunc("POST /api/v1/sessions/{id}/workspaces", s.handleWorkspace)
	// 훅 전용 — /clear·compact 로 갈린 대화의 새 cc 를 카드에 반영한다.
	mux.HandleFunc("POST /api/v1/sessions/{id}/rekey", s.handleRekey)
	// 처방은 턴마다 돈다. **세션 카드 파생을 안 도는 표면이다** — /notices 와 같은 이유(설계 §6).
	mux.HandleFunc("POST /api/v1/sessions/{id}/prescriptions", s.handlePrescriptions)

	// 큐 — Q 계층.
	mux.HandleFunc("GET /api/v1/items/next", s.handleNextItem)
	mux.HandleFunc("POST /api/v1/items", s.handleAddItem)
	mux.HandleFunc("POST /api/v1/items/{id}/claim", s.handleClaimItem)
	mux.HandleFunc("POST /api/v1/items/{id}/claim/release", s.handleReclaimClaim)
	mux.HandleFunc("POST /api/v1/items/{id}/finish", s.handleFinishItem)
	mux.HandleFunc("POST /api/v1/items/{id}/move", s.handleMoveItem)
	// 선행 절단. DELETE 가 아니라 POST 인 이유는 handlers_items.go 의 cutAfterRequest 에 있다.
	mux.HandleFunc("POST /api/v1/items/{id}/after/cut", s.handleCutAfter)

	// 랜딩 레인 — 순서와 배타. 셋(서기·보고·이탈)이 한 라우트인 이유는 handlers_landing.go 에 있다.
	mux.HandleFunc("POST /api/v1/landing", s.handleLand)
	mux.HandleFunc("POST /api/v1/landing/rows/{id}/release", s.handleReleaseLaneRow)
	mux.HandleFunc("GET /api/v1/landing/queue", s.handleLandingQueue)

	// 판단 — J 계층. **추가 전용**이라 PUT·DELETE 가 없다.
	mux.HandleFunc("POST /api/v1/judgments", s.handleAddJudgment)
	mux.HandleFunc("GET /api/v1/judgments", s.handleSearchJudgments)

	// 발번·스냅숏.
	mux.HandleFunc("POST /api/v1/counters/{name}/next", s.handleCounter)
	mux.HandleFunc("GET /api/v1/snapshots/{key}", s.handleGetSnapshot)
	mux.HandleFunc("PUT /api/v1/snapshots/{key}", s.handlePutSnapshot)

	// 프로젝트 축. 화면 없이(fd project ls) 등록된 프로젝트와 그 실적을 보는 유일한 길이다.
	mux.HandleFunc("GET /api/v1/projects", s.handleListProjects)
	// 잔해 삭제 — 이 계획에서 유일하게 되돌릴 수 없는 표면이다. 안전판은 service 가 쥔다.
	mux.HandleFunc("POST /api/v1/projects/{id}/remove", s.handleRemoveProject)

	// 화면·알림·진단.
	mux.HandleFunc("GET /api/v1/dashboard.json", s.handleDashboard)
	mux.HandleFunc("GET /api/v1/notices", s.handleNotices) // 꼬리 전용. 세션 카드 파생을 안 돈다
	mux.HandleFunc("GET /api/v1/events", s.handleEvents)
	mux.HandleFunc("GET /events", s.handleEvents) // 짧은 별칭. 화면이 이걸 문다
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /metrics", s.handleMetrics)

	// 화면 로그인. **게이트 앞에서 갈라진다**(withAuth·withIdempotency 가 함께 보는
	// JudgePreSessionPath) — 로그인이 게이트 뒤에 있으면 로그인하려면 이미 로그인돼 있어야 한다.
	mux.HandleFunc("GET /login", s.handleLogin)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)

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
//	        screenWrite — 화면 폼의 출처를 대조하고 쿼리 키를 헤더로 올린다
//	          idempotency — 쓰기 재시도를 같은 결과로 접는다
//	            recover   — 패닉을 500 으로. 액세스 로그가 그 500 을 본다
//
// recover 가 accessLog **안쪽**인 이유: 바깥에 두면 패닉 요청의 액세스 로그가
// 상태코드 0 으로 남아 "무엇이 500 을 냈나"를 지표에서 못 찾는다.
//
// screenWrite 가 idempotency **바깥**인 이유: 안쪽에 두면 키가 헤더로 올라가기 전에
// 이미 400 이 나간 뒤다. accessLog 안쪽인 이유: 여기서 내는 403 도 로그에 남아야 한다.
func (s *server) chain(h http.Handler) http.Handler {
	return s.withRequestID(s.withRateLimit(s.withAuth(s.withAccessLog(s.withScreenWrite(s.withIdempotency(s.withRecover(s.withRelativeLocation(h))))))))
}

// ShutdownGrace 는 드레인이 인플라이트에 주는 시간이다.
//
// ★ 공개인 이유는 이 값이 **자기 갱신 반응 시간의 항**이기 때문이다
// (cmd/fd/selfwatch.go 의 selfUpdateReactionBudget 이 이 값을 더한다). 여기서만 알면
// 그쪽이 같은 숫자를 다시 적게 되고, 두 벌은 반드시 표류한다.
//
// **10초의 근거(2026-08-05 실측).** 정본 서버 액세스 로그 6시간(요청 2902건)에서
// 라우트별 소요를 갈랐다. 수명이 정해진 요청의 **관측 최댓값이 0.864초**이고
// (`GET /{$}` — 보드 파생 git 호출을 포함한 가장 무거운 자리), 가장 무거운 REST 는
// `GET /api/v1/items/next` p95 0.249초다. 유예는 그 최댓값의 **12배**다.
// 꼬리(p95 59.9초 · max 971초)는 **전부 `GET /events`** 였다 — 그것이 수명이 정해지지
// 않은 유일한 응답이고, 그래서 유예가 아니라 Drain() 통지로 나간다.
// 재측정(2026-08-07): 같은 라우트 max 0.864s(08-05)→2.757s(08-06)→1.313s(현 세대 17h·1678건) — 요동이지 추세가 아니다. 배수는 7.6배로 돌아왔다.
//
// ★ **액세스 로그가 못 보는 항이 하나 있다.** 요청 줄을 아직 한 줄도 안 보낸 커넥션
// (`StateNew`)은 로그에 원리적으로 안 남는데, `srv.Shutdown` 은 그런 커넥션을 **5초가
// 지나기 전에는 유휴로 안 친다**(net/http 의 규칙이다). 즉 말 없는 소켓 하나가 붙어 있으면
// 인플라이트가 0건이어도 드레인이 5초를 쓴다(실측 5.31초). 그래서 위 "12배 여유"는
// 요청 축의 값이고, 실제 여유는 그 항이 실재할 때 절반 이하다 — 유예는 아직 안 깨지지만
// **항을 다 셌다고 말하면 거짓이다.** 그리고 그 5초는 아래 drain_ms 에 인플라이트 대기와
// 구분 없이 합산된다(후속: fd-drain-ms-conflates-statenew-floor).
// ★ 컨테이너의 stop_grace_period 는 이 값보다 **커야 한다**(compose.yaml —
// TestComposeStopGraceExceedsShutdownGrace 가 그 부등식을 붙든다).
const ShutdownGrace = 10 * time.Second

// Listen 은 수신 주소를 연다. **바인드 성공을 값으로 낸다.**
//
// 포트 선점은 **사유를 남기고 종료한다**(설계 §7) — 조용히 실패하면
// 전 세션이 "서버 미도달"만 보고 원인을 모른다.
//
// ★ Serve 에서 뽑아낸 이유는 호출부가 "리스너가 실제로 열렸다"를 알아야 하기 때문이다.
// 배포 관측(cmd/fd 의 noteBuild)이 정확히 그 사실에 매달린다 — 뜨지도 못한 기동이 원장에
// 배포를 남기면 LastDeployAt 이 **한 번도 응답한 적 없는 바이너리**의 시각을 낸다.
//
// ★ **ready 콜백이 아니라 값이다.** 콜백을 받으면 시험이 넘길 수 있는 것이 nil 뿐이라
// 콜백 안이 안 잠긴다 — 이 저장소가 자기 갱신 축에서 이미 치른 값이다(2026-08-07 실측,
// cmd/fd 의 serveAPIOptions 주석). 값으로 내면 순서가 호출부에서 자명해지고, 그 순서를
// 어기는 것은 시험이 잡는다(cmd/fd 의 TestServeSkipsDeployNoteWhenBindFails).
//
// ★ ctx 를 받는 이유는 실패 로그가 ErrorContext 라서다 — 상관키를 잃지 않으려면 함께 온다.
func Listen(ctx context.Context, addr string, log *slog.Logger) (net.Listener, error) {
	if log == nil {
		log = slog.Default()
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.ErrorContext(ctx, "포트를 열 수 없다 — 이미 쓰고 있는 프로세스가 있는지 확인해라",
			"route", clip(addr, 120), "error", err.Error())
		return nil, fmt.Errorf("%s 를 열 수 없다: %w", addr, err)
	}
	return ln, nil
}

// Serve 는 핸들러를 **이미 열린 리스너**에 붙이고 ctx 가 끝날 때까지 돌린다.
//
// ★ 리스너 소유권을 가져간다 — srv.Serve 가 반환할 때 net/http 가 ln 을 닫으므로
// 호출부는 Listen 이 준 것을 넘긴 뒤 잊으면 된다. 정리 경로를 두 벌로 만들지 않는다.
//
// ★ h 가 http.Handler 가 아니라 Handler 인 이유는 Handler 의 독 코멘트에 있다.
func Serve(ctx context.Context, ln net.Listener, h Handler, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	// service.name 은 진입점이 이미 걸어 두었다 — 여기서 다시 걸면 한 줄에 두 번 찍힌다.

	// ★ 인플라이트 요청 컨텍스트를 ctx 에서 **뗀다.** 취소만 뗀다 —
	// middleware.go 의 멱등 기록 저장이 쓰는 것과 같은 관용구다.
	// (값은 안 옮겨진다: 이 ctx 에는 실린 값이 없다. 상관키는 requestID 미들웨어가
	// r.Context() 에 걸고 그것은 여기보다 하류다. 그 사실이 바뀌면 이 줄도 바뀐다.)
	//
	// 떼기 전에는 드레인 신호가 곧 절단이었다: BaseContext 가 serveCtx 라 취소 한 번에
	// 모든 r.Context() 가 죽었고, srv.Shutdown 은 **이미 다 죽은 뒤에** 도착해 기다릴
	// 대상이 없었다. 즉 Shutdown 을 부르면서 그것이 할 일을 먼저 없앤 배선이었다.
	//
	// ★ 인플라이트를 **유예를 주고** 끊는 자리는 아래 2단 하나이고 그때는 ERROR 로 말한다.
	// 다만 `defer cutInflight()` 는 모든 반환 경로에 걸려 있어서, 리스너가 스스로 죽는 갈래
	// (`case err := <-done`)에서는 유예 없이 끊긴다 — 그 갈래는 이 함수가 곧 반환하고
	// 살릴 리스너가 없다. 조용하지는 않다("서버가 멈췄다"가 원인을 낸다). 그 갈래를 유예로
	// 감싸지 않는 이유는 기다릴 이유가 없어서이지, 절단이 안 일어나서가 아니다.
	baseCtx, cutInflight := context.WithCancel(context.WithoutCancel(ctx))
	defer cutInflight()

	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		// WriteTimeout 을 걸지 않는다 — SSE 는 수명이 정해지지 않은 응답이라
		// 상한을 걸면 정상 구독이 주기적으로 끊긴다.
		IdleTimeout:    120 * time.Second,
		BaseContext:    func(net.Listener) context.Context { return baseCtx },
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
		// 리스너가 스스로 죽었다(포트 회수·fd 고갈). 매달린 스트림을 남길 이유가 없다 —
		// 여기서 기다리지는 않는다(기다릴 리스너가 이미 없다). 이 통지가 없으면 호출자가
		// 곧바로 store 를 닫는데 SSE 핸들러는 아직 살아서 닫힌 DB 를 만난다.
		h.Drain()
		if err != nil {
			log.ErrorContext(ctx, "서버가 멈췄다", "error", err.Error())
		}
		return err
	case <-ctx.Done():
		t0 := time.Now()

		// 1단 — 수명이 정해지지 않은 응답에 종료를 알린다.
		//
		// ★ **Shutdown 보다 먼저다.** srv.Shutdown 은 요청 컨텍스트를 안 끊고, 커넥션이
		// 유휴가 되는 길은 핸들러 반환 하나뿐이다. 이 통지가 없으면 SSE 구독 하나가
		// 아래 유예를 통째로 쓰고, 모든 종료·모든 자기 갱신이 그만큼 늦어진다.
		// ★ RegisterOnShutdown 을 안 쓴다: 그쪽은 `go f()` 라 순서 보장이 없고 Shutdown 이
		// 기다리지도 않는다 — join 할 자 없는 goroutine 을 하나 늘릴 뿐이다.
		h.Drain()

		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownGrace)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			// 2단 — 유예를 넘겼다. **여기서 처음으로** 인플라이트를 끊는다.
			//
			// cutInflight 를 Close 보다 먼저 부르는 이유: 요청이 띄운 자식(git 서브프로세스)까지
			// 걷히게 하려면 컨텍스트가 끊겨야 한다. Close 만 하면 커넥션은 닫히는데 핸들러와
			// 그 자식은 계속 돈다.
			//
			// ★ 그래도 이 함수는 핸들러 goroutine 을 **join 하지 않는다** — net/http 가 그 수단을
			// 안 준다. 그래서 반환 뒤 호출자가 store 를 닫으면 아직 도는 쿼리가
			// "database is closed" 로 죽을 수 있다. 절단 사유가 두 벌이 되는 것뿐이고 파손은
			// 아니지만, 이 갈래가 ERROR 로 표시되는 이유 중 하나다.
			log.ErrorContext(ctx, "유예 안에 마무리를 못 했다 — 인플라이트를 끊고 강제로 닫는다",
				"grace", ShutdownGrace.String(), "error", err.Error())
			cutInflight()
			if cerr := srv.Close(); cerr != nil {
				log.ErrorContext(ctx, "강제 종료도 실패했다", "error", cerr.Error())
			}
		}
		<-done
		// ★ drain_ms 를 남긴다. 성공한 자기 갱신은 exec 로 상태가 통째로 사라지므로
		// (selfwatch.go 의 "성공은 여기 안 남는다"), 이 한 줄이 **이 축을 사후에 잴 수 있는
		// 유일한 자리**다. 드레인이 2ms 에서 8초로 자라도 이것 없이는 아무 데도 안 남는다 —
		// ERROR 는 유예를 넘겼는지만 말하는 이진 판별이다.
		//
		// ★ **이 값은 두 사실을 접는다**(이 레포가 대체로 금지하는 것이다): "인플라이트가 그만큼
		// 걸렸다"와 "말 없는 StateNew 소켓이 net/http 의 5초 바닥을 밟았다"가 안 갈린다.
		// 지금 가르지 않는 이유는 net/http 가 그 구분을 밖으로 안 내주기 때문이고, 그 사실을
		// 여기 적어 두는 것이 지금 할 수 있는 전부다. 5초 근처의 drain_ms 는 그 바닥을 먼저 의심해라.
		//
		// ★ 이 줄은 stderr 뿐이라 **컨테이너 세대와 함께 지워진다**(제거 시 docker 가 json 로그를
		// 지운다 — 2026-08-06 하루 7세대 실측). 세대를 넘겨 남기는 기구는 일부러 없다: 자랄
		// 원천(≥5초 침묵 소켓)이 실측 0건이다(fd-shutdown-log-dies-with-container-generation).
		log.InfoContext(ctx, "서버 종료", "drain_ms", time.Since(t0).Milliseconds())
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
