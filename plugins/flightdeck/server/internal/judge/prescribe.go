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
	PrescribeLaneTurn  = "lane-turn" // lane-turn:<줄 행 id>
	PrescribeOverlap   = "overlap"   // overlap:<상대 세션 id>
	PrescribeOutside   = "outside"   // outside:<경로>
	PrescribeSilent    = "silent"    // 대상 없음
	PrescribeUnclaimed = "unclaimed" // 대상 없음
)

// PrescribeMax 는 한 번에 **문구로** 내는 처방 수다. 넘는 것은 요약 한 줄이 된다.
// 대규모 리팩터 한 턴이 outside 처방 수십 건을 쏟는 경로를 이 상수가 막는다.
//
// ★ **접힌 것은 발화로 안 센다(2026-08-06 개정).** 앞선 판은 여기에 "키는 호출자가 전부
// 발화 기록한다 — 요약된 것도 이미 낸 것이다"를 계약으로 적었고, 그 계약이 접힌 처방을
// **영구히 지웠다**: 기록되면 suppressed 가 그 키를 누르는데 해제 규칙은 silent 에만 있다.
// 세션은 그 문구를 한 번도 못 보고, 원장에는 "정상적으로 접혔다"로만 남는다.
//
// 상한은 그래도 무의미해지지 않는다 — **순환한다.** 표시된 것만 눌리므로 같은 조건이
// 다음 턴에 다시 오면 접혔던 것이 올라오고, 그 뒤에는 전부 눌린다. 설계 §4 의 상시 점등은
// **같은 것이 매 턴 반복되는 것**이라 이 동작과 다르다(service 쪽 시험
// TestFoldedPrescriptionsAreNotRecordedAndComeBack 이 그 경계를 단정한다).
//
// ★ **위 시나리오는 실측으로 반증됐다**(fd-prescribe-threshold-baseline, 2026-08-04.
// 발화 55건·33시간). outside 는 전 기간 통틀어 **2건** 떴고 둘 다 확인됐다 —
// "리팩터 한 턴이 outside 수십 건을 쏟는다"는 일이 일어난 적이 없다.
// 실제로 접힌 것은 처방이 뜬 턴 35개 중 **2개**뿐이고, 한 턴의 최대 발화는 6건이었다.
//
// ★ **재측(2026-08-06) — 위 "35턴 중 2개"는 이제 낡았다.** 표본이 발화 55건에서 239건으로
// 늘어난 뒤 다시 쟀다(턴 = 같은 세션·같은 초의 prescribe 이벤트 묶음):
//
//	턴 129개 중 접힌 턴 **15개**(11.6%) · 한 턴 최대 **7건**
//	접혀서 사라지던 축  overlap 11 · unclaimed 11 · silent 4 · outside 2
//
// ★ **늘어난 원인은 `lane-turn` 이 아니다 — 표본이다.** 원장에 lane-turn 처방은 **전 기간
// 0건**이다(`kind='prescribe'` 257건 시점의 축 분포: overlap 138 · unclaimed 67 · silent 37 ·
// outside 15). 코드도 상시가 아니라 줄 행마다 1회다(laneTurnPrescription 의 `LaneTurnRow <= 0`
// 조기 반환 + suppressed). **그래서 이 축의 접힘 위험은 아직 한 번도 실측된 적이 없다** —
// 아래 소실 기구는 다른 네 축에서 관측된 것이고, lane-turn 에 대해서는 코드를 읽어 세운 추론이다.
//
// 접힘은 드문 사건이 아니었고, 사라지던 28건 중 11건(**39%**)이 `unclaimed` 로 overlap 과
// 동률이다 — silent 와 달리 해제 규칙이 없어 한 번 접히면 그 세션에서 영영 안 뜨는 축이다.
// 이 수치가 위 개정의 근거다.
//
// ★ 그래서 3 을 남기되 **근거를 바꿔 적는다.** 이 상수가 막는 것은 outside 폭주가 아니라
// overlap 폭주다 — 발화 55건 중 31건이 overlap 이고 세션 7개에 몰렸다.
// 상한을 올릴 이유는 없다: 턴의 11.6%가 접히지만 접힌 것은 다음 턴에 올라오므로,
// 3 이 하는 일은 **한 턴에 읽을 양의 제한**뿐이다.
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
	// SiblingClaims 는 **같은 대화의 다른 카드**가 지금 쥐고 있는 항목 id 다.
	//
	// ★ 왜 별도 축인가. 정체가 3중키(machine·worktree·cc)라 `git worktree add` 로 트리를
	// 옮기면 정의상 새 카드가 난다 — 그리고 그 갈림은 설계다(DESIGN §3). 그런데 선점은
	// MCP 가 쓰고(프로세스 기동 시 워크트리가 고정된다) 발자국은 훅이 쓴다(매 이벤트 cwd 로
	// 다시 푼다). 그래서 **선점은 카드 A 에, 발자국은 카드 B 에** 쌓인다. `pick` 응답 자신이
	// 워크트리 생성을 지시하고 선점이 그보다 먼저 오므로, 규율대로 일한 세션은 전부 이 배치다.
	// 실측(2026-08-06, unclaimed 처방 80건 전수): 카드 단위로 선점을 쥔 채 발화 0건,
	// 대화 단위로는 10건. 그 열 번이 이 축이 없어서 난 거짓 양성이다.
	//
	// ★ **경로를 일부러 안 싣는다. Claims 와 합치지도 않는다.** 합치면 outside 처방이
	// 형제의 선언 경로를 기준으로 돌기 시작한다 — 실측상 그 순간 11카드에 선언 경로 밖
	// 발자국 82개(한 카드 최대 45)가 켜지는데, 전 기간 outside 발화 총계가 22건이다.
	// 4배가 한 판에 쏟아진다. 타입이 []string 인 것 자체가 그 결정을 못박는다(Closed 를
	// Claims 와 안 합친 것과 같은 논거이고, 이쪽은 근거가 실측이다).
	SiblingClaims []string
	// TurnPaths 는 마지막 처방 이후 새로 만진 경로다(처방이 없었으면 세션 시작 이후).
	TurnPaths []string
	// Others 는 살아 있는 세션 목록이다. 자기 자신이 섞여 있어도 이 함수가 뺀다 —
	// 호출자가 빼는 것에 의존하면 그 축이 시험 밖에 있게 된다.
	//
	// ★ "자기 자신"은 **카드 id 가 아니라 대화**다. 정체가 3중키라 한 대화가 카드 여러 장이
	// 될 수 있고, 카드 id 만 빼면 나머지 형제 카드가 남의 세션처럼 보인다(sameConversation).
	Others []LiveSession
	// SelfCC 는 이 세션이 속한 대화의 cc id 다.
	//
	// ★ 겹침 축에서는 형제 카드를 **가려내는** 데 쓴다(남이 아니므로 조율 처방을 안 낸다).
	// 선점 축에서는 반대로 형제가 쥔 것을 **끌어오는** 근거다 — 그 결과가 SiblingClaims 다.
	// 즉 이 값은 배제자이기도 하고 판정의 근거이기도 하다. 앞선 판은 "가려내는 데만 쓴다"고
	// 적었는데, 그 문장이 참인 동안 선점 축의 거짓 양성이 살아 있었다.
	SelfCC string
	// LastJudgment 는 이 세션의 마지막 판단 시각이다.
	// **판단이 하나도 없으면 호출자가 세션 시작 시각을 넣는다** — 제로값이면 기준이 없어 안 뜬다.
	LastJudgment time.Time
	// NewPaths 는 마지막 판단 이후 새로 만진 경로 수다.
	NewPaths int
	// LaneTurnRow 는 랜딩 줄에서 **지금 이 세션 차례가 된 줄 행의 번호**다. 0 이면 차례가 아니다.
	//
	// ★ 불리언이 아니라 행 번호인 이유는 억제 키가 `lane-turn:<이 번호>` 이기 때문이다.
	// suppressed 가 silent 외 모든 키를 무조건 누르고, 발화 기록(event)은 추가 전용이며,
	// 그 기록을 읽는 창의 하한인 session.opened_at 은 재개해도 안 되돌아간다. 셋을 이으면
	// 접미 없는 `lane-turn` 하나는 **세션 카드 수명 전체에 걸쳐 정확히 1회**로 굳는다.
	// 그런데 굶주림 정책상 차례를 받고 랜딩에 실패한 세션은 맨 뒤로 가서 **새 줄 행**을 받는다 —
	// 그 세션에게 두 번째 차례는 영영 안 뜨고, 그 뒤에 선 전원이 그만큼 더 기다린다.
	// 행 번호를 키에 실으면 "같은 행에는 한 번"과 "새 행에는 다시"가 한 규칙에서 같이 나온다.
	//
	// ★ 판정 자체(맨 앞이 나인가 · 레인을 쥔 사람이 없는가)는 호출자 몫이다. 이 패키지는
	// 저장층을 모르고, 순서 집행이 걸리는 자리는 하나여야 한다.
	LaneTurnRow int64
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
// 순서는 고정이다: lane-turn → overlap → outside → unclaimed → silent.
// 앞의 둘이 앞인 이유는 **그 둘만이 남에게 걸리는 사건**이기 때문이다.
// 뒤의 셋은 이 세션의 규율 축이라 접혀도 남이 지금 보는 화면이 바로 틀리지는 않는다
// — 다만 outside 는 남이 보는 겹침 판정의 **입력**을 낡은 채로 둔다(아래 대가 문단).
//
// ★ **개정 — lane-turn 을 들이면서 맨 앞을 내줬다.** 원래 이 자리는 "overlap 이 맨 앞인
// 이유는 그것만이 남이 알아야 하는 사건이기 때문이고, 나머지 셋은 접혀도 남의 화면이
// 틀리지 않는다"였다. lane-turn 에 대해 그 둘째 문장은 거짓이고, 첫째 논거는 오히려
// overlap 보다 **강하게** 성립한다.
//
// ★ **기구가 2026-08-06 에 바뀌었다 — 영구 소실은 이제 한 턴 지연이다.** 원래 이 자리는
// "접힌 처방도 호출자가 전부 발화 기록하고 suppressed 가 silent 외를 무조건 누르므로
// 접힌 lane-turn 은 영구히 사라진다"였다. 그 계약을 뒤집었다(PrescribeMax 주석의 개정):
// **표시된 것만 발화 기록하므로** 접힌 lane-turn 은 그 턴에 안 나가고 다음 턴에 올라온다.
//
// 그래도 맨 앞은 lane-turn 이다. 남은 대가가 **여전히 이 축만 남에게 떨어지기** 때문이다 —
// 한 턴 늦는 동안 그 세션은 레인을 안 쥔 채 남고 **뒤에 선 전원의 랜딩이 그만큼 선다.**
// 다른 축의 한 턴 지연은 그 세션 자신의 규율이 늦어질 뿐 아무도 멈추지 않는다.
// 그리고 그 지연은 화면에 안 뜬다 — 원장에는 "정상적으로 접혔다"로만 남는다.
//
// ★ **대가는 치른다 — 그런데 그 대가가 떨어지는 자리를 원래 틀리게 적었다.** 이 자리는
// 원래 "상한을 넘는 턴에서 접히는 쪽이 lane-turn 이 아니라 overlap 이 된다"였다. 틀렸다:
// FoldPrescriptions 는 `ps[:PrescribeMax]` 로 **뒤를 자르므로** 접히는 것은 이 순서의
// 뒤쪽 전부다. 맨 뒤부터 silent · unclaimed · outside 가 먼저 접히고, overlap 은
// lane-turn 이 떠 있는 턴이면 **셋째 상대부터** 접힌다(상한 3 중 한 자리를 lane-turn 이 쓴다).
//
// 그래서 우리가 실제로 뒤로 민 것은 overlap 이 아니라 **그 뒤 축들**이고, 완화 근거 둘은
// 축마다 다르게 성립한다:
//
//	· **키가 쪼개져 있다** — overlap 은 상대마다, outside 는 경로마다 별개 키라
//	  접히는 것이 통지 하나지 축 전체가 아니다. 다만 overlap 이 하나라도 접히는 턴이면
//	  이미 overlap 이 적어도 둘 표시된 뒤인 반면, **lane-turn 이 뜬 턴에 overlap 이 둘만 더 떠도**
//	  outside 는 그 턴에 한 건도 표시되지 않는다. 부분 손실과 전량 손실은 다르다.
//	· **확인율** — 0/31 은 overlap 의 수치다(위 silent 임계 주석의 정정 문단. 카드가 갈려
//	  처방은 발자국 카드에 뜨고 ack 은 판단 카드에 꽂히므로 원리적으로 0). outside 에는
//	  이 근거가 **없다** — 같은 실측에서 outside 는 2건 뜨고 둘 다 확인됐다(PrescribeMax 문단).
//
// 즉 "접혀도 되는 쪽은 이미 아무도 안 읽고 있었다"는 overlap 에만 참이다. 그런데도 이 순서를
// 고르는 이유는 손실의 **크기**다: 접힌 키는 이제 안 눌리므로 **모든 축이 되돌아오지만,
// 그 축의 입력이 다시 생겨야 한다** — outside 는 같은 경로를 다시 만져야 하고 overlap 은
// 상대가 다시 나타나야 한다(`TurnPaths` 가 `f.LastAt.After(since)` 로 뽑히기 때문이다).
// 그동안 outside 한 경로가 접히면 남이 보는 겹침 판정의 입력이 그 경로만큼 낡은 채로 남고,
// lane-turn 이 접히면 레인이 빈 채로 남아 **뒤에 선 전원**이 선다. 그리고 접기는
// **드물지 않다** — 턴 129개 중 15개(11.6%)다(PrescribeMax 주석의 재측 문단).
//
// 이 순서를 잠근 것은 TestLaneTurnSurvivesFolding(맨 앞 · 접힘)과
// TestAxisOrderIsLockedWhereverTwoAxesCanCoexist(축 순서 전체)다.
func Prescribe(in PrescribeInput) []Prescription {
	var out []Prescription
	if p, ok := laneTurnPrescription(in); ok {
		out = append(out, p)
	}
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

// ① 랜딩 레인 차례가 왔다 — 줄 행마다 1회.
//
// ★ 맨 앞이다. 근거는 Prescribe 독스트링에 있다(접히면 그 줄 행의 통지가 **한 턴 늦고**
// 그동안 뒤 줄 전원이 선다). 파일 순서 == 호출 순서라 이 함수도 맨 위에 있다.
//
// ★ **0 은 "차례 아님"이다** — 그리고 호출자가 레인을 못 읽었을 때도 0 이 들어온다.
// 둘을 안 가르는 것이 맞다. "못 읽었다"를 "차례다"로 접으면 줄과 점유가 어긋난 자리로
// 세션을 보내고, 그 세션은 레인 획득이 실패하는 자리에서 처방을 믿은 대가를 치른다.
// 음수는 존재할 수 없는 값이라(줄 행 id 는 AUTOINCREMENT rowid) 같이 접는다.
func laneTurnPrescription(in PrescribeInput) (Prescription, bool) {
	if in.LaneTurnRow <= 0 {
		return Prescription{}, false
	}
	key := fmt.Sprintf("%s:%d", PrescribeLaneTurn, in.LaneTurnRow)
	if suppressed(in, key) {
		return Prescription{}, false
	}
	return Prescription{
		Key:    key,
		Reason: fmt.Sprintf("랜딩 줄 맨 앞이 이 세션이고 레인을 쥔 사람이 없다(줄 행 %d)", in.LaneTurnRow),
		Text: fmt.Sprintf(
			"랜딩 레인 차례가 왔다 — 줄 맨 앞이 너고(줄 행 %d) 레인을 쥔 사람이 없다.\n"+
				"  → land() 로 레인을 쥐고 랜딩을 시작해라. 끝나면 land(result='ok') 로 보고하고 반납한다.\n"+
				"  → 차례를 흘리면 레인은 빈 채로 남는다 — 뒤에 선 세션 전원이 그동안 못 움직인다.\n"+
				"  → 랜딩할 것이 없어졌으면 land(leave='사유') 로 줄에서 빠져라. 그것도 뒤를 푼다.",
			in.LaneTurnRow),
	}, true
}

// ② 남과 경로가 겹치기 시작했다 — 상대마다 1회.
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

// ③ 선점한 항목의 선언 경로 밖 — 경로마다 1회.
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

// ④ 선점 없이 편집 — 세션당 1회.
//
// ★ 이 조건은 흔하다. 세션당 1회로 눌러 잡지 않으면 편집마다 떠서 §4 의 실패가 된다.
//
// ★ **"선점 0건"이 넷을 뭉갠다.** 한 번도 안 집었다(처방이 맞다) · 집고 일하는 중
// (Claims 가 막는다) · **방금 제대로 끝냈다**(처방이 틀리다) · **형제 카드가 쥐고 있다**
// (처방이 틀리다). finish 가 선점을 반납하므로 셋째가 첫째와 똑같이 보이고, 그래서
// 성실하게 마무리한 세션이 잔소리를 듣는다. uncoveredByClosed 가 그 셋째만 갈라낸다.
//
// 넷째는 2026-08-07 에 이름이 붙었다 — 그전까지 이 목록은 셋이었다. 규율이 지시하는
// `git worktree add` 가 카드를 가르고 선점은 갈리기 전 카드에 남는데, 그 상태가
// "한 번도 안 집었다"와 글자 그대로 똑같이 보였다. SiblingClaims 가 그 넷째를 갈라낸다.
//
// ★ **문구는 판정이 실제로 든 근거만 댄다(2026-08-06 개정).** 앞선 판은 `in.TurnPaths` 의
// 앞 3개를 그대로 실었다 — 덮였는지 안 덮였는지를 안 보고. 그래서 판정이 옳은 발화에서도
// 문구가 **덮인 경로**를 지목했고, 읽는 쪽은 "선점이 저 파일을 덮는데 왜 뜨지"로 읽어
// 거짓 양성으로 오진했다(실측 2026-08-05, 세션 01KZ85KS: 지목된
// `tools/gitleaks-config.test.sh` 는 닫은 항목 둘이 선언한 경로였고, 안 덮인 것은
// `tools/gitleaks-allowlist.test.sh` 였다). 그 오진은 원장에 잘못된 판단 하나와 큐 항목
// 하나를 남겼고 둘 다 철회됐다 — **안 덮인 경로 하나만 댔으면 5초짜리 일이었다.**
//
// 처방의 값어치는 "틀리지 않는다"가 아니라 **"고칠 자리를 가리킨다"** 이고, 맞는 처방이
// 틀린 증거를 들고 오면 그 다음부터 처방 전체가 안 읽힌다(설계 §4 의 상시 점등과 같은 종착).
func unclaimedPrescription(in PrescribeInput) (Prescription, bool) {
	if len(in.Claims) > 0 || len(in.SiblingClaims) > 0 ||
		len(in.TurnPaths) == 0 || suppressed(in, PrescribeUnclaimed) {
		return Prescription{}, false
	}
	uncovered, grounded := uncoveredByClosed(in)
	if grounded && len(uncovered) == 0 {
		return Prescription{}, false // 전부 덮였다 = 방금 제대로 끝냈다
	}
	if grounded {
		// 근거가 있는 발화다 — 상태를 이름으로 부르고 **안 덮인 것만** 증거로 댄다.
		//
		// ★ 여기서 `paths` 를 고치라고 하지 않는다. fd 에 항목의 등록 경로를 나중에 갱신하는
		// 수단이 **없다**(`fd move` 는 프로젝트 축뿐이고 재등록은 409 다). 실행할 수 없는 지시를
		// 싣는 것은 이 개정이 없애려는 결함 — 문구가 사람을 헛되이 움직이는 것 — 의 재발이다.
		ids := claimIDs(in.Closed)
		return Prescription{
			Key: PrescribeUnclaimed,
			Reason: fmt.Sprintf("끝낸 항목 %s 가 선언하지 않은 경로 %d개를 편집했다(이번 구간 %d개 중)",
				ids, len(uncovered), len(in.TurnPaths)),
			Text: fmt.Sprintf(
				"끝낸 항목 %s 가 선언하지 않은 %s 를 만졌다 — 그 자리는 큐에도 카드에도 없다.\n"+
					"  → 같은 작업의 남은 자리면 note(kind='decision') 으로 범위가 왜 늘었는지 남겨라.\n"+
					"  → 별개 작업이면 add(id=…, title=…, body=…, paths=[…]) 로 항목을 세우고 집어라.",
				ids, strings.Join(clipList(uncovered, 3), ", ")),
		}, true
	}
	// 근거가 없다 — 닫은 항목이 없거나(한 번도 안 집었다) 비교 가능한 경로가 하나도 없다.
	// 그때는 "안 덮인 집합"이라는 말 자체가 성립하지 않으므로 만진 것을 그대로 댄다.
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

// uncoveredByClosed 는 이번 구간에 만진 경로 중 **방금 끝낸 항목이 안 덮은** 것을 낸다.
// grounded 는 그 판정에 근거가 있었는지다 — false 면 uncovered 는 뜻이 없다(언제나 비어 있다).
//
// ★ **불리언이 아니라 집합을 낸다.** 앞선 판(coveredByClosed)은 안 덮인 것을 하나 찾는 즉시
// `return false` 해서 그 집합을 버렸다. 발화 조건을 만드는 계산이 곧 **문구에 실을 값**을
// 만드는 계산인데 그것을 버리면, 호출부는 댈 것이 `TurnPaths` 전부밖에 없어진다.
// 이 함수의 이름이 뒤집힌 것이 그 개정의 전부다.
//
// ★ 하나라도 밖이면 발화한다. 그것은 새 일이고, 새 일에 대고 "무엇을 하는지가 큐에 없다"는
// 여전히 참이다 — 부분 일치로 접으면 큰 항목 하나를 닫은 세션이 그 뒤 무엇을 하든 안 걸린다.
//
// ★ 선언 경로가 없는 항목은 접을 근거를 못 준다. 빈 선언을 "전부 덮음"으로 읽으면
// paths 를 안 적은 항목 하나가 이 축을 통째로 끈다 — outsidePrescriptions 가 빈 선언에
// 대고 "밖"을 판정하지 않는 것과 같은 이유다.
func uncoveredByClosed(in PrescribeInput) (uncovered []string, grounded bool) {
	declared := declaredPaths(in.Closed)
	if len(declared) == 0 {
		return nil, false
	}
	// ★ **비교 불가능한 좌표의 경로는 근거로 쓰지 않는다.**
	//
	// 관측 경로는 RelPath(카드의 워크트리, p) 가 만드는데, 카드의 워크트리 밖 경로는
	// 절대경로 그대로 남는다(service.RelPath). 선언 경로는 언제나 저장소 상대다.
	// pathRelated 는 성분을 **앞에서부터** 맞추므로 그 둘은 원리적으로 절대 안 맞는다 —
	// 즉 절대경로가 하나라도 섞이면 이 가드가 통째로 무력해진다.
	// 실측: 2026-08-05 에 observed 406개 중 108개(27%)였고, 2026-08-06 에 **전체** 1592개 중
	// 174개(observed 기준으로는 1284개 중 174개, 13.6%)였다. 분모가 갈리므로 27%→10.9% 로
	// 읽으면 안 된다 — 같은 분모(observed)로 재면 27%→13.6% 다.
	// 증분 005(절대경로 발자국 삭제)가 랜딩한 뒤로는 **0개**다 — 그래도 이 가드는 존치한다(아래).
	//
	// 그것을 "안 덮였다"로 세면 **가장 성실하게 마무리한 세션이 잔소리를 듣는다** —
	// 이 함수가 막으라고 있는 바로 그 결과다. "못 읽었다"는 "없다"가 아니라는
	// 같은 규율이 경로 실재 축(judge/itempaths.go 의 PathUnknown)에도 있다.
	//
	// ★ 그래서 이런 경로는 **증거로도 안 쓴다.** 덮였는지 아닌지 판정할 수 없는 것을
	// "안 덮였다"고 문구에 지목하면 그것이 곧 이 함수가 없애려는 거짓 증거다.
	//
	// 다만 비교 가능한 것이 **하나도 없으면** 덮였다고 말하지 않는다(grounded=false).
	// 근거가 0인 것을 통과로 접으면 이번엔 반대로 처방이 통째로 꺼진다.
	for _, p := range in.TurnPaths {
		if !comparablePath(p) {
			continue
		}
		grounded = true
		if !PathsOverlap([]string{p}, declared) {
			uncovered = append(uncovered, p)
		}
	}
	return uncovered, grounded
}

// comparablePath 는 그 경로가 선언 경로와 **같은 좌표계**에 있는지다. 순수 함수다.
//
// 선언 경로는 저장소 상대다. 절대경로는 카드의 워크트리 밖이라는 뜻이고(RelPath 가 그때만
// 원본을 남긴다), 그 좌표는 이 판정이 읽을 수 있는 것이 아니다.
//
// ★ 상류가 막힌 뒤에도 이 가드는 **존치한다.** service.Beat 가 포함 축 관문을 세워
// 이제 워크트리 밖 경로는 애초에 footprint 로 안 들어간다(service.RelPathWithin).
// 그래도 지우면 안 되는 이유는 둘이다:
//
//	① (소멸했다) 이미 들어온 것이 DB 에 남아 있었다 — 실측 시점(2026-08-05) observed 발자국
//	   406개 중 108개(27%)가 절대경로였다. **증분 005 가 그 행들을 전부 지웠으므로 이 사유는
//	   더 이상 존치 근거가 아니다.** 지운다면 ② 만 보고 판단해야 한다.
//	② 관문이 서는 자리가 **다른 계층의 검증에 기대고 있다.** Beat 가 태우는
//	   service.RelPathWithin 은 root 가 비었거나 filepath.Rel 이 실패하면 절대경로를
//	   within=true 로 그대로 통과시킨다(의도된 fail-open 이고 legacy/plan.go 가 자기
//	   fail-open 의 전례로 이것을 인용한다). 지금 그 경로가 안 열리는 이유는 오직
//	   service.OpenSession 이 빈 worktree 와 비절대 worktree 를 거절하기 때문이다.
//	   **그 검증이 느슨해지는 순간 절대경로가 다시 들어오고, 이 가드가 그 뒤에 선다.**
//
//	   ★ 앞 판은 여기서 legacy.PlanImport 의 fail-open("포함 축은 기준 트리가 없어 안 본다")을
//	   근거로 들었다. **거짓이다** — internal/legacy 는 footprint·Touch 를 아예 안 만진다.
//	   legacy 의 절대경로는 item.paths 로 가고, 거기서 발자국이 되는 유일한 길인 Pick 도
//	   같은 RelPathWithin 관문을 태우며 origin=claimed 라 TurnPaths(service.Prescriptions 가
//	   origin=='observed' 인 발자국만 채운다)에서 걸러진다. declared 는 쓰는 코드가 아예 없다
//	   (model.OriginDeclared 주석: "지금 생산자가 없다"). 즉 legacy 의 문은 이 가드가 보는
//	   집합으로 안 통한다 — 다음 사람이 같은 추론을 다시 하지 않도록 여기 남긴다.
//
// 즉 이것은 중복 방어가 아니라 **다른 계층이 서는 자리를 대신 지키는 방어**다. 지우려면
// 먼저 service.OpenSession 의 worktree 검증이 여전히 절대경로 유입을 막는지부터 확인해야
// 하고, 그것은 별개 항목이다.
func comparablePath(p string) bool {
	p = strings.TrimSpace(p)
	return p != "" && !strings.HasPrefix(p, "/")
}

// ⑤ 오래 일했는데 판단이 0건.
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
