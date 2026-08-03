package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/api"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
	"github.com/kweiza/flightdeck/internal/web"
)

// 시험 하네스 — **실물 서버**를 띄운다.
//
// 가짜 HTTP 핸들러로 대신하지 않는 이유가 둘이다.
//  1. wire.go 의 필드 이름이 internal/api 의 요청 구조체와 어긋나면 서버가 조용히 0값을 받는다.
//     가짜 서버를 쓰면 그 어긋남을 **원리적으로** 못 본다(내가 만든 것에 내가 단정하게 된다).
//  2. 단정의 좌표계가 "서버가 실제로 무엇을 갖게 됐나"여야 한다. 요청을 셌다는 단정은
//     "보냈다"만 말하고 "저장됐다"는 말하지 못한다.

type harness struct {
	t       *testing.T
	srv     *httptest.Server
	st      *store.Store
	svc     *service.Service
	state   string
	project string
	token   string // 빈 문자열이면 인증 꺼짐. newHarnessAuth 가 채운다
	env     map[string]string
}

// newHarness 는 실물 store + internal/api + internal/web 을 한 주소에 붙인다.
func newHarness(t *testing.T) *harness { return newHarnessAuth(t, "") }

// newHarnessAuth 는 토큰을 켠 하네스다.
//
// ★ 이 갈래가 없어서 이 모듈 전체가 **토큰을 켠 조립기를 한 번도 안 돌렸다.**
// 그래서 대시보드가 게이트 사슬 밖에 붙어 있다는 사실을 전 시험 초록 상태로 놓쳤다.
// 인증이 걸린다는 것을 시험하려면 먼저 인증을 켜야 한다 — 대조가 없으면 검사도 없다.
func newHarnessAuth(t *testing.T, token string) *harness {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "fd.db"))
	if err != nil {
		t.Fatalf("DB 를 못 열었다: %v", err)
	}
	t.Cleanup(func() {
		if cerr := st.Close(); cerr != nil {
			t.Errorf("DB 닫기 실패: %v", cerr)
		}
	})
	quiet := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := service.New(st, quiet)
	h := buildHandler(svc, web.New(svc, web.WithLogger(quiet)), api.Options{Log: quiet, Token: token})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	hs := &harness{
		t: t, srv: srv, st: st, svc: svc,
		token:   token,
		state:   filepath.Join(dir, "state"),
		project: "testproj",
	}
	hs.env = map[string]string{
		"FD_URL":                 srv.URL,
		"FD_STATE_DIR":           hs.state,
		"FD_PROJECT":             hs.project,
		"FD_LOG":                 "error",
		"CLAUDE_CODE_SESSION_ID": "cc-session-uuid-1",
	}
	return hs
}

// down 은 서버를 죽인다. 열화(L1) 경로 시험의 전제다.
//
// ★ 대조가 성립했는지를 **결과를 읽기 전에** 단정한다: 정말 미도달인가.
// 서버가 살아 있는데 초록이 나오면 그 시험은 아무것도 안 지킨 것이다.
func (h *harness) down() {
	h.t.Helper()
	h.srv.Close()
	cli := newClient(ResolveStateDir(envOf(h.env), ""), envOf(h.env),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := cli.Healthz(context.Background()); !Unreachable(err, 0) {
		h.t.Fatalf("대조 전제가 깨졌다 — 서버를 죽였는데 미도달이 아니다(err=%v)", err)
	}
}

// up 은 죽인 서버 자리에 **같은 주소로** 다시 띄운다(재연결 경로).
func (h *harness) up() {
	h.t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	handler := buildHandler(h.svc, web.New(h.svc, web.WithLogger(quiet)),
		api.Options{Log: quiet, Token: h.token})
	srv := httptest.NewUnstartedServer(handler)
	ln, err := listenOn(h.srv.URL)
	if err != nil {
		h.t.Fatalf("같은 주소로 다시 못 띄웠다: %v", err)
	}
	srv.Listener = ln
	srv.Start()
	h.srv = srv
	h.t.Cleanup(srv.Close)

	cli := newClient(ResolveStateDir(envOf(h.env), ""), envOf(h.env),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := cli.Healthz(context.Background()); err != nil {
		h.t.Fatalf("대조 전제가 깨졌다 — 서버를 다시 띄웠는데 도달이 안 된다: %v", err)
	}
}

// run 은 fd 한 번을 돌리고 종료코드와 stdout 을 낸다.
func (h *harness) run(stdin string, args ...string) (int, string) {
	h.t.Helper()
	var out, errb bytes.Buffer
	code := run(args, envOf(h.env), strings.NewReader(stdin), &out, &errb)
	return code, out.String()
}

// judgments 는 서버가 **실제로 갖게 된** 판단이다. 단정의 좌표계가 여기다.
func (h *harness) judgments(kind model.JudgmentKind) []model.Judgment {
	h.t.Helper()
	js, err := h.st.ListJudgmentsByKind(context.Background(), h.project, kind, 200)
	if err != nil {
		h.t.Fatalf("판단 조회 실패: %v", err)
	}
	return js
}

func mustContain(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Fatalf("%s 에 %q 가 없다:\n%s", what, w, got)
		}
	}
}

// service0BoardOptions 는 시험이 서버 상태를 직접 볼 때 쓰는 보드 인자다.
// 화면과 같은 축을 본다 — 시험만 다른 인자를 쓰면 그 차이가 시험의 사각이 된다.
func service0BoardOptions() service.BoardOptions {
	return service.BoardOptions{IncludeQueue: true, IncludeNotes: true}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// timeUnit 은 SSE 처럼 끝나지 않는 응답을 끊는 짧은 구간이다.
const timeUnit = time.Second

// requireTokenEverywhere 는 루프백 면제를 끄고 하네스를 재조립한다.
//
// httptest 는 127.0.0.1 이라 기본 설정에서는 토큰 없이도 통과한다. 그 상태로
// "인증이 걸린다"를 단정하면 **루프백이라 통과한 것인지 게이트가 막은 것인지 구분되지 않는다.**
func (h *harness) requireTokenEverywhere() {
	h.t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	handler := buildHandler(h.svc, web.New(h.svc, web.WithLogger(quiet)),
		api.Options{Log: quiet, Token: h.token, RequireTokenOnLoopback: true})
	h.srv.Close()
	h.srv = httptest.NewServer(handler)
	h.t.Cleanup(h.srv.Close)
}
