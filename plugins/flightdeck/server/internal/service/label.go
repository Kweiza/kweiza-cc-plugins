package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// LabelInput 은 이미 있는 항목의 꼬리표를 고치는 요청이다.
//
// ★ 범위를 **꼬리표 한 축으로 못박는다.** title·body·paths·after 를 함께 고치는 일반
// amend 로 번지면 "무엇을 고칠 수 있나"가 표면마다 달라지고 그 차이를 아무도 못
// 따라간다 — move 가 프로젝트 한 축으로 못박은 이유와 같고, cutAfterRequest 가
// "전용 동사"라고 적은 이유와도 같다. 본문·선행의 사후 수정은 DESIGN §11 이
// "안 만든다"로 이미 판정했고 이 표면은 그 판정을 안 건드린다.
type LabelInput struct {
	Project   string
	SessionID string // 누가 고쳤는지. 원장에 남는다
	ItemID    string
	Add       []string
	Rm        []string
}

// LabelResult 는 고친 결과다.
//
// ★ Before·After 와 **Added·Removed 를 따로** 담는다. 요청한 것과 실제로 바뀐 것이
// 다르기 때문이다: 이미 있는 것을 더하거나 없는 것을 빼는 것은 집합 연산이라
// 거절하지 않지만, 그때 화면이 "더했다"고만 말하면 사람은 안 바뀐 것을 바뀐 줄 안다.
// 조용한 무변화를 안 만드는 것이 이 두 필드의 존재 이유다.
type LabelResult struct {
	Item    model.Item `json:"item"`
	Before  []string   `json:"before"`
	After   []string   `json:"after"`
	Added   []string   `json:"added"`
	Removed []string   `json:"removed"`

	// Derived 는 쓰기 **뒤** 되읽기의 신선도다. 꼬리표 고침은 되돌리는 코드가 없으므로
	// 되읽기가 실패해도 결과를 버리지 않는다(DESIGN §5「쓰기 뒤 조회가 실패하면」,
	// MoveResult·CutAfterResult 와 같은 자리). item 축 실패가 있으면 Item 은 아는
	// 사실(프로젝트·id·저장된 Labels)만 채워진 것이다.
	Derived
}

