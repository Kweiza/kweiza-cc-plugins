package mcpsrv

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 표시 — 응답은 **사람이 읽는 텍스트**다.
//
// JSON 을 그대로 뱉지 않는다. 이 자리의 소비자는 에이전트이고, 에이전트는 읽고 판단한다.
// 다만 기계가 세는 값(탈락 사유 코드 등)은 문자열 안에 **그대로 보이게** 둔다 —
// 사람 말로 풀어 쓰면 §10 의 사유 분포를 응답에서 셀 수 없게 된다.
//
// 이 파일의 함수는 전부 순수 함수다. 표시 판정이 핸들러 본문에 흩어지면
// 시험이 그 로직의 사본을 단정하게 되고, 그러면 변이가 조용히 새어 나간다.

// BoardTokenBudget 은 board 기본 출력의 상한이다(설계 §6: "기본 1,200토큰").
//
// 기존 도구가 첫 명령에서 신호 6%짜리 출력을 내던 것이 이 제품이 고치려는 결함이다.
// detail=true 일 때만 이 상한을 푼다.
const BoardTokenBudget = 1200

// EstimateTokens 는 문자열의 토큰 수 **상한**을 어림한다. 순수 함수다.
//
// ★ 호스트의 토크나이저가 아니다. 그것을 정확히 재려면 의존을 하나 더 넣어야 하고,
// 여기서 필요한 것은 정확한 값이 아니라 **자라지 않는다는 보장**이다.
// 그래서 넉넉하게 잡는다: ASCII 는 0.3토큰/자, 그 밖(한글·기호)은 1.5토큰/자.
// 실제보다 크게 나오므로 이 어림으로 상한을 지키면 실제 값도 지켜진다.
func EstimateTokens(s string) int {
	tenths := 0
	for _, r := range s {
		if r < 128 {
			tenths += 3
		} else {
			tenths += 15
		}
	}
	return (tenths + 9) / 10
}

// FormatAge 는 경과 시간을 사람이 읽는 짧은 말로 만든다. 순수 함수다.
//
// 음수는 "0초"다 — 시계가 어긋난 것이고, 그것을 "-3초 전"으로 내면
// 읽는 쪽이 데이터 손상으로 오해한다. 어긋남 자체는 파생 신선도가 나른다.
func FormatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d초", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d분", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d시간 %d분", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%d일 %d시간", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// ShortID 는 ULID 를 화면용으로 줄인다. 순수 함수다.
// 절대 이 값으로 다시 조회하지 않는다 — 표시 전용이다.
func ShortID(id string) string {
	rs := []rune(id)
	if len(rs) <= 8 {
		return id
	}
	return string(rs[:8]) + "…"
}

// FormatFreshness 는 파생 신선도 한 줄이다. 순수 함수다.
//
// 설계 §6: 모든 패널에 "(파생: git@14:31, 12초 전)" 이 붙는다.
// 서버가 죽었을 때 마지막 상태가 현재 사실인 척하는 것을 구조로 막는 축이다.
func FormatFreshness(d service.Derived) string {
	f := d.Freshness
	state := "최신"
	if f.Stale {
		state = "낡음"
	}
	s := fmt.Sprintf("파생 %s@%s %s", f.Source, f.ObservedAt.UTC().Format("15:04:05"), state)
	if n := len(d.Failures); n > 0 {
		s += fmt.Sprintf(" · 못 읽은 축 %d개", n)
	}
	return s
}

// renderFailures 는 파생 실패를 축 이름과 원인 전문으로 낸다.
// 침묵하면 빈 필드가 "값이 0이다"로 읽힌다.
func renderFailures(d service.Derived, limit int) []string {
	if len(d.Failures) == 0 {
		return nil
	}
	out := []string{fmt.Sprintf("못 읽은 파생 %d축:", len(d.Failures))}
	for i, f := range d.Failures {
		if limit > 0 && i >= limit {
			out = append(out, fmt.Sprintf("  … %d축 더", len(d.Failures)-limit))
			break
		}
		out = append(out, fmt.Sprintf("  · %s — %s", f.Axis, clip(f.Detail, 200)))
	}
	return out
}

// signalOrder 는 신호를 찍는 순서다. 고정 — 같은 입력에 같은 줄이어야 눈으로 비교된다.
var signalOrder = []model.SignalKind{
	model.SignalPrompt, model.SignalTool, model.SignalMCP, model.SignalCommit, model.SignalPush,
}

// FormatSignals 는 신호 넷(다섯)의 나이를 나란히 낸다. 순수 함수다.
//
// ★ **합치지 않는다.** 하나로 접으면 "에이전트가 긴 도구를 돌리는 중"과
// "사람이 읽기만 하는 중" 둘 중 하나를 반드시 오판한다(설계 §4).
// 그리고 "죽었다"를 만들지 않는다 — 나이를 숫자로만 낸다.
func FormatSignals(sig map[model.SignalKind]time.Time, now time.Time) string {
	var parts []string
	for _, k := range signalOrder {
		at, ok := sig[k]
		if !ok {
			continue // 키 부재는 "그 종류가 한 번도 안 왔다"다. 0값으로 채우지 않는다
		}
		parts = append(parts, fmt.Sprintf("%s %s", k, FormatAge(now.Sub(at))))
	}
	if len(parts) == 0 {
		return "신호 없음"
	}
	return strings.Join(parts, " · ")
}

// activityKinds 는 "이 세션이 일하고 있나"에 답하는 신호다.
//
// ★ **mcp 와 push 는 일부러 뺐다.** mcp 는 도구 호출이면 무엇이든 찍는다 —
// 이 파일과 짝인 callTool 이 이름을 안 가리고 dispatch 전에 찍으므로 읽기 전용
// board 하나로도 점등되고, service.Note 의 문은 REST 로 열려 있어 PreCompact 훅의
// 자동 초안까지 들어온다. 포함하면 아무 일도 안 한 세션이 점등돼 판별력이 0이 된다.
// push 는 랜딩하고 떠난 세션이 계속 일하는 것처럼 보인다.
// (옛 근거였던 "세션 열기·상태 전이가 찍는다"는 그 두 자리를 지워 사라졌다 —
// web/format.go 의 같은 주석과 함께 읽어라.)
var activityKinds = []model.SignalKind{model.SignalPrompt, model.SignalTool, model.SignalCommit}

// activityOf 는 "이 세션이 일하고 있나"와 그 사유를 낸다. 순수 함수다.
//
// 화면 ①이 선점을 필터로 쓰는 이상 이 배지가 **"쥐고만 있고 안 하는 세션"**을 가리키는
// 유일한 축이다. 불리언만 내면 사람이 무엇을 근거로 회수할지 못 정하므로 사유를 함께 낸다.
// 이 판정은 죽음을 말하지 않는다 — 나이를 숫자로 낼 뿐이고 회수는 사람이 한다(설계 §4).
func activityOf(sig map[model.SignalKind]time.Time, now time.Time) (bool, string) {
	var newest time.Time
	for _, k := range activityKinds {
		if at, ok := sig[k]; ok && at.After(newest) {
			newest = at
		}
	}
	if newest.IsZero() {
		return false, "○ 활동 없음"
	}
	return true, "● 활동 " + FormatAge(now.Sub(newest)) + " 전"
}

// formatPaths 는 경로를 몇 개까지만 보여준다.
func formatPaths(paths []string, limit int) string {
	if len(paths) == 0 {
		return "발자국 없음"
	}
	if limit <= 0 || len(paths) <= limit {
		return fmt.Sprintf("경로 %d: %s", len(paths), strings.Join(paths, ", "))
	}
	return fmt.Sprintf("경로 %d: %s +%d", len(paths), strings.Join(paths[:limit], ", "), len(paths)-limit)
}

// ─────────────────────────────────────────────────────────────────────────────
// board
// ─────────────────────────────────────────────────────────────────────────────

// BoardRenderOptions 는 보드 한 장의 표시 인자다.
type BoardRenderOptions struct {
	Self   string
	Detail bool
	Now    time.Time
	// Budget 은 토큰 상한이다. 0 이면 BoardTokenBudget, Detail 이면 무시한다.
	Budget int
	// Tail 은 응답 꼬리다. **예산 안에 함께 든다** — 꼬리를 예산 밖에 두면
	// 상한이 지켜졌다는 시험이 실제 응답 길이를 안 보는 것이 된다.
	Tail string
	// Notice 는 이 보드가 어떻게 나온 값인지다(열화 배너). 비면 아무것도 안 찍는다.
	//
	// **맨 위**에 온다. 아래로 밀면 낡은 스냅숏을 먼저 읽고 그것을 현재 사실로 믿은 뒤에야
	// 배너를 보게 되고, 그때는 이미 판단이 끝나 있다. 예산에도 함께 든다.
	Notice string
}

// boardCardFloor 는 예산이 아무리 빠듯해도 내는 카드 수다. **예산보다 세다.**
//
// ★ 이 바닥이 없어서 보드가 카드를 0장 냈다. 실측(2026-08-05 01:12 UTC): 살아 있는
// 세션 34건, 카드 0장, "34건을 예산 때문에 접었다" 한 줄만.
//
// 기제: 고정분(머리·발·꼬리·배너)을 루프 **앞에서** 재고 `used+cost > budget` 이면
// break 하므로, 고정분만으로 예산을 넘으면 **첫 블록에서 즉시 break 해 kept==0** 이 된다.
// 그 출력은 예산을 지키지도 못하면서(고정분이 이미 넘었다) 보드의 본체를 100% 잃는다 —
// 양쪽을 다 잃는 유일한 결말이라 어떤 예산 정책으로도 정당화되지 않는다.
// 예산의 목적은 화면을 작게 하는 것이지 **비우는 것이 아니다.**
//
// 그래서 바닥이 예산을 이긴다. 넘긴 사실은 아래에서 소리 내어 말한다 — 조용히 넘기면
// "기본 출력은 예산 안이다"라는 이 함수의 계약이 거짓이 되고, 거짓인 계약은 다음 사람이
// 못 고친다(고정분에 상한을 더한 이번 변경도 그 계약을 믿었으면 시작조차 못 했다).
//
// 값이 3인 이유: 이 보드에서 사람이 카드로 하는 일은 "누가 내 자리를 만지나"이고,
// rankCards 가 ① 나 ② 사건 붙은 카드 ③ 나와 겹치는 카드 순으로 이미 정렬한다.
// 앞 셋이면 그 질문의 답이 나온다 — 바닥은 화면을 채우는 값이 아니라 **판별력이 죽는 지점**이다.
const boardCardFloor = 3

