package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 큐 — 추천·선점·등록.

// PickMode 는 pick 한 번이 실제로 무엇을 했는지다.
//
// 넷을 가른다. 뭉개면 "추천만 받았다"와 "선점했다"가 구분되지 않아
// 에이전트가 안 잡은 항목을 잡은 줄 알고 일을 시작한다.
type PickMode string

const (
	PickRecommended PickMode = "recommended" // 추천 1건. **선점하지 않았다**
	PickClaimed     PickMode = "claimed"     // 지정한 항목을 새로 선점했다
	PickResumed     PickMode = "resumed"     // 이미 자기 선점이라 맥락을 다시 냈다(재개 경로)
	PickNone        PickMode = "none"        // 적격 0건. 탈락 사유가 전부 실린다
)

// PickInput 은 pick 한 번의 인자다. **파생값이 하나도 없다** —
// 브랜치·HEAD·겹침·의존 충족은 전부 서버가 읽는다.
type PickInput struct {
	Project   string
	SessionID string
	ItemID    string // 비면 추천, 있으면 선점
}

// PickResult 는 pick 한 번의 결과다.
type PickResult struct {
	Mode     PickMode          `json:"mode"`
	Reason   string            `json:"reason"` // 왜 이것인가 · 왜 못 골랐나. **항상 채운다**
	Item     *model.Item       `json:"item,omitempty"`
	Claim    *model.Claim      `json:"claim,omitempty"`
	Overlaps []judge.Overlap   `json:"overlaps,omitempty"` // 탈락 사유가 아니다. 거르지 않고 알린다
	Rejected []model.Rejection `json:"rejected,omitempty"` // 탈락 사유 **전부**
	Notes    []model.Judgment  `json:"notes,omitempty"`    // 이 항목에 연결된 판단 전문
	Branch   string            `json:"branch,omitempty"`   // 항목 id 가 곧 브랜치 이름이다(전역 유일)
	Setup    []string          `json:"setup,omitempty"`    // 워크트리 준비 명령
	Scope    string            `json:"scope"`              // 무엇을 후보로 봤나 — 안 본 것을 침묵하지 않는다
	// QueueOpen 은 **남은** 열린 항목 수다(이 호출이 집은 것을 뺀 값).
	//
	// ★ 포인터인 이유가 둘이다.
	//  1. 서버는 독립 컨테이너인데 플러그인은 자동 갱신된다. 구서버 + 신 클라이언트면
	//     이 키가 응답에 없고, 값 타입이면 0 으로 접혀 **신선한 온라인 응답이
	//     "큐 열림 0건" 을 단정한다**. SkewBanner 는 api_version 문자열만 보므로 안 뜬다.
	//  2. 오프라인 캐시에는 스키마 버전축이 없다 — 이 필드가 생기기 전에 굳은 next 응답이
	//     그대로 재생된다. nil 이면 "이 응답은 그 축을 안 낸다"로 정확히 읽힌다.
	QueueOpen *int `json:"queue_open,omitempty"`

	// PathCheck 는 이 항목이 선언한 경로가 이 프로젝트에 실재하는가다.
	//
	// ★ **포인터다.** nil 은 "이 응답은 그 축을 안 읽었다"를 뜻하고, 그 상태가 실제로 난다:
	// 오프라인 `fd next` 는 디스크 캐시의 옛 바이트를 그대로 다시 내는데, 이 필드가
	// 생기기 전에 저장된 캐시에는 키가 없어 역직렬화 후 nil 이 온다.
	// 값 타입이면 그 상황이 Kind:"" 라는 여섯 갈래 어디에도 없는 유령 상태가 되고,
	// 낡은 캐시가 관측한 적 없는 사실을 단정하게 된다.
	//
	// 적격 0건(PickNone)에도 nil 이다 — 항목이 없으면 관측할 대상이 없다.
	PathCheck *judge.ItemPathVerdict `json:"path_check,omitempty"`

	Derived
}

