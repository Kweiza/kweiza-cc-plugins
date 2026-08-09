package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
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
	ItemID    string   // 비면 추천, 있으면 단독 선점
	ItemIDs   []string // 묶음 선점. **첫째가 선두**이고 그 id 가 브랜치가 된다
}

// PickResult 는 pick 한 번의 결과다.
type PickResult struct {
	Mode     PickMode        `json:"mode"`
	Reason   string          `json:"reason"` // 왜 이것인가 · 왜 못 골랐나. **항상 채운다**
	Item     *model.Item     `json:"item,omitempty"`
	Claim    *model.Claim    `json:"claim,omitempty"`
	Overlaps []judge.Overlap `json:"overlaps,omitempty"` // 탈락 사유가 아니다. 거르지 않고 알린다
	// Rejected 는 **추천**(pickRecommend) 경로가 낸 탈락 사유 전부다 — 후보 중
	// 적격에 못 든 것들이 여기 온다.
	//
	// ★ 묶음 선점(pickBundle) 경로의 구성원 실패는 여기 **안 온다** — 그 사유는
	// Bundle.Members[].Rejection 에 산다(리뷰 라운드 1 finding 4). 두 곳에 겹쳐 실으면
	// 같은 실패를 두 번 세게 된다 — 침묵보다 중복 계수가 낫다는 말은 여기엔 안 통한다,
	// 이건 침묵이 아니라 **같은 사실을 두 자리에 적는 것**이라 사유 분포 집계가 깨진다.
	// 그래서 일부러 한 곳에만 둔다.
	Rejected []model.Rejection `json:"rejected,omitempty"`
	Notes    []model.Judgment  `json:"notes,omitempty"`  // 이 항목에 연결된 판단 전문
	Branch   string            `json:"branch,omitempty"` // 항목 id 가 곧 브랜치 이름이다(전역 유일)
	Setup    []string          `json:"setup,omitempty"`  // 워크트리 준비 명령
	Scope    string            `json:"scope"`            // 무엇을 후보로 봤나 — 안 본 것을 침묵하지 않는다
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

	// CloseDeclared 는 이 항목을 닫으려다 **롤백된** 선언이다(원장의 item.finish 인데
	// 항목은 done/dropped 이 아니다).
	//
	// ★ **포인터다.** PathCheck 과 같은 이유이고, 그 상태가 실제로 난다: 구서버 + 신
	// 클라이언트, 그리고 이 필드가 생기기 전에 굳은 오프라인 캐시가 그대로 재생된다.
	// 값 타입이면 그 상황이 "선언 0건"으로 접혀 **관측한 적 없는 사실을 단정한다** —
	// 하필 그 단정이 "이 항목은 깨끗하다"라서, 이 축이 막으려는 사고를 그대로 통과시킨다.
	//
	//	nil            = 이 응답은 그 축을 안 읽었다
	//	non-nil, 0건   = 읽었고 선언이 없다
	//	non-nil, n건   = 읽었고 n번 닫히려다 롤백됐다
	//
	// 왜 읽었는데 못 읽는 갈래가 있나 — 원장 조회가 실패했을 때다. 추천 경로
	// (pickRecommend)는 그 사실을 항목마다 반복하지 않고 Bundle.Scope 가 한 번
	// 말한다(bundleScope 의 closeRead). item_id 지정 선점·재개·묶음 구성원은
	// 후보 집합 자체가 없어 그런 Scope 절이 없으므로, 이 포인터 하나가 그 경로들의
	// 유일한 신호다.
	//
	// **다섯 갈래 전부에서 채운다**(추천 선두·구성원, item_id 선점·재개, 묶음
	// 선두·구성원 — PathCheck 과 같은 계약이다). 한때는 추천 경로에서만 채웠다 —
	// pickExplicit(:339 부근)의 주석이 그 실패를 이미 Bundle 축에 대해 적어 뒀는데
	// ("pick 다섯 갈래 중 셋이 이 자리를 지나므로 여기서 안 채우면 신선한 온라인
	// 응답에 거짓 원인이 붙는다") 이 브랜치가 같은 규율을 새 축(CloseDeclared)에는
	// 걸지 않았다. 세션이 회수된 항목을 `pick item_id=X` 로 **직접** 집으면
	// pickExplicit 을 타므로 그 세 갈래에서 경고를 한 번도 못 봤다 — 그 구멍을
	// 여기서 닫는다.
	//
	// ★ 이 수는 **하한이다.** 원장에 안 써진 마무리가 있을 수 있다 — BeginTx 가 실패한
	// 트랜잭션은 이벤트를 예약조차 안 하고, 쓰기 실패는 WARN 으로 삼킨다
	// (store.CloseDeclarationsByItem 의 doc 이 남는 사유 셋을 센다). 문구가 그렇게 말해야 한다.
	CloseDeclared *model.CloseDeclaration `json:"close_declared,omitempty"`

	// Bundle 은 이 응답이 낸 묶음이다.
	//
	// ★ **포인터다.** QueueOpen·PathCheck 과 같은 이유이고, 그 상태가 실제로 난다:
	// 서버는 독립 컨테이너인데 플러그인은 자동 갱신되고(구서버 + 신 클라이언트),
	// 오프라인 `fd next` 는 이 필드가 생기기 전에 굳은 디스크 캐시를 그대로 재생한다.
	// 슬라이스만 두면 그 상태가 **"묶을 게 하나도 없다"를 단정한다** — 관측한 적 없는 사실을.
	// SkewBanner 는 api_version 문자열만 보므로 필드 추가로는 안 뜬다.
	//
	// nil = 이 응답은 묶음 축을 안 읽었다 · 구성원 0건 = 묶을 게 없어 단독이다.
	//
	// 적격 0건(PickNone)에도 nil 이다 — PathCheck 와 같은 이유로, 선두가 없으면
	// 방사형으로 붙일 이웃도 애초에 없다(관측할 대상이 없다).
	Bundle *BundleInfo `json:"bundle,omitempty"`

	Derived
}

// BundleInfo 는 pick 한 번이 낸 묶음이다. **저장되지 않는다.**
type BundleInfo struct {
	Members []BundleMember `json:"members"` // 선두 제외
	Reason  string         `json:"reason"`  // 정렬 네 키의 실제 값
	Scope   string         `json:"scope"`   // 무엇을 이웃 후보로 봤나 — 형제 축을 못 읽었으면 그 사실도 여기 남는다
}

