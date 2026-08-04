package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/mcpsrv"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 훅 — 전부 **fail-open**. 어떤 입력에도, 어떤 실패에도 종료코드 0 이다.
//
// 이것이 깨지면 세션이 안 뜬다. 그래서 이 파일의 모든 경로는 오류를 화면이 아니라
// 로그(stderr)로 보내고 stdout 에는 훅 계약이 요구하는 JSON 만 낸다.

// HookPayload 는 훅 stdin 으로 오는 것 중 이 도구가 쓰는 축이다.
//
// 모르는 필드는 무시한다(DisallowUnknownFields 를 쓰지 않는다) — 플랫폼이 필드를
// 늘리는 것은 정상이고, 그때 훅이 죽으면 세션이 안 뜬다.
type HookPayload struct {
	SessionID      string         `json:"session_id"`
	TranscriptPath string         `json:"transcript_path"`
	CWD            string         `json:"cwd"`
	HookEventName  string         `json:"hook_event_name"`
	Source         string         `json:"source"`
	Prompt         string         `json:"prompt"`
	ToolName       string         `json:"tool_name"`
	ToolInput      map[string]any `json:"tool_input"`
	Trigger        string         `json:"trigger"`
	CustomInstr    string         `json:"custom_instructions"`
	// StopHookActive 는 지금 이 호출 자체가 **Stop 훅의 주입이 만든 턴**의 끝에서
	// 왔다는 뜻이다. hookStop 이 이것을 확인 없이 넘기면 무한 루프다(hookStop 주석 참고).
	StopHookActive bool `json:"stop_hook_active"`
}

// ParseHookPayload 는 훅 stdin 을 읽는다. 순수 함수다.
//
// **빈 입력과 깨진 JSON 을 가른다.** 둘 다 실패지만 처방이 다르다 —
// 빈 입력은 훅이 잘못 연결된 것이고, 깨진 JSON 은 플랫폼이 바뀐 것이다.
// 둘 중 무엇인지 로그에 남지 않으면 그날 아무도 원인을 못 찾는다.
func ParseHookPayload(raw []byte) (HookPayload, error) {
	var p HookPayload
	if len(strings.TrimSpace(string(raw))) == 0 {
		return p, fmt.Errorf("훅 stdin 이 비었다 — 훅이 페이로드 없이 불렸다(연결 오류이거나 수동 실행이다)")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("훅 페이로드가 JSON 이 아니다(%d바이트): %w", len(raw), err)
	}
	return p, nil
}

