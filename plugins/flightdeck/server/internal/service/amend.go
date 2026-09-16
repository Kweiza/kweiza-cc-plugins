package service

import (
	"context"
	"strings"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// AmendInput 은 이미 있는 항목의 본문을 고치는 요청이다.
//
// ★ 범위는 **title·body·paths 셋**이다(설계 §3). labels 는 label 이 이미 가졌고,
// 선행·state·close_reason 은 안 연다 — "무엇을 고칠 수 있나"가 표면마다 갈리는 것이
// §11 이 경고한 실패이고, 그 경고는 표면이 열린 뒤에 더 유효하다.
//
// nil 은 "안 건드린다"다. 빈 문자열을 가리키는 포인터는 "빈 값으로 바꿔라"다.
type AmendInput struct {
	Project   string
	SessionID string
	ItemID    string
	Title     *string
	Body      *string
	Paths     *[]string
	Reason    string
}

// AmendResult 는 고친 결과다.
//
// ★ Changed 는 **요청한 것이 아니라 실제로 값이 달라진 축**이다(LabelResult 의
// Added·Removed 와 같은 규율). Rev 는 되돌릴 좌표라 응답에 싣는다.
//
// ★ Overlaps 는 paths 를 고쳤을 때만 뜻이 있다. 이 동사는 세 축 중 유일하게 **남의
// 화면을 움직이므로**, 고친 사람이 그 사실을 알 경로를 여기서 만든다.
type AmendResult struct {
	Item     model.Item      `json:"item"`
	Rev      int             `json:"rev"`
	Changed  []string        `json:"changed"`
	Before   model.Item      `json:"before"`
	Overlaps []judge.Overlap `json:"overlaps"`

	// Derived 는 쓰기 **뒤** 파생의 신선도다. 본문 고침은 되돌리는 코드가 없으므로
	// 겹침 계산이나 되읽기가 실패해도 결과를 버리지 않는다(DESIGN §5, LabelResult 와 같은 자리).
	Derived
}

// AmendItem 은 항목의 본문을 제자리에서 고친다.
//
// 읽기부터 쓰기까지가 한 트랜잭션이어야 한다 — 벌어지면 두 세션이 서로의 수정을
// 덮어쓴다(SetLabels 와 같은 이유로 store 의 Tx 를 직접 연다).
func (s *Service) AmendItem(ctx context.Context, in AmendInput) (AmendResult, error) {
	var res AmendResult
	in.Project = strings.TrimSpace(in.Project)
	in.ItemID = strings.TrimSpace(in.ItemID)
	// ★ Reason 은 **거절 판정에만** 다듬은 값을 쓴다. in.Reason 자체는 안 건드린다 —
	//   store/amend.go 가 "저장은 원문 그대로 한다(TrimSpace 한 값이 아니다) … 거절
	//   판정에만 다듬은 값을 쓴다"고 명시했다. 여기서 in.Reason 을 덮으면 이 층이
	//   그 결정을 말없이 뒤집는다(양끝 공백만 잃지만 아래 층 계약과 어긋난다).
	reason := strings.TrimSpace(in.Reason)

	if in.Project == "" {
		return res, &RefusedError{
			What:     "amend",
			Reason:   "프로젝트가 비었다",
			Guidance: "요청 본문의 project 를 채워라. CLI 는 `.flightdeck.yaml` 의 프로젝트를 자동으로 싣는다.",
		}
	}
	// ★ 워크스페이스 관문 — 대상 프로젝트가 이 세션이 쓸 수 있는 곳인가(service/workspace.go).
	//   명부 밖 이름은 여기서 끊긴다: 통과시키면 오타 하나가 프로젝트를 하나 만든다.
	if err := s.GateTargetProject(ctx, in.SessionID, in.Project); err != nil {
		return res, err
	}
	if in.ItemID == "" {
		return res, &RefusedError{
			What:     "amend",
			Reason:   "고칠 항목 id 가 비었다",
			Guidance: "고칠 항목 id 를 줘라: `fd amend <item-id> --title/--body/--path … --reason <사유>`",
		}
	}
	// ★ 빈 요청을 **쓰기 전에** 거절한다. 서버까지 갔다 와도 같은 결론이지만 그 왕복은
	//   원장에 개정 행 하나를 남긴다 — 아무것도 안 바꾸는 개정이 이력에 쌓이면 나중에
	//   그 이력을 읽는 사람이 무엇이 일어났는지를 못 가린다.
	//
	// ★ errors.New 가 아니라 RefusedError 다 — api.ClassifyError 는 화이트리스트라
	//   errors.New 는 아무 갈래에도 안 걸리고 500 internal 로 나간다. 이 갈래는 MCP 에서
	//   정상 도달 가능하다: amend 도구의 필수 인자는 item_id·reason 뿐이라(tools.go)
	//   title·body·paths 를 셋 다 안 준 호출이 그대로 여기까지 온다.
	if in.Title == nil && in.Body == nil && in.Paths == nil {
		return res, &RefusedError{
			What:   "amend",
			Reason: "고칠 축을 하나는 줘라 — title·body·paths 중 하나도 안 주면 개정 이력만 늘어난다",
			Guidance: "--title·--body·--path 중 하나 이상을 줘라. 꼬리표는 `fd label` 이고, " +
				"선행과 상태는 이 동사가 안 고친다.",
		}
	}
	if reason == "" {
		return res, &RefusedError{
			What:   "amend",
			Reason: "고친 사유가 비었다 — 사유 없는 수정은 나중에 되짚을 수 없다",
			Guidance: "--reason 에 한 구절이면 된다(\"경로 리네임 추종\" · \"전제가 틀렸다\"). " +
				"이 값은 개정 이력에 남고, 되짚을 사람이 그 표를 여는 유일한 이유가 그것이다.",
		}
	}

	var rec store.AmendRecord
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		var e error
		// ★ Reason 은 in.Reason(원문)을 그대로 넘긴다 — reason(다듬은 값)이 아니다.
		//   store.AmendItem 이 원문을 저장하기로 이미 결정했다(store/amend.go:64-65).
		rec, e = t.AmendItem(in.Project, in.ItemID, store.AmendPatch{
			Title: in.Title, Body: in.Body, Paths: in.Paths, Reason: in.Reason,
		}, in.SessionID)
		return e
	})
	if err != nil {
		return res, err
	}

	res = AmendResult{
		Rev: rec.Rev, Changed: rec.Changed, Before: rec.Before, Item: rec.After,
	}

	// ★ 여기서부터는 **쓰기 뒤 파생**이다. 실패해도 결과를 버리지 않는다 — 쓰기는
	//   이미 커밋됐고 되돌리는 코드가 없다(DESIGN §5).
	//
	// ★ 시각을 **한 번만** 잡는다(pick.go:337 과 같은 규율) — 되읽기·겹침 계산·
	//   Derived 계산이 서로 다른 s.now() 를 보게 두면 같은 응답 안에서 "지금"이 갈린다.
	now := s.now()
	d := &derive{}

	// 저장된 값을 다시 읽는다 — 요청 값을 그대로 돌려주면 무엇이 저장됐는지가 아니라
	// 무엇을 보냈는지를 화면에 내게 된다.
	if it, gerr := s.st.GetItem(ctx, in.Project, in.ItemID); gerr != nil {
		s.log.WarnContext(ctx, "본문 고친 뒤 되읽기 실패 — 쓰기는 커밋됐다",
			"project", clip(in.Project, 64), "item", clip(in.ItemID, 64), "error", gerr.Error())
		d.fail("item", gerr)
	} else {
		res.Item = it
	}

	// ★ 겹침은 paths 를 **실제로 바꿨을 때만** 센다. 안 바꿨으면 이 항목의 겹침은
	// 이 수정의 결과가 아니라 원래 있던 사실이고, 그것을 여기 내면 고친 사람은
	// 자기가 방금 만든 겹침이라고 읽는다.
	//
	// ★★ **새 경로가 비었으면 계산 자체를 안 돌린다**(재리뷰 4번째 상태, 2026-09-16).
	// 경로가 없는 항목은 겹칠 대상이 원리적으로 없다 — RenderAdd 가 이미 그렇게
	// 말한다("경로가 없으면 이 항목은 겹침 축에 안 잡힌다"). 그런데 liveOverlapSessions
	// (board.go)는 paths 를 인자로 안 받는다 — 세션 카드·로스터 조회일 뿐이라 그
	// 실패는 **경로 개수와 무관하게** 일어난다. 그 실패를 "overlaps" 축에 적으면
	// 본문은 "볼 것도 없다"(경로 0)를 말하는데 꼬리는 "몰라서 못 봤다, 0이라고
	// 넘겨짚지 마라"를 말해 한 응답 안에서 부딪힌다 — 검사 순서를 바꾸는 미봉이
	// 아니라 계산을 원인에서 없앤다. 이 갈래에서는 Overlaps 가 nil 로 남고
	// Derived 에도 실패가 안 남는다 — "셀 것이 없다"는 근거를 본문·꼬리가 함께 갖는다.
	if containsString(rec.Changed, "paths") && len(res.Item.Paths) > 0 { // ★ 이 헬퍼는 landing.go 에 이미 있다(중복 선언 금지)
		// ★ live·selfCC 를 얻는 조합은 board.go 의 liveOverlapSessions 다 — pick.go 의
		//   Pick 과 공유하는 단일 지점이다(사본을 amend.go 안에 다시 두지 않는다).
		//   Pick 은 이 GetProject 를 이미 진입부에서 했지만 amend 는 쓰기 전에 project
		//   구조체가 필요 없어서 안 읽어 뒀다 — 그래서 여기서 한 번 더 읽는다.
		if proj, perr := s.st.GetProject(ctx, in.Project); perr != nil {
			s.log.WarnContext(ctx, "겹침을 못 셌다 — 수정은 커밋됐다",
				"project", clip(in.Project, 64), "item", clip(in.ItemID, 64), "error", perr.Error())
			d.fail("overlaps", perr)
		} else if live, selfCC, lerr := s.liveOverlapSessions(ctx, proj, in.SessionID, now, d); lerr != nil {
			s.log.WarnContext(ctx, "겹침을 못 셌다 — 수정은 커밋됐다",
				"project", clip(in.Project, 64), "item", clip(in.ItemID, 64), "error", lerr.Error())
			d.fail("overlaps", lerr)
		} else {
			res.Overlaps = judge.OverlapsWithLive(res.Item.Paths, live, in.SessionID, selfCC)
		}
	}

	res.Derived = d.result(now)
	return res, nil
}