// BundleMember 는 묶음 구성원 하나다.
type BundleMember struct {
	// Item 은 이 구성원의 항목 전문이다.
	//
	// ★ 못 집은 구성원도 채운다(리뷰 라운드 1 finding 1). pickExplicit 이 선점에
	// 실패하면 자기가 이미 읽은 항목도 버리고 빈 PickResult 를 낸다(그 함수의 본문은
	// 이 태스크가 안 고친다 — 전역 제약). 그래서 pickBundle 이 실패한 구성원마다
	// 표시용으로 한 번 더 읽는다: 추천 경로는 항상 실물 Item 을 내므로, 묶음 경로도
	// 같은 모양을 지켜야 이 필드를 그대로 읽는 화면(태스크 10)이 못 집은 구성원만
	// 빈 줄(id="", state="")로 찍는 일이 없다.
	//
	// 딱 한 갈래는 못 채운다 — 요청한 id 가 애초에 큐에 없을 때(재조회도 실패한다).
	// 그때는 State 등 나머지 필드를 정직하게 비운 채로 두되 **ID 만은 채운다**
	// (요청받은 id 를 보존한다) — 존재하지 않는 항목의 상태를 지어내지 않는다.
	// (같은 id 는 Link.Item·Rejection.Item 에도 있지만, 화면이 늘 보는 자리는
	// Item 이라서 거기에도 있어야 한다.)
	Item      model.Item             `json:"item"`
	Link      judge.Link             `json:"link"` // 왜 선두와 묶였나
	PathCheck *judge.ItemPathVerdict `json:"path_check,omitempty"`
	// CloseDeclared 는 이 구성원을 닫으려다 롤백된 선언이다. 계약은 PickResult 쪽과
	// 글자 그대로 같다(nil = 그 축을 안 읽었다 · non-nil 0건 = 읽었고 없다).
	//
	// ★ 선두와 **양쪽 다** 있어야 한다. renderBundle 은 BundleInfo 하나만 받고 Members
	// 는 정의상 선두 제외라 선두를 모르는데, 이 사고의 항목은 정확히 선두였다.
	CloseDeclared *model.CloseDeclaration `json:"close_declared,omitempty"`
	Notes         []model.Judgment        `json:"notes,omitempty"` // 집었을 때만 전문
	// Claimed 는 이 구성원이 실제로 선점됐는가다.
	//
	// ★ 태스크 8이 예고했던 세 번째 상태("채택 시도했지만 실패")가 실제로 생겼다
	// (pickBundle). 그런데도 **포인터로 승격하지 않기로 했다** — bool 을 유지하되
	// Claimed 와 Rejection 을 **항상 쌍으로 읽게** 계약을 건다:
	//
	//	Claimed=true              → 집었다. Rejection 은 nil 이다.
	//	Claimed=false, Rejection≠nil → 집으려 시도했고 실패했다. 사유가 여기 있다.
	//	Claimed=false, Rejection=nil → 이 응답을 만든 경로가 채택 축을 아예 안 봤다
	//	                                (추천 경로 — 아직 안 집은 것이라 시도 자체가 없다).
	//
	// 세 상태를 가르는 것은 Claimed 혼자가 아니라 **이 두 필드의 쌍**이다. 그래서
	// bool 로도 충분하다 — Bundle·PathCheck·QueueOpen 처럼 "한 필드가 곧 그 축을
	// 읽었는지 여부까지 말해야 하는" 자리가 아니라, 이웃 필드(Rejection)가 이미
	// 그 역할을 나눠 맡는 자리다. 지키는 쪽의 의무는 하나: **집기를 시도했는데
	// 실패한 모든 갈래는 반드시 Rejection 을 채운다**(pickBundle 의 rejectionOf 가
	// 그 의무를 진다) — 하나라도 빠뜨리면 그 실패가 "안 봤다"로 오독된다.
	Claimed   bool             `json:"claimed"`
	Rejection *model.Rejection `json:"rejection,omitempty"` // 못 집었으면 사유(코드+상세). Claimed=false 인데 이게 nil 이면 "이 축을 안 봤다"는 뜻이다
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
//  1. 인자가 없으면 judge.EligibleBundle 이 고르고, **탈락 사유 전부**를 함께 낸다.
//  2. 추천을 못 해도 pick_eval 을 남긴다 — 적격 0건도 기록이다.
//  3. 경로 겹침은 **거르지 않고 결과에 실어 낸다**(설계 §5).
//  4. 이미 자기 선점이면 거절이 아니라 **맥락 재출력**이다(재개 경로).
func (s *Service) Pick(ctx context.Context, in PickInput) (PickResult, error) {
	now := s.now()
	d := &derive{}

	if strings.TrimSpace(in.SessionID) == "" {
		return PickResult{}, &RefusedError{What: "pick", Reason: "session_id 가 비었다"}
	}
	// item_ids 가 왔는데(길이 > 0) 다듬고 나면 쓸 게 하나도 없으면(전부 공백·중복)
	// 그 요청은 "추천해 달라"가 아니라 잘못 채운 묶음 요청이다. 조용히 추천 경로로
	// 미끄러지면 세션은 묶음을 넣은 줄 알고 기다리는데 서버는 다른 질문에 답한다.
	if len(in.ItemIDs) > 0 && len(dedupeIDs(in.ItemIDs)) == 0 {
		return PickResult{}, &RefusedError{What: "pick",
			Reason: "item_ids 에 쓸 수 있는 항목 id 가 없다"}
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
	switch {
	case len(in.ItemIDs) > 0:
		res, err = s.pickBundle(ctx, proj, in, live, selfCC, d, now)
	case strings.TrimSpace(in.ItemID) != "":
		res, err = s.pickExplicit(ctx, proj, in, live, selfCC, d, now)
	default:
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
	// ★ 묶음 축을 **이 경로에서도 낸다.** 이것이 이 브랜치가 세운 포인터 계약이다:
	//
	//	nil        = 이 응답은 그 축을 **안 읽었다** (구서버 · 이 필드 이전에 굳은 캐시)
	//	구성원 0건  = 읽었고, 이 응답이 함께 낸 항목이 없다(단독이다)
	//
	// 안 채우면 무엇이 깨지나: 렌더가 nil 을 "낡은 캐시이거나 서버가 이 축을 모르는
	// 판이다"로 찍는다. 그런데 pick 다섯 갈래 중 **셋**(item_id 지정 선점 · 재개 ·
	// 묶음 선두)이 이 자리를 지나므로, 현행 서버의 **신선한 온라인 응답**에 그 문장이
	// 붙는다 — 두 원인 다 거짓이고, 그걸 읽은 세션은 있지도 않은 서버 스큐를 고치러
	// 간다. 이건 이 저장소가 다른 모든 자리에서 금지하는 "관측 안 함을 값으로 접는"
	// 실패 그 자체다(QueueOpen·PathCheck 이 포인터인 이유와 같다).
	//
	// 구성원 0건이 "묶을 게 없다"로 읽히지 않도록 Scope 에 **왜 0건인지**를 적는다.
	// 두 갈래의 0건이 말하는 사실이 다르기 때문이다:
	//	추천 경로   — 이웃을 찾았고 직접 이어진 것이 없었다
	//	이 경로     — 이웃을 애초에 안 찾았다(방사형 판정을 안 돌린다)
	// 같은 값(빈 목록)으로 접히면 세션은 이 항목에 형제가 없다고 잘못 결론짓는다.
	//
	// pickBundle 은 선두로 이 함수를 태운 뒤 자기 BundleInfo 로 덮어쓴다 — 여기 값은
	// item_id 단독 호출(선점·재개)에서만 살아남는다.
	res.Bundle = &BundleInfo{
		Reason: "item_id 하나를 지정한 호출이라 이웃을 찾지 않았다",
		Scope: "이웃 후보를 아예 안 봤다 — 방사형 판정(judge.EligibleBundle)은 추천 경로에서만 돈다. " +
			"함께 집으려면 item_ids 에 선두부터 순서대로 줘라",
	}
	res.Overlaps = judge.OverlapsWithLive(item.Paths, live, in.SessionID, selfCC)
	res.Setup = SetupCommands(proj.Path, proj.DefaultBranch, item.ID)
	res.PathCheck = s.checkItemPaths(ctx, proj, item.Paths)
	// ★ 종료 선언 축 — 위 Bundle 주석이 이미 적어 둔 그 실패를 여기서도 반복하지
	// 않는다. closeDeclarations 는 후보 슬라이스를 받아 앵커(생성 시각 이후·동시각
	// 제외 — pick.go:805 부근의 그 함수 자신의 doc)를 건다. 그 규칙을 여기서
	// 다시 적으면 규칙이 두 벌이 되므로, 항목 하나짜리 후보 슬라이스를 만들어
	// **같은 함수를 재사용**한다(closeDeclarations 자체는 항목 수에 무관하게
	// 원장 전체를 한 번 읽고 여기서 거르므로, 후보가 하나뿐이어도 안전하다).
	closed, closeRead := s.closeDeclarations(ctx, proj.ID, []judge.Candidate{{Item: item}})
	res.CloseDeclared = closeDeclaredOf(closed, item.ID, closeRead)
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
		// 판단을 못 읽어도 **재개 자체를 실패시키지 않는다.** 이 갈래에 도달했다는 것은
		// 원장이 이미 "이 세션이 쥐고 있다"고 말했다는 뜻이다. 여기서 오류를 올리면
		// 묶음 경로의 구성원 루프가 그것을 "못 집었다(claim-failed)"로 받아 적고
		// (rejectionOf), 세션은 **자기가 쥔 항목을 안 쥔 줄 알게 된다** — 만료도
		// 세션 종료 반납도 없는 판이라 그 항목은 그대로 아무도 못 집는 상태가 된다.
		s.notesOrNote(ctx, proj.ID, item.ID, &res, d)
		s.st.LogEvent(ctx, "item.resume", proj.ID, in.SessionID, map[string]any{"item": item.ID})
		res.Derived = d.result(now)
		s.log.InfoContext(ctx, "선점 재개", "project", proj.ID, "session_id", in.SessionID, "item", item.ID)
		return res, nil
	}

	// 새 선점. 판정은 store.JudgeClaim 이 하고 여기서 흉내 내지 않는다 —
	// 흉내 내면 조회와 삽입 사이에 남이 잡는 창이 생긴다.
	var claim model.Claim
	// 포함 축이 버린 경로. Tx 안에서 세션의 워크트리를 읽어야 나오므로 여기 담아
	// Tx 뒤의 경고 로그가 읽는다(Beat 와 같은 모양).
	var outside []string
	err = s.st.Tx(ctx, func(t *store.Tx) error {
		// ★ 포함 축 — item.Paths 는 상류(AddItem·AddFollowup)의 좌표계 관문만 지나왔다.
		// 그 관문이 쓰는 judge.JudgePathCoordinate 는 자기 주석에 "포함 축은 여기 없다"고
		// 적어 놨고, POSIX 절대경로는 흠이 없어 통과한다. 그래서 `fd add --path
		// /tmp/남의레포/x.go` 가 선점하는 순간 claimed 발자국이 됐다.
		//
		// 4530e3c 는 문이 셋이라고 적었지만(Beat · api.NormalizeFootprints ·
		// legacy.PlanImport) **넷이다.** 이 자리가 그 네 번째이고, 넷 중 Beat 와 함께
		// 유일하게 살아 있는 문이다 — 나머지 둘은 호출자가 없거나 일회성 CLI 다.
		//
		// 규율은 Beat 와 같다: 거절하지 않고 버리되 원장에 남긴다. 여기서 거절하면
		// 경로 하나가 선점 전체를 멈추고, 그러면 큐가 항목 하나에 막힌다.
		sess, err := t.GetSession(in.SessionID)
		if err != nil {
			return err
		}
		inside := make([]string, 0, len(item.Paths))
		outside = outside[:0]
		for _, p := range item.Paths {
			rel, within := RelPathWithin(sess.Worktree, p)
			if rel == "" {
				continue
			}
			if !within {
				outside = append(outside, p)
				continue
			}
			// ★ 절대경로만 rel 로 갈아 끼운다. 상대경로는 **원본 그대로** 둔다.
			//
			// 이것이 Beat 와 이 문의 진짜 차이다. RelPathWithin 은 filepath.Clean 을
			// 거치므로 "pipeline/" 의 후행 슬래시가 사라지는데, item.Paths 에서 그
			// 슬래시는 **디렉토리 표기**이고 겹침 축이 그것을 읽는다
			// (judge.PathsOverlap — TestPickReportsOverlapWithoutFilteringIt 이
			// pair[0]=="pipeline/" 를 못박고 있다). Beat 가 받는 것은 훅이 준 파일
			// 절대경로라 정규화가 무해하지만, 여기 오는 것은 **사람이 선언한 경로**다.
			//
			// 포함 판정은 그대로 쓴다 — 상대경로는 RelPathWithin 계약상 언제나 within
			// 이므로(상대화할 것이 없으면 "안"이다) 이 분기가 판정을 우회하지 않는다.
			if !filepath.IsAbs(p) {
				rel = p
			}
			inside = append(inside, rel)
		}

		// 시도를 먼저 예약한다 — 롤백돼도 남는다(거절당한 선점도 원장의 자산이다).
		//
		// ★ "paths" 는 **선언된 전부**다. 이 칸의 의미를 "실제로 Touch 한 수"로 바꾸지
		// 않는다 — session.beat 의 count 가 두 번 의미를 바꿔 원장 질의가 세 정의를
		// 걸치게 된 전례가 바로 옆에 있다(session.go 의 그 주석). 대신 outside 를
		// 더해서 paths - outside 로 Touch 수를 복원하게 둔다.
		t.LogEvent("item.claim", proj.ID, in.SessionID, map[string]any{
			"item": item.ID, "paths": len(item.Paths), "overlaps": len(res.Overlaps),
			"outside": len(outside), "dropped_paths": clipDroppedPaths(outside),
		})
		c, err := t.ClaimItem(proj.ID, item.ID, in.SessionID)
		if err != nil {
			return err
		}
		claim = c
		// 항목이 선언한 경로를 이 세션의 발자국으로 남긴다(origin=claimed).
		// 착수 직후 구간은 브랜치 diff 가 정의상 비어 있어 이 축이 그 구간을 덮는다.
		for _, p := range inside {
			if err := t.Touch(in.SessionID, p, model.OriginClaimed, c.At); err != nil {
				return err
			}
		}
		return nil
	})
	if len(outside) > 0 {
		s.log.WarnContext(ctx, "항목이 선언한 경로 일부가 카드의 워크트리 밖이다 — 발자국으로 안 남긴다",
			"project", proj.ID, "session_id", clip(in.SessionID, 64), "item", clip(item.ID, 64),
			"dropped", len(outside), "first_path", clip(outside[0], 200))
	}
	if err != nil {
		s.logFail(ctx, "item.claim", proj.ID, in.SessionID, err, failAbout{Item: item.ID})
		s.log.ErrorContext(ctx, "선점 실패",
			"project", proj.ID, "session_id", clip(in.SessionID, 64), "item", clip(item.ID, 64),
			"error", err.Error())
		return PickResult{}, err
	}

	// ─────────────────────────────────────────────────────────────────────────
	// ★★ 여기부터는 **커밋 뒤**다. 이 아래에서 `return PickResult{}, err` 를 하면 안 된다.
	//
	// 왜: 위 Tx 가 성공했다는 것은 선점이 원장에 **영구히** 남았다는 뜻이다. 그 뒤의
	// 읽기가 실패했다고 오류를 올리면 무슨 일이 벌어지나 —
	//   · 단독 경로: 요청이 500 으로 죽는데 선점은 그대로 남는다. 세션은 못 집은 줄 안다.
	//   · 묶음 경로: 구성원 루프가 그 오류를 rejectionOf 로 받아 Claimed=false ·
	//     "claim-failed" 로 적는다 — **커밋된 선점이 실패로 보고된다.**
	// 그리고 그 항목은 거기서 끝이 아니다: schema.sql 에 만료가 없고, 세션이 닫혀도
	// 선점을 푸는 코드가 없고, store.JudgeClaim 은 점유자가 있으면 생존 검사 없이
	// 거절한다. 즉 **사람이 강제로 풀 때까지 모든 세션에게 영구히 막힌 항목**이 된다.
	// 쥔 세션조차 자기가 쥔 줄 모르니 반납할 생각도 못 한다.
	//
	// 그래서 이 아래의 실패는 전부 "집었다 · 다만 응답이 반쪽이다"로 접는다.
	// 무엇이 반쪽인지는 d.note 로 이름을 남겨 응답 본문에 실린다(renderFailures) —
	// 침묵으로 접으면 "판단 0건"과 "판단을 못 읽었다"가 같은 화면이 된다.
	//
	// 이 규율은 산문만으로는 안 지켜진다. TestPickExplicitHasNoFatalReturnAfterCommit
	// 이 이 표식 아래의 소스를 실제로 훑어서 강제한다.
	// ─────────────────────────────────────────────────────────────────────────

	// 선점 뒤 상태를 다시 읽는다 — ClaimItem 이 항목 상태를 claimed 로 바꾼다.
	if fresh, ferr := s.st.GetItem(ctx, proj.ID, item.ID); ferr == nil {
		item = fresh
		res.Item = &item
	} else {
		d.note("item-refresh:"+clip(item.ID, 64),
			"선점은 커밋됐는데 항목을 다시 못 읽었다 — 위에 찍힌 상태는 선점 **직전**의 값이다: "+
				clip(ferr.Error(), 300))
	}
	res.Mode, res.Claim = PickClaimed, &claim
	res.Reason = fmt.Sprintf("항목 %s 를 선점했다", item.ID)
	s.notesOrNote(ctx, proj.ID, item.ID, &res, d)
	res.Derived = d.result(now)
	s.log.InfoContext(ctx, "선점",
		"project", proj.ID, "session_id", in.SessionID, "item", item.ID, "overlaps", len(res.Overlaps))
	return res, nil
}

// pickBundle 은 세션이 지정한 묶음을 선점한다.
//
// 지키는 것 둘:
//
//  1. **선두는 원자다.** 못 집으면 아무것도 안 쓰고 거절한다 — 브랜치가 정의되지
//     않으므로 "묶음을 집었다"고 말할 수 없다(선두의 id 가 곧 브랜치 이름이다).
//  2. **구성원은 각각 별도 트랜잭션이다.** 하나를 남이 채 갔다는 이유로 이미 성립한
//     선두 선점을 되돌리면 세션이 아무것도 못 얻고, 동시 세션이 스물 넘는 환경에서
//     그 재시도는 잦다. 대신 **침묵하지 않는다** — 못 집은 사유를 그대로 싣는다
//     (BundleMember.Claimed 옆의 계약 참고).
//
// 선두·구성원 전부 pickExplicit 을 그대로 태운다 — 흉내 내면 조회와 삽입 사이에
// 남이 잡는 창이 생긴다(judge.JudgeClaim 이 그 창을 막는 유일한 자리다).
func (s *Service) pickBundle(ctx context.Context, proj model.Project, in PickInput,
	live []judge.LiveSession, selfCC string, d *derive, now time.Time) (PickResult, error) {

	ids := dedupeIDs(in.ItemIDs) // 순서 보존 중복 제거 — 둘째 사본이 재개 경로를 타 사유를 흐리지 않게
	lead, rest := ids[0], ids[1:]

	// ① 선두 — 기존 단독 경로를 그대로 탄다. 실패하면 여기서 즉시 반환한다:
	// 트랜잭션을 하나도 안 열었으니 되돌릴 것도 없다(원자성이 공짜로 나온다).
	res, err := s.pickExplicit(ctx, proj,
		PickInput{Project: in.Project, SessionID: in.SessionID, ItemID: lead}, live, selfCC, d, now)
	if err != nil {
		return PickResult{}, err
	}

	res.Scope = fmt.Sprintf("지정된 묶음 %d건(선두 %s)", len(ids), lead)
	res.Bundle = &BundleInfo{
		Reason: fmt.Sprintf("세션이 지정한 묶음이다 — 선두 %s 가 브랜치가 된다", lead),
		Scope:  "판정 없이 지정된 그대로 집었다",
	}

	// ② 구성원 — 되는 대로 집는다. pickExplicit 호출 하나하나가 자기 트랜잭션이라
	// 하나의 실패가 앞서 성립한 선두·다른 구성원의 선점에 영향을 못 준다.
	//
	// heldMembers 는 **이 세션이 쥐고 있는** 구성원 수, newMembers 는 그중 **이 호출이
	// 새로 쓴** 수다. 둘을 나누지 않으면 재개(이미 내 선점)가 "집었다"로 보고돼
	// 아무 쓰기도 없었던 호출이 원장에 없는 사건을 말하게 된다.
	heldMembers, newMembers := 0, 0
	allPaths := append([]string(nil), res.Item.Paths...)
	for _, id := range rest {
		m := BundleMember{Link: judge.Link{Item: id, Detail: "세션이 함께 지정했다"}}
		sub, serr := s.pickExplicit(ctx, proj,
			PickInput{Project: in.Project, SessionID: in.SessionID, ItemID: id}, live, selfCC, d, now)
		if serr != nil {
			m.Rejection = rejectionOf(id, serr)
			// pickExplicit 이 실패로 던진 PickResult 는 비어 있다(Item 도 포함해서) —
			// 자기가 이미 읽은 항목을 실패와 함께 버린다. 화면에 빈 줄(id="")로 뜨지
			// 않도록 한 번 더 읽는다. 이 조회조차 실패하면 id 가 애초에 없는 것이므로
			// State 는 정직하게 비우고 ID 만 남긴다(BundleMember.Item 의 계약 참고).
			var cands []judge.Candidate
			if it, ierr := s.st.GetItem(ctx, proj.ID, id); ierr == nil {
				m.Item = it
				cands = []judge.Candidate{{Item: it}}
			} else {
				m.Item.ID = id
			}
			// ★ 종료 선언 축은 **못 집은 구성원에게도** 낸다. 렌더가 이 줄을 사유 줄
			// **위**에 일부러 올려 뒀는데(renderBundle 의 그 주석 — 못 집은 구성원이야말로
			// 다음 세션이 다시 집으러 오는 자리라서), 이 자리가 안 채우고 있어서 그 줄이
			// 항상 "이 응답은 이 축을 안 읽었다"였다. 신선한 온라인 응답에 거짓 원인을
			// 붙이는 그 실패를 이 축이 이미 두 번 겪었다(Bundle · CloseDeclared).
			//
			// 재조회조차 실패한 갈래(큐에 없는 id)는 후보가 비어 "읽었고 0건"이 나간다.
			// 정확히는 **앵커를 걸 대상이 없다**는 뜻이다 — 항목이 없으면 CreatedAt 이
			// 없고, 이 축의 앵커 규칙(선언이 항목 생성 뒤여야 센다)을 매길 수가 없다.
			// 그래도 nil 보다 이쪽을 고른다: nil 은 화면에서 원인 셋(구서버 · 옛 캐시 ·
			// 이번 조회 실패)을 대는데 신선한 온라인 응답에서 셋 다 거짓이고, 0건의
			// 오차는 "왜 0인가"에 그친다. 없는 항목에 가짜 CreatedAt 을 주는 길은 더
			// 나쁘다 — zero 시각이면 앵커가 무조건 통과해 남의 선언을 이 id 에 붙인다.
			// 원장 자체를 못 읽으면 closeRead=false 라 nil 이 그대로 남는다.
			closed, closeRead := s.closeDeclarations(ctx, proj.ID, cands)
			m.CloseDeclared = closeDeclaredOf(closed, id, closeRead)
			s.log.WarnContext(ctx, "묶음 구성원 선점 실패 — 나머지를 진행한다",
				"project", proj.ID, "session_id", in.SessionID, "item", clip(id, 64),
				"error", serr.Error())
			res.Bundle.Members = append(res.Bundle.Members, m)
			continue
		}
		// 집었으면 판단 전문을 함께 낸다 — 추천(미집음)과 다른 점이 이것이다.
		// sub.Notes 는 pickExplicit 이 linkedJudgments 로 이미 채워 왔다.
		//
		// ★ Claimed=true 는 재개(PickResumed)에도 준다 — 원장이 "이 세션이 쥐고 있다"고
		// 말하는 상태가 맞기 때문이다. 대신 **새로 쓴 것**은 따로 센다: 아래 사유 문장이
		// "집었다"는 동사를 쓰려면 실제로 쓰기가 일어났어야 한다.
		m.Item, m.Claimed = *sub.Item, true
		m.Notes, m.PathCheck = sub.Notes, sub.PathCheck
		// ★ 형제 축(PathCheck)과 같은 모양이다 — sub 는 pickExplicit 이 이미 이
		// 구성원 자기 것으로 채워 왔다. 여기서 안 나르면 PathCheck 은 실리는데
		// 이 축만 조용히 사라진다.
		m.CloseDeclared = sub.CloseDeclared
		heldMembers++
		if sub.Mode == PickClaimed {
			newMembers++
		}
		allPaths = append(allPaths, sub.Item.Paths...)
		res.Bundle.Members = append(res.Bundle.Members, m)
	}

	// ③ 겹침은 묶음 전체 경로의 합집합으로 다시 본다 — "남과 부딪히는가"는 항목
	// 단위가 아니라 묶음 단위 질문이다(이 세션이 그 경로들을 한 브랜치에서 함께
	// 건드리기 때문이다). 선두만 보고 넘기면 뒤늦게 집은 구성원의 겹침이 안 보인다.
	res.Overlaps = judge.OverlapsWithLive(allPaths, live, in.SessionID, selfCC)

	// ★ 파생 신선도도 묶음 전체를 반영해 다시 낸다. 선두 단독 호출 시점의 스냅샷을
	// 그대로 두면 구성원 처리 중에 d 에 쌓인 실패(예: 안전하지 않은 id 라 워크트리
	// 명령을 못 낸 구성원)가 응답에서 사라진다 — d 는 이 함수 전체가 공유하는
	// 누산기라 이 재계산은 부작용이 없다(d.result 는 순수 조회다). 리뷰 라운드 1
	// finding 2 가 이 한 줄이 진짜로 관측 가능한 사실을 바꾼다는 것을 실측으로
	// 반증했다 — TestPickBundleDerivedReflectsMemberSetupFailure 가 그 반증을 잠근다.
	res.Derived = d.result(now)

	// ④ 사유는 **원장이 실제로 담고 있는 것**만 말한다.
	//
	// ★ 예전에는 이 문장을 무조건 "선두 X 를 선점했다. 묶음 N건 중 M건을 집었다"로
	// 찍었다. 그런데 선두가 이미 이 세션의 선점이면 pickExplicit 은 재개 경로를 타
	// **아무것도 쓰지 않는다**(선점 시각을 덮으면 "언제부터 쥐고 있나"가 사라지고
	// 그 값이 회수 판단 다섯 축 중 하나라서 일부러 그렇게 돼 있다). 그 상태에서
	// "선점했다 · M건을 집었다"를 내면 응답이 **일어나지 않은 쓰기를 보고한다** —
	// 동시 세션이 서른인 판에서 그 한 문장이 "이번 호출이 남의 것을 빼앗아 왔다"로
	// 읽힐 수 있다. 원장은 옳았고 문장만 거짓말이었다.
	//
	// 그래서 셋을 가른다: 전부 새로 집었다 · 하나도 새로 안 집었다(순수 재출력) ·
	// 섞였다. 수(쥔 건수)는 참이면 그대로 살린다 — 동사만 바꾼다.
	held := 1 + heldMembers // 선두는 위에서 쥔 것이 확정됐다(집었거나 이미 내 것이었거나)
	newly := newMembers
	leadPart := fmt.Sprintf("선두 %s 를 선점했다", lead)
	if res.Mode == PickClaimed {
		newly++
	} else {
		leadPart = fmt.Sprintf("선두 %s 는 이미 이 세션의 선점이다 — 새로 쓴 것 없이 다시 낸다", lead)
	}
	switch {
	case newly == held:
		res.Reason = fmt.Sprintf("%s. 묶음 %d건 중 %d건을 집었다", leadPart, len(ids), held)
	case newly == 0:
		res.Reason = fmt.Sprintf("%s. 묶음 %d건 중 %d건을 이 세션이 이미 쥐고 있다 — "+
			"이 호출은 새로 집은 것이 없다(쥔 상태의 재출력이다)", leadPart, len(ids), held)
	default:
		res.Reason = fmt.Sprintf("%s. 묶음 %d건 중 %d건을 이 세션이 쥐고 있고, "+
			"그중 %d건을 이번에 새로 집었다", leadPart, len(ids), held, newly)
	}
	return res, nil
}

// AccountedIDs 는 이 응답이 **이름으로 설명한** 항목 id 전부다. 순수 함수다.
//
// "설명했다"는 집었다는 뜻이 아니다 — 집었든, 못 집고 사유를 실었든, 이 응답이 그
// id 를 한 번은 언급했다는 뜻이다. 호출자(cmd/fd·mcpsrv)는 이 집합을 자기가 보낸
// item_ids 와 대조해서 **응답이 통째로 빠뜨린 id** 를 찾는다(judge.UnaccountedIDs).
//
// ★ 이게 없으면 무엇이 깨지나: item_ids 를 모르는 구서버가 선두만 집고 200 을 내면
// 클라이언트는 그것을 정상 응답으로 읽고 종료코드 0 을 낸다. 나머지 id 는 아무도
// 안 쥔 채 이름조차 안 불린다 — 세션은 쥐지 않은 것을 쥐었다고 믿고 일을 시작한다.
//
// 세 자리를 다 읽는다(Item.ID · Rejection.Item · Link.Item). BundleMember 계약상
// 조회조차 실패한 구성원은 Item.ID 만 채워지고, 반대로 다른 갈래에서는 Rejection
// 쪽에만 id 가 사는 판이 있을 수 있다 — 한 자리만 보면 그 갈래가 "설명 안 됨"으로
// 오분류돼 있지도 않은 스큐를 신고하게 된다.
func (r PickResult) AccountedIDs() []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if r.Item != nil {
		add(r.Item.ID)
	}
	add(r.Branch)
	if r.Bundle != nil {
		for _, m := range r.Bundle.Members {
			add(m.Item.ID)
			add(m.Link.Item)
			if m.Rejection != nil {
				add(m.Rejection.Item)
			}
		}
	}
	return out
}

// dedupeIDs 는 순서를 지키며 공백과 중복을 걷어낸다. 순수 함수다.
//
// 같은 id 를 두 번 넣으면 둘째 사본이 pickExplicit 의 재개 경로("이미 내 선점")를
// 타 사유가 흐려진다 — 실제로 못 집은 게 아닌데 구성원 목록에 재개 사유가 섞인다.
func dedupeIDs(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// 묶음 구성원 선점 실패의 탈락 사유 코드 — rejectionOf 가 낸다.
//
// ★ judge.Reject* 표(internal/judge/eligible.go)와 **다른 자리**에 둔다. 그 표는
// 추천 시점의 적격 판정(judge.EligibleBundle)이 pick_eval.rejected 에 남기는 사유고,
// 이건 선점 **시도 자체**가 store 축에서 실패한 결과다 — 질문이 다르다(적격이냐
// vs 지금 잡혔냐). 같은 이름 공간에 섞으면 두 생애주기가 뭉개진다.
//
// 리뷰 라운드 1 finding 3: 문자열 리터럴로 두면 rejection.reason_code 로 분기하는
// 소비자가 참조할 심볼이 없고, 오타를 컴파일러가 못 잡는다.
const (
	// RejectClaimNotFound 는 지정한 id 가 큐에 없다는 뜻이다(오타이거나 지워졌다).
	RejectClaimNotFound = "not-found"
	// RejectClaimFailed 는 store 오류가 알려진 두 타입(ClaimHeldError·ClaimRefusedError)
	// 어느 쪽도 아닐 때의 안전망이다. Detail 에 원문이 항상 남으므로 코드가
	// 뭉툭해도 왜인지는 여전히 읽을 수 있다.
	RejectClaimFailed = "claim-failed"
)

// rejectionOf 는 구성원 선점 실패를 탈락 사유 한 줄로 바꾼다. 순수 함수다.
//
// ★ 실물 오류 타입은 store.ClaimHeldError(남이 쥐고 있다)·store.ClaimRefusedError
// (없음·끝남·폐기 — 점유자 축과 무관한 사유)다. store.ConflictError 는 다른
// 도메인(스키마 제약 위반)의 타입이라 여기 안 온다 — GetItem·ClaimItem 어느 쪽도
// 그 타입을 내지 않는다.
//
// 사유 코드를 사람 말로 풀지 않는다 — 기계가 세는 값은 그대로 보인다. Detail 에는
// 항상 원문을 남긴다 — 매칭되는 타입이 없어 code 가 fallback 이어도 Detail 만으로
// 왜인지는 알 수 있게 한다.
func rejectionOf(id string, err error) *model.Rejection {
	code := RejectClaimFailed
	var held *store.ClaimHeldError
	var refused *store.ClaimRefusedError
	switch {
	case errors.Is(err, store.ErrNotFound):
		code = RejectClaimNotFound
	case errors.As(err, &held):
		code = judge.RejectClaimed // 남이 이미 선점했다 — 적격 판정 축과 같은 코드를 쓴다
	case errors.As(err, &refused):
		code = judge.RejectClosed // 항목이 이미 끝났거나 폐기됐다
	}
	return &model.Rejection{Item: id, Reason: code, Detail: clip(err.Error(), 200)}
}

// siblingIndex 는 후보들에 걸린 판단 링크를 모아 judge 가 쓸 색인으로 만든다.
//
// 실패해도 pick 을 실패시키지 않는다 — 형제 축 하나 때문에 추천을 잃는 것이 더 나쁘고,
// 빈 색인이면 나머지 두 축이 그대로 돈다. 못 읽은 사실은 derive 에 안 넣는다
// (derive 에 넣으면 FreshnessOf 가 git 축을 낡음으로 접는다). 대신 로그에 남기고,
// **두 번째 반환값**으로 호출부에 그대로 넘긴다 — 호출부가 그걸 Bundle.Scope
// 문장에 적어야 "구성원 0건"(형제가 실제로 없다)과 "형제 축을 아예 못 읽었다"가
// 같은 값(빈 색인)으로 접히지 않는다. 이건 error 가 아니다: pick 을 실패시키지
// 않는다는 계약은 그대로다 — 이 값을 무시해도 판정 자체는 여전히 옳게 돈다.
func (s *Service) siblingIndex(ctx context.Context, project string, cands []judge.Candidate) (judge.SiblingIndex, bool) {
	ids := make([]string, 0, len(cands))
	for _, c := range cands {
		ids = append(ids, c.Item.ID)
	}
	links, err := s.st.JudgmentLinksForItems(ctx, project, ids)
	if err != nil {
		s.log.WarnContext(ctx, "형제 색인 조회 실패 — 형제 축 없이 판정한다",
			"project", clip(project, 64), "count", len(ids), "error", err.Error())
		return judge.SiblingIndex{}, false
	}
	return judge.SiblingIndex(links), true
}

// closeDeclarations 는 후보들에 걸린 **롤백된 종료 선언**을 항목별로 모은다.
//
// siblingIndex 와 같은 모양이다. 실패해도 pick 을 실패시키지 않는다 — 이 축 하나
// 때문에 추천을 통째로 잃는 것이 더 나쁘고, 축이 없어도 나머지 판정은 그대로 돈다.
// 못 읽은 사실은 **derive 에 안 넣는다**. 바로 위 siblingIndex 가 같은 함수에서 같은
// 판단을 이미 내렸다 — "못 읽은 사실은 derive 에 안 넣는다(derive 에 넣으면
// FreshnessOf 가 git 축을 낡음으로 접는다). 대신 로그에 남기고, **두 번째 반환값**으로
// 호출부에 그대로 넘긴다." pick 은 git 읽기가 실제로 도는 경로라 예외가 안 선다.
// 호출부는 그 bool 을 EligibleInput.CloseDeclarationsRead 와 Bundle.Scope 문장에
// 태워야 한다 — 안 그러면 "선언 0건"(진짜로 없다)과 "이 축을 아예 못 읽었다"가
// 같은 값(빈 맵)으로 접힌다.
//
// ★ 원장이 낸 수를 그대로 안 쓴다. 관문 둘을 **여기서** 건다(store 는 원장만 읽는다 —
// SQL 조인을 쓰면 json_extract 를 조인 조건에 넣어야 하는데 그 선례가 저장소에 0건이다):
//
//  1. **시간 앵커.** 항목의 CreatedAt **이후**의 선언만 남긴다. item 의 PK 가
//     (project, id) 라 지워졌다 다시 만들어진 id, 프로젝트를 옮겨 비워진 뒤 재사용된
//     id 가 옛 화신의 선언을 물려받는다. 두 값 다 이미 손에 있어 추가 조회가 없다.
//     ★ 앵커는 **접힌 값 단위**로 건다. store 가 항목별로 접어 주므로 여기 오는 시각은
//     마지막 선언(Last) 하나뿐이다 — 그것조차 생성 이전이면 그 접힘은 통째로 옛 화신의
//     것이라 버린다. 생성 시각을 걸치는 접힘은 남는데, 그 상태는 같은 id 가 지워졌다
//     다시 만들어진 **뒤에 또** 롤백된 finish 가 나야 성립한다(실측 0건). 그 갈래에서
//     수가 실제보다 커지는 것은 아는 한계다.
//     같은 시각은 **안 센다** — 항목이 있어야 닫을 수 있으니 동시각은 이 화신의 선언일
//     수 없고, 애매한 쪽은 하한으로 접는 것이 이 축의 규율이다.
//
//  2. **좌표 어긋남은 표류와 가른다.** 후보 목록에 없는 id 의 선언은 **버린다**.
//     실측 3건이 그 모양이다(context-platform 에서 친 finish 인데 항목은
//     kweiza-cc-plugins 에 있다 — fd-session-row-fanout·fd-ci-timing-baseline·
//     fd-prescribe-unclaimed-fires-after-finish). 그것은 좌표 오류지 표류가 아니다.
//
// ★ 이 수는 **하한이다.** LogEvent 가 쓰기 실패를 WARN 으로만 삼키고(store/event.go 의
// LogEvent), BeginTx 가 실패한 트랜잭션은 이벤트를 예약조차 안 하므로, 원장에 안 써진
// 마무리가 있을 수 있다. 문구가 그렇게 말해야 한다.
// (예전에는 "flushDeferred 가 트랜잭션의 ctx 를 그대로 쓴다"가 첫 사유였다. 그 갈래는
// 닫혔다 — store.flushCtx 가 취소를 떼고 예산을 다시 건다.)
func (s *Service) closeDeclarations(ctx context.Context, project string,
	cands []judge.Candidate) (map[string]model.CloseDeclaration, bool) {

	all, err := s.st.CloseDeclarationsByItem(ctx, project)
	if err != nil {
		s.log.WarnContext(ctx, "종료 선언 조회 실패 — 이 축 없이 판정한다",
			"project", clip(project, 64), "count", len(cands), "error", err.Error())
		return nil, false
	}
	out := make(map[string]model.CloseDeclaration, len(cands))
	for _, c := range cands {
		d, ok := all[c.Item.ID]
		if !ok || d.Count() == 0 {
			continue
		}
		if !d.Last.After(c.Item.CreatedAt) {
			continue
		}
		out[c.Item.ID] = d
	}
	return out, true
}

// closeDeclaredOf 는 항목 하나의 종료 선언을 응답에 실을 포인터로 바꾼다. 순수 함수다.
//
// ★ **포인터의 뜻은 PathCheck 의 규약 그대로다**(PickResult.PathCheck 의 주석):
// nil 은 "이 응답은 그 축을 안 읽었다"이고, 그 상태가 실제로 난다 — 구서버 + 신
// 클라이언트, 그리고 이 필드가 생기기 전에 굳은 오프라인 캐시가 그것을 만든다.
// 그래서 **읽었으면 선언이 0건이어도 non-nil 을 싣는다**(zero 값 = 읽었고 0건).
// 값 타입이나 "있을 때만 채움"으로 두면 그 두 상태가 한 값으로 접히고, 그러면
// 원장을 못 읽은 응답이 "이 항목은 깨끗하다"를 관측 없이 단정한다 —
// checkItemPaths 가 "절대 nil 을 돌려주지 않는다"로 선 것과 같은 자리다.
//
// read 가 false 면 맵을 아예 안 본다. 그때 못 읽었다는 사실은 Bundle.Scope 가 말한다
// (bundleScope 의 closeRead 인자) — 항목마다 같은 고백을 반복하지 않는다.
func closeDeclaredOf(m map[string]model.CloseDeclaration, id string, read bool) *model.CloseDeclaration {
	if !read {
		return nil
	}
	d := m[id] // 없으면 zero — "읽었고 0건"이다. 맵 원소의 주소는 못 잡으므로 복사본을 낸다
	return &d
}

// bundleScope 는 Bundle.Scope 문장을 만든다. 순수 함수다 — 시험이 DB 없이 문구를 고정한다.
//
// ★ total 은 **적격 여부와 무관하게 후보 전부를 센 수**(len(cands))다. EligibleBundle
// 내부에서 실제로 이웃 후보가 된 집합(fit·absorbable)의 크기는 Bundle 구조체가 안
// 돌려준다. 그 수를 "적격 항목" 이라고 잘못 부르면(실측: 후보 5·적격 3인데
// "이웃 후보는 적격 항목 5건이다") 관측한 적 없는 사실을 단정하는 것이 된다.
// 그래서 total 은 "관찰한 후보"라고만 부른다 — len(cands) 에 대해 참인 문장이다.
//
// sibRead 가 false 면 형제 축을 못 읽었다는 사실을 문장에 남긴다. 키 부재를 값으로
// 접지 않는다는 전역 규율이 이 한 줄에서도 지켜져야 한다 — 안 남기면 이 묶음이
// "형제가 진짜로 없다"인지 "형제 축을 아예 못 봤다"인지 응답만으로 못 가른다.
//
// closeRead 도 같은 규율이고 **따로 적는다.** 하나로 뭉치면 "형제는 읽었고 종료 선언만
// 못 읽었다"가 화면에서 "둘 다 못 읽었다"와 같아진다. 이쪽은 항목마다 낼 수도 있지만
// 그러면 같은 고백이 후보 수만큼 반복되므로, 축의 상태는 축의 자리(범위 문장)에서
// 한 번만 말한다 — 항목별 값은 PickResult.CloseDeclared 가 나른다.
func bundleScope(total int, sibRead, closeRead bool) string {
	sc := fmt.Sprintf("관찰한 후보는 전체 %d건이다(적격 여부와 무관하게 센 수다). "+
		"그 중 선두와 형제·선행 축으로 **직접** 이어진 것만 묶었다(전이 없음)", total)
	if !sibRead {
		sc += " · 형제 축(같은 판단에 함께 걸린 형제)은 이번에 못 읽었다 — " +
			"이 묶음은 선행·경로 축만 보고 나온 결과다"
	}
	if !closeRead {
		sc += " · 이 후보들이 이미 닫히려다 롤백된 적이 있는지(원장의 item.finish)는 " +
			"이번에 못 읽었다 — 이 순위는 그 축 없이 나온 결과다"
	}
	return sc
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

	sib, sibRead := s.siblingIndex(ctx, proj.ID, cands)
	closed, closeRead := s.closeDeclarations(ctx, proj.ID, cands)
	best, rejected := judge.EligibleBundle(judge.EligibleInput{
		Self: in.SessionID, SelfCC: selfCC, Candidates: cands, Live: live, Facts: facts, HeldResources: held,
		// ★ 맵과 bool 을 **함께** 싣는다. 빈 맵 하나로 접으면 "선언 0건"과 "이 축을 아예
		// 못 읽었다"가 judge 안에서 같은 값이 되고, Go 의 nil 맵 조회는 zero 를 내므로
		// 순수 함수 시험이 두 상태를 가를 관측점을 하나도 못 갖는다. 같은 구조체의
		// HeldResources 가 "비어 있으면 아무도 안 쥠"이라는 정반대 계약이라 nil 을
		// "안 읽음"으로 재활용할 수도 없다.
		CloseDeclarations:     closed,
		CloseDeclarationsRead: closeRead,
		// Now 는 기아 축(judge.StarvationAge)에만 쓴다. 주입된 시계를 그대로 넘긴다 —
		// 여기서 time.Now() 를 부르면 시험이 가짜 시계를 밀어도 이 축만 실시계로
		// 판정한다(fd-lane-timestamps-ignore-injected-clock 이 고발한 그 모양이다).
		Now: now,
	}, sib)

	res := PickResult{Rejected: rejected, Scope: scope, QueueOpen: &openCount}
	eval := model.PickEval{Project: proj.ID, SessionID: in.SessionID, Rejected: rejected}
	if best != nil {
		eval.Picked = best.Lead.Item.ID
		for _, m := range best.Members {
			eval.PickedWith = append(eval.PickedWith, m.Item.ID)
		}
	}
	// ★ 적격 0건도 기록이다. 사유가 없으면 큐는 블랙박스가 되고,
	//   블랙박스는 두 번째 세션부터 무시된다.
	if err := s.st.RecordPickEval(ctx, eval); err != nil {
		return PickResult{}, err
	}
	// picked_count 는 선두를 포함한 묶음 크기다. best 가 nil 이면 0 이다 —
	// 여기서 1을 더하면 "추천 없음"인데도 1건 집은 것처럼 로그가 거짓말한다.
	pickedCount := 0
	if best != nil {
		pickedCount = len(eval.PickedWith) + 1
	}
	s.st.LogEvent(ctx, "item.pick", proj.ID, in.SessionID, map[string]any{
		"picked": eval.Picked, "picked_count": pickedCount,
		"count": len(cands), "skipped": len(rejected),
	})

	if best == nil {
		res.Mode = PickNone
		res.Reason = fmt.Sprintf("적격 항목이 0건이다(후보 %d건, 탈락 사유 %d줄). %s",
			len(cands), len(rejected), scope)
		res.Derived = d.result(now)
		s.log.InfoContext(ctx, "추천 없음",
			"project", proj.ID, "session_id", in.SessionID, "count", len(cands), "skipped", len(rejected))
		return res, nil
	}

	item := best.Lead.Item
	res.Mode, res.Item, res.Branch = PickRecommended, &item, item.ID
	res.Overlaps = best.Lead.Overlaps
	res.Setup = SetupCommands(proj.Path, proj.DefaultBranch, item.ID)
	res.PathCheck = s.checkItemPaths(ctx, proj, item.Paths)
	// ★ 선두에도 싣는다. renderBundle 은 Members(선두 제외)만 받아 선두를 모르므로,
	// 구성원 자리에만 심으면 이 사고를 낳은 그 항목에 대해 응답이 통째로 침묵한다.
	res.CloseDeclared = closeDeclaredOf(closed, item.ID, closeRead)
	if res.Setup == nil {
		d.note("setup:"+clip(item.ID, 64),
			"항목 id 가 브랜치·디렉토리 이름으로 안전하지 않아 워크트리 준비 명령을 만들지 않았다")
	}
	// ★ 묶음은 여기서 조립한다. 구성원의 판단 전문(Notes)은 **안 싣는다** — 추천은
	// 아직 선점이 아니라서, 후보마다 전문을 실으면 컨텍스트만 태운다(설계 §6).
	// PathCheck 는 구성원별로 따로 본다 — 합치면 `fd move <id>` 처방이 엉뚱한
	// id 를 가리키게 된다(경로 겹침·부재는 항목 단위 사실이다).
	res.Bundle = &BundleInfo{
		Reason: best.Reason,
		Scope:  bundleScope(len(cands), sibRead, closeRead),
	}
	for i, m := range best.Members {
		res.Bundle.Members = append(res.Bundle.Members, BundleMember{
			Item: m.Item, Link: best.Links[i],
			PathCheck: s.checkItemPaths(ctx, proj, m.Item.Paths),
			// ★ 종료 선언도 구성원마다 **자기 것**을 싣는다. 합치거나 선두 것을 빌려주면
			// 화면이 엉뚱한 항목을 "이미 닫히려 했다"고 지목한다 — PathCheck 을 항목
			// 단위로 가른 것과 같은 이유다(둘 다 항목 단위 사실이다).
			CloseDeclared: closeDeclaredOf(closed, m.Item.ID, closeRead),
			// Notes 는 안 싣는다 — 추천은 아직 안 집은 것이라
			// 후보마다 전문을 실으면 컨텍스트를 태운다(설계 §6).
		})
	}
	// ★ 실제로 통하는 인자만 처방한다(태스크 7 리뷰 라운드 1 finding 1 이 남긴 규율).
	// mcpsrv 의 pick 도구는 additionalProperties:false·DisallowUnknownFields 로
	// 모르는 필드를 거절하므로, 스키마에 없는 인자를 처방하면 이 응답의 유일한
	// 실행 가능 줄이 "json: unknown field" 로 죽는다.
	//
	// 태스크 9 가 item_ids 를 실제로 스키마에 더했다 — 그래서 구성원이 있는 묶음은
	// 이제 item_ids 에 [선두, 구성원...] 순서대로를 처방한다. 구성원이 없는
	// (묶음 크기 1인) 추천은 **여전히 item_id 를 처방한다** — 그 모양은 항상
	// item_id 로 통하는 단독 선점·재개 경로 그대로이고, 배열에 원소 하나만
	// 담아 item_ids 를 쓰라고 시키면 더 짧고 이미 검증된 경로를 두고 에둘러
	// 가라고 하는 것이라 정직하지 않다.
	if len(best.Members) > 0 {
		ids := make([]string, 0, len(best.Members)+1)
		ids = append(ids, item.ID)
		for _, m := range best.Members {
			ids = append(ids, m.Item.ID)
		}
		res.Reason = fmt.Sprintf("%s · 후보 %d건 중 1순위다. "+
			"아직 선점하지 않았다 — 집으려면 item_ids 에 [%s] 를 선두부터 순서대로 주고 다시 불러라",
			best.Reason, len(cands), strings.Join(ids, ", "))
	} else {
		res.Reason = fmt.Sprintf("%s · 후보 %d건 중 1순위다. "+
			"아직 선점하지 않았다 — 집으려면 item_id 에 %s 를 주고 다시 불러라",
			best.Reason, len(cands), item.ID)
	}
	// ★ 여기서 오류를 올리면 **이미 만들어 원장에 적기까지 한 결과를 통째로 버린다.**
	// 이 줄에 오기까지 후보 수집·판정·묶음 조립이 끝났고 RecordPickEval 이 이미
	// "이 세션에 X 를 추천했다"를 썼다. 거기서 500 을 내면 원장과 응답이 갈라진다 —
	// 다음 사람이 pick_eval 을 읽고 "추천이 나갔는데 왜 아무도 안 집었나"를 묻는데
	// 그 질문에 답할 근거가 어디에도 없다.
	//
	// 그리고 같은 함수가 **같은 테이블을 읽는 다른 조회**(siblingIndex)에 대해서는 이미
	// 부드럽게 실패하며 그 사실을 Bundle.Scope 에 적는다. 한 함수 안에 정반대의 실패
	// 정책 둘이 있었다. notesOrNote 는 그 비대칭을 없애는 자리이고, 못 읽은 사실은
	// 아래 d.result 가 파생 실패 축으로 그대로 나른다(침묵으로 접지 않는다).
	s.notesOrNote(ctx, proj.ID, item.ID, &res, d)
	res.Derived = d.result(now)
	s.log.InfoContext(ctx, "추천",
		"project", proj.ID, "session_id", in.SessionID, "item", item.ID,
		"count", len(cands), "skipped", len(rejected), "overlaps", len(res.Overlaps),
		"bundle", len(best.Members))
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

// notesOrNote 는 연결된 판단을 싣거나, **못 실은 사실을 이름으로 남긴다.**
//
// ★ 왜 오류를 안 올리나. 이 함수를 부르는 세 자리는 전부 **원장이 이미 무언가를 말한
// 뒤**다 — 선점 두 자리(재개 · 선점 커밋 직후)는 "이 세션이 이 항목을 쥐고 있다"를,
// 추천 한 자리(pickRecommend 끝)는 RecordPickEval 이 쓴 "이 세션에 X 를 추천했다"를.
// 어느 쪽이든 여기서 오류를 올리면 **원장과 응답이 갈라진다.**
//
// 선점 쪽은 값이 더 크다. 거기서 판단 조회 실패로
// 오류를 올리면 묶음 구성원 루프가 그것을 rejectionOf 로 받아 **커밋된 선점을
// Claimed=false·claim-failed 로 보고**하고, 단독 경로는 요청이 통째로 500 이 된다.
// 어느 쪽이든 세션은 자기가 쥔 항목을 안 쥔 줄 알게 되는데, 이 판에는 선점 만료도
// 세션 종료 반납도 없어서 그 항목은 사람이 손대기 전까지 아무도 못 집는다.
// 판단 전문 하나를 잃는 것과 큐 항목 하나를 영구히 잠그는 것은 값이 다르다.
//
// 침묵으로 접지도 않는다. 그냥 nil 을 두면 "이 항목에 걸린 판단이 없다"와
// "판단을 못 읽었다"가 같은 화면이 되고, 두 번째 경우 세션은 앞선 판단이 없다고 믿고
// 이미 기각된 길을 다시 간다 — 이 도구가 존재하는 이유가 정확히 그것을 막는 것이다.
func (s *Service) notesOrNote(ctx context.Context, project, itemID string, res *PickResult, d *derive) {
	notes, err := s.linkedJudgments(ctx, project, itemID)
	if err != nil {
		s.log.WarnContext(ctx, "연결된 판단 조회 실패 — 선점은 그대로 두고 사실만 남긴다",
			"project", clip(project, 64), "item", clip(itemID, 64), "error", err.Error())
		d.note("notes:"+clip(itemID, 64),
			"이 항목의 선점은 원장에 남았는데 연결된 판단을 못 읽었다 — "+
				"이 응답의 '연결된 판단 0건' 은 **없다는 뜻이 아니라 못 읽었다는 뜻이다**: "+
				clip(err.Error(), 300))
		return
	}
	res.Notes = notes
}

// linkedJudgments 는 항목 하나에 연결된 판단 전문이다.
//
// 앞 판은 저장 계층에 "링크 대상으로 찾기" 조회가 없어 **종류 9개를 훑어 링크로 걸렀다**.
// 항목 하나에 질의 9회였고, 묶음이 들어오면서 N×9 가 됐다.
// 접근자(store.JudgmentsForItem)를 만들어 항목당 1회로 줄인다.
func (s *Service) linkedJudgments(ctx context.Context, project, itemID string) ([]model.Judgment, error) {
	return s.st.JudgmentsForItem(ctx, project, itemID)
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
		s.logFail(ctx, "item.add", in.Project, in.SessionID, err, failAbout{Item: in.ID})
		s.log.ErrorContext(ctx, "항목 등록 실패",
			"project", clip(in.Project, 64), "item", clip(in.ID, 64), "error", err.Error())
		return model.Item{}, err
	}
	// ★ **못 읽어도 등록 확인을 낸다**(DESIGN §5「쓰기 뒤 조회가 실패하면」). 항목은 위
	// 트랜잭션에서 이미 큐에 들어갔다 — 여기서 오류를 올리면 세션은 "못 만들었다"고 믿는데
	// 큐에는 있고, 재시도하면 store 가 중복으로 거절한다. 만들지도 다시 만들지도 못하는
	// 상태가 된다.
	//
	// ★ 채널이 A(로그만)인 이유: 이 함수의 반환은 model.Item 하나라 못 읽음을 나를 필드도
	// derive 도 없다 — 고른 것이 아니라 타입이 강제한다. 다행히 잃는 것이 거의 없다:
	// 화면(RenderAdd)이 쓰는 값은 전부 입력으로 아는 것이고(id·프로젝트·상태·제목·경로·선행),
	// 못 읽은 것은 서버가 채운 CreatedAt 뿐이다. 그 축이 화면에 필요해지는 날 이 반환을
	// 결과 타입으로 바꾸고 C 로 옮겨라.
	saved, err := s.st.GetItem(ctx, in.Project, in.ID)
	if err != nil {
		s.log.WarnContext(ctx, "항목 등록 뒤 되읽기 실패 — 등록은 커밋됐다",
			"project", clip(in.Project, 64), "item", clip(in.ID, 64), "error", err.Error())
		saved = it
	}
	s.log.InfoContext(ctx, "항목 등록",
		"project", in.Project, "session_id", in.SessionID, "item", it.ID, "count", len(it.After))
	return saved, nil
}
