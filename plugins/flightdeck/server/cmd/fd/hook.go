package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/mcpsrv"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/window"
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
	// Reason 은 SessionEnd 가 **끝나는 이유**로 주는 값이다. 시작하는 것의 이름이 아니다.
	// ★ clear 와 resume 말고는 아무도 안 쏜다 — 설치본 2.1.221·2.1.222 를 뜯어 확인했다
	// (executeSessionEndHooks 호출부가 그 둘뿐이고, logout·prompt_input_exit·other·
	// bypass_permissions_disabled 는 zod 열거값에만 있다). 그래서 이 훅으로는 프로세스
	// 종료를 못 잡는다. 그 한계는 DESIGN.md 가 말한다.
	Reason string `json:"reason"`
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

// emitBlock 은 Stop 훅의 decision:block 이다 — 턴을 끝내려는 모델을 reason 과 함께 되살린다.
//
// ★ 무한루프 방벽은 이 함수가 아니라 **호출자의 stop_hook_active 가드**다. block 이 만든
// 턴이 끝나면 Stop 이 stop_hook_active=true 로 다시 오고, hookStop 은 그 자리에서 반환한다 —
// 그래서 block 은 사람이 몰던 턴의 끝마다 최대 한 번이다. 2026-08-04 의 additionalContext
// 무한루프(hook.go 아래 ★★ 실측)와 같은 사슬이고 같은 가드가 끊는다.
// (세션×키) 억제표를 방벽으로 쓰지 않는 이유는 ★★★ 문단이 이미 적었다 — 그것은
// 우연한 edge-triggering 이라 방벽이 못 되고, 여기서는 지속(매 프롬프트 한 번)이 목적이라
// 애초에 억제가 반대 방향이다.
//
// ★ 이 계약(decision/reason 최상위 필드)은 **2026-08-12 에 실물로 실측됐다** — 0.19.0
// 배포 직후, 항목을 선점한 세션이 finish 없이 턴을 끝내자 라이프사이클 관문(stage=finish)의
// block 이 발화했고, 하네스가 "Stop hook feedback:" 으로 reason 전문을 주입하며 **사람 개입
// 없이 턴을 되살렸다**(항목 fd-decision-block-first-live-observation — 그 항목 자체가 실험
// 장치였다). 되살아난 턴이 finish 로 정상 종료됐으므로 stop_hook_active 사슬 절단도 같은
// 실측에 들어 있다. 원문("공개 문서 기준·실측 아직 없음")은 이 문단이 대체한다.
func emitBlock(out io.Writer, reason string) {
	buf, err := json.Marshal(struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}{Decision: "block", Reason: reason})
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
	case "session-end":
		a.hookSessionEnd(ctx, p)
	default:
		a.log.Error("모르는 훅 이름", "mode", clip(event, 40),
			"error", "session-start|user-prompt|post-tool|pre-compact|stop|session-end 중 하나여야 한다")
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
		// ★ 로그로만 흘리면 아무도 안 본다. 훅 로그는 사람이 찾아가야 나오고,
		// 셀 수 없는 큐는 재생도 적재도 막힌 상태라 가장 급한 축이다.
		in.Unreadable = append(in.Unreadable, a.cli.Outbox.Dir()+": "+err.Error())
	}
	// ★ 옛 채널 자리의 대기도 더한다. 업그레이드 직후에는 고정 큐가 비어 있고
	// 판단은 전부 옛 자리에 남아 있는 것이 흔한 상태라, 고정 큐만 보면 배너가
	// 조용해진다 — 정작 사람이 물어보지 않고도 보는 유일한 표면이 바로 이 배너다.
	// "이 머신에 쌓여 있다"는 문장은 그대로 참이다: 옛 자리도 이 머신 안이다.
	for _, lo := range a.cli.LegacyLeftovers() {
		in.Pending += lo.Pending
		// ★ lo.Pending 은 셀 수 없었으면 **0 이다** — 사유는 lo.Err 에만 담긴다.
		// 그러니 Err 을 따로 안 옮기면 손상된 큐가 배너에서 정확히 0건으로 보인다.
		if lo.Err != "" {
			in.Unreadable = append(in.Unreadable, lo.Dir+": "+lo.Err)
		}
	}

	cc := a.ccSessionID(p.SessionID)
	if cc == "" {
		in.Notice = strings.TrimSpace(in.Notice + " CLAUDE_CODE_SESSION_ID 도 훅 페이로드의 session_id 도 못 읽었다 — 이 세션은 등록되지 않는다(fd doctor 가 그 축을 잰다).")
		emitContext(out, "SessionStart", RenderSessionStart(in))
		return
	}

	// ★ 표류 수리. **순서가 전부다 — rekey 가 아래 OpenSession 보다 앞이어야 한다.**
	// 뒤에 두면 그 upsert 가 새 cc 로 카드 B 를 먼저 만들고, 그러면 rekey 가 3중키
	// UNIQUE 에 걸려 두 카드를 진짜로 병합하는 경로가 또 필요해진다 — 이 설계에 그 경로는
	// 일부러 없다. 앞에 두면 같은 upsert 가 이미 고쳐진 카드 A 로 그냥 떨어진다.
	// 뒤바꿔도 컴파일도 되고 대부분의 시험도 초록이라, 그 축은 시험이 따로 붙들고 있다
	// (TestClearKeepsOneCardAndItsClaim).
	beaconKey, beacon, haveBeacon := a.findWindow()

	// ★ **워크트리를 한 번 더 대조한다. 지우지 마라 — Find 는 이 축을 안 본다.**
	// window.Find 가 맞추는 것은 (머신·조상 pid·시작 시각) 셋뿐이다. 그런데 바로 아래
	// rekey 가 고치는 카드의 키는 **3중키(머신·워크트리·cc)** 다. 그래서 한 창 안에서
	// MCP 의 워크트리 관측(자기 cwd)과 훅의 것(페이로드 cwd → resolveProject)이 갈리면,
	// 여기서 남의 워크트리 카드의 cc 를 갈아엎고 아래 upsert 는 내 워크트리로 새 카드를
	// 또 만든다 — 그 카드의 선점이 아무 잘못도 없는 워크트리 쪽에 **고아로** 남는다.
	// 이 기능이 없애려는 바로 그 결과를 이 기능이 만들어 내는 것이다(설계 §4
	// "세션 카드는 3중키로 서고, 남의 창은 수리하지 않는다").
	// ★ 이 인용은 오래 §2 의 "둘째 층" 을 가리켰고 **§2 에는 층 구조도 이 규칙도 없었다** —
	//   코드가 문서를 근거로 대는데 문서가 그 말을 모르는 상태였다. 규칙을 §4 에 적고
	//   여기를 그리로 돌렸다.
	//
	// ★ 거절은 **오류가 아니다** — 비콘을 못 찾은 것과 같은 급이라 그냥 아래 OpenSession 으로
	// 떨어진다(=오늘 거동). 다만 Debug 로 묻지 않는다: 두 채널이 같은 좌표를 다르게 풀었다는
	// 것은 **다른 데의 진짜 결함**이고, 이 레포는 그 축이 갈려 한 세션이 카드 3장으로 뜬 일을
	// 이미 겪었으며 그것이 오래 안 보였다(internal/window/dir.go 머리말).
	if haveBeacon && !sameWorktree(beacon.Worktree, a.proj.Worktree) {
		a.log.Warn("비콘의 워크트리가 이 훅의 것과 다르다 — 남의 창으로 보고 수리하지 않는다",
			"beacon", clip(beacon.Worktree, 200), "hook", clip(a.proj.Worktree, 200))
		in.Notice = strings.TrimSpace(in.Notice +
			" 이 창의 비콘이 다른 워크트리(" + clip(beacon.Worktree, 120) + ")를 가리켜 카드 합치기를 건너뛴다 — " +
			"훅과 MCP 가 좌표를 다르게 풀었다는 뜻이다(fd doctor 가 그 축을 잰다).")
		// 비콘 자체를 안 믿는다. 아래 SaveIdentity 까지 막는다 —
		// 남의 트리 비콘에 내 cc·카드를 적으면 그것도 같은 오염이다.
		haveBeacon = false
	}

	// ★ 비콘의 session_id 가 비었으면 **옛 cc 로 카드를 되찾는다.**
	//
	// 그 자리를 적는 것은 훅뿐이고(아래 SaveIdentity), 훅은 비콘을 찾은 뒤에만 적는다.
	// 그래서 심기가 첫 SessionStart 보다 늦으면 그 자리는 빈 채로 남는다 — 그리고 늦는 것은
	// 가정이 아니다(설계 개정 ②: `fd mcp` 가 부모 claude 보다 ≈6.6시간 늦게 뜬 실측이 있다).
	// 그 상태의 /clear 는 rekey 대상을 몰라 건너뛰고 카드가 두 장이 되는데, 그때 고아가 되는
	// 것이 하필 **첫 구간의 선점과 판단을 든 카드**다.
	//
	// FindSession 은 **찾기만 하는** 조회라 옛 cc 의 카드 A 가 있으면 그것을 그대로 돌려주고,
	// 없으면 오류를 낸다(만들지 않는다). 아래 rekey 가 그 카드를 새 cc 로 옮기고,
	// 이어지는 OpenSession(cc) 이 같은 카드로 떨어진다.
	//
	//	옛 cc 의 카드가 있다  + rekey 성공 → 카드 1장. 이 갈래가 노린 것이다
	//	옛 cc 의 카드가 있다  + rekey 거절 → 카드 2장. 다만 둘 다 원래 있던 것이다
	//	옛 cc 의 카드가 없다  + rekey 성공 → 해당 없음(찾은 것이 없으면 rekey 를 안 탄다)
	//	옛 cc 의 카드가 없다  + rekey 거절 → 카드 1장. **조회가 아무것도 안 만든다**
	//
	// ★ 넷째 갈래가 2장에서 1장이 된 것이 fd-session-lookup-without-upsert 의 성과다.
	//   옛 코드는 여기서 OpenSession(3중키 upsert)을 조회로 썼고, 그것이 행이 없을 때
	//   **만들었다.** 지금은 GET /api/v1/sessions 라 만들 수 없다.
	if haveBeacon && beacon.SessionID == "" && beacon.CCSessionID != "" && beacon.CCSessionID != cc {
		old, oerr := a.FindSession(ctx, beacon.CCSessionID)
		switch {
		case oerr != nil:
			// 못 찾아도 오류가 아니다 — 비콘을 못 찾은 것과 같은 급이라 폴백한다.
			// ★ 그리고 이제 **아무것도 안 만든다.** 이것이 옛 코드와의 차이 전부다.
			a.log.Warn("비콘의 옛 cc 로 카드를 못 찾았다 — 이번 전환은 합치지 못한다",
				"error", oerr.Error(), "cc", clip(beacon.CCSessionID, 40))
		case old.ID != "":
			beacon.SessionID = old.ID
		}
	}

	if haveBeacon && beacon.SessionID != "" && beacon.CCSessionID != cc {
		if _, rerr := a.Rekey(ctx, beacon.SessionID, cc); rerr != nil {
			// ★ 삼키고 알린다. 409(이미 남이 쓰는 cc)는 미도달이 아니라 *APIError 로 오므로
			// 기존 열화 경로가 이걸 안 잡아 준다 — 아래 OpenSession 실패를 다루는 꼴과 같이 간다.
			// 여기서 오류를 위로 올리면 훅이 세션을 막는다(이 파일 머리말).
			a.log.Warn("세션 rekey 실패", "error", rerr.Error(),
				"session", clip(beacon.SessionID, 40), "cc", clip(cc, 40))
			// ★ 화면에는 **서버가 닿을 때만** 싣는다(일곱 줄 아래 OpenSession 실패와 같은 규율).
			// 서버가 죽었으면 rekey 는 정의상 매번 실패하고, 그 사실은 배너가 이미 말하고 있다 —
			// 여기서 또 얹으면 /clear 마다 같은 말이 배너 위에 한 줄씩 쌓인다.
			if reachable {
				in.Notice = strings.TrimSpace(in.Notice +
					" cc 가 갈렸는데 카드를 못 합쳤다: " + clip(rerr.Error(), 200))
			}
		} else {
			a.moveSessionCache(beacon.CCSessionID, cc)
		}
	}

	// ★ I-1(최종 리뷰). OpenSession(아래) 은 요청한 프로젝트가 미등록이면 서버가 실제로
	// 연 프로젝트를 a.proj.ID 에 이미 채택해 둔다(app.go 의 adoptResolvedProject — 그래야
	// 이 프로세스의 후속 쓰기가 정상 프로젝트로 간다). 그 채택이 일어났는지는 여기서
	// **호출 전 값을 남겨 뒀다가** 비교해야만 안다 — a.proj.ID 는 그 함수가 직접 고친다.
	requestedProject := a.proj.ID
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
		// ★ 채택이 실제로 일어났으면 화면에도 옮긴다. 지금까지 이 사실은 서버 로그
		// (session.project.mismatch 이벤트)로만 나갔고, 그 자리는 에이전트가 실제로
		// 읽는 자리가 아니다 — 그래서 프로젝트가 왜 요청과 다른지 아무도 몰랐다.
		if a.proj.ID != requestedProject {
			in.Notice = strings.TrimSpace(in.Notice + fmt.Sprintf(
				" 요청한 프로젝트 %q 는 등록돼 있지 않다 — 이 세션은 %q 다.", requestedProject, a.proj.ID))
			// 머리줄도 실제 프로젝트를 말해야 한다 — 안 그러면 헤더와 notice 가 서로 반박한다.
			in.Project = a.proj.ID
		}
	}

	// 비콘에 이번 cc 와 그 카드를 적어 둔다. 다음 전환에서 이 값이 rekey 의 대상이 된다.
	if haveBeacon && in.SessionID != "" {
		if _, werr := window.SaveIdentity(a.beaconDir, beaconKey, cc, in.SessionID, a.now()); werr != nil {
			a.log.Warn("창 비콘 갱신 실패", "error", werr.Error())
		}
	}
	// ★ **이 한 줄을 지우지 마라 — 그리고 지우면 빨간불이 난다.** 아래 형제와 실패 모양이
	// 같아서(반환값이 없고 사유는 Debug 로만 남는다 — pruneWindows 가 그렇게 정한 바다)
	// 호출이 사라져도 화면·로그·종료코드 어디에도 신호가 없다. 한동안 실제로 이 한 줄을
	// 지워도 cmd/fd 가 통째로 초록이었다. 그래서 이 이음매를 파일시스템 좌표계로 따로
	// 잠갔다 — hook_beacon_test.go 의 TestOnlySessionStartHookPrunesWindowBeacons 가
	// 죽은 창의 비콘을 심고 훅을 실제로 돌린다.
	//
	// 그 표는 **여섯 훅 이벤트를 다 돌려** session-start 에서만 지워지는 것까지 본다.
	// 호출 여부만 재면 runHook 머리에 한 줄 넣는 판도 초록인데, 그 판이 왜 안 되는지는
	// pruneWindows 머리말이 말한다 — 여기서 다시 적지 않는다.
	a.pruneWindows()
	// ★ 바이너리 캐시 GC 도 **같은 자리**다. 위 함수가 "훅에서만 한다 — SessionStart
	// 타임아웃이 10초라 디렉토리 하나를 훑을 여유가 있고 MCP 는 도구 응답 지연에
	// 민감하다"고 적어 둔 그 판정이 여기에 그대로 선다. 잴 것도 같은 모양이다 —
	// 디렉토리 하나의 목록 + 파일 몇 개의 stat 이고, 내용은 안 읽으니 비콘 쪽보다 싸다.
	// 이 GC 가 없으면 자리가 소스 트리마다 갈리므로(릴리스마다 키가 바뀐다) 22MB×N 이
	// 무한히 쌓인다 — 상한을 가진 자리가 여기 하나뿐이다.
	//
	// pruneWindows 와 **실패 모양도 같게** 둔다: 반환값이 없고 사유는 Debug 로만 남는다.
	// 캐시가 안 잘린 것에 대해 사용자가 지금 할 수 있는 일이 없고, 세션 시작을 막을
	// 이유는 더더욱 없다(이 훅의 존재 이유는 조정이지 청소가 아니다).
	//
	// ★ **이 한 줄을 지우지 마라 — 그리고 지우면 빨간불이 난다.** 실패가 Debug 로만 남는
	// 위 설계 때문에 이 호출이 사라져도 화면·로그·종료코드 어디에도 신호가 없다(한동안
	// 실제로 전 시험이 초록이었다). 그래서 이 이음매만 파일시스템 좌표계로 따로 잠갔다 —
	// bincache_test.go 의 TestSessionStartHookPrunesBinCache 가 훅을 실제로 돌리고 심어 둔
	// 옛 항목이 사라졌는지를 본다. 형제인 위 pruneWindows 도 이제 같은 대우를 받는다
	// (바로 위 주석이 그 자리를 가리킨다).
	a.pruneBinCache()

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

