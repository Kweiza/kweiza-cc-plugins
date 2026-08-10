package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// runProject 는 `fd project <하위명령>` 이다.
//
// ★ 이 명령군은 **사람의 표면**이다(runClaim 과 같은 갈래). 세션이 프로젝트를 만드는 것은
// 자동 등록이고 그것은 여기 없다 — 여기 있는 것은 등록된 것을 보고, 접고, 치우는 길뿐이다.
func (a *App) runProject(ctx context.Context, args []string, out io.Writer) int {
	const help = "fd project ls  — 등록된 프로젝트와 그 실적을 낸다\n" +
		"  fd project rm --project <id> --reason \"...\" [--yes]  — 잔해를 지운다"
	// 선두가 플래그면 하위 명령을 빼먹은 것이다(runClaim 과 같은 갈래) — 그대로 두면
	// `fd project --foo` 가 "모르는 project 하위 명령: --foo" 라는 엉뚱한 말을 한다.
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(out, "project 하위 명령을 줘라:")
		fmt.Fprintln(out, "  "+help)
		return 2
	}
	switch args[0] {
	case "ls":
		return a.runProjectLs(ctx, args[1:], out)
	default:
		fmt.Fprintf(out, "모르는 project 하위 명령: %s\n  %s\n", clip(args[0], 40), help)
		return 2
	}
}

// runProjectLs 는 `fd project ls` 다.
func (a *App) runProjectLs(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("project ls")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rr, err := a.cli.Read(ctx, "/api/v1/projects")
	if err != nil {
		if rr.Banner != "" {
			fmt.Fprintln(out, rr.Banner)
		}
		fmt.Fprintf(out, "프로젝트 목록을 읽지 못했다: %v\n", err)
		return 1
	}
	if !rr.Fresh {
		fmt.Fprintln(out, rr.Banner)
		fmt.Fprintln(out, "아래는 캐시된 목록이다.")
	}
	var resp projectsResp
	if err := json.Unmarshal(rr.Body, &resp); err != nil {
		fmt.Fprintf(out, "응답 해석 실패: %v\n", err)
		return 1
	}
	if len(resp.Projects) == 0 {
		fmt.Fprintln(out, "등록된 프로젝트가 없다.")
		return 0
	}
	// ★ %-34s 류를 안 쓴다. Go 의 폭 지정은 룬 수로 채우는데 한글은 터미널에서 2칸을
	// 먹어서, 한글 헤더와 ASCII 데이터가 같은 %-Ns 를 타면 칸이 어긋난다(실측: 헤더
	// "프로젝트"가 38칸, 데이터 "junk"가 34칸 — 4칸 밀림. env.go 의 padDisplay 주석에
	// 그 계산을 적어 뒀다). padDisplay/padDisplayRight 는 표시 폭으로 채워서 헤더와
	// 행이 같은 계산을 탄다.
	fmt.Fprintln(out, strings.Join([]string{
		padDisplay("프로젝트", 34), padDisplay("상태", 6),
		padDisplayRight("항목", 6), padDisplayRight("세션", 8), padDisplayRight("판단", 8),
		"마지막 세션",
	}, " "))
	for _, p := range resp.Projects {
		state := "-"
		switch {
		case p.Pinned:
			state = "핀"
		case p.Archived:
			state = "보관"
		}
		last := "없음"
		if !p.LastSessionAt.IsZero() {
			// ★ humanAge 는 이미 "…전" 을 붙여 낸다(env.go) — 여기서 또 붙이면 "3초 전 전"이 된다.
			last = humanAge(time.Since(p.LastSessionAt))
		}
		fmt.Fprintln(out, strings.Join([]string{
			padDisplay(clip(p.ID, 34), 34), padDisplay(state, 6),
			padDisplayRight(strconv.Itoa(p.Items), 6),
			padDisplayRight(strconv.Itoa(p.Sessions), 8),
			padDisplayRight(strconv.Itoa(p.Judgments), 8),
			last,
		}, " "))
	}
	// ★ 지울 수 있는지를 여기서 말한다 — 사람이 rm 을 쳐 보고서야 알게 두지 않는다.
	// 판단이 못 지워지는 이유는 정책이 아니라 원장이 정한 제약이다(judgment_no_delete 트리거).
	fmt.Fprintln(out, "\n항목이나 판단이 있는 프로젝트는 지울 수 없다(판단은 원장이라 삭제 금지다).")
	fmt.Fprintln(out, "줄에서만 빼려면 대시보드에서 보관하라 — 되돌릴 수 있다.")
	return 0
}
