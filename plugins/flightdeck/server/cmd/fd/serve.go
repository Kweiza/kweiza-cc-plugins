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
func buildHandler(svc *service.Service, webH http.Handler, opt api.Options) http.Handler {
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
	defer func() {
		if cerr := st.Close(); cerr != nil {
			log.Error("DB 닫기 실패", "error", cerr.Error())
		}
	}()

	svc := service.New(st, log)
	token := envOr(env, "FD_TOKEN", "")
	webH := web.New(svc, web.WithLogger(log))
	handler := buildHandler(svc, webH, api.Options{
		Token:         token,
		RatePerMinute: *rate,
		Log:           log,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("기동", "route", clip(*addr, 120), "db_path", clip(path, 200),
		"api_version", service.APIVersion, "auth_required", token != "")

	if err := api.Serve(ctx, *addr, handler, log); err != nil {
		// api.Serve 가 이미 원인 전문을 남겼다. 여기서 더하는 것은 **처방**이다.
		log.Error("서버를 띄우지 못했다", "route", clip(*addr, 120),
			"error", err.Error(), "reason", PortAdvice(*addr, err))
		return 1
	}
	log.Info("종료", "route", clip(*addr, 120))
	return 0
}

func envOr(env func(string) (string, bool), key, def string) string {
	if v, ok := env(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}