// findWindow 는 내 조상 사슬 위의 비콘을 찾는다.
//
// 못 찾아도 **오류가 아니다** — 설계 §5 의 폴백이고, 그 폴백이 오늘 거동이다.
// 그래서 사유는 로그로만 남긴다: 이 자리에서 화면에 문구를 얹으면 비콘을 안 쓰는
// 대다수 실행(Cursor·비리눅스·MCP 미기동)이 매번 배너를 하나씩 더 달게 된다.
func (a *App) findWindow() (window.Key, window.Beacon, bool) {
	if a.beaconDir == "" {
		return window.Key{}, window.Beacon{}, false
	}
	anc := window.Ancestors(os.Getpid(), window.PPidOf, 24)
	m, ok, why := window.Find(a.beaconDir, a.machine, anc, window.StartedOf)
	if !ok {
		a.log.Debug("창 비콘을 못 찾았다", "why", why)
		return window.Key{}, window.Beacon{}, false
	}
	return m.Key, m.Beacon, true
}

// sameWorktree 는 두 워크트리 경로가 같은 트리를 가리키는지다. 순수 함수다.
//
// 양쪽 다 이미 정리된 절대경로로 저장된다(훅은 resolveProject 의 filepath.Clean,
// MCP 는 ResolveIdentity 의 filepath.Clean + canAttribute 의 IsAbs). 그래도 여기서 한 번 더
// 정리하는 이유는 이 함수가 **거절 판정**이기 때문이다 — 한쪽에 슬래시 하나가 더 붙었다는
// 이유로 남의 창이라고 읽으면 표류 수리가 조용히 꺼진다.
//
// ★ 빈 값은 **같지 않다**로 본다. filepath.Clean("") 은 "." 이라 그냥 Clean 해서 비교하면
// 양쪽이 다 비었을 때 "." == "." 로 통과한다. 좌표를 못 읽은 둘을 같다고 읽는 순간
// 이 가드가 하는 일이 없어진다 — 못 읽은 것은 맞은 것이 아니다.
func sameWorktree(x, y string) bool {
	x, y = strings.TrimSpace(x), strings.TrimSpace(y)
	if x == "" || y == "" {
		return false
	}
	return filepath.Clean(x) == filepath.Clean(y)
}

