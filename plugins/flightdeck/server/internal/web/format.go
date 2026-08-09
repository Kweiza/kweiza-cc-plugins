// Package web 는 서버 렌더 대시보드다. HTML 한 장, 섹션 6개, 쓰기 버튼 5개(살아 있는 것은 셋).
//
// 이 패키지가 지키는 것 넷:
//
//  1. **판정을 본문에 흘리지 않는다.** "낡았다"·"디스크 임계"·"이 요청을 받아도 되나"는 전부
//     이 파일의 순수 함수가 사유 문자열과 함께 돌려주고, 시험이 그 함수를 직접 부른다.
//     본문에 흩어지면 시험이 로직의 사본을 단정하게 되고 변이가 조용히 샌다.
//
//  2. **"죽었다"를 쓰지 않는다.** 신호 넷의 나이를 숫자로 나란히 낸다(설계 §4).
//     세션에는 어떤 임계 경고도 붙이지 않는다 — 상시 점등된 경고는 판별력이 0이다.
//     임계는 자원(디스크)에만 붙인다.
//
//  3. **모든 패널에 파생 표기를 붙인다.** 서버가 죽었을 때 마지막 상태가 현재 사실인 척하는 것을
//     구조로 막는다(설계 §6).
//
//  4. **파생물에 손대는 쓰기 폼을 만들지 않는다.** 쓰기는 다섯뿐이고(선점 회수·항목 폐기·
//     랜딩 줄 행 회수·레인 정지/재개·잡 우회 기록) 전부 사유가 필수다. 그중 **앞 셋이
//     Tier A** 이고, 뒤 둘(레인 정지/재개·잡 우회 기록)은 Tier B 라 **비활성 버튼으로
//     자리만 낸다** — 지우면 "이 축이 없다"와 "우리가 이 축을 안 본다"가 구분되지 않는다.
//
//     줄 행 회수가 Tier A 인 이유는 **이 서버가 실제로 그 일을 하기 때문**이다.
//     레인에 자동 만료가 없어서(그 판정으로 이 레포는 실측 두 번 틀렸다) 물린 줄을
//     푸는 길이 사람뿐이고, 화면과 CLI 가 그 길의 전부다.
package web

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// ─────────────────────────────────────────────────────────────────────────────
// 나이 — 숫자만 낸다. 판정하지 않는다
// ─────────────────────────────────────────────────────────────────────────────

// Age 는 경과 시간을 한국어 한 덩이로 옮긴다. 순수 함수다.
//
// ★ "죽었다"·"오래됨" 같은 판정 어휘를 만들지 않는다. 나이는 사실이고 판정은 사람 몫이다
// (설계 §4 — 그 판정은 실측에서 두 번 틀렸다).
//
// 음수(미래)를 0으로 접지 않는다. 접으면 시계가 어긋난 머신의 세션이 "방금"으로 보여
// 가장 이상한 상태가 가장 정상으로 읽힌다.
// SkewThreshold 는 "미래"를 시계 어긋남으로 부르기 시작하는 경계다.
//
// 초 미만의 음수는 **잡음이지 어긋남이 아니다** — 서버가 시각을 찍은 뒤 렌더까지의
// 왕복과 반올림만으로 몇 밀리초가 뒤집힌다. 그것을 "시계 어긋남"으로 부르면
// 가장 흔한 경우에 가장 큰 경고가 붙고, 상시 점등된 경고는 판별력이 0이 된다
// (설계 §4 가 무갱신 경고에 대해 못박은 것과 같은 축이다).
const SkewThreshold = 2 * time.Second

func Age(d time.Duration) string {
	if d < -SkewThreshold {
		return "미래 " + span(-d) + " (시계 어긋남)"
	}
	if d < time.Second {
		// 여기에는 경계 안의 음수도 들어온다. 접는 것이 아니라 **잡음으로 판정한 것**이고,
		// 진짜 어긋남은 위에서 이미 걸렀다.
		return "방금"
	}
	return span(d) + " 전"
}