// ValidateItemID 는 항목 id 가 브랜치 이름·디렉토리 이름으로 쓰여도 안전한지 본다. 순수 함수다.
//
// ★ 이 값은 **셸 명령과 git ref 두 소비자**에게 그대로 간다(pick 이 워크트리 준비 명령을 낸다).
// 가드는 소비 계층에 둬야 한다 — 생성부에서만 막으면 이관·수입 경로로 들어온 id 가 그대로 샌다.
// 그래서 여기서 한 번, AddItem 에서 한 번, 명령을 만들 때 한 번 본다.
func ValidateItemID(id string) error {
	switch {
	case strings.TrimSpace(id) == "":
		return errors.New("항목 id 가 비었다")
	case len(id) > 100:
		return fmt.Errorf("항목 id 가 %d자다 — 브랜치 이름으로도 쓰이므로 100자 이하여야 한다", len(id))
	case strings.HasPrefix(id, "-"):
		return fmt.Errorf("항목 id %q 가 '-' 로 시작한다 — git 과 셸이 옵션으로 읽는다", clip(id, 64))
	case strings.HasPrefix(id, "."), strings.HasSuffix(id, "."):
		return fmt.Errorf("항목 id %q 가 '.' 로 시작하거나 끝난다 — git ref 규칙 위반이다", clip(id, 64))
	case strings.Contains(id, ".."):
		return fmt.Errorf("항목 id %q 에 '..' 가 있다 — git ref 규칙 위반이고 경로 탈출 통로다", clip(id, 64))
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '/':
		default:
			if unicode.IsControl(r) {
				return fmt.Errorf("항목 id 에 제어문자가 있다(%q)", clip(id, 64))
			}
			return fmt.Errorf("항목 id %q 에 쓸 수 없는 문자 %q 가 있다 — "+
				"[A-Za-z0-9._/-] 만 쓴다(브랜치 이름·디렉토리 이름·셸 인자로 그대로 나간다)", clip(id, 64), string(r))
		}
	}
	return nil
}

// WorktreeDir 는 항목 하나가 쓸 워크트리 상대 경로다. 순수 함수다.
func WorktreeDir(itemID string) string { return path.Join(".flightdeck", "worktrees", itemID) }

// SetupCommands 는 이 항목을 집은 세션이 그대로 붙여 넣을 준비 명령이다. 순수 함수다.
//
// id 가 안전하지 않으면 **명령을 만들지 않는다**(nil). 규율 산문이 아니라 부재로 막는다 —
// 틀린 명령을 내는 것보다 안 내는 쪽이 낫고, 사유는 호출부가 Reason 에 싣는다.
func SetupCommands(projectPath, defaultBranch, itemID string) []string {
	if ValidateItemID(itemID) != nil || strings.TrimSpace(projectPath) == "" {
		return nil
	}
	if strings.TrimSpace(defaultBranch) == "" {
		defaultBranch = "main"
	}
	dir := WorktreeDir(itemID)
	// ★ 경로를 인용한다. 이 문자열의 소비자는 사람이 아니라 **에이전트의 Bash 도구**다 —
	// pick 응답이 "이걸 실행해라"로 읽히도록 만들어져 있다.
	//
	// itemID 는 ValidateItemID 로 막는데 같은 줄의 projectPath 는 검증도 인용도 없었다.
	// 그 비대칭이 위험한 이유는 한쪽만 막은 가드가 **막는다고 믿게 만들기** 때문이다.
	// 악의가 없어도 경로에 공백 하나만 있으면 cd 가 조용히 다른 디렉토리로 가고,
	// 그 뒤 worktree 가 엉뚱한 저장소에 브랜치를 만든다.
	//
	// 검증만으로 끝내지 않는다 — 공백은 정당한 경로 문자라 거절할 수 없고, 인용만이 그 축을 덮는다.
	p := shellQuote(projectPath)
	return []string{
		"cd " + p,
		fmt.Sprintf("git worktree add %s -b %s %s", shellQuote(dir), itemID, shellQuote(defaultBranch)),
		"cd " + shellQuote(projectPath+"/"+dir),
	}
}

