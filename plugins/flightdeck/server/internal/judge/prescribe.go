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
// ★ **위 시나리오는 실측으로 반증됐다**(fd-prescribe-threshold-baseline, 2026-08-04.
// 발화 55건·33시간). outside 는 전 기간 통틀어 **2건** 떴고 둘 다 확인됐다 —
// "리팩터 한 턴이 outside 수십 건을 쏟는다"는 일이 일어난 적이 없다.
// 실제로 접힌 것은 처방이 뜬 턴 35개 중 **2개**뿐이고, 한 턴의 최대 발화는 6건이었다.
//
// ★ 그래서 3 을 남기되 **근거를 바꿔 적는다.** 이 상수가 막는 것은 outside 폭주가 아니라
// overlap 폭주다 — 발화 55건 중 31건이 overlap 이고 세션 7개에 몰렸다.
// 상한을 올릴 이유는 없다: 접기가 두 턴에서만 걸렸으므로 3 은 이미 느슨하다.
const PrescribeMax = 3

// silent 임계.
//
// ★ **실측된 값이다**(fd-prescribe-threshold-baseline, 2026-08-04. 방법은 설계 §10).
// 각 임계가 자를 대상의 분포를 재고 그 분위수로 위치를 확인했다.
//
//	판단 사이 새 경로 109구간 — p50 0 · p75 3 · p90 6 · p95 15 · max 49
//	  → 임계 12 는 구간의 6.4% 를 자른다(≈p94)
//	판단 사이 간격 62구간 — p50 8.0m · p75 20.1m · p90 45.9m · p95 1.27h · max 9.33h
//	  → 임계 60분은 구간의 8.1% 를 자른다(≈p92)
//
// 둘 다 이미 꼬리만 자르고 있어 그대로 둔다. 실제 발화도 33시간 동안 9건 · 세션 9개로
// 세션당 1건 미만이다.
//
// ★ **확인율이 낮은데 임계를 안 줄인 이유를 여기 남긴다.** silent 확인율은 1/9(11%)이고,
// 발화 뒤 판단이 실제로 남은 것도 6건 중 1건이다. 설계 §10 은 "떨어지면 조건을 줄인다"고
// 했지만, 이 경우 임계를 올려도 고쳐지지 않는다 — 발화가 이미 세션당 1건 미만이라
// 더 줄이면 처방이 사라질 뿐 확인율은 안 오른다. **낮은 확인율의 원인이 임계가 아니다.**
// ★ **정정(2026-08-05).** 위 문단은 원래 "확인율의 병목은 overlap 이고 거기서 무시가
// 학습된다"고 적었다. **틀렸다.** overlap 확인율 0/31 을 재보니 무시가 아니라 **구조**였다:
//
//	카드 133장 — 발자국만 있고 판단 0인 카드 51장 · 판단만 있고 발자국 0인 카드 46장 · 둘 다 6장
//	overlap 을 맞은 카드 7장은 전부 발자국 1~62개에 판단 0. 그중 셋은 형제 카드가 판단 12~31개를 가졌다
//
// 발자국은 훅이 쓰고 판단은 MCP 가 쓴다. 한 대화의 정체가 카드 둘로 갈리면 처방은
// **발자국 카드**에서 뜨고 ack 은 **판단 카드**에 꽂힌다 — 그래서 overlap 확인율은
// 행동이 아니라 **원리적으로** 0이다. 문구를 고쳐도 임계를 올려도 안 움직인다.
//
// 즉 확인율은 지금 "세션이 처방을 따르나"가 아니라 "카드가 안 갈렸나"를 재고 있다.
// 갈림의 원인은 07e5df4·4de4b21 이 고쳤으므로 이 수치는 그 뒤로 다시 재야 뜻이 생긴다.
//
// ★ 그리고 **`tool` 신호 횟수는 쓸 수 없다.** signal 표의 PK 가 (session_id, kind) 라
// 종류별 한 행이고 갱신된다 — 횟수라는 값이 존재하지 않는다(schema.sql:91-96).
// 위 실측이 event 표를 쓴 이유가 이것이다.
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
	// Closed 는 이 구간에 이 세션이 **제대로 끝낸** 항목과 그 선언 경로다.
	//
	// ★ 왜 별도 축인가. finish 는 선점을 반납하므로 그 직후 Claims 가 빈다. 그러면
	// 방금 끝낸 그 일의 경로를 근거로 unclaimed 가 뜬다 — 가장 성실하게 마무리한 세션이
	// 가장 확실하게 잔소리를 듣는다. "한 번도 안 집었다"와 "방금 제대로 끝냈다"는
	// 다른 상태인데 `len(Claims)==0` 하나로는 안 갈린다. 그 둘을 가르는 축이다.
	//
	// Claims 와 합치지 않는다 — 합치면 outside 처방이 이미 닫힌 항목의 선언 경로를 기준으로
	// 돌게 되고, 끝낸 항목이 살아 있는 항목처럼 남의 겹침 판정에 계속 끼어든다.
	Closed []ClaimView
	// TurnPaths 는 마지막 처방 이후 새로 만진 경로다(처방이 없었으면 세션 시작 이후).
	TurnPaths []string
	// Others 는 살아 있는 세션 목록이다. 자기 자신이 섞여 있어도 이 함수가 뺀다 —
	// 호출자가 빼는 것에 의존하면 그 축이 시험 밖에 있게 된다.
	//
	// ★ "자기 자신"은 **카드 id 가 아니라 대화**다. 정체가 3중키라 한 대화가 카드 여러 장이
	// 될 수 있고, 카드 id 만 빼면 나머지 형제 카드가 남의 세션처럼 보인다(sameConversation).
	Others []LiveSession
	// SelfCC 는 이 세션이 속한 대화의 cc id 다. 형제 카드를 가려내는 데만 쓴다.
	SelfCC string
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
		if o.ID == in.SessionID || sameConversation(in.SelfCC, o.CCSessionID) {
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

// sameConversation 은 두 카드가 **같은 대화**인지다. 순수 함수다.
//
// ★ cc 동등만 본다. 접두 일치도 워크트리 조상 관계도 쓰지 않는다 —
// DESIGN §3 이 일부러 없앤 축이고, 되살리면 서로 다른 두 대화가 한 트리 안에서
// 일할 때(이 제품의 정상 흐름이다: `.flightdeck/worktrees/<브랜치>`) 진짜 겹침이 통째로 꺼진다.
// 실측으로 확인했다: 그런 상하위 쌍 17건은 **전부 다른 대화**였고 병합 때 실제로 충돌한다.
//
// ★ 빈 값끼리는 같지 않다. "못 읽었다"를 "같다"로 접으면 관측이 깨진 순간
// 겹침 축이 조용히 전부 꺼진다 — 이 레포가 반복해서 겪은 실패 모양이다.
func sameConversation(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	return a != "" && a == b
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
//
// ★ **"선점 0건"이 셋을 뭉갠다.** 한 번도 안 집었다(처방이 맞다) · 집고 일하는 중
// (Claims 가 막는다) · **방금 제대로 끝냈다**(처방이 틀리다). finish 가 선점을 반납하므로
// 셋째가 첫째와 똑같이 보이고, 그래서 성실하게 마무리한 세션이 잔소리를 듣는다.
// coveredByClosed 가 그 셋째만 갈라낸다.
func unclaimedPrescription(in PrescribeInput) (Prescription, bool) {
	if len(in.Claims) > 0 || len(in.TurnPaths) == 0 || suppressed(in, PrescribeUnclaimed) {
		return Prescription{}, false
	}
	if coveredByClosed(in) {
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

// coveredByClosed 는 이번 구간에 만진 경로가 **전부** 방금 끝낸 항목의 선언 경로 안인지 본다.
//
// ★ 전부여야 한다. 하나라도 밖이면 그것은 새 일이고, 새 일에 대고 "무엇을 하는지가 큐에 없다"는
// 여전히 참이다 — 부분 일치로 접으면 큰 항목 하나를 닫은 세션이 그 뒤 무엇을 하든 안 걸린다.
//
// ★ 선언 경로가 없는 항목은 접을 근거를 못 준다. 빈 선언을 "전부 덮음"으로 읽으면
// paths 를 안 적은 항목 하나가 이 축을 통째로 끈다 — outsidePrescriptions 가 빈 선언에
// 대고 "밖"을 판정하지 않는 것과 같은 이유다.
func coveredByClosed(in PrescribeInput) bool {
	declared := declaredPaths(in.Closed)
	if len(declared) == 0 {
		return false
	}
	// ★ **비교 불가능한 좌표의 경로는 근거로 쓰지 않는다.**
	//
	// 관측 경로는 RelPath(카드의 워크트리, p) 가 만드는데, 카드의 워크트리 밖 경로는
	// 절대경로 그대로 남는다(service.RelPath). 선언 경로는 언제나 저장소 상대다.
	// pathRelated 는 성분을 **앞에서부터** 맞추므로 그 둘은 원리적으로 절대 안 맞는다 —
	// 즉 절대경로가 하나라도 섞이면 이 가드가 통째로 무력해진다.
	// 실측(2026-08-05): observed 발자국 406개 중 108개(27%)가 절대경로다.
	//
	// 그것을 "안 덮였다"로 세면 **가장 성실하게 마무리한 세션이 잔소리를 듣는다** —
	// 이 함수가 막으라고 있는 바로 그 결과다. "못 읽었다"는 "없다"가 아니라는
	// 같은 규율이 경로 실재 축(judge/itempaths.go 의 PathUnknown)에도 있다.
	//
	// 다만 비교 가능한 것이 **하나도 없으면** 덮였다고 말하지 않는다. 근거가 0인 것을
	// 통과로 접으면 이번엔 반대로 처방이 통째로 꺼진다.
	comparable := 0
	for _, p := range in.TurnPaths {
		if !comparablePath(p) {
			continue
		}
		comparable++
		if !PathsOverlap([]string{p}, declared) {
			return false
		}
	}
	return comparable > 0
}

// comparablePath 는 그 경로가 선언 경로와 **같은 좌표계**에 있는지다. 순수 함수다.
//
// 선언 경로는 저장소 상대다. 절대경로는 카드의 워크트리 밖이라는 뜻이고(RelPath 가 그때만
// 원본을 남긴다), 그 좌표는 이 판정이 읽을 수 있는 것이 아니다.
func comparablePath(p string) bool {
	p = strings.TrimSpace(p)
	return p != "" && !strings.HasPrefix(p, "/")
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