// span 은 부호 없는 경과의 크기 표현이다.
func span(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d초", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d분", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		if m == 0 {
			return fmt.Sprintf("%d시간", h)
		}
		return fmt.Sprintf("%d시간 %d분", h, m)
	default:
		days := int(d.Hours()) / 24
		h := int(d.Hours()) - days*24
		if h == 0 {
			return fmt.Sprintf("%d일", days)
		}
		return fmt.Sprintf("%d일 %d시간", days, h)
	}
}

// SignalAge 는 신호 한 종류의 표시분이다.
//
// Known 이 별도 필드인 이유: 시각의 0값과 "그 종류가 한 번도 안 왔다"를 가르기 위해서다.
// 0값으로 접으면 "안 온 신호"가 "1970년에 온 신호"로 둔갑하고, 둘은 나이 표시에서 구분되지 않는다.
type SignalAge struct {
	Kind  model.SignalKind
	Label string
	Known bool
	At    time.Time
	Age   string
}

// signalOrder 는 화면에 나란히 세우는 순서다. **합치지 않는다**(설계 §4).
var signalOrder = []struct {
	kind  model.SignalKind
	label string
}{
	{model.SignalPrompt, "prompt(사람)"},
	{model.SignalTool, "tool(도구)"},
	{model.SignalMCP, "mcp"},
	{model.SignalCommit, "commit"},
	{model.SignalPush, "push"},
}

// SignalAges 는 신호 넷(+push)을 **고정 순서로 전부** 낸다. 순수 함수다.
//
// 없는 종류를 빼지 않는다 — 빼면 "그 신호가 안 왔다"와 "이 화면이 그 축을 안 본다"가
// 구분되지 않는다. 그것이 이 제품이 반복해 맞은 실패의 모양이다.
func SignalAges(now time.Time, sig map[model.SignalKind]time.Time) []SignalAge {
	out := make([]SignalAge, 0, len(signalOrder))
	for _, s := range signalOrder {
		row := SignalAge{Kind: s.kind, Label: s.label, Age: "없음"}
		if at, ok := sig[s.kind]; ok {
			row.Known, row.At, row.Age = true, at, Age(now.Sub(at))
		}
		out = append(out, row)
	}
	return out
}

// activityKinds 는 "이 세션이 일하고 있나"에 답하는 신호다.
//
// ★ **mcp 와 push 는 일부러 뺐다.**
//
//	mcp  — 도구 호출이면 무엇이든 찍는다. mcpsrv 의 callTool 이 이름을 안 가리고
//	       dispatch **전에** 찍으므로 읽기 전용 board 하나로도 점등되고,
//	       service.Note 도 찍는데 그 문은 REST 로 열려 있어 PreCompact 훅의
//	       **자동 초안**과 CLI fd note 가 들어온다. 사람도 에이전트도 아무 일을
//	       안 한 시점에 켜지는 신호라 배지의 판별력이 0이 된다.
//	push — 랜딩하고 떠난 세션이 계속 일하는 것처럼 보인다.
//
// ★ 옛 근거는 "세션 열기와 상태 전이가 mcp 를 찍는다"였다(실측: 카드 26장 중 16장이
// mcp 하나뿐이었다). 그 두 자리는 지웠다 — 열기는 도구 호출이 아니기 때문이다.
// 근거가 바뀌었을 뿐 결론은 그대로다.
//
// 이 목록을 늘리기 전에 그 신호가 **사람이나 에이전트의 작업**을 뜻하는지 먼저 물어라.
var activityKinds = []model.SignalKind{model.SignalPrompt, model.SignalTool, model.SignalCommit}