// RenderBoard 는 보드 한 장을 사람이 읽는 텍스트로 만든다. 순수 함수다.
//
// 기본 출력은 BoardTokenBudget 안이다. 넘치면 세션 블록을 자르고
// **잘랐다는 사실과 남은 건수를 찍는다** — 조용히 자르면 "세션이 셋뿐"과
// "셋만 보여준다"가 구분되지 않는다.
//
// 한 가지 예외가 boardCardFloor 다 — 고정분이 예산을 다 먹어도 카드는 그만큼 낸다.
// 그때는 출력이 예산을 넘고, 넘었다는 사실과 넘긴 주체를 함께 찍는다.
func RenderBoard(v service.BoardView, opt BoardRenderOptions) string {
	now := opt.Now
	if now.IsZero() {
		now = v.At
	}
	pathLimit := 3
	if opt.Detail {
		pathLimit = 0
	}

	var head []string
	if strings.TrimSpace(opt.Notice) != "" {
		head = append(head, opt.Notice)
	}
	// ★★ 선점 필터. 이 화면이 답하는 질문은 "누가 살아 있나"가 아니라
	// **"어느 작업이 잡혀 있나"** 다. 선점 없는 카드는 안 낸다 — 자기 카드도 예외가 없다.
	//
	// ★ 필터가 **여기**여야 하는 이유. toolBoard 는 `v.Sessions` 로 겹침(judge.OverlapsWithLive)과
	// cc 표류(DriftedTwins)를 **직접 계산한다.** 그 슬라이스를 줄이면 선점 없이 편집하는
	// 세션이 남의 겹침 판정에서도 사라지고, 카드가 여러 장으로 갈린 사실을 말해 주는
	// 유일한 배너도 죽는다 — 조용한 오탐이 아니라 조용한 미탐이다. 그래서 렌더 안에서만 거른다.
	claimed := make([]service.SessionCard, 0, len(v.Sessions))
	for _, c := range v.Sessions {
		if len(c.View.Claims) > 0 {
			claimed = append(claimed, c)
		}
	}
	folded := len(v.Sessions) - len(claimed)

	head = append(head,
		fmt.Sprintf("보드 · %s · %s · %s",
			v.Project.ID, v.At.UTC().Format("2006-01-02 15:04 UTC"), FormatFreshness(v.Derived)),
		fmt.Sprintf("잡혀 있는 작업 %d건 (선점 기준이다 — 세션의 생사가 아니다)",
			len(claimed)+len(v.OutsideClaims)),
	)
	if b := splitBanner(v.Splits); b != "" {
		head = append(head, b)
	}

	ranked := rankCards(v, claimed, opt.Self, now)
	blocks := make([]string, 0, len(ranked))
	for _, c := range ranked {
		blocks = append(blocks, boardCard(c, now, pathLimit, opt.Detail, v.Asks, v.Blocked))
	}

	var foot []string
	if len(claimed)+len(v.OutsideClaims) == 0 {
		// ★ 0건의 뜻이 바뀌었다. 예전에는 "이 창에 다른 세션이 없다"였고 지금은
		//   **"아무도 항목을 안 쥐고 있다"** 다. 후자는 정상 상태라, 그 사실을 말해야
		//   사람이 없는 서버 장애를 찾아 헤매지 않는다.
		foot = append(foot, "잡혀 있는 작업이 없다 — 아무 세션도 큐 항목을 쥐고 있지 않다. 서버 장애가 아니다.")
	}
	// ★ 창 밖인데 항목을 쥔 세션. **창을 이 화면에 걸지 않는다** — 걸면 회수가 가장
	// 필요한 카드(오래 조용한데 쥐고 있는 것)가 정확히 창 때문에 사라진다.
	// 카드가 아니라 한 줄인 이유: git 파생을 안 읽었다(창 밖까지 파생하면 카드당
	// git 호출 1~4회가 세션 수만큼 터진다). 그 사실을 줄머리가 말한다.
	for _, ov := range v.OutsideClaims {
		_, act := activityOf(ov.Signals, now)
		foot = append(foot, fmt.Sprintf("창 밖 선점 · %s %s · %s · 파생 안 읽음",
			ShortID(ov.Session.ID), strings.Join(ov.Claims, ", "), act))
	}
	// ★ 접은 것을 침묵하지 않는다. 그리고 **조율에서 빠진 게 아니라는 사실**을 함께 말한다 —
	// 안 그러면 읽는 쪽이 "저 세션은 아무도 안 본다"로 잘못 읽는다.
	if folded > 0 {
		foot = append(foot, fmt.Sprintf(
			"선점 없는 세션 %d건은 안 낸다 — 겹침 처방은 그 세션들도 그대로 본다.", folded))
	}
	// 창 밖으로 잘린 것을 침묵시키지 않는다. 창은 표시 구간이지 생존 판정이 아니다(설계 §4) —
	// 이 줄이 없으면 "그런 세션이 없다"와 "안 보여 준다"가 구분되지 않는다.
	if v.OutOfWindow > 0 {
		age := ""
		if !v.OldestOutside.IsZero() {
			age = fmt.Sprintf("(가장 오래된 신호 %s 전) ", FormatAge(now.Sub(v.OldestOutside)))
		}
		// ★ 창 값은 v.Window 에서 그대로 가져온다 — 숫자를 박아 두면 기본값이
		// 바뀔 때마다(0113b35 처럼) 조용히 낡는다. 그리고 "이렇게 본다"에서 멈춘다 —
		// "window=Nh 로 본다"처럼 손잡이를 돌리라는 투로 쓰지 않는다. MCP board 도구는
		// window 인자를 받지 않고(tools.go), 그 인자를 새로 만들지도 않는다(설계가
		// 도구 수를 7개로 눌러 잡는다) — 없는 손잡이를 가리키는 문구는 그 자체가 결함이다.
		// 웹 패널(internal/web/page.go)이 이미 이렇게 한다: 사실만 말하고 지시하지 않는다.
		foot = append(foot, fmt.Sprintf(
			"창 밖 %d건 %s— 창은 표시 구간이지 생존 판정이 아니다(지금 창 %s)",
			v.OutOfWindow, age, FormatAge(v.Window)))
	}
	if opt.Detail {
		foot = append(foot, boardDetailFoot(v)...)
	} else {
		foot = append(foot, boardBriefFoot(v)...)
	}
	// 레인 절 — v.Lane 이 nil 이면 이 조회가 레인을 안 읽은 것이라 아예 안 찍는다.
	// 읽었으면(0건이어도) 반드시 한 줄을 낸다 — renderLane 이 그 0건 문장에
	// "질의는 돌았다"를 적어 nil(안 읽음)과 빈 슬라이스(질의는 돌았는데 아무도 없음)를 가른다
	// (service.BoardView.Lane 주석과 같은 판정).
	if v.Lane != nil {
		foot = append(foot, renderLane(v.Lane, now))
	}
	if opt.Detail {
		foot = append(foot, renderFailures(v.Derived, 0)...)
	} else if len(v.Derived.Failures) > 0 {
		foot = append(foot, fmt.Sprintf("파생 %d축을 못 읽었다 — detail=true 로 축 이름과 원인을 본다",
			len(v.Derived.Failures)))
	}

	if opt.Detail {
		return joinAll(head, blocks, foot, opt.Tail)
	}

	budget := opt.Budget
	if budget <= 0 {
		budget = BoardTokenBudget
	}
	fixed := joinAll(head, nil, foot, opt.Tail)
	used := EstimateTokens(fixed)
	kept := 0
	for _, b := range blocks {
		cost := EstimateTokens(b) + 1
		// 잘랐다는 줄의 몫을 미리 뗀다 — 그 줄이 예산을 넘겨 버리면
		// "잘랐다"는 사실 자체가 잘려 나간다.
		reserve := 0
		if kept < len(blocks)-1 {
			reserve = 24
		}
		// ★ 카드 바닥이 예산보다 세다 — boardCardFloor 주석을 보라.
		if used+cost+reserve > budget && kept >= boardCardFloor {
			break
		}
		used += cost
		kept++
	}

	// 두 사실은 **따로** 말한다. "접었다"는 카드가 넘쳤다는 것이고, 아래 ⚠ 는 고정분이
	// 넘쳤다는 것이다. 원인이 다르므로 뭉치면 읽는 사람이 손댈 자리를 못 찾는다.
	var notes []string
	if kept < len(blocks) {
		notes = append(notes, fmt.Sprintf("… 세션 %d건을 예산(%d토큰) 때문에 접었다 — detail=true 로 전부 본다",
			len(blocks)-kept, budget))
	}
	if used > budget {
		msg := fmt.Sprintf(
			"⚠ 이 출력은 예산 %d토큰을 %d토큰 넘는다 — 넘긴 것은 카드가 아니라 고정분(머리·발·꼬리·배너)이다",
			budget, used-budget)
		if kept > 0 {
			msg += fmt.Sprintf(". 카드 %d장은 바닥(%d장)이 지켰다", kept, boardCardFloor)
		}
		notes = append(notes, msg)
	}
	if len(notes) == 0 {
		return joinAll(head, blocks, foot, opt.Tail)
	}
	shown := append([]string(nil), blocks[:kept]...)
	shown = append(shown, notes...)
	return joinAll(head, shown, foot, opt.Tail)
}

func joinAll(head, blocks, foot []string, tail string) string {
	var parts []string
	parts = append(parts, strings.Join(head, "\n"))
	if len(blocks) > 0 {
		parts = append(parts, strings.Join(blocks, "\n"))
	}
	if len(foot) > 0 {
		parts = append(parts, strings.Join(foot, "\n"))
	}
	if strings.TrimSpace(tail) != "" {
		parts = append(parts, tail)
	}
	return strings.Join(parts, "\n\n")
}

// rankCards 는 예산이 자를 순서를 정한다. 자르는 것은 이 순서의 **뒤부터**다.
//
//	① 나 ② 사건(ask·blocked)이 붙은 카드 ③ 나와 경로가 겹치는 카드 ④ 나머지 — 신호 최신순
//
// ★ 앞선 판은 목록 위치 순으로 잘랐다. 그래서 열린 ask 가 붙은 카드가 조용한 카드보다
// 먼저 접힐 수 있었고, 사건을 카드에 붙여도 예산이 그것을 먼저 버렸다.
// ★ cards 를 따로 받는다 — v.Sessions 가 아니다. 화면은 선점을 든 카드만 내지만
// selfPaths 는 **거르지 않은 v.Sessions** 에서 찾아야 한다: 내가 선점 없이 일하는 중이면
// 내 카드는 화면에 없어도 내 경로는 있고, 그 경로가 남과 겹치는지가 정렬의 근거이기 때문이다.
func rankCards(v service.BoardView, cards []service.SessionCard, self string, now time.Time) []service.SessionCard {
	hasNote := map[string]bool{}
	for _, j := range v.Asks {
		hasNote[j.SessionID] = true
	}
	for _, j := range v.Blocked {
		hasNote[j.SessionID] = true
	}

	var selfPaths []string
	for _, c := range v.Sessions {
		if c.View.Session.ID == self || c.IsSelf {
			selfPaths = c.View.Paths
		}
	}

	rank := func(c service.SessionCard) int {
		switch {
		case c.IsSelf || c.View.Session.ID == self:
			return 0
		case hasNote[c.View.Session.ID]:
			return 1
		case len(selfPaths) > 0 && judge.PathsOverlap(selfPaths, c.View.Paths):
			return 2
		default:
			return 3
		}
	}

	// ★ rank 를 미리 한 번씩만 계산해 카드 옆에 붙여 둔다. 비교자 안에서 다시 부르면
	// sort.SliceStable 이 O(n log n) 번 부르게 되고, judge.PathsOverlap 은 경로쌍 비교라
	// 그 반복이 그대로 헛일이 된다 — 카드 수가 늘면 정렬 하나가 매 렌더마다 그 값을 다시 문다.
	// rank 를 카드와 같은 구조체에 넣어 두는 이유: 정렬이 원소를 맞바꿀 때 rank 도
	// 같이 옮겨가야 하고, 인덱스로 따로 든 슬라이스는 스왑을 안 따라간다.
	type withRank struct {
		card service.SessionCard
		rank int
	}
	tmp := make([]withRank, len(cards))
	for i, c := range cards {
		tmp[i] = withRank{card: c, rank: rank(c)}
	}
	sort.SliceStable(tmp, func(i, j int) bool {
		if tmp[i].rank != tmp[j].rank {
			return tmp[i].rank < tmp[j].rank
		}
		// 같은 등급이면 최근 신호가 앞이다. 신호가 아예 없으면 뒤로.
		return lastSignal(tmp[i].card, now).After(lastSignal(tmp[j].card, now))
	})
	out := make([]service.SessionCard, len(tmp))
	for i, r := range tmp {
		out[i] = r.card
	}
	return out
}

