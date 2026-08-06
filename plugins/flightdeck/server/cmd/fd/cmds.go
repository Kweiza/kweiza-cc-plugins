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

	"github.com/kweiza/flightdeck/internal/api"
	"github.com/kweiza/flightdeck/internal/buildinfo"
	"github.com/kweiza/flightdeck/internal/judge"
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

// TakeLeadingPositionals 는 맨 앞에서부터 이어지는 위치 인자를 **전부** 떼어낸다.
// TakeFirstPositional 과 같은 순서 문제(위/뒤 어느 쪽에 플래그가 와도 같은 뜻)를
// 여러 개의 위치 인자로 확장한 판이다 — `fd pick <id> <id>… [--flags]` 를 받으려면
// 첫 번째 하나만 떼는 것으로는 부족하다: 둘째 id 가 그대로 "모르는 플래그"로 보여
// flag.Parse 가 죽거나, 최악의 경우 조용히 버려진다(바로 이 태스크가 닫는 결함이다).
//
// ★ **`args[:i:i]` 로 자른다** — 3-인덱스 슬라이스라 cap 이 len 과 같아진다.
// `args[:i]` 로 두면(리뷰 라운드 1 finding 3) cap 이 호출자의 원본 배열 끝까지
// 남고, 호출부가 이 반환값에 `append` 하면 그 배열의 뒤쪽(=rest 가 가리키는 바로 그
// 메모리)을 덮어쓴다. 지금은 `run()` 이 args 를 다시 안 읽어 우연히 안 터졌을 뿐이다 —
// 호출부가 하나라도 늘면 그 순간 조용히 값이 바뀐다. cap 을 끊으면 append 가 항상
// 새 배열을 할당해 이 위험이 원천적으로 없다.
func TakeLeadingPositionals(args []string) (pos []string, rest []string) {
	i := 0
	for i < len(args) && !strings.HasPrefix(args[i], "-") {
		i++
	}
	return args[:i:i], args[i:]
}

// nonBlankPositionals 는 다듬으면 빈 문자열이 되는 항목을 골라낸다. 순수 함수다.
//
// ★ 왜 필요한가(리뷰 라운드 1 finding 1 — CRITICAL). `len(itemIDs) == 0` 만으로는
// `fd pick "   "` 를 못 거른다: 슬라이스 길이는 1이라 통과하고, 그 공백이 그대로
// URL 경로에 실려 `/api/v1/items/   /claim` 이 되어 서버가 **추천 경로**로 떨어진다.
// 그러면 아무것도 안 집었는데 종료코드 0·"브랜치: wa"·워크트리 명령이 나온다 —
// 이 태스크가 닫으려던 바로 그 모양("성공했다고 말하지만 아무것도 안 집었다")이
// 공백 인자 한 자리로 되살아난다. 옛 코드는 `strings.TrimSpace(itemID) == ""` 로
// 이 축을 봤는데, 다중 인자로 옮기며 그 트림을 빠뜨렸다.
func nonBlankPositionals(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			out = append(out, id)
		}
	}
	return out
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

