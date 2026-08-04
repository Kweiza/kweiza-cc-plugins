package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/mcpsrv"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 클라이언트 서브명령 — **전부 REST 를 친다.** 서비스 계층을 직접 부르지 않는다.
//
// 이유는 하나다: 다른 머신에서도 같은 바이너리가 돌아야 한다. 직접 부르면
// 서버 머신에서만 도는 명령이 생기고, 그 비대칭은 반드시 사고가 된다.

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

// bodyFlagHelp 는 `--body` 의 도움말 문구다. **셋이 같은 문구를 쓴다.**
//
// ★ 문구를 상수로 두는 이유는 이 항목이 준 교훈 그대로다. 앞서 add 는 이 문구를 걸어 놓고
// 읽는 코드가 없었고, note 는 코드를 고친 뒤에도 문구가 옛 동작("비면 읽는다")을 말하고 있었다.
// 문구와 동작이 각자 세 벌이면 어긋난 벌을 아무도 못 센다.
const bodyFlagHelp = "본문(`-` 이면 stdin 에서 읽는다)"

// resolveBody 는 `--body` 값을 푼다. **`-` 일 때만 stdin 을 읽는다.**
//
// ★ 본문이 없다고 stdin 을 읽지 않는다. 앞선 판은 본문이 비면 stdin 을 EOF 까지 읽었고,
// 그래서 **stdin 이 열려 있는 곳에서는 영원히 멈췄다** — 훅과 에이전트의 Bash 도구가
// 정확히 그 환경이다(스모크에서 3분 넘게 멈췄다). 더 나쁜 것은 훅 경로다: 거기 stdin 은
// 훅 JSON 페이로드라, 읽으면 그것을 판단 본문으로 삼는다.
// 단위 시험은 이 축을 원리적으로 못 본다 — 시험은 본문을 주거나 이미 닫힌 리더를 쓴다.
//
// ★ **이 판정을 사본으로 두지 않는다.** note·finish 가 같은 열 줄을 각자 들고 있었고
// add 는 아예 안 들고 있어서, `fd add --body -` 가 오류도 없이 `-` 한 글자를 본문으로
// 저장했다. 그렇게 등록된 항목 하나(fd-item-move)는 고칠 방법이 없어 폐기됐는데
// **id 는 전역 유일이라 회수되지 않아 그 이름이 영구히 죽었다.**
// 이 레포는 "같은 판정을 두 자리에 두면 한쪽만 고칠 때 조용히 어긋난다"를 세 번 겪었다.
func (a *App) resolveBody(flagValue string) string {
	if flagValue != "-" {
		return flagValue
	}
	// stdin 읽기는 **명시적으로 요청했을 때만** 한다.
	b, err := io.ReadAll(io.LimitReader(a.stdin, 4<<20))
	if err != nil {
		return ""
	}
	return string(b)
}

