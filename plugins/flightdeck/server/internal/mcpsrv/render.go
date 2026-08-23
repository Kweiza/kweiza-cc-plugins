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
//
// ★ **머리줄의 N 은 축 수이고 아래 줄 수와 다를 수 있다.** 원인이 하나인 쌍을 한 줄로
// 접기 때문이다(foldTwinFailures). 축 수를 줄이지 않는 이유는 그 수가 "몇 개를 못 봤나"에
// 답하는 유일한 자리여서다 — 접기가 그것까지 먹으면 부재가 조용해진다.
func renderFailures(d service.Derived, limit int) []string {
	if len(d.Failures) == 0 {
		return nil
	}
	rows := foldTwinFailures(d.Failures)
	out := []string{fmt.Sprintf("못 읽은 파생 %d축:", len(d.Failures))}
	for i, r := range rows {
		if limit > 0 && i >= limit {
			out = append(out, fmt.Sprintf("  … %d줄 더", len(rows)-limit))
			break
		}
		out = append(out, "  · "+r)
	}
	return out
}

// foldTwinFailures 는 **한 원인이 낸 실패 둘**을 한 줄로 접는다. 순수 함수다.
//
// 죽은 워크트리 경로 하나가 `uncommitted:<세션>` 과 `uncommitted-delta:<세션>` 을 **함께**
// 실패시킨다(service/board.go 의 두 호출이 같은 `Session.Worktree` 를 본다). 원인은 하나인데
// 화면이 같은 말을 두 번 하고, dangling 워크트리가 이미 수십 개라 창 안에 여럿 들어오면
// 그 값이 배로 든다. 그래서 화면에서만 접는다 — **다른 소비자(웹 패널·Derived 원본)는
// 축을 그대로 받는다.** 원장과 `/metrics` 는 이 축을 애초에 안 본다(2026-08-12 실측 —
// event.kind 전수(33종)에 파생 축이 없고 /metrics 는 runs·cards·seconds 뿐이다).
//
// ★ **접기 조건이 좁은 것이 이 함수의 요점이다.** 셋을 다 만족할 때만 접는다:
// 같은 세션 · 같은 경로 · 둘 다 실패. 한쪽만 실패한 경우를 접으면 "규모만 죽었다"와
// "둘 다 죽었다"가 같은 화면이 되고, 그러면 두 축을 별개 git 호출로 갈라 세운 이유가
// (규모를 못 읽어도 경로 축이 산다 — gitreader.UncommittedDelta 독스트링 ②) 화면에서 사라진다.
//
// ★ **원인 꼬리가 같을 때만 접는다.** `gitreader.CommandError` 가 원인을
// `git <args>: status <n>: <stderr>` 로 적으므로 `: status ` 부터가 명령줄을 뺀 꼬리다.
// 2026-08-12 실측: 죽은 경로에 대해 `status --porcelain` 과 `diff --numstat` 의 stderr 는
// **글자 그대로 같다**(`fatal: cannot change to '…': No such file or directory`) — 다른 것은
// 하위 명령뿐이고 그것은 축 이름이 이미 말한다. 형식이 바뀌면 꼬리를 못 뽑아 **안 접힌다** —
// 뭉개는 쪽이 아니라 갈라 내는 쪽으로 넘어진다.
//
// ★ 접은 줄은 앞의 `(경로)` 를 **빼고** 꼬리에 든 경로를 쓴다. 넣으면 한 줄에 경로가 두 번
// 들어가 `clip(…, 200)` 이 원인을 통째로 먹는다 — 접기 전 화면이 정확히 그 상태였다
// (실측: 두 줄 다 stderr 가 안 보이고 `status --porce…` 에서 잘렸다).
func foldTwinFailures(fs []service.DerivedFailure) []string {
	const (
		pathAxis  = "uncommitted:"
		deltaAxis = "uncommitted-delta:"
	)
	// ★ 판정을 먼저 다 끝내고 나서 낸다. 한 번에 훑으면 두 축이 오는 **순서**에 결과가
	//   달라진다 — 규모 축이 먼저 오면 그 줄을 이미 내보낸 뒤에 접기가 결정돼 세 줄이 된다.
	paths, deltas := map[string]string{}, map[string]string{}
	for _, f := range fs {
		if sess, ok := strings.CutPrefix(f.Axis, deltaAxis); ok {
			deltas[sess] = f.Detail
		} else if sess, ok := strings.CutPrefix(f.Axis, pathAxis); ok {
			paths[sess] = f.Detail
		}
	}
	tails := map[string]string{} // 접을 세션 → 공통 원인 꼬리
	for sess, pd := range paths {
		if tail, ok := twinTail(pd, deltas[sess]); ok {
			tails[sess] = tail
		}
	}

	out := make([]string, 0, len(fs))
	done := map[string]bool{}
	for _, f := range fs {
		sess, ok := strings.CutPrefix(f.Axis, deltaAxis)
		if !ok {
			sess, ok = strings.CutPrefix(f.Axis, pathAxis)
		}
		if tail, folds := tails[sess]; ok && folds {
			if done[sess] {
				continue // 이미 접힌 쌍의 짝이다
			}
			done[sess] = true
			out = append(out, fmt.Sprintf("uncommitted+delta:%s — 미커밋 경로·규모 둘 다 관측 실패: %s",
				sess, clip(tail, 200)))
			continue
		}
		out = append(out, fmt.Sprintf("%s — %s", f.Axis, clip(f.Detail, 200)))
	}
	return out
}

// twinTail 은 두 원인이 **한 원인**인지 판정하고, 맞으면 공통 꼬리를 낸다.
// 경로가 다르거나 꼬리가 다르거나 한쪽이 없으면 접지 않는다.
func twinTail(pathDetail, deltaDetail string) (string, bool) {
	if pathDetail == "" || deltaDetail == "" {
		return "", false // 한쪽만 실패했다 — 갈라서 내야 하는 바로 그 경우다
	}
	p1, t1 := failurePathAndTail(pathDetail)
	p2, t2 := failurePathAndTail(deltaDetail)
	if p1 == "" || t1 == "" || p1 != p2 || t1 != t2 {
		return "", false
	}
	return t1, true
}

