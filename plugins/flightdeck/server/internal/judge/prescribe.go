package judge

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// 처방 — 사건이 남게 만드는 강제 지점(설계 §6 "규율은 응답에 싣는다 — 필요할 때만, 그 자리에서").
//
// ★ 상태가 아니라 **전이**에서만 뜬다. (세션 × Key) 당 1회다.
// 조건이 지속되는 동안 매번 뜨면 설계 §4 가 고발한 실패를 재현한다 —
// 살아 있는 5건 중 3건에 경고가 붙어 판별력이 0이 된 그 화면이다.

const (
	PrescribeOverlap   = "overlap"   // overlap:<상대 세션 id>
	PrescribeOutside   = "outside"   // outside:<경로>
	PrescribeSilent    = "silent"    // 대상 없음
	PrescribeUnclaimed = "unclaimed" // 대상 없음
)

// PrescribeMax 는 한 번에 **문구로** 내는 처방 수다. 넘는 것은 요약 한 줄이 되지만
// **키는 호출자가 전부 발화 기록한다** — 요약된 것도 이미 낸 것이다.
// 대규모 리팩터 한 턴이 outside 처방 수십 건을 쏟는 경로를 이 상수가 막는다.
//
// ★ **이 값에도 근거가 없다.** 위 시나리오(리팩터 한 턴이 outside 수십 건을 쏟는다)는
// 추론이지 실측이 아니다 — 아래 SilentNewPaths·SilentGap, service.go 의
// DefaultLiveWindow 와 같은 처지의 잠정값이다. 처방 발화·확인율이 실측되면(설계 §10)
// 이 상수도 그때 조정한다.
const PrescribeMax = 3

// silent 임계.
//
// ★ **이 두 값에는 근거가 없다.** 창 2시간과 같은 성질의 잠정값이고, 발화율이 실측되면
// 조정한다. 근거 없는 상수를 근거 있는 척 두지 않는 것이 설계 §10 이 요구한 것이다 —
// 그 절이 고발한 것은 "락 다섯 중 ttl 근거 주석이 있는 것이 하나뿐"이었다.
//
// ★ 그리고 **`tool` 신호 횟수는 쓸 수 없다.** signal 표의 PK 가 (session_id, kind) 라
// 종류별 한 행이고 갱신된다 — 횟수라는 값이 존재하지 않는다(schema.sql:91-96).
const (
	SilentNewPaths = 12
	SilentGap      = 60 * time.Minute
)

// ClaimView 는 이 세션이 쥔 항목 하나와 그 항목이 선언한 경로다.
type ClaimView struct {
	ItemID string
	Paths  []string
}

// PrescribeInput 은 처방 판정에 필요한 전부다. I/O 도 상태도 없다.
type PrescribeInput struct {
	Now       time.Time
	SessionID string
	Claims    []ClaimView
	// TurnPaths 는 마지막 처방 이후 새로 만진 경로다(처방이 없었으면 세션 시작 이후).
	TurnPaths []string
	// Others 는 살아 있는 세션 목록이다. 자기 자신이 섞여 있어도 이 함수가 뺀다 —
	// 호출자가 빼는 것에 의존하면 그 축이 시험 밖에 있게 된다.
	Others []LiveSession
	// LastJudgment 는 이 세션의 마지막 판단 시각이다.
	// **판단이 하나도 없으면 호출자가 세션 시작 시각을 넣는다** — 제로값이면 기준이 없어 안 뜬다.
	LastJudgment time.Time
	// NewPaths 는 마지막 판단 이후 새로 만진 경로 수다.
	NewPaths int
	// Emitted 는 이미 낸 키 → 낸 시각이다.
	//
	// ★ 불리언이 아니라 시각인 이유: 억제 해제 규칙(silent 은 판단 뒤 다시 뜬다)이
	// Emitted[key] 와 LastJudgment 의 대소 비교로 표현된다. 불리언으로 받으면 그 규칙이
	// 서비스 계층으로 새어 나가고, 그러면 §12 가 금지한 "시험이 사본을 단정하는" 모양이 된다.
	Emitted map[string]time.Time
}

