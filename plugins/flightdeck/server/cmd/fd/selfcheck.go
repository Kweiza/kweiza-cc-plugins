package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/buildinfo"
	"github.com/kweiza/flightdeck/internal/store"
)

// runSelfcheck 는 `fd selfcheck --db <경로>` 다.
//
// **재기동해도 되는가**에만 답한다. 이 명령이 0 을 내면 감시기가 syscall.Exec 로 넘어간다.
// 그래서 여기서 하는 일은 최소여야 한다 — 무엇을 더 볼수록 이 명령 자체가 실패 원인이 된다.
//
// 보는 것 셋:
//  1. 이 바이너리가 실행된다(이 프로세스가 떴다는 것 자체)
//  2. 자기 빌드 좌표를 낸다
//  3. DB 증분 계획이 거절이 아니다 — **적용하지 않는다**
//
// ★ 3번이 강등도 막는다. 옛 바이너리로 되돌리면 DB 버전이 그 바이너리의 SchemaVersion
// 보다 높고, PlanMigration 이 이미 그 경우를 거절로 낸다. 규칙을 새로 만들지 않는다.
//
// ★ **대상 파일을 한 바이트도 안 바꾼다.** 예전에는 재기가 기본 DSN 으로 열어
// journal_mode(WAL) 을 걸었고, 그래서 `fd selfcheck --db <백업>` 이 그 아카이브를 WAL 로
// 영구 변환했다 — 사람이 백업을 점검하는 것은 자연스러운 사용인데 그 순간 마지막 남은
// 백업이 바뀐다. ProbeMigration 이 이제 그 pragma 없이 연다(그 함수의 주석이 예전 근거와
// 그것을 뒤집은 이유를 적어 뒀다).
//
// ★ 그래서 **안 재게 된 축이 하나 있다**: "이 파일에 WAL 을 걸 수 있는가". 감시기가
// 재기동을 걸기 직전이라면 그 축은 이미 증명돼 있다(지금 도는 서버가 바로 그 파일을
// WAL 로 열고 있다). 서버가 안 돌고 있는데 사람이 이 명령을 부르면 그 축은 관측되지
// 않은 채로 남고, 실패는 실제 `fd serve` 기동에서 드러난다. 안 잰 축을 잰 척하지 않는다.
func runSelfcheck(args []string, out io.Writer) int {
	fs := newFlagSet("selfcheck")
	dbPath := fs.String("db", "", "검사할 SQLite 파일 경로")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*dbPath) == "" {
		fmt.Fprintln(out, "selfcheck: --db 가 비었다 — 무엇을 검사할지가 없다")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	plan, err := store.ProbeMigration(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(out, "selfcheck: DB 증분 계획을 읽지 못했다: %v\n", err)
		return 1
	}
	if plan.Action == store.MigrateReject {
		fmt.Fprintf(out, "selfcheck: 이 판으로 DB 를 열면 거절된다 — %s\n", plan.Reason)
		return 1
	}

	// 계약: 첫 줄에서 감시기가 새 판의 좌표를 읽는다.
	fmt.Fprintf(out, "fd selfcheck ok build=%s\n", buildinfo.Short(buildinfo.Self()))
	fmt.Fprintf(out, "  증분 계획: %s — %s\n", plan.Action, plan.Reason)
	return 0
}