// failurePathAndTail 은 파생 실패 원인에서 **관측 대상 경로**와 **명령줄을 뺀 원인 꼬리**를
// 가른다. 둘 중 하나라도 못 뽑으면 빈 문자열을 내고, 부르는 쪽은 그때 접지 않는다.
//
// 형식: `미커밋 … 관측 실패(<경로>): git -C <경로> <하위명령>: status <n>: <stderr>`
// (gitreader 의 `fmt.Errorf("…(%s): %w", worktree, err)` + CommandError.Error()).
func failurePathAndTail(detail string) (path, tail string) {
	if open := strings.Index(detail, "("); open >= 0 {
		if close := strings.Index(detail[open:], "): "); close > 0 {
			path = detail[open+1 : open+close]
		}
	}
	if i := strings.Index(detail, ": status "); i >= 0 {
		tail = detail[i+2:]
	}
	return path, tail
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
	// git 호출 2~5회가 세션 수만큼 터진다). 그 사실을 줄머리가 말한다.
	for _, ov := range v.OutsideClaims {
		_, act := activityOf(ov.Signals, now)
		foot = append(foot, fmt.Sprintf("창 밖 선점 · %s %s · %s · 파생 안 읽음",
			ShortID(ov.Session.ID), strings.Join(ov.Claims, ", "), act))
	}
	// ★ 회수 손잡이는 창 밖 선점이 **있을 때만** 낸다(상시 점등은 판별력 0 — 설계 §4).
	// 표시만으로는 재고가 안 돈다: 실측(2026-08-07)에서 무신호 9~24.5h 세션 4곳이
	// 12건(잔량의 최대 27%)을 쥐고 있었는데, 진단 줄만 있고 처방이 없어 아무도 안 풀었다.
	// 자동 만료는 생존 오판 실측 2회로 기각된 설계다(schema.sql) — 판정은 사람이 한다.
	// 문구에 생존 낱말("죽…")을 안 쓴다 — §4 와 render_test 의 낱말 가드가 보드 표면에서
	// 그 낱말을 통째로 금지한다. 생사 판정은 이 화면이 아니라 회수하는 사람의 몫이다.
	if len(v.OutsideClaims) > 0 {
		foot = append(foot, "  위 선점은 자동으로 안 풀린다 — 신호 나이를 보고 판정한 사람이 푼다: "+
			"fd claim release --item <id> --reason \"...\" (사유는 판단으로 원장에 남는다)")
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
	// (service.BoardView.Lane 주석과 같은 판정). Task 6 부터는 자원별로 갈려 **한 줄이 아니라
	// 자원 수만큼**(0건이면 한 줄) 나온다 — foot 은 줄마다 하나씩 이어 붙이는 슬라이스라
	// append(...,) 로 펼친다.
	if v.Lane != nil {
		foot = append(foot, renderLane(v.Lane, now, opt.Detail)...)
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
	return joinTail(strings.Join(parts, "\n\n"), tail)
}

// joinTail 은 본문에 꼬리를 잇는 **유일한 자리**다. 순수 함수다.
//
// ★ 왜 함수 하나로 두나. 이 조립이 일어나는 곳이 셋이었다 — `Server.withTail`(도구 대부분) ·
// 이 `joinAll`(board) · `toolPick` 의 두 갈래. 셋이 같은 규율("본문 · 빈 줄 하나 · 꼬리")을
// 손으로 각각 적고 있었고, 그래서 둘이 조용했다:
//
//	① **규율이 갈려도 아무도 모른다.** 미회계 갈래는 개행을 하나만 적는데 바로 앞의
//	   RenderBundleUnaccounted 가 개행으로 끝나서 **우연히** 맞고 있었다. 그 함수의 끝
//	   개행을 지우는 순간 그 응답만 꼬리 앞 빈 줄이 사라진다(변이로 확인했다).
//	② **변이가 한 자리만 덮는다.** fd-mcp-render-assertions-may-be-response-wide 에서
//	   "본문을 비우면 어떤 시험이 살아남나"를 잴 때 withTail 에만 변이를 넣었더니
//	   board·pick 시험 다섯이 "본문 없이 통과"로 **잘못 보였다** — 본문이 비어서가 아니라
//	   변이가 그 경로에 안 닿았을 뿐이었다. 하마터면 없는 결함 다섯을 고치러 갔다.
//
// 꼬리가 비면 본문만 낸다 — `Tail` 옵션 없이 `RenderBoard` 를 부르는 순수 함수 시험이
// 그 갈래이고, joinAll 이 갖고 있던 거동을 그대로 옮겼다.
func joinTail(body, tail string) string {
	body = strings.TrimRight(body, "\n")
	if strings.TrimSpace(tail) == "" {
		return body
	}
	return body + "\n\n" + tail
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

// queueAgeClause 는 큐 줄에 붙는 나이 절이다 — "(최고령 1일 6시간 · 1일+ 2건)".
//
// ★ 이 절이 없으면 화석이 안 보인다. 보드는 이미 최고령부터 냈다
// (store/item.go 의 ORDER BY created_at). 그런데 나이를 안 찍어서 "26시간째 아무도
// 안 집었다"가 화면 어디에도 없었고, 세션은 그 줄을 그냥 "큐에 있는 항목 셋"으로
// 읽고 지나갔다(판단 01KZAW342JAC6EAW8C31RCXXK0 의 실측).
//
// ★ 순서를 **가정하지 않는다.** OpenItems 가 나이순으로 온다는 것은 지금 저장 계층의
// 성질이고, 여기서 [0] 을 최고령으로 단정하면 그 성질이 바뀌는 날 조용히 틀린다.
// 전부 훑는 비용은 큐 길이에 선형이고 큐는 수십 건이다.
//
// v.At 이 zero 거나 CreatedAt 이 zero 인 항목은 **안 센다** — 관측을 못 한 것을
// 값으로 접지 않는다. 전부 못 읽으면 절 자체가 안 나간다.
func queueAgeClause(v service.BoardView) string {
	if len(v.OpenItems) == 0 || v.At.IsZero() {
		return ""
	}
	var oldest time.Duration
	starved := 0
	for _, it := range v.OpenItems {
		if it.CreatedAt.IsZero() {
			continue
		}
		// 티클러는 굶김 축(최고령·굶은 건수)에서 뺀다 — 기한까지 늙는 것이 정상이라
		// 넣으면 이 절이 상시 점등돼 판별력이 0이 된다(§4). 건수에는 그대로 든다.
		if judge.IsTickler(it.Labels) {
			continue
		}
		if age := v.At.Sub(it.CreatedAt); age > oldest {
			oldest = age
		}
		if v.At.Sub(it.CreatedAt) >= judge.StarvationAge {
			starved++
		}
	}
	if oldest <= 0 {
		return ""
	}
	s := "(최고령 " + FormatAge(oldest)
	// 굶은 것이 0건이면 그 절을 안 낸다 — 상시 점등된 경고는 판별력이 0이 된다.
	if starved > 0 {
		s += fmt.Sprintf(" · %s+ %d건", FormatAge(judge.StarvationAge), starved)
	}
	return s + ")"
}

// ticklerMark 는 티클러 표식이다 — `티클러` · `티클러(08-26 발화)` ·
// `티클러(08-26 발화·지났다)`. 티클러가 아니면 빈 문자열이다.
//
// ★ board 줄(queueItemAge)과 pick 줄(renderTicklerLine)이 **이 하나를 공유한다.**
// 같은 사실에 두 번째 표기를 붙이면 읽는 쪽이 두 지표로 읽고, 두 수가 갈려도
// "다른 값이겠지"로 넘어가 불일치가 조용히 정상으로 등록된다 — RenderPick 이 큐 열림
// 줄에 대해 이미 적어 둔 경고와 같은 이유다.
//
// ★ **표시뿐이다.** 기한이 지나도 승격시키지 않고 아무것도 안 막는다(judge/tickler.go
// 의 ★ 둘 · 설계 §5·§8). 이 함수를 판정에 쓰면 표시-전용 규약이 조용히 넓어진다.
func ticklerMark(at time.Time, labels []string) string {
	if !judge.IsTickler(labels) {
		return ""
	}
	out := "티클러"
	if on, ok := judge.FiresOn(labels); ok {
		out += "(" + on.Format("01-02") + " 발화"
		if ticklerDue(at, on) {
			out += "·지났다"
		}
		out += ")"
	}
	return out
}

// ticklerDue 는 기한이 왔는가다. 기준 시각을 모르면(zero) **단정하지 않는다** —
// 모르는 것을 "지났다"로 메우면 부재를 값으로 접는 것이고, 그 방향의 오답이 더 비싸다
// (기한 전 항목을 열게 만든다).
func ticklerDue(at, on time.Time) bool {
	return !at.IsZero() && !at.Before(on)
}

// renderTicklerLine 은 pick 응답의 티클러 줄이다. 티클러가 아니면 빈 문자열이다 —
// 상시 점등된 줄은 판별력이 0이 된다(§4).
//
// ★ 왜 pick 에도 있어야 하나. 2026-08-23 에 한 세션이 통째로 헛일했다. 08-21 세션이
// 한 항목에 `tickler` 를 달아 굶김 축에서 뺐는데, 이틀 뒤 pick 이 그것을 후보 7건 중
// 1순위로 추천하면서 티클러라는 말을 한 마디도 안 했다. 그 세션은 집어서 같은 SQL 을
// 돌리고 같은 미충족을 봤다 — `judge.FiresOn` 이 막으려고 만들어진 2026-08-12 사건
// (세 시간 반에 네 세션이 같은 재측)의 재연이다. 축을 만들어 놓고 **픽업 경로에
// 안 꽂았던 것**이다: 유일한 발화처인 queueItemAge 는 boardDetailFoot 에서만 불리는데
// 픽업 절차는 `board`(기본) → `pick` 이라 그 화면을 지나지 않는다.
//
// ★ 기한 **없는** 티클러가 그 사고의 실물이라, 그 경우에 침묵하지 않는다. 없는 날짜를
// 지어내지는 않되 없다는 사실은 말한다 — 침묵하면 화면에서 티클러가 아닌 것과 구별되지
// 않고, 그것이 바로 08-23 에 일어난 일이다.
func renderTicklerLine(at time.Time, labels []string, indent string) string {
	mark := ticklerMark(at, labels)
	if mark == "" {
		return ""
	}
	on, dated := judge.FiresOn(labels)
	switch {
	case !dated:
		return indent + mark + " — 발화일(`fires:`)이 없다. 언제 여는지가 화면에 없으면 " +
			"볼 때마다 원장을 다시 재게 된다 — 걸린 판단을 읽고 `label` 로 기한을 박아라\n"
	case ticklerDue(at, on):
		return indent + mark + " — 열 때가 됐다\n"
	default:
		return indent + mark + " — 아직 기한 전이다. 굶김 축에서 빠져 있어 " +
			"나이가 길어도 잊힌 항목이 아니다\n"
	}
}

// queueItemAge 는 detail 줄 앞머리의 항목 나이다. 임계를 넘긴 것은 ★ 를 단다.
//
// 못 재면 "나이?" 를 낸다 — 0 이나 빈 문자열로 접으면 "방금 생겼다"로 읽힌다.
func queueItemAge(at time.Time, it model.Item) string {
	if at.IsZero() || it.CreatedAt.IsZero() {
		return "나이?"
	}
	age := at.Sub(it.CreatedAt)
	// 티클러는 ★ 를 안 단다 — 대신 그 사실을 이름으로 낸다. 표식 없는 긴 나이가
	// "잊힌 항목"으로 읽히면, 굶김 축에서 뺀 것이 침묵으로 바뀐다.
	//
	// ★ 그리고 **기한을 같이 낸다**(있으면). 나이만 있는 티클러는 뜻이 없다 — 언제
	// 열리는지가 화면에 없으면 볼 때마다 원장을 다시 재게 된다. 2026-08-12 에 한 항목을
	// 두고 세 시간 반에 네 세션이 같은 재측을 돌렸고, 앞 세션이 "아직 아니다"를 판단으로
	// 남겼는데도 그랬다. 지난 기한은 그 사실까지 말한다 — 안 지난 것과 같아 보이면
	// 기한이 와도 아무도 안 연다.
	//
	// 여기는 **표시뿐이다.** 기한이 지나도 승격시키지 않고 아무것도 안 막는다
	// (judge.FiresOn 의 ★ 참조).
	if mark := ticklerMark(at, it.Labels); mark != "" {
		return FormatAge(age) + "·" + mark
	}
	if age >= judge.StarvationAge {
		return "★" + FormatAge(age)
	}
	return FormatAge(age)
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
		line := fmt.Sprintf("큐 열림 %d건%s: %s", len(v.OpenItems), queueAgeClause(v), strings.Join(ids, ", "))
		if len(v.OpenItems) > 3 {
			line += fmt.Sprintf(" +%d", len(v.OpenItems)-3)
		}
		out = append(out, line)
	} else {
		out = append(out, "큐 열림 0건")
	}
	out = append(out, "자원 점유: "+heldOrNoneLine(v.Held))
	return out
}

func boardDetailFoot(v service.BoardView) []string {
	var out []string
	out = append(out, fmt.Sprintf("큐 열림 %d건%s", len(v.OpenItems), queueAgeClause(v)))
	for _, it := range v.OpenItems {
		line := fmt.Sprintf("  · %s · %s — %s", queueItemAge(v.At, it), it.ID, clip(it.Title, 90))
		if len(it.Paths) > 0 {
			line += " [" + strings.Join(it.Paths, ", ") + "]"
		}
		out = append(out, line)
	}
	out = append(out, "자원 점유: "+heldOrNoneLine(v.Held))

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
	// ★ 두 구간을 **각각 자기 이름과 함께** 낸다. 라벨 없이 세 수만 있으면 다음 사람이
	//   그것을 "지금 값"으로 읽는데, 전 역사 쪽은 분모가 단조 증가라 그렇게 읽으면 틀린다.
	//   구간 폭은 값(r.Window)에서 가져온다 — 문장에 24시간을 박으면 상수가 바뀌는 날
	//   문구만 조용히 낡는다(위 창 문구와 같은 규율).
	// ★ 게이트는 전 역사 Emitted 다. 최근이 0인 것은 침묵할 사실이 아니라 "이 구간에
	//   처방이 없었다"는 사실이라 0 그대로 낸다 — 안 나온 것과 못 잰 것을 가르는 자리다.
	if r := v.AckReach; r != nil && r.AllTime.Emitted > 0 {
		out = append(out, fmt.Sprintf(
			"확인율 최근 %s — 발화 대화 %d · 그중 ack 이 닿을 수 있는 대화 %d · 실제 ack %d "+
				"(앞 두 수가 크게 다르면 처방을 받고 판단을 안 남긴 대화가 그만큼이다)",
			FormatAge(r.Window), r.Recent.Emitted, r.Recent.Reachable, r.Recent.Acked))
		out = append(out, fmt.Sprintf(
			"확인율 전 역사 — 발화 대화 %d · 그중 ack 이 닿을 수 있는 대화 %d · 실제 ack %d "+
				"(분모가 단조 증가한다 — 추세로만 읽어라)",
			r.AllTime.Emitted, r.AllTime.Reachable, r.AllTime.Acked))
	}
	return out
}

// renderLane 은 보드의 레인 절이다 — Task 6 부터는 **자원별로** 한 줄씩 낸다. 순수 함수다.
//
// 설계 §9 ① 이 요구하는 축을 전부 낸다: **점유자의 획득 경과**(머리) · 대기 줄 전체
// (순번 · 세션 · 대기 경과 · **마지막 신호 나이**). 회수는 자동 만료가 아니라 사람이
// 이 두 나이를 보고 내리는 판정이라, 그 주 표면인 보드에서 빠지면 판정의 근거가 없다.
//
// laneResourceCap 은 renderLane 이 brief 모드에서 내는 자원 절의 상한이다.
//
// ★ 리뷰 Important #3(2026-08-12, 실측 재현) — 자원별 레인 절은 이 함수가 생기기 전에는
// 상한이 없었고, 그 절은 joinAll 의 foot(고정분)이라 board 예산 루프가 **못 자른다**
// (RenderBoard 의 카드 자르기는 blocks 만 본다). 실측: 자원 6개짜리 레인 절 하나가 1422토큰
// (예산 1200)을 혼자 먹었다 — 카드가 하나도 없어도 고정분만으로 예산을 넘긴다.
// 값 4는 조율값이다: 회수 판정에 절실한 것은 "지금 막힌 자원"이지 "이 프로젝트가 쓰는
// 자원 전부"가 아니고, 어긋남(⚠) 자원은 이 상한과 무관하게 전부 낸다(아래 참고).
const laneResourceCap = 4

// renderLane 은 보드의 레인 절이다 — Task 6 부터는 **자원별로** 한 줄씩 낸다. 순수 함수다.
//
// 설계 §9 ① 이 요구하는 축을 전부 낸다: **점유자의 획득 경과**(머리) · 대기 줄 전체
// (순번 · 세션 · 대기 경과 · **마지막 신호 나이**). 회수는 자동 만료가 아니라 사람이
// 이 두 나이를 보고 내리는 판정이라, 그 주 표면인 보드에서 빠지면 판정의 근거가 없다.
//
// ★ 호출부(RenderBoard)가 v.Lane == nil 이면 이 함수를 아예 안 부른다. 그래서 여기 들어온
// 이상 질의는 이미 돈 것이고, Resources 가 비었어도 그 사실("질의는 돌았다")을 문장에
// 반드시 남긴다 — 안 남기면 "질의가 안 돌았다"(nil)와 "아무도 안 섰다"(빈 Resources)가
// 화면에서 같아진다(service.LaneView 주석과 같은 판정). **이 0건 문구는 프로젝트 전체
// 기준이다** — len(l.Resources)==0(자원 우주 자체가 빈 것)일 때만 나가고, 한 줄 고정이다.
// 자원이 하나라도 있으면 그 수만큼 줄이 나온다(renderResourceLane 이 자원 하나씩을 맡는다).
//
// ★ detail=true 는 상한을 안 둔다(laneResourceCap 주석·아래 접기 문구의 "detail=true 로
// 전부 본다" 약속을 지키려면 detail 이 실제로 전부를 내야 한다).
func renderLane(l *service.LaneView, now time.Time, detail bool) []string {
	if len(l.Resources) == 0 {
		// ★ 짧게 쓴다. 이 줄은 레인이 비어 있어도 **매 보드마다** 나가고 잘리지 않는
		//   고정분이라(joinAll 의 foot), 한 낱말이 세션 카드 하나를 접는 값이 된다.
		//   실제로 길게 썼을 때 TestBoardDefaultOutputWithinBudget 이 5토큰 초과로 빨개졌다.
		//   "지금 아무도 안 섰다"는 "0건"과 같은 말이라 뺀다 —
		//   락이 걸린 축은 "질의는 돌았다"(nil 과 빈 슬라이스를 가르는 문구)뿐이다.
		return []string{"랜딩 레인 0건(질의는 돌았다)"}
	}
	if detail || len(l.Resources) <= laneResourceCap {
		lines := make([]string, 0, len(l.Resources))
		for _, rl := range l.Resources {
			lines = append(lines, renderResourceLane(rl, now))
		}
		return lines
	}
	// ★ 자원이 상한을 넘는다 — 접되, **어긋남(⚠) 자원은 접지 않는다.** 가장 시끄러운
	// 문장(회수해야 할 대상)이 접히면 예산은 지켜도 이 절의 존재 이유가 사라진다.
	// 순서는 원래 순서(이름순, service.LandingLane)를 그대로 유지한다 — warn·rest 로
	// 나눠도 각 버킷 안에서는 훑은 순서를 그대로 담으므로 안정적이다.
	var warn, rest []service.ResourceLane
	for _, rl := range l.Resources {
		if resourceLaneWarns(rl) {
			warn = append(warn, rl)
		} else {
			rest = append(rest, rl)
		}
	}
	// 어긋남 자원은 상한과 무관하게 전부 보여준다 — 남는 자리만 나머지로 채운다.
	remaining := laneResourceCap - len(warn)
	if remaining < 0 {
		remaining = 0
	}
	if remaining > len(rest) {
		remaining = len(rest)
	}
	shown := make([]service.ResourceLane, 0, len(warn)+remaining)
	shown = append(shown, warn...)
	shown = append(shown, rest[:remaining]...)
	folded := len(rest) - remaining

	lines := make([]string, 0, len(shown)+1)
	for _, rl := range shown {
		lines = append(lines, renderResourceLane(rl, now))
	}
	if folded > 0 {
		lines = append(lines, fmt.Sprintf(
			"…자원 %d개는 예산 때문에 접었다 — detail=true 로 전부 본다", folded))
	}
	return lines
}

// renderResourceLane 은 자원 하나의 레인 한 줄이다. 순수 함수다 — renderLane 이 자원마다
// 이 함수를 부른다. 옛 단일 레인 시절 renderLane 의 본문이 그대로 여기로 옮겨왔고, 머리의
// "랜딩" 자리가 자원 이름으로 바뀐 것 말고는 판정이 안 바뀌었다(TestBoardDefaultOutputWithinBudget
// 이 기본 자원 하나일 때 출력이 안 늘도록 접두를 `<resource>: ` 로 짧게 유지한다).
func renderResourceLane(rl service.ResourceLane, now time.Time) string {
	if len(rl.Entries) == 0 {
		// ★ 점유는 있는데 이 자원의 줄 행이 하나도 없다 — landing.go 의 불변식("살아 있는
		// 점유에는 반드시 대응하는 살아 있는 줄 행이 있다")이 이 자원 기준으로 깨진 가장
		// 위험한 모양이다(TestLiveLandingHoldAlwaysHasALiveQueueRow 류가 잡으려는 상태
		// 그 자체다. Land 도 이 상태를 만나면 점유자를 그대로 실어 보낸다 — landing.go
		// 참고). rl.Holder 는 여기서 반드시 non-nil 이다 — LandingLane 의 자원 우주가
		// 줄 행·점유의 합집합이라, Entries 도 Holder 도 없는 자원은 애초에 Resources 에
		// 안 실린다.
		//
		// ★ 회수 판정용 **두 나이**(설계 §9 ①)를 이 문장에 싣는다. 아래 정상 경로는 획득 경과를
		// 머리에, 신호 나이를 항목마다 나눠 싣는데 여기는 항목이 0건이라 실을 자리가 이 한 줄뿐이다.
		// 그런데 회수 판정이 **가장 절실한** 화면이 바로 여기다 — 줄 행이 사라진 점유는 사람이
		// 거둬야 하고 그 판단의 근거가 이 두 숫자인데, 둘 다 없으면 화면이 "누가"만 답하고
		// "얼마나 오래됐나"를 되묻게 만든다.
		//
		// ★ 그중 Holder.LastSignalAt 은 LandingLane 이 홀더용으로 채워 두고도
		// renderLane 이 전 함수 통틀어 한 번도 안 읽던 필드였다 —
		// TestLandingQueueHasAProductionReader 가 잡으려는 "계산만 되고 읽는 쪽이 0건"의 필드 판.
		//
		// 낱말은 아래 정상 경로(`점유 획득 %s전` · 항목별 `신호 %s전`/`신호 없음`)를 그대로 베낀다.
		// 같은 숫자가 한 화면에서 두 어휘로 읽히면 회수 판정이 대조부터 해야 한다.
		//
		// ★ nil 은 빈칸으로 두지 않고 "신호 없음"이라고 **적는다** — 여기서 하는 일은 그것뿐이다.
		// **못 읽음과 없음을 가르는 자리가 아니다.** 이 nil 은 두 경우가 이미 뭉개진 값이다:
		// 그 둘을 실제로 가르는 것은 service/landing.go 의 lastSignal 의 **둘째 반환값**인데,
		// 이 필드를 채우는 자리(Land 의 점유자 채움 · LandingLane 의 sig 클로저)가 전부 그 값을
		// `_` 로 버린다. 읽기 실패는 그쪽 WARN 에만 남는다. 그 규율이 지켜지는 곳은 불변으로
		// 남는 판단 본문(ReleaseLaneRow)이고, 거기만 "읽지 못했다"와 "없음"을 다른 문장으로
		// 적는다 — 화면은 애초에 그 축을 못 받는다.
		//
		// ★ ShortID 바로 뒤에 여는 괄호를 두지 않고 문장 꼬리에 ` · ` 로 잇는다. 머리에
		// `<세션>(…)` 모양이 생기면 항목 조각을 잘라 보는 시험(laneEntrySegment)이 그 `)` 를
		// 먼저 집는다 — 이 분기는 항목이 0건이라 지금은 안 밟히지만, 같은 함정을 새로 파지 않는다.
		sig := "신호 없음"
		if rl.Holder.LastSignalAt != nil {
			sig = "신호 " + FormatAge(now.Sub(*rl.Holder.LastSignalAt)) + "전"
		}
		return fmt.Sprintf("%s: ⚠ 정합 어긋남: 점유자 %s 는 있는데 줄 행이 하나도 없다 · 점유 획득 %s전 · %s",
			rl.Resource, ShortID(rl.Holder.SessionID), FormatAge(now.Sub(rl.Holder.AcquiredAt)), sig)
	}
	parts := make([]string, 0, len(rl.Entries))
	for i, e := range rl.Entries {
		mark := ""
		if rl.Holder != nil && rl.Holder.SessionID == e.SessionID {
			mark = "◀점유"
		}
		// ★ **신호 나이를 낸다**(설계 §9 ①). 자동 만료를 안 만든 근거가 "사람이 나이를 보고
		// 판정한다"인데, 그 판정을 내리는 사람은 대기자가 아니라 보드를 보는 사람이다.
		// 여기서 빼면 LaneEntry.LastSignalAt 은 계산만 되고 읽는 쪽이 0건이 된다 —
		// 이 브랜치가 TestLandingQueueHasAProductionReader 로 잡으려는 함정의 필드 판이다.
		// nil 은 빈칸이 아니라 "신호 없음"으로 적는다 — 다만 그 nil 은 못 읽음과 없음이 이미
		// 뭉개진 값이다(위 어긋남 갈래 주석과 같은 이유. LaneEntry 쪽을 채우는 것이 그중
		// LandingLane 의 sig 클로저다).
		sig := "신호 없음"
		if e.LastSignalAt != nil {
			sig = "신호 " + FormatAge(now.Sub(*e.LastSignalAt)) + "전"
		}
		parts = append(parts, fmt.Sprintf("%d.%s(행%d·대기 %s전·%s%s)",
			i+1, ShortID(e.SessionID), e.RowID, FormatAge(now.Sub(e.EnqueuedAt)), sig, mark))
	}
	// ★ 머리에 **자원 이름**과 **점유자의 획득 경과**를 낸다(설계 §9 ①). 자원 이름이
	// 접두인 이유는 이 함수가 자원 하나만 맡아서다 — "랜딩 레인" 이라는 고정 낱말은
	// 옛 단일 레인 시절의 것이고, 지금은 그 자리에 어느 자원인지가 와야 여러 줄을 볼 때
	// 사람이 갈라 읽는다. 회수를 판정하는 사람이 봐야 할 두 숫자가 획득 경과와 신호
	// 나이인데, 앞엣것은 LaneHolder.AcquiredAt 에 채워져 있으면서 이 함수가 안 읽어
	// 화면에 없던 시절이 있었다(옛 renderLane 결함).
	//
	// ★ 점유자의 ShortID 를 머리에 **다시 적지 않는다.** 누가 쥐었나는 항목의 ◀점유 표시가
	// 이미 답하고, 머리에 `<세션>(…)` 모양을 하나 더 두면 항목 조각을 잘라 보는 시험
	// (laneEntrySegment)이 머리 쪽을 먼저 집어 표시 뒤바뀜을 못 잡게 된다.
	head := fmt.Sprintf("%s: 레인 %d건", rl.Resource, len(rl.Entries))
	if rl.Holder != nil {
		head += fmt.Sprintf("(점유 획득 %s전)", FormatAge(now.Sub(rl.Holder.AcquiredAt)))
	}
	line := head + ": " + strings.Join(parts, " ")
	if rl.Holder != nil && !resourceLaneHolderIsQueued(rl) {
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
		sig := "신호 없음"
		if rl.Holder.LastSignalAt != nil {
			sig = "신호 " + FormatAge(now.Sub(*rl.Holder.LastSignalAt)) + "전"
		}
		line += fmt.Sprintf(" · ⚠ 점유자 %s 의 줄 행이 안 보인다(정합 어긋남) · %s",
			ShortID(rl.Holder.SessionID), sig)
	}
	return line
}

// resourceLaneHolderIsQueued 는 자원 하나의 점유자가 그 자원의 줄 목록에도 있는지다.
// 옛 laneHolderIsQueued 가 *service.LaneView 를 받던 것을 자원판으로 좁혔다 — 개편 뒤에는
// "레인"이 아니라 "자원의 레인"이 이 축의 단위다.
func resourceLaneHolderIsQueued(rl service.ResourceLane) bool {
	for _, e := range rl.Entries {
		if e.SessionID == rl.Holder.SessionID {
			return true
		}
	}
	return false
}

// resourceLaneWarns 는 자원 하나가 renderResourceLane 에서 ⚠(정합 어긋남)를 낼지를 본다.
// 순수 함수다 — renderResourceLane 이 실제로 ⚠ 를 찍는 두 갈래(항목 0건인데 점유자가
// 있다 · 항목은 있는데 그중 점유자가 없다)를 여기서 한 판정으로 합친다. renderLane 의
// 자원 상한이 이 값으로 "접지 않을 자원"을 고른다.
func resourceLaneWarns(rl service.ResourceLane) bool {
	return rl.Holder != nil && !resourceLaneHolderIsQueued(rl)
}

// heldOrNoneLine 은 자원 점유 줄이다. **0건일 때도 낸다.**
//
// ★ 예전에는 기본 보드가 점유 0건이면 이 줄을 통째로 뺐고, detail 만 「자원 점유 없음」을
// 냈다. 그래서 이 축은 **쓰는 사람에게만 보이는 축**이었다 — 안 써 본 세션은 축의 존재
// 자체를 모른다. 2026-08-14 에 스테이징을 점유한 세션이 `자원 점유 없음` 을 읽고
// **「fd 에 스테이징 자원 축이 아예 없다」**로 결론낸 뒤 그 오독을 다른 프로젝트 원장에
// 증거로 남겼다(판단 01KZYXQ4…). 실제로는 자원명이 자유 문자열이라
// land(resources:["env:dell"]) 한 줄이면 그날 배타가 섰다.
//
// 그러니 0건은 **「아무도 안 쥐었다」와 「걸 자리가 없다」를 겸하면 안 된다.** 이 제품이
// 반복해 맞은 실패가 「없는 축을 조용히 빼는 것」이고(web/query.go:18) 이 줄이 그 실패의
// 실물 1건이다.
func heldOrNoneLine(held []model.ResourceHold) string {
	if len(held) == 0 {
		return "0건 — 아무도 안 쥐었다는 뜻이지 걸 자리가 없다는 뜻이 아니다. " +
			`자원명은 자유 문자열이다: land(resources:["env:dell"])`
	}
	return heldLine(held)
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
		// ★ 티클러 줄은 **머리줄 바로 뒤**다 — 경로보다도 앞이다. "지금 이걸 집어야
		// 하나"가 "무슨 경로를 건드리나"보다 먼저 오는 질문이기 때문이다. 본문 4000자와
		// 묶음 절 뒤로 밀면 집기 전에 읽는 세션에게 사실상 안 보인다(종료 선언 축이
		// 같은 이유로 이 자리를 잡았다).
		b.WriteString(renderTicklerLine(now, it.Labels, ""))
		if len(it.Paths) > 0 {
			fmt.Fprintf(&b, "경로: %s\n", strings.Join(it.Paths, ", "))
		}
		b.WriteString(renderPathCheck(r.PathCheck, it.ID))
		// ★ 종료 선언 축은 **선두에도** 찍는다. renderBundle 은 BundleInfo 하나만 받고
		// Members 는 정의상 선두 제외라 선두를 모른다 — 구성원 자리에만 심으면 이 사고를
		// 낳은 그 항목(선두였다)에 대해 응답이 정확히 침묵한다.
		//
		// 들여쓰기 0칸은 바로 위 `경로 실재:` 와 같은 깊이라는 뜻이다. 자리도 여기여야
		// 한다 — 본문 4000자와 묶음 절 뒤로 밀면 집기 전에 읽는 세션에게 사실상 안 보인다.
		b.WriteString(renderCloseDeclared(r.CloseDeclared, ""))
		if len(it.After) > 0 {
			fmt.Fprintf(&b, "선행: %s\n", formatAfter(it.After))
			b.WriteString(renderAfterCheck(r.AfterCheck, ""))
		}
		if strings.TrimSpace(it.Body) != "" {
			fmt.Fprintf(&b, "본문:\n%s\n", indent(clip(it.Body, 4000), "  "))
		}
	}

	// 묶음 절. renderBundle 이 세 갈래(부재·단독·구성원 목록)를 전부 말한다 —
	// 이 위치는 항목 블록 뒤·브랜치 줄 앞이다.
	b.WriteString(renderBundle(r.Bundle, now))
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
		// ★ 새 꼬리(2026-08-12, Task 13): 워크트리 절 다음에 랜딩 순서를 말한다.
		// lane-turn 큐가 전 기간 0건이었던 원인 셋 중 하나가 이 자리다 — pick 응답이
		// "집었으면 다음은 무엇인가"를 끝까지 말하지 않으면 세션은 finish 뒤에 land 로
		// 줄을 선다는 것을 스스로 찾아내야 한다. 워크트리 명령을 못 만든 갈래에서도
		// 낸다 — 이 문장의 질문(다음 순서)은 워크트리 성패와 무관하다.
		b.WriteString("끝나면: finish → land 로 줄 서기 → 차례에 랜딩. 기다림은 `fd lane wait` 가 턴 안에서 잇는다.\n")
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
// bundleAt 은 구성원의 티클러 기한을 재는 기준 시각이다 — 선두와 **같은 시각**을
// 쓴다. 한 응답 안에서 두 시각을 쓰면 같은 발화일이 선두에선 지났고 구성원에선
// 안 지난 것으로 보일 수 있다.
func renderBundle(bi *service.BundleInfo, bundleAt time.Time) string {
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
		// ★ 구성원의 티클러도 낸다. 선두에만 심으면 구성원에 대해 응답이 정확히
		// 침묵한다 — 바로 아래 종료 선언 주석이 적어 둔 그 실패의 반대편이다.
		// 자리는 못 집은 갈래의 continue 보다 **위**다: 못 집은 구성원이야말로 다음
		// 세션이 다시 집으러 오는 항목이고, 그것이 티클러면 다시 오는 것 자체가 헛걸음이다.
		b.WriteString(renderTicklerLine(bundleAt, m.Item.Labels, "    "))
		// ★ 종료 선언은 **머리줄 바로 밑**에 찍는다. 아래 못 집은 갈래는 continue 로 절을
		// 끊으므로 이 줄을 그 뒤에 두면 못 집은 구성원에게 영영 안 나온다 — 그런데
		// "이미 닫으려던 항목"과 "지금 못 집었다"는 겹쳐서 나는 사실이고, 못 집은 구성원이야말로
		// 다음 세션이 다시 집으러 오는 자리다. 그래서 사유 줄보다 위다.
		//
		// 값은 **그 구성원의 것**이다. 선두의 r.CloseDeclared 를 여기 넘기면 다섯 항목이
		// 같은 사실을 말하게 되고, 그 변이는 전체 문자열 Contains 로는 안 죽는다
		// (경로 축이 실제로 그렇게 죽어 있었다 — render_test.go:1326-1333).
		b.WriteString(renderCloseDeclared(m.CloseDeclared, "    "))
		if len(m.Item.After) > 0 {
			fmt.Fprintf(&b, "    선행: %s\n", formatAfter(m.Item.After))
			b.WriteString(renderAfterCheck(m.AfterCheck, "    "))
		}
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

// renderAfterCheck 는 선행 충족 축이다. 순수 함수다.
//
// ★ **어느 갈래에서도 침묵하지 않는다** — renderPathCheck 과 같은 규율이다. 침묵하면
// "충족됐다"와 "이 축을 안 봤다"가 같은 화면이 되고, 그러면 이 줄이 막으려는 바로 그
// 사고(막힌 항목을 조용히 집는 것)를 화면이 다시 만든다.
//
// ★ **거절 문구가 아니다.** 이 서버는 명시 선점을 안 막는다(PickResult.AfterCheck 의
// 머리말에 실측 근거가 있다). 그래서 문장이 "집지 마라"가 아니라 "이것이 아직 안 풀렸다,
// 알고 집는 거냐"여야 한다 — 처방까지 함께 낸다: 기다릴 것인지, 선행을 고칠 것인지.
//
// ★ 호출부가 `len(After) > 0` 을 이미 확인하고 부른다. 선행이 없는 항목에까지 이 줄을
// 찍으면 화면 대부분이 "선행 충족: 없다"로 덮이고, 그렇게 잡음이 된 줄은 정작 참인 날
// 아무도 안 읽는다(doctor 의 「0건」 줄을 안 찍는 것과 같은 판단이다).
func renderAfterCheck(v *service.AfterVerdict, pad string) string {
	if v == nil {
		return pad + "선행 충족: **이 응답에 없다** — 구서버이거나 이 축이 생기기 전에 굳은 캐시다. " +
			"충족 여부를 직접 확인해라\n"
	}
	if v.Satisfied {
		return pad + "선행 충족: 전부 충족됐다\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s선행 **미충족** %d건: %s\n", pad, len(v.Reasons), strings.Join(v.Reasons, " · "))
	if len(v.WithInCall) > 0 {
		fmt.Fprintf(&b, "%s  그중 %d건은 **이 호출이 함께 집는다**(%s) — 정상 경로다. "+
			"같은 워크트리에서 선행부터 순서대로 해라.\n",
			pad, len(v.WithInCall), strings.Join(v.WithInCall, " · "))
	}
	if len(v.WithInCall) < len(v.Reasons) {
		fmt.Fprintf(&b, "%s  **막지 않는다 — 명시 선점은 사람의 선택이고 이 서버는 그것을 안 덮는다.** "+
			"기다려서 풀리는 축이면 기다리고, 선행이 폐기됐거나 틀렸으면 "+
			"`fd after cut` 으로 끊어라(그 처방을 집행할 쓰기는 그것 하나뿐이다).\n", pad)
	}
	return b.String()
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

// renderCloseDeclared 는 종료 선언 축이다. 순수 함수이고 renderPathCheck 의 **쌍둥이**다.
//
// ★ **어느 갈래에서도 침묵하지 않는다.** renderPathCheck 이 이상이 없어도 한 줄을 찍는
// 그 이유 그대로다 — 침묵하면 "선언이 없다"와 "이 축을 안 봤다"가 같은 화면이 되고,
// 그러면 원장 조회가 통째로 실패한 날에도 pick 은 평소와 똑같아 보인다. 이 사고가
// 정확히 그 모양이었다: 신호는 08-04 부터 원장에 있었고 08-05 의 추천이 그것을
// 한 글자도 말하지 않았다.
//
// ★ nil 갈래의 문장은 renderPathCheck·renderBundle 의 그것과 **글자가 다르다.**
// 같은 문장을 쓰면 구성원 절 안에서 남의 판정과 내 판정이 문자열로 구분되지 않고,
// render_test.go:1415-1435 의 "제 것인가" 단정이 그 순간 무의미해진다(그 시험은
// unreadSum 을 남의 절에서 못 찾는 것으로 격리를 잰다). 실제로 복제하면 붉어진다.
//
// ★ 접두를 `종료 선언:` 으로 새로 판 이유는 기존 접두들에 개수·절 분할 시험이 물려
// 있기 때문이다(`경로 실재: ` 4개 · `fd move ` 1개 · `브랜치: ` 1개 · 구성원 표식 3종).
//
// ★ nil 갈래 문구는 **원인 중립**이다. pick.go 의 세 서비스 경로(추천·item_id
// 선점재개·묶음)가 이제 전부 이 축을 채우므로(pickExplicit 이 closeDeclarations 를
// 재사용한다), 신선한 온라인 응답에서 nil 이 남는 길은 원인이 **셋**이다 —
// 구서버(이 필드를 모른다) · 옛 캐시(필드가 생기기 전에 굳었다) · 이번 조회 자체의
// 실패(closeRead=false). 문구가 그중 둘만 대면 셋째(조회 실패)가 실제로 난 날에도
// 거짓 원인을 찍는다 — QueueOpen 이 같은 이유로 원인 중립 문장을 쓴다(render.go:975
// 부근, "지어낸 원인보다 정확하다"). 그래서 여기서도 어느 것인지 단정하지 않는다.
//
// ★ 수는 **하한이다.** store 의 CloseDeclarationsByItem doc 이 못박은 계약이다
// (BeginTx 가 실패하면 예약 자체가 없고, 쓰기 실패는 WARN 으로 삼킨다). 그래서 0건
// 갈래에서도 "0이다"로 단정하지 않는다 — 0 이야말로 안 써진 마무리에 가장 잘 속는 값이다.
//
// ★ 처방이 mode 로 갈린다 — done 은 이미 랜딩됐을 수 있고 dropped 는 이미 버리기로
// 판정됐을 수 있다. 둘을 "끝난 일" 하나로 뭉치면 다음 세션이 무엇을 확인해야 하는지가
// 사라진다(랜딩 이력인가, 버린 판단인가). 그래서 둘 다 0이 아니면 두 줄을 다 낸다.
//
// ★ 매개변수 이름 `indent` 가 같은 파일의 indent(s, pad) 헬퍼를 가린다. 계약이 정한
// 시그니처라 그대로 두고, 대신 이 함수 안에서는 줄마다 접두를 직접 붙인다 —
// 여기서 indent(...) 를 부르면 "cannot call non-function" 으로 컴파일이 죽는다.
func renderCloseDeclared(d *model.CloseDeclaration, indent string) string {
	// 이어지는 줄은 "종료 선언: " 만큼 민다 — renderPathCheck 의 되돌리기 줄과 같은 모양이다.
	const cont = "           "
	if d == nil {
		return indent + "종료 선언: 이 응답은 이 축을 안 읽었다 — " +
			"구서버이거나 옛 캐시이거나 이번 조회가 실패했을 수 있다(이 응답만으로는 못 가른다).\n"
	}
	if d.Count() == 0 {
		return indent + "종료 선언: 원장에서 하나도 못 봤다 — 이 항목을 닫으려다 롤백된 시도가 관측되지 않았다.\n" +
			indent + cont + "이 수는 하한이다 — 원장에 안 써진 마무리는 여기서 영영 0으로 보인다.\n"
	}
	// 세션 id 는 event.session_id 에서 오는데 그 열은 NULL 을 받고(schema.sql:367)
	// store 는 그것을 빈 문자열로 낸다. 그대로 찍으면 "세션  · mode=done" 이 되어
	// 읽는 쪽이 잘린 줄로 오해한다.
	session := ShortID(d.LastSession)
	if session == "" {
		session = "미상"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s종료 선언: 롤백된 마무리 선언 적어도 %d건(done %d · dropped %d) — 마지막 %s · 세션 %s · mode=%s\n",
		indent, d.Count(), d.Done, d.Dropped,
		d.Last.UTC().Format("2006-01-02 15:04"), session, d.LastMode)
	if d.Done > 0 {
		fmt.Fprintf(&b, "%s%sdone %d건: 이미 랜딩됐을 수 있다.\n", indent, cont, d.Done)
	}
	if d.Dropped > 0 {
		fmt.Fprintf(&b, "%s%sdropped %d건: 이미 버리기로 판정됐을 수 있다.\n", indent, cont, d.Dropped)
	}
	fmt.Fprintf(&b, "%s%s연결된 판단부터 읽어라. 이 수는 하한이다 — 원장에 안 써진 마무리는 여기 안 잡힌다.\n", indent, cont)
	return b.String()
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
	// ★ **실패·수신자 갈래보다 먼저 찍는다.** 아래 둘은 조기 반환이라, 이 줄을 뒤에
	//   놓으면 하필 무언가 잘못된 순간에 좌표가 화면에서 사라진다.
	//
	// 왜 좌표를 말하나. 같은 병의 선례가 add 에 있다(RenderAdd 의 ★): 프로젝트 좌표를
	// 화면이 안 내는 바람에 항목 10건이 남의 프로젝트로 갔고 id 하나가 영구히 죽었다.
	// note 는 더 나쁘다 — 판단은 추가 전용이라 잘못 걸리면 되돌릴 수 없다.
	// 자기 프로젝트면 안 찍는다: 항상 찍으면 배경이 되고 배경은 아무도 안 읽는다.
	if r.CrossProject != "" {
		fmt.Fprintf(&b, "이 판단은 **다른 프로젝트**의 항목에 걸렸다 — %s. "+
			"그 항목을 집는 세션이 읽는다(내 프로젝트 board 에서는 판단으로만 보인다).\n",
			r.CrossProject)
	}
	// ★ 수신자 축이 실패했으면 "없다"를 단정하지 않는다 — recipients=nil 은 그때
	//   "받을 세션 0건"이 아니라 "못 읽었다"다. 판단은 커밋됐으므로 재호출하면 중복이
	//   남는다는 것까지 같은 줄에 말한다(그 함정이 이 갈래가 생긴 이유다).
	if hasFailureAxis(r.Derived, "recipients") {
		b.WriteString("받을 세션은 이 응답이 못 읽었다 — 판단은 저장됐다. " +
			"**다시 부르지 마라**(추가 전용이라 중복이 남는다). 받을 세션은 board 로 봐라.\n")
		for _, line := range renderFailures(r.Derived, 3) {
			b.WriteString(line + "\n")
		}
		return b.String()
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
	// 되돌리는 길도 같은 줄에 적는다. MCP 표면에는 move 가 없고(설계 §6 이 도구 수를 여덟으로
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
	// ★ **본문은 이 호출이 마지막 기회다.** 바로 위 줄이 프로젝트 축에 대해 되돌리는 길을
	//   적는 것과 같은 자리·같은 이유로 여기 싣는다 — 고칠 수 있는 축과 못 고치는 축이
	//   한 화면에 함께 있어야 "무엇을 고칠 수 있나"가 갈리지 않는다(DESIGN §11).
	//
	//   무엇이 없는지와 왜 없는지는 §11 이 적는다. 여기서 적는 것은 **지금 무엇을 해야
	//   하는가** 하나뿐이다 — 그것이 §6 의 "규율은 응답에 싣는다, 필요할 때만 그 자리에서"다.
	//   이 한 줄이 없으면 세션은 본문이 고쳐지는 줄 알고 add 를 대충 쓰고, 틀린 전제가
	//   큐에 남아 다음 사람이 그것을 조사로 되짚는다(이 줄을 낳은 항목이 그 비용이다).
	fmt.Fprintf(&b, "본문은 나중에 못 고친다 — 틀렸으면 `note(item_id: \"%s\")` 로 정정을 얹어라(집을 때 전문으로 온다).\n", it.ID)
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
	// ★ "FK 로" 라고 쓰지 않는다. judgment_link.target_id 에는 REFERENCES 가 없고
	//   (schema.sql:265), **그 부재가 이 변경의 존재 이유다** — 표면이 반대로 말하면
	//   이 브랜치가 없앤 거짓말 하나를 같은 응답에서 되살린다.
	if len(r.Followups) > 0 {
		ids := make([]string, 0, len(r.Followups))
		for _, f := range r.Followups {
			ids = append(ids, f.ID)
		}
		fmt.Fprintf(&b, "후속 %d건 등록: %s (판단에 이어졌다)\n", len(ids), strings.Join(ids, ", "))
		// ★ 창작 후속이 **하나라도** 있으면 낸다(2026-08-21 개정 — 앞선 판은 버스트 ≥3 에만 냈다).
		//
		// 뒤집은 근거는 문구가 아니라 **도달률**이다. 거르는 기준(service.TriageGuidance)은
		// judgeMissingFollowups 거절에만 실리는데, 그 관문은 후속을 0건으로 내려는 세션에게만
		// 세션당 한 번 문다 — 원장 창 2026-08-11T10:12~08-20 에서 **1회** 발화하는 동안 같은
		// 창이 창작 후속 338건을 냈다(1/338). 게다가 이 저장소의 상주 규율("후속은 add 말고
		// followups 에")을 지키면 followupCandidates 가 비어 그 관문이 아예 안 돈다 —
		// **규율 준수가 유일한 억제 표면을 끈다.**
		//
		// 그 위에 배치의 구멍이 있었다: 0건은 아래 0건 줄이, ≥3건은 이 줄이 잡는데 **1~2건
		// 구간이 통째로 비었고**, 그 구간 세션이 본 것은 바로 위의 등록 칭찬 한 줄뿐이다.
		// 구멍의 크기(committed finish 510회): 0개 280 · 1개 97 · 2개 59 · ≥3개 74.
		//
		// 겨누는 실물: 같은 창의 done 후속 141건 중 81건(57%)을 **만든 세션이 중앙값 20.7분 뒤
		// 다시 집어서** 닫았다. 원장 왕복만 늘린 것이고, 그 세션들은 그 자리에서 할 수 있었음을
		// 이미 행동으로 증명했다 — 이 줄이 주는 것이 정확히 그 실행 경로다.
		//
		// 상시 점등이 아니다 — 후속 0건이 committed finish 의 54.9%라 절반 이하에서만 뜬다.
		// R≥1 조건은 **여전히 기각이다** — 전 관측창에서 상시 참이라 판별력이 0이고(§4),
		// 지표 도입 당일 필요한 후속을 회피한 굿하트 실물이 이미 있다.
		// 문구는 지시가 아니라 기준 + 실행 동사다 — 판단은 세션이 한다.
		if len(ids) > 0 {
			// ★ 발화 조건은 넓히되 **실측 문장은 안 넓힌다.** "유입의 절반"은 창작 ≥3 에서만
			// 참인 수다(finish 의 9.5%가 유입의 51%). 1~2건에 그대로 실으면 이 응답이 거짓을
			// 말하고, 이 저장소에는 관문 문구가 거짓이어서 세션 행동을 실제로 오염시킨 실물이
			// 있다(d878bab — 그 문구가 권한 우회를 쓴 세션이 있었고, 나온 것은 중복 판단이었다).
			if len(ids) >= 3 {
				fmt.Fprintf(&b, "  창작 후속 %d건 — 이런 버스트가 후속 유입의 절반을 낳는다. ", len(ids))
			} else {
				fmt.Fprintf(&b, "  창작 후속 %d건 — 큐가 그만큼 늘었다. ", len(ids))
			}
			b.WriteString("**본문이 곧 패치**인 것이 있으면 pick(item_ids=[…]) 으로 지금 이어받아 " +
				"이 세션에서 닫아라. **물으면 정해지는 것도 후속이 아니다 — 지금 물어라.** " +
				"후속은 \"지금 못 하는 이유\"가 있는 것만이다.\n")
			if len(ids) >= 3 {
				b.WriteString("  같은 검증 축인 것들은 한 항목으로 묶을 수 있었는지 보라.\n")
			}
		}
		// ★ 선행 없이 등록된 후속에만 낸다. `landed_ref` 가 Tier A 에서 영영 NULL 이라
		//   (설계 §3) 후속을 쓰는 사람은 걸 sha 를 어디서도 못 얻는데, 그 값이 **지금
		//   이 자리에서는 파생된다** — 서버가 이 브랜치 head 를 알고, 그것이 기본 브랜치의
		//   조상일 때만 AfterCandidate 가 채워진다.
		//
		//   실측된 사고가 이 줄의 근거다: 전제가 3일 미랜딩인 항목이 선행 없이 큐에 남아
		//   기아 78h 1순위로 추천됐고, 집은 세션이 코드를 열고서야 전제 부재를 알았다.
		//
		//   **못 붙인다는 사실을 함께 말한다.** 열린 항목에 선행을 더하는 표면이 없어서
		//   (item_after INSERT 는 AddItem 안에만 있다) "다음에 걸어라"만 적으면 세션은
		//   지금 고칠 수 있다고 믿고 방법을 찾다가 시간을 쓴다.
		if r.AfterCandidate != "" {
			bare := make([]string, 0, len(r.Followups))
			for _, f := range r.Followups {
				if len(f.After) == 0 {
					bare = append(bare, f.ID)
				}
			}
			if len(bare) > 0 {
				fmt.Fprintf(&b, "  선행 없이 등록된 후속 %d건(%s) — 이 작업 뒤에 와야 하는 것이 있었다면 "+
					"`after: [{sha: \"%s\"}]` 였다(이 브랜치 head 이고 이미 랜딩됐다). "+
					"**지금은 못 붙인다** — 열린 항목에 선행을 더하는 표면이 없다. "+
					"그래도 필요하면 note(item_id=…) 로 남겨라(집을 때 전문으로 온다).\n",
					len(bare), strings.Join(bare, ", "), r.AfterCandidate)
			}
		}
	}
	// ★ 이은 것은 **따로** 낸다. 등록과 한 줄에 담으면 "후속 2건 등록"이라고 말하는데
	// 큐에는 1건만 늘어, 세션이 자기가 큐에 무엇을 했는지 못 본다.
	if len(r.LinkedFollowups) > 0 {
		fmt.Fprintf(&b, "기존 항목 %d건을 후속으로 **이었다**: %s (새로 만들지 않았다)\n",
			len(r.LinkedFollowups), strings.Join(r.LinkedFollowups, ", "))
		// ★ 실어 보낸 제목·본문을 **버렸다고 말한다.** 여기서 침묵하면 "적게 하고 서버가
		// 버린다"가 된다 — 설계 §3 이 이 변경으로 없애기로 한 바로 그 모양이다.
		// 오늘까지 스키마가 title·body 를 필수로 받아 왔으므로(tools.go) 세션은
		// 관성으로 싣고, 잇기는 그것을 안 읽는다(store 에 항목 본문 갱신이 없다).
		// 그리고 이 변경 전에는 같은 입력이 "안 넣었다"로 시끄럽게 나왔다 —
		// 여기서 침묵하면 신호가 조용해지는 쪽으로 퇴행한다.
		b.WriteString("  함께 실어 보낸 제목·본문은 **안 썼다** — 그 항목의 것을 안 덮는다" +
			"(다른 세션이 그 본문을 근거로 계획을 세운다). 내용이 다르면 다른 id 로 add 해라.\n")
	}
	// ★ 0건 문구는 **등록과 잇기가 둘 다 0**일 때만 뜬다. 등록만 보면 잇기만 한 마무리에서
	// "지금 add 로 넣어라"가 떠서 방금 이은 것을 부정한다.
	// ★ "있다면 지금 add 로 넣어라"를 지웠다 — 규율 전체에 항목화를 미는 문장만 10곳이고
	// 거르는 기준이 0이던 불균형의 한 자리다(2026-08-07 실측: 이 줄을 포함한 관문·문구가
	// add 를 followups 로 전환만 시키고 총유입을 못 줄였다). 빠뜨림 방지는 거절-시점
	// 관문(judgeMissingFollowups)이 진다 — 그쪽이 준수가 실측된 표면이다.
	// ★ **0건 줄이 안심시키기만 하면 안 된다(2026-08-11 개정).** 앞선 판은 "정말 없다면
	//   그것이 맞다"로 시작해서, followups 를 **실었는데 0건이 된** 세션이 이 줄을 읽고도
	//   못 알아챘다. 그 호출은 항목을 닫았고 finish 는 한 트랜잭션이라 다시 못 불러서,
	//   판단↔후속 링크가 영영 죽었다(원장 `item.finish`: count:0 · tx:committed).
	//
	//   키가 왔는데 비었으면 이제 관문이 미리 거절한다(followups_arrival.go). 그런데
	//   **키가 아예 안 온 갈래는 서버가 "안 보냈다"와 "보냈는데 유실됐다"를 원리적으로 못
	//   가른다** — 그 갈래를 지는 것이 이 문구다. 그래서 되돌릴 수 없다는 사실과 복구 경로를
	//   먼저 말하고, 그 다음에야 "정말 없으면 맞다"를 말한다.
	if len(r.Followups) == 0 && len(r.LinkedFollowups) == 0 {
		b.WriteString("후속 0건 — followups 를 **실었는데** 이 줄이 보이면 그것은 서버에 안 왔다는 뜻이다. " +
			"finish 는 한 트랜잭션이라 **다시 못 부른다** — 지금 add 로 항목을 만들고 " +
			"note(kind='handoff', item_id=…) 로 위 판단을 그 항목에 직접 걸어라(링크를 대신 산다).\n" +
			"  정말 없거나 그 자리에서 끝냈다면 그것이 맞다. " +
			"빠뜨린 것이 있으면 add 로 넣되, **본문이 곧 패치인 것은 항목이 아니라 지금 할 일이다.**\n")
	}
	// ★ 건너뛴 후속은 **반드시 낸다.** 사유는 둘로 갈렸다(만들 대상이 그 사이 생겼다 ·
	// 이을 대상이 사라졌다) — 화면은 무엇이 안 들어갔는지를 이름으로 말하고,
	// 왜인지는 원장(item.followup_skipped)이 갖는다. 한쪽 사유를 화면에 박으면
	// 다른 쪽에서 거짓이 된다.
	if len(r.SkippedFollowups) > 0 {
		fmt.Fprintf(&b, "후속 %d건은 **안 넣었다**: %s\n",
			len(r.SkippedFollowups), strings.Join(r.SkippedFollowups, ", "))
		b.WriteString("  그 사이 그 id 의 항목이 생겼거나, 이을 대상이 사라졌다 — 사유는 원장에 있다.\n")
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
	b.WriteString(finishBalanceLines(r.QueueBalance))
	// ★ 파생 실패를 낸다 — 예컨대 커밋 뒤 항목 되읽기가 실패하면 첫 줄의 id·상태는
	//   트랜잭션이 아는 사실이지만 전문(제목·본문·경로)은 이 응답이 못 읽은 것이다.
	//   그 사실이 여기 없으면 JSON 에만 남아 MCP·CLI 세션은 결손을 모른 채 지나간다.
	for _, line := range renderFailures(r.Derived, 3) {
		b.WriteString(line + "\n")
	}
	return b.String()
}

// hasFailureAxis 는 파생 실패에 그 축이 있는가다. 순수 함수다.
func hasFailureAxis(d service.Derived, axis string) bool {
	for _, f := range d.Failures {
		if f.Axis == axis {
			return true
		}
	}
	return false
}

// finishBalanceLines 는 "이 마무리가 큐를 늘렸나 줄였나"를 낸다.
//
// ★ 왜 이 자리에 있나. 실측(kweiza-cc-plugins · event 원장) R=1.30 — 사이클 1회
// (pickup→작업→finish)마다 큐가 +0.29 다. **pickup 을 더 돌려서는 큐가 안 준다.**
// (그 1.30 은 **2026-08-06 무렵의 전 기간** 값이다. 지금 다시 재면 최근 20 창은 0.80 이고,
// 이 수의 출처와 §10 기한을 읽을 때의 오차는 store.QueueReproduction 의 doc 과 DESIGN §10 이
// 적는다 — 이 자리는 그 값을 화면에 놓는 이유만 적는다.)
// 그런데 세션은 자기가 큐에 무엇을 했는지 볼 방법이 없었다: 보드는 총량만 내고
// 그것도 다음 세션이 본다. 측정을 그 자리에 놓으면 판단은 사람이 한다 — 이 저장소가
// 추천 강제를 기각하고 실측을 남긴 것과 같은 형태다(fd-recommend-path-barely-used).
//
// ★ **거절하지 않는다.** R 이 높다고 finish 를 막으면 세션은 followups 를 안 실어
// 우회하고, 그러면 판단과 후속의 링크가 끊긴다 — 그것이 이 도구가 가장 비싸게 산 자산이다.
func finishBalanceLines(b *service.QueueBalance) string {
	// nil 은 "수지 0"이 아니다. 침묵하면 조회가 실패한 응답이 "큐가 안 늘었다"를 단정한다.
	if b == nil {
		return "큐 수지는 이 응답이 못 읽었다 — 이 마무리가 큐를 늘렸는지 줄였는지 모른다.\n"
	}
	var s strings.Builder
	fmt.Fprintf(&s, "큐 수지: 이 마무리가 닫음 %d · 만듦 %d → %+d\n", b.Closed, b.Added, b.Delta())

	// 굶은 것이 0건이면 그 절을 뺀다 — 상시 점등된 경고는 판별력이 0이 된다.
	if b.Starved > 0 {
		fmt.Fprintf(&s, "  열린 %d건 · %s 넘게 안 집힌 것 %d건(최고령 %s)\n",
			b.Open, FormatAge(judge.StarvationAge), b.Starved, FormatAge(b.Oldest))
	} else if b.Oldest > 0 {
		fmt.Fprintf(&s, "  열린 %d건(최고령 %s)\n", b.Open, FormatAge(b.Oldest))
	} else {
		fmt.Fprintf(&s, "  열린 %d건\n", b.Open)
	}

	// 표본 0을 R=0.00 으로 찍으면 "큐가 안 는다"로 읽힌다. 못 잰 것은 못 쟀다고 적는다.
	//
	// ★ 갈래가 **셋**이다. 앞 판은 둘을 한 문장으로 내어, 집계가 실패했을 뿐인데 응답이
	//   "최근 마무리 표본 0"이라고 **원인을 단정**했다 — 마무리가 20회 쌓여 있어도 같은
	//   문장이 나갔다. 표본 0은 참일 수 있는 사실이라 실패와 섞으면 그 사실이 못 쓰게 된다.
	// ★ **원인을 지어내지 않는다.** 이 줄이 아는 것은 "이 응답에 원자료가 실렸나"뿐이다 —
	//   원자료가 없는 이유는 집계 실패일 수도, 프록시가 필드를 떨군 것일 수도, 이 축을 값
	//   타입으로 내던 옛 서버 판(0.10~0.12)일 수도 있다. 반대로 원자료가 실렸다고 "집계가
	//   됐다"를 단정할 수도 없다 — 그 옛 서버는 집계가 **실패해도** 제로값을 실어 보낸다.
	//   즉 wire 위의 존재/부재는 성공/실패의 대리값이 아니다. pick.go 의 QueueOpen 이 같은
	//   이유로 원인 중립 문장을 쓴다("지어낸 원인보다 정확하다").
	rate, verdict := b.Rate()
	switch verdict {
	case service.RateUnmeasured:
		s.WriteString("  R 은 못 쟀다 — 이 응답에 재생산율 원자료가 없다. " +
			"큐가 느는지 주는지 이 응답은 모른다.\n")
		return s.String()
	case service.RateNoSample:
		s.WriteString("  R 은 아직 없다 — 이 응답이 낸 표본이 0회다. " +
			"마무리가 쌓이면 이 줄이 값을 낸다.\n")
		return s.String()
	}
	trend := "큐가 준다면 R<1 이어야 한다"
	if rate < 1 {
		trend = "큐가 줄고 있다"
	}
	fmt.Fprintf(&s, "  최근 %d회 마무리 기준 R=%.2f — %s\n", b.ReproWindow, rate, trend)
	return s.String()
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

// renderDelta 는 상대 경로 하나의 증감 표기다. 순수 함수다.
//
// ★ **키가 없으면 `(규모?)` 다 — `(+0/-0)` 이 아니다.** 수와 **모양이 달라야** 한다:
// 0 으로 찍으면 읽는 쪽이 "안 만졌다"로 읽고, 그것이 이 축이 없애려는 오탐의 거울상이다.
// 못 재는 자리가 넷 있다 — 이진 파일 · 미추적 파일 · footprint 에만 있는 경로 · git 파생 실패.
func renderDelta(m map[string]model.LineDelta, path string) string {
	d, ok := m[path]
	if !ok {
		return "(규모?)"
	}
	return fmt.Sprintf("(+%d/-%d)", d.Added, d.Removed)
}

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
		lines = append(lines, fmt.Sprintf(
			"겹침 %d건 (거르지 않고 알린다 · 상대 규모 큰 순):", len(in.Overlaps)))

		// ★ 정렬(judge.SortOverlapsBySize)이 못 읽은 것을 맨 위로 올리므로, 잘리는 쪽
		// (in.Overlaps[tailOverlapLimit:])에도 못 읽은 것이 섞일 수 있다 — git 파생이
		// 통째로 실패하면(프로젝트 경로를 모를 때 · git 이 없을 때) 모든 겹침이 한꺼번에
		// 못 읽음이 되고, 그 열화 상태에서는 "제일 작은 쪽"이 거짓이 된다. 잘리는 쪽을
		// 실제로 살펴 갈라 말한다 — 판정은 judge.OverlapHasUnknownSize 한 자리다
		// (정렬이 쓰는 판정과 다른 판정을 쓰면 정렬은 위로 올리는데 문구는 아래라고
		// 말하는 어긋남이 생긴다).
		cutReason := "제일 작은 쪽이다"
		if len(in.Overlaps) > tailOverlapLimit {
			for _, c := range in.Overlaps[tailOverlapLimit:] {
				if judge.OverlapHasUnknownSize(c) {
					cutReason = "규모를 못 읽은 것이 섞여 있다 — 작다는 뜻이 아니다"
					break
				}
			}
		}

		for i, o := range in.Overlaps {
			if i >= tailOverlapLimit {
				lines = append(lines, fmt.Sprintf(
					"  · … %d건 더(%s) — 수는 위 머리줄이 전부 센 값이다. 이름까지는 board 가 낸다",
					len(in.Overlaps)-tailOverlapLimit, cutReason))
				break
			}
			pairs := make([]string, 0, len(o.Pairs))
			for i, p := range o.Pairs {
				if i >= 4 {
					pairs = append(pairs, fmt.Sprintf("+%d", len(o.Pairs)-4))
					break
				}
				pairs = append(pairs, fmt.Sprintf("%s↔%s%s", p[0], p[1], renderDelta(o.TheirDelta, p[1])))
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
		// ★ Resources 가 있으면 그것을 낸다(Task 3 의 all-or-nothing 취득 — 이 land 가 실제로
		// 쥔 자원 전부가 여기 있다). 비면 옛 문장으로 폴백한다 — **구서버 응답 호환**: 자원
		// 개편 이전 REST 서버가 낸 JSON 에는 "resources" 필드 자체가 없어(cmd/fd/mcpbackend.go
		// 의 json.Unmarshal 이 그대로 통과시킨다) 여기서 nil 슬라이스로 온다. 그때 자원 이름을
		// 지어내지 않고 침묵하지도 않는 것이 이 갈래다.
		if len(r.Resources) > 0 {
			fmt.Fprintf(&b, "land · 네 차례다 — %s 를 쥐었다 (줄 행 %d)\n",
				strings.Join(r.Resources, " "), r.RowID)
		} else {
			fmt.Fprintf(&b, "land · 네 차례다 — 레인을 쥐었다 (줄 행 %d)\n", r.RowID)
		}
		b.WriteString("다 쓰면 result 로 보고하고 반납해라. 줄 서 놓고 그만두려면 leave 를 써라.\n")

	case "waiting":
		fmt.Fprintf(&b, "land · 너는 %d번째다 (줄 행 %d · 자원 %s)\n",
			r.Position, r.RowID, strings.Join(r.Resources, " "))
		// ★ **Blockers 가 있으면 그것으로 자원별로 보인다.** service.Land 는 waiting 을 내는
		// 유일한 경로이고, 대기로 남는 것 자체가 blockers 를 최소 하나 채운 뒤에만 일어난다
		// (internal/service/landing.go:194 이하 — grantable=false 가 되는 자리마다 blockers
		// append 가 함께 있다). 그래서 **실제 서비스 응답에는 이 갈래가 항상 온다.**
		//
		// ★ Blockers 가 비었는데 Holder 만 있는 갈래는 옛 한 줄 표시로 폴백한다 — 살아 있는
		// 것은 둘이다: ① 자원 개편 이전 REST 응답(Blockers 필드 자체가 없던 시절의 JSON,
		// turn 갈래와 같은 구서버 호환 사유), ② 이 파일의 옛 단위 시험들
		// (TestRenderLandWaitingWithHolder 등)이 Holder 만 손으로 채워 재는 자리 — LandResult
		// 주석의 "Holder 는 blockers[0] 과 같은 포인터"라는 불변식은 service.Land 가 실제로
		// 채울 때만 성립하고 수기로 만든 값에는 안 미친다. Blockers 루프로만 바꾸면 그
		// 시험들이 깨진다(실측: 처음 이 폴백 없이 구현해 돌렸더니 그 세 시험이 바로 빨강이
		// 됐다) — 그래서 추가가 아니라 갈림이다.
		switch {
		case len(r.Blockers) > 0:
			for _, bl := range r.Blockers {
				switch {
				case bl.Holder != nil:
					fmt.Fprintf(&b, "%s: 점유 %s · 획득 %s 전", bl.Resource,
						ShortID(bl.Holder.SessionID), FormatAge(now.Sub(bl.Holder.AcquiredAt)))
					if bl.Holder.LastSignalAt != nil {
						fmt.Fprintf(&b, " · 마지막 신호 %s 전\n", FormatAge(now.Sub(*bl.Holder.LastSignalAt)))
					} else {
						b.WriteString(" · 마지막 신호 없음\n")
					}
				default:
					fmt.Fprintf(&b, "%s: %d번째 · 앞 줄 행 %d(%s)\n", bl.Resource, bl.Position,
						bl.FrontRowID, ShortID(bl.FrontSessionID))
				}
			}
		case r.Holder != nil:
			fmt.Fprintf(&b, "지금 레인: %s · 획득 %s 전",
				ShortID(r.Holder.SessionID), FormatAge(now.Sub(r.Holder.AcquiredAt)))
			if r.Holder.LastSignalAt != nil {
				fmt.Fprintf(&b, " · 마지막 신호 %s 전\n", FormatAge(now.Sub(*r.Holder.LastSignalAt)))
			} else {
				b.WriteString(" · 마지막 신호 없음\n")
			}
		default:
			b.WriteString("지금 레인을 쥔 사람이 없다 — 앞사람이 아직 land 를 안 불렀다.\n")
		}
		// ★ **교체다, 추가가 아니다.** 여기 있던 문장은 "차례는 서버가 밀어주지 않는다 —
		// 다시 물으려면 land 를 다시 불러라." 였다. 통로가 서면서 그 첫 절이 거짓이 됐다.
		//
		// ★ 그래도 "이제 가만히 있어도 된다"로는 쓰지 않는다. 처방은 (세션 × 키) 1회고 키에
		// 줄 행 번호가 실려 있어(judge.PrescribeInput.LaneTurnRow 주석), **훅이 못 돌면**
		// 이 줄 행의 차례 통지는 영영 안 온다 — 그 실패는 화면에 안 뜬다. 폴링을 닫는
		// 문장을 쓰면 그 세션은 자기 차례를 영영 모른 채 서 있고 뒤 줄 전원이 그만큼 선다.
		// 그래서 두 문장이다: 하나는 밀어 온다는 사실, 하나는 그것이 한 번뿐이라 두 번째 길이
		// 그대로 남아 있다는 사실. 키 이름을 본문에 박는 이유는 세션이 실제로 받는 처방과
		// 이 문장을 이어 읽어야 하기 때문이다.
		b.WriteString("차례가 오면 처방이 턴 끝에 온다(Stop 훅이 끌어간다) — 키는 lane-turn 이고 그 줄 행 앞으로 한 번뿐이다.\n")
		// ★ 둘째 문장 교체(2026-08-12): "land 를 다시 불러 묻는 길" 은 손 폴링 안내였다.
		// 대기의 통로는 이제 fd lane wait 다 — 취득의 정본이 land 인 것은 그대로다.
		b.WriteString("`fd lane wait` 가 턴 안에서 차례까지 기다린다(취득은 land 가 트랜잭션 안에서 다시 판정한다) — 기다림을 끊으려면 leave 다.\n")

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