// EditedPaths 는 PostToolUse 페이로드에서 편집 대상 경로를 뽑는다. 순수 함수다.
//
// ★ 미커밋 발자국의 **유일한 원천**이다(설계 §6). 여기서 조용히 0건이 되면
// 착수 직후 구간의 경로 겹침 축이 통째로 죽는다 — 그 구간은 브랜치 diff 가 정의상 비어 있다.
// 그래서 도구마다 다른 키 이름을 전부 본다.
func EditedPaths(toolInput map[string]any) []string {
	if toolInput == nil {
		return nil
	}
	var out []string
	for _, key := range []string{"file_path", "path", "notebook_path"} {
		if v, ok := toolInput[key].(string); ok && strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	// MultiEdit 류는 edits 배열 안에 경로가 없고 file_path 하나다. 배열형은 아래로 덮는다.
	if raw, ok := toolInput["file_paths"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// hookOutput 은 훅 stdout 계약이다. additionalContext 가 없으면 **아무것도 안 낸다** —
// 빈 객체를 내면 컨텍스트 예산을 소모하면서 아무 정보도 안 준다.
type hookOutput struct {
	HookSpecificOutput hookSpecific `json:"hookSpecificOutput"`
}

type hookSpecific struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

func emitContext(out io.Writer, event, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	buf, err := json.Marshal(hookOutput{HookSpecificOutput: hookSpecific{
		HookEventName: event, AdditionalContext: text,
	}})
	if err != nil {
		return // 직렬화가 실패해도 세션을 막지 않는다. 사유는 호출부가 로그로 남긴다
	}
	fmt.Fprintln(out, string(buf))
}

// runHook 은 훅 하나를 처리한다. **항상 0 을 돌려준다.**
func (a *App) runHook(ctx context.Context, event string, stdin io.Reader, out io.Writer) int {
	raw, rerr := io.ReadAll(io.LimitReader(stdin, 4<<20))
	if rerr != nil {
		a.log.Error("훅 stdin 읽기 실패", "error", rerr.Error(), "mode", event)
		raw = nil
	}
	p, perr := ParseHookPayload(raw)
	if perr != nil {
		// 페이로드가 없어도 SessionStart 는 배너를 내야 한다 — 서버 상태를 알리는 것이
		// 이 훅의 존재 이유이고, 그 이유는 세션 id 를 몰라도 유효하다.
		a.log.Warn("훅 페이로드 해석 실패", "mode", event, "error", perr.Error())
	}

	switch event {
	case "session-start":
		a.hookSessionStart(ctx, p, out)
	case "user-prompt":
		a.hookUserPrompt(ctx, p, out)
	case "post-tool":
		a.hookPostTool(ctx, p)
	case "pre-compact":
		a.hookPreCompact(ctx, p)
	case "stop":
		a.hookStop(ctx, p, perr, out)
	default:
		a.log.Error("모르는 훅 이름", "mode", clip(event, 40),
			"error", "session-start|user-prompt|post-tool|pre-compact|stop 중 하나여야 한다")
	}
	return 0 // ★ 이 함수의 반환값은 항상 0 이다. 훅이 세션을 막으면 안 된다
}

// hookSessionStart 는 보드 요약 + 내 선점 + 미확인 + **서버 상태 배너**를 낸다.
func (a *App) hookSessionStart(ctx context.Context, p HookPayload, out io.Writer) {
	if strings.TrimSpace(p.CWD) != "" {
		a.proj = resolveProject(a.env, p.CWD)
	}
	a.cli.Flush(ctx)

	in := SessionStartInput{
		Now:       a.now(),
		ServerURL: a.cli.URL,
		Project:   a.proj.ID,
		Worktree:  a.proj.Worktree,
		Notice:    a.notice,
	}
	banner, reachable := a.ServerBanner(ctx)
	in.Banner = banner

	if pend, err := a.cli.Outbox.List(); err == nil {
		in.Pending = len(pend)
	} else {
		a.log.Warn("아웃박스 조회 실패", "error", err.Error())
	}

	cc := a.ccSessionID(p.SessionID)
	if cc == "" {
		in.Notice = strings.TrimSpace(in.Notice + " CLAUDE_CODE_SESSION_ID 도 훅 페이로드의 session_id 도 못 읽었다 — 이 세션은 등록되지 않는다(fd doctor 가 그 축을 잰다).")
		emitContext(out, "SessionStart", RenderSessionStart(in))
		return
	}

	res, stale, err := a.OpenSession(ctx, cc, "")
	if err != nil {
		a.log.Error("훅에서 세션 열기 실패", "mode", "session-start", "error", err.Error())
		if reachable {
			in.Notice = strings.TrimSpace(in.Notice + " 세션 등록에 실패했다: " + clip(err.Error(), 200))
		}
	} else {
		in.SessionID, in.Created, in.Claims = res.Session.ID, res.Created, res.Claims
		if stale {
			in.Notice = strings.TrimSpace(in.Notice + " 이 세션 정보는 캐시다 — 서버에 아직 등록되지 않았다.")
		}
	}

	v, boardBanner, berr := a.Board(ctx, in.SessionID)
	if berr != nil {
		a.log.Error("훅에서 보드 조회 실패", "mode", "session-start", "error", berr.Error())
		if in.Banner == "" && boardBanner != "" {
			in.Banner = boardBanner
		}
	} else {
		in.Board = mcpsrv.RenderBoard(v, mcpsrv.BoardRenderOptions{Self: in.SessionID, Now: a.now()})
		in.BoardStale = !reachable
		in.Asks, in.Blocked = v.Asks, v.Blocked
	}
	emitContext(out, "SessionStart", RenderSessionStart(in))
}

// hookUserPrompt 는 prompt 신호를 남기고 미확인 알림만 주입한다.
//
// 보드 전체를 매 프롬프트마다 넣지 않는다 — 컨텍스트 예산이 이 설계의 제약이고,
// 매번 넣으면 세션이 그것을 읽지 않게 되어 알림 자체가 무의미해진다.
func (a *App) hookUserPrompt(ctx context.Context, p HookPayload, out io.Writer) {
	sess := a.beatFromHook(ctx, p, model.SignalPrompt, nil)
	if sess == "" {
		return
	}
	v, _, err := a.Board(ctx, sess)
	if err != nil {
		a.log.Warn("프롬프트 훅에서 보드 조회 실패", "error", err.Error())
		return
	}
	var b strings.Builder
	for _, j := range v.Asks {
		fmt.Fprintf(&b, "[ask] %s\n", clip(firstLine(j.Title, j.Body), 200))
	}
	for _, j := range v.Blocked {
		fmt.Fprintf(&b, "[blocked] %s\n", clip(firstLine(j.Title, j.Body), 200))
	}
	if b.Len() == 0 {
		return
	}
	emitContext(out, "UserPromptSubmit", "flightdeck 미확인:\n"+strings.TrimRight(b.String(), "\n"))
}

// hookPostTool 은 tool 신호와 **미커밋 발자국**을 남긴다.
func (a *App) hookPostTool(ctx context.Context, p HookPayload) {
	a.beatFromHook(ctx, p, model.SignalTool, EditedPaths(p.ToolInput))
}

// hookPreCompact 는 압축 직전에 초안 판단을 남긴다.
//
// ★ 이 훅이 지키는 것은 **좌표**다: 압축 뒤 세션이 "나는 무엇을 쥐고 있었나 · 어느 경로를
// 만지고 있었나"를 잃는 것을 막는다. 대화 본문을 복원하지는 못한다 —
// transcript 형식은 이 설계가 기대는 플랫폼 사실 목록(§13)에 없으므로 파싱하지 않는다.
// 못 하는 것을 하는 척하지 않는다.
func (a *App) hookPreCompact(ctx context.Context, p HookPayload) {
	cc := a.ccSessionID(p.SessionID)
	if cc == "" {
		a.log.Warn("pre-compact: 세션 id 를 못 읽어 초안을 남기지 못했다")
		return
	}
	res, _, err := a.OpenSession(ctx, cc, "")
	if err != nil {
		a.log.Error("pre-compact: 세션 좌표를 못 얻었다", "error", err.Error())
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "압축 직전 자동 초안(trigger=%s).\n", clip(p.Trigger, 40))
	if len(res.Claims) > 0 {
		fmt.Fprintf(&b, "이 세션이 쥔 항목: %s\n", strings.Join(res.Claims, " "))
	} else {
		b.WriteString("이 세션이 쥔 항목: 없음\n")
	}
	fmt.Fprintf(&b, "워크트리 %s · 브랜치 %s\n", a.proj.Worktree, orDash(res.Branch))
	if s := strings.TrimSpace(p.CustomInstr); s != "" {
		fmt.Fprintf(&b, "압축 지시: %s\n", clip(s, 1000))
	}
	b.WriteString("압축 뒤 세션에게: 판단 본문은 이 초안에 없다 — note 로 남긴 것만 남는다.")

	a.cli.Session = res.Session.ID
	if _, werr := a.cli.Write(ctx, "note", "/api/v1/judgments", noteReq{
		Project: a.proj.ID, SessionID: res.Session.ID, Kind: string(model.JudgmentDraft),
		Title: "압축 직전 초안", Body: b.String(),
	}); werr != nil {
		a.log.Error("pre-compact 초안 저장 실패", "error", werr.Error())
	}
}

// hookStop 은 턴이 끝날 때 처방을 받아 낸다.
//
// ★ 턴 끝에 모으는 이유: 한 턴에 파일 20개를 고쳐도 처방은 1회로 묶인다.
// 그리고 에이전트가 다음 턴을 시작하기 전이라 사람을 안 기다린다.
//
// ★ fail-open 이다. 서버가 죽어도 조용히 반환한다 — 훅이 세션을 막으면 안 된다.
//
// ★★ **stop_hook_active 면 아무것도 안 낸다. 이 가드가 없으면 무한 루프다.**
// 2026-08-04 실측(Claude Code 2.1.221): Stop 훅의 additionalContext 는 붙기만 하는 것이
// 아니라 **모델을 다시 부른다**. 그 턴이 끝나면 Stop 이 또 불리고, 또 주입한다.
// 무가드 판을 실제로 돌려 루프를 냈고 사람이 인터럽트로 끊었다.
// 그리고 이 가드는 임시방편이 아니라 옳은 의미론이다 — 처방은 **사람이 몰던 턴의 끝**에
// 한 번 뜨는 것이지, 자기가 만든 턴의 끝에 다시 뜨는 것이 아니다.
//
// ★★★ **가드는 파싱 성패에 달려 있어야지, 억제에 기대면 안 된다.** runHook 은 페이로드
// 해석이 실패해도(session-start 가 페이로드 없이 배너를 내야 하므로) 경고만 남기고
// 제로값 HookPayload 로 계속 진행한다 — 그 제로값의 StopHookActive 는 항상 false 다.
// 플랫폼이 Stop 페이로드 모양을 바꾸는 날 이 훅의 모든 호출이 파싱에 실패하면
// 재진입 가드가 **매번** 꺼진다. 그때 남는 방벽은 처방의 (세션×키) 당 1회 억제뿐인데,
// 그것은 우연한 edge-triggering이라 재진입 턴이 새 경로를 편집하면(새 outside:<path> 키)
// 뚫린다. 그래서 파싱 자체가 실패했으면 이 가드가 참인지 거짓인지 알 수 없다는 뜻이고,
// 모르면 안전한 쪽(아무것도 안 낸다)으로 fail-close 한다 — 다른 훅과 달리 이 훅만
// **재진입이 세션을 못 쓰게 만들 수 있어** fail-open 원칙보다 이 가드가 앞선다.
func (a *App) hookStop(ctx context.Context, p HookPayload, perr error, out io.Writer) {
	if perr != nil {
		// 페이로드를 못 읽었으니 이 호출이 재진입인지 알 길이 없다 — 모르면 안 낸다.
		a.log.Warn("stop: 페이로드 해석 실패 — 재진입 여부를 몰라 처방을 안 낸다", "error", perr.Error())
		return
	}
	if p.StopHookActive {
		// 내가 만든 턴이다. 여기서 또 내면 그 턴이 또 턴을 만든다.
		return
	}
	if strings.TrimSpace(p.CWD) != "" {
		a.proj = resolveProject(a.env, p.CWD)
	}
	cc := a.ccSessionID(p.SessionID)
	if cc == "" {
		a.log.Warn("stop: 세션 id 를 못 읽어 처방을 못 냈다")
		return
	}
	res, _, err := a.OpenSession(ctx, cc, "")
	if err != nil {
		a.log.Warn("stop: 세션 좌표를 못 얻었다", "error", err.Error())
		return
	}
	a.cli.Session = res.Session.ID

	wr, err := a.cli.Write(ctx, "prescriptions",
		"/api/v1/sessions/"+urlPath(res.Session.ID)+"/prescriptions", struct{}{})
	if err != nil {
		a.log.Warn("stop: 처방 조회 실패", "error", err.Error())
		return
	}
	var got struct {
		Shown  []PrescriptionLine `json:"shown"`
		Folded int                `json:"folded"`
	}
	if err := json.Unmarshal(wr.Body, &got); err != nil {
		a.log.Warn("stop: 처방 응답 해석 실패", "error", err.Error())
		return
	}
	if text := RenderPrescriptions(got.Shown, got.Folded); text != "" {
		emitContext(out, "Stop", text)
	}
}

// beatFromHook 은 신호 하나를 남기고 세션 id 를 돌려준다. 실패하면 빈 문자열이다.
func (a *App) beatFromHook(ctx context.Context, p HookPayload, kind model.SignalKind, paths []string) string {
	if strings.TrimSpace(p.CWD) != "" {
		a.proj = resolveProject(a.env, p.CWD)
	}
	cc := a.ccSessionID(p.SessionID)
	if cc == "" {
		a.log.Warn("훅에 세션 id 가 없다", "mode", string(kind))
		return ""
	}
	res, _, err := a.OpenSession(ctx, cc, "")
	if err != nil {
		a.log.Warn("훅에서 세션 좌표를 못 얻었다", "mode", string(kind), "error", err.Error())
		return ""
	}
	a.cli.Session = res.Session.ID
	if _, werr := a.cli.Write(ctx, "beat",
		"/api/v1/sessions/"+urlPath(res.Session.ID)+"/signals",
		beatReq{Kind: string(kind), Paths: paths}); werr != nil {
		a.log.Warn("신호 기록 실패", "mode", string(kind), "error", werr.Error())
	}
	return res.Session.ID
}

var _ = service.APIVersion // 훅도 같은 계약 버전을 쓴다
var _ = time.Second