// runPick 은 `fd pick <id> [<id>…]` 다. **오프라인에서는 거절된다.**
//
// ★ 인자가 여럿이면 **묶음 선점**이다 — 첫째가 선두(브랜치가 되는 id)이고 나머지는
// item_ids 로 그대로 실어 보낸다(claimReq.ItemIDs). 예전에는 TakeFirstPositional +
// fs.Arg(0) 로 딱 하나만 읽어, `fd pick c1 c2` 를 주면 c2 가 **말도 없이 버려지고**
// 종료코드는 0 이었다 — 추천 응답이 item_ids 로 묶음을 프리스크라이브하는 지금,
// 그 침묵은 "선점됐다고 믿고 시작했는데 실은 c2 를 아무도 안 쥔" 사고로 직행한다.
//
// ★ 인자 하나는 **item_ids 를 아예 안 싣는다**(claimReq.ItemIDs 가 비어 nil).
// 오늘까지의 단독 선점·재개 경로와 문자 그대로 같은 요청을 보내야 하기 때문이다 —
// service.PickInput.ItemIDs 가 1개짜리로 차 있어도 pickBundle 이 pickExplicit 과
// 같은 결과를 내긴 하지만(태스크 8), 요청 자체를 다르게 만들 이유가 없다.
func (a *App) runPick(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("pick")
	session := fs.String("cc-session", "", "Claude Code 세션 id")
	itemIDs, rest := TakeLeadingPositionals(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	// ★ **뒤에 남은 위치 인자도 합친다** — 비었을 때만 대신 쓰지 않는다. flag.Parse 는
	// 첫 비플래그 인자에서 멈추므로, `fd pick w1 --cc-session x w2` 처럼 id 사이에
	// 플래그가 끼면 앞에서 이미 w1 을 건졌어도 w2 는 fs.Args() 에 따로 남는다.
	// 여기서 안 합치면 이 갈래에서도 조용히 버려진다 — 이 태스크가 고치려는 사고 그대로다.
	//
	// ★ 합치는 것도 **같은 "-" 판정을 통과한 것만**이다(리뷰 라운드 1 finding 2).
	// flag.Parse 는 첫 비플래그 인자에서 멈추므로 그 뒤에 "-" 로 시작하는 오타가 있어도
	// (`fd pick ta --cc-session x tb --bogus`) fs.Args() 에 그대로 남는다. 이걸
	// 검증 없이 합치면 "--bogus" 가 항목 id 로 둔갑해 서버에 그대로 실려 가고,
	// 서버가 그것을 "못 찾음"으로 거절해도 **묶음의 나머지는 성공해 종료코드가 0** 이
	// 된다 — 같은 오타가 자리만 옮기면 통과하는 비대칭이라 여기서 막는다.
	tailIDs, leftover := TakeLeadingPositionals(fs.Args())
	itemIDs = append(itemIDs, tailIDs...)
	if len(leftover) > 0 {
		fmt.Fprintf(out, "플래그처럼 보이는 인자를 항목 id 자리로 못 읽었다: %s\n"+
			"플래그는 id 들 앞이나(맨 뒤 한 무더기로) 모아 써라 — id 사이에 있으면 그 뒤는 이 명령이 못 본다.\n",
			clip(leftover[0], 80))
		return 2
	}
	// ★ 길이만 보면 안 된다(리뷰 라운드 1 finding 1 — CRITICAL). `fd pick "   "` 는
	// len(itemIDs)==1 이라 통과했었고, 그 공백이 그대로 URL 에 실려 서버가
	// **추천 경로**로 떨어졌다 — 아무것도 안 집었는데 종료코드 0·"브랜치: …"·
	// 워크트리 명령이 나오는, 이 태스크가 닫으려던 바로 그 모양이다. 길이를 재기
	// 전에 반드시 다듬어 걸러낸다.
	itemIDs = nonBlankPositionals(itemIDs)
	if len(itemIDs) == 0 {
		fmt.Fprintln(out, "집을 항목 id 를 줘라: fd pick <item-id> [<item-id>…] — 여럿이면 첫째가 선두(브랜치)다")
		return 2
	}
	a.cli.Flush(ctx)
	sess, err := a.sessionID(ctx, *session)
	if err != nil {
		fmt.Fprintf(out, "선점하지 못했다: %v\n", err)
		return 1
	}
	a.cli.Session = sess
	req := claimReq{Project: a.proj.ID, SessionID: sess}
	if len(itemIDs) > 1 {
		req.ItemIDs = itemIDs // 선두 포함 전체 순서 — 경로는 선두(itemIDs[0])로 보낸다
	}
	res, err := a.cli.Write(ctx, "pick", "/api/v1/items/"+urlPath(itemIDs[0])+"/claim", req)
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
	// ★ **보낸 것과 돌아온 것을 대조한다.** 여기까지 오면 HTTP 는 200 이었지만
	// 그것은 "요청한 id 를 전부 다뤘다"를 뜻하지 않는다 — item_ids 를 모르는 구서버는
	// 그 필드를 조용히 버리고 경로의 선두 하나만 집는다(양쪽 api_version 이 "1" 이라
	// SkewBanner 도 안 뜬다). 그 응답을 그대로 렌더하고 0 을 내면 `fd pick a b c` 가
	// a 만 찍고 성공으로 끝난다 — b·c 는 아무도 안 쥔 채 이름조차 안 불린다.
	//
	// 종료코드를 1 로 낸다. 이 명령의 소비자는 사람만이 아니라 **스크립트와
	// 에이전트의 Bash 도구**이고, 그들이 읽는 유일한 기계 신호가 종료코드다.
	// 본문은 위에서 이미 냈다 — 선두는 실제로 집혔을 수 있으므로 지우지 않는다.
	if missing := judge.UnaccountedIDs(itemIDs, pr.AccountedIDs()); len(missing) > 0 {
		fmt.Fprint(out, mcpsrv.RenderBundleUnaccounted(missing))
		return 1
	}
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
	closeSession := fs.Bool("close", false, "항목을 끝낸 뒤 이 세션도 닫는다")
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

	// ★ 호출 둘이다(항목 finish → 세션 close). 한 트랜잭션이 아니므로 **끝났는데 못 닫은
	// 상태를 그대로 낸다** — 둘 다 성공한 척하면 다음 사람이 보드에서 이 카드를 보고
	// "아직 일하는 중"으로 읽는다.
	if *closeSession {
		if _, cerr := a.CloseSession(ctx, sess, "finish --close"); cerr != nil {
			fmt.Fprintf(out, "\n항목은 끝났으나 세션을 못 닫았다: %v\n", cerr)
			fmt.Fprintln(out, "선점은 반납됐으니 보드 ①에서는 이미 안 보인다 — 다만 겹침 판정에는 아직 잡힌다. 다시 닫으려면: fd close")
			return 1
		}
		fmt.Fprintln(out, "\n그리고 이 세션을 닫았다. 다음 신호가 오면 다시 살아난다.")
	}
	return 0
}

// runClose 는 이 세션을 닫는다.
//
// ★ 선점이 남아 있으면 거절한다. 닫힌 카드는 ListLive 에서 빠지고, 그러면 그 선점이
// **아무에게도 안 보인다** — 항목을 아무도 못 집는데 누가 잡았는지도 안 보이는 상태가 된다.
// 우회 플래그는 두지 않는다: 우회할 필드가 있으면 우회된다.
func (a *App) runClose(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("close")
	why := fs.String("why", "", "닫는 사유(표시 전용)")
	session := fs.String("cc-session", "", "Claude Code 세션 id")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cc := a.ccSessionID(*session)
	if cc == "" {
		fmt.Fprintln(out, "CLAUDE_CODE_SESSION_ID 를 못 읽었다 — 그 탐지가 깨진 것이다(fd doctor 가 그 축을 잰다).")
		return 1
	}
	// 선점 목록은 이 응답에 실려 온다. 따로 묻지 않는다 — 두 번 물으면 그 사이가 창이다.
	res, _, err := a.OpenSession(ctx, cc, "")
	if err != nil {
		fmt.Fprintf(out, "세션 좌표를 못 얻어 닫지 못했다: %v\n", err)
		return 1
	}
	if len(res.Claims) > 0 {
		fmt.Fprintf(out, "안 닫았다 — 선점 %d건이 남아 있다: %s\n",
			len(res.Claims), strings.Join(res.Claims, ", "))
		fmt.Fprintln(out, "닫으면 이 선점이 보드에서 사라진다 — 보드 ①은 선점을 든 카드만 낸다.")
		fmt.Fprintln(out, "먼저 끝내라: fd finish <item-id> --body …")
		return 1
	}

	sess, err := a.CloseSession(ctx, res.Session.ID, *why)
	if err != nil {
		fmt.Fprintf(out, "닫지 못했다: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "close · 세션 %s 를 닫았다 [%s]\n", sess.ID, sess.State)
	fmt.Fprintln(out, "다음 프롬프트·도구·MCP 호출이 오면 이 카드는 다시 살아난다 — 닫기는 판정이 아니라 관측이다.")
	return 0
}

// ─────────────────────────────────────────────────────────────────────────────
// 랜딩 레인
// ─────────────────────────────────────────────────────────────────────────────

// flagsSet 은 **실제로 준 플래그**의 이름들이다.
//
// ★ 값으로 판정하면 안 되는 자리가 있어서 필요하다: `--fail ""` 는 값이 빈 문자열이라
// "안 줬다"와 구분되지 않는데, 둘의 뜻이 정반대다(안 줬다=줄 서기 · 빈 사유로 보고=거절).
// 값으로 접으면 사유 없는 보고가 **조용히 줄 서기로 둔갑한다.**
func flagsSet(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// LandExitCode 는 land 응답 하나의 종료코드다. 순수 함수다.
//
// ★ 종료코드가 답하는 질문은 "요청이 성공했나"가 아니라 **"지금 랜딩해도 되는가"** 다.
// `fd land && <랜딩>` 이 이 명령의 가장 자연스러운 쓰임이고, 거기서 waiting 에 0 을 내면
// 그 한 줄이 배타를 통째로 우회한다 — 서버는 내내 옳고 아무 로그도 안 남는다.
//
// 모르는 상태도 1 이다. 0 으로 접으면 상태 낱말이 하나 늘어난 날 그 낱말이 조용히
// "랜딩해도 된다"가 된다.
func LandExitCode(state string) int {
	switch strings.TrimSpace(state) {
	case "turn": // 레인을 쥐었다
		return 0
	case "released", "left": // 놓겠다고 한 것을 놓았다
		return 0
	default: // waiting · reclaimed · 모르는 낱말
		return 1
	}
}

// runLand 는 `fd land` 다. 랜딩 줄의 취득·보고·이탈 셋을 한 명령으로 한다.
//
// ★ 셋을 한 명령에 둔 이유: 셋 다 **자기 줄 행 하나**를 다루는 일이고, 세션이 그 셋을
// 오가는 것이 정상 경로다(서고 → 기다리고 → 쓰고 → 놓는다). MCP 의 land 도구도 같은 축이라
// 두 표면의 모양이 같아진다.
//
// ★ **회수는 여기 없다**(fd lane release 다). 회수는 세션이 자기 자리를 다루는 일이 아니라
// 사람이 남의 점유를 끊는 일이다 — MCP 의 land 도구가 release 인자를 거절하는 것과 같은 판정이고,
// 무엇보다 land 는 폴링으로 **반복해서** 부르는 명령이라 그 자리에 회수를 섞으면
// 오타 한 번이 남의 랜딩을 끊는다.
//
// 값이 성립하는지(사유가 비었나 · 종류가 ok|fail 인가)는 **서버가 판정한다.** 여기서
// 한 벌 더 두면 두 벌이 되고, 두 벌은 반드시 표류한다. 이 함수가 보는 것은
// "플래그를 줬는가"뿐이다.
func (a *App) runLand(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("land")
	fail := fs.String("fail", "", "검증이 깨져 반납한다 — 사유를 함께 준다")
	leave := fs.String("leave", "", "줄에서 스스로 빠진다 — 사유를 함께 준다")
	ok := fs.Bool("ok", false, "다 쓰고 반납한다(랜딩됐다는 뜻이 아니다 — 레인을 놓았다는 뜻이다)")
	session := fs.String("cc-session", "", "Claude Code 세션 id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	set := flagsSet(fs)
	// ★ 불리언만 **값도 본다.** `--ok=false` 는 준 것이지만 "보고하겠다"는 뜻이 아니다.
	//   준 것만 보면 그 표기가 반납으로 둔갑하고, 그 반납은 되돌릴 수 없다.
	//   문자열 둘은 반대다 — `--fail ""` 은 "사유 없이 보고하겠다"라서 서버가 거절해야 한다.
	chose := map[string]bool{"ok": set["ok"] && *ok, "fail": set["fail"], "leave": set["leave"]}

	// 둘 이상을 준 것은 도구가 조용히 하나를 고를 일이 아니다 — 보고와 이탈은 다른 원장 결과다.
	var given []string
	for _, n := range []string{"ok", "fail", "leave"} {
		if chose[n] {
			given = append(given, "--"+n)
		}
	}
	if len(given) > 1 {
		fmt.Fprintf(out, "%s 를 함께 줬다 — 한 번에 하나만 해라.\n", strings.Join(given, " 와 "))
		fmt.Fprintln(out, "레인을 반납하려면 --ok|--fail <사유>, 줄에서 완전히 빠지려면 --leave <사유> 다.")
		return 2
	}

	cmd, req := CmdLandAcquire, landReq{Mode: api.LandModeAcquire}
	switch {
	case chose["ok"]:
		cmd = CmdLandReport
		req = landReq{Mode: api.LandModeReport, Kind: string(model.LandingLeftOK)}
	case chose["fail"]:
		cmd = CmdLandReport
		req = landReq{Mode: api.LandModeReport, Kind: string(model.LandingLeftFail), Detail: *fail}
	case chose["leave"]:
		cmd = CmdLandLeave
		req = landReq{Mode: api.LandModeLeave, Detail: *leave}
	}

	a.cli.Flush(ctx)
	sess, err := a.sessionID(ctx, *session)
	if err != nil {
		fmt.Fprintf(out, "%s 하지 못했다: %v\n", cmd, err)
		return 1
	}
	a.cli.Session = sess
	req.Project, req.SessionID = a.proj.ID, sess

	res, err := a.cli.Write(ctx, cmd, landingPath, req)
	if err != nil {
		fmt.Fprintf(out, "%s 하지 못했다: %v\n", cmd, err)
		return 1
	}
	if !res.Sent {
		// 레인 명령의 열화는 전부 거절이라 여기 오지 않는다. 그래도 조용히 성공으로 접지 않는다 —
		// 표가 바뀌는 날 "안 한 일"이 0 으로 끝나는 것이 이 자리의 유일한 사고다.
		fmt.Fprintf(out, "%s: %s\n", res.Mode, res.Reason)
		return 1
	}
	var lr service.LandResult
	if err := json.Unmarshal(res.Body, &lr); err != nil {
		fmt.Fprintf(out, "랜딩 응답 해석 실패: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, strings.TrimRight(mcpsrv.RenderLand(lr, a.now()), "\n"))
	// ★ 렌더는 MCP 도구와 **공유**한다(같은 사실을 두 벌로 그리지 않는다). 그 대가로
	//   차례 안내가 도구 인자 이름(result·leave)으로 적혀 있어 이 셸에서는 부를 수 없는
	//   이름이다 — 없는 손잡이를 가리키는 문구는 이 레포가 결함으로 분류하는 부류라,
	//   그 자리에서 이 채널의 이름으로 옮겨 준다.
	if lr.State == "turn" {
		fmt.Fprintln(out, "이 셸에서는: fd land --ok · fd land --fail \"<사유>\" · fd land --leave \"<사유>\"")
	}
	return LandExitCode(lr.State)
}

// runLane 은 `fd lane <하위명령>` 이다. 지금 있는 하위 명령은 release 하나다.
//
// 하위 명령을 하나 두려고 이름 공간을 여는 이유: 이 자리에 앞으로 오는 것들
// (레인 목록·강제 재정렬)이 전부 **레인 전체를 다루는 사람의 일**이고,
// 세션이 자기 자리를 다루는 land 와 섞이면 안 된다.
func (a *App) runLane(ctx context.Context, args []string, out io.Writer) int {
	const help = "fd lane release --row <id> --reason \"...\"  — 물린 줄 행 하나를 사람이 회수한다"
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(out, "lane 하위 명령을 줘라. 지금 있는 것은 release 하나다:")
		fmt.Fprintln(out, "  "+help)
		return 2
	}
	switch args[0] {
	case "release":
		return a.runLaneRelease(ctx, args[1:], out)
	default:
		fmt.Fprintf(out, "모르는 lane 하위 명령: %s\n  %s\n", clip(args[0], 40), help)
		return 2
	}
}

// laneActor 는 회수를 **누가 했나**다. 순수 함수다.
//
// 이 값은 회수 판단 본문에 그대로 박혀 불변으로 남는다. 그래서 지어내지 않는다:
// 세션 좌표가 있으면 그것이 가장 정확하고(어느 세션에서 끊었나), 없으면 사람 좌표로 접고,
// 둘 다 없으면 **빈 문자열**이다. 모르는 것을 "cli" 같은 고정 문자열로 채우면
// 그 판단이 영원히 "모름"을 "cli 가 했음"으로 말하게 된다.
func laneActor(fromFlag, ccSession, user, host string) string {
	if v := strings.TrimSpace(fromFlag); v != "" {
		return v
	}
	if v := strings.TrimSpace(ccSession); v != "" {
		return v
	}
	u, h := strings.TrimSpace(user), strings.TrimSpace(host)
	switch {
	case u != "" && h != "":
		return u + "@" + h
	case u != "":
		return u
	case h != "":
		return h
	default:
		return ""
	}
}

// runLaneRelease 는 `fd lane release --row <id> --reason "..."` 다.
//
// ★ **물린 레인의 유일한 탈출구다.** 자동 만료가 없고, 세션 정체가
// (machine, worktree, cc_session_id) 라 죽은 세션 명의로 land(leave) 를 부를 방법이 없다.
// 이 명령이 없으면 복구가 sqlite3 직접 UPDATE 뿐이고 — 그 경로에는 판단이 한 줄도 안 남는다.
//
// ★ **세션을 요구하지 않는다.** 요구하는 순간 탈출구가 다시 막힌다: 회수하는 사람은
// 대개 그 세션이 아니고, 그 세션은 이미 죽었다. 그래서 열지도(a.sessionID) 않는다.
func (a *App) runLaneRelease(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("lane release")
	row := fs.Int64("row", 0, "회수할 줄 행 번호(보드의 레인 절과 land 응답이 낸다)")
	reason := fs.String("reason", "", "왜 회수하나 — 필수. 판단으로 원장에 남는다")
	actor := fs.String("actor", "", "누가 회수하나(비면 이 셸의 좌표로 채운다)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	set := flagsSet(fs)
	// 여기서 보는 것은 **플래그를 줬는가**뿐이다. 값이 성립하는지(사유가 비었나 ·
	// 그 행이 살아 있나)는 서버가 판정한다 — 그 판정에는 처방까지 붙어 있고,
	// 사본을 여기에 두면 두 벌이 표류한다.
	if !set["row"] || !set["reason"] {
		fmt.Fprintln(out, "회수 대상과 사유를 줘라: fd lane release --row <id> --reason \"...\"")
		fmt.Fprintln(out, "번호는 보드의 레인 절과 land 응답이 낸다. 사유 없는 회수는 나중에 되짚을 수 없다.")
		return 2
	}

	a.cli.Flush(ctx)
	user, _ := a.env("USER")
	if strings.TrimSpace(user) == "" {
		user, _ = a.env("LOGNAME")
	}
	res, err := a.cli.Write(ctx, CmdLaneRelease, laneReleasePath(*row), laneReleaseReq{
		Project: a.proj.ID,
		Actor:   laneActor(*actor, a.ccSessionID(""), user, a.host),
		Reason:  *reason,
	})
	if err != nil {
		fmt.Fprintf(out, "회수하지 못했다: %v\n", err)
		return 1
	}
	if !res.Sent {
		fmt.Fprintf(out, "%s: %s\n", res.Mode, res.Reason)
		return 1
	}
	var rr service.LaneReleaseResult
	if err := json.Unmarshal(res.Body, &rr); err != nil {
		fmt.Fprintf(out, "회수는 됐으나 응답 해석 실패: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "lane release · 줄 행 %d 를 회수했다(세션 %s)\n", rr.RowID, rr.SessionID)
	if rr.HeldRelease {
		fmt.Fprintln(out, "레인 점유까지 풀었다 — 다음 land 가 그 자리를 가져간다.")
	} else {
		// 대기 중인 행이었다. 이 사실을 안 말하면 "레인이 풀렸다"고 오해하고
		// 진짜 점유자를 그대로 둔 채 랜딩하러 간다.
		fmt.Fprintln(out, "이 행은 대기 중이었다 — 점유는 원래 없었고 줄에서만 뺐다. 레인은 여전히 남이 쥐고 있을 수 있다.")
	}
	if rr.JudgmentID != "" {
		fmt.Fprintf(out, "판단 %s 에 남겼다 — 무엇을 관측하고 끊었는지가 거기 있다.\n", rr.JudgmentID)
	}
	return 0
}

// runClaim 은 `fd claim <하위명령>` 이다. 지금 있는 하위 명령은 release 하나다.
//
// (세션이 항목을 집는 것은 pick/MCP 의 일이라 여기 없다 — 이 명령군은 **사람의**
// 표면이다. pick 이 steal_reason 을 거절하는 것과 한 쌍의 설계다.)
func (a *App) runClaim(ctx context.Context, args []string, out io.Writer) int {
	const help = "fd claim release --item <id> --reason \"...\"  — 죽은 세션의 선점 하나를 사람이 회수한다"
	// 선두가 플래그면 하위 명령을 빼먹은 것이다(runLane 과 같은 갈래) — 그대로 두면
	// `fd claim --item x` 가 "모르는 하위 명령: --item" 이라는 엉뚱한 말을 한다.
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(out, "claim 하위 명령을 줘라. 지금 있는 것은 release 하나다:")
		fmt.Fprintln(out, "  "+help)
		return 2
	}
	switch args[0] {
	case "release":
		return a.runClaimRelease(ctx, args[1:], out)
	default:
		fmt.Fprintf(out, "모르는 claim 하위 명령: %s\n  %s\n", clip(args[0], 40), help)
		return 2
	}
}

// runClaimRelease 는 `fd claim release --item <id> --reason "..."` 다.
//
// ★ **죽은 선점의 유일한 CLI 탈출구다.** claim 에는 자동 만료가 없고(schema.sql —
// 생존 오판 실측 2회로 의식적으로 기각), 세션 정체가 (machine, worktree, cc_session_id) 라
// 죽은 세션 명의로는 아무 호출도 못 한다. 이 명령이 없으면 복구가 대시보드 폼 아니면
// sqlite3 직접 UPDATE 인데 — 후자에는 판단이 한 줄도 안 남는다.
//
// ★ **세션을 요구하지 않는다**(레인 회수와 같은 판정) — 회수하는 사람은 대개
// 그 세션이 아니고, 그 세션은 이미 죽었다.
func (a *App) runClaimRelease(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("claim release")
	item := fs.String("item", "", "회수할 항목 id(보드의 세션 카드·창 밖 선점 줄이 낸다)")
	reason := fs.String("reason", "", "왜 회수하나 — 필수. 판단으로 원장에 남는다")
	actor := fs.String("actor", "", "누가 회수하나(비면 이 셸의 좌표로 채운다)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	set := flagsSet(fs)
	// 여기서 보는 것은 **플래그를 줬는가**뿐이다. 값이 성립하는지(사유가 비었나 ·
	// 그 선점이 살아 있나)는 서버가 판정한다 — 사본을 여기 두면 두 벌이 표류한다.
	if !set["item"] || !set["reason"] {
		fmt.Fprintln(out, "회수 대상과 사유를 줘라: fd claim release --item <id> --reason \"...\"")
		fmt.Fprintln(out, "잡힌 항목은 보드가 낸다. 사유 없는 회수는 나중에 되짚을 수 없다.")
		return 2
	}

	a.cli.Flush(ctx)
	user, _ := a.env("USER")
	if strings.TrimSpace(user) == "" {
		user, _ = a.env("LOGNAME")
	}
	res, err := a.cli.Write(ctx, CmdClaimRelease, claimReleasePath(*item), claimReleaseReq{
		Project: a.proj.ID,
		Actor:   laneActor(*actor, a.ccSessionID(""), user, a.host),
		Reason:  *reason,
	})
	if err != nil {
		fmt.Fprintf(out, "회수하지 못했다: %v\n", err)
		return 1
	}
	if !res.Sent {
		fmt.Fprintf(out, "%s: %s\n", res.Mode, res.Reason)
		return 1
	}
	var rr service.ClaimReclaimResult
	if err := json.Unmarshal(res.Body, &rr); err != nil {
		fmt.Fprintf(out, "회수는 됐으나 응답 해석 실패: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "claim release · %s 의 선점을 회수했다(점유자였던 세션 %s)\n", rr.Item, rr.Holder)
	fmt.Fprintln(out, "항목은 open 으로 돌아갔다 — 다음 pick 이 집을 수 있다.")
	if rr.JudgmentID != "" {
		fmt.Fprintf(out, "판단 %s 에 남겼다 — 무엇을 관측하고 회수했는지가 거기 있다.\n", rr.JudgmentID)
	}
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
	// ★ **판 나이를 맨 위에 찍는다.** 아래 축이 전부 맞는데 답이 이상한 경우의 원인이
	// 여기였다 — 코드는 멀쩡하고 도는 판이 낡았을 뿐인데 그 사실이 어디에도 안 보였다.
	// api_version 은 계약이 깨질 때만 오르므로 이 축을 못 나른다.
	fmt.Fprintf(out, "  이 판 %s\n", buildinfo.Short(buildinfo.Self()))
	// ★ 스프레드 호출(`ExeLines(os.Executable())`)이 더 이상 안 된다 — **기대 자리**를 셋째
	// 인자로 받기 때문이다. 그 자리를 여기서 다시 조립하지 않는다: a.binDir 은 newApp 이
	// BinCacheDir 로 한 번만 채운 값이고, 판단이 두 자리에 살면 이 줄이 조용히 거짓 경보를
	// 낸다(app.go 의 binDir 주석). 빈 문자열도 **그대로 넘긴다** — ExeLines 가 그때 자리
	// 축을 안 내는 것이 계약이라 호출부가 가를 것이 없다.
	exe, exeErr := os.Executable()
	for _, line := range ExeLines(exe, exeErr, a.binDir) {
		fmt.Fprintln(out, "  "+line)
	}
	// ★ **한 줄이던 것을 둘로 가른다.** 예전에는 「상태 디렉토리」 한 줄이었는데 그 이름이
	// 이제 두 자리를 뜻한다 — 응답 캐시는 여전히 채널 사다리(CLAUDE_PLUGIN_DATA·
	// XDG_STATE_HOME)를 타고(값이 자기 시각을 달고 다녀 채널마다 갈려도 각자 옳다 —
	// cache.go 의 CacheEntry.At), 바이너리 캐시는 채널 무관한 고정 자리로 떨어져 나갔다
	// (exec 되고 나면 어느 판인지 안 말하고 답하므로 두 벌이 다르면 하나가 거짓이다).
	// 한 줄로 두면 그 갈림 자체가 화면에서 사라진다 — 2026-08-06 에 두 자리의 빌드 시각이
	// 55분 어긋나 한 응답의 서버 축과 렌더 축이 갈렸을 때, 이 줄을 본 사람은
	// "상태 디렉토리는 맞는데?"에서 멈췄다. 축이 둘이면 줄도 둘이어야 한다.
	fmt.Fprintf(out, "  응답 캐시 %s (%s)\n", a.sd.Path, a.sd.Source)
	if a.binDir == "" {
		// ★ 빈 자리를 %s 로 흘리면 「바이너리 캐시  (…)」가 되어 '못 읽었다'로 읽힌다.
		// 여기는 **자리가 없는 것이 정상 판정인** 유일한 축이다(HOME 도 FD_STATE_DIR 도
		// 없어 런처가 짓기를 거절한 상태). 값이 없으므로 답은 사유 쪽에 있다.
		fmt.Fprintf(out, "  바이너리 캐시 없음 (%s)\n", a.binSrc)
	} else {
		// 파일 이름이 아니라 **디렉토리**를 찍는다 — 이름의 키 규칙(fd-<접은 소스 트리>)은
		// 런처가 유일한 주인이라 여기서 해독하지 않는다. 지금 도는 파일이 무엇인지는
		// 바로 위 '실행 파일' 줄이 이미 답했고, 둘을 견주는 일도 그 줄이 한다.
		fmt.Fprintf(out, "  바이너리 캐시 %s (%s)\n", a.binDir, a.binSrc)
	}
	// 머신 id 는 세션 정체 3중키의 첫 축이다. **값만 찍으면 부족하고 읽은 자리를 함께 찍는다** —
	// 이 축이 채널마다 갈려 한 세션이 카드 세 장으로 떴을 때, 값이 다르다는 것보다
	// "어느 파일에서 왔나"가 원인에 이르는 열쇠였다(그 줄이 없어 /proc 을 뒤져야 했다).
	fmt.Fprintf(out, "  머신 %s (%s)\n", a.machine, a.machineSrc)
	// ★ 비콘 자리도 **사유를 함께** 찍는다. 머신 id 와 같은 이유다 — 이제 세션 정체가
	// 이 자리를 거쳐 오는데(07e5df4), 워크트리 불일치 알림을 본 사람이 여기를 못 보면
	// 자기 셸 채널의 워크트리만 보고 MCP 의 것은 못 본다. 그 불일치가 불일치의 전부인데.
	fmt.Fprintf(out, "  창 비콘 %s (%s)\n", a.beaconDir, a.beaconSrc)
	// ★ 주소·토큰도 **어디서 읽었는지**를 찍는다. machineSrc 가 그 선례다 —
	// 값이 예상과 다를 때 "왜 저 값인가"에 답할 자리가 없으면 /proc 을 뒤지게 된다.
	fmt.Fprintf(out, "  서버 주소 %s (%s)\n", a.cli.URL, a.cli.Endpoint.URLSource)
	fmt.Fprintf(out, "  서버 토큰 %s (%s)\n",
		map[bool]string{true: "설정됨", false: "없음"}[a.cli.Token != ""], a.cli.Endpoint.TokenSource)
	fmt.Fprintf(out, "  프로젝트 %s · 주 저장소 %s · 워크트리 %s\n", a.proj.ID, a.proj.Path, a.proj.Worktree)
	fmt.Fprintf(out, "  좌표 판정: %s\n", a.proj.Detail)
	// 처방 채널은 부재를 기본값으로 접지 않는다 — 2026-08-04 에 실측한 사실 그대로 찍는다
	// (Claude Code 2.1.221: Stop 훅 stdout 의 additionalContext 가 실제로 주입된다).
	fmt.Fprintln(out, "  처방 채널   Stop 훅 stdout      (2026-08-04 실측: 주입됨)")
	// ★ 아웃박스는 이제 **채널 무관한 고정 자리**에 있다(OutboxPath). 그래서 이 줄이 세는
	// 것은 이 채널의 대기가 아니라 **이 머신의 대기**다 — 예전에는 채널마다 달랐다.
	// 자리와 사유를 함께 찍는 것은 그대로다: 값이 예상과 다를 때 "왜 저기냐"에 답할 자리다.
	if pend, err := a.cli.Outbox.List(); err != nil {
		fmt.Fprintf(out, "  ! 아웃박스를 못 읽었다: %v\n", err)
	} else {
		fmt.Fprintf(out, "  아웃박스 대기 %d건 (%s · %s)\n",
			len(pend), a.cli.Outbox.Dir(), a.cli.Outbox.Source())
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
	// ★ 옛 채널 자리에 남은 것. 대기는 다음 재생이 **보내서** 비우고, 격리는 그 자리에 남는다
	// (보관소는 제 큐 옆에 남는 것이 설계다 — '어디서 온 것인가'가 사라지면 안 된다).
	for _, lo := range a.cli.LegacyLeftovers() {
		fmt.Fprintf(out, "  ! 옛 자리 %s — 대기 %d건(다음 재생이 보낸다) · 격리 %d건(그 자리에 남는다)\n",
			lo.Dir, lo.Pending, lo.Rejected)
		if lo.Err != "" {
			fmt.Fprintf(out, "      ! 세다 걸렸다: %s\n", clip(lo.Err, 200))
		}
	}
	// ★ 옛 **바이너리** 자리에 남은 것. 위 아웃박스 줄과 **판이 다르다**: 저쪽은 다음 재생이
	// 보내서 스스로 비우고, 이쪽은 **아무도 안 비운다.** 자리를 고정 자리로 옮길 때 옛 자리를
	// 이관도 삭제도 안 하기로 했고(그 판정은 유효하다 — LegacyBinDirs 주석), pruneBinCache 는
	// 지금 자리 한 디렉토리만 훑는다. 그래서 이 줄이 **그 자리를 말하는 유일한 표면**이다.
	// 여기에 지우는 코드를 얹지 마라 — doctor 는 말만 한다.
	//
	// ★ ExeLines 의 "자리 밖" 줄과 **겹칠 수 있고, 겹쳐도 된다.** 축이 다르다: 그쪽은 지금
	// 도는 **프로세스**, 이쪽은 아무도 안 도는 **파일**이다. 이행 창에는 둘 다 참이고,
	// 하나로 접으면 세션이 재기동한 뒤 42MB 가 다시 안 보이게 된다(그 침묵이 이 항목이다).
	//
	// ★ **자리를 여기서 조립하지 않는다.** 후보 목록은 LegacyBinDirs 하나가 갖고,
	// 홈은 homeDir 하나가 갖는다(app.go 가 부르는 그 함수다 — 사본이 아니라 같은 주인이다).
	// 목표는 a.binDir 을 그대로 넘긴다: 지금 자리가 옛 자리로 찍히면 그 자체가 거짓이다.
	//
	// ★ **이 자리에 두는 이유** — 바로 아래 "이 채널이 계산할 수 있는 자리만" 문장이 이 줄의
	// 정직성을 떠받친다(채널마다 후보가 갈리는 것이 이 축의 성질이다). 「바이너리 캐시」 줄 옆에
	// 붙이면 그 문장과 30줄 떨어져 사람이 둘을 안 잇는다.
	for _, lb := range legacyBinLeftovers(LegacyBinDirs(a.env, homeDir(a.env), a.binDir)) {
		if lb.Err != "" {
			// 0개와 '못 셌다'를 가른다 — 침묵은 '깨끗하다'로 읽힌다(훅 배너가 큐에 대해
			// 같은 판정을 한다: hook_banner_legacy_test.go 의 "못 셌다" 축).
			fmt.Fprintf(out, "  ! 옛 바이너리 자리 %s — 세다 걸렸다: %s\n", lb.Dir, clip(lb.Err, 200))
			continue
		}
		fmt.Fprintf(out, "  ! 옛 바이너리 자리 %s — fd %d개 · %s"+
			"(아무도 안 쓴다. 지우려면 사람이 지운다)\n", lb.Dir, lb.Files, humanBytes(lb.Bytes))
	}
	// ★ **이 목록이 못 보는 범위를 함께 찍는다.** 빼면 "0건"이 '깨끗하다'로 읽히는데,
	// 그것은 안 잰 축을 잰 척하는 것이다(§13). 이 문장이 그 축의 유일한 파수꾼이다.
	// ★ 이제 **두 부류**를 덮는다(아웃박스 큐 · 바이너리 자리). 둘 다 후보가 채널 환경에서
	// 오므로 같은 한계를 갖고, 바이너리 쪽은 그 한계가 곧 "갈린 보고가 각자 옳다"의 근거다
	// (env.go 의 LegacyBinDirs ★). 이름을 안 대면 그 근거가 화면에서 사라진다.
	fmt.Fprintln(out, "  옛 자리 탐색(아웃박스 큐 · 바이너리 자리)은 이 채널이 계산할 수 있는 "+
		"자리만이다 — 다른 채널(훅·MCP 는 CLAUDE_PLUGIN_DATA)의 자리는 여기서 안 보인다.")
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

// legacyBinPrefix 는 **옛** 런처가 짓던 산출물의 이름 접두다.
//
// ★ **bincache.go 의 `binCachePrefix`("fd-")를 쓰지 않는다. 두 상수를 합치지 마라.**
// 방향이 반대다 — 저쪽은 GC 가 **안 지울 것**을 넓히는 배제 조건이라(남의 파일일 수 있다)
// 좁게 잡을수록 안전하고, 이쪽은 진단이 **말할 것**을 넓히는 포함 조건이라 좁게 잡으면
// 침묵한다. 그리고 옛 런처가 짓던 이름은 접두가 없는 그냥 `fd` 다(개정 전 bin/fd 의
// `bin="$state/bin/fd"`). "fd-" 로 세면 이 줄이 존재하는 유일한 이유인 그 두 파일이
// **정확히 0개**로 세어진다(2026-08-07 실측 잔존이 둘 다 이름 `fd` 다).
// exe.go 가 자리 축에서 같은 함정을 같은 근거로 피했다.
const legacyBinPrefix = "fd"

// legacyBin 은 옛 바이너리 자리 하나에 남은 것이다. **읽기만 한다.**
type legacyBin struct {
	Dir   string
	Files int
	Bytes int64
	Err   string // 셀 수 없었던 사유. 0개와 '못 쟀다'를 가르는 자리다
}

// legacyBinLeftovers 는 후보 자리들을 stat 해서 **남은 것이 있는 자리만** 낸다.
//
// ★ **판정이 여기 없다.** 자리를 고르는 일은 LegacyBinDirs 가, 크기 문구는 humanBytes 가,
// 출력은 runDoctor 가 한다. 여기가 하는 것은 세기뿐이다.
//
// ★ 빈 자리·없는 자리는 **안 낸다.** 후보가 셋이라 매 실행마다 세 줄이 뜨면 그것은 진단이
// 아니라 배경 소음이고, 소음이 된 줄은 정작 참인 날 아무도 안 읽는다(LegacyLeftovers 가
// 같은 판정을 같은 이유로 한다 — client.go 의 "빈 자리는 안 낸다").
//
// ★ **bincache.go 의 readBinCache 를 안 쓴다.** 그쪽 산출물은 GC 판정의 입력(경로·mtime)이라
// 크기가 없다. 크기 축을 그쪽에 얹으면 GC 의 입력 구조체가 **진단 때문에** 넓어지는데,
// GC 가 재는 축은 나이 하나다. 대신 여기에는 판정이 하나도 없어서 사본이 될 것도 없다.
//
// ★ 디렉토리는 안 센다(런처는 거기에 디렉토리를 안 짓는다). 재귀도 안 한다 — 남의 트리를
// 훑어 크기를 부풀리면 사람이 지울 대상을 오판한다.
func legacyBinLeftovers(dirs []string) []legacyBin {
	var out []legacyBin
	for _, dir := range dirs {
		ents, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue // 아예 없는 자리다. 이 머신이 그 판을 안 거쳤거나 사람이 이미 지웠다
			}
			out = append(out, legacyBin{Dir: dir, Err: err.Error()})
			continue
		}
		lb := legacyBin{Dir: dir}
		for _, e := range ents {
			if e.IsDir() || !strings.HasPrefix(e.Name(), legacyBinPrefix) {
				continue
			}
			info, ierr := e.Info()
			if ierr != nil {
				continue // 훑는 사이 사라졌다 — 사람이 지우는 중이다. 그것은 오류가 아니다
			}
			lb.Files++
			lb.Bytes += info.Size()
		}
		if lb.Files == 0 {
			continue
		}
		out = append(out, lb)
	}
	return out
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
