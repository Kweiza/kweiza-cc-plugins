// 명령 fd 는 flightdeck 의 **하나뿐인 바이너리**다.
//
// 얼굴이 셋이다:
//
//	fd serve                     조정 서버(REST 정본 · HTML · SSE · /healthz · /metrics)
//	fd mcp                       stdio MCP — REST 위의 얇은 껍데기
//	fd status|open|beat|…        클라이언트. **전부 REST 를 친다**
//	fd hook <event>              훅 4종. 전부 fail-open(어떤 실패에도 종료코드 0)
//
// 하나로 둔 이유: 배포 단위가 하나면 클라이언트·서버 버전 스큐를 /healthz 한 축으로 볼 수 있고,
// 플러그인이 캐시할 산출물도 하나다.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/kweiza/flightdeck/internal/service"
)

const usage = `fd — flightdeck 클라이언트/서버

  fd serve [--addr :7420] [--db <경로>]   조정 서버를 띄운다
  fd mcp                                  stdio MCP 서버(플러그인이 부른다)
  fd hook <event>                         훅. stdin 으로 페이로드를 받는다
                                          (session-start|user-prompt|post-tool|pre-compact)

  fd status                               서버 상태 배너 + 보드
  fd open [--label …]                     세션 등록(재호출은 재개다)
  fd beat --kind prompt|tool|mcp [--path] 생존 신호
  fd next                                 추천 1건 + 탈락 사유 전부. **선점하지 않는다**
  fd pick <item-id>                       선점(오프라인에서는 거절된다)
  fd add --id … --title … --body …        큐 항목 등록
  fd finish <item-id> --body …            판단+후속+종료+반납을 한 번에
  fd note --kind … --body …               판단 기록(오프라인이면 아웃박스)
  fd alloc <counter>                      원자 발번
  fd doctor                               이 머신과 서버의 축을 실제로 잰다

환경: FD_URL(기본 http://127.0.0.1:7420) · FD_TOKEN · FD_PROJECT · FD_STATE_DIR · FD_LOG
`

func main() { os.Exit(run(os.Args[1:], os.LookupEnv, os.Stdin, os.Stdout, os.Stderr)) }

// run 은 시험이 부르는 진입점이다. os.Exit 를 여기 두지 않는 이유가 그것이다.
func run(args []string, env func(string) (string, bool), stdin io.Reader, stdout, stderr io.Writer) int {
	log := newLogger(env, stderr)
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	ctx := context.Background()

	switch args[0] {
	case "serve":
		return runServe(args[1:], env, log)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version":
		fmt.Fprintf(stdout, "fd api=%s\n", service.APIVersion)
		return 0
	}

	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		// cwd 가 세션의 워크트리다(설계 §13). 못 읽으면 좌표가 없다 — 다만 훅은 그래도 살아야 한다.
		log.Error("cwd 를 읽지 못했다", "error", cwdErr.Error())
	}
	app := newApp(env, log, cwd, stdin)
	if app.notice != "" {
		log.Warn("클라이언트 초기화 경고", "reason", app.notice)
	}

	switch args[0] {
	case "mcp":
		return runMCP(ctx, env, log, stdin, stdout)

	case "hook":
		if len(args) < 2 {
			log.Error("훅 이름이 없다", "error", "fd hook <session-start|user-prompt|post-tool|pre-compact>")
			return 0 // ★ fail-open. 인자가 틀려도 세션을 막지 않는다
		}
		return app.runHook(ctx, args[1], stdin, stdout)

	case "status":
		return app.runStatus(ctx, args[1:], stdout)
	case "open":
		return app.runOpen(ctx, args[1:], stdout)
	case "beat":
		return app.runBeat(ctx, args[1:], stdout)
	case "note":
		return app.runNote(ctx, args[1:], stdout)
	case "next":
		return app.runNext(ctx, args[1:], stdout)
	case "pick":
		return app.runPick(ctx, args[1:], stdout)
	case "add":
		return app.runAdd(ctx, args[1:], stdout)
	case "finish":
		return app.runFinish(ctx, args[1:], stdout)
	case "alloc":
		return app.runAlloc(ctx, args[1:], stdout)
	case "doctor":
		return app.runDoctor(ctx, args[1:], stdout)
	default:
		fmt.Fprintf(stderr, "모르는 명령: %s\n\n%s", clip(args[0], 40), usage)
		return 2
	}
}

// newLogger 는 구조화 로거다. **stderr 로만 낸다** —
// stdout 은 MCP 프로토콜과 훅 계약이 쓰는 자리라 한 줄이라도 섞이면 그 둘이 통째로 깨진다.
func newLogger(env func(string) (string, bool), stderr io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if v, ok := env("FD_LOG"); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "debug":
			level = slog.LevelDebug
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}
	}
	return slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: level})).
		With("service.name", "flightdeck")
}