// pruneWindows 는 죽은 창의 비콘을 치운다.
//
// ★ **훅에서만 한다.** SessionStart 타임아웃이 10초(plugins/flightdeck/hooks/hooks.json)라
// 디렉토리 하나를 훑을 여유가 있는 쪽이고, MCP 는 도구 응답 지연에 민감하다 —
// 그 지연은 매 도구 호출마다 사람이 기다리는 시간이다.
//
// ★ 그리고 그 훅 중에서도 **session-start 하나다.** 같은 hooks.json 에서 async 가 아닌 것,
// 곧 사람이 그 시간을 통째로 기다리는 것은 셋이다 — user-prompt 2초 · stop 3초 ·
// session-start 10초. 앞의 둘은 예산이 가장 작으면서 매 프롬프트·매 턴 끝마다 돈다.
// 나머지 셋(post-tool · pre-compact · session-end)은 async:true 라 예산 논거가 다르지만
// post-tool 은 편집마다 돈다 — 횟수 쪽에서 걸린다. 그래서 "상한 없이 자라는 디렉토리를
// 훑는다"를 감당하는 자리가 여섯 중 여기뿐이다. 여기가 먼저 눈에 띄어서가 아니다.
func (a *App) pruneWindows() {
	if a.beaconDir == "" {
		return
	}
	// 머신 축을 함께 넘긴다 — 이 프로세스는 **남의 머신 pid** 가 살았는지 알 수 없다
	// (공유 홈, window.Prune 주석).
	if _, err := window.Prune(a.beaconDir, a.machine, window.Alive); err != nil {
		a.log.Debug("비콘 가지치기 실패", "error", err.Error())
	}
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
//
// ★ 보내기 전에 git 이 무시하는 경로를 뺀다. 안 빼면 스크래치가 발자국이 되고, 그러면
// 표류 처방이 그것을 근거로 헛발화하고 두 세션이 각자 워크트리에서 같은 이름의 스크래치를
// 쓸 때 **물리적으로 충돌할 수 없는 것**에 겹침이 뜬다(DropIgnoredPaths 주석 참조).
// 여기서 거르는 이유는 좌표계다 — 무시 여부는 그 경로가 든 트리만 답할 수 있고,
// 그 트리를 아는 것은 서버가 아니라 이 프로세스다.
func (a *App) hookPostTool(ctx context.Context, p HookPayload) {
	a.beatFromHook(ctx, p, model.SignalTool, DropIgnoredPaths(a.log, EditedPaths(p.ToolInput)))
}

// hookPreCompact 는 압축 직전에 초안 판단을 남긴다.
//
// ★ 이 훅이 지키는 것은 **좌표**다: 압축 뒤 세션이 "나는 무엇을 쥐고 있었나 · 어느 경로를
// 만지고 있었나"를 잃는 것을 막는다. 대화 본문을 복원하지는 못한다 —
// transcript 형식은 이 설계가 기대는 플랫폼 사실 목록(§13)에 없으므로 파싱하지 않는다.
// 못 하는 것을 하는 척하지 않는다.
func (a *App) hookPreCompact(ctx context.Context, p HookPayload) {
	// ★ 다른 다섯 훅과 같은 자리다 — 이것만 빠져 있었다.
	//
	// 규율이 `git worktree add` 를 지시하므로 대화는 도중에 트리를 옮긴다. 그때 훅
	// 프로세스의 cwd 와 페이로드의 cwd 가 갈리고, 좌표를 다시 안 풀면 이 초안이
	// **엉뚱한 카드**로 간다. 압축 직전 초안은 그 대화가 컨텍스트를 잃기 직전에 남기는
	// 마지막 기록이라 가장 나쁜 자리에서 어긋난다 — 복귀한 세션이 자기 카드에서 못 찾는다.
	if strings.TrimSpace(p.CWD) != "" {
		a.proj = resolveProject(a.env, p.CWD)
	}
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
	// ★ I-1: 이 함수는 아래에서 noteReq{Project: a.proj.ID, …} 로 **프로젝트 좌표를 실어
	// 쓴다** — hookSessionStart 와 달리 이 훅이 실제로 그 값을 소비하는 자리다. 별도
	// 처리가 필요 없는 이유는 여기가 아니라 위 OpenSession 호출 자체에 있다:
	// a.OpenSession → a.openSession 이 성공하면 서버가 실제로 연 프로젝트로 a.proj.ID 를
	// 이미 채택한 뒤다(app.go 의 adoptResolvedProject) — 그래서 몇 줄 아래의 a.proj.ID 는
	// 항상 서버가 방금 이 세션에 실제로 매긴 값이고, 이 함수가 화면에 알림을 못 내는 것
	// (이 훅은 additionalContext 에 notice 채널이 없다)도 문제가 안 된다.
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

	// ★ I-1: 이 훅은 a.proj.ID 를 **안 싣는다** — 본문은 빈 구조체이고 경로는
	// 세션 id(res.Session.ID)뿐이라 프로젝트 좌표가 요청에 아예 없다. 그래서 위
	// OpenSession 이 프로젝트를 다른 것으로 채택했더라도(app.go 의 adoptResolvedProject)
	// 이 쓰기는 애초에 그 값을 안 보므로 영향이 없다 — 별도 처리가 필요 없다.
	//
	// ★ 리터럴 "prescriptions" 대신 CmdPrescriptions 를 쓴다(CmdProjectRemove 와 같은
	// 이유) — offline.go·outbox.go 가 이 이름으로 명시 갈래를 잡아 뒀다.
	wr, err := a.cli.Write(ctx, CmdPrescriptions,
		"/api/v1/sessions/"+urlPath(res.Session.ID)+"/prescriptions", struct{}{})
	if err != nil {
		a.log.Warn("stop: 처방 조회 실패", "error", err.Error())
		return
	}
	var got struct {
		Shown     []PrescriptionLine `json:"shown"`
		Folded    int                `json:"folded"`
		Lifecycle *struct {
			Stage  string `json:"stage"`
			Reason string `json:"reason"`
		} `json:"lifecycle"`
	}
	if err := json.Unmarshal(wr.Body, &got); err != nil {
		a.log.Warn("stop: 처방 응답 해석 실패", "error", err.Error())
		return
	}

	text := RenderPrescriptions(got.Shown, got.Folded)
	if got.Lifecycle != nil && strings.TrimSpace(got.Lifecycle.Reason) != "" {
		reason := got.Lifecycle.Reason
		if text != "" {
			reason += "\n\n" + text // 처방을 잃지 않는다 — block 턴에서 additionalContext 는 안 나간다
		}
		emitBlock(out, reason)
		return
	}
	if text != "" {
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
	// ★ I-1: beatReq 에도 프로젝트 필드가 없다(wire.go) — 신호는 세션 id 경로 하나로
	// 귀속된다. hookStop 과 같은 이유로 이 쓰기는 a.proj.ID 를 안 읽으므로 프로젝트
	// 채택 여부와 무관하다.
	if _, werr := a.cli.Write(ctx, "beat",
		"/api/v1/sessions/"+urlPath(res.Session.ID)+"/signals",
		beatReq{Kind: string(kind), Paths: paths}); werr != nil {
		a.log.Warn("신호 기록 실패", "mode", string(kind), "error", werr.Error())
	}
	return res.Session.ID
}

// hookSessionEnd 는 /clear 로 떠나는 대화의 카드를 닫는다.
//
// ★ reason 은 **끝나는 이유**이지 시작하는 것의 이름이 아니다. 설치본 2.1.221·2.1.222 실측:
// executeSessionEndHooks 를 부르는 자리는 `o3t("clear", …)`(clearConversation)와
// `o3t("resume", …)`(돌고 있는 REPL 의 대화 갈아타기) 둘뿐이고, 프로세스 종료를 알리는 훅
// 이벤트는 31종 어디에도 없다. **그래서 이 훅으로 창을 닫고 나간 세션은 못 잡는다.**
//
// ★ **clear 만 본다.** hooks.json 의 matcher 가 이미 거르지만 여기서 한 번 더 본다 —
// matcher 가 바뀌거나 플랫폼이 다른 사유를 쏘기 시작한 날, 이 갈래가 없으면 살아 있는
// 세션이 조용히 닫힌다. resume 은 **/fork 도 같은 사유로 오므로** 일부러 뺐다.
//
// ★ 이것이 안전한 것은 store 의 "열면 살아난다"(Tx.OpenSession)가 있기 때문이다.
// clear 직후 SessionStart 가 같은 카드를 rekey 로 이어받고 그 OpenSession 이 되살린다.
// 그 안전핀 없이 여기만 넣으면 살아서 일하는 세션이 보드에서 사라진다.
func (a *App) hookSessionEnd(ctx context.Context, p HookPayload) {
	if p.Reason != "clear" {
		a.log.Debug("session-end: clear 가 아니라 아무것도 안 한다", "reason", clip(p.Reason, 40))
		return
	}
	if strings.TrimSpace(p.CWD) != "" {
		a.proj = resolveProject(a.env, p.CWD)
	}
	cc := a.ccSessionID(p.SessionID)
	if cc == "" {
		a.log.Warn("session-end: 세션 id 를 못 읽어 카드를 못 닫았다")
		return
	}
	res, _, err := a.OpenSession(ctx, cc, "")
	if err != nil {
		a.log.Warn("session-end: 세션 좌표를 못 얻었다", "error", err.Error())
		return
	}
	// ★ I-1: 아래 CloseSession 은 PATCH 본문에 상태·사유만 싣고 경로가 세션 id 다
	// (patchStateReq, wire.go) — 프로젝트 좌표가 요청에 없다. hookStop·beatFromHook 과
	// 같은 이유로 이 쓰기는 a.proj.ID 를 안 읽는다.
	//
	// ★ 선점을 든 카드는 안 닫는다. rekey 가 거절되면 그 선점이 통째로 안 보이게 되고,
	// 그러면 항목을 아무도 못 집는데 누가 잡았는지도 안 보인다.
	if len(res.Claims) > 0 {
		a.log.Info("session-end: 선점이 남아 있어 카드를 안 닫는다",
			"session_id", clip(res.Session.ID, 64), "claims", len(res.Claims))
		return
	}
	if _, cerr := a.CloseSession(ctx, res.Session.ID, "/clear"); cerr != nil {
		a.log.Warn("session-end: 카드를 못 닫았다",
			"session_id", clip(res.Session.ID, 64), "error", cerr.Error())
	}
}

var _ = service.APIVersion // 훅도 같은 계약 버전을 쓴다
var _ = time.Second
