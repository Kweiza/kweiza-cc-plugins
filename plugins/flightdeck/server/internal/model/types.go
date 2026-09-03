// Package model 은 flightdeck 의 도메인 타입을 담는다.
//
// 이 패키지는 다른 어떤 내부 패키지도 import 하지 않는다 — store·gitreader·api·mcpsrv 가
// 전부 여기에만 의존하므로, 여기가 순환을 만들면 전부가 엉킨다.
//
// 열거값은 상수로 둔다. 스키마의 CHECK 가 같은 값을 강제하므로 둘이 어긋나면 삽입이 실패한다 —
// 즉 이 파일과 schema.sql 은 **서로를 검사한다**. 한쪽만 고치면 시험이 빨간불을 낸다.
package model

import (
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// 열거값 — schema.sql 의 CHECK 와 문자열이 정확히 일치해야 한다
// ─────────────────────────────────────────────────────────────────────────────

// SessionState 는 세션의 상태다.
//
// active 는 파생이고(최근 신호가 있으면 active), paused·blocked 만 사람이 쓴다 —
// "생각 중인 세션"과 "막힌 세션"은 관측상 구분되지 않기 때문이다.
// done 은 finish 호출이 만든다.
type SessionState string

const (
	SessionActive  SessionState = "active"
	SessionPaused  SessionState = "paused"
	SessionBlocked SessionState = "blocked" // blocked_why 필수 (스키마 CHECK)
	SessionDone    SessionState = "done"
)

// SignalKind 는 생존 '사실'의 종류다. 판정이 아니다.
//
// 넷을 나란히 두고 합치지 않는다. 하나만 보면 반드시 오판한다 —
// 에이전트가 긴 도구를 돌리는 동안 Prompt 는 안 오지만 Tool 은 오고,
// 사람이 읽기만 하는 동안은 Prompt 만 온다.
// Commit·Push 는 서버의 git 리더가 직접 관측하는 유일한 신호다(클라이언트를 안 믿는다).
//
// ★ MCP 는 **도구 호출**이다 — 그 이상도 이하도 아니다. 세션을 여는 것과 상태를
// 바꾸는 것은 여기 안 들어간다(그렇게 찍던 두 자리를 지웠다). 다만 조회 전용 도구도,
// 판단 저장(service.Note — REST 로 열려 있어 PreCompact 훅의 자동 초안이 들어온다)도
// 이 신호를 찍는다. 그래서 MCP 는 "살아 있다"이지 "일하고 있다"가 아니다.
type SignalKind string

const (
	SignalPrompt SignalKind = "prompt"
	SignalTool   SignalKind = "tool"
	SignalMCP    SignalKind = "mcp"
	SignalCommit SignalKind = "commit"
	SignalPush   SignalKind = "push"
)

// FootprintOrigin 은 그 경로를 어떻게 알게 됐는지다.
//
// 뭉개지 않는다. 뭉개면 "선언했으나 안 건드림"과 "선언 없이 건드림"이 구분되지 않고,
// 그러면 "겹침 없음"과 "이 축을 안 본다"도 구분되지 않는다.
type FootprintOrigin string

const (
	OriginObserved FootprintOrigin = "observed" // PostToolUse 훅이 실제 편집을 봤다 — service.Beat
	OriginDeclared FootprintOrigin = "declared" // 세션이 집을 때 선언했다 — ★ 지금 생산자가 없다(아래)
	OriginClaimed  FootprintOrigin = "claimed"  // 항목이 선언한 경로 — service.Pick
)

// ★ OriginDeclared 를 만드는 자리가 지금 **하나도 없다.**
//
// 유일한 생산자였던 POST /api/v1/footprints 를 2026-08-05 에 지웠다 — 그 표면을 치는
// 클라이언트가 0건이었고, 그래서 `declared` 행도 DB 에 **0건**이었다(observed 592 ·
// claimed 140 실측). 지운 근거 전문은 api/handlers_session.go 의 그 자리에 있다.
//
// 열거에서는 **안 뺐다.** 스키마 CHECK(footprint.origin)와 PK 가 셋을 전제하고 있어
// 빼면 마이그레이션이 되고, 그것은 이 축의 문제가 아니다. 그리고 이 값의 뜻("세션이
// 집을 때 선언했다")은 여전히 유효하다 — 되살릴 자리가 생기면 그때 생산자를 만들면 된다.
// 다만 **지금 이 값을 근거로 무엇을 판정하지 마라.** 항상 0건이다.

// ItemState 는 큐 항목의 상태다.
type ItemState string

const (
	ItemOpen    ItemState = "open"
	ItemClaimed ItemState = "claimed"
	ItemDone    ItemState = "done"
	ItemDropped ItemState = "dropped" // close_reason 필수 (스키마 CHECK)
)

// JudgmentKind 는 사람이 남기는 판단의 종류다. 이것만이 원리적으로 파생 불가한 자산이다.
type JudgmentKind string

const (
	JudgmentHandoff  JudgmentKind = "handoff"  // 랜딩된 것·검증 방법·일부러 안 한 것·후속
	JudgmentDecision JudgmentKind = "decision" // 되돌리기 비싼 결정과 근거
	JudgmentBlocked  JudgmentKind = "blocked"
	JudgmentAsk      JudgmentKind = "ask" // 남이 건드리면 곤란한 것 — 커밋 전 의도의 유일한 축
	JudgmentNow      JudgmentKind = "now"
	JudgmentRejected JudgmentKind = "rejected" // 검토했으나 기각한 후보
	JudgmentNotDone  JudgmentKind = "not-done" // 일부러 안 한 것
	JudgmentVerified JudgmentKind = "verified" // 확인했으나 "문제 없음"이었던 조사
	JudgmentDraft    JudgmentKind = "draft"    // PreCompact 자동 초안
)

// SnapshotMethod 는 그 수치가 어떻게 나왔는지다.
// Manual 이면 evidence 가 필수다(스키마 CHECK) — 근거 없는 숫자를 못 넣게 한다.
type SnapshotMethod string

const (
	SnapshotCommand SnapshotMethod = "command"
	SnapshotManual  SnapshotMethod = "manual"
)

// ─────────────────────────────────────────────────────────────────────────────
// 엔티티
// ─────────────────────────────────────────────────────────────────────────────

type Project struct {
	ID            string
	Path          string
	RemoteURL     string // 비어 있어도 Tier A 는 완전히 돈다
	DefaultBranch string
	Config        string // .flightdeck.yaml 캐시(JSON). 정본은 레포 안의 파일
	ConfigFromSHA string
	CreatedAt     time.Time
	// PinnedAt·ArchivedAt 는 **표시 축**이다. 제로값이 "아님"이고, 둘 다 사람이 화면에서
	// 정한다. 판정 경로(겹침·처방·추천)는 이 둘을 안 읽는다 — 읽는 순간 접어 둔 프로젝트가
	// 조용히 조율에서 빠진다.
	PinnedAt   time.Time
	ArchivedAt time.Time
}

// WorkspaceMember 는 루트 프로젝트가 선언한 멤버 레포 하나다(증분 014).
//
// ★ 이 타입이 model 에 사는 이유는 **세 계층이 같은 값을 나르기 때문**이다:
// judge 가 커밋된 `.flightdeck.yaml` 에서 뜯고(ParseWorkspace), store 가 캐시 표에
// 넣고, service 가 절대경로로 풀어 git 을 읽는다. 계층마다 제 타입을 두면 그 사이
// 변환이 세 벌 생기고, 변환은 필드를 하나 빠뜨려도 컴파일이 통과한다.
//
// Path 는 **루트 상대**다 — 절대경로를 담지 않는 근거는 증분 014 의 머리말에 있다.
type WorkspaceMember struct {
	// Project 는 멤버 레포의 프로젝트 id 다. 파일에서 비어 오면 Path 의 마지막 마디로
	// 채워지는데, 그 채움은 **judge.MemberProjectID 한 자리**에서만 일어난다 —
	// 이 구조체가 store 에 닿을 때는 이미 채워져 있다(빈 값은 저장하지 않는다).
	Project string
	Path    string
}

type Machine struct {
	ID        string
	Hostname  string
	FirstSeen time.Time
	LastSeen  time.Time
}

// Session 은 한 Claude Code 세션이다.
//
// 정체는 (MachineID, Worktree, CCSessionID) 3중키다. 워크트리 경로만으로는 안 된다 —
// 경로는 규율상 재사용되고(지우고 다시 만든다), 그러면 옛 세션 행과 합쳐진다.
// 그리고 이 타입 어디에도 접두 일치가 없으므로 조상 트리의 등록을 물려받는 것이 불가능하다.
//
// Ended 필드가 없다. 세션 종료를 신뢰성 있게 감지할 수단이 없으므로
// 채웠다면 반드시 거짓으로 채워졌을 것이다.
// PID 필드도 없다 — pid 死를 근거로 살아 있는 세션을 죽었다고 판정한 사고가 실재한다.
type Session struct {
	ID          string // 서버 발급 ULID
	Project     string
	MachineID   string
	Worktree    string
	CCSessionID string
	Label       string // 표시 전용. 어떤 필터의 축도 아니다
	State       SessionState
	BlockedWhy  string
	OpenedAt    time.Time

	// Harness 는 이 카드를 연 도구다(claude·codex). **선언에서만 온다**(--harness).
	//
	// ★ 빈 값이 「미상」이고, 그것을 claude 로 접지 않는다 — 환경으로는 못 가르기
	// 때문이다(중첩 실행이 양방향으로 거짓말한다). 원장에서는 NULL 로 산다.
	//
	// ★ **3중키에 안 들어간다.** 좌표가 아니라 속성이다 — 키에 넣으면 표기 하나가
	// 바뀔 때 같은 세션이 다른 카드가 된다.
	Harness string
}

// 하네스 이름 — 정체가 **어느 도구**에서 왔는가(DESIGN 「14. 하네스 축」).
//
// ★ 자리가 여기인 이유: 소비자가 세 패키지다 — mcpsrv(환경 축을 고른다) · judge(처방
// 문법을 고른다) · store/render(화면에 찍는다). judge 는 mcpsrv 를 import 할 수 없고
// (역방향 의존이다) model 은 아무것도 import 하지 않으므로, 셋이 함께 볼 수 있는 자리는
// 여기 하나뿐이다. 값을 두 벌로 두면 한쪽만 고쳐진 날 그 어긋남이 조용하다.
const (
	HarnessClaude = "claude"
	HarnessCodex  = "codex"
)

// HarnessLabel 은 화면에 찍을 하네스 이름이다. 선언이 없으면 「미상」이다.
//
// ★ 문자열이 mcpsrv 의 Identity.HarnessLabel 과 **같아야 한다.** 한쪽은 관측 직후의
// 배너가, 다른 쪽은 원장을 읽은 보드가 쓰는데 두 화면이 같은 상태를 다르게 부르면
// 사람이 그것을 두 가지 일로 읽는다.
func (s Session) HarnessLabel() string {
	if strings.TrimSpace(s.Harness) == "" {
		return "미상"
	}
	return s.Harness
}

type Workspace struct {
	SessionID string
	Project   string
	Path      string
	IsPrimary bool
}

type Signal struct {
	SessionID string
	Kind      SignalKind
	At        time.Time
}

type Footprint struct {
	SessionID string
	Path      string
	Origin    FootprintOrigin
	FirstAt   time.Time
	LastAt    time.Time
}

type RefState struct {
	Project string
	Ref     string
	SHA     string
	Subject string
	At      time.Time // 관측 시각. UI 가 신선도를 이걸로 표시한다
}

// ChangeSet 은 두 커밋 사이의 변경 경로다.
// 변경 시점에 계산해 불변으로 보관한다 — 브랜치가 지워져도 남는다.
type ChangeSet struct {
	Project    string
	BaseSHA    string
	HeadSHA    string
	Paths      []string
	ComputedAt time.Time
}

type Item struct {
	Project     string
	ID          string // 브랜치 이름으로도 쓰이므로 전역 유일해야 한다
	Title       string
	Body        string
	Paths       []string
	Labels      []string // 표시 전용
	State       ItemState
	CloseReason string
	LandedRef   string // 러너가 실제로 fast-forward 한 sha 만
	CreatedAt   time.Time
	ClosedAt    *time.Time
	After       []After
}

// After 는 선행 조건이다. 셋 중 **정확히 하나**만 채운다(스키마 CHECK).
// 브랜치 이름을 담을 자리가 없다 — 랜딩이 끝나면 브랜치가 지워져
// 조건이 충족되는 바로 그 순간 판정이 해석 불가가 되기 때문이다.
type After struct {
	Item string
	Job  string
	SHA  string
	// Project 는 **Item 이 어느 프로젝트의 것인가**다(증분 015). 비면 이 항목과 같은
	// 프로젝트이고, 증분 이전의 모든 선행이 그 상태다 — 그래서 빈 값이 곧 기존 동작이다.
	//
	// ★ Job·SHA 에는 안 쓴다. sha 는 전역 유일하고(커밋 해시), 잡은 Tier B 라 아직
	// 프로젝트를 넘는 형태가 없다. 세 축에 다 붙이면 안 쓰는 두 축이 「채워도 되나」라는
	// 질문을 만들고, 그 질문의 답이 코드 어디에도 없게 된다.
	Project string
}

type Claim struct {
	Project     string
	ItemID      string
	SessionID   string
	At          time.Time
	ReleasedAt  *time.Time
	ForceReason string
}

// PickEval 은 추천 1건과 탈락 사유 전부다.
// 사유가 없으면 큐는 블랙박스가 되고, 블랙박스는 두 번째 세션부터 무시된다.
type PickEval struct {
	Project   string
	SessionID string
	At        time.Time
	Picked    string // 묶음 **선두**의 항목 id. 빈 문자열이면 적격 0건
	// PickedWith 는 선두를 뺀 나머지다. 비면 단독이었다는 뜻이고,
	// 저장에서 NULL 이 된다 — 빈 배열로 쓰지 않는다.
	PickedWith []string
	Rejected   []Rejection
}

type Rejection struct {
	Item   string `json:"item"`
	Reason string `json:"reason_code"`
	Detail string `json:"detail"`
}

type Judgment struct {
	ID         string
	Project    string
	SessionID  string
	At         time.Time
	Kind       JudgmentKind
	Title      string
	Body       string
	Supersedes string
	Links      []JudgmentLink
}

type JudgmentLink struct {
	TargetKind string // item | job | commit | session
	TargetID   string

	// TargetProject 는 이 링크가 가리키는 것이 **어느 프로젝트의** 것인가다.
	//
	// ★ 빈 값은 "모른다"가 아니라 **판단 자신의 프로젝트**다. 읽는 쪽이
	// COALESCE(target_project, judgment.project) 로 해석하므로, 증분 이전에 쌓인
	// 링크 4240건은 컬럼이 NULL 인 채로 지금까지와 똑같이 읽힌다 — 백필이 필요 없다.
	// 그 무해함이 이 필드를 NOT NULL 로 안 만든 이유다.
	//
	// ★ 채워지는 것은 **교차 프로젝트 링크뿐**이다. A 프로젝트 세션이 B 프로젝트
	// 항목에 판단을 걸면 여기에 B 가 실리고, 그래야 B 를 집는 세션이 그 판단을 본다.
	// 이 필드가 없던 동안 그런 링크 10건이 원장에 들어갔고 한 번도 안 읽혔다.
	TargetProject string
}

type Snapshot struct {
	Project     string
	Key         string
	Value       string
	Method      SnapshotMethod
	Evidence    string // Method=Manual 이면 필수
	InputDigest string // 현재 트리와 다르면 UI 가 "낡음"을 붙인다
	ComputedAt  time.Time
}

type ResourceHold struct {
	ID          int64
	Project     string
	Resource    string
	SessionID   string
	JobID       string
	AcquiredAt  time.Time
	ReleasedAt  *time.Time
	ForceReason string
}

// LandingLeftKind 는 랜딩 줄에서 빠진 종류다. schema 의 CHECK 와 문자열이 정확히 일치해야 한다.
//
// 종류를 사유와 한 컬럼에 뭉개지 않는다 — `force:<사유>` 접두 파싱은
// api/idempotency.go 가 이미 기각한 방식이다. 종류는 CHECK 로, 사유는 별도 컬럼으로 둔다.
type LandingLeftKind string

const (
	// LandingLeftOK 는 **"랜딩됐다"가 아니다.** 세션이 ok 로 보고하고 레인을 놓았다는 뜻뿐이다.
	// 랜딩 sha 의 출처는 러너가 실제로 fast-forward 한 sha 하나이고(설계 §5),
	// 클라이언트 자기 보고를 그 자리에 넣으면 "남의 커밋이 이 항목의 랜딩 sha 로 박힌"
	// 결함(3회 관측)이 이름만 바꿔 부활한다. Item.LandedRef 를 이 값으로 채우지 마라.
	LandingLeftOK LandingLeftKind = "ok"

	LandingLeftFail   LandingLeftKind = "fail"   // 검증 실패. left_detail 필수(스키마 CHECK)
	LandingLeftLeave  LandingLeftKind = "leave"  // 줄 서 놓고 스스로 빠졌다. left_detail 필수
	LandingLeftFinish LandingLeftKind = "finish" // 세션이 마무리하며 함께 닫혔다
	LandingLeftForce  LandingLeftKind = "force"  // 사람이 회수했다. left_detail 필수
)

// LandingRow 는 랜딩 레인 줄의 한 자리다. **ID 가 곧 순번이다.**
// GrantedAt 이 없다 — "쥐었나"는 resource_hold 의 부분 유니크 인덱스가 정본이다.
type LandingRow struct {
	ID         int64
	Project    string
	SessionID  string
	EnqueuedAt time.Time
	LeftAt     *time.Time // nil 이면 아직 줄에 있다
	LeftKind   LandingLeftKind
	LeftDetail string
	Resources  []string // 이 행이 줄 선 자원들(정렬). 008 이전 행은 마이그레이션이 'landing' 을 백필했다
}

type Event struct {
	ID        int64
	At        time.Time
	Project   string
	SessionID string
	Kind      string
	Payload   string // JSON
}

// CloseDeclaration 은 이 항목을 닫으려다 롤백된 선언이다.
//
// ★ 뜻은 "일이 끝났다"가 아니라 **"이 항목은 닫혀야 한다는 판단이 본문과 함께 내려졌다"**이다.
// 원장의 item.finish 는 트랜잭션 첫 문장에서 예약되고 롤백 갈래에서도 흘러가므로, 그 이벤트가
// 있는데 항목이 아직 열려 있다면 그 시도는 죽은 것이다 — 그리고 그때 쓰인 판단 본문도 함께
// 죽었다(실측 10300·5421·5060·3032 바이트).
//
// ★ 이 타입이 **model 에 있는 이유.** store 도 judge 도 이것을 손에 쥐는데 둘 사이에는 의존이
// 없다(store 는 judge 를 import 하지 않고, judge 의 import 는 model 하나뿐이다). 어느 한쪽에
// 두면 없던 방향의 의존이 새로 생긴다. Rejection 이 같은 모양의 선례다 — judge 가 만들고
// store 가 저장한다.
//
// ★ Done 과 Dropped 을 **합쳐 담지 않는다.** 처방이 갈린다 — done 은 "이미 랜딩됐을 수 있다",
// dropped 는 "이미 버리기로 판정됐을 수 있다"이고, 실측 384건 중 dropped 가 76건(20%)이다.
type CloseDeclaration struct {
	Done        int       // mode=done 인 선언 수
	Dropped     int       // mode=dropped 인 선언 수
	Last        time.Time // 마지막 선언 시각
	LastSession string    // 마지막 선언을 한 세션 id
	LastMode    string    // 마지막 선언의 mode ("done" | "dropped")
}

// Count 는 mode 를 가리지 않은 선언 수다. "몇 번 선언됐나"는 합이다.
func (d CloseDeclaration) Count() int { return d.Done + d.Dropped }

// ─────────────────────────────────────────────────────────────────────────────
// 조회 결과 — 화면과 도구가 쓰는 조립 타입
// ─────────────────────────────────────────────────────────────────────────────

// LineDelta 는 경로 하나의 증감이다.
//
// ★ **이것을 나르는 맵에서 키가 없는 것은 0 이 아니라 "못 읽었다"** 다. 둘을 뭉개면
// "안 만졌다"와 "못 쟀다"가 같은 화면이 되고, 그것이 이 축이 없애려는 오탐의 거울상이다.
// 바로 아래 SessionView.Signals 가 같은 관용을 쓴다("없는 종류는 키가 없다").
//
// 규모를 못 재는 자리가 넷 있고 넷 다 키 부재로 접힌다 — 이진 파일(numstat 이 `-`/`-` 를 낸다) ·
// 미추적 파일(numstat 에 아예 안 나온다) · footprint 에만 있는 경로 · git 파생 실패.
type LineDelta struct {
	Added   int
	Removed int
}

// SessionView 는 board 가 세션 하나에 대해 보여주는 전부다.
//
// "죽었다"는 필드가 없다. 신호 넷의 나이를 숫자로만 낸다 —
// 불리언을 만드는 순간 그것이 회수·회피·탈락 셋의 상류가 되고,
// 그 판정은 실측에서 두 번 틀렸다.
type SessionView struct {
	Session Session
	Signals map[SignalKind]time.Time // 없는 종류는 키가 없다. 0값과 부재를 가른다
	Paths   []string                 // footprint ∪ change_set
	// PathDelta 는 Paths 중 규모를 잰 것의 증감이다(커밋 구간 + 미커밋 구간의 **합**).
	//
	// ★ **없는 키는 0 이 아니라 "못 읽었다"** 다 — 바로 위 Signals 와 같은 관용이다.
	// 이진 파일 · 미추적 파일 · footprint 에만 있는 경로 · git 파생 실패가 그 자리다.
	// Paths 와 합치지 않는 이유가 이것이다: 합치면 "바뀌었는데 못 쟀다"를 표현할 수 없다.
	//
	// 두 구간(`forkSHA..branch` 와 `HEAD..worktree`)은 **서로소**라 더하면 갈래 지점
	// 이후 전부가 정확히 나온다. 어느 쪽이 이기는 규칙이 필요 없다.
	PathDelta    map[string]LineDelta
	HasFootprint bool     // false 면 "발자국 없음"을 명시한다. 침묵하지 않는다
	Claims       []string // 선점한 항목 id
	LastNote     *Judgment
	Branch       string
	BranchSHA    string
	AheadMain    int
}

// Freshness 는 파생값이 언제 관측된 것인지다.
// 모든 패널에 붙는다 — 서버가 죽었을 때 마지막 상태가 현재 사실인 척하는 것을 구조로 막는다.
type Freshness struct {
	Source     string // "git" | "cache" | "db"
	ObservedAt time.Time
	Stale      bool
}
