package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/service"
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
	case "rm":
		return a.runProjectRm(ctx, args[1:], out)
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

// runProjectRm 은 `fd project rm --project <id> --reason "..." [--yes]` 다.
//
// ★ --yes 없이 부르면 **세기만 한다.** 무엇이 함께 지워질지를 먼저 보여주는 것이
// 이 명령의 절반이다 — 되돌릴 수 없기 때문이다. --force 류의 우회 플래그는 없다:
// 한 번 만들면 다음 사람이 그것을 쓴다(항목이 있는 프로젝트를 강제로 지우는 길이 열린다).
func (a *App) runProjectRm(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("project rm")
	project := fs.String("project", "", "지울 프로젝트 id")
	reason := fs.String("reason", "", "왜 지우나 — 필수. 원장에 남는다")
	yes := fs.Bool("yes", false, "실제로 지운다. 없으면 무엇이 지워질지 세기만 한다")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	set := flagsSet(fs)
	if !set["project"] || !set["reason"] {
		fmt.Fprintln(out, "지울 대상과 사유를 줘라: fd project rm --project <id> --reason \"...\"")
		fmt.Fprintln(out, "무엇이 있는지는 `fd project ls` 가 낸다. 되돌릴 수 없는 삭제다.")
		return 2
	}

	a.cli.Flush(ctx)
	// ★ lane release·claim release 와 같은 자리(cmds.go) — USER 가 비면 LOGNAME 으로
	// 물러선다. 행위자는 원장의 event(project.removed)에 남는 유일한 "누가"다.
	user, _ := a.env("USER")
	if strings.TrimSpace(user) == "" {
		user, _ = a.env("LOGNAME")
	}
	res, err := a.cli.Write(ctx, "project-remove",
		"/api/v1/projects/"+urlPath(*project)+"/remove", projectRemoveReq{
			Actor: user, Reason: *reason, Confirm: *yes,
		})
	if err != nil {
		fmt.Fprintf(out, "지우지 못했다: %v\n", err)
		return 1
	}
	if !res.Sent {
		fmt.Fprintf(out, "%s: %s\n", res.Mode, res.Reason)
		return 1
	}
	var rr service.ProjectRemoval
	if err := json.Unmarshal(res.Body, &rr); err != nil {
		fmt.Fprintf(out, "응답 해석 실패: %v\n", err)
		return 1
	}

	fmt.Fprintf(out, "프로젝트 %s 에 묶인 것:\n", rr.Project)
	for _, k := range sortedKeys(rr.Counts) {
		if rr.Counts[k] > 0 {
			fmt.Fprintf(out, "  %-20s %d\n", k, rr.Counts[k])
		}
	}
	// ★ event 는 지워지지 않는다는 사실을 여기서 말한다. 안 그러면 사람이 "다 지웠다"고
	//   믿고, 나중에 event 에서 그 프로젝트 이름을 보고 삭제가 실패한 줄 안다.
	fmt.Fprintln(out, "  (event 는 안 지운다 — 지웠다는 사실 자체가 거기 남는다)")

	if rr.Refusal != "" {
		fmt.Fprintf(out, "\n안 지웠다: %s\n", rr.Refusal)
		return 1
	}
	if rr.Removed {
		fmt.Fprintf(out, "\n지웠다. 프로젝트 %s 는 원장에서 사라졌다.\n", rr.Project)
		fmt.Fprintln(out, "그 경로에서 세션이 다시 열리면 자동 등록으로 다시 생긴다 — "+
			"워크트리 잔해라면 그 경로부터 없애라.")
	}
	return 0
}

// sortedKeys 는 표 이름을 정렬해 낸다. 출력 순서가 흔들리면 시험이 흔들린다.
func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
