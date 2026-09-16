package service

import (
	"context"
	"strings"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// show — 항목 하나의 이력을 **읽는다**
// ─────────────────────────────────────────────────────────────────────────────
//
// ★ 축이 pick 과 다르다. pick 은 **선점**이고 이 동사는 **읽기**다. 그래서 닫힌 항목도
// 준다 — 그것이 이 표면의 존재 이유다.
//
// 근거는 실측이다(운영 원장, 2026-09-17 읽기 전용 측정):
//
//	· 닫힌 항목(done·dropped)에 걸린 판단 **1,985건**(열린 항목은 125건뿐)
//	· 항목이 **닫힌 뒤에** 얹힌 판단 40건 — decision 23 · verified 8 · **ask 5** · handoff 3 · not-done 1
//	· 판단 3건 이상 걸린 항목 332개
//
// `judgment_link` 는 항목↔판단을 구조화된 링크로 잇는데 그 링크를 **역방향으로 읽는
// 경로가 pick 하나뿐**이었고, pick 은 `state='open'` 만 준다. `SearchJudgments` 에는
// 항목 필터가 없다(전문 검색뿐). 즉 저 1,985건은 도달 불가였고 그중 ask 5건은 누군가
// 답을 기다리며 남긴 것이다.
//
// 같은 회차에 `item_revision`(증분 016, 2026-09-16)도 **읽는 경로가 0건**이었다.
// `RenderAmend` 가 "개정 3 — 옛 값은 그대로 남는다"고 좌표를 약속하는데 그 좌표를 열
// 문이 없었다. 두 결함의 소비자가 같다 — **그 항목을 되짚는 사람**이라 한 동사로 묶는다.

// ShowInput 은 항목 하나를 읽는 요청이다.
//
// ★ 고칠 축이 없다. 이 동사는 원장에 아무것도 안 쓴다 — `state`·`close_reason` 을
// 안 건드리는 것이 닫힌 항목을 줄 수 있는 근거다.
type ShowInput struct {
	Project string
	// SessionID 는 **선택**이다. 이 축이 하는 일은 워크스페이스 관문 하나뿐이고
	// (GateTargetProject), 그 관문은 세션을 모르면 판정 근거가 없어 통과시킨다.
	// 읽기라 원장에 귀속할 행이 없어서, 없다고 거절하면 못 읽는 사람만 생긴다.
	SessionID string
	ItemID    string
}

// ShowResult 는 항목 하나의 지금 본문 + 개정 이력 + 걸린 판단이다.
//
// ★ **자르지 않는다.** 예산은 표시 계층(mcpsrv.RenderShow)이 건다 — board 가 같은
// 자리에서 같은 방식이다(BoardTokenBudget 은 렌더러의 상수다). 여기서 자르면 REST 가
// 정본이 아니게 되고, 잘린 사실을 아는 유일한 계층이 화면 하나로 줄어든다.
type ShowResult struct {
	Item model.Item `json:"item"`
	// Revisions 는 **rev 오름차순**이다(오래된 개정이 먼저). 각 행이 담은 title·body·paths 는
	// 고치기 직전의 값이고, Changed 는 그 사슬에서 복원한 "이 개정이 바꾼 축"이다.
	Revisions []model.ItemRevision `json:"revisions"`
	// Judgments 는 이 항목에 걸린 판단 **전문**이다. 최신 먼저, 동점이면 id 역순
	// (store.JudgmentsForItem 의 정렬 그대로).
	Judgments []model.Judgment `json:"judgments"`

	// Derived 는 두 파생 축(revisions·judgments)의 신선도다.
	//
	// ★ **0 과 못 잼을 가르는 유일한 자리다.** 개정이 0건인 것과 개정 이력을 못 읽은
	// 것은 다른 사실이고, 판단 0건과 판단을 못 읽은 것도 다른 사실이다. 못 읽었을 때
	// 빈 목록만 내면 되짚는 사람은 "앞선 판단이 없다"고 믿고 이미 기각된 길을 다시
	// 간다 — 이 표면이 존재하는 이유가 정확히 그것을 막는 것이다(DESIGN §5).
	Derived
}

// ShowItem 은 항목 하나의 이력을 읽는다.
//
// ★ **종료 상태를 안 본다.** 닫힌 항목을 거절하면 이 동사는 존재할 이유가 없다 —
// 위 머리말의 1,985건이 전부 그쪽에 있다.
//
// ★ 파생 실패는 결과를 안 죽인다(DESIGN §5). 항목 본문은 이미 읽었고, 개정 이력이나
// 판단을 못 읽었다는 것은 그 자체로 화면에 낼 값이 있는 사실이다.
func (s *Service) ShowItem(ctx context.Context, in ShowInput) (ShowResult, error) {
	var res ShowResult
	in.Project = strings.TrimSpace(in.Project)
	in.ItemID = strings.TrimSpace(in.ItemID)

	// ★ errors.New 가 아니라 RefusedError 다 — api.ClassifyError 는 화이트리스트라
	//   errors.New 는 아무 갈래에도 안 걸리고 500 internal 로 나간다. 두 갈래 다 MCP 에서
	//   정상 도달 가능하다(show 의 필수 인자는 item_id 하나뿐이고 project 는 선택이다).
	if in.Project == "" {
		return res, &RefusedError{
			What:     "show",
			Reason:   "프로젝트가 비었다",
			Guidance: "요청의 project 를 채워라. CLI 는 `.flightdeck.yaml` 의 프로젝트를 자동으로 싣는다.",
		}
	}
	// ★ 워크스페이스 관문 — 남의 프로젝트 항목은 `--project`(멤버 지정)로 간다.
	//   `item_project` 축은 안 만든다: amend 가 그렇게 정했고(tools.go 의 projectArg 주석),
	//   「어디에 걸까」와 「어디 있나 찾아줘」는 못 찾았을 때의 행동이 정반대다.
	if err := s.GateTargetProject(ctx, in.SessionID, in.Project); err != nil {
		return res, err
	}
	if in.ItemID == "" {
		return res, &RefusedError{
			What:     "show",
			Reason:   "읽을 항목 id 가 비었다",
			Guidance: "읽을 항목 id 를 줘라: `fd show <item-id>`. 닫힌 항목도 읽는다.",
		}
	}

	// ★ 항목 먼저다. 없는 항목은 여기서 store.ErrNotFound 를 단 채로 그대로 올라가
	//   표준 not-found 가 된다(errors.Is(err, store.ErrNotFound) 가 성립한다) — 이
	//   계층이 문구를 새로 지으면 좌표(NFItem + project/id)가 사라지고 404 처방표가
	//   무엇이 없었는지 말하지 못한다.
	it, err := s.st.GetItem(ctx, in.Project, in.ItemID)
	if err != nil {
		return res, err
	}
	res.Item = it

	now := s.now()
	d := &derive{}

	// ★ 개정 이력. 실패해도 결과를 안 죽인다 — 항목 본문은 이미 참이다.
	if revs, rerr := s.st.ItemRevisions(ctx, in.Project, in.ItemID); rerr != nil {
		s.log.WarnContext(ctx, "개정 이력을 못 읽었다 — 항목 본문은 그대로 낸다",
			"project", clip(in.Project, 64), "item", clip(in.ItemID, 64), "error", rerr.Error())
		d.fail("revisions", rerr)
	} else {
		// 사슬의 마지막 칸은 **지금 값**이다(위에서 읽은 it). store 가 그 값을 또
		// 읽지 않게 여기서 넘긴다 — 두 벌을 두면 두 읽기 사이가 창이 된다.
		store.MarkItemRevisionChanges(revs, it)
		res.Revisions = revs
	}

	// ★ 걸린 판단. judgment_link 역방향이고, 이 한 줄이 이 표면의 존재 이유다.
	if js, jerr := s.st.JudgmentsForItem(ctx, in.Project, in.ItemID); jerr != nil {
		s.log.WarnContext(ctx, "걸린 판단을 못 읽었다 — 항목 본문은 그대로 낸다",
			"project", clip(in.Project, 64), "item", clip(in.ItemID, 64), "error", jerr.Error())
		d.fail("judgments", jerr)
	} else {
		res.Judgments = js
	}

	res.Derived = d.result(now)
	return res, nil
}