// ActivityOf 는 "이 세션이 일하고 있나"와 **그 사유**를 낸다. 순수 함수다.
//
// 불리언만 내지 않는 이유는 이 패키지의 계약 그대로다 — 화면이 배지 하나로 접으면
// 사람이 무엇을 근거로 회수할지 못 정한다. 사유는 **항상 채운다**: 비면 "활동 없음"과
// "이 축을 안 읽었다"가 구분되지 않는다.
//
// 이 판정은 **죽음을 말하지 않는다.** 나이를 숫자로 낼 뿐이고, 회수는 사람이 한다(설계 §4).
func ActivityOf(now time.Time, sig map[model.SignalKind]time.Time) (bool, string) {
	var newest time.Time
	for _, k := range activityKinds {
		if at, ok := sig[k]; ok && at.After(newest) {
			newest = at
		}
	}
	if newest.IsZero() {
		return false, "활동 없음"
	}
	return true, "활동 " + Age(now.Sub(newest))
}

// ─────────────────────────────────────────────────────────────────────────────
// 파생 표기 — 모든 패널에 붙는다
// ─────────────────────────────────────────────────────────────────────────────

// DerivedLabel 은 "(파생: git@14:31 · 12초 전)" 한 줄을 만든다. 순수 함수다.
//
// 설계 §6 이 요구하는 전 패널 공통 표기다. 관측 시각이 없으면 **시각을 지어내지 않고**
// 그 사실을 쓴다 — 지어내면 서버가 죽은 화면이 현재 사실인 척한다.
func DerivedLabel(now time.Time, f model.Freshness, failures int) string {
	src := strings.TrimSpace(f.Source)
	if src == "" {
		src = "미상"
	}
	if f.ObservedAt.IsZero() {
		return fmt.Sprintf("(파생: %s · 관측 시각 없음 — 이 값이 언제 것인지 모른다)", src)
	}
	b := fmt.Sprintf("(파생: %s@%s · %s", src, f.ObservedAt.UTC().Format("15:04"), Age(now.Sub(f.ObservedAt)))
	if f.Stale {
		b += " · 낡음"
	}
	if failures > 0 {
		b += fmt.Sprintf(" · 못 읽은 축 %d", failures)
	}
	return b + ")"
}

// DBFreshness 는 DB 만 읽어 만든 패널의 신선도다.
//
// Stale=false 인 이유: 큐·판단·원장은 DB 가 정본이라 지금 읽은 값이 곧 현재 사실이다.
// git 파생이 아닌 것까지 상시 "낡음"으로 칠하면 그 표시의 판별력이 0이 된다.
func DBFreshness(now time.Time) model.Freshness {
	return model.Freshness{Source: "db", ObservedAt: now, Stale: false}
}

// ─────────────────────────────────────────────────────────────────────────────
// 스냅숏 낡음 — 판정을 자동으로 붙인다(설계 §3)
// ─────────────────────────────────────────────────────────────────────────────

// SnapshotState 는 스냅숏 수치를 지금 믿어도 되는지다.
type SnapshotState string

const (
	SnapshotCurrent SnapshotState = "current" // 판정 당시 입력이 지금 입력과 같다
	SnapshotStale   SnapshotState = "stale"   // 다르다 — 이 숫자는 낡았다
	SnapshotUnknown SnapshotState = "unknown" // 대조할 축이 없다. **현재라고 읽히면 안 된다**
)

// SnapshotVerdict 는 낡음 판정이다. 불리언이 아니라 **사유**를 담는다.
type SnapshotVerdict struct {
	State  SnapshotState
	Reason string // 항상 채운다
}

// Warn 은 화면에 주의 표시를 붙일지다. unknown 도 붙인다 —
// "대조 못 했다"가 "현재다"로 읽히면 근거 없는 숫자가 근거 있는 척한다.
func (v SnapshotVerdict) Warn() bool { return v.State != SnapshotCurrent }