// shellQuote 는 POSIX 셸의 작은따옴표 인용이다.
//
// 작은따옴표 안에서는 어떤 문자도 특별하지 않다. 유일한 예외가 작은따옴표 자신이라
// 그것만 `'\”` 로 닫고-이스케이프-열기 한다. 이 규칙이면 개행·세미콜론·달러·백틱이
// 전부 리터럴이 된다 — 메타문자 목록을 유지보수할 필요가 없다는 것이 이 방식의 값어치다.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Pick 은 항목 하나를 추천하거나 선점한다.
//
// 지키는 것 넷:
//
//  1. 인자가 없으면 judge.Eligible 이 고르고, **탈락 사유 전부**를 함께 낸다.
//  2. 추천을 못 해도 pick_eval 을 남긴다 — 적격 0건도 기록이다.
//  3. 경로 겹침은 **거르지 않고 결과에 실어 낸다**(설계 §5).
//  4. 이미 자기 선점이면 거절이 아니라 **맥락 재출력**이다(재개 경로).
func (s *Service) Pick(ctx context.Context, in PickInput) (PickResult, error) {
	now := s.now()
	d := &derive{}

	if strings.TrimSpace(in.SessionID) == "" {
		return PickResult{}, &RefusedError{What: "pick", Reason: "session_id 가 비었다"}
	}
	proj, err := s.st.GetProject(ctx, in.Project)
	if err != nil {
		return PickResult{}, err
	}
	cards, err := s.sessionCards(ctx, proj, s.cut(now, 0), in.SessionID, d)
	if err != nil {
		return PickResult{}, err
	}
	live := liveFor(cards)
	selfCC := selfCCOf(cards, in.SessionID)

	var res PickResult
	if strings.TrimSpace(in.ItemID) != "" {
		res, err = s.pickExplicit(ctx, proj, in, live, selfCC, d, now)
	} else {
		res, err = s.pickRecommend(ctx, proj, in, live, selfCC, d, now)
	}
	if err != nil {
		return PickResult{}, err
	}
	// ★ 큐 규모는 **선점 쓰기가 끝난 뒤에** 센다.
	//
	// 먼저 세면 claimed 응답의 수에 방금 집은 항목이 들어간다(ClaimItem 이 open→claimed
	// 로 옮긴다). 그러면 같은 응답이 항목을 [claimed] 로 찍어 놓고 두 줄 밑에서 열림으로
	// 세고, 쓰기가 없는 재개 경로는 같은 세계에 대해 1 작은 수를 낸다 — 재출력이 원본과
	// 다른 수를 내면 그건 재출력이 아니다.
	//
	// 각주로는 못 덮는다: JudgeClaim 의 "상태가 claimed 인데 점유자가 없다" 갈래로 들어온
	// 선점은 항목이 애초에 open 이 아니라 오프셋이 0 이다. 고정 각주는 그 절반에서 거짓말이 된다.
	s.fillQueueOpen(ctx, proj.ID, &res)
	return res, nil
}

// fillQueueOpen 은 응답에 남은 큐 열림 수를 싣는다.
//
// **실패해도 pick 을 실패시키지 않는다.** 표시용 숫자 하나 때문에 선점을 잃는 것이
// 더 나쁘고, nil 은 렌더가 "이 응답에 없다"로 정확히 말한다.
//
// derive(d.note/d.fail)에 넣지 않는다 — FreshnessOf 가 failures>0 을 **git 축** Stale 로
// 접기 때문에, DB 카운트 한 번이 실패했을 뿐인데 세션이 브랜치·HEAD·조상 판정이
// 낡았다고 읽게 된다.
func (s *Service) fillQueueOpen(ctx context.Context, project string, res *PickResult) {
	if res.QueueOpen != nil {
		return // 추천 경로가 candidates() 의 관측을 이미 실었다. 같은 사실을 두 번 세지 않는다
	}
	n, err := s.st.CountOpen(ctx, project)
	if err != nil {
		s.log.WarnContext(ctx, "큐 열림 수 조회 실패",
			"project", clip(project, 64), "error", err.Error())
		return
	}
	res.QueueOpen = &n
}