// Prescription 은 처방 하나다.
type Prescription struct {
	Key    string `json:"key"`    // 억제 단위이자 전이 식별자
	Reason string `json:"reason"` // 왜 떴는가. 시험이 단정하는 축이다
	Text   string `json:"text"`   // 세션에게 낼 문구
}

// Prescribe 는 지금 내야 할 처방 전부를 낸다. 표시 상한은 FoldPrescriptions 가 건다.
//
// 순서는 고정이다: overlap → outside → unclaimed → silent.
// overlap 이 맨 앞인 이유는 **그것만이 남이 알아야 하는 사건**이기 때문이다.
// 나머지 셋은 이 세션의 규율 축이라 접혀도 남의 화면이 틀리지 않는다.
func Prescribe(in PrescribeInput) []Prescription {
	var out []Prescription
	out = append(out, overlapPrescriptions(in)...)
	out = append(out, outsidePrescriptions(in)...)
	if p, ok := unclaimedPrescription(in); ok {
		out = append(out, p)
	}
	if p, ok := silentPrescription(in); ok {
		out = append(out, p)
	}
	return out
}

// FoldPrescriptions 는 표시분과 접힌 수를 가른다. 순서는 Prescribe 가 정한 그대로다.
func FoldPrescriptions(ps []Prescription) (shown []Prescription, folded int) {
	if len(ps) <= PrescribeMax {
		return ps, 0
	}
	return ps[:PrescribeMax], len(ps) - PrescribeMax
}

// ① 남과 경로가 겹치기 시작했다 — 상대마다 1회.
func overlapPrescriptions(in PrescribeInput) []Prescription {
	others := append([]LiveSession(nil), in.Others...)
	// 순서를 고정한다 — 같은 입력에 같은 출력이어야 시험이 순서를 단정할 수 있다.
	sort.Slice(others, func(i, j int) bool { return others[i].ID < others[j].ID })

	var out []Prescription
	for _, o := range others {
		if o.ID == in.SessionID {
			continue
		}
		pairs := OverlapPairs(in.TurnPaths, o.Paths)
		if len(pairs) == 0 {
			continue
		}
		key := PrescribeOverlap + ":" + o.ID
		if suppressed(in, key) {
			continue
		}
		mine, theirs := pairs[0][0], pairs[0][1]
		out = append(out, Prescription{
			Key: key,
			Reason: fmt.Sprintf("이번에 만진 %s 가 세션 %s 의 발자국 %s 와 겹친다(겹친 쌍 %d)",
				mine, o.ID, theirs, len(pairs)),
			Text: fmt.Sprintf(
				"%s 를 만졌는데 세션 %s%s 도 %s 를 잡고 있다.\n"+
					"  → note(kind='ask', body='무엇을 왜 잡는가') 로 의도를 남겨라. "+
					"그 세션의 다음 프롬프트에 배달된다.",
				mine, o.ID, labelSuffix(o.Label), theirs),
		})
	}
	return out
}

// ② 선점한 항목의 선언 경로 밖 — 경로마다 1회.
//
// ★ 선언 경로가 하나도 없으면 이 축은 **안 돈다.** 빈 선언에 대고 "밖"을 판정할 수 없고,
// 빈 선언을 "전부 밖"으로 접으면 paths 를 안 적은 항목 하나가 첫 턴에 처방을 쏟는다.
func outsidePrescriptions(in PrescribeInput) []Prescription {
	declared := declaredPaths(in.Claims)
	if len(declared) == 0 {
		return nil
	}
	ids := claimIDs(in.Claims)
	var out []Prescription
	for _, p := range in.TurnPaths {
		if PathsOverlap([]string{p}, declared) {
			continue
		}
		key := PrescribeOutside + ":" + p
		if suppressed(in, key) {
			continue
		}
		out = append(out, Prescription{
			Key:    key,
			Reason: fmt.Sprintf("%s 는 선점 항목 %s 의 선언 경로(%s) 밖이다", p, ids, strings.Join(declared, " ")),
			Text: fmt.Sprintf(
				"%s 는 선점한 %s 가 선언한 경로 밖이다 — 남이 보는 겹침 판정의 입력이 낡았다.\n"+
					"  → 같은 작업이면 note(kind='decision') 으로 범위가 왜 늘었는지 남겨라.\n"+
					"  → 별개 작업이면 add(id=…, title=…, body=…, paths=['%s']) 로 항목을 만들어라.",
				p, ids, p),
		})
	}
	return out
}

