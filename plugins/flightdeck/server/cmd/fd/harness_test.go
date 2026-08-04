package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
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
	db      string // SQLite 파일. **재기동을 넘어 살아남는 유일한 것**이라 좌표를 들고 있는다
	project string
	token   string // 빈 문자열이면 인증 꺼짐. newHarnessAuth 가 채운다
	env     map[string]string
	home    string // unpinnedEnv 가 쓰는 **가짜 홈**. 진짜 홈을 건드리지 않기 위한 자리다
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
	hs := &harness{
		t:       t,
		token:   token,
		state:   filepath.Join(dir, "state"),
		db:      filepath.Join(dir, "fd.db"),
		project: "testproj",
		home:    filepath.Join(dir, "home"),
	}
	hs.openStore()
	t.Cleanup(hs.closeStore) // srv.Close 보다 **먼저** 등록한다 — 정리는 LIFO 라 나중에 돈다

	quiet := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	h := buildHandler(hs.svc, web.New(hs.svc, web.WithLogger(quiet)), api.Options{Log: quiet, Token: token})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	hs.srv = srv

	hs.env = map[string]string{
		"FD_URL":                 srv.URL,
		"FD_STATE_DIR":           hs.state,
		"FD_PROJECT":             hs.project,
		"FD_LOG":                 "error",
		"CLAUDE_CODE_SESSION_ID": "cc-session-uuid-1",
	}
	return hs
}

// openStore 는 DB 파일 하나를 열고 그 위에 서비스를 새로 만든다.
//
// 파일 경로를 필드로 들고 있는 이유: 재기동을 **볼륨은 같고 프로세스는 새로**로
// 흉내 내려면 같은 파일을 다시 열 수 있어야 한다(restartProcess).
func (h *harness) openStore() {
	h.t.Helper()
	st, err := store.Open(h.db)
	if err != nil {
		h.t.Fatalf("DB 를 못 열었다(%s): %v", h.db, err)
	}
	h.st = st
	h.svc = service.New(st, quietLogger())
}

// closeStore 는 지금 열려 있는 DB 를 닫는다. 두 번 불러도 안전하다.
func (h *harness) closeStore() {
	if h.st == nil {
		return
	}
	st := h.st
	h.st, h.svc = nil, nil
	if err := st.Close(); err != nil {
		h.t.Errorf("DB 닫기 실패: %v", err)
	}
}

// restartProcess 는 **컨테이너 교체**다 — 볼륨(DB 파일)은 그대로, 프로세스는 새로.
// 설계 §7 의 "컨테이너 크래시 → restart: unless-stopped" 가 그리는 조건이 이것이다.
//
// up() 과 무엇이 다른가를 정확히 적는다. 뭉개면 이 갈래가 무엇을 지키는지 아무도 모른다.
//
//   - up() 은 **HTTP 표면만** 새로 만든다. 같은 *store.Store 와 *service.Service 를
//     그대로 재사용하므로, 저장 계층 객체가 들고 있는 프로세스 상태는 살아남는다.
//   - restartProcess() 는 거기에 더해 **DB 파일을 닫고 다시 연다.** 그래서 남는 것이
//     디스크에 커밋된 것뿐이다.
//
// ★ 실측으로 확인한 것: 멱등 표의 **메모리 층**은 up() 도 이미 새로 만든다
// (api.NewServer 가 매번 idemStore 를 새로 만든다). 그래서 "메모리 전용 멱등"이라는
// 변이는 두 갈래 모두 빨간불을 낸다 — 이 갈래가 그 변이 때문에 필요한 것은 아니다.
// 이 갈래가 **추가로** 닫는 것은 DB 파일을 실제로 다시 열어야만 보이는 축이다:
// 기록이 정말 디스크에 커밋됐는가 · 저장 계층 객체에 얹힌 캐시가 답을 대신하고 있지 않은가.
// 그리고 이쪽이 운영의 실제 조건이므로, 같은 값이면 더 강한 쪽을 쓴다.
func (h *harness) restartProcess() {
	h.t.Helper()
	h.down()
	h.closeStore()
	h.openStore()
	h.up()
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
	return h.runEnv(h.env, stdin, args...)
}

