package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/kweiza/flightdeck/internal/store"
)

// 게이트와 관측.
//
// ★ 이 파일의 규율 하나: **401·429 는 액세스 로그에 줄을 내지 않는다.**
// 인증 실패와 한도 초과는 정의상 반복해서 오는 요청이라 건별로 남기면
// 초과 트래픽이 그대로 로그 증폭이 된다. 그 축의 정본은 /metrics 의
// flightdeck_unauthorized_total · flightdeck_rate_limited_total 이다.

type ctxKey int

const infoKey ctxKey = 1

// reqInfo 는 요청 1건의 상관 정보다. 액세스 로그와 오류 본문이 같은 값을 쓴다.
//
// SessionID 는 핸들러가 본문을 읽은 **뒤에** 채운다 — 미들웨어는 본문의 스키마를
// 모르기 때문이다. 이 값이 없으면 "어느 세션이 이 500 을 맞았나"에 답할 자리가 없다.
type reqInfo struct {
	mu        sync.Mutex
	requestID string
	sessionID string
}

func (i *reqInfo) setSession(id string) {
	if i == nil {
		return
	}
	i.mu.Lock()
	i.sessionID = clip(id, 64)
	i.mu.Unlock()
}

func (i *reqInfo) session() string {
	if i == nil {
		return ""
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.sessionID
}

func infoFrom(ctx context.Context) *reqInfo {
	v, _ := ctx.Value(infoKey).(*reqInfo)
	return v
}

// RoutePattern 은 로그·지표에 실을 라우트 이름을 만든다. 순수 함수다.
//
// ★ **실제 경로를 절대 쓰지 않는다.** 경로에는 항목 id·세션 id·스냅숏 키가 들어 있어
// 라벨에 넣으면 시계열이 요청 수만큼 늘고, 동시에 인증 없이 열려 있는 /metrics 로
// 도메인 좌표가 새어 나간다. 못 맞춘 요청은 경로 대신 고정 문자열로 접는다.
func RoutePattern(pattern, method string) string {
	p := strings.TrimSpace(pattern)
	if p == "" || p == "/" {
		m := strings.ToUpper(strings.TrimSpace(method))
		if m == "" {
			return "unmatched"
		}
		return m + " unmatched"
	}
	return p
}

// withRequestID 는 상관키를 만들고 응답 헤더에 되돌려 준다.
//
// 클라이언트가 준 X-Request-Id 가 있으면 **그것을 잇는다** — 소비자 신고와
// 서버 로그를 잇는 열쇠는 한쪽이 끊으면 그 자리에서 죽는다.
func (s *server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := clip(strings.TrimSpace(r.Header.Get("X-Request-Id")), 64)
		if id == "" {
			id = store.NewID()
		}
		info := &reqInfo{requestID: id}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), infoKey, info)))
	})
}

// withRateLimit 은 원격 주소당 한도를 건다. **한도는 기본으로 꺼져 있다.**
func (s *server) withRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.lim.allow(hostOf(r.RemoteAddr)) {
			next.ServeHTTP(w, r)
			return
		}
		s.met.incRateLimited()
		// 로그 줄 없음 — 초과 트래픽이 그대로 로그 증폭이 되기 때문이다.
		s.writeError(w, r, Classified{
			Status: http.StatusTooManyRequests, Code: "rate_limited",
			Message:  "요청 한도를 넘었다",
			Guidance: "잠시 뒤에 다시 불러라 — 이 축은 /metrics 의 flightdeck_rate_limited_total 로만 센다.",
		})
	})
}