// splitBanner 는 갈림 보고를 머리 한 줄로 낸다.
//
// ★ 없으면 **빈 문자열**이다. 항상 찍으면 배너가 배경이 되고 배경은 아무도 안 읽는다.
// ★ 카드 절이 아니라 머리에 두는 이유: 이것은 특정 카드의 성질이 아니라 이 관측
//
//	전체가 낡은 클라이언트에서 왔다는 사실이다.
func splitBanner(reports []judge.SplitReport) string {
	if len(reports) == 0 {
		return ""
	}
	// ★ len(reports) 를 세지 않는다. 보고는 **갈림 그룹** 단위이고 한 대화가 무관한
	// 그룹을 둘 이상 가질 수 있다 — 그대로 세면 대화 하나가 여러 개로 부풀어
	// 보이고, 그러면 이 배너가 고치려던 바로 그 부풀림을 스스로 저지른다.
	ccs := map[string]bool{}
	for _, r := range reports {
		ccs[r.CCSessionID] = true
	}
	return fmt.Sprintf(
		"⚠ 대화 %d개의 카드가 상하위 경로로 갈렸다 — 그 카드를 연 클라이언트에서 "+
			"워크트리 정규화(4de4b21)가 안 돈다. 정규화가 도는 판은 이 모양을 만들 수 없다.",
		len(ccs))
}

// lastSignal 은 신호 넷 중 가장 최근 시각이다. 없으면 제로값이다.
// **합치지 않는다** — 여기서 최댓값을 쓰는 것은 정렬 키일 뿐이고,
// 카드 본문은 종류별로 따로 낸다(설계 §4).
func lastSignal(c service.SessionCard, now time.Time) time.Time {
	var out time.Time
	for _, at := range c.View.Signals {
		if at.After(out) {
			out = at
		}
	}
	return out
}

// boardCard 는 세션 하나의 블록이다.
//
// asks·blocked 는 보드 전체의 사건 목록이다 — 이 카드는 그중 자기 세션 것만 걸러 싣는다.
func boardCard(c service.SessionCard, now time.Time, pathLimit int, detail bool, asks, blocked []model.Judgment) string {
	v := c.View
	mark := " "
	if c.IsSelf {
		mark = "*"
	}
	label := v.Session.Label
	if strings.TrimSpace(label) == "" {
		label = "(꼬리표 없음)"
	}

	// ★ 브랜치는 0값과 "못 읽었다"를 가른다. 그 구분이 없으면
	//   git 이 죽은 화면과 브랜치가 main 과 같은 화면이 똑같이 보인다.
	branch := "브랜치 ?(못 읽음)"
	if c.BranchKnown {
		branch = v.Branch
		if branch == "" {
			branch = "(분리 HEAD)"
		}
		if c.AheadKnown {
			branch += fmt.Sprintf(" +%d", v.AheadMain)
		}
	}

	state := string(v.Session.State)
	if v.Session.State == model.SessionBlocked && v.Session.BlockedWhy != "" {
		state += "(" + clip(v.Session.BlockedWhy, 80) + ")"
	}

	// ★ 선점이 이 카드의 **존재 이유**다(화면 ①은 선점을 든 카드만 낸다). 그래서
	// 메타 줄이 아니라 머리줄에 온다. 항목 id 는 전부 낸다 — 이 화면의 필터가 곧
	// 이 값이라, 말없이 자르면 화면이 자기 근거를 숨긴다.
	claims := strings.Join(v.Claims, ", ")
	_, act := activityOf(v.Signals, now)

	// ★ 줄 수는 **둘로 유지한다.** 카드당 한 줄이 늘면 예산(1200토큰) 안에 드는 카드 수가
	// 줄고, boardCardFloor 가 예산을 이기는 갈래로 밀려 기본 출력이 상한을 넘는다
	// (실측: 3줄로 늘렸더니 24세션 기본 보드가 1208토큰이 됐다).
	lines := []string{
		fmt.Sprintf("%s%s %s · %s", mark, ShortID(v.Session.ID), claims, act),
		fmt.Sprintf("   %s · %s · %s · %s · %s",
			label, branch, state, formatPaths(v.Paths, pathLimit), FormatSignals(v.Signals, now)),
	}
	if !v.HasFootprint {
		// 안 막는다는 사실이 화면에 있어야 한다(설계 §5의 "그래도 안 보이는 것" ①).
		lines[1] += " | 경로 축에서 아무도 안 막는다"
	}
	// 이 세션이 남긴 ask·blocked 를 카드 안에 붙인다 — 전역 꼬리만으로는
	// 누가 남겼는지가 카드와 안 이어진다. detail 여부와 무관하게 붙인다:
	// 예산 때문에 카드째 접히는 것은 brief 모드에서만 일어나고,
	// 그때의 안전망은 (제거하지 않는) 전역 꼬리·전역 목록이 맡는다.
	lines = append(lines, noteLines(v.Session.ID, asks, blocked, now)...)
	if detail {
		if c.DeriveError != "" {
			lines = append(lines, "   파생 결손: "+clip(c.DeriveError, 200))
		}
		if v.LastNote != nil {
			lines = append(lines, fmt.Sprintf("   마지막 판단 [%s] %s (%s 전)",
				v.LastNote.Kind, clip(firstLine(v.LastNote.Title, v.LastNote.Body), 80),
				FormatAge(now.Sub(v.LastNote.At))))
		}
	}
	return strings.Join(lines, "\n")
}

// cardNoteLimit 은 카드 하나가 싣는 사건 줄 수다.
//
// ★ 이것이 **카드 바닥의 비용**을 정한다. boardCardFloor 는 예산을 이기고 카드를 남기는데,
// 그 카드 한 장의 크기에 상한이 없으면 바닥이 예산을 얼마나 넘길지도 상한이 없다.
// 실측(2026-08-05, 살아 있는 세션 33건): 남은 카드 3장에 사건 줄이 8개 붙어 예산을
// 531토큰 넘겼고, 그 줄들이 초과분의 대부분이었다.
//
// 이 축은 세션이 오래 살수록 자란다(한 세션이 ask 를 계속 남긴다). 즉 꼬리·배너와
// 같은 O(N) 이고, 같은 이유로 상한이 필요하다. 최신순으로 앞의 몇 개만 낸다 —
// 오래된 요청보다 방금 온 요청이 지금 조율에 필요한 것이다.
const cardNoteLimit = 2

// noteLines 는 이 카드가 실을 사건 줄이다.
//
// ★ 전역 꼬리를 없애지 않는다. 카드가 접히면 사건도 접히므로 꼬리가 그 안전망이다.
// 상한에 걸려 안 보인 것도 마찬가지다 — 수를 말하고, 전부는 detail 과 꼬리가 맡는다.
func noteLines(sessionID string, asks, blocked []model.Judgment, now time.Time) []string {
	var out []string
	dropped := 0
	add := func(kind string, js []model.Judgment) {
		shown := 0
		for _, j := range js {
			if j.SessionID != sessionID {
				continue
			}
			if shown >= cardNoteLimit {
				dropped++
				continue
			}
			shown++
			out = append(out, fmt.Sprintf("   [%s %s] %s",
				kind, FormatAge(now.Sub(j.At)), clip(firstLine(j.Title, j.Body), 100)))
		}
	}
	add("ask", asks)
	add("blocked", blocked)
	if dropped > 0 {
		out = append(out, fmt.Sprintf("   … 이 세션의 사건 %d건 더 — detail=true 로 전부 본다", dropped))
	}
	return out
}

func boardBriefFoot(v service.BoardView) []string {
	var out []string
	if len(v.OpenItems) > 0 {
		ids := make([]string, 0, 3)
		for i, it := range v.OpenItems {
			if i >= 3 {
				break
			}
			ids = append(ids, it.ID)
		}
		line := fmt.Sprintf("큐 열림 %d건: %s", len(v.OpenItems), strings.Join(ids, ", "))
		if len(v.OpenItems) > 3 {
			line += fmt.Sprintf(" +%d", len(v.OpenItems)-3)
		}
		out = append(out, line)
	} else {
		out = append(out, "큐 열림 0건")
	}
	if len(v.Held) > 0 {
		out = append(out, "자원 점유: "+heldLine(v.Held))
	}
	return out
}

func boardDetailFoot(v service.BoardView) []string {
	var out []string
	out = append(out, fmt.Sprintf("큐 열림 %d건", len(v.OpenItems)))
	for _, it := range v.OpenItems {
		line := fmt.Sprintf("  · %s — %s", it.ID, clip(it.Title, 90))
		if len(it.Paths) > 0 {
			line += " [" + strings.Join(it.Paths, ", ") + "]"
		}
		out = append(out, line)
	}
	if len(v.Held) > 0 {
		out = append(out, "자원 점유: "+heldLine(v.Held))
	} else {
		out = append(out, "자원 점유 없음")
	}

	if len(v.Blocked) > 0 {
		out = append(out, fmt.Sprintf("막힘 %d건", len(v.Blocked)))
		for _, j := range v.Blocked {
			out = append(out, "  · "+clip(firstLine(j.Title, j.Body), 120))
		}
	}
	if len(v.Asks) > 0 {
		out = append(out, fmt.Sprintf("요청(ask) %d건", len(v.Asks)))
		for _, j := range v.Asks {
			out = append(out, "  · "+clip(firstLine(j.Title, j.Body), 120))
		}
	}
	if r := v.AckReach; r != nil && r.Emitted > 0 {
		out = append(out, fmt.Sprintf(
			"확인율 — 발화 카드 %d · 그중 ack 이 닿을 수 있는 카드 %d · 실제 ack %d "+
				"(두 수가 크게 다르면 그 차이가 카드 갈림이다)",
			r.Emitted, r.Reachable, r.Acked))
	}
	return out
}