// TakeFirstPositional 은 맨 앞의 위치 인자를 떼어낸다. 순수 함수다.
//
// ★ 표준 flag 패키지는 **첫 비플래그 인자에서 파싱을 멈춘다.** 그래서
// `fd finish <id> --body …` 를 그대로 넘기면 --body 가 플래그가 아니라 위치 인자가 되고,
// 본문이 빈 채로 거절당한다 — 사용자에게는 "본문을 줬는데 안 받는다"로 보인다.
// 이 함수가 그 순서를 양쪽 다 받게 만든다: 앞에 와도 뒤에 와도 같은 뜻이다.
func TakeFirstPositional(args []string) (pos string, rest []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

// runStatus 는 `fd status` 다. 서버 상태 배너 + 보드.
func (a *App) runStatus(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("status")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	a.cli.Flush(ctx)
	banner, _ := a.ServerBanner(ctx)
	if banner != "" {
		fmt.Fprintln(out, banner)
	}
	// ★ 세션을 먼저 연다. 그것이 **프로젝트를 등록하는 유일한 경로**라서다 —
	//   안 열면 처음 쓰는 저장소에서 보드가 404 로 끊기고, 그 404 는
	//   "프로젝트가 없다"라고만 말해 무엇을 해야 하는지 알려주지 않는다.
	//   실패해도 진행한다: 조회는 세션 없이도 성립한다.
	self, serr := a.sessionID(ctx, "")
	if serr != nil {
		a.log.Warn("status: 세션 좌표를 못 얻었다", "reason", clip(serr.Error(), 200))
	}
	v, staleBanner, err := a.Board(ctx, self)
	if err != nil {
		if staleBanner != "" && banner == "" {
			fmt.Fprintln(out, staleBanner)
		}
		fmt.Fprintf(out, "보드를 못 냈다: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, mcpsrv.RenderBoard(v, mcpsrv.BoardRenderOptions{Self: self, Now: a.now(), Detail: true}))
	return 0
}

// runOpen 은 `fd open` 이다.
func (a *App) runOpen(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("open")
	label := fs.String("label", "", "표시용 라벨(어떤 필터의 축도 아니다)")
	session := fs.String("cc-session", "", "Claude Code 세션 id(비면 환경에서 읽는다)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	a.cli.Flush(ctx)
	cc := a.ccSessionID(*session)
	if cc == "" {
		fmt.Fprintln(out, "CLAUDE_CODE_SESSION_ID 를 못 읽었다 — 그 탐지가 깨진 것이다(fd doctor 가 그 축을 잰다). 지어내지 않는다.")
		return 1
	}
	res, stale, err := a.OpenSession(ctx, cc, *label)
	if err != nil {
		fmt.Fprintf(out, "세션 열기 실패: %v\n", err)
		return 1
	}
	if stale {
		fmt.Fprintln(out, StaleBanner(a.now(), a.cli.Cache.LastContact(), a.cli.URL))
	}
	verb := "재개"
	if res.Created {
		verb = "신규"
	}
	fmt.Fprintf(out, "세션 %s(%s) · 프로젝트 %s · 브랜치 %s\n",
		verb, res.Session.ID, res.Project.ID, orDash(res.Branch))
	if len(res.Claims) > 0 {
		fmt.Fprintf(out, "이미 쥐고 있는 항목: %s\n", strings.Join(res.Claims, " "))
	}
	fmt.Fprintln(out, mcpsrv.FormatFreshness(res.Derived))
	return 0
}

// runBeat 는 `fd beat` 다. 훅이 부르는 것과 같은 경로다.
func (a *App) runBeat(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("beat")
	kind := fs.String("kind", "mcp", "prompt|tool|mcp|commit|push")
	session := fs.String("cc-session", "", "Claude Code 세션 id")
	var paths stringList
	fs.Var(&paths, "path", "이번에 만진 경로(여러 번 줄 수 있다)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	sess, err := a.sessionID(ctx, *session)
	if err != nil {
		fmt.Fprintf(out, "신호를 못 보냈다: %v\n", err)
		return 1
	}
	res, err := a.cli.Write(ctx, "beat",
		"/api/v1/sessions/"+urlPath(sess)+"/signals", beatReq{Kind: *kind, Paths: paths})
	if err != nil {
		fmt.Fprintf(out, "신호를 못 보냈다: %v\n", err)
		return 1
	}
	if !res.Sent {
		fmt.Fprintf(out, "%s: %s\n", res.Mode, res.Reason)
		return 0
	}
	fmt.Fprintf(out, "신호 %s 기록(경로 %d)\n", *kind, len(paths))
	return 0
}

// runNote 는 `fd note` 다. **오프라인에서도 성공한다**(아웃박스).
func (a *App) runNote(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("note")
	kind := fs.String("kind", "", "handoff|decision|blocked|ask|now|rejected|not-done|verified|draft")
	title := fs.String("title", "", "제목")
	body := fs.String("body", "", bodyFlagHelp)
	item := fs.String("item", "", "연결할 항목 id")
	session := fs.String("cc-session", "", "Claude Code 세션 id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	text := a.resolveBody(*body)
	if strings.TrimSpace(text) == "" {
		fmt.Fprintln(out, "판단 본문이 비었다 — 무엇을 왜 그렇게 했는지가 이 표의 존재 이유다. 한 줄이라도 남겨라.")
		return 2
	}
	a.cli.Flush(ctx)
	sess, _ := a.sessionID(ctx, *session) // 세션을 못 얻어도 판단은 남긴다(세션 없는 판단이 없는 판단보다 낫다)
	a.cli.Session = sess
	res, err := a.cli.Write(ctx, "note", "/api/v1/judgments", noteReq{
		Project: a.proj.ID, SessionID: sess, Kind: *kind,
		Title: *title, Body: text, ItemID: *item,
	})
	if err != nil {
		fmt.Fprintf(out, "판단을 못 남겼다: %v\n", err)
		return 1
	}
	if !res.Sent {
		fmt.Fprintf(out, "서버 미도달 — 아웃박스에 쌓았다(%s).\n%s\n", res.Mode, res.Reason)
		return 0
	}
	var nr service.NoteResult
	if err := json.Unmarshal(res.Body, &nr); err != nil {
		fmt.Fprintf(out, "저장은 됐으나 응답 해석 실패: %v\n", err)
		return 0
	}
	fmt.Fprintln(out, mcpsrv.RenderNote(nr))
	return 0
}

// runNext 는 `fd next` 다. **선점하지 않는다.**
func (a *App) runNext(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("next")
	session := fs.String("cc-session", "", "Claude Code 세션 id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	a.cli.Flush(ctx)
	sess, err := a.sessionID(ctx, *session)
	if err != nil {
		fmt.Fprintf(out, "추천을 못 받았다: %v\n", err)
		return 1
	}
	path := fmt.Sprintf("/api/v1/items/next?project=%s&session_id=%s", urlValue(a.proj.ID), urlValue(sess))
	rr, err := a.cli.Read(ctx, path)
	if err != nil {
		if rr.Banner != "" {
			fmt.Fprintln(out, rr.Banner)
		}
		fmt.Fprintf(out, "추천을 못 받았다: %v\n", err)
		return 1
	}
	if !rr.Fresh {
		fmt.Fprintln(out, rr.Banner)
		fmt.Fprintln(out, "아래는 캐시된 추천이다 — **선점은 아직 아무것도 안 됐다.**")
	}
	var res service.PickResult
	if err := json.Unmarshal(rr.Body, &res); err != nil {
		fmt.Fprintf(out, "추천 응답 해석 실패: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, mcpsrv.RenderPick(res, a.now()))
	return 0
}

// runPick 은 `fd pick <id>` 다. **오프라인에서는 거절된다.**
func (a *App) runPick(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("pick")
	session := fs.String("cc-session", "", "Claude Code 세션 id")
	itemID, rest := TakeFirstPositional(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if itemID == "" {
		itemID = fs.Arg(0)
	}
	if strings.TrimSpace(itemID) == "" {
		fmt.Fprintln(out, "집을 항목 id 를 줘라: fd pick <item-id>")
		return 2
	}
	a.cli.Flush(ctx)
	sess, err := a.sessionID(ctx, *session)
	if err != nil {
		fmt.Fprintf(out, "선점하지 못했다: %v\n", err)
		return 1
	}
	a.cli.Session = sess
	res, err := a.cli.Write(ctx, "pick", "/api/v1/items/"+urlPath(itemID)+"/claim",
		claimReq{Project: a.proj.ID, SessionID: sess})
	if err != nil {
		fmt.Fprintf(out, "선점하지 못했다: %v\n", err)
		return 1
	}
	var pr service.PickResult
	if err := json.Unmarshal(res.Body, &pr); err != nil {
		fmt.Fprintf(out, "선점 응답 해석 실패: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, mcpsrv.RenderPick(pr, a.now()))
	return 0
}

// runAdd 는 `fd add` 다.
func (a *App) runAdd(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("add")
	id := fs.String("id", "", "항목 id(브랜치 이름·워크트리 디렉토리로 그대로 쓰인다)")
	title := fs.String("title", "", "제목")
	body := fs.String("body", "", bodyFlagHelp)
	session := fs.String("cc-session", "", "Claude Code 세션 id")
	var paths, labels, afterItems, afterSHAs stringList
	fs.Var(&paths, "path", "이 항목이 만질 경로")
	fs.Var(&labels, "label", "표시용 라벨")
	fs.Var(&afterItems, "after-item", "선행 항목 id")
	fs.Var(&afterSHAs, "after-sha", "랜딩된 선행 sha")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var after []afterWire
	for _, v := range afterItems {
		after = append(after, afterWire{Item: v})
	}
	for _, v := range afterSHAs {
		after = append(after, afterWire{SHA: v})
	}
	// ★ add 도 셋 중 하나다. 앞서 여기만 안 읽어서 `-` 한 글자가 본문으로 저장됐다.
	text := a.resolveBody(*body)

	sess, _ := a.sessionID(ctx, *session)
	a.cli.Session = sess
	res, err := a.cli.Write(ctx, "add", "/api/v1/items", addReq{
		Project: a.proj.ID, SessionID: sess, ID: *id, Title: *title, Body: text,
		Paths: paths, Labels: labels, After: after,
	})
	if err != nil {
		fmt.Fprintf(out, "항목을 못 만들었다: %v\n", err)
		return 1
	}
	// 응답은 항목을 `{"item": …}` 로 감싼다(internal/api). 감싸지 않은 것으로 읽으면
	// 필드가 전부 0값이 되고 **등록은 됐는데 화면이 빈 줄을 내는** 모양이 된다.
	var wrap struct {
		Item model.Item `json:"item"`
	}
	if err := json.Unmarshal(res.Body, &wrap); err != nil {
		fmt.Fprintf(out, "등록은 됐으나 응답 해석 실패: %v\n", err)
		return 0
	}
	it := wrap.Item
	if strings.TrimSpace(it.ID) == "" {
		fmt.Fprintf(out, "등록은 됐으나 응답에서 항목을 못 찾았다 — 응답 형식이 바뀌었다: %s\n",
			clip(string(res.Body), 300))
		return 1
	}
	fmt.Fprintf(out, "항목 %s 등록 — %s (선행 %d · 경로 %d)\n", it.ID, it.Title, len(it.After), len(it.Paths))
	return 0
}

// runFinish 는 `fd finish <id>` 다.
func (a *App) runFinish(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("finish")
	outcome := fs.String("outcome", "done", "done|dropped")
	title := fs.String("title", "", "판단 제목")
	body := fs.String("body", "", "핸드오프 "+bodyFlagHelp)
	closeReason := fs.String("close-reason", "", "dropped 면 필수")
	session := fs.String("cc-session", "", "Claude Code 세션 id")
	itemID, rest := TakeFirstPositional(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if itemID == "" {
		itemID = fs.Arg(0)
	}
	if strings.TrimSpace(itemID) == "" {
		fmt.Fprintln(out, "끝낼 항목 id 를 줘라: fd finish <item-id>")
		return 2
	}
	text := a.resolveBody(*body)
	if strings.TrimSpace(text) == "" {
		fmt.Fprintln(out, "판단 본문(body)이 비어 있어 끝낼 수 없다.")
		fmt.Fprintln(out, service.HandoffGuidance)
		return 2
	}
	sess, err := a.sessionID(ctx, *session)
	if err != nil {
		fmt.Fprintf(out, "마무리하지 못했다: %v\n", err)
		return 1
	}
	a.cli.Session = sess
	res, err := a.cli.Write(ctx, "finish", "/api/v1/items/"+urlPath(itemID)+"/finish", finishReq{
		Project: a.proj.ID, SessionID: sess, Outcome: *outcome,
		Title: *title, Body: text, CloseReason: *closeReason,
	})
	if err != nil {
		fmt.Fprintf(out, "마무리하지 못했다: %v\n", err)
		return 1
	}
	var fr service.FinishResult
	if err := json.Unmarshal(res.Body, &fr); err != nil {
		fmt.Fprintf(out, "마무리는 됐으나 응답 해석 실패: %v\n", err)
		return 0
	}
	fmt.Fprintln(out, mcpsrv.RenderFinish(fr))
	return 0
}

// runAlloc 은 `fd alloc <counter>` 다.
func (a *App) runAlloc(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("alloc")
	name, rest := TakeFirstPositional(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if name == "" {
		name = fs.Arg(0)
	}
	if strings.TrimSpace(name) == "" {
		fmt.Fprintln(out, "카운터 이름을 줘라: fd alloc <counter>")
		return 2
	}
	res, err := a.cli.Write(ctx, "alloc",
		"/api/v1/counters/"+urlPath(name)+"/next", counterReq{Project: a.proj.ID})
	if err != nil {
		fmt.Fprintf(out, "발번하지 못했다: %v\n", err)
		return 1
	}
	var ar allocResp
	if err := json.Unmarshal(res.Body, &ar); err != nil {
		fmt.Fprintf(out, "발번 응답 해석 실패: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "%d\n", ar.Value)
	return 0
}

// runDoctor 는 `fd doctor` 다. 서버 축과 **이 머신의 축**을 함께 낸다.
func (a *App) runDoctor(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("doctor")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cwd, cwdErr := os.Getwd()
	fmt.Fprintln(out, "■ 이 머신")
	for _, ax := range service.ProbePlatform(a.env, cwd, cwdErr) {
		if ax.Observed {
			fmt.Fprintf(out, "  ✓ %-24s %s\n", ax.Name, clip(ax.Value, 120))
		} else {
			fmt.Fprintf(out, "  ✗ %-24s 관측 안 됨 — %s\n", ax.Name, clip(ax.Detail, 160))
		}
	}
	fmt.Fprintf(out, "  상태 디렉토리 %s (%s)\n", a.sd.Path, a.sd.Source)
	// 머신 id 는 세션 정체 3중키의 첫 축이다. **값만 찍으면 부족하고 읽은 자리를 함께 찍는다** —
	// 이 축이 채널마다 갈려 한 세션이 카드 세 장으로 떴을 때, 값이 다르다는 것보다
	// "어느 파일에서 왔나"가 원인에 이르는 열쇠였다(그 줄이 없어 /proc 을 뒤져야 했다).
	fmt.Fprintf(out, "  머신 %s (%s)\n", a.machine, a.machineSrc)
	// ★ 주소·토큰도 **어디서 읽었는지**를 찍는다. machineSrc 가 그 선례다 —
	// 값이 예상과 다를 때 "왜 저 값인가"에 답할 자리가 없으면 /proc 을 뒤지게 된다.
	fmt.Fprintf(out, "  서버 주소 %s (%s)\n", a.cli.URL, a.cli.Endpoint.URLSource)
	fmt.Fprintf(out, "  서버 토큰 %s (%s)\n",
		map[bool]string{true: "설정됨", false: "없음"}[a.cli.Token != ""], a.cli.Endpoint.TokenSource)
	fmt.Fprintf(out, "  프로젝트 %s · 주 저장소 %s · 워크트리 %s\n", a.proj.ID, a.proj.Path, a.proj.Worktree)
	fmt.Fprintf(out, "  좌표 판정: %s\n", a.proj.Detail)
	// ★ 아웃박스는 **상태 디렉토리마다 따로 쌓인다**(채널마다 다르다 — 훅·MCP 는
	// CLAUDE_PLUGIN_DATA, 사용자 셸은 XDG_STATE_HOME|~/.local/state). 그래서 이 줄이
	// 세는 것은 "이 머신의 대기"가 아니라 **이 채널의 대기**다. 자리를 함께 찍지 않으면
	// 같은 머신에서 채널을 바꿔 물었을 때 숫자가 달라지는 이유를 알 길이 없다.
	if pend, err := a.cli.Outbox.List(); err != nil {
		fmt.Fprintf(out, "  ! 아웃박스를 못 읽었다: %v\n", err)
	} else {
		fmt.Fprintf(out, "  아웃박스 대기 %d건 (이 채널의 자리: %s)\n", len(pend), a.sd.Path)
	}
	// 격리된 판단은 **버려진 것이 아니라 옮겨진 것**이다. 안 찍으면 조용히 사라진 것과 같다.
	if rej, err := a.cli.Outbox.Rejected(); err != nil {
		fmt.Fprintf(out, "  ! 격리 파일을 못 읽었다: %v\n", err)
	} else if len(rej) > 0 {
		fmt.Fprintf(out, "  ! 격리된 판단 %d건 — 영구 거절이라 큐에서 뺐다(버리지 않았다)\n", len(rej))
		for _, r := range rej {
			fmt.Fprintf(out, "      %s · %s\n", r.At.Format(time.RFC3339), clip(r.Reason, 200))
		}
	}
	if a.notice != "" {
		fmt.Fprintf(out, "  ! %s\n", a.notice)
	}

	// 서버 절. **REST 에 진단 엔드포인트가 없으므로**(설계 §6 의 표에 없다)
	// /healthz 가 낼 수 있는 것만 낸다. 없는 축을 있는 척 지어내지 않는다.
	h, herr := a.cli.Healthz(ctx)
	if herr != nil {
		fmt.Fprintln(out, RenderHealth(h, false, a.cli.URL))
		fmt.Fprintf(out, "    사유: %v\n", herr)
		return 1
	}
	fmt.Fprintln(out, RenderHealth(h, true, a.cli.URL))
	fmt.Fprintln(out, "  프로젝트별 git 도달성은 서버 표면에 없다 — 이 도구는 그 축을 재지 않았다.")
	return 0
}

// sessionID 는 이 실행이 붙을 세션 id 다.
//
// 세션 열기는 3중키로 멱등이므로(store.OpenSession) 매번 불러도 새 세션이 안 생긴다.
// 그래서 "세션 id 를 어디 적어 두고 재사용"하는 기구를 만들지 않는다 —
// 적어 둔 값은 원본이 움직이는 순간 조용히 거짓이 된다.
//
// ★ 그 멱등은 **3중키가 채널 간에 안정할 때만** 채널을 넘는다. 안정하지 않았던 적이 있다:
// 머신 축을 훅·CLI 는 상태 디렉토리의 파일에서, MCP 는 hostname 에서 만들어
// 한 Claude 세션이 보드에 카드 세 장으로 떴다. 저장층은 내내 옳았고 클라이언트가 갈렸다.
// 지금은 MachineID 가 고정 자리를 쓰고 mcpsrv 가 주입을 받는다(env.go · mcp.go).
func (a *App) sessionID(ctx context.Context, fromFlag string) (string, error) {
	if v, ok := a.env("FD_SESSION"); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v), nil
	}
	cc := a.ccSessionID(fromFlag)
	if cc == "" {
		return "", fmt.Errorf("CLAUDE_CODE_SESSION_ID 를 못 읽었다 — 그 탐지가 깨진 것이다(fd doctor 가 그 축을 잰다)")
	}
	res, _, err := a.OpenSession(ctx, cc, "")
	if err != nil {
		return "", err
	}
	return res.Session.ID, nil
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(못 읽음)"
	}
	return s
}

// urlPath 는 경로 성분 하나를 이스케이프한다. 항목 id 에 '/' 가 허용되므로 필요하다.
func urlPath(s string) string {
	return strings.ReplaceAll(urlValue(s), "+", "%20")
}

// runMove 는 `fd move <item-id> --project <대상>` 이다.
//
// 왜 이 명령이 있나: 항목을 잘못된 프로젝트에 등록하면 되돌릴 길이 **0** 이었다.
// move 가 없고, drop 후 같은 id 재등록은 "id 는 전역 유일" 규칙이 409 로 막으며,
// 본문·경로를 고치는 명령도 없다. 그래서 fd 항목 10건이 context-platform 에 갇혀
// **fd 레포에서 `fd next` 가 그것을 하나도 못 보는** 상태가 실제로 났다.
//
// 고칠 수 있는 것은 **프로젝트 한 축뿐이다.** 일반 amend 로 번지지 않게 여기서 막는다.
func (a *App) runMove(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("move")
	to := fs.String("project", "", "대상 프로젝트 id")
	session := fs.String("cc-session", "", "Claude Code 세션 id")
	itemID, rest := TakeFirstPositional(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if itemID == "" {
		itemID = fs.Arg(0)
	}
	if strings.TrimSpace(itemID) == "" {
		fmt.Fprintln(out, "옮길 항목 id 를 줘라: fd move <item-id> --project <대상>")
		return 2
	}
	if strings.TrimSpace(*to) == "" {
		fmt.Fprintln(out, "대상 프로젝트를 줘라: fd move <item-id> --project <대상>")
		return 2
	}
	sess, _ := a.sessionID(ctx, *session)
	a.cli.Session = sess
	res, err := a.cli.Write(ctx, "move", "/api/v1/items/"+urlPath(itemID)+"/move", moveReq{
		Project: a.proj.ID, SessionID: sess, To: *to,
	})
	if err != nil {
		fmt.Fprintf(out, "항목을 못 옮겼다: %v\n", err)
		return 1
	}
	var got struct {
		From      string `json:"from"`
		To        string `json:"to"`
		CrossRefs int    `json:"cross_refs"`
		Item      struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"item"`
	}
	if uerr := json.Unmarshal(res.Body, &got); uerr != nil {
		fmt.Fprintf(out, "옮겼으나 응답을 못 읽었다: %v\n", uerr)
		return 1
	}
	fmt.Fprintf(out, "move · %s 를 %s → %s 로 옮겼다\n", got.Item.ID, got.From, got.To)
	if got.Item.Title != "" {
		fmt.Fprintf(out, "제목: %s\n", got.Item.Title)
	}
	if got.CrossRefs > 0 {
		// 막지 않고 알린다 — 막으면 오등록을 되돌릴 길이 다시 0이 된다.
		fmt.Fprintf(out, "주의: 옛 프로젝트에 남은 항목 %d건이 이 항목을 선행으로 가리킨다. "+
			"그 관계는 프로젝트를 넘어 표현되지 않으므로 확인해라.\n", got.CrossRefs)
	}
	return 0
}