// unpinnedEnv 는 **상태 디렉토리 축을 고정하지 않는** 환경이다.
//
// ★ 왜 정식 갈래가 필요한가. 기본 env 는 FD_STATE_DIR 를 한 값에 못박는데, 그것이
// ResolveStateDir 의 **첫 가지**라서 나머지 넷(CLAUDE_PLUGIN_DATA · XDG_STATE_HOME ·
// ~/.local/state · 임시 디렉토리)이 하네스를 통해서는 **평가조차 되지 않는다.**
// 머신 id 가 채널마다 갈려 한 세션이 카드 세 장으로 뜬 결함이 전 시험 초록 상태로 산
// 구조적 이유가 이것이다 — 축을 고정한 시험은 그 축의 결함을 원리적으로 못 본다.
//
// ★ **HOME 을 반드시 가짜로 준다. 이것이 이 갈래의 핵심이다.**
// FD_STATE_DIR 를 빼는 순간 MachineIDPath 의 둘째 가지(~/.flightdeck/machine-id)가 열리고,
// homeDir 은 주입된 HOME 이 없으면 os.UserHomeDir() 로 떨어진다 — 그 함수는 **프로세스
// 환경**을 읽으므로 시험이 못 바꾼다. 즉 HOME 없이 FD_STATE_DIR 만 빼면 시험이
// **사용자의 진짜 ~/.flightdeck/machine-id 를 읽고 쓴다.** 지금 그 문을 막고 있는 유일한 것이
// 다름 아닌 그 FD_STATE_DIR 고정이라, 고정을 푸는 갈래는 반드시 홈을 함께 옮겨야 한다.
// TestUnpinnedEnvNeverReachesTheRealHome 이 그 짝을 강제한다.
//
// ★ **FD_URL 은 일부러 고정된 채로 둔다.** 그것까지 풀면 DefaultURL(127.0.0.1:7420)로
// 떨어져 시험이 개발자 머신의 **진짜 조정 서버**를 친다. 그것은 사각이 아니라 사고다.
// "환경 축을 고정하지 않는다"가 "전부 푼다"는 뜻은 아니고, 무엇을 왜 남기는지가 이 주석이다.
func (h *harness) unpinnedEnv(extra map[string]string) map[string]string {
	h.t.Helper()
	if err := os.MkdirAll(h.home, 0o755); err != nil {
		h.t.Fatalf("가짜 홈을 못 만들었다(%s): %v", h.home, err)
	}
	e := map[string]string{}
	for k, v := range h.env {
		e[k] = v
	}
	delete(e, "FD_STATE_DIR") // 이 갈래의 존재 이유
	e["HOME"] = h.home        // 위 ★ — 빼면 진짜 홈을 건드린다
	for k, v := range extra {
		e[k] = v
	}
	return e
}

// runUnpinned 는 상태 디렉토리 축을 푼 환경으로 fd 한 번을 돌린다.
func (h *harness) runUnpinned(extra map[string]string, stdin string, args ...string) (int, string) {
	h.t.Helper()
	return h.runEnv(h.unpinnedEnv(extra), stdin, args...)
}

// runEnv 는 **다른 환경으로** fd 한 번을 돌린다.
//
// 상태 디렉토리를 고르는 축(FD_STATE_DIR·CLAUDE_PLUGIN_DATA·XDG_STATE_HOME)은
// 하네스가 FD_STATE_DIR 로 고정해 두므로, 그 우선순위 자체를 시험하려면
// 환경을 바꿔 끼울 자리가 필요하다. 전역 환경을 흔들지 않는 것이 이 갈래의 존재 이유다.
// 축을 푼 환경이 필요하면 손으로 만들지 말고 unpinnedEnv 를 써라 — 손으로 만들면
// HOME 을 잊고, 그러면 시험이 사용자의 진짜 홈에 쓴다.
func (h *harness) runEnv(env map[string]string, stdin string, args ...string) (int, string) {
	h.t.Helper()
	var out, errb bytes.Buffer
	code := run(args, envOf(env), strings.NewReader(stdin), &out, &errb)
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

// runWithStdin 은 임의의 io.Reader 를 stdin 으로 준다.
//
// run/runEnv 는 문자열을 받아 strings.NewReader 로 감싸므로 **항상 즉시 EOF** 다.
// 그래서 "본문이 없으면 stdin 을 EOF 까지 읽는다"는 폴백을 원리적으로 못 본다 —
// 실제 훅·에이전트 환경의 stdin 은 열려 있고 EOF 가 안 온다.
func (h *harness) runWithStdin(stdin io.Reader, args ...string) (int, string) {
	h.t.Helper()
	var out, errb bytes.Buffer
	code := run(args, envOf(h.env), stdin, &out, &errb)
	return code, out.String()
}