// renderLane 은 보드의 레인 절 한 줄이다. 순수 함수다.
//
// 설계 §9 ① 이 요구하는 축을 전부 낸다: **점유자의 획득 경과**(머리) · 대기 줄 전체
// (순번 · 세션 · 대기 경과 · **마지막 신호 나이**). 회수는 자동 만료가 아니라 사람이
// 이 두 나이를 보고 내리는 판정이라, 그 주 표면인 보드에서 빠지면 판정의 근거가 없다.
//
// ★ 호출부(RenderBoard)가 v.Lane == nil 이면 이 함수를 아예 안 부른다. 그래서 여기 들어온
// 이상 질의는 이미 돈 것이고, Entries 가 비었어도 그 사실("질의는 돌았다")을 문장에
// 반드시 남긴다 — 안 남기면 "질의가 안 돌았다"(nil)와 "아무도 안 섰다"(빈 Entries)가
// 화면에서 같아진다(service.LaneView 주석과 같은 판정).
func renderLane(l *service.LaneView, now time.Time) string {
	if len(l.Entries) == 0 {
		if l.Holder == nil {
			// ★ 짧게 쓴다. 이 줄은 레인이 비어 있어도 **매 보드마다** 나가고 잘리지 않는
			//   고정분이라(joinAll 의 foot), 한 낱말이 세션 카드 하나를 접는 값이 된다.
			//   실제로 길게 썼을 때 TestBoardDefaultOutputWithinBudget 이 5토큰 초과로 빨개졌다.
			//   "지금 아무도 안 섰다"는 "0건"과 같은 말이라 뺀다 —
			//   락이 걸린 축은 "질의는 돌았다"(nil 과 빈 슬라이스를 가르는 문구)뿐이다.
			return "랜딩 레인 0건(질의는 돌았다)"
		}
		// ★ 점유는 있는데 줄 행이 하나도 없다 — landing.go 의 불변식("살아 있는 랜딩 점유에는
		// 반드시 대응하는 살아 있는 줄 행이 있다")이 깨진 가장 위험한 모양이다
		// (TestLiveLandingHoldAlwaysHasALiveQueueRow 가 잡으려는 상태 그 자체다. Land 도 이
		// 상태를 만나면 점유자를 그대로 실어 보낸다 — landing.go 참고). 위 0건 분기로 접으면
		// 정확히 이 상태에서 경고가 필요한데 그 경고에 영원히 안 닿는다 — 그래서 여기서 먼저
		// 가른다: 조용한 "비어 있음"이 아니라 화면에서 가장 시끄러운 문장을 낸다.
		//
		// ★ 회수 판정용 **두 나이**(설계 §9 ①)를 이 문장에 싣는다. 아래 정상 경로는 획득 경과를
		// 머리에, 신호 나이를 항목마다 나눠 싣는데 여기는 항목이 0건이라 실을 자리가 이 한 줄뿐이다.
		// 그런데 회수 판정이 **가장 절실한** 화면이 바로 여기다 — 줄 행이 사라진 점유는 사람이
		// 거둬야 하고 그 판단의 근거가 이 두 숫자인데, 둘 다 없으면 화면이 "누가"만 답하고
		// "얼마나 오래됐나"를 되묻게 만든다.
		//
		// ★ 그중 Holder.LastSignalAt 은 LandingLane 이 홀더용으로 질의를 한 번 더 돌려 채워 두고도
		// renderLane 이 전 함수 통틀어 한 번도 안 읽던 필드였다 —
		// TestLandingQueueHasAProductionReader 가 잡으려는 "계산만 되고 읽는 쪽이 0건"의 필드 판.
		//
		// 낱말은 아래 정상 경로(`점유 획득 %s전` · 항목별 `신호 %s전`/`신호 없음`)를 그대로 베낀다.
		// 같은 숫자가 한 화면에서 두 어휘로 읽히면 회수 판정이 대조부터 해야 한다.
		//
		// ★ nil 은 빈칸으로 두지 않고 "신호 없음"이라고 **적는다** — 여기서 하는 일은 그것뿐이다.
		// **못 읽음과 없음을 가르는 자리가 아니다.** 이 nil 은 두 경우가 이미 뭉개진 값이다:
		// 그 둘을 실제로 가르는 것은 service/landing.go 의 lastSignal 의 **둘째 반환값**인데,
		// 이 필드를 채우는 세 자리(Land 의 점유자 채움 · LandingLane 의 루프 · LandingLane 의
		// 점유자-줄에-없음 갈래)가 전부 그 값을 `_` 로 버린다. 읽기 실패는 그쪽 WARN 에만 남는다.
		// 그 규율이 지켜지는 곳은 불변으로 남는 판단 본문(ReleaseLaneRow)이고, 거기만
		// "읽지 못했다"와 "없음"을 다른 문장으로 적는다 — 화면은 애초에 그 축을 못 받는다.
		//
		// ★ ShortID 바로 뒤에 여는 괄호를 두지 않고 문장 꼬리에 ` · ` 로 잇는다. 머리에
		// `<세션>(…)` 모양이 생기면 항목 조각을 잘라 보는 시험(laneEntrySegment)이 그 `)` 를
		// 먼저 집는다 — 이 분기는 항목이 0건이라 지금은 안 밟히지만, 같은 함정을 새로 파지 않는다.
		sig := "신호 없음"
		if l.Holder.LastSignalAt != nil {
			sig = "신호 " + FormatAge(now.Sub(*l.Holder.LastSignalAt)) + "전"
		}
		return fmt.Sprintf("⚠ 랜딩 레인 정합 어긋남: 점유자 %s 는 있는데 줄 행이 하나도 없다 · 점유 획득 %s전 · %s",
			ShortID(l.Holder.SessionID), FormatAge(now.Sub(l.Holder.AcquiredAt)), sig)
	}
	parts := make([]string, 0, len(l.Entries))
	for i, e := range l.Entries {
		mark := ""
		if l.Holder != nil && l.Holder.SessionID == e.SessionID {
			mark = "◀점유"
		}
		// ★ **신호 나이를 낸다**(설계 §9 ①). 자동 만료를 안 만든 근거가 "사람이 나이를 보고
		// 판정한다"인데, 그 판정을 내리는 사람은 대기자가 아니라 보드를 보는 사람이다.
		// 여기서 빼면 LaneEntry.LastSignalAt 은 계산만 되고 읽는 쪽이 0건이 된다 —
		// 이 브랜치가 TestLandingQueueHasAProductionReader 로 잡으려는 함정의 필드 판이다.
		// nil 은 빈칸이 아니라 "신호 없음"으로 적는다 — 다만 그 nil 은 못 읽음과 없음이 이미
		// 뭉개진 값이다(위 어긋남 갈래 주석의 세 자리와 같은 이유. LaneEntry 쪽을 채우는 것이
		// 그중 LandingLane 의 루프다).
		sig := "신호 없음"
		if e.LastSignalAt != nil {
			sig = "신호 " + FormatAge(now.Sub(*e.LastSignalAt)) + "전"
		}
		parts = append(parts, fmt.Sprintf("%d.%s(행%d·대기 %s전·%s%s)",
			i+1, ShortID(e.SessionID), e.RowID, FormatAge(now.Sub(e.EnqueuedAt)), sig, mark))
	}
	// ★ 머리에 **점유자의 획득 경과**를 낸다(설계 §9 ①). 회수를 판정하는 사람이 봐야 할
	// 두 숫자가 획득 경과와 신호 나이인데, 앞엣것은 LaneHolder.AcquiredAt 에 채워져 있으면서
	// 이 함수가 안 읽어 화면에 없었다.
	//
	// ★ 점유자의 ShortID 를 머리에 **다시 적지 않는다.** 누가 쥐었나는 항목의 ◀점유 표시가
	// 이미 답하고, 머리에 `<세션>(…)` 모양을 하나 더 두면 항목 조각을 잘라 보는 시험
	// (laneEntrySegment)이 머리 쪽을 먼저 집어 표시 뒤바뀜을 못 잡게 된다.
	head := fmt.Sprintf("랜딩 레인 %d건", len(l.Entries))
	if l.Holder != nil {
		head += fmt.Sprintf("(점유 획득 %s전)", FormatAge(now.Sub(l.Holder.AcquiredAt)))
	}
	line := head + ": " + strings.Join(parts, " ")
	if l.Holder != nil && !laneHolderIsQueued(l) {
		// 살아 있는 점유에는 반드시 대응하는 살아 있는 줄 행이 있어야 한다(landing.go 의 불변식).
		// 그게 깨진 상태를 침묵하면 "레인이 비었다"로 오독된다.
		//
		// ★ 점유자의 **신호 나이**도 여기서 낸다(설계 §9 — ① 이 아니라 **그 절 끝의 점유자
		// 신호 문단**이다. ① 은 신호 나이를 대기 줄 항목 괄호 안에만 두고, 점유자에 대해서는
		// "창 밖 세션도 답하는 접근자가 필요하다"를 따로 적는다). 위 완전 어긋남 갈래(항목 0건)는
		// 회수 판정용 두 나이를 다 싣는데 이쪽은 획득 경과만 머리에 있고 신호 나이가 없었다 —
		// **같은 종류의 사고인데 한쪽만 고친 자리다.** 항목마다 붙는 `신호 %s전` 은 줄에
		// **있는** 세션들의 것이고 점유자는 정의상 그 목록에 없으므로, 그 값이 이 화면
		// 어디에도 없었다. 회수를 판정하는 사람은 "누구의 줄 행이 사라졌나"만 듣고
		// "그 세션이 얼마나 조용한가"는 되물어야 했다.
		//
		// ★ 낱말은 완전 어긋남 갈래와 정상 경로에서 그대로 베낀다(`신호 %s전`/`신호 없음`).
		// 같은 축이 한 화면에서 두 어휘로 읽히면 회수 판정이 대조부터 해야 한다.
		// nil 을 "신호 없음"으로 적는 것의 한계도 그 갈래 주석과 같다 — 이 nil 은
		// 못 읽음과 없음이 이미 뭉개진 값이고, 가르는 축은 화면에 애초에 안 온다.
		//
		// ★ 이 모양이 세 자리째지만 헬퍼로 **안 뽑는다.** 지금 render.go 를 쥔 세션이
		// 더 있어(겹침 통보 완료) 기존 두 자리를 건드리면 충돌 면적이 그만큼 넓어진다.
		// 뽑는 것은 그 자리가 조용해진 뒤에 할 별개의 일이다.
		sig := "신호 없음"
		if l.Holder.LastSignalAt != nil {
			sig = "신호 " + FormatAge(now.Sub(*l.Holder.LastSignalAt)) + "전"
		}
		line += fmt.Sprintf(" · ⚠ 점유자 %s 의 줄 행이 안 보인다(정합 어긋남) · %s",
			ShortID(l.Holder.SessionID), sig)
	}
	return line
}

// laneHolderIsQueued 는 지금 점유자가 줄 목록에도 있는지다.
func laneHolderIsQueued(l *service.LaneView) bool {
	for _, e := range l.Entries {
		if e.SessionID == l.Holder.SessionID {
			return true
		}
	}
	return false
}

func heldLine(held []model.ResourceHold) string {
	parts := make([]string, 0, len(held))
	for _, h := range held {
		holder := ShortID(h.SessionID)
		if h.SessionID == "" {
			holder = "job:" + h.JobID
		}
		parts = append(parts, fmt.Sprintf("%s ← %s", h.Resource, holder))
	}
	return strings.Join(parts, " · ")
}

func firstLine(title, body string) string {
	if strings.TrimSpace(title) != "" {
		return title
	}
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		return body[:i]
	}
	return body
}

// ─────────────────────────────────────────────────────────────────────────────
// pick
// ─────────────────────────────────────────────────────────────────────────────

