package service

import (
	"context"
	"fmt"
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
// CrossRefs 를 결과에 담는 이유: 옮기면 옛 프로젝트에 남은 항목이 이 항목을
// 선행(dep_item)으로 가리키던 관계가 **스키마로 표현 불가**해진다. 막지는 않지만
// 몇 건이 그렇게 되는지 화면이 말해야 한다 — 침묵하면 그 관계가 조용히 죽는다.
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
// 그 일반 amend 는 별도 미결 질문이다(옛 셸 도구의 wq-amend-command 가 같은 자리에 열려 있다).
func (s *Service) MoveItem(ctx context.Context, in MoveInput) (MoveResult, error) {
	var res MoveResult
	in.Project = strings.TrimSpace(in.Project)
	in.ItemID = strings.TrimSpace(in.ItemID)
	in.To = strings.TrimSpace(in.To)

	if in.Project == "" {
		return res, fmt.Errorf("프로젝트가 비었다")
	}
	if in.ItemID == "" {
		return res, fmt.Errorf("옮길 항목 id 가 비었다")
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
	// 무엇보다 **CrossRefs 가 함께 죽는다.** 그 값은 스키마로 표현 못 하게 된 선행 관계
	// 건수이고, 이 타입의 주석이 "침묵하면 그 관계가 조용히 죽는다"고 적어 둔 자리다 —
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