// pickExplicit 은 지정된 항목을 선점한다(또는 재개 맥락을 낸다).
func (s *Service) pickExplicit(ctx context.Context, proj model.Project, in PickInput,
	live []judge.LiveSession, selfCC string, d *derive, now time.Time) (PickResult, error) {

	item, err := s.st.GetItem(ctx, proj.ID, in.ItemID)
	if err != nil {
		return PickResult{}, err
	}

	res := PickResult{Item: &item, Branch: item.ID, Scope: "지정된 항목 1건"}
	res.Overlaps = judge.OverlapsWithLive(item.Paths, live, in.SessionID, selfCC)
	res.Setup = SetupCommands(proj.Path, proj.DefaultBranch, item.ID)
	res.PathCheck = s.checkItemPaths(ctx, proj, item.Paths)
	if res.Setup == nil {
		d.note("setup:"+clip(item.ID, 64),
			"항목 id 가 브랜치·디렉토리 이름으로 안전하지 않아 워크트리 준비 명령을 만들지 않았다")
	}

	// 재개인지 먼저 본다. 재개면 아무것도 쓰지 않는다 — 선점 시각을 덮으면
	// "언제부터 쥐고 있나"가 사라지고, 그 값이 회수 판단의 다섯 축 중 하나다.
	cur, cerr := s.st.GetClaim(ctx, proj.ID, item.ID)
	resume := cerr == nil && cur.ReleasedAt == nil && cur.SessionID == in.SessionID &&
		item.State != model.ItemDone && item.State != model.ItemDropped
	if cerr != nil && !errors.Is(cerr, store.ErrNotFound) {
		return PickResult{}, cerr
	}

	if resume {
		res.Mode, res.Claim = PickResumed, &cur
		res.Reason = fmt.Sprintf("이미 이 세션(%s)의 선점이다 — 맥락을 다시 낸다", in.SessionID)
		if notes, err := s.linkedJudgments(ctx, proj.ID, item.ID); err != nil {
			return PickResult{}, err
		} else {
			res.Notes = notes
		}
		s.st.LogEvent(ctx, "item.resume", proj.ID, in.SessionID, map[string]any{"item": item.ID})
		res.Derived = d.result(now)
		s.log.InfoContext(ctx, "선점 재개", "project", proj.ID, "session_id", in.SessionID, "item", item.ID)
		return res, nil
	}

	// 새 선점. 판정은 store.JudgeClaim 이 하고 여기서 흉내 내지 않는다 —
	// 흉내 내면 조회와 삽입 사이에 남이 잡는 창이 생긴다.
	var claim model.Claim
	err = s.st.Tx(ctx, func(t *store.Tx) error {
		// 시도를 먼저 예약한다 — 롤백돼도 남는다(거절당한 선점도 원장의 자산이다).
		t.LogEvent("item.claim", proj.ID, in.SessionID, map[string]any{
			"item": item.ID, "paths": len(item.Paths), "overlaps": len(res.Overlaps),
		})
		c, err := t.ClaimItem(proj.ID, item.ID, in.SessionID)
		if err != nil {
			return err
		}
		claim = c
		// 항목이 선언한 경로를 이 세션의 발자국으로 남긴다(origin=claimed).
		// 착수 직후 구간은 브랜치 diff 가 정의상 비어 있어 이 축이 그 구간을 덮는다.
		for _, p := range item.Paths {
			if err := t.Touch(in.SessionID, p, model.OriginClaimed, c.At); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.logFail(ctx, "item.claim", proj.ID, in.SessionID, err)
		s.log.ErrorContext(ctx, "선점 실패",
			"project", proj.ID, "session_id", clip(in.SessionID, 64), "item", clip(item.ID, 64),
			"error", err.Error())
		return PickResult{}, err
	}

	// 선점 뒤 상태를 다시 읽는다 — ClaimItem 이 항목 상태를 claimed 로 바꾼다.
	if fresh, err := s.st.GetItem(ctx, proj.ID, item.ID); err == nil {
		item = fresh
		res.Item = &item
	}
	res.Mode, res.Claim = PickClaimed, &claim
	res.Reason = fmt.Sprintf("항목 %s 를 선점했다", item.ID)
	if notes, err := s.linkedJudgments(ctx, proj.ID, item.ID); err != nil {
		return PickResult{}, err
	} else {
		res.Notes = notes
	}
	res.Derived = d.result(now)
	s.log.InfoContext(ctx, "선점",
		"project", proj.ID, "session_id", in.SessionID, "item", item.ID, "overlaps", len(res.Overlaps))
	return res, nil
}

// pickRecommend 는 적격 항목 하나를 고르고 탈락 사유 전부를 남긴다.
func (s *Service) pickRecommend(ctx context.Context, proj model.Project, in PickInput,
	live []judge.LiveSession, selfCC string, d *derive, now time.Time) (PickResult, error) {

	cands, scope, openCount, err := s.candidates(ctx, proj, live)
	if err != nil {
		return PickResult{}, err
	}
	facts := s.afterFacts(ctx, proj, cands, d)
	held, err := s.heldResources(ctx, proj.ID)
	if err != nil {
		return PickResult{}, err
	}

	picked, rejected := judge.Eligible(judge.EligibleInput{
		Self: in.SessionID, SelfCC: selfCC, Candidates: cands, Live: live, Facts: facts, HeldResources: held,
	})

	res := PickResult{Rejected: rejected, Scope: scope, QueueOpen: &openCount}
	eval := model.PickEval{Project: proj.ID, SessionID: in.SessionID, Rejected: rejected}
	if picked != nil {
		eval.Picked = picked.Item.ID
	}
	// ★ 적격 0건도 기록이다. 사유가 없으면 큐는 블랙박스가 되고,
	//   블랙박스는 두 번째 세션부터 무시된다.
	if err := s.st.RecordPickEval(ctx, eval); err != nil {
		return PickResult{}, err
	}
	s.st.LogEvent(ctx, "item.pick", proj.ID, in.SessionID, map[string]any{
		"picked": eval.Picked, "count": len(cands), "skipped": len(rejected),
	})

	if picked == nil {
		res.Mode = PickNone
		res.Reason = fmt.Sprintf("적격 항목이 0건이다(후보 %d건, 탈락 사유 %d줄). %s",
			len(cands), len(rejected), scope)
		res.Derived = d.result(now)
		s.log.InfoContext(ctx, "추천 없음",
			"project", proj.ID, "session_id", in.SessionID, "count", len(cands), "skipped", len(rejected))
		return res, nil
	}

	item := picked.Item
	res.Mode, res.Item, res.Branch = PickRecommended, &item, item.ID
	res.Overlaps = picked.Overlaps
	res.Setup = SetupCommands(proj.Path, proj.DefaultBranch, item.ID)
	res.PathCheck = s.checkItemPaths(ctx, proj, item.Paths)
	if res.Setup == nil {
		d.note("setup:"+clip(item.ID, 64),
			"항목 id 가 브랜치·디렉토리 이름으로 안전하지 않아 워크트리 준비 명령을 만들지 않았다")
	}
	res.Reason = fmt.Sprintf("의존자 %d건 · %s 생성 · 후보 %d건 중 1순위다. "+
		"아직 선점하지 않았다 — 집으려면 item_id 를 주고 다시 불러라",
		picked.Dependents, item.CreatedAt.Format("2006-01-02"), len(cands))
	if notes, err := s.linkedJudgments(ctx, proj.ID, item.ID); err != nil {
		return PickResult{}, err
	} else {
		res.Notes = notes
	}
	res.Derived = d.result(now)
	s.log.InfoContext(ctx, "추천",
		"project", proj.ID, "session_id", in.SessionID, "item", item.ID,
		"count", len(cands), "skipped", len(rejected), "overlaps", len(res.Overlaps))
	return res, nil
}

// candidates 는 판정에 넣을 후보 집합과 **그 범위를 설명하는 문장**을 만든다.
//
// 범위는 열린 항목 ∪ 살아 있는 세션이 쥔 항목이다.
// ★ 살아 있지 않은 세션이 쥔 항목은 여기 안 들어온다(저장 계층에 전 항목 열거가 없다).
// 그 사실을 Scope 문장으로 낸다 — 안 본 것을 침묵하면 "겹침 없음"과 "이 축을 안 본다"가
// 구분되지 않는 것과 똑같은 실패가 후보 집합에서 재현된다.
func (s *Service) candidates(ctx context.Context, proj model.Project, live []judge.LiveSession) ([]judge.Candidate, string, int, error) {
	open, err := s.st.ListOpen(ctx, proj.ID)
	if err != nil {
		return nil, "", 0, err
	}
	items := make([]model.Item, 0, len(open))
	seen := map[string]bool{}
	for _, it := range open {
		items = append(items, it)
		seen[it.ID] = true
	}

	claimedCount := 0
	for _, l := range live {
		ids, err := s.st.ClaimedItems(ctx, l.ID)
		if err != nil {
			return nil, "", 0, err
		}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			it, err := s.st.GetItem(ctx, proj.ID, id)
			if errors.Is(err, store.ErrNotFound) {
				continue // 다른 프로젝트의 선점이다
			}
			if err != nil {
				return nil, "", 0, err
			}
			seen[id] = true
			items = append(items, it)
			claimedCount++
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })

	cands := make([]judge.Candidate, 0, len(items))
	for _, it := range items {
		c := judge.Candidate{Item: it}
		if cl, err := s.st.GetClaim(ctx, proj.ID, it.ID); err == nil && cl.ReleasedAt == nil {
			c.ClaimedBy = cl.SessionID
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, "", 0, err
		}
		if c.Dependents, err = s.st.Dependents(ctx, proj.ID, it.ID); err != nil {
			return nil, "", 0, err
		}
		// Needs 는 .flightdeck.yaml 의 resources 에서 온다. Tier A 에는 그 파일을 읽는
		// 코드가 없으므로 **비어 있다** — 지어내지 않는다. 자원 축은 그때 살아난다.
		cands = append(cands, c)
	}
	scope := fmt.Sprintf("후보 = 열린 항목 %d건 + 살아 있는 세션이 쥔 항목 %d건. "+
		"살아 있지 않은 세션이 쥔 항목은 후보에 없다", len(open), claimedCount)
	// ★ len(open) 을 scope 문자열과 **같은 자리에서** 낸다. 호출부가 따로 세면 그 사이에
	// sessionCards 가 끼어들어 인접한 두 줄이 다른 수를 찍을 수 있고, 나중에 이 함수의
	// 술어가 바뀌면 두 줄이 영구히 갈린다.
	return cands, scope, len(open), nil
}

// afterFacts 는 선행 조건 판정에 필요한 **사실**을 모은다. 판정은 judge 가 한다.
//
// ★ 키 부재와 값을 가른다. 못 읽은 축은 **넣지 않는다** — 넣으면 "조회하지 않았다"가
// "충족되지 않았다"로 접히고, 조회를 빠뜨린 버그가 정상적인 대기로 보인다.
// 대신 못 읽었다는 사실을 Failures 에 남긴다.
func (s *Service) afterFacts(ctx context.Context, proj model.Project, cands []judge.Candidate, d *derive) judge.AfterFacts {
	f := judge.AfterFacts{
		ItemStates:  map[string]model.ItemState{},
		JobStates:   map[string]string{},
		SHAAncestry: map[string]judge.AncestryResult{},
	}
	var g GitReader
	if strings.TrimSpace(proj.Path) != "" {
		g = s.git(proj.Path)
	}
	for _, c := range cands {
		for _, a := range c.Item.After {
			switch {
			case a.Item != "":
				if _, done := f.ItemStates[a.Item]; done {
					continue
				}
				dep, err := s.st.GetItem(ctx, proj.ID, a.Item)
				if errors.Is(err, store.ErrNotFound) {
					// 키를 안 넣는다 → after-unknown. 그리고 그 사실을 표면에 낸다:
					// 존재하지 않는 선행은 "기다리면 풀린다"가 아니라 오타다.
					d.note("after-item:"+clip(a.Item, 64),
						fmt.Sprintf("항목 %s 의 선행 %s 가 큐에 없다 — 오타이거나 지워졌다", c.Item.ID, a.Item))
					continue
				}
				if err != nil {
					d.fail("after-item:"+clip(a.Item, 64), err)
					continue
				}
				f.ItemStates[a.Item] = dep.State

			case a.Job != "":
				// 잡은 Tier B 다. 조회하지 않았다는 사실을 그대로 둔다(키 부재 = after-unknown).
				d.note("after-job:"+clip(a.Job, 64),
					"잡 상태는 Tier B 다 — 이 서버는 잡을 조회하지 않는다(판정 자체를 안 했다)")

			case a.SHA != "":
				if _, done := f.SHAAncestry[a.SHA]; done {
					continue
				}
				if g == nil {
					d.note("after-sha:"+clip(a.SHA, 40), "프로젝트 경로가 없어 조상 판정을 못 했다")
					continue
				}
				res, err := g.Ancestry(ctx, a.SHA, proj.DefaultBranch)
				if err != nil {
					d.fail("after-sha:"+clip(a.SHA, 40), err)
					continue
				}
				d.ok()
				f.SHAAncestry[a.SHA] = res
			}
		}
	}
	return f
}

// heldResources 는 지금 쥐어져 있는 자원의 점유자 색인이다.
func (s *Service) heldResources(ctx context.Context, project string) (map[string]string, error) {
	holds, err := s.st.ListHeld(ctx, project)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, h := range holds {
		holder := h.SessionID
		if holder == "" {
			holder = "job:" + h.JobID // 잡이 쥔 것도 남이 쥔 것이다. 빈 문자열로 접으면 안 쥔 것이 된다
		}
		out[h.Resource] = holder
	}
	return out, nil
}

// linkedJudgments 는 항목 하나에 연결된 판단 전문이다.
//
// ★ 저장 계층에 "링크 대상으로 찾기" 조회가 없어서 **종류별로 훑어 링크로 거른다**.
// 인덱스(judgment_link_by_target)는 있으나 접근자가 없고, 그 접근자를 만드는 것은
// 이 계층의 담당이 아니다. 종류 수가 고정(9)이라 질의 수는 항목 수와 무관하다.
func (s *Service) linkedJudgments(ctx context.Context, project, itemID string) ([]model.Judgment, error) {
	kinds := []model.JudgmentKind{
		model.JudgmentHandoff, model.JudgmentDecision, model.JudgmentBlocked,
		model.JudgmentAsk, model.JudgmentNow, model.JudgmentRejected,
		model.JudgmentNotDone, model.JudgmentVerified, model.JudgmentDraft,
	}
	var out []model.Judgment
	for _, k := range kinds {
		js, err := s.st.ListJudgmentsByKind(ctx, project, k, 50)
		if err != nil {
			return nil, err
		}
		for _, j := range js {
			for _, l := range j.Links {
				if l.TargetKind == "item" && l.TargetID == itemID {
					out = append(out, j)
					break
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At) // 최신이 먼저
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 항목 등록
// ─────────────────────────────────────────────────────────────────────────────

// AddItemInput 은 큐 항목 하나를 만드는 인자다.
//
// 사람이 주는 것은 title·body·paths·after 뿐이다. 상태·랜딩 sha·역인덱스는 서버가 채운다
// (Q 계층의 쓰기 권한 — 설계 §3).
type AddItemInput struct {
	Project   string
	SessionID string
	ID        string
	Title     string
	Body      string
	Paths     []string
	Labels    []string // 표시 전용. 어떤 배제 판정에도 안 쓴다
	After     []model.After
}

// judgeItemPathsCoordinate 는 경로 목록에 좌표계 관문(judge.JudgePathCoordinate)을
// 순서대로 태운다. 처음 위반한 경로의 순번과 사유를 "%d번째 경로: %s" 형태로 담아
// 낸다 — 위반이 없으면 nil.
//
// ★ 표기 규약: **사람이 읽는 "%d번째" 는 1-based 다.** range 인덱스를 그대로 실으면
// 목록의 세 번째 것이 "2번째"로 나오고, 사람은 그 말을 믿고 두 번째 것을 고치러 간다.
// 사유는 사람이 고칠 수 있어야 사유다(coordinateGuidance 와 같은 규율). 이 규약은
// service·store·cmd/fd 를 걸쳐 있어 한 패키지 시험으로 못 잡으므로 소스 전수 가드가
// 따로 있다 — indexnotation_test.go. 새로 %d번째 를 쓰면 그 가드가 걸린다.
//
// add(item.paths)와 finish(followup.paths)가 이 헬퍼를 공유한다. 둘 다 사람/에이전트가
// 대화형으로 등록하는 경로이고, 훅이 자동으로 보내는 발자국과 달리 스펙 §4.2 의
// "사람이 넣으면 거절" 기준에 정확히 해당한다. 오류를 RefusedError 로 감싸는 것과
// What·Guidance 는 호출부마다 다르므로 여기서는 안 한다 — 순수하게 판정만 나른다.
func judgeItemPathsCoordinate(paths []string) error {
	for i, p := range paths {
		if v := judge.JudgePathCoordinate(p); !v.OK {
			return fmt.Errorf("%d번째 경로: %s", i+1, v.Reason)
		}
	}
	return nil
}

// AddItem 은 큐 항목을 만든다.
func (s *Service) AddItem(ctx context.Context, in AddItemInput) (model.Item, error) {
	if err := ValidateItemID(in.ID); err != nil {
		return model.Item{}, &RefusedError{What: "add", Reason: err.Error(),
			Guidance: "항목 id 는 브랜치 이름과 워크트리 디렉토리 이름으로 그대로 쓰인다."}
	}
	if strings.TrimSpace(in.Title) == "" {
		return model.Item{}, &RefusedError{What: "add", Reason: "제목이 비었다"}
	}
	if strings.TrimSpace(in.Body) == "" {
		return model.Item{}, &RefusedError{What: "add",
			Reason: "본문이 비었다",
			Guidance: "무엇을 해야 하는지가 없으면 다음 세션이 이 항목을 집을 수 없다 — " +
				"제목은 좌표이고 본문이 내용이다."}
	}
	for i, a := range in.After {
		if err := store.ValidateAfter(a); err != nil {
			return model.Item{}, &RefusedError{What: "add",
				Reason: fmt.Sprintf("%d번째 선행 조건: %v", i+1, err),
				Guidance: "미랜딩 선행은 항목 id 로, 랜딩된 것은 sha 로 가리켜라 — " +
					"브랜치 이름을 담을 자리가 없다(랜딩이 끝나면 브랜치가 지워져 그 순간 해석 불가가 된다)."}
		}
	}

	// ★ item.paths 는 가장 큰 경로 컬럼인데 여기 오기 전까지 검증이 하나도 없었다.
	// 세션 worktree 와 달리 클라이언트 OS 라는 관문조차 없어서 사람이 무엇을 붙여넣든
	// 들어온다. 통과시키면 그 항목의 겹침 축이 **조용히** 죽는다 — 오류가 아니라
	// '겹침 없음'이라 정상 응답과 구분되지 않는다.
	//
	// ★ finish 의 followup 경로(finish.go)도 judgeItemPathsCoordinate 를 그대로 쓴다.
	// finish 는 t.AddItem 을 직접 불러 이 함수의 검증을 거치지 않으므로, 거기서 따로
	// 부르지 않으면 같은 사람이 같은 세션에서 add 는 거절당하고 finish 는 조용히
	// 통과하는 반쪽 관문이 된다 — 반쪽 발화는 균일한 부재보다 나쁘다.
	//
	// ★ item.paths 로 가는 문은 **셋**이다. 이 주석은 오래 둘만 세고 있었다.
	// 세 번째는 레거시 이관(legacy/apply.go 의 tx.AddItem)이고, 그 관문은 여기가
	// 아니라 계획 쪽(legacy/plan.go, code="bad_path_coordinate")에 있다.
	// 규율도 다르다 — add·finish 는 **거절**하고 이관은 **그 경로만 버리고 남긴다.**
	// 갈린 이유는 고칠 사람이 그 자리에 있느냐다(legacy/plan.go 의 그 주석을 보라).
	if err := judgeItemPathsCoordinate(in.Paths); err != nil {
		return model.Item{}, &RefusedError{What: "add",
			Reason: err.Error(),
			Guidance: "경로는 저장소 상대(internal/api/x.go) 또는 POSIX 절대경로여야 한다 — " +
				"좌표계가 다르면 이 항목의 겹침 축이 조용히 죽는다."}
	}

	it := model.Item{
		Project: in.Project, ID: in.ID, Title: in.Title, Body: in.Body,
		Paths: in.Paths, Labels: in.Labels, State: model.ItemOpen, After: in.After,
	}
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		t.LogEvent("item.add", in.Project, in.SessionID, map[string]any{
			"item": it.ID, "paths": len(it.Paths), "after": len(it.After),
		})
		return t.AddItem(it)
	})
	if err != nil {
		s.logFail(ctx, "item.add", in.Project, in.SessionID, err)
		s.log.ErrorContext(ctx, "항목 등록 실패",
			"project", clip(in.Project, 64), "item", clip(in.ID, 64), "error", err.Error())
		return model.Item{}, err
	}
	saved, err := s.st.GetItem(ctx, in.Project, in.ID)
	if err != nil {
		return model.Item{}, err
	}
	s.log.InfoContext(ctx, "항목 등록",
		"project", in.Project, "session_id", in.SessionID, "item", it.ID, "count", len(it.After))
	return saved, nil
}