// RenderPick 은 pick 한 번의 결과다. 순수 함수다.
//
// 꼬리에 **브랜치 이름과 워크트리 준비 명령**을 낸다(설계 §6).
// 명령을 못 만들었으면 그 사실과 사유를 낸다 — 침묵하면 "명령이 없는 도구"로 읽힌다.
func RenderPick(r service.PickResult, now time.Time) string {
	var b strings.Builder

	// 묶음 수를 **둘** 센다. 하나로 뭉개면 응답이 안 한 일을 셈에 넣는다.
	//
	//	n     = 이 응답이 다루는 묶음 크기(선두 + 구성원 전부). 요청·제안된 규모다.
	//	heldN = 이 응답이 실제로 쥔 수(선두 + Claimed 인 구성원). **관측된 규모**다.
	//	unclaimed = 구성원 중 안 집힌 것들의 id — 아래에서 이름으로 부른다.
	//
	// ★ 예전에는 겹침 범위 줄과 브랜치 줄이 둘 다 len(Members)+1 을 썼다. 그런데
	// service.pickBundle 은 **집은 구성원의 경로만** 합쳐서 겹침을 판정한다. 3건 중
	// 1건만 집힌 응답이 "묶음 3건의 경로를 전부 합쳐서 봤다"고 말하면, 겹침 0건을
	// 본 세션은 못 집은 2건의 경로까지 안전하다고 결론짓는다 — 겹침 축은 정확히
	// 그 결론을 막으려고 있는 축이다. 커버리지 과장은 침묵보다 나쁘다.
	//
	// Bundle 이 nil 이면 이 응답이 그 축을 안 읽은 것이므로 둘 다 1(단독 문구)로 둔다 —
	// renderBundle 이 그 부재를 따로 말하고, 여기서 흉내 내지 않는다.
	n, heldN := 1, 1
	var unclaimed []string
	if r.Bundle != nil {
		n = len(r.Bundle.Members) + 1
		for _, m := range r.Bundle.Members {
			if m.Claimed {
				heldN++
			} else {
				unclaimed = append(unclaimed, m.Item.ID)
			}
		}
	}
	switch r.Mode {
	case service.PickRecommended:
		if n > 1 {
			fmt.Fprintf(&b, "pick · 추천 묶음 %d건 — **아직 선점하지 않았다**\n", n)
		} else {
			b.WriteString("pick · 추천 1건 — **아직 선점하지 않았다**\n")
		}
	case service.PickClaimed:
		if n > 1 {
			fmt.Fprintf(&b, "pick · 선점했다 — 묶음 %d건 중 %d건\n", n, heldN)
		} else {
			b.WriteString("pick · 선점했다\n")
		}
	case service.PickResumed:
		// ★ 재개 갈래도 묶음 수를 말한다. 예전에는 이 줄만 묶음을 통째로 빠뜨려서,
		// 묶음 재개 응답의 머리줄이 단독 재개와 글자 하나 다르지 않았다 — 세션은
		// 자기가 몇 건을 쥔 채 이 브랜치로 돌아왔는지를 머리줄에서 못 읽었다.
		if n > 1 {
			fmt.Fprintf(&b, "pick · 재개 — 선두는 이미 내 선점이다(선점 시각은 그대로 둔다). "+
				"묶음 %d건 중 %d건을 쥐고 있다\n", n, heldN)
		} else {
			b.WriteString("pick · 재개 — 이미 내 선점이다(선점 시각은 그대로 둔다)\n")
		}
	default:
		b.WriteString("pick · 적격 0건\n")
	}
	fmt.Fprintf(&b, "사유: %s\n", r.Reason)
	if r.Scope != "" {
		fmt.Fprintf(&b, "범위: %s\n", r.Scope)
	}
	// 큐 규모. board 가 쓰는 이름을 **그대로** 쓴다(같은 술어에 두 번째 이름을 붙이면
	// 두 수가 갈려도 읽는 쪽이 "다른 지표겠지"로 넘어가 불일치가 조용히 정상으로 등록된다).
	//
	// nil 을 침묵으로 접지 않는다. 원인은 셋인데(구버전 서버 · 옛 캐시 · 조회 실패)
	// nil 하나로는 못 가르므로 **원인 중립 문장**을 쓴다 — 지어낸 원인보다 정확하고,
	// 이 문장은 SkewBanner 가 못 잡는 스큐 구간의 유일한 신호이기도 하다.
	if r.QueueOpen != nil {
		fmt.Fprintf(&b, "큐 열림 %d건\n", *r.QueueOpen)
	} else {
		b.WriteString("큐 열림 수가 이 응답에 없다 — 서버 판이 이 축을 안 내거나 세지 못했다\n")
	}

	if r.Item != nil {
		it := *r.Item
		fmt.Fprintf(&b, "\n▸ %s — %s [%s]\n", it.ID, it.Title, it.State)
		if len(it.Paths) > 0 {
			fmt.Fprintf(&b, "경로: %s\n", strings.Join(it.Paths, ", "))
		}
		b.WriteString(renderPathCheck(r.PathCheck, it.ID))
		if len(it.After) > 0 {
			fmt.Fprintf(&b, "선행: %s\n", formatAfter(it.After))
		}
		if strings.TrimSpace(it.Body) != "" {
			fmt.Fprintf(&b, "본문:\n%s\n", indent(clip(it.Body, 4000), "  "))
		}
	}

	// 묶음 절. renderBundle 이 세 갈래(부재·단독·구성원 목록)를 전부 말한다 —
	// 이 위치는 항목 블록 뒤·브랜치 줄 앞이다.
	b.WriteString(renderBundle(r.Bundle))
	// 꼬리의 "겹침:" 줄이 **어떤 경로 집합**을 보고 나온 값인지 말한다. RenderTail 은
	// 모든 도구가 쓰고 묶음을 모르므로 이 자리가 유일한 발화처다.
	//
	// ★ 규칙은 **모드마다 다르다.** 한 줄로 뭉치면 한쪽이 반드시 거짓이 된다 —
	// 실제로 한 번 그랬다(아래 참고). 겹침을 누가 어떻게 계산하는지가 갈리기 때문이다:
	//
	//	추천(PickRecommended)  judge.EligibleBundle 이 bundlePaths(선두 ∪ **구성원 전부**)로
	//	                       낸 Lead.Overlaps 를 그대로 싣는다. 아직 아무것도 안 집었지만
	//	                       **판정 범위는 묶음 전체**다 — 그게 이 추천을 통째로 집었을 때
	//	                       부딪히는지를 미리 답하는 값이기 때문이다.
	//	선점·재개             service.pickBundle 이 **집은 것만** allPaths 에 합쳐 다시 낸다.
	//	                       (pickExplicit 은 구성원 0건이라 선두뿐 — 같은 규칙의 밑변이다.)
	//
	// 예전엔 둘 다 heldN(=쥔 수)으로 갈랐다. 추천은 아무것도 안 쥐므로 heldN 이 늘 1 이라,
	// 구성원 있는 추천이 "항목 X 의 경로만 봤다 · 구성원 N건은 판정에 안 들어갔다"를 찍었다 —
	// **둘 다 거짓이다.** 관측한 것을 안 했다고 말하는 것이라, 이 고침 물결이 닫으려던
	// 결함과 같은 부류다(방향만 반대다: 과장이 아니라 축소).
	//
	// 그래서 게이트는 heldN 이 아니라 **r.Mode** 다. 브랜치 줄은 그대로 heldN 을 쓴다 —
	// 그 줄의 질문은 "지금 무엇을 쥐었나"라서 heldN 이 옳다. 두 줄이 다른 수를 쓰는 것이
	// 정상이고, 그 이유가 이 주석이다.
	//
	// Bundle 이 nil 일 때는 아무 말도 안 한다 — 그 응답은 묶음 축 자체를 안 읽었고
	// (구버전 서버·옛 캐시) 범위를 단정할 근거가 없다. 그 부재는 renderBundle 이 말한다.
	if r.Bundle != nil && r.Item != nil {
		// scopeN = 이 응답의 겹침이 **실제로 합친** 항목 수.
		scopeN := heldN
		if r.Mode == service.PickRecommended {
			scopeN = n // 추천은 묶음 전체를 합쳐서 본다(아직 안 집었어도)
		}
		if scopeN > 1 {
			fmt.Fprintf(&b, "겹침 판정 범위: 묶음 %d건의 경로를 전부 합쳐서 봤다 — "+
				"남과 부딪히는지는 묶음 단위 질문이다.\n", scopeN)
		} else {
			fmt.Fprintf(&b, "겹침 판정 범위: 항목 %s 의 경로만 봤다 — "+
				"이 응답이 합친 경로는 그것뿐이다.\n", r.Item.ID)
		}
		// 판정 밖으로 빠진 구성원은 **이름으로** 부른다. 수만 말하면 어느 것의 경로가
		// 밖인지 못 가리고, 세션은 겹침 0건을 묶음 전체에 적용해 버린다.
		//
		// ★ 추천 모드에는 이 줄이 없다 — 안 집은 구성원의 경로도 **판정에 들어갔기
		// 때문이다.** 여기서 습관적으로 unclaimed 를 찍으면 그것이 정확히 위에서 말한
		// 그 거짓말이 된다.
		if r.Mode != service.PickRecommended && len(unclaimed) > 0 {
			fmt.Fprintf(&b, "  안 집은 구성원 %d건(%s)의 경로는 이 판정에 **안 들어갔다** — "+
				"그 항목들에 대해서는 겹침을 관측하지 않았다.\n",
				len(unclaimed), strings.Join(unclaimed, ", "))
		}
	}

	if r.Claim != nil {
		fmt.Fprintf(&b, "선점 시각: %s (%s 전)\n",
			r.Claim.At.UTC().Format("2006-01-02 15:04 UTC"), FormatAge(now.Sub(r.Claim.At)))
	}

	if r.Branch != "" {
		fmt.Fprintf(&b, "\n브랜치: %s\n", r.Branch)
		if r.Bundle != nil && len(r.Bundle.Members) > 0 {
			// 브랜치는 선두 하나뿐이다 — 구성원은 같은 워크트리에 얹혀 갈 뿐 각자
			// 브랜치를 갖지 않는다. 이 사실을 안 말하면 "브랜치가 구성원마다
			// 따로 있나"로 읽힐 여지가 남는다.
			//
			// ★ 여기서 세는 것은 **쥔 수(heldN)** 다. 요청 크기를 쓰면 "3건을 이
			// 워크트리에서 함께 한다"가 나오는데, 그중 2건은 남이 쥐고 있어 이 세션이
			// 손댈 수 없다 — 그대로 믿은 세션은 남의 항목을 자기 브랜치에서 고친다.
			switch {
			case heldN > 1:
				fmt.Fprintf(&b, "  묶음 선두의 id 다. %d건을 이 워크트리에서 함께 한다.\n", heldN)
			case r.Mode == service.PickRecommended:
				fmt.Fprintf(&b, "  묶음 선두의 id 다. 구성원 %d건은 아직 선점 전이라 "+
					"지금 확정된 것은 선두 1건뿐이다.\n", len(r.Bundle.Members))
			default:
				fmt.Fprintf(&b, "  묶음 선두의 id 다. 구성원 %d건은 이 응답이 못 집었다 — "+
					"이 워크트리에서 함께 하는 것은 선두 1건뿐이다.\n", len(r.Bundle.Members))
			}
		}
		if len(r.Setup) > 0 {
			b.WriteString("워크트리 준비:\n")
			for _, c := range r.Setup {
				fmt.Fprintf(&b, "  %s\n", c)
			}
		} else {
			b.WriteString("워크트리 준비 명령을 만들지 않았다 — " +
				"항목 id 가 브랜치·디렉토리 이름으로 안전하지 않거나 프로젝트 경로가 없다.\n")
		}
	}

	// 연결된 판단. 지정 선점·재개는 **전문**이고 추천은 제목만이다(설계 §6).
	// 추천은 아직 안 집은 항목이라 전문을 실으면 후보마다 컨텍스트를 태우게 된다.
	if len(r.Notes) > 0 {
		full := r.Mode == service.PickClaimed || r.Mode == service.PickResumed
		fmt.Fprintf(&b, "\n연결된 판단 %d건%s:\n", len(r.Notes), map[bool]string{true: " (전문)", false: " (제목만 — 집으면 전문이 온다)"}[full])
		for _, j := range r.Notes {
			fmt.Fprintf(&b, "  [%s] %s · %s\n", j.Kind,
				j.At.UTC().Format("2006-01-02 15:04"), clip(firstLine(j.Title, j.Body), 100))
			if full && strings.TrimSpace(j.Body) != "" {
				b.WriteString(indent(clip(j.Body, 4000), "    ") + "\n")
			}
		}
	}

	if len(r.Rejected) > 0 {
		fmt.Fprintf(&b, "\n탈락 사유 %d줄 (사유 코드 그대로):\n", len(r.Rejected))
		for _, rj := range r.Rejected {
			fmt.Fprintf(&b, "  %-20s %-24s %s\n", rj.Reason, clip(rj.Item, 24), clip(rj.Detail, 120))
		}
	}

	if lines := renderFailures(r.Derived, 6); len(lines) > 0 {
		b.WriteString("\n" + strings.Join(lines, "\n") + "\n")
	}
	fmt.Fprintf(&b, "\n%s", FormatFreshness(r.Derived))
	return b.String()
}

