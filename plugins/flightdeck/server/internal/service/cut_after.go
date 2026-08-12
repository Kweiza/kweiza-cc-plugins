package service

import (
	"context"
	"strings"

	"github.com/kweiza/flightdeck/internal/model"
)

// CutAfterInput 은 항목 하나에서 선행 조건 하나를 끊는 요청이다.
type CutAfterInput struct {
	Project   string
	SessionID string // 누가 끊었는지. 원장에 남는다
	ItemID    string
	Dep       model.After // 끊을 선행. item·job·sha 중 정확히 하나
}

// CutAfterResult 는 끊은 결과다.
//
// Item 을 되읽어 싣는 이유: 이 동사는 **하나씩** 끊으므로, 남은 것이 안 보이면 "이제 집을 수 있나"에
// 답하려고 pick 을 다시 불러야 하고 "하나 풀었더니 또 하나"가 반복된다. judge.AfterSatisfied 가
// 미충족 사유를 전부 내는 것과 같은 이유다.
type CutAfterResult struct {
	Item model.Item  `json:"item"`
	Cut  model.After `json:"cut"` // 방금 끊은 것. 응답만 보고 원장에 판단을 쓸 수 있어야 한다

	// Derived 는 절단 **뒤** 되읽기의 신선도다. 절단은 되돌리는 코드가 없으므로 되읽기가
	// 실패해도 결과를 버리지 않는다(DESIGN §5「쓰기 뒤 조회가 실패하면」, MoveResult 와 같은 자리).
	Derived
}

// CutAfter 는 선행 조건 하나를 끊는다.
//
// ★ 이 동사가 존재하는 이유. judge.AfterSatisfied 는 선행이 폐기되면(after-dropped-dep) 또는
// dep_sha 가 해석 불가면(after-bad-ref) "기다려도 안 풀린다 — **선행을 고쳐라**"를 낸다.
// 그런데 그 명령을 집행할 쓰기 표면이 하나도 없었다: add·claim·finish·move·note·land·alloc·snapshot
// 어느 것도 item_after 행을 못 지운다. 처방이 있는데 수단이 없으면 항목은 영구히 굶고,
// 화면에는 "고쳐라"만 계속 뜬다. 실측 피해 2건(image-model-8b-swap · t3-gpu-perf-measure)이
// 그 모양으로 멈춰 있었고, 그 전에도 같은 벽이 두 번 나와 둘 다 close_reason 으로 우회됐다.
//
// ★ **범위는 선행 한 축이다.** 항목 본문(title·body)은 만들어진 시점의 사진이고 변경은 판단으로
// 나른다 — 그 규율은 DESIGN §11 이 적고 store 의 관문이 지킨다. 여기서 본문 수정으로 번지면
// 그 관문이 지키던 문장이 거짓이 된다. 고쳐야 할 것은 본문이 아니라 걸린 조건이다.
func (s *Service) CutAfter(ctx context.Context, in CutAfterInput) (CutAfterResult, error) {
	var res CutAfterResult
	in.Project = strings.TrimSpace(in.Project)
	in.ItemID = strings.TrimSpace(in.ItemID)
	in.Dep = model.After{
		Item: strings.TrimSpace(in.Dep.Item),
		Job:  strings.TrimSpace(in.Dep.Job),
		SHA:  strings.TrimSpace(in.Dep.SHA),
	}

	// ★ 좌표가 비면 **내려가기 전에** 거절한다. 빈 항목 id 로 내려가면 "그런 선행이 없다"(404)가
	// 나가는데 진짜 사유는 인자를 안 준 것이고, 그러면 사람은 dep 이름을 의심하러 간다.
	//
	// ★ 그리고 그 거절은 **RefusedError 여야 한다.** `api.ClassifyError` 는 화이트리스트라
	// 평범한 error 는 어느 갈래에도 안 걸리고 500 `internal` 이 된다 — 위 문단이 애써 만든
	// "진짜 사유"가 정작 화면에서는 "서버 내부 오류다"로 바뀐다. 404 로 오도되는 것을 막으려다
	// 500 으로 오도하면 고친 것이 없다.
	if in.Project == "" {
		return res, &RefusedError{
			What:     "after cut",
			Reason:   "프로젝트가 비었다",
			Guidance: "요청 본문의 project 를 채워라. CLI 는 `.flightdeck.yaml` 의 프로젝트를 자동으로 싣는다.",
		}
	}
	if in.ItemID == "" {
		return res, &RefusedError{
			What:   "after cut",
			Reason: "선행을 끊을 항목 id 가 비었다",
			Guidance: "선행을 끊을 항목 id 를 줘라: `fd after cut <item-id> --item <dep>`. " +
				"지금 무엇이 걸려 있는지는 `fd pick <item-id>` 의 항목 절이 낸다.",
		}
	}

	if err := s.st.RemoveAfter(ctx, in.Project, in.ItemID, in.Dep, in.SessionID); err != nil {
		return res, err
	}

	// 끊은 뒤의 항목을 다시 읽는다. 요청 값을 그대로 돌려주면 무엇이 저장됐는지가 아니라
	// 무엇을 보냈는지를 화면에 내게 된다.
	//
	// ★ **못 읽어도 결과를 버리지 않는다**(DESIGN §5). 절단은 이미 커밋됐고 되돌리는 코드가 없다 —
	// 여기서 오류를 올리면 선행은 끊긴 채로 호출자는 실패만 받고, 그러면 같은 명령을 다시 쏜다.
	// 그 재시도는 이번엔 404(그런 선행이 없다)로 답하므로, 사람은 **끊긴 적이 없다고 믿게 된다.**
	d := &derive{}
	it, gerr := s.st.GetItem(ctx, in.Project, in.ItemID)
	if gerr != nil {
		s.log.WarnContext(ctx, "선행 절단 뒤 되읽기 실패 — 절단은 커밋됐다",
			"project", clip(in.Project, 64), "item", clip(in.ItemID, 64), "error", gerr.Error())
		d.fail("item", gerr)
		it = model.Item{Project: in.Project, ID: in.ItemID}
	}
	return CutAfterResult{Item: it, Cut: in.Dep, Derived: d.result(s.now())}, nil
}