// ③ 선점 없이 편집 — 세션당 1회.
//
// ★ 이 조건은 흔하다. 세션당 1회로 눌러 잡지 않으면 편집마다 떠서 §4 의 실패가 된다.
func unclaimedPrescription(in PrescribeInput) (Prescription, bool) {
	if len(in.Claims) > 0 || len(in.TurnPaths) == 0 || suppressed(in, PrescribeUnclaimed) {
		return Prescription{}, false
	}
	return Prescription{
		Key:    PrescribeUnclaimed,
		Reason: fmt.Sprintf("선점 0건인데 경로 %d개를 편집했다", len(in.TurnPaths)),
		Text: fmt.Sprintf(
			"항목을 선점하지 않고 %s 를 고치고 있다 — 큐에도 카드에도 무엇을 하는지가 없다.\n"+
				"  → pick(item_id=…) 로 집거나, 큐 밖 작업이면 note(kind='decision') 으로 "+
				"무엇을 왜 하는지 남겨라.",
			strings.Join(clipList(in.TurnPaths, 3), ", ")),
	}, true
}

// ④ 오래 일했는데 판단이 0건.
func silentPrescription(in PrescribeInput) (Prescription, bool) {
	reason, ok := silentReason(in)
	if !ok || suppressed(in, PrescribeSilent) {
		return Prescription{}, false
	}
	return Prescription{
		Key:    PrescribeSilent,
		Reason: reason,
		Text: "일한 뒤로 판단이 하나도 안 남았다 — 판단은 원리적으로 파생 불가한 유일한 자산이다(설계 §5).\n" +
			"  → note(kind='decision', body='무엇을 정했고 무엇을 기각했나') 로 남겨라.",
	}, true
}

// silentReason 은 판단 공백 조건과 그 사유다.
//
// ★ 시간 팔에 "새 경로 ≥ 1" 이 붙는다. 순수 OR 로 두면 조사만 하는 세션이 60분마다 걸리는데,
// 그 세션은 경로 축에서 아무도 안 막으므로 찌를 이유가 없다(설계 §5).
func silentReason(in PrescribeInput) (string, bool) {
	if in.NewPaths >= SilentNewPaths {
		return fmt.Sprintf("마지막 판단 이후 새 경로 %d개(임계 %d)", in.NewPaths, SilentNewPaths), true
	}
	if in.NewPaths == 0 || in.LastJudgment.IsZero() {
		return "", false
	}
	if gap := in.Now.Sub(in.LastJudgment); gap >= SilentGap {
		return fmt.Sprintf("마지막 판단 후 %d분(임계 %d분) · 그 사이 새 경로 %d개",
			int(gap.Minutes()), int(SilentGap.Minutes()), in.NewPaths), true
	}
	return "", false
}

// suppressed 는 이 키가 지금 눌려 있는지 본다.
//
// ★ silent 만 판단 뒤에 풀린다. 판단을 남기는 세션은 다시 조용해졌을 때 한 번 더 찔러야 하고,
// 한 번 무시한 세션을 계속 찌르는 것은 §4 가 고발한 상시 점등이다. **재촉은 이 설계에 없다.**
func suppressed(in PrescribeInput, key string) bool {
	at, ok := in.Emitted[key]
	if !ok {
		return false
	}
	if key != PrescribeSilent {
		return true
	}
	return !in.LastJudgment.After(at)
}

func declaredPaths(claims []ClaimView) []string {
	var out []string
	for _, c := range claims {
		out = append(out, c.Paths...)
	}
	return out
}

func claimIDs(claims []ClaimView) string {
	ids := make([]string, 0, len(claims))
	for _, c := range claims {
		ids = append(ids, c.ItemID)
	}
	return strings.Join(ids, ", ")
}

func labelSuffix(label string) string {
	if strings.TrimSpace(label) == "" {
		return ""
	}
	return "(" + label + ")"
}

func clipList(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return append(append([]string(nil), xs[:n]...), fmt.Sprintf("외 %d개", len(xs)-n))
}