// RenderBundleUnaccounted 는 **요청했는데 응답이 설명하지 않은 id** 를 낸다. 순수 함수다.
//
// ★ 이 문장이 없으면 무엇이 깨지나. item_ids 를 모르는 구서버는 그 필드를 조용히
// 버리고 경로의 선두 하나만 집은 뒤 200 을 낸다 — api_version 이 양쪽 다 "1" 이라
// SkewBanner 도 안 뜬다. 그러면 `pick(item_ids:[a,b,c])` 가 정상 응답처럼 보이는데
// b·c 는 아무도 안 쥔 상태다. 선점이 존재하는 이유가 바로 그 상황을 막는 것이므로,
// 이건 화면 문제가 아니라 **선점 계약이 깨진 것**이고 반드시 이름을 부른다.
//
// 원인을 지어내지 않는다 — 구서버일 수도, 프록시가 필드를 떨어뜨렸을 수도, 응답이
// 중간에서 갈렸을 수도 있다. 확실한 것 하나만 말한다: 이 id 들을 이 세션이 쥐었다는
// 근거가 응답 어디에도 없다.
func RenderBundleUnaccounted(missing []string) string {
	if len(missing) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "★ 요청한 항목 %d건을 이 응답이 설명하지 않는다: %s\n",
		len(missing), strings.Join(missing, ", "))
	b.WriteString("  이 세션이 그것들을 쥐었다는 근거가 응답 어디에도 없다 — " +
		"선점됐다고 가정하고 손대면 남과 같은 파일을 동시에 고치게 된다.\n")
	b.WriteString("  서버가 item_ids 를 모르는 판일 수 있다(구서버 + 신 클라이언트). " +
		"`fd status` 로 서버 판을 보고, 필요하면 하나씩 item_id 로 다시 집어라.\n")
	return b.String()
}

// 묶음 구성원 표식 — 셋이고, 서로 다르다.
//
// ★ internal/service/pick.go 의 BundleMember.Claimed 주석이 세 상태를 못박는다:
// 집었다 · 시도했지만 실패했다 · 아직 안 봤다(추천, 시도 자체가 없다). 표식이 둘뿐이면
// 뒤의 둘이 하나로 뭉개지고, 그러면 "탈락"과 "아직 안 건드림"이 화면에서 같아진다.
const (
	markClaimed  = "+" // Claimed=true — 집었다
	markRejected = "✗" // Claimed=false, Rejection!=nil — 집으려 했는데 실패했다
	markProposed = "○" // Claimed=false, Rejection=nil — 아직 집기를 시도하지 않았다(추천 경로)
)

// renderBundle 은 묶음 절이다. 순수 함수다.
//
// ★ **어느 갈래에서도 침묵하지 않는다.** 셋을 다 말한다:
// 축을 안 읽었다 · 묶을 게 없어 단독이다 · 이런 것들과 묶였다.
// 침묵하면 "묶을 게 없다"와 "이 축을 안 봤다"가 같은 화면이 되고,
// 그러면 판정이 통째로 실패한 날에도 pick 은 평소와 똑같아 보인다.
// renderPathCheck 이 같은 이유로 이상이 없어도 한 줄을 찍는다.
func renderBundle(bi *service.BundleInfo) string {
	if bi == nil {
		// ★ 이 문장은 이제 **정말로 축을 안 읽은 응답에만** 붙는다. 서비스의 세 갈래
		// (추천 · item_id 선점/재개 · 묶음)가 전부 non-nil 을 내므로, nil 이 남는 길은
		// 둘뿐이다: 이 필드를 모르는 구서버, 또는 필드가 생기기 전에 굳은 오프라인 캐시.
		// 그래서 원인 둘을 그대로 대는 것이 옳다 — 한때 이 문장이 현행 서버의 신선한
		// 응답에도 붙어서 두 원인이 다 거짓이었고, 그 구멍은 pickExplicit 이 축을
		// 채우면서 닫혔다(그 함수의 주석 참고).
		//
		// 겹침 범위는 여기서 **단정하지 않는다.** 축을 안 읽은 응답이라 이 응답이 어떤
		// 경로 집합으로 겹침을 봤는지도 알 수 없다 — 모르는 것을 "선두 경로만 봤다"로
		// 메우면 부재를 값으로 접는 같은 실패를 한 칸 옆에서 반복하는 것이 된다.
		return "\n묶음: 이 응답은 그 축을 읽지 않았다 — 낡은 캐시이거나 서버가 이 축을 모르는 판이다.\n" +
			"  그래서 아래 겹침이 어떤 경로 집합을 보고 나온 값인지도 이 응답만으로는 알 수 없다.\n"
	}
	var b strings.Builder
	if len(bi.Members) == 0 {
		// ★ "함께 갈 항목이 없다"고 단정하지 않는다. 구성원 0건이 나오는 갈래가 둘인데
		// 두 갈래가 말하는 사실이 다르기 때문이다:
		//   추천 경로  — 이웃을 **찾아봤고** 직접 이어진 것이 없었다.
		//   item_id 경로 — 이웃을 **애초에 안 찾았다**(방사형 판정을 안 돌린다).
		// 두 번째에 대고 "함께 갈 항목이 없다"고 하면 관측한 적 없는 사실을 단정하게
		// 되고, 그걸 읽은 세션은 이 항목에 형제가 없다고 결론짓는다. 어느 쪽인지는
		// Scope 가 말한다 — 그래서 여기서 Scope 를 **반드시** 찍는다(예전에는 구성원이
		// 있을 때만 찍어서, 정작 0건일 때 그 사실이 침묵으로 사라졌다).
		b.WriteString("\n묶음: 구성원이 없다 — 단독이다.\n")
		if bi.Reason != "" {
			fmt.Fprintf(&b, "  %s\n", bi.Reason)
		}
		if bi.Scope != "" {
			fmt.Fprintf(&b, "  묶음 범위: %s\n", bi.Scope)
		}
		return b.String()
	}
	fmt.Fprintf(&b, "\n묶음 구성원 %d건 (선두는 위의 항목이다):\n", len(bi.Members))
	for _, m := range bi.Members {
		// ★ 세 상태를 세 표식으로 낸다(internal/service/pick.go 의
		// BundleMember.Claimed 주석이 적은 그 쌍 그대로). Claimed 필드 하나만 보고
		// 가르면(!Claimed) "집었다"의 반대를 전부 "실패"로 접는데, 그 반대에는
		// **아직 시도조차 안 한 추천 후보**가 섞여 있다. 그걸 실패와 같은 표식으로
		// 찍으면 4건 추천에서 셋이 ✗ 로 보이고, 그걸 본 에이전트가 "셋이 탈락했다"로
		// 읽어 묶음을 버리고 혼자 다시 집는다 — 판정이 방금 지어 준 묶음을
		// 화면의 표식 하나가 무너뜨리는 것이다.
		mark := markProposed // Claimed=false, Rejection=nil → 아직 집기를 시도하지 않았다(추천 경로)
		switch {
		case m.Claimed:
			mark = markClaimed
		case m.Rejection != nil:
			mark = markRejected
		}
		fmt.Fprintf(&b, "\n  %s %s — %s [%s]\n", mark, m.Item.ID, m.Item.Title, m.Item.State)
		if m.Rejection != nil {
			fmt.Fprintf(&b, "    못 집었다: %-16s %s\n",
				m.Rejection.Reason, clip(m.Rejection.Detail, 160))
			b.WriteString("    이 항목 없이 나머지를 진행한다. " +
				"필요하면 그 세션에게 note(kind:\"ask\") 로 알려라\n")
			continue
		}
		// ★ 축이 비어도 근거가 비는 것은 아니다. pickBundle(item_ids 로 지정한
		// 묶음)이 만드는 Link 는 Axes 가 없다 — 판정 없이 세션이 그대로 지정했기
		// 때문이다(pick.go:427) — 그런데 Detail 은 "세션이 함께 지정했다"로 채워
		// 온다. len(Axes)>0 으로만 게이트를 걸면 그 경로(**item_ids 로 집는
		// 전체 경로**)의 구성원은 영원히 "왜 묶였나" 줄을 못 낸다.
		if len(m.Link.Axes) > 0 {
			axes := make([]string, 0, len(m.Link.Axes))
			for _, a := range m.Link.Axes {
				axes = append(axes, string(a))
			}
			fmt.Fprintf(&b, "    묶은 근거: [%s] %s\n", strings.Join(axes, " + "), m.Link.Detail)
		} else if strings.TrimSpace(m.Link.Detail) != "" {
			fmt.Fprintf(&b, "    묶은 근거: %s\n", m.Link.Detail)
		}
		if len(m.Item.Paths) > 0 {
			fmt.Fprintf(&b, "    경로: %s\n", strings.Join(m.Item.Paths, ", "))
		}
		b.WriteString(indent(strings.TrimRight(renderPathCheck(m.PathCheck, m.Item.ID), "\n"), "    ") + "\n")
		if len(m.Notes) > 0 {
			fmt.Fprintf(&b, "    연결된 판단 %d건 (전문):\n", len(m.Notes))
			for _, j := range m.Notes {
				fmt.Fprintf(&b, "      [%s] %s · %s\n", j.Kind,
					j.At.UTC().Format("2006-01-02 15:04"), clip(firstLine(j.Title, j.Body), 100))
				if strings.TrimSpace(j.Body) != "" {
					b.WriteString(indent(clip(j.Body, 4000), "        ") + "\n")
				}
			}
		}
	}
	if bi.Reason != "" {
		fmt.Fprintf(&b, "\n왜 이 묶음인가: %s\n", bi.Reason)
	}
	if bi.Scope != "" {
		fmt.Fprintf(&b, "묶음 범위: %s\n", bi.Scope)
	}
	return b.String()
}

