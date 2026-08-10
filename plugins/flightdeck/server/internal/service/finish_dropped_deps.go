package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 폐기 관문 — 종속이 있는 항목을 버리는 것을 한 번은 붙잡는다.
//
// ★ 이 파일이 finish.go 와 따로 있는 이유는 finish_followups.go 와 같다:
// finish.go 에는 호출 세 줄만 남기고 판정은 여기 둔다. 그 파일은 지금도 여러 축이
// 동시에 쥐는 자리라, 관문 하나 때문에 남의 훅과 부딪히느니 파일을 가른다.

// droppedDepsEvent 는 이 관문이 **한 번 발화했다**는 사실이다.
// 이 이벤트가 관문을 "벽"이 아니라 "관문"으로 만든다(followupsMissingEvent 와 같은 자리).
const droppedDepsEvent = "item.finish_dropped_deps"

// DroppedDepsGuidance 는 종속을 남긴 채 폐기하려는 세션에게 그 자리에서 내는 문구다.
//
// ★ **집행 가능한 동사를 가리킨다.** 이 관문을 낳은 결함이 정확히 "처방은 있는데 그것을
// 집행할 동사가 없다"였다 — judge.AfterSatisfied 의 after-dropped-dep 은 "선행을 고쳐라"라고
// 말하는데, 그것을 할 쓰기 표면이 하나도 없었다. 그 공백을 다시 만들지 않으려면
// 이 문구가 실제로 존재하는 명령을 이름으로 대야 한다.
const DroppedDepsGuidance = `이 항목을 폐기하면 위 항목들은 ` + "`after-dropped-dep`" + ` 로 떨어진다 —
"기다려도 안 풀린다"는 상태이고, 그것은 **기다림으로는 절대 안 풀린다.**

셋 중 하나를 골라라:
  ① 그 선행이 이제 무의미하면 끊어라 — fd after cut <기대는-항목> --item <이 항목>
  ② 다른 것을 기다려야 하면 끊고 새로 걸어라(선행 추가는 항목을 만들 때다).
  ③ 그대로 두는 것이 옳다면 그대로 다시 불러라 — **이 관문은 한 번만 막는다.**
     다만 그때는 왜 남기는지 note(kind='decision') 로 남겨라. 안 남기면 다음 세션이
     그 항목을 집었다가 영구 미충족을 발견하고 같은 조사를 처음부터 다시 한다.`

// judgeDroppedWithDependents 는 종속을 남긴 채 폐기하는 finish 를 **한 번** 막는다.
//
// ★★ **한 번만 막는 것이 이 설계의 전부다**(judgeMissingFollowups 와 같은 규율).
//
// 영영 막으면 관문이 아니라 벽이다 — 종속을 그대로 두는 것이 옳은 경우가 실제로 있고
// (그 항목도 함께 접히는 중이거나, 기대던 쪽이 이미 딴 길로 갔거나), 그때 finish 가 안 되면
// 세션은 close_reason 으로 우회하거나 종속을 거짓으로 손본다. **그 둘이 이 결함의 선례에서
// 실제로 일어난 일이다** — 같은 벽이 두 번 나왔고 두 번 다 우회됐다.
//
// 반대로 안 막으면 지금과 같다. 지금은 종속이 있는 항목을 폐기해도 **경고가 0이고**,
// 기대던 항목들은 다음 pick 에서야 after-dropped-dep 로 나타난다 — 그것도 탈락 사유 줄로만.
//
// 관측을 못 하면 막지 않는다(fail-open). 종속 조회나 이벤트 조회가 실패하면 그냥 통과다 —
// 계측 하나가 실패했다고 마무리를 잃는 것이 훨씬 나쁘다.
func (s *Service) judgeDroppedWithDependents(ctx context.Context, in FinishInput) *RefusedError {
	// done 은 안 본다. **끝난 선행은 충족이다** — judge.AfterSatisfied 가 ItemDone 을 ""(충족)로
	// 읽으므로 기대던 쪽은 오히려 풀린다. 흔한 경로에 조회 하나도 안 얹는다.
	if in.Outcome != model.ItemDropped {
		return nil
	}
	waiting, err := s.st.DependentItems(ctx, in.Project, in.ItemID)
	if err != nil || len(waiting) == 0 {
		return nil // 못 읽었으면 안 막는다(fail-open) · 없으면 발화할 것이 없다
	}
	if s.alreadyWarnedDroppedDeps(ctx, in) {
		return nil // 이미 한 번 말했다. 두 번째는 사람의 판단이다
	}
	s.st.LogEvent(ctx, droppedDepsEvent, in.Project, in.SessionID,
		map[string]any{"item": clip(in.ItemID, 100), "waiting": len(waiting)})

	return &RefusedError{
		What: "finish",
		Reason: fmt.Sprintf(
			"%s 를 폐기하려는데, 이 항목을 선행으로 기다리는 살아 있는 항목이 %d건 있다: %s",
			clip(in.ItemID, 64), len(waiting), strings.Join(waiting, ", ")),
		Guidance: DroppedDepsGuidance,
	}
}

// alreadyWarnedDroppedDeps 는 이 (세션·항목) 조합에 이 관문이 이미 발화했는지다.
//
// 항목 id 까지 보는 이유는 alreadyWarnedFollowups 와 같다 — 한 세션이 항목 여럿을 마무리한다.
// 세션만 보면 첫 항목에서 발화한 뒤 둘째 항목은 조용히 지나간다.
func (s *Service) alreadyWarnedDroppedDeps(ctx context.Context, in FinishInput) bool {
	evs, err := s.st.ListSessionEvents(ctx, in.SessionID, droppedDepsEvent, time.Time{})
	if err != nil {
		return true // 못 읽었으면 막지 않는다 — 관문의 실패가 마무리를 막으면 안 된다
	}
	for _, e := range evs {
		if eventItemID(e) == in.ItemID {
			return true
		}
	}
	return false
}
