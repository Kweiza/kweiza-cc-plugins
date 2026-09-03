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

  fd status [--workspace]                 서버 상태 배너 + 보드(--workspace 면 멤버 요약도)
  fd open [--label …]                     세션 등록(재호출은 재개다)
  fd beat --kind prompt|tool|mcp [--path] 생존 신호
  fd next [--workspace]                   추천 1건 + 탈락 사유 전부. **선점하지 않는다**
  fd pick <item-id> [<item-id>…]          선점(여럿이면 첫째가 선두 · 오프라인에서는 거절된다)
  fd add --id … --title … --body …        큐 항목 등록
  fd finish <item-id> --body … [--close]  판단+후속+종료+반납을 한 번에. --close 면 세션도 닫는다
  fd close [--session <카드 id>] [--why …] 세션을 닫는다. 선점이 남아 있으면 거절한다.
                                          **되돌릴 수 있다** — 다음 신호가 오면 카드가 살아난다.
                                          --session 은 보드 배너가 내는 카드 id 다 — /clear 로
                                          cc 가 갈려 손이 안 닿는 카드를 그 축으로 지목한다
  fd note --kind … --body …               판단 기록(오프라인이면 아웃박스)
  fd move <item-id> --project <대상>      항목을 다른 프로젝트로 옮긴다(고칠 수 있는 것은 이 한 축뿐)
  fd label <item-id> --add/--rm <꼬리표>  이미 있는 항목의 꼬리표를 고친다(둘 다 반복 지정 가능)
  fd after cut <item-id> --item <dep>     걸린 선행 하나를 끊는다(--job·--sha 도 된다).
                                          선행이 폐기됐거나 sha 가 해석 불가일 때의 **유일한 탈출구**
  fd land [--ok|--fail <사유>|--leave <사유>]
                                          랜딩 줄에 선다. 인자가 없으면 서거나 내 자리를 다시 묻는다.
                                          **내 차례가 아니면 종료코드 1** — "fd land && <랜딩>" 이 성립하게
  fd lane wait [--resource <이름>]…            줄을 서고 차례까지 턴 안에서 기다린다(폴링은 조회, 취득은 land)
  fd lane release --row <id> --reason …   물린 줄 행을 사람이 회수한다(사유는 판단으로 남는다)
  fd claim release --item <id> --reason … 죽은 세션의 선점을 사람이 회수한다(항목은 open 으로 돌아간다)
  fd project ls                           등록된 프로젝트와 그 실적을 표로 낸다(지울 수 있는지도 말한다)
  fd project rm --project <id> --reason … 잔해 프로젝트를 원장에서 지운다(--yes 없이는 세기만 한다)
  fd alloc <counter>                      원자 발번
  fd doctor                               이 머신과 서버의 축을 실제로 잰다

  --project <이름>                        워크스페이스 멤버 레포에 건다(status·next·pick·add·
                                          finish·land·label·lane wait). 루트 레포의
                                          .flightdeck.yaml 의 workspace 명부에 있는 이름만 받고
                                          그 밖은 거절한다 — 명부는 **커밋돼야** 읽힌다

  fd import --from-code <레포> [--from-docs <레포>] [--apply]
                                          옛 도구 산출물을 옮긴다. **기본값은 예행**이고
                                          --apply 가 있어야 쓴다. 원본은 읽기만 한다
  fd export --to-legacy --out <디렉토리>   옛 형식으로 되쓴다(완전 왕복은 아니다 — 출력이 목록을 낸다)
  fd export --judgments --out <디렉토리>   판단 원장을 JSONL 로 낸다. **DB 전량**이라 --project 를 안 받는다

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

	// ★ --harness 는 **전역**이다. hook 도 mcp 도 같은 선언을 받아야 하고, 서브명령마다
	// 따로 파싱하면 한 곳만 고칠 때 조용히 어긋난다(DESIGN 「14. 하네스 축」 ②).
	harness, args := SplitHarnessFlag(args)

	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		// cwd 가 세션의 워크트리다(설계 §13). 못 읽으면 좌표가 없다 — 다만 훅은 그래도 살아야 한다.
		log.Error("cwd 를 읽지 못했다", "error", cwdErr.Error())
	}
	app := newApp(env, log, cwd, stdin)
	app.harness = harness
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
	case "close":
		return app.runClose(ctx, args[1:], stdout)
	case "move":
		return app.runMove(ctx, args[1:], stdout)
	case "label":
		return app.runLabel(ctx, args[1:], stdout)
	case "after":
		return app.runAfter(ctx, args[1:], stdout)
	case "land":
		return app.runLand(ctx, args[1:], stdout)
	case "lane":
		return app.runLane(ctx, args[1:], stdout)
	case "claim":
		return app.runClaim(ctx, args[1:], stdout)
	case "project":
		return app.runProject(ctx, args[1:], stdout)
	case "alloc":
		return app.runAlloc(ctx, args[1:], stdout)
	case "setup":
		return app.runSetup(ctx, args[1:], stdout)
	case "doctor":
		return app.runDoctor(ctx, args[1:], stdout)
	case "migrate":
		return app.runMigrate(ctx, args[1:], stdout)
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

// SplitHarnessFlag 는 인자에서 `--harness <이름>`(과 `--harness=<이름>`)을 떼어낸다. 순수 함수다.
//
// ★ 하네스는 **선언**이지 관측이 아니다(DESIGN 「14. 하네스 축」 ②) — 환경변수로 받으면
// 중첩 실행에서 상속되어 거짓말한다. 인자는 상속되지 않으므로 그 축이 원리적으로 안 샌다.
//
// 모르는 이름이어도 여기서 거절하지 않는다. 훅은 fail-open 이고(인자가 틀려도 세션을 막지
// 않는다), 모르는 이름의 처리는 ResolveIdentityAs 가 경고로 말한다 — 판정은 한 자리에 둔다.
func SplitHarnessFlag(args []string) (harness string, rest []string) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--harness":
			if i+1 < len(args) {
				harness = strings.TrimSpace(args[i+1])
				i++
			}
		case strings.HasPrefix(a, "--harness="):
			harness = strings.TrimSpace(strings.TrimPrefix(a, "--harness="))
		default:
			rest = append(rest, a)
		}
	}
	return harness, rest
}
