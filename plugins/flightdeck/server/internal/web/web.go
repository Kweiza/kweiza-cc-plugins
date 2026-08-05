package web

import (
	"bytes"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/service"
)

//go:embed dashboard.gohtml
var files embed.FS

// tpl 은 기동 시 한 번 파싱한다. 파싱 실패는 요청 시각이 아니라 **여기서** 죽어야 한다 —
// 요청 때 죽으면 대시보드가 필요한 순간에만 없어진다.
var tpl = template.Must(template.New("dashboard.gohtml").Funcs(template.FuncMap{
	// ★ 함수는 이것 하나다. **이스케이프를 끄는 함수(template.HTML 반환)를 절대 들이지 않는다** —
	// 하나 있으면 그것을 쓰는 자리가 반드시 생기고, 그 순간 저장 XSS 가 열린다.
	"join": func(sep string, xs []string) string { return strings.Join(xs, sep) },
}).ParseFS(files, "dashboard.gohtml"))

const (
	// defaultRefresh 는 SSE 가 없을 때의 폴백 주기(초)다.
	// 짧게 잡으면 읽는 도중에 화면이 사라지고, 길게 잡으면 조정 화면이 낡는다.
	defaultRefresh = 30

	// defaultSSEPath 는 서버의 SSE 엔드포인트다(설계 §2 의 GET /events).
	// **없어도 페이지가 성립한다** — 붙지 않으면 폴백 주기로 새로고침한다.
	//
	// ★ **상대경로다.** 절대경로(`/events`)로 두면 이 화면이 리버스 프록시의
	// 경로 접두(`/dcp-dev-board/` 같은 것) 뒤에 놓였을 때 브라우저가 원점의 `/events` 를
	// 찾아가고, 프록시는 그 경로를 모르므로 구독이 조용히 실패한다.
	// 그러면 **화면은 멀쩡히 뜨는데 영원히 안 갱신되고**, 스트림이 안 열렸으니
	// 메타 리프레시 폴백도 안 켜진다 — 이 화면이 앞서 한 번 죽었던 바로 그 모양이다.
	//
	// 이 페이지의 폼·링크는 이미 전부 상대경로다(`./`·`actions/drop`·`?project=`).
	// SSE 만 절대경로면 **같은 페이지 안에서 두 규칙이 공존**하게 되고, 그 비대칭이 결함이었다.
	// 상대경로는 접두 유무와 무관하게 맞는 자리를 가리킨다 — 단 접두 뒤에서는
	// 프록시가 뒤 슬래시를 붙여 줘야 한다(폼도 같은 조건이라 새 제약이 아니다).
	defaultSSEPath = "events"
)

// handler 는 대시보드 한 벌이다.
type handler struct {
	svc     *service.Service
	log     *slog.Logger
	now     func() time.Time
	refresh int
	ssePath string
	mux     *http.ServeMux
}

// Option 은 선택 설정이다.
type Option func(*handler)

// WithLogger 는 로거를 바꾼다. nil 은 무시한다.
func WithLogger(l *slog.Logger) Option {
	return func(h *handler) {
		if l != nil {
			h.log = l
		}
	}
}

// WithClock 은 시계를 바꾼다(시험이 나이 표시를 고정할 때 쓴다). nil 은 무시한다.
func WithClock(f func() time.Time) Option {
	return func(h *handler) {
		if f != nil {
			h.now = f
		}
	}
}

// WithRefresh 는 폴백 새로고침 주기(초)를 바꾼다. 0 이하는 무시한다.
func WithRefresh(sec int) Option {
	return func(h *handler) {
		if sec > 0 {
			h.refresh = sec
		}
	}
}

// WithSSEPath 는 SSE 경로를 바꾼다. **빈 문자열이면 SSE 를 아예 안 건다** —
// 그때도 페이지는 폴백 주기로 새로고침하며 그대로 성립한다.
func WithSSEPath(p string) Option {
	return func(h *handler) { h.ssePath = p }
}

// Handler 는 읽기 전용 대시보드를 낸다.
//
// 라우트는 넷뿐이다: GET / (한 장) · POST actions/reclaim · POST actions/drop ·
// POST actions/lane-release.
//
// 쓰기가 셋인 이유는 설계 §6 의 버튼 다섯 중 **뒤 둘**만 여전히 Tier B 이기
// 때문이다 — 줄 행 회수는 이 서버가 하는 일이라 열렸고(자동 만료가 없으므로 사람이
// 푸는 유일한 길이다), 정지/재개와 잡 우회 기록은 러너의 일이라 아직 없다.
func Handler(svc *service.Service) http.Handler { return New(svc) }