// JudgeSnapshot 은 보관된 input_digest 를 현재 입력과 대조한다. 순수 함수다.
//
// ★ 세 값을 가른다. 불리언으로 접으면 "대조해 보니 같다"와 "대조할 것이 없다"가 같은 초록이 되고,
// 그러면 근거 없는 숫자가 이 표에서 가장 믿음직하게 보인다(그 숫자를 손으로 올리지 말라는
// 규율이 스키마의 CHECK 가 된 것과 같은 이유다).
//
// 비교는 공백을 걷어낸 **정확 일치**다. 접두 일치(짧은 sha)를 같다고 보지 않는다 —
// 그렇게 하면 무엇을 대조했는지가 값마다 달라지고, 판정의 뜻이 흐려진다.
func JudgeSnapshot(inputDigest, currentDigest string) SnapshotVerdict {
	in, cur := strings.TrimSpace(inputDigest), strings.TrimSpace(currentDigest)
	switch {
	case in == "" && cur == "":
		return SnapshotVerdict{SnapshotUnknown,
			"판정 당시 입력 해시도 현재 입력도 없다 — 이 숫자가 지금 것인지 말할 수 없다"}
	case in == "":
		return SnapshotVerdict{SnapshotUnknown,
			"판정 당시 입력 해시가 비어 있다 — 대조할 축이 없다"}
	case cur == "":
		return SnapshotVerdict{SnapshotUnknown,
			"현재 입력을 못 읽었다(기본 브랜치 ref 관측 없음) — 대조할 축이 없다"}
	case in == cur:
		return SnapshotVerdict{SnapshotCurrent,
			"판정 당시 입력이 현재 입력과 같다(" + short(cur) + ")"}
	default:
		return SnapshotVerdict{SnapshotStale,
			"이 숫자는 낡았다 — 판정 당시 입력 " + short(in) + ", 현재 입력 " + short(cur)}
	}
}

// short 는 sha 류를 짧게 줄인다. 원문이 짧으면 그대로 둔다.
func short(s string) string {
	rs := []rune(s)
	if len(rs) <= 12 {
		return s
	}
	return string(rs[:12]) + "…"
}

// ─────────────────────────────────────────────────────────────────────────────
// 자원 임계 — 경고는 여기에만 붙는다
// ─────────────────────────────────────────────────────────────────────────────

// ResourceAlert 는 자원 한 축의 상태다. Text 는 항상 채운다.
type ResourceAlert struct {
	Level string // ok | warn | crit | unknown
	Text  string
}

