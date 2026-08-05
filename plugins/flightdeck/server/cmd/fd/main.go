// 명령 fd 는 flightdeck 의 **하나뿐인 바이너리**다.
//
// 얼굴이 셋이다:
//
//	fd serve                     조정 서버(REST 정본 · HTML · SSE · /healthz · /metrics)
//	fd mcp                       stdio MCP — REST 위의 얇은 껍데기
//	fd status|open|beat|…        클라이언트. **전부 REST 를 친다**
//	fd hook <event>              훅 5종. 전부 fail-open(어떤 실패에도 종료코드 0)
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
                                          (session-start|user-prompt|post-tool|pre-compact|stop)
  fd selfcheck --db <경로>                 이 바이너리로 재기동해도 되는가에만 답한다.
                                          fd serve 의 자동 갱신이 자식으로 부르고, 거절을
                                          손으로 재현할 때 같은 명령을 쓴다

  fd status                               서버 상태 배너 + 보드
  fd open [--label …]                     세션 등록(재호출은 재개다)
  fd beat --kind prompt|tool|mcp [--path] 생존 신호
  fd next                                 추천 1건 + 탈락 사유 전부. **선점하지 않는다**
  fd pick <item-id>                       선점(오프라인에서는 거절된다)
  fd add --id … --title … --body …        큐 항목 등록
  fd finish <item-id> --body …            판단+후속+종료+반납을 한 번에
  fd note --kind … --body …               판단 기록(오프라인이면 아웃박스)
  fd move <item-id> --project <대상>      항목을 다른 프로젝트로 옮긴다(고칠 수 있는 것은 이 한 축뿐)
  fd alloc <counter>                      원자 발번
  fd doctor                               이 머신과 서버의 축을 실제로 잰다

  fd import --from-code <레포> [--from-docs <레포>] [--apply]
                                          옛 도구 산출물을 옮긴다. **기본값은 예행**이고
                                          --apply 가 있어야 쓴다. 원본은 읽기만 한다
  fd export --to-legacy --out <디렉토리>   옛 형식으로 되쓴다(완전 왕복은 아니다 — 출력이 목록을 낸다)

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
	case "selfcheck":
		// ★ App 을 만들지 않는다. 이 명령은 재기동 검증의 피험자라, 서버 도달·세션 열기
		// 같은 축이 끼면 그 축의 실패가 "새 판이 고장났다"로 오독된다.
		return runSelfcheck(args[1:], stdout)
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
		// ★ MCP 는 자기 이름의 로거를 **새로 만든다.** 기존 로거에 덧칠하면
		// JSON 한 줄에 service.name 이 두 값으로 실린다(중복 키 처리는 파서마다 다르다).
		return runMCP(ctx, app, newLoggerNamed(env, stderr, "flightdeck-mcp"), stdin, stdout)

	case "hook":
		if len(args) < 2 {
			log.Error("훅 이름이 없다", "error", "fd hook <session-start|user-prompt|post-tool|pre-compact|stop>")
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
	case "move":
		return app.runMove(ctx, args[1:], stdout)
	case "alloc":
		return app.runAlloc(ctx, args[1:], stdout)
	case "setup":
		return app.runSetup(ctx, args[1:], stdout)
	case "doctor":
		return app.runDoctor(ctx, args[1:], stdout)
	case "import":
		return app.runImport(ctx, args[1:], stdout)
	case "export":
		return app.runExport(ctx, args[1:], stdout)
	default:
		fmt.Fprintf(stderr, "모르는 명령: %s\n\n%s", clip(args[0], 40), usage)
		return 2
	}
}

// newLogger 는 이 프로세스의 구조화 로거다. **stderr 로만 낸다** —
// stdout 은 MCP 프로토콜과 훅 계약이 쓰는 자리라 한 줄이라도 섞이면 그 둘이 통째로 깨진다.
func newLogger(env func(string) (string, bool), stderr io.Writer) *slog.Logger {
	return newLoggerNamed(env, stderr, "flightdeck")
}

// newLoggerNamed 는 service.name 을 골라 로거를 **새로 만든다.**
//
// ★ 이 파일이 그 필드를 거는 **유일한 자리**다(설계 규율: 라이브러리 계층 api·service·web 은
// 받은 로거를 그대로 쓴다). 이름이 달라야 하는 프로세스는 기존 로거에 덧칠하지 않고
// 여기서 새로 만든다 — 덧칠하면 JSON 한 줄에 같은 키가 **다른 값으로** 두 번 들어가고,
// 중복 키의 처리는 파서마다 다르다. 그러면 수집기 판올림 한 번에
// "어느 프로세스가 무엇을 했나"라는 축이 조용히 사라진다.
func newLoggerNamed(env func(string) (string, bool), stderr io.Writer, service string) *slog.Logger {
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
		With("service.name", service)
}
