package api

import (
	"io"
	"net/http"
	"strings"
	"time"

	"context"
	"errors"
	"github.com/kweiza/flightdeck/internal/buildinfo"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// 화면·알림·진단 표면.

// LoopbackReach 는 루프백 면제가 **실제로 닿는가**에 대한 관측이다.
//
// ★ 설정과 관측을 가르는 이유가 실물 사고다(2026-08-05). 서버를 컨테이너로 띄우면
// 호스트에서 온 요청의 원격 주소가 브리지 게이트웨이(172.x)이고 IsLoopback 이 false 다 —
// **면제를 받는 클라이언트가 하나도 없다.** 그런데 설정값만 옮기는 표면은 그 상황에서도
// "루프백 요청만 토큰 없이 통과한다"를 낸다. 운영자는 그것을 믿고 클라이언트에 토큰을
// 안 준 채 전환하고, 첫 쓰기에서 전면 차단을 만난다. 실제로 그렇게 됐고 대응은
// 인증을 통째로 끄는 것이었다.
//
// 참인 설정을 말하면서 거짓인 결론을 읽게 하는 문장은 침묵보다 나쁘다 —
// buildinfo.Coord 의 Known 과 SelfUpdateStatus 의 Watching 이 같은 규율이다.
type LoopbackReach struct {
	// Configured 는 설정상 면제가 열려 있는가다(= !RequireTokenOnLoopback).
	Configured bool
	// Observed 는 이 서버에 루프백으로 **실제로 도달한 요청**이 있었는가다.
	//
	// ★ 면제가 발동했는가가 아니라 도달했는가다. 다들 토큰을 실어서 면제가 한 번도
	// 안 걸리는 것은 정상이고, 도달 자체가 0인 것만이 배선 결함이다.
	Observed bool
	// InContainer 는 안 닿는 **사유를 아는 경우** 그 사실이다. 배선이 판정해 준다.
	//
	// 문구가 아니라 불리언인 이유: 사람이 읽을 문장은 표면의 몫이고, 배선이 문자열을
	// 넘기면 self_update 축의 문구("자기 갱신을 안 한다")가 인증 문맥으로 새어 든다.
	InContainer bool
}

// open 은 면제가 **실제로 열려 있는가**다. 설정과 관측이 둘 다 참일 때만 참이다.
func (r LoopbackReach) open() bool { return r.Configured && r.Observed }

// AuthStatus 는 /healthz 가 알리는 인증 상태다.
//
// ★ **조용한 무인증은 안 된다.** 토큰이 설정되지 않았다는 사실과 루프백 면제가
// 켜져 있다는 사실이 여기 없으면, 운영자는 서버가 열려 있다는 것을 알 방법이 없다.
//
// ★ LoopbackOpen 과 LoopbackConfigured 를 **한 필드로 접지 마라.** 접으면
// "면제를 껐다"와 "면제는 켰는데 아무도 못 받는다"가 같은 false 로 보이고,
// 그 둘은 처방이 정반대다(전자는 의도한 상태, 후자는 배선 결함이다).
type AuthStatus struct {
	TokenSet bool `json:"token_set"`
	// LoopbackOpen 은 **관측**이다 — 설정이 열려 있고 실제로 루프백 도달이 있었을 때만 참.
	LoopbackOpen bool `json:"loopback_open"`
	// LoopbackConfigured 는 설정값 그대로다.
	LoopbackConfigured bool   `json:"loopback_configured"`
	Notice             string `json:"notice"`
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
	// Build 는 **응답하는 이 프로세스**의 빌드 좌표다. 파일의 것이 아니다.
	//
	// ★ api_version 으로는 판 나이를 못 나른다 — 그 값은 계약이 깨질 때만 오르므로
	// 판이 수십 커밋 벌어져도 "1" == "1" 이다. 그 구간에서 응답의 축 하나가 조용히
	// 사라지는 것이 실제로 관측됐다(fd-binary-vintage-has-no-signal).
	//
	// 좌표 누출이 아니다 — sha 는 이 저장소를 읽을 수 있는 사람에게만 의미가 있고,
	// db_path 처럼 서버의 파일 배치를 알리지 않는다.
	Build buildinfo.Coord `json:"build"`
	// SelfUpdate 는 이 서버가 자기 판을 따라가고 있는가다.
	SelfUpdate SelfUpdateStatus `json:"self_update"`
}

// SelfUpdateStatus 는 서버의 자동 갱신 축이다.
//
// ★ **Watching 이 먼저다.** 감시기가 안 떴는데 나머지가 비어 있으면
// "아직 갱신이 없었다"로 읽힌다 — 그것은 "안 보고 있다"와 전혀 다르다.
// buildinfo.Coord 의 Known 이 같은 규율이다.
//
// **성공은 여기 안 남는다.** 성공하면 프로세스가 갈아치워져 새 프로세스는 그 사실을 모른다.
type SelfUpdateStatus struct {
	Watching bool       `json:"watching"`
	Reason   string     `json:"reason,omitempty"`
	LastAt   *time.Time `json:"last_at,omitempty"`
	From     string     `json:"from,omitempty"`
	To       string     `json:"to,omitempty"`
	Outcome  string     `json:"outcome,omitempty"` // refused | failed
	Detail   string     `json:"detail,omitempty"`

	// ★ Stalled 는 **보고는 있는데 지금 실행 파일을 못 잰다**는 사실이다.
	// Watching=true 인데 이것이 차 있으면 "따라가는 중"이 아니라 "눈이 멀었다"다 —
	// 그 둘을 안 가르면 지워진 바이너리를 감시하는 서버가 화면에서는 정상으로 보인다.
	Stalled string `json:"stalled,omitempty"`
}

// AuthNotice 는 지금 인증 상태를 사람이 읽을 한 줄로 만든다. 순수 함수다.
//
// ★ 갈래 순서가 계약이다. **면제가 꺼진 경우를 관측보다 먼저 본다** — 끈 서버는
// 도달이 0이어도 정상이고, 그것을 "안 닿는다"로 말하면 의도한 설정이 결함으로 읽힌다.
func AuthNotice(tokenSet bool, reach LoopbackReach) string {
	switch {
	case !tokenSet:
		return "토큰이 설정되지 않았다 — 이 서버는 전 요청을 무인증으로 통과시킨다"
	case !reach.Configured:
		return "토큰이 설정돼 있다. 루프백에도 토큰이 필요하다"
	case reach.Observed:
		return "토큰이 설정돼 있다. 루프백 요청만 토큰 없이 통과한다"
	case reach.InContainer:
		return "토큰이 설정돼 있다. 루프백 면제는 설정상 열려 있으나 이 서버에 루프백으로 도달한 요청이 없다 — " +
			"컨테이너라서다(/.dockerenv). 호스트에서 오는 요청은 브리지 게이트웨이로 보이므로 루프백이 아니다. " +
			"클라이언트도 토큰이 필요하다"
	default:
		return "토큰이 설정돼 있다. 루프백 면제는 설정상 열려 있으나 이 서버에 루프백으로 도달한 요청이 아직 없다 — " +
			"면제를 받는 클라이언트가 하나도 없을 수 있다. 클라이언트에 토큰을 실어라"
	}
}

// UnauthorizedGuidance 는 401 을 받은 사람에게 **무엇을 하면 되는지**를 낸다. 순수 함수다.
//
// ★ 이 문구가 관측을 따라야 하는 이유가 실물 사고다. 앞선 판은 조건 없이
// "루프백은 토큰 없이 통과한다(/healthz 가 그 설정을 알린다)" 를 붙였고, 그래서
// **루프백에서 401 을 맞은 응답이 자기 자신을 반박했다** — 401 을 낸 그 응답이
// 401 이 나면 안 된다고 안내한 셈이다. 원인을 찾던 세션이 그 문장 때문에
// 배선(도커 브리지)이 아니라 자기 토큰 설정을 의심했다.
//
// 거짓을 지우면서 **처방까지 지우지 않는다** — 어느 갈래든 Bearer 지시는 남는다.
func UnauthorizedGuidance(reach LoopbackReach) string {
	const base = "Authorization: Bearer <token> 을 실어라."
	switch {
	case !reach.Configured:
		return base + " 이 서버는 루프백에도 토큰을 요구한다."
	case reach.Observed:
		return base + " 루프백은 토큰 없이 통과한다(/healthz 가 그 설정을 알린다)."
	case reach.InContainer:
		return base + " 루프백 면제는 설정상 열려 있으나 이 서버는 컨테이너라 호스트에서 오는 요청이" +
			" 루프백으로 안 보인다 — 면제를 기대하지 마라(/healthz 의 auth 절이 그 관측을 낸다)."
	default:
		return base + " 루프백 면제는 설정상 열려 있으나 이 서버에 루프백으로 도달한 요청이 아직 없다" +
			" — 면제를 기대하지 마라(/healthz 의 auth 절이 그 관측을 낸다)."
	}
}

// HealthzOf 는 내부 상태를 무인증으로 내보내도 되는 형태로 옮긴다. 순수 함수다.
//
// 오류 **전문**을 고정 문구로 갈아 끼운다. 그 문자열에는 DB 파일 경로와
// 드라이버 이름이 섞여 들어오고, 그것을 그대로 내면 걷어낸 db_path 가
// 다른 필드로 되살아난다 — 실제로 그 모양의 누출이 흔하다.
// ★ build 를 **인자로 받는다.** service.Health 에 필드를 더하지 않는 이유가 둘이다 —
// 빌드 좌표는 DB·디스크 상태가 아니라 프로세스의 성질이고, 인자로 받으면 이 함수가
// tokenSet·loopbackOpen 과 같은 모양으로 순수하게 남아 시험이 값을 직접 준다.
// su 도 같은 이유로 인자다 — 핸들러가 body 에 따로 얹으면 순수 함수가 불완전한 body 를
// 만들고 그 시험은 통과한다. 실제 응답과 갈리는 그 모양을 이 저장소가 반복해서 문제 삼았다.
func HealthzOf(h service.Health, tokenSet bool, reach LoopbackReach, build buildinfo.Coord, su SelfUpdateStatus) HealthzBody {
	b := HealthzBody{
		OK: h.OK, APIVersion: h.APIVersion, DBOK: h.DBOK, Build: build, SelfUpdate: su,
		DiskFreePct: h.DiskFreePct, DiskKnown: h.DiskKnown, At: h.At,
		Auth: AuthStatus{
			TokenSet: tokenSet, LoopbackOpen: reach.open(),
			LoopbackConfigured: reach.Configured,
			Notice:             AuthNotice(tokenSet, reach),
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
	// buildinfo.Self 는 파일이 아니라 **이 프로세스**를 읽는다. 실측에서 설치 파일은 이미
	// 최신으로 교체돼 있는데 프로세스는 지워진 아이노드의 옛 코드를 돌고 있었다 —
	// 파일을 재는 진단은 그 상황에서 "최신"이라 답한다.
	//
	// s.opt.SelfUpdate 가 nil 인 것은 정상 갈래다(시험 조립·다른 진입점이 안 줄 수 있다).
	// 그때 빈 구조체를 내면 "아직 갱신이 없었다"로 읽히는데 그것은 "배선이 안 됐다"와
	// 전혀 다르다 — 그래서 사유를 채운 값으로 대신한다.
	su := SelfUpdateStatus{Watching: false, Reason: "이 서버는 자동 갱신 축을 배선하지 않았다"}
	if s.opt.SelfUpdate != nil {
		su = s.opt.SelfUpdate()
	}
	body := HealthzOf(h, s.opt.Token != "", s.loopbackReach(), buildinfo.Self(), su)
	status := http.StatusOK
	if !body.OK {
		status = http.StatusServiceUnavailable
	}
	s.writeJSON(w, r, status, body)
}

// handleMetrics 는 프로메테우스 텍스트 포맷을 직접 쓴다(의존성 0).
func (s *server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	snap := s.met.snapshot(s.hub.Count(), s.hub.Dropped())
	// 파생 비용은 service 계층이 센다(비싼 일이 거기 있으므로). 표면은 옮기기만 한다.
	// svc 가 nil 인 조립이 시험에 있으므로 그 축은 0 으로 남긴다 — 0 과 "안 잰다"를
	// 여기서 가르지 않는 이유는, 그 조립에는 파생을 돌릴 경로 자체가 없어서다.
	if s.svc != nil {
		d := s.svc.DeriveStats()
		snap.DeriveRuns, snap.DeriveCards, snap.DeriveSeconds = d.Runs, d.Cards, d.Seconds
	}
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

// handleNotices 는 **응답 꼬리에 실을 알림만** 낸다(최근 ask·blocked).
//
// ★ 이 표면이 왜 따로 있나. MCP 도구는 응답마다 꼬리에 미확인 알림을 붙이는데(설계 §6),
// 그 값을 가져올 표면이 dashboard.json 뿐이었다. 그래서 **도구 호출 1회마다**
// 세션 카드 파생(git worktree list · ChangedPaths · UncommittedPaths)이 통째로 한 번 더 돌았다.
// 지금은 8~60ms 라 무해하지만, 세션·워크트리가 늘면 모든 도구 응답 지연에
// 저장소 전수 훑기가 얹힌다.
//
// dashboard.json 에 "파생을 건너뛰는 인자"를 더하는 쪽도 정당했지만 이쪽을 골랐다.
// 이유는 **비대칭이 이미 결함을 가리키고 있어서**다: service 계층에는 이 축만 여는
// RecentNotes 가 진작 있었고(그 함수 주석이 바로 이 비용을 이유로 든다),
// 없는 것은 그것을 밖으로 내는 REST 표면 하나뿐이었다. 인자를 더하면
// "무엇을 부르는가"가 여전히 화면 표면이라 /metrics 의 라우트 라벨에서 두 트래픽이
// 계속 한 덩어리로 남는다 — 표면을 가르면 그 라벨이 그대로 계측 축이 된다.
func (s *server) handleNotices(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireQuery(w, r, "project", "알림은 프로젝트 안에서만 좌표가 있다.")
	if !ok {
		return
	}
	limit, err := queryInt(r, "limit", 0)
	if err != nil {
		s.writeError(w, r, badRequest("bad_limit", "limit 이 정수가 아니다", "예: limit=20"))
		return
	}
	// **거르지 않은 것을 낸다.** "내가 쓴 것은 알림이 아니다"는 표시 계층의 판정이고
	// (mcpsrv.FilterNotes), 그 축을 여기서 접으면 같은 목록을 다른 '나'로 다시 볼 수 없다.
	// 그래서 self·session_id 인자를 두지 않는다 — 결과를 안 바꾸는 인자는 만들지 않는다.
	notes, err := s.svc.RecentNotes(r.Context(), project, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"notes": notes})
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
	//
	// ★ **무엇을 구독했는지 함께 말한다.** 범위를 안 말하면 뒤이은 침묵이
	// "아무 일도 없다"인지 "필터가 아무것도 안 맞춘다"인지 구분되지 않는다 —
	// 프로젝트 이름 오타 하나로 영원히 조용한 스트림을 정상으로 읽게 된다.
	// 알려진 프로젝트인지도 함께 본다: 모르는 이름이면 그 사실이 첫 줄에 뜬다.
	if _, err := io.WriteString(w, ": connected "+s.subscriptionScope(r.Context(), project)+"\n\n"); err != nil {
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

// subscriptionScope 는 이 구독이 무엇을 받는지 한 줄로 말한다.
//
// 침묵과 부재를 가르기 위한 것이다 — 스트림이 조용할 때 그것이 "일이 없다"인지
// "이 필터에 맞는 것이 애초에 없다"인지는 이 줄이 없으면 답할 수 없다.
func (s *server) subscriptionScope(ctx context.Context, project string) string {
	if project == "" {
		return "— 전 프로젝트(필터 없음)"
	}
	if s.st == nil {
		return "— 프로젝트 " + clip(project, 64)
	}
	if _, err := s.st.GetProject(ctx, project); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "— 프로젝트 " + clip(project, 64) +
				" (★ 등록되지 않은 이름이다 — 이 구독은 아무것도 받지 못한다. 오타이거나 아직 안 만든 프로젝트다)"
		}
		return "— 프로젝트 " + clip(project, 64) + " (등록 여부를 확인하지 못했다)"
	}
	return "— 프로젝트 " + clip(project, 64)
}