// JudgeDisk 는 디스크 여유를 판정한다. 순수 함수다.
//
// ★ known=false 를 0% 로 접지 않는다. 0% 는 "가득 찼다"는 값이고 "못 쟀다"는 값이 아니다.
// 뭉개면 못 재는 플랫폼에서 상시 빨간불이 되고, 상시 점등된 경고는 판별력이 0이다.
func JudgeDisk(known bool, freePct float64) ResourceAlert {
	if !known {
		return ResourceAlert{"unknown", "디스크 여유를 못 쟀다 — 이 축은 지금 관측되지 않는다"}
	}
	pct := fmt.Sprintf("%.1f%%", freePct)
	switch {
	case freePct < 5:
		return ResourceAlert{"crit", "디스크 여유 " + pct + " — 임계(5%) 아래다"}
	case freePct < 15:
		return ResourceAlert{"warn", "디스크 여유 " + pct + " — 주의(15%) 아래다"}
	default:
		return ResourceAlert{"ok", "디스크 여유 " + pct}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 탈락 사유 분포 — 큐가 블랙박스가 되지 않게 하는 축(설계 §3·§10)
// ─────────────────────────────────────────────────────────────────────────────

// ReasonCount 는 탈락 사유 하나의 집계다.
type ReasonCount struct {
	Reason  string
	Count   int
	Items   []string // 예시(최대 3건). 사유만 있고 대상이 없으면 되짚을 수가 없다
	Example string   // 그 사유의 상세 한 줄
}

// RejectionStats 는 분포 한 벌이다.
type RejectionStats struct {
	Evals   int // 판정 회수
	Picked  int // 그중 추천이 나온 회수
	None    int // 적격 0건이었던 회수
	Total   int // 탈락 줄 수(not-top 제외)
	NotTop  int // 적격이었으나 추천 순위 밖 — **거르는 축이 아니다**
	Reasons []ReasonCount
	Since   time.Time
	Until   time.Time
}

// RejectionDistribution 은 pick_eval 기록에서 사유 분포를 만든다. 순수 함수다.
//
// 정렬은 건수 내림차순, 같으면 사유 이름 오름차순이다 — 순서가 흔들리면
// 화면을 두 번 보는 사람이 같은 표를 다른 표로 읽는다.
//
// ★ judge.RejectNotTop 은 분포에서 뺀다. 그 코드는 **거르는 축이 아니라**
// "적격이었으나 추천 1건에 못 들었다"는 원장 완결용 줄이고(judge/eligible.go 의 불변식),
// 섞어 세면 가장 흔한 코드가 항상 그것이 되어 분포가 아무것도 말하지 못한다.
// 버리지는 않는다 — NotTop 으로 따로 낸다.
func RejectionDistribution(evals []model.PickEval) RejectionStats {
	st := RejectionStats{Evals: len(evals)}
	idx := map[string]*ReasonCount{}
	for _, e := range evals {
		if e.Picked != "" {
			st.Picked++
		} else {
			st.None++
		}
		if !e.At.IsZero() {
			if st.Since.IsZero() || e.At.Before(st.Since) {
				st.Since = e.At
			}
			if e.At.After(st.Until) {
				st.Until = e.At
			}
		}
		for _, r := range e.Rejected {
			code := strings.TrimSpace(r.Reason)
			if code == judge.RejectNotTop {
				st.NotTop++
				continue
			}
			st.Total++
			if code == "" {
				// 사유 코드가 빈 줄은 버리지 않고 이름을 붙여 센다.
				// 버리면 "사유를 안 남긴 판정"이 통계에서 사라져 그 결함이 영영 안 보인다.
				code = "(사유 코드 없음)"
			}
			rc, ok := idx[code]
			if !ok {
				rc = &ReasonCount{Reason: code}
				idx[code] = rc
			}
			rc.Count++
			if r.Item != "" && len(rc.Items) < 3 {
				rc.Items = append(rc.Items, r.Item)
			}
			if rc.Example == "" {
				rc.Example = r.Detail
			}
		}
	}
	st.Reasons = make([]ReasonCount, 0, len(idx))
	for _, rc := range idx {
		st.Reasons = append(st.Reasons, *rc)
	}
	sort.Slice(st.Reasons, func(i, j int) bool {
		if st.Reasons[i].Count != st.Reasons[j].Count {
			return st.Reasons[i].Count > st.Reasons[j].Count
		}
		return st.Reasons[i].Reason < st.Reasons[j].Reason
	})
	return st
}

// ─────────────────────────────────────────────────────────────────────────────
// 잡동사니
// ─────────────────────────────────────────────────────────────────────────────

// AfterLabel 은 선행 조건 하나를 사람이 읽는 한 줄로 옮긴다. 순수 함수다.
//
// 축이 하나도 안 채워진 행을 침묵으로 넘기지 않는다 — 그 행은 스키마 CHECK 가 막는 모양이라
// 화면에 나타났다면 그 자체가 사고 신호다.
func AfterLabel(a model.After) string {
	switch {
	case a.Item != "":
		return "항목 " + a.Item
	case a.Job != "":
		return "잡 " + a.Job
	case a.SHA != "":
		return "커밋 " + short(a.SHA) + "@landed"
	default:
		return "빈 선행 — 축이 하나도 안 채워졌다(스키마 CHECK 가 막는 모양이다)"
	}
}

// Clip 은 화면에 실을 외부 문자열을 자르고 제어문자를 걷어낸다. 순수 함수다.
//
// 이스케이프는 html/template 이 하고 여기서는 **길이와 제어문자만** 다룬다.
// 둘을 한 함수에 섞으면 어느 쪽이 지켜졌는지 시험이 말할 수 없다.
func Clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}

// ─────────────────────────────────────────────────────────────────────────────
// 종료 선언 — 닫으려다 롤백된 시도
// ─────────────────────────────────────────────────────────────────────────────

// CloseDeclUnread 는 "이 축을 못 읽었다"는 센티널이다.
//
// ★ 빈 문자열과 **반드시 갈라야 한다.** 빈 문자열은 "원장을 읽었고 선언이 0건이다"이고
// 이것은 "원장을 아예 못 읽었다"다. 둘을 한 값으로 접으면 조회가 죽은 화면이 그 항목을
// "깨끗하다"고 말하게 되고, 그 거짓말이 정확히 이 축이 막으려는 사고다.
// 같은 층의 선례가 ItemRow.Dependents 의 -1 이다(page.go 의 역인덱스 조회 실패 갈래).
const CloseDeclUnread = "?"

// CloseDeclaredLabel 은 항목 한 줄의 종료 선언 표기다. 순수 함수다.
//
// 규약 셋:
//   - read=false → CloseDeclUnread. **0건으로 접지 않는다.**
//   - 선언 0건 → 빈 문자열. 화면에 아무것도 안 낸다(없는 것에 자리를 주지 않는다).
//   - 그 밖 → "최소 N건"으로 쓴다. store 가 낸 수는 정확한 수가 아니라 **하한**이다 —
//     flushDeferred 가 트랜잭션의 ctx 를 그대로 쓰고 LogEvent 는 쓰기 실패를 WARN 으로만
//     삼키므로, 클라이언트가 끊기면 행이 안 써진다. 문구가 그 사실을 말해야 한다.
//
// ★ created 이전의 선언은 **버린다.** item 의 PK 가 (project, id) 라 지웠다 다시 만든 id 가
// 옛 이벤트를 물려받는다. store.CloseDeclarationsByItem 이 그 앵커를 일부러 안 걸고
// 호출자에게 넘긴다고 doc 에 적어 뒀다("이 함수는 원자료만 낸다").
//
// ★ 앵커는 Last 하나로만 건다. 집계가 이미 mode 별 수로 접혀 있어 그보다 정밀하게 못 자른다 —
// **되살아난 id 에 옛 선언이 섞여 수만 부푼 경우는 이 함수가 못 가른다.** 정직하게 적는다:
// 안 적으면 다음 세션이 이 축을 완전한 것으로 믿는다.
//
// ★ 시각을 못 읽은 선언에는 앵커를 안 건다. 그것을 "항목보다 옛것"으로 몰면 관측하지 않은
// 사실을 단정하는 것이다 — 그때는 버리지 않고 시각만 미상으로 낸다. (이 규율은 아래
// 동시각 규율과 **다른** 규율이다 — Last 가 zero 인 갈래와 Last 가 created 와 같은
// 갈래를 같은 조건으로 묶지 않는다.)
//
// ★ 경계는 **동시각 포함**이다 — service.closeDeclarations(pick.go:817)와 글자로
// 맞춘다: `!d.Last.After(c.Item.CreatedAt)` 면 버린다. 항목이 있어야 닫을 수 있으니
// 동시각은 이 화신의 선언일 수 없고, 애매한 쪽은 하한으로 접는 것이 이 축의 규율이다
// (pick_wiring_test.go:269 의 "생성과 같은 시각은 안 센다" 가 그 경계를 이름 붙여
// 못박았다). 예전에는 여기가 `Before` 로만 걸러 동시각을 남겼다 — 같은 사실에
// service 와 web 두 표면이 다른 답을 내는 병이었다.
func CloseDeclaredLabel(d model.CloseDeclaration, read bool, created time.Time) string {
	if !read {
		return CloseDeclUnread
	}
	if d.Count() == 0 {
		return ""
	}
	if !created.IsZero() && !d.Last.IsZero() && !d.Last.After(created) {
		return ""
	}
	last, mode, sess := "마지막 시각 미상", "mode 미상", "세션 미상"
	if !d.Last.IsZero() {
		last = "마지막 " + d.Last.Format("01-02 15:04")
	}
	if d.LastMode != "" {
		mode = "mode=" + Clip(d.LastMode, 16)
	}
	if d.LastSession != "" {
		sess = "세션 " + short(Clip(d.LastSession, 64))
	}
	return fmt.Sprintf("종료 선언 최소 %d건(done %d · dropped %d) — %s · %s · %s",
		d.Count(), d.Done, d.Dropped, last, mode, sess)
}