func formatAfter(after []model.After) string {
	parts := make([]string, 0, len(after))
	for _, a := range after {
		switch {
		case a.Item != "":
			parts = append(parts, "item:"+a.Item)
		case a.SHA != "":
			parts = append(parts, "sha:"+a.SHA)
		case a.Job != "":
			parts = append(parts, "job:"+a.Job)
		default:
			parts = append(parts, "(빈 선행 — 스키마 CHECK 를 우회해 들어왔다)")
		}
	}
	return strings.Join(parts, " · ")
}

func indent(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

// renderPathCheck 는 경로 실재 축 한 줄이다. 순수 함수다.
//
// ★ **어느 갈래에서도 침묵하지 않는다.** 이상이 없어도 한 줄을 찍는 이유는
// RenderTail 이 겹침 0건일 때도 "겹침: 없음"을 찍는 것과 같다 — 침묵하면
// "이상 없다"와 "이 축을 안 봤다"가 같은 화면이 되고, 그러면 stat 이 전부 실패한 날에도
// pick 은 평소와 똑같아 보인다.
//
// ★ 접두가 "경로 실재:" 인 이유는 바로 위 줄이 이미 "경로: <목록>" 이기 때문이다.
// 같은 접두를 쓰면 선언과 관측이 안 갈린다.
//
// ★ 되돌리는 명령은 **유일 지목일 때만** 낸다. 여럿이 지목된 상태에서 그 명령을 내면
// 그것이 곧 오등록 단정이고, 그 단정이 실물 큐에서 5건 헛발화하던 규칙이다.
func renderPathCheck(v *judge.ItemPathVerdict, itemID string) string {
	if v == nil {
		return "경로 실재: 이 응답은 그 축을 읽지 않았다 — 낡은 캐시이거나 서버가 이 축을 모르는 판이다.\n"
	}
	s := "경로 실재: " + v.Summary + "\n"
	if v.Kind == judge.KindMisregistered && v.Suggest != "" {
		s += fmt.Sprintf("           맞다면 지금 되돌려라: `fd move %s --project %s`\n", itemID, v.Suggest)
	}
	return s
}

// ─────────────────────────────────────────────────────────────────────────────
// note · add · finish · alloc
// ─────────────────────────────────────────────────────────────────────────────

// RenderNote 는 판단 저장 확인과 이 노트를 읽을 세션 수다. 순수 함수다.
func RenderNote(r service.NoteResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "note · [%s] 저장했다 (판단 %s · %d자)\n",
		r.Judgment.Kind, r.Judgment.ID, len([]rune(r.Judgment.Body)))
	if r.Judgment.Supersedes != "" {
		fmt.Fprintf(&b, "정정: %s 를 대체한다 — 옛 행은 그대로 남는다(추가 전용)\n", r.Judgment.Supersedes)
	}
	if len(r.Recipients) == 0 {
		b.WriteString("지금 이 노트를 읽을 다른 세션이 없다 — 다음에 여는 세션이 board 에서 본다.\n")
		return b.String()
	}
	ids := make([]string, 0, len(r.Recipients))
	for _, id := range r.Recipients {
		ids = append(ids, ShortID(id))
	}
	fmt.Fprintf(&b, "지금 살아 있는 세션 %d건이 읽는다: %s\n", len(ids), strings.Join(ids, ", "))
	return b.String()
}

// RenderAdd 는 항목 등록 확인이다. 순수 함수다.
func RenderAdd(it model.Item) string {
	var b strings.Builder
	// ★ **어느 프로젝트에 들어갔는지를 여기서 말한다.**
	//
	// 오등록은 대부분 MCP 로 add 하다 난다 — 프로젝트 좌표를 세션의 cwd 가 정하므로
	// 워크트리에서 띄운 세션은 자기가 어디에 넣는지 모른 채 넣는다. 그런데 앞선 판의 이 문구는
	// 좌표를 한 글자도 안 냈고, 그래서 **틀린 순간에 화면에 아무 신호가 없었다.**
	//
	// 그 결과가 실물로 있다: 항목 10건이 남의 프로젝트에 등록돼 그 프로젝트에는 존재하지도
	// 않는 경로를 가리켰고, 그중 하나(fd-item-move)는 폐기됐는데 **id 가 전역 유일이라
	// 회수되지 않아 그 이름이 영구히 죽었다.**
	//
	// 되돌리는 길도 같은 줄에 적는다. MCP 표면에는 move 가 없고(설계 §6 이 도구 수를 일곱으로
	// 눌러 잡는다 — 컨텍스트 예산), 대신 §6 이 정한 방식이 이것이다:
	// **"규율은 응답에 싣는다 — 필요할 때만, 그 자리에서."**
	fmt.Fprintf(&b, "add · %s 를 **프로젝트 %s** 의 큐에 넣었다 [%s]\n", it.ID, it.Project, it.State)
	fmt.Fprintf(&b, "제목: %s\n", it.Title)
	if len(it.Paths) > 0 {
		fmt.Fprintf(&b, "경로 %d: %s\n", len(it.Paths), strings.Join(it.Paths, ", "))
	} else {
		b.WriteString("경로 0 — 경로가 없으면 이 항목은 겹침 축에 안 잡힌다.\n")
	}
	if len(it.After) > 0 {
		fmt.Fprintf(&b, "선행 %d: %s\n", len(it.After), formatAfter(it.After))
	}
	fmt.Fprintf(&b, "이 id 가 그대로 브랜치 이름이 된다: %s\n", it.ID)
	fmt.Fprintf(&b, "프로젝트가 %s 가 아니어야 한다면 지금 되돌려라: `fd move %s --project <맞는 것>`\n",
		it.Project, it.ID)
	return b.String()
}

// RenderFinish 는 마무리 결과다. 순수 함수다.
func RenderFinish(r service.FinishResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "finish · %s 를 %s 로 닫았다\n", r.Item.ID, r.Item.State)
	if r.Item.CloseReason != "" {
		fmt.Fprintf(&b, "폐기 사유: %s\n", clip(r.Item.CloseReason, 300))
	}
	fmt.Fprintf(&b, "판단 %s 저장 (%s · %d자)\n",
		r.Judgment.ID, r.Judgment.Kind, len([]rune(r.Judgment.Body)))
	if len(r.Followups) > 0 {
		ids := make([]string, 0, len(r.Followups))
		for _, f := range r.Followups {
			ids = append(ids, f.ID)
		}
		fmt.Fprintf(&b, "후속 %d건 등록: %s (판단과 FK 로 이어졌다)\n", len(ids), strings.Join(ids, ", "))
	} else {
		b.WriteString("후속 0건 — 이번에 나온 후속이 정말 없다면 그대로 두고, 있다면 지금 add 로 넣어라.\n")
	}
	// ★ 건너뛴 후속은 **반드시 낸다.** 안 내면 세션이 "후속 N건 등록"만 보고 떠나는데,
	// 그 id 의 항목은 남이 만든 다른 것이다 — 흡수가 조용한 거짓이 된다.
	if len(r.SkippedFollowups) > 0 {
		fmt.Fprintf(&b, "후속 %d건은 **안 넣었다** — 같은 id 가 이미 있다: %s\n",
			len(r.SkippedFollowups), strings.Join(r.SkippedFollowups, ", "))
		b.WriteString("  그 id 의 항목은 남이 만든 것일 수 있다. 내용이 다르면 다른 id 로 add 해라.\n")
	}
	if len(r.Released) > 0 {
		fmt.Fprintf(&b, "자원 반납: %s\n", strings.Join(r.Released, ", "))
	}
	// ★ 아직 쥔 항목을 **이름으로 부른다.** finish 는 항목 하나만 닫는데(항목마다
	// 자기 판단이 필요하다) pick 은 묶음을 집는다 — 그 비대칭 때문에 묶음 3건을 집은
	// 세션은 finish 한 번 뒤에도 2건을 쥔 채로 남는다. 지금까지 그 사실을 말하는
	// 표면이 하나도 없었고, schema.sql 에는 만료가 없고 세션이 닫혀도 선점이 안
	// 풀리므로 그 2건은 **사람이 강제로 풀 때까지 아무도 못 집는다.**
	// 여기서 침묵하면 그 상태가 만들어지는 것을 아무도 모른 채 지나간다.
	switch {
	case r.StillHeld == nil:
		// nil 은 "0건"이 아니다 — 조회가 실패했거나 서버 판이 이 축을 안 낸다.
		b.WriteString("이 세션이 아직 쥔 다른 항목이 있는지는 이 응답이 못 읽었다 — " +
			"`fd status` 로 확인해라(있는데 안 닫으면 아무도 그것을 못 집는다).\n")
	case len(*r.StillHeld) == 0:
		b.WriteString("이 세션이 쥔 항목은 이제 0건이다 — 남은 선점이 없다.\n")
	default:
		fmt.Fprintf(&b, "★ 이 세션이 **아직 쥐고 있는** 항목 %d건: %s\n",
			len(*r.StillHeld), strings.Join(*r.StillHeld, ", "))
		b.WriteString("  finish 는 항목 하나만 닫는다 — 항목마다 자기 판단이 필요하기 때문이다. " +
			"위 항목들은 여전히 이 세션의 선점이라 **남이 못 집는다.** " +
			"각각 finish 로 닫거나, 안 할 거면 그 판단을 남기고 닫아라.\n")
	}
	// 문장이 조건부인 이유: 중복 id 후속은 트랜잭션 밖으로 빠졌다(finish.go 의 ② 주석).
	// 넷이 한 트랜잭션이라고 그대로 적으면 그 응답에서만 거짓이 된다.
	if len(r.SkippedFollowups) > 0 {
		b.WriteString("판단 저장·종료·자원 반납은 한 트랜잭션이었다 — " +
			"위 후속만 빠졌고, 그것이 판단을 지킨 값이다.\n")
	} else {
		b.WriteString("판단 저장·후속 등록·종료·자원 반납이 한 트랜잭션이었다 — 검산할 순서가 없다.\n")
	}
	return b.String()
}

// RenderAlloc 은 발번 결과다. 순수 함수다.
func RenderAlloc(counter string, n int64) string {
	return fmt.Sprintf("alloc · %s = %d\n"+
		"원자 발번이라 같은 번호가 두 번 나오지 않는다 — 락으로는 못 지키던 자리다.\n", counter, n)
}

// ─────────────────────────────────────────────────────────────────────────────
// 꼬리 — 미확인 알림과 겹침은 **모든** 응답에 붙는다
// ─────────────────────────────────────────────────────────────────────────────

// TailInput 은 응답 꼬리에 실을 사실이다.
//
// Observed 축이 둘 다 따로 있는 이유: "겹침 0건"과 "이 도구는 경로 축을 안 읽었다"는
// 다른 사실이다. 뭉개면 도구가 자기가 무엇을 안 보는지 모르는 채 초록불을 내게 된다 —
// 이 제품이 겨냥하는 뿌리 원인 중 하나가 정확히 그것이다.
type TailInput struct {
	Banner           string
	Now              time.Time
	Notes            []model.Judgment
	NotesObserved    bool
	NotesError       string
	Overlaps         []judge.Overlap
	OverlapsObserved bool
	OverlapsNote     string // 안 읽었으면 왜 안 읽었나
}