// withAuth 는 인증 게이트다. 판정은 순수 함수 JudgeAuth 가 한다.
func (s *server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /healthz 는 게이트 앞에 둔다. 배너 훅이 "서버가 살아 있나 · 인증이 켜져 있나"를
		// 묻는 유일한 창인데, 그것을 인증 뒤에 두면 토큰 오설정 시 아무것도 알 수 없다.
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		d := JudgeAuth(r.RemoteAddr, r.Header.Get("Authorization"), s.opt.Token, s.opt.RequireTokenOnLoopback)
		if !d.OK {
			s.met.incUnauthorized()
			// 로그 줄 없음(위 규율). 사유는 응답에만 싣는다 — 그 문구는 전부 이 계층이 쓴 것이다.
			w.Header().Set("WWW-Authenticate", `Bearer realm="flightdeck"`)
			s.writeError(w, r, Classified{
				Status: http.StatusUnauthorized, Code: "unauthorized",
				Message:  d.Reason,
				Guidance: "Authorization: Bearer <token> 을 실어라. 루프백은 토큰 없이 통과한다(/healthz 가 그 설정을 알린다).",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder 는 상태코드와 바이트 수를 본다.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (rec *statusRecorder) WriteHeader(code int) {
	if rec.wrote {
		return
	}
	rec.status, rec.wrote = code, true
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	if !rec.wrote {
		rec.WriteHeader(http.StatusOK)
	}
	return rec.ResponseWriter.Write(b)
}

// Unwrap 은 http.NewResponseController 가 Flush 를 찾아가게 한다.
// 없으면 SSE 가 이 래퍼 뒤에서 버퍼링돼 하트비트가 안 나간다.
func (rec *statusRecorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

// withAccessLog 는 **게이트를 통과한 요청 1건당 1줄**을 남기고 지표를 센다.
func (s *server) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			route := RoutePattern(r.Pattern, r.Method)
			dur := s.now().Sub(start).Seconds()
			s.met.observe(route, rec.status, dur)
			info := infoFrom(r.Context())
			id := ""
			if info != nil {
				id = info.requestID
			}
			s.log.InfoContext(r.Context(), "request served",
				"route", route,
				"status", rec.status,
				"duration", dur,
				"request_id", id,
				"session_id", info.session(),
			)
		}()
		next.ServeHTTP(rec, r)
	})
}

// bodyRecorder 는 응답 본문을 함께 모은다(멱등 재생용).
type bodyRecorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
	wrote  bool
}

func (rec *bodyRecorder) WriteHeader(code int) {
	if rec.wrote {
		return
	}
	rec.status, rec.wrote = code, true
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *bodyRecorder) Write(b []byte) (int, error) {
	if !rec.wrote {
		rec.WriteHeader(http.StatusOK)
	}
	rec.body.Write(b)
	return rec.ResponseWriter.Write(b)
}

func (rec *bodyRecorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

// withIdempotency 는 쓰기 재시도를 같은 결과로 접는다.
//
// 읽기는 그대로 지나간다. 5xx 는 저장하지 않는다 — 일시 장애를 영구 응답으로
// 굳히면 하류가 복구된 뒤에도 같은 실패만 돌려주게 된다.
func (s *server) withIdempotency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isWrite(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if v := JudgeIdempotencyKey(r.Method, key); !v.OK {
			s.writeError(w, r, badRequest("idempotency_key_required", v.Reason,
				"Idempotency-Key: <session>:<seq> 를 실어라 — 훅은 타임아웃으로 끊고 다시 부르는 것이 정상 동작이라, "+
					"이 키가 없으면 같은 신호·항목이 조용히 두 번 들어간다."))
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.opt.MaxBodyBytes))
		if err != nil {
			s.writeError(w, r, Classified{
				Status: http.StatusRequestEntityTooLarge, Code: "body_too_large",
				Message: "요청 본문이 상한을 넘었다",
			})
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		fp := Fingerprint(r.Method, r.URL.Path, body)
		// 이 라우트가 재기동을 넘겨야 하는가. 판정은 순수 함수가 하고 사유를 함께 낸다.
		pv := JudgePersistIdempotency(r.Method, r.URL.Path)
		entry, replay, m := s.idem.begin(r.Context(), key, fp, pv.Persist)
		if m != nil && m.Conflict {
			s.met.incConflict()
			s.writeError(w, r, Classified{
				Status: http.StatusConflict, Code: "idempotency_conflict",
				Message:  m.Reason,
				Guidance: "키는 요청 1건에 하나다 — <session>:<seq> 의 seq 를 올려라.",
			})
			return
		}
		if entry == nil {
			// 앞선 같은 키의 요청을 기다리다 끊겼다. 재시도가 맞는 상황이라 503 으로 낸다.
			s.writeError(w, r, Classified{
				Status: http.StatusServiceUnavailable, Code: "idempotency_wait_canceled",
				Message: "같은 키의 앞선 요청을 기다리다 취소됐다 — 다시 불러라",
			})
			return
		}
		if replay {
			s.met.incReplay()
			ct := entry.ctype
			if ct == "" {
				ct = "application/json; charset=utf-8"
			}
			w.Header().Set("Content-Type", ct)
			w.Header().Set("Idempotency-Replayed", "true")
			w.WriteHeader(entry.status)
			if _, err := w.Write(entry.body); err != nil {
				s.log.WarnContext(r.Context(), "멱등 재생 응답 쓰기 실패", "error", err.Error())
			}
			return
		}

		rec := &bodyRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			// ★ 요청 컨텍스트의 취소를 떼고 저장한다. 클라이언트가 응답을 받자마자 끊으면
			//   그 컨텍스트가 죽는데, 그때 저장을 건너뛰면 **끊는 클라이언트일수록**
			//   멱등 기억이 안 남는다 — 재시도하는 쪽이 정확히 그 클라이언트다.
			s.idem.finish(context.WithoutCancel(r.Context()), key, entry,
				rec.status, rec.Header().Get("Content-Type"), rec.body.Bytes(), pv.Persist)
		}()
		next.ServeHTTP(rec, r)
	})
}

