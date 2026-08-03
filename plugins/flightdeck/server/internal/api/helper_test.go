package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// 이 패키지의 시험은 **소비자 좌표계**로만 단정한다 —
// 실제 HTTP 응답의 상태코드·헤더·본문 · SSE 한 줄 · 구조화 로그 실물 한 줄.
//
// ★ 로거를 JSON 핸들러로 둔 이유: 프로덕션이 JSON 이라 텍스트 핸들러로 시험하면
// "필드가 있는가"라는 축을 원리적으로 못 본다(텍스트는 빈 값도 그럴듯하게 찍는다).

func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// syncBuffer 는 여러 고루틴이 쓰는 로그 버퍼다(SSE 핸들러가 다른 고루틴에서 쓴다).
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// records 는 로그 줄을 파싱해 돌려준다. 파싱 실패는 시험을 세운다 —
// 조용히 건너뛰면 "줄이 없다"와 "줄을 못 읽었다"가 구분되지 않는다.
func (s *syncBuffer) records(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("로그 줄을 JSON 으로 못 읽었다: %v\n%s", err, line)
		}
		out = append(out, m)
	}
	return out
}

// served 는 특정 request_id 의 액세스 로그 줄을 모은다.
func (s *syncBuffer) served(t *testing.T, requestID string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, r := range s.records(t) {
		if r["msg"] == "request served" && r["request_id"] == requestID {
			out = append(out, r)
		}
	}
	return out
}

type env struct {
	t      *testing.T
	h      http.Handler
	srv    *server
	st     *store.Store
	svc    *service.Service
	logs   *syncBuffer
	dbPath string
	repo   string // 프로젝트 경로. **git 저장소가 아니다**(파생은 실패하고 조정은 산다)
}

const testProject = "cp"

func newEnv(t *testing.T, tune func(*Options)) *env {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fd.db")
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("프로젝트 디렉토리 생성 실패: %v", err)
	}

	logs := &syncBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	st, err := store.OpenWithLogger(dbPath, log)
	if err != nil {
		t.Fatalf("DB 열기 실패: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	svc := service.New(st, log)
	opt := Options{Log: log}
	if tune != nil {
		tune(&opt)
	}
	srv := newServer(svc, opt)
	return &env{
		t: t, srv: srv, h: srv.chain(srv.routes()),
		st: st, svc: svc, logs: logs, dbPath: dbPath, repo: repo,
	}
}

// reqOpt 는 요청 하나를 손보는 조각이다.
type reqOpt func(*http.Request)

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }
func withRemote(addr string) reqOpt { return func(r *http.Request) { r.RemoteAddr = addr } }
func withKey(k string) reqOpt       { return withHeader("Idempotency-Key", k) }

// loopback 은 요청을 루프백에서 온 것으로 만든다.
// httptest.NewRequest 의 기본 RemoteAddr 은 192.0.2.1 이라 **루프백이 아니다** —
// 그 기본값이 이 시험의 "원격" 축을 공짜로 준다.
func loopback() reqOpt { return withRemote("127.0.0.1:54321") }

func (e *env) do(method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	e.t.Helper()
	var rdr io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rdr = strings.NewReader(b)
		default:
			raw, err := json.Marshal(b)
			if err != nil {
				e.t.Fatalf("요청 본문 직렬화 실패: %v", err)
			}
			rdr = bytes.NewReader(raw)
		}
	}
	r := httptest.NewRequest(method, path, rdr)
	r.Header.Set("Content-Type", contentTypeJSON)
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

var keySeq struct {
	mu sync.Mutex
	n  int
}

// write 는 쓰기 요청에 유일한 멱등 키를 붙여 보낸다.
func (e *env) write(method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	e.t.Helper()
	keySeq.mu.Lock()
	keySeq.n++
	k := "test:" + itoa(keySeq.n)
	keySeq.mu.Unlock()
	return e.do(method, path, body, append([]reqOpt{withKey(k)}, opts...)...)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// decodeBody 는 응답 본문을 맵으로 읽는다.
func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("응답 본문을 JSON 으로 못 읽었다: %v\n%s", err, w.Body.String())
	}
	return m
}

// errorOf 는 오류 응답의 error 절을 꺼낸다. 없으면 시험을 세운다.
func errorOf(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	body := decodeBody(t, w)
	e, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("오류 응답에 error 절이 없다: %s", w.Body.String())
	}
	return e
}

// openSession 은 세션 하나를 열고 그 id 를 돌려준다.
func (e *env) openSession(ccSessionID string) string {
	e.t.Helper()
	w := e.write(http.MethodPost, "/api/v1/sessions", map[string]any{
		"project":       testProject,
		"project_path":  e.repo,
		"machine_id":    "m1",
		"hostname":      "host1",
		"worktree":      e.repo,
		"cc_session_id": ccSessionID,
		"label":         "시험 세션",
	})
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		e.t.Fatalf("세션 열기 실패: %d %s", w.Code, w.Body.String())
	}
	body := decodeBody(e.t, w)
	sess, ok := body["session"].(map[string]any)
	if !ok {
		e.t.Fatalf("세션 응답에 session 절이 없다: %s", w.Body.String())
	}
	id, _ := sess["ID"].(string)
	if id == "" {
		e.t.Fatalf("세션 id 가 비었다: %s", w.Body.String())
	}
	return id
}
