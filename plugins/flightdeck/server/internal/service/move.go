package service

import (
	"context"
	"strings"

	"github.com/kweiza/flightdeck/internal/model"
)

// MoveInput 은 항목 하나를 다른 프로젝트로 옮기는 요청이다.
type MoveInput struct {
	Project   string // 지금 있는 프로젝트
	SessionID string // 누가 옮겼는지. 원장에 남는다
	ItemID    string
	To        string // 대상 프로젝트
}

// MoveResult 는 옮긴 결과다.
//
// ★ **CrossRefs 의 뜻이 바뀌었다(증분 015).** 옛 뜻은 "옮기면서 **표현 불가**가 된 선행
// 참조 수"였다 — 옛 프로젝트에 남은 항목이 이 항목을 dep_item 으로 가리키던 관계가
// 같은 프로젝트 안에서만 해석돼 이동 직후 죽었기 때문이다. 이제 그 관계는
// `item_after.dep_project` 로 표현되고, 이동이 그 값을 **다시 쓴다**(store.RewriteDepProject).
// 그래서 이 수는 **살린 건수**다.
//
// 필드 이름을 안 바꾼다: REST·CLI·아웃박스가 `cross_refs` 를 읽고 있고, 이름을 갈면
// 옛 클라이언트가 값 없이 0을 받아 「아무 관계도 없었다」로 읽는다. 뜻이 바뀐 사실은
// 화면 문구가 말한다(cmd/fd 의 move 렌더).
type MoveResult struct {
	Item      model.Item `json:"item"`
	From      string     `json:"from"`
	To        string     `json:"to"`
	CrossRefs int        `json:"cross_refs"`

	// Derived 는 이동 **뒤** 되읽기의 신선도다. 이동은 되돌리는 코드가 없으므로
	// 되읽기가 실패해도 결과를 버리면 안 된다(DESIGN §5「쓰기 뒤 조회가 실패하면」).
	// item 축 실패가 있으면 Item 은 아는 사실(프로젝트·id)만 채워진 것이다.
	Derived
}

// MoveItem 은 항목을 다른 프로젝트로 옮긴다.
//
// ★ 범위를 **프로젝트 한 축으로 못박는다.** title·body·paths 를 함께 고치는 일반 amend 로
// 번지면, "무엇을 고칠 수 있나"가 표면마다 달라지고 그 차이를 아무도 못 따라간다.
// 그 일반 amend 는 별도 미결 질문이고, **그 지위를 DESIGN §11 이 적는다** — 지금 표면이
// 전수로 없다는 사실과, 그것이 영구 결정은 아니라는 것까지. 여기 있던 포인터(옛 셸 도구의
// `wq-amend-command`)는 이 레포에 **0건**이다. 그 항목이 안 넘어왔으므로 이쪽에서 따라갈
// 수 없다 — 끊긴 포인터를 남겨 두면 다음 사람이 그것을 찾느라 시간을 쓴다.
func (s *Service) MoveItem(ctx context.Context, in MoveInput) (MoveResult, error) {
	var res MoveResult
	in.Project = strings.TrimSpace(in.Project)
	in.ItemID = strings.TrimSpace(in.ItemID)
	in.To = strings.TrimSpace(in.To)

	// ★ 입력 거절은 **RefusedError 여야 한다.** `api.ClassifyError` 는 화이트리스트라
	// 평범한 error 는 어느 갈래에도 안 걸리고 500 `internal` + "서버 내부 오류다"가 된다 —
	// 사람이 읽으라고 쓴 이 문구는 아무에게도 안 닿고 서버에는 ERROR 로그만 쌓인다.
	// 인자를 안 준 것은 사용자 오류(400)이지 서버 결함이 아니다.
	//
	// 같은 부류의 선례: `api/land_resource_name_test.go` 가 자원 이름 오타에서 정확히
	// 이 결함(500 으로 나가던 것)을 잡고 HTTP 레벨로 잠갔다.
	if in.Project == "" {
		return res, &RefusedError{
			What:     "move",
			Reason:   "프로젝트가 비었다",
			Guidance: "요청 본문의 project 를 채워라. CLI 는 `.flightdeck.yaml` 의 프로젝트를 자동으로 싣는다.",
		}
	}
	if in.ItemID == "" {
		return res, &RefusedError{
			What:     "move",
			Reason:   "옮길 항목 id 가 비었다",
			Guidance: "옮길 항목 id 를 줘라: `fd move <item-id> --project <대상>`",
		}
	}

	cross, err := s.st.MoveItem(ctx, in.Project, in.ItemID, in.To, in.SessionID)
	if err != nil {
		return res, err
	}
	// 옮긴 뒤의 항목을 **대상 프로젝트에서** 다시 읽는다. 요청 값을 그대로 돌려주면
	// 실제로 무엇이 저장됐는지가 아니라 무엇을 보냈는지를 화면에 내게 된다.
	//
	// ★ **못 읽어도 결과를 버리지 않는다**(DESIGN §5). 이동은 이미 커밋됐고 되돌리는
	// 코드가 없다 — 여기서 오류를 올리면 항목은 옮겨진 채로 호출자는 실패만 받고,
	// 무엇보다 **CrossRefs 가 함께 죽는다.** 그 값은 이동이 다시 쓴 선행 참조 건수이고,
	// 그 수가 없으면 "관계가 따라왔나"를 사람이 원장을 열어야 안다 —
	// 되읽기 하나 때문에 그것까지 잃는 것은 정확히 반대 방향이다.
	// 아는 사실은 프로젝트와 id 뿐이라 그 둘만 채우고 나머지는 item 축으로 고백한다.
	d := &derive{}
	it, gerr := s.st.GetItem(ctx, in.To, in.ItemID)
	if gerr != nil {
		s.log.WarnContext(ctx, "항목 이동 뒤 되읽기 실패 — 이동은 커밋됐다",
			"from", clip(in.Project, 64), "to", clip(in.To, 64),
			"item", clip(in.ItemID, 64), "error", gerr.Error())
		d.fail("item", gerr)
		it = model.Item{Project: in.To, ID: in.ItemID}
	}
	return MoveResult{
		Item: it, From: in.Project, To: in.To, CrossRefs: cross,
		Derived: d.result(s.now()),
	}, nil
}