// withRecover 는 핸들러 패닉을 500 으로 옮기고 스택을 남긴다.
//
// 다시 던지지 않는다 — 다시 던지면 net/http 가 커넥션을 닫아 버려
// 소비자가 상태코드조차 못 받고, 그러면 "무엇이 터졌나"를 물을 좌표가 사라진다.
// 대신 이 줄이 유일한 원천이 되므로 **스택을 반드시 싣는다**.
func (s *server) withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rv := recover()
			if rv == nil {
				return
			}
			s.met.incPanic()
			info := infoFrom(r.Context())
			id := ""
			if info != nil {
				id = info.requestID
			}
			s.log.ErrorContext(r.Context(), "request panicked",
				"route", RoutePattern(r.Pattern, r.Method),
				"request_id", id,
				"session_id", info.session(),
				"error", clip(toString(rv), 600),
				"stack", clip(string(debug.Stack()), 4000),
			)
			s.writeError(w, r, Classified{
				Status: http.StatusInternalServerError, Code: "panic",
				Message:  "서버 내부 오류다. 원인 전문은 서버 로그에 있다",
				Guidance: "이 응답의 request_id 로 로그를 찾아라.",
				Internal: true,
			})
		}()
		next.ServeHTTP(w, r)
	})
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case error:
		return t.Error()
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "직렬화할 수 없는 패닉 값"
		}
		return string(b)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 한도 — 원격 주소당 토큰 버킷
// ─────────────────────────────────────────────────────────────────────────────

// Refill 은 경과 시간만큼 토큰을 채운다. 순수 함수다.
// 상한(burst)을 넘지 않는다 — 안 막으면 오래 쉰 클라이언트가 무한 버스트를 얻는다.
func Refill(tokens float64, last, now time.Time, ratePerSec, burst float64) float64 {
	if now.After(last) {
		tokens += now.Sub(last).Seconds() * ratePerSec
	}
	if tokens > burst {
		tokens = burst
	}
	return tokens
}

type bucket struct {
	tokens float64
	last   time.Time
}

type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // 초당
	burst   float64
	now     func() time.Time
}

func newLimiter(perMinute, burst int, now func() time.Time) *limiter {
	l := &limiter{buckets: map[string]*bucket{}, now: now}
	if perMinute > 0 {
		l.rate = float64(perMinute) / 60.0
		l.burst = float64(perMinute)
		if burst > 0 {
			l.burst = float64(burst)
		}
	}
	return l
}

// allow 는 요청 1건을 통과시킬지 본다. rate 가 0 이면 **한도가 없다**.
func (l *limiter) allow(key string) bool {
	if l.rate <= 0 {
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = Refill(b.tokens, b.last, now, l.rate, l.burst)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	if len(l.buckets) > 10000 {
		l.sweepLocked(now)
	}
	return true
}

// sweepLocked 는 다 찬 버킷을 걷어낸다(가진 상태가 없는 것과 같다).
func (l *limiter) sweepLocked(now time.Time) {
	for k, b := range l.buckets {
		if Refill(b.tokens, b.last, now, l.rate, l.burst) >= l.burst {
			delete(l.buckets, k)
		}
	}
}

func hostOf(remoteAddr string) string {
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return h
	}
	return remoteAddr
}
