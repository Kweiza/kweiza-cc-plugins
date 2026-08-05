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