// tailOverlapLimit 은 꼬리가 **줄을 내는** 겹침 세션 수다. 건수는 머리줄이 전부 센다.
//
// ★ 안쪽 차원은 이미 잘리고 있었는데(겹침 한 건 안의 경로쌍 4개) **바깥 차원인 겹침 건수에는
// 상한이 없었다.** 꼬리는 모든 응답에 붙고 board 에서는 고정분이라, 겹침 줄이 늘면 카드가
// 그만큼 밀려난다. 즉 꼬리가 살아 있는 세션 수에 O(N) 으로 자라는데 예산은 상수다.
// 실측(2026-08-05): 겹침 16줄일 때 788토큰 = 예산 1200 의 66%.
//
// ★★ 알림 쪽에 같은 상한을 **여기 두지 않는다.** 그 축은 tailNoteLimit(mcpsrv.go)이 이미
// 쥐고 있다 — 같은 판정을 두 자리에 두면 반드시 표류한다(이 패키지가 워크트리·머신·프로젝트
// 축에서 세 번 겪고 세 번 다 주입으로 고친 그 사고다).
const tailOverlapLimit = 5

// RenderTail 은 응답 꼬리를 만든다. 순수 함수다.
func RenderTail(in TailInput) string {
	var lines []string
	lines = append(lines, "── 꼬리 ──")

	switch {
	case in.NotesError != "":
		lines = append(lines, "알림: 못 읽었다 — "+clip(in.NotesError, 200))
	case !in.NotesObserved:
		lines = append(lines, "알림: 이 응답은 알림 축을 읽지 않았다.")
	case len(in.Notes) == 0:
		lines = append(lines, "알림: 다른 세션이 남긴 ask·blocked 가 없다.")
	default:
		lines = append(lines, fmt.Sprintf(
			"알림 %d건 (Tier A 에는 확인 원장이 없다 — '미확인'을 '최근'으로 근사한다)", len(in.Notes)))
		for _, j := range in.Notes {
			lines = append(lines, fmt.Sprintf("  · [%s] %s %s 전 — %s",
				j.Kind, ShortID(j.SessionID), FormatAge(in.Now.Sub(j.At)),
				clip(firstLine(j.Title, j.Body), 140)))
		}
	}

	switch {
	case !in.OverlapsObserved:
		note := in.OverlapsNote
		if note == "" {
			note = "이 도구는 경로 축을 읽지 않았다 — board 나 pick 이 그 축을 읽는다"
		}
		lines = append(lines, "겹침: "+note)
	case len(in.Overlaps) == 0:
		lines = append(lines, "겹침: 없음 — 살아 있는 세션 어느 것과도 경로가 안 겹친다.")
	default:
		lines = append(lines, fmt.Sprintf("겹침 %d건 (거르지 않고 알린다):", len(in.Overlaps)))
		for i, o := range in.Overlaps {
			if i >= tailOverlapLimit {
				lines = append(lines, fmt.Sprintf(
					"  · … %d건 더 — 수는 위 머리줄이 전부 센 값이다. 이름까지는 board 가 낸다",
					len(in.Overlaps)-tailOverlapLimit))
				break
			}
			pairs := make([]string, 0, len(o.Pairs))
			for i, p := range o.Pairs {
				if i >= 4 {
					pairs = append(pairs, fmt.Sprintf("+%d", len(o.Pairs)-4))
					break
				}
				pairs = append(pairs, fmt.Sprintf("%s↔%s", p[0], p[1]))
			}
			label := o.Label
			if label == "" {
				label = "(꼬리표 없음)"
			}
			lines = append(lines, fmt.Sprintf("  · %s %s: %s",
				ShortID(o.SessionID), label, strings.Join(pairs, ", ")))
		}
	}

	if strings.TrimSpace(in.Banner) != "" {
		lines = append(lines, in.Banner)
	}
	return strings.Join(lines, "\n")
}

// SortJudgmentsNewest 는 판단을 최신순으로 정렬한 사본을 낸다. 순수 함수다.
func SortJudgmentsNewest(js []model.Judgment) []model.Judgment {
	out := append([]model.Judgment(nil), js...)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// 거절
// ─────────────────────────────────────────────────────────────────────────────

// RenderRefusal 은 거절 하나를 사람이 읽는 텍스트로 만든다. 순수 함수다.
//
// ★ **처방을 함께 낸다.** 사유만 주고 처방을 안 주면 에이전트는 무엇을 고쳐야 하는지
// 모른 채 같은 호출을 반복한다. finish 를 body 없이 부른 세션이 여기서
// "무엇을 적어야 하는가 넷"을 받는다 — 규율 산문을 도구 설명이 아니라
// **필요할 때 그 자리에서** 싣는다는 것이 이 자리의 전부다.
func RenderRefusal(what, reason, guidance string) string {
	s := fmt.Sprintf("%s 거절: %s", what, reason)
	if strings.TrimSpace(guidance) != "" {
		s += "\n" + guidance
	}
	return s
}

// ─────────────────────────────────────────────────────────────────────────────
// land
// ─────────────────────────────────────────────────────────────────────────────

// RenderLand 는 land 한 번의 결과다. 순수 함수다.
//
// 세 갈래가 뼈대다: **네 차례다**(turn) · **너는 N번째다**(waiting — 앞사람 세션·획득 경과·
// 마지막 신호 나이) · **이 레인은 네 것이 아니다**(reclaimed — 사유가 무엇이 일어났는지 말한다).
// report·leave 의 확인(released·left)은 그보다 단순해서 한 줄이다.
//
// ★ **개정 — 통로가 서서 옛 금지가 만료됐다.** 원래 이 자리는 "**lane-turn 처방을
// 언급하지 않는다.** 레인이 넘어갈 때 알림을 미는 통로는 아직 없다(설계 단계 ③ 이 그것을
// 만든다) — 없는 통로를 가리키는 문구는 이 레포가 결함으로 분류하는 부류다. waiting 응답이
// 낼 수 있는 유일한 처방은 '다시 물어라'(폴링)뿐이다." 였다. judge.PrescribeLaneTurn 이
// 들어오면서 그 금지의 근거가 사라졌고, 그러자 waiting 꼬리에 있던
// "차례는 서버가 밀어주지 않는다"가 **거짓이 됐다** — 그래서 추가가 아니라 교체다.
//
// ★ **허용은 waiting 하나뿐이다.** 나머지 넷에서는 여전히 금지다 — 근거가 "통로가 없다"에서
// "그 자리에서 할 말이 아니다"로 바뀌었을 뿐이다. turn 은 이미 쥐었고, released·left 는 줄을
// 떠났고, reclaimed 의 일은 잘못된 믿음을 고치는 것이라 사유가 처방을 겸한다(아래 ★ 참고 —
// 거기에 통지 이야기를 얹으면 머리글과 사유가 다투는 그 결함이 되돌아온다).
// 이 갈림을 잠근 것은 TestRenderLandWaitingPointsAtLaneTurn 이다.
func RenderLand(r service.LandResult, now time.Time) string {
	var b strings.Builder
	switch r.State {
	case "turn":
		fmt.Fprintf(&b, "land · 네 차례다 — 레인을 쥐었다 (줄 행 %d)\n", r.RowID)
		b.WriteString("다 쓰면 result 로 보고하고 반납해라. 줄 서 놓고 그만두려면 leave 를 써라.\n")

	case "waiting":
		fmt.Fprintf(&b, "land · 너는 %d번째다 (줄 행 %d)\n", r.Position, r.RowID)
		if r.Holder == nil {
			b.WriteString("지금 레인을 쥔 사람이 없다 — 앞사람이 아직 land 를 안 불렀다.\n")
		} else {
			fmt.Fprintf(&b, "지금 레인: %s · 획득 %s 전",
				ShortID(r.Holder.SessionID), FormatAge(now.Sub(r.Holder.AcquiredAt)))
			if r.Holder.LastSignalAt != nil {
				fmt.Fprintf(&b, " · 마지막 신호 %s 전\n", FormatAge(now.Sub(*r.Holder.LastSignalAt)))
			} else {
				b.WriteString(" · 마지막 신호 없음\n")
			}
		}
		// ★ **교체다, 추가가 아니다.** 여기 있던 문장은 "차례는 서버가 밀어주지 않는다 —
		// 다시 물으려면 land 를 다시 불러라." 였다. 통로가 서면서 그 첫 절이 거짓이 됐다.
		//
		// ★ 그래도 "이제 가만히 있어도 된다"로는 쓰지 않는다. 처방은 (세션 × 키) 1회고 키에
		// 줄 행 번호가 실려 있어(judge.PrescribeInput.LaneTurnRow 주석), 접히거나 훅이 못 돌면
		// 이 줄 행의 차례 통지는 **영구히 사라진다** — 그 실패는 화면에 안 뜬다. 폴링을 닫는
		// 문장을 쓰면 그 세션은 자기 차례를 영영 모른 채 서 있고 뒤 줄 전원이 그만큼 선다.
		// 그래서 두 문장이다: 하나는 밀어 온다는 사실, 하나는 그것이 한 번뿐이라 두 번째 길이
		// 그대로 남아 있다는 사실. 키 이름을 본문에 박는 이유는 세션이 실제로 받는 처방과
		// 이 문장을 이어 읽어야 하기 때문이다.
		b.WriteString("차례가 오면 처방이 턴 끝에 온다(Stop 훅이 끌어간다) — 키는 lane-turn 이고 그 줄 행 앞으로 한 번뿐이다.\n")
		b.WriteString("한 번 지나가면 같은 줄 행에는 다시 안 온다 — land 를 다시 불러 묻는 길은 그대로 열려 있다.\n")

	case "released":
		fmt.Fprintf(&b, "land · 보고하고 레인을 반납했다 (줄 행 %d)\n", r.RowID)

	case "left":
		fmt.Fprintf(&b, "land · 줄에서 빠졌다 (줄 행 %d)\n", r.RowID)

	case "reclaimed":
		// ★ 머리글이 사유를 앞지르지 않는다. service.laneNotMine 은 "내가 점유자가 아니다"
		// **전부**를 이 한 낱말로 접는데 도달 갈래가 셋이다: 진짜 회수됨(left_detail) ·
		// 아직 대기 중인 세션의 보고("레인을 쥔 적이 없다 …") · 줄에 선 적조차 없는 세션
		// ("이 프로젝트 줄에 선 기록이 없다"). 머리글에 "회수됐다"를 박으면 뒤의 둘에서
		// **한 문장 안에서 회수됐다와 쥔 적이 없다가 정면 충돌한다** — 사용자에게 나가는
		// 거짓 문장이다. 그래서 머리글은 세 갈래 모두에 참인 것만 말하고(네 것이 아니다)
		// **무엇이 일어났나는 사유가 말한다**(laneLeftReason 이 절대 빈 문자열을 안 내므로
		// 이 자리가 비는 경우는 없다). State 어휘 다섯과 LandExitCode 표는 그대로다.
		fmt.Fprintf(&b, "land · 이 레인은 네 것이 아니다 — %s\n", r.Reason)

	default:
		// KnownTool 이 표와 디스패치를 지키듯, 여기는 service.LandResult.State 다섯 낱말과
		// 이 switch 가 어긋나지 않는다는 전제 위에 있다. 어긋나면 침묵하지 않고 값을 그대로 보인다.
		fmt.Fprintf(&b, "land · 이 서버가 모르는 상태 %q 다 — 서버 결함이다 (줄 행 %d)\n", r.State, r.RowID)
	}
	return b.String()
}
