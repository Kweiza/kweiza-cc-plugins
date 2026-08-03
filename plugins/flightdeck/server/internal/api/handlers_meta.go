package api

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/service"
)

// 화면·알림·진단 표면.

// AuthStatus 는 /healthz 가 알리는 인증 설정이다.
//
// ★ **조용한 무인증은 안 된다.** 토큰이 설정되지 않았다는 사실과 루프백 면제가
// 켜져 있다는 사실이 여기 없으면, 운영자는 서버가 열려 있다는 것을 알 방법이 없다.
type AuthStatus struct {
	TokenSet     bool   `json:"token_set"`
	LoopbackOpen bool   `json:"loopback_open"`
	Notice       string `json:"notice"`
}

// HealthzBody 는 /healthz 응답이다.
//
// ★ service.Health 를 그대로 내지 않는 **유일한** 자리다. 그 타입은 db_path 와
// 하류 오류 전문을 들고 있는데 이 표면은 인증 앞에 있다(배너 훅이 토큰 오설정
// 상황에서도 물어야 하므로). 서버의 파일 경로가 무인증으로 새는 것은
// "오류 응답에 내부 이름을 넣지 마라"와 같은 규율이고, 그래서 여기서 걷어낸다.
type HealthzBody struct {
	OK          bool       `json:"ok"`
	APIVersion  string     `json:"api_version"`
	DBOK        bool       `json:"db_ok"`
	DBError     string     `json:"db_error,omitempty"`
	DiskFreePct float64    `json:"disk_free_pct"`
	DiskKnown   bool       `json:"disk_known"`
	DiskError   string     `json:"disk_error,omitempty"`
	At          time.Time  `json:"at"`
	Auth        AuthStatus `json:"auth"`
}

// AuthNotice 는 지금 설정을 사람이 읽을 한 줄로 만든다. 순수 함수다.
func AuthNotice(tokenSet, loopbackOpen bool) string {
	switch {
	case !tokenSet:
		return "토큰이 설정되지 않았다 — 이 서버는 전 요청을 무인증으로 통과시킨다"
	case loopbackOpen:
		return "토큰이 설정돼 있다. 루프백 요청만 토큰 없이 통과한다"
	default:
		return "토큰이 설정돼 있다. 루프백에도 토큰이 필요하다"
	}
}

// HealthzOf 는 내부 상태를 무인증으로 내보내도 되는 형태로 옮긴다. 순수 함수다.
//
// 오류 **전문**을 고정 문구로 갈아 끼운다. 그 문자열에는 DB 파일 경로와
// 드라이버 이름이 섞여 들어오고, 그것을 그대로 내면 걷어낸 db_path 가
// 다른 필드로 되살아난다 — 실제로 그 모양의 누출이 흔하다.
func HealthzOf(h service.Health, tokenSet, loopbackOpen bool) HealthzBody {
	b := HealthzBody{
		OK: h.OK, APIVersion: h.APIVersion, DBOK: h.DBOK,
		DiskFreePct: h.DiskFreePct, DiskKnown: h.DiskKnown, At: h.At,
		Auth: AuthStatus{
			TokenSet: tokenSet, LoopbackOpen: loopbackOpen,
			Notice: AuthNotice(tokenSet, loopbackOpen),
		},
	}
	if h.DBError != "" {
		b.DBError = "DB 에 접속할 수 없다 — 원인 전문은 서버 로그에 있다"
	}
	if h.DiskError != "" {
		b.DiskError = "디스크 여유를 재지 못했다 — 원인 전문은 서버 로그에 있다"
	}
	return b
}

// handleHealthz 는 서버 상태와 **인증 설정**을 알린다. 인증 게이트 앞에 있다.
func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	h := s.svc.Health(r.Context())
	body := HealthzOf(h, s.opt.Token != "", !s.opt.RequireTokenOnLoopback)
	status := http.StatusOK
	if !body.OK {
		status = http.StatusServiceUnavailable
	}
	s.writeJSON(w, r, status, body)
}