// New 는 옵션을 받는 생성자다.
func New(svc *service.Service, opts ...Option) http.Handler {
	h := &handler{
		svc: svc,
		// ★ service.name 을 여기서 덧칠하지 않는다 — 그 필드는 프로세스 진입점 하나가 건다.
		//   라이브러리가 각자 덧칠하면 JSON 한 줄에 같은 키가 두 번 들어가고,
		//   중복 키의 처리는 파서마다 다르다.
		log:     slog.Default(),
		now:     func() time.Time { return time.Now().UTC() },
		refresh: defaultRefresh,
		ssePath: defaultSSEPath,
	}
	for _, o := range opts {
		o(h)
	}
	h.mux = http.NewServeMux()
	h.mux.HandleFunc("GET /{$}", h.dashboard)
	h.mux.HandleFunc("POST /actions/reclaim", h.reclaim)
	h.mux.HandleFunc("POST /actions/drop", h.drop)
	h.mux.HandleFunc("POST /actions/lane-release", h.laneRelease)
	h.mux.HandleFunc("/", h.notFound)
	return h
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

func (h *handler) notFound(w http.ResponseWriter, r *http.Request) {
	// 4xx 는 로그 의무가 없다(경로 오타 한 번에 로그가 증폭된다).
	//
	// ★ **요청 경로를 본문에 되비추지 않는다.** 같은 서버의 api 쪽 404(handleUnmatched)가
	//   그 규율로 서 있는데 여기만 반대면, 그 비대칭 자체가 결함의 신호다 —
	//   지금은 text/plain + nosniff 라 XSS 가 아니지만, 이 응답이 한 번 HTML 로 바뀌거나
	//   누가 이 문자열을 다른 표면으로 옮기는 날 그 자리가 반사형 노출 통로가 된다.
	//   그리고 "어느 경로였나"는 소비자가 이미 안다 — 자기가 친 것이다.
	http.Error(w, "flightdeck 대시보드에 그런 경로가 없다.\n대시보드는 / 한 장이다.",
		http.StatusNotFound)
}

// dashboard 는 화면 한 장을 낸다.
func (h *handler) dashboard(w http.ResponseWriter, r *http.Request) {
	start := h.now()
	q := r.URL.Query()
	page := h.buildPage(r.Context(), pageRequest{
		project: strings.TrimSpace(q.Get("project")),
		query:   Clip(q.Get("q"), 200),
		notice:  NoticeText(q.Get("notice"), q.Get("item")),
	})

	status := http.StatusOK
	if page.NotFound {
		status = http.StatusNotFound
	}
	h.render(r, w, page, status)
	h.log.InfoContext(r.Context(), "request served",
		"route", "GET /", "status", status,
		"duration", h.now().Sub(start).Seconds(),
		// ★ 이름을 바꿨다. 이 값은 이제 "살아 있는 세션 수"가 아니라 **선점을 든 카드 수**다
		// (섹션 ①이 선점을 필터로 쓴다). 옛 이름으로 두면 로그·경보가 카드 급감을
		// 서버 장애로 읽는다 — 이 자리는 이 변경에서 시험이 없는 유일한 관측 축이라
		// 회귀로도 안 잡힌다.
		"claimed_cards", len(page.Live.Sessions))
}

// render 는 버퍼에 다 찍은 뒤에만 내보낸다.
//
// ★ 스트림에 직접 찍으면 템플릿이 중간에 실패했을 때 **반쪽 HTML 이 200 으로 나간다**.
// 그러면 화면은 그럴듯한데 아래쪽 패널만 통째로 없고, 그 상태가 "막힘 0건"으로 읽힌다.
func (h *handler) render(r *http.Request, w http.ResponseWriter, page Page, status int) {
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, page); err != nil {
		h.log.ErrorContext(r.Context(), "대시보드 렌더 실패",
			"route", "GET /", "error", err.Error())
		http.Error(w, "대시보드를 렌더하지 못했다. 서버 로그의 원인 전문을 보라.",
			http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		// 클라이언트가 끊은 경우가 대부분이라 WARN 이다. 다만 삼키지 않는다 —
		// 삼키면 "안 보낸 것"과 "보냈는데 끊긴 것"이 구분되지 않는다.
		h.log.WarnContext(r.Context(), "대시보드 응답 전송 중단",
			"route", "GET /", "error", err.Error())
	}
}
