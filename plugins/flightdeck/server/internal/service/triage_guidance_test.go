package service

import (
	"strings"
	"testing"
)

// 거르는 기준은 **물으면 정해지는 것**도 후속에서 뺀다 (2026-08-21).
//
// 실측(원장 mode=ro, 창 2026-08-11T10:12~08-20, 창작 후속 338건): 결정 지시형 강신호 155건 중
// **사람 한 번 물으면 끝나는 것이 손검증 14건**(+경계 2)이다. 나머지는 실측 선행 51건 ·
// 세션 재량 ~90건이라 물음 대상이 아니다. 그 14건은 굶는다 — 열림 64%(전체 55.6%),
// 열린 것 나이 중앙 **6.3일**(전체 3.0일의 2.1배). 반대로 **답이 온 5건은 전부 1시간 안에
// 코드 0줄로 닫혔다.** 물었으면 그 자리에서 끝났을 것들이다.
//
// ★ **도구 이름을 안 박는다.** fd 에는 세션→사람 채널이 없다 — note(kind='ask') 는
// FilterNotes 가 self 를 빼서 정의상 **다른 세션**에게 가고(기존 1,439건의 수신자 표기가
// 전부 세션이다), 훅이 낼 수 있는 additionalContext·decision:block 은 둘 다 모델이 읽는다.
// 그래서 행위로만 말한다: 대화형 세션에서도 헤드리스에서도 **둘 다 참인 문장**이어야 한다.
// 없는 통로를 가리키는 문구는 이 레포가 결함으로 분류하는 부류다(mcpsrv/render.go RenderLand).
//
// ★ 잠그는 것은 **기준이 있다는 사실**뿐이다. 문장 전문을 잠그면 개정할 때마다 시험이
// 깨져서, 기준을 고치는 대신 시험을 고치는 쪽으로 사람을 민다(스킬 축과 같은 논법).
func TestTriageGuidanceSendsAnswerableQuestionsBackToThisSession(t *testing.T) {
	if !strings.Contains(TriageGuidance, "물으면") {
		t.Fatalf("거르는 기준에 '물으면 정해지는 것' 부류가 없다 — 그 14건은 나이 중앙 6.3일로 굶고,\n"+
			"답이 온 5건은 전부 1시간 안에 코드 0줄로 닫혔다:\n%s", TriageGuidance)
	}
	// 못 묻는 세션(헤드리스·자동)에서 문구가 막다른 길이 되면 안 된다 — 그 갈래의 행동도 말해야 한다.
	if !strings.Contains(TriageGuidance, "못 물으면") {
		t.Fatalf("못 묻는 갈래의 행동이 없다 — fd 는 세션의 대화형 여부를 모른다(근사 신호도 6.3%% 오탐).\n"+
			"한쪽에서만 참인 문구는 나머지 세션에게 없는 통로를 가리킨다:\n%s", TriageGuidance)
	}
	// 도구 이름을 박지 않는다 — 이 머신에서 존재를 안 쟀다(§13: 플랫폼 동작은 직접 잰 것만).
	if strings.Contains(TriageGuidance, "AskUserQuestion") {
		t.Fatalf("거르는 기준이 도구 이름을 박았다 — 그 도구의 존재·가용 조건을 이 머신에서 안 쟀다(§13):\n%s", TriageGuidance)
	}
}