// handleMetrics 는 프로메테우스 텍스트 포맷을 직접 쓴다(의존성 0).
func (s *server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	snap := s.met.snapshot(s.hub.Count(), s.hub.Dropped())
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, RenderMetrics(snap)); err != nil {
		s.log.WarnContext(r.Context(), "지표 응답 쓰기 실패", "error", err.Error())
	}
}

// handleDashboard 는 화면 한 장분의 값을 낸다.
//
// 읽기 전용이다. 파생물에 손을 대는 표면을 열면 대시보드가 다시 손 기재 저장소가 되고,
// 그 순간 이 제품이 없애려던 병목 1위가 부활한다(설계 §6).
func (s *server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireQuery(w, r, "project", "어느 프로젝트의 화면인지 없이는 좌표가 없다.")
	if !ok {
		return
	}
	window, err := queryDuration(r, "window", 0)
	if err != nil {
		s.writeError(w, r, badRequest("bad_window", "window 가 구간 표기가 아니다", "예: window=8h"))
		return
	}
	queue, err := queryBool(r, "queue", true)
	if err != nil {
		s.writeError(w, r, badRequest("bad_queue", "queue 가 불리언이 아니다", "예: queue=false"))
		return
	}
	notes, err := queryBool(r, "notes", true)
	if err != nil {
		s.writeError(w, r, badRequest("bad_notes", "notes 가 불리언이 아니다", "예: notes=false"))
		return
	}
	limit, err := queryInt(r, "note_limit", 0)
	if err != nil {
		s.writeError(w, r, badRequest("bad_note_limit", "note_limit 이 정수가 아니다", "예: note_limit=20"))
		return
	}
	self := strings.TrimSpace(r.URL.Query().Get("self"))
	infoFrom(r.Context()).setSession(self)

	view, err := s.svc.Board(r.Context(), project, service.BoardOptions{
		Window: window, Self: self, IncludeQueue: queue, IncludeNotes: notes, NoteLimit: limit,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, view)
}

// handleEvents 는 SSE 구독이다.
//
// ★ 하트비트 주석 줄을 주기적으로 보낸다. 프록시와 로드밸런서는 조용한 연결을
// 유휴로 보고 끊는데, 그러면 "이벤트가 안 온다"와 "연결이 끊겼다"가 구분되지 않는다.
//
// ★ 클라이언트가 끊으면 이 핸들러가 **반드시 반환한다**. 반환 경로가 하나뿐이도록
// 구독 해제를 defer 에 두고, 쓰기 실패도 전부 반환으로 접는다 —
// 여기서 새는 고루틴은 세션 수만큼 누적되고 그 사실은 어디에도 안 남는다.
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx 가 버퍼링하면 하트비트가 죽는다
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	subscriber := s.hub.Subscribe(project)
	defer s.hub.Unsubscribe(subscriber)

	// 첫 줄을 즉시 보낸다 — 구독이 성립했다는 사실 자체가 소비자에게 필요한 신호다.
	if _, err := io.WriteString(w, ": connected\n\n"); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		s.log.WarnContext(r.Context(), "SSE 플러시 실패 — 구독을 끊는다", "error", err.Error())
		return
	}

	beat := time.NewTicker(s.opt.Heartbeat)
	defer beat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return // 클라이언트가 끊었다
		case <-beat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		case frame, ok := <-subscriber.ch:
			if !ok {
				return // 허브가 닫았다(서버 종료)
			}
			if _, err := w.Write(frame); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}

// handleUnmatched 는 라우트를 못 맞춘 요청에 JSON 으로 답한다.
//
// 기본 404 는 평문이라 클라이언트의 오류 파싱 경로가 두 벌이 된다.
// 그리고 **경로를 응답에 되비추지 않는다** — 되비추면 그것이 반사형 노출 통로가 된다.
func (s *server) handleUnmatched(w http.ResponseWriter, r *http.Request) {
	s.writeError(w, r, Classified{
		Status: http.StatusNotFound, Code: "no_route",
		Message:  "그런 표면이 없다",
		Guidance: "정본 표면은 /api/v1/ 아래에 있다. 목록은 설계 §6 의 REST 표다.",
	})
}