// SetLabels 는 항목의 꼬리표를 고친다.
//
// 계산(judge.ApplyLabels)과 쓰기(store.SetLabels)를 가르되, **지금 값을 읽는 것부터
// 쓰기까지가 한 트랜잭션 안**이어야 한다 — 읽기와 쓰기 사이가 벌어지면 두 세션이
// 서로의 꼬리표를 지운다. 그래서 store 의 Tx 를 직접 연다.
func (s *Service) SetLabels(ctx context.Context, in LabelInput) (LabelResult, error) {
	var res LabelResult
	in.Project = strings.TrimSpace(in.Project)
	in.ItemID = strings.TrimSpace(in.ItemID)

	if in.Project == "" {
		return res, errors.New("프로젝트가 비었다")
	}
	if in.ItemID == "" {
		return res, errors.New("꼬리표를 고칠 항목 id 가 비었다")
	}
	// ★ 빈 요청을 **쓰기 전에** 거절한다. 서버까지 갔다 와도 같은 결론이지만, 그
	// 왕복은 오프라인에서 아웃박스에 쌓이는 쓰기가 된다(runAfterCut 이 축 수를
	// 클라이언트에서 세는 것과 같은 규율).
	//
	// ★ errors.New 가 아니라 RefusedError 다(리뷰 Important) — api.ClassifyError 는
	// 화이트리스트라 errors.New 는 아무 갈래에도 안 걸리고 500 internal 로 나간다.
	// 그런데 이 갈래는 MCP 에서 정상 도달 가능하다: label 도구의 필수 인자는
	// item_id 하나뿐이라(tools.go) add·rm 을 둘 다 안 준 호출이 그대로 여기까지 온다.
	// 500 이면 이 문구 대신 "서버 내부 오류다"만 나가고 서버엔 ERROR 로그가 쌓인다.
	if len(nonBlank(in.Add))+len(nonBlank(in.Rm)) == 0 {
		return res, &RefusedError{
			What:     "label",
			Reason:   "더하거나 뺄 꼬리표를 하나는 줘라 — 빈 요청은 원장만 늘린다",
			Guidance: "--add 나 --rm 중 하나는 꼬리표를 하나 이상 담아서 줘라.",
		}
	}

	var before, after []string
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		cur, gerr := t.GetItem(in.Project, in.ItemID)
		if gerr != nil {
			return gerr
		}
		// ★ 종료된 항목은 안 고친다. tickler 의 유일한 판정 소비자는 굶김 축이고 그
		// 축은 열린 항목만 본다 — 끝난 항목의 꼬리표를 바꾸는 것은 아무 데도 안
		// 닿으면서 원장만 늘린다. SetItemState 가 종료를 안 되돌리는 규율과 같은 방향이다.
		//
		// store 의 ItemClosedError 를 안 쓰는 이유: 그 타입은 상태 전이용이라
		// Want(되돌리려던 상태)를 담고, 꼬리표 수정에는 거기 넣을 값이 없다.
		// 여기 RefusedError 는 **Guidance 를 실을 수 있다** — 이 저장소가 거절에
		// 처방을 함께 내는 그 자리다.
		if cur.State == model.ItemDone || cur.State == model.ItemDropped {
			return &RefusedError{
				What: "label",
				Reason: fmt.Sprintf("항목 %s 는 이미 %s 다 — 끝난 항목의 꼬리표는 안 고친다",
					clip(in.ItemID, 64), cur.State),
				Guidance: "꼬리표가 뜻을 갖는 곳은 굶김 축 하나이고 그 축은 열린 항목만 본다. " +
					"끝난 항목에 대해 남길 것이 있으면 note(item_id=…) 를 얹어라 — " +
					"그쪽은 닫힌 항목에도 붙고 원장에 남는다.",
			}
		}
		before = cur.Labels
		after = judge.ApplyLabels(cur.Labels, in.Add, in.Rm)
		return t.SetLabels(in.Project, in.ItemID, after, in.SessionID)
	})
	if err != nil {
		return res, err
	}

	res = LabelResult{
		Before: before, After: after,
		Added: onlyIn(after, before), Removed: onlyIn(before, after),
	}
	// 저장된 값을 다시 읽는다 — 요청 값을 그대로 돌려주면 무엇이 저장됐는지가
	// 아니라 무엇을 보냈는지를 화면에 내게 된다(MoveItem 과 같은 규율).
	//
	// ★ **못 읽어도 결과를 버리지 않는다**(DESIGN §5, MoveResult·CutAfterResult 와 같은 자리).
	// 쓰기는 이미 커밋됐고 되돌리는 코드가 없다 — 여기서 오류를 올리면 꼬리표는 바뀐 채로
	// 호출자는 실패만 받고, 무엇보다 Added·Removed 까지 함께 죽는데 그 둘이 이 응답의
	// 값이다. 아는 사실은 프로젝트·id·방금 계산한 After 뿐이라 그것만 채우고 나머지는
	// item 축으로 고백한다.
	d := &derive{}
	it, gerr := s.st.GetItem(ctx, in.Project, in.ItemID)
	if gerr != nil {
		s.log.WarnContext(ctx, "꼬리표 고친 뒤 되읽기 실패 — 쓰기는 커밋됐다",
			"project", clip(in.Project, 64), "item", clip(in.ItemID, 64), "error", gerr.Error())
		d.fail("item", gerr)
		it = model.Item{Project: in.Project, ID: in.ItemID, Labels: after}
	}
	res.Item = it
	res.Derived = d.result(s.now())
	return res, nil
}

// nonBlank 는 공백 아닌 값만 남긴다.
func nonBlank(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// onlyIn 은 a 에는 있고 b 에는 없는 값이다. 순서는 a 를 따른다.
func onlyIn(a, b []string) []string {
	has := make(map[string]bool, len(b))
	for _, v := range b {
		has[v] = true
	}
	out := make([]string, 0)
	for _, v := range a {
		if !has[v] {
			out = append(out, v)
		}
	}
	return out
}
