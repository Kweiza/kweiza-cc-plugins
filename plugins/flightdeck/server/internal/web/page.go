package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// 화면 모델. 템플릿은 여기 담긴 것만 찍는다 — 템플릿 안에서 판정하지 않는다.
//
// ★ 패널마다 Derived(파생 표기)와 Fail(못 읽은 축)이 따로 있다. 한 장으로 합치면
// "큐는 읽었는데 git 만 죽었다"가 화면에서 사라지고, 그러면 반쪽짜리 화면이 온전한 척한다.

// Panel 은 모든 섹션이 공유하는 머리말이다.
type Panel struct {
	Derived string // "(파생: git@14:31 · 12초 전)"
	Fail    []service.DerivedFailure
	Err     string // 이 패널을 못 만든 사유. 비어 있지 않으면 내용은 반쪽이다
}

// SessionRow 는 섹션 ① 의 세션 한 장이다.
type SessionRow struct {
	ID          string
	Short       string
	Label       string
	State       string
	BlockedWhy  string
	Worktree    string
	Branch      string
	BranchNote  string // 못 읽었을 때만 채운다. 빈 브랜치 이름과 "못 읽음"을 가른다
	Ahead       string
	AheadNote   string
	Signals     []SignalAge
	Paths       []string
	MorePaths   int
	Footprint   string // "발자국 없음" 또는 "경로 N건"
	HasPaths    bool
	Claims      []string
	LastNote    *NoteRow
	DeriveError string
	IsSelf      bool
	OpenedAge   string

	// HasActivity·Activity 는 활동 배지다(ActivityOf). 불리언과 사유를 함께 든다 —
	// 불리언만 두면 화면이 "왜"를 못 말하고, 사람은 그 "왜"로 회수를 판단한다.
	HasActivity bool
	Activity    string
	// Derived 가 false 면 git 파생(브랜치·ahead·미커밋)을 **안 읽은** 행이다.
	// 창 밖 선점자 줄이 그렇다 — 파생은 카드당 git 호출 1~4회라 창 밖까지 안 돈다.
	// 0값과 "안 읽었다"를 가르는 축이라, 화면이 이 사실을 말해야 한다.
	Derived bool
}

// LivePanel 은 섹션 ① 이다.
type LivePanel struct {
	Panel
	Window   string
	Sessions []SessionRow
	Empty    string
	Targets  []ClaimTarget // 선점 회수 폼의 선택지

	// OutOfWindow 는 창 밖이라 표에 안 나온 세션이 있다는 사실을 사람이 읽는 한 줄로 낸다.
	// 0건이면 빈 문자열이다 — **화면이 반드시 말한다**, MCP board 와 같은 이유다.
	// 침묵하면 "그런 세션이 없다"와 "안 보여 준다"가 구분되지 않는다(설계 §4).
	OutOfWindow string

	// Folded 는 **선점이 없어 안 낸** 세션이 있다는 사실이다. 0건이면 빈 문자열이다.
	//
	// ★ OutOfWindow 와 따로 두는 이유: 접힌 사유가 둘이고 처방이 다르다.
	// 창 밖은 "오래 조용하다"이고 선점 없음은 "큐 밖에서 일한다"다. 한 줄로 뭉치면
	// 읽는 사람이 손댈 자리를 못 찾는다.
	//
	// ★ 그리고 **조율에서 빠진 것이 아니라는 사실을 함께 말한다.** 겹침 처방은
	// 그 세션들도 그대로 본다(prescribe.go 가 ListLive 를 직접 읽는다) — 안 그러면
	// 사람이 "저 세션은 아무도 안 본다"고 잘못 읽는다.
	Folded string
}

// ClaimTarget 은 회수 가능한 선점 하나다. **근거를 함께 낸다**(설계 §4 의 여섯 축 중 표시분).
//
// ★ 여섯째가 종료 선언이다 — "이 항목을 닫으려다 롤백된 적이 있다". 앞의 다섯(신호 나이·
// 발자국·선점 시각·브랜치·ahead)은 전부 **세션**에 대한 사실이고, 이것만 **항목**에 대한
// 사실이다. 그래서 카드가 아니라 이 줄에 실린다.
type ClaimTarget struct {
	ItemID string
	Title  string // 회수 폼 줄은 이것이 전부다 — 제목이 없으면 무엇을 회수하는지 id 로만 판단한다
	Holder string
	Since  string
	Live   string // 그 세션이 창 안에 있나 — 판정이 아니라 사실 표기다

	// CloseDeclared 는 ItemRow 와 **같은 규약**이다(빈 문자열=선언 없음, CloseDeclUnread=못 읽음).
	// 여기서 다시 계산하지 않고 ItemRow 에서 그대로 옮긴다 — 두 자리에서 따로 세면
	// 같은 이름이 표면마다 다른 사실을 내는 어긋남이 재현된다(설계 §4-⑤).
	CloseDeclared string
}

// UnackedPanel 은 섹션 ② 다.
type UnackedPanel struct {
	Panel
	Jobs  []UnackedJobRow
	Empty string
	Tier  string
}

type UnackedJobRow struct {
	ID, Kind, ItemID, State, FailKind, LogTail string
	Ended                                      string
}

// ItemRow 는 섹션 ③ 의 항목 한 줄이다.
type ItemRow struct {
	ID         string
	Title      string
	Body       string
	State      string
	Paths      []string
	Labels     []string
	After      []string
	Dependents int
	Holder     string
	Since      string

	// CloseDeclared 는 이 항목을 닫으려다 롤백된 선언의 표기다(format.go 의 CloseDeclaredLabel).
	//
	// **빈 문자열 = 선언 없음 · CloseDeclUnread("?") = 이 축을 못 읽었다.** 셋째 상태를
	// 0으로 접지 않는 것이 이 층의 규율이고, 같은 구조체의 Dependents = -1 이 선례다.
	//
	// ★ 사실을 **여기 하나에** 싣는다. buildPage 가 회수 폼의 원천을 큐 Items 로 삼으므로
	// (p.Live.Targets = p.Queue.claimTargets(board)) 큐 표·회수 폼·폐기 폼 셋이 배선 없이
	// 같은 문장을 얻는다. 표면마다 따로 계산하면 같은 이름이 다른 사실을 낸다.
	//
	// ★ ④ 랜딩 이력의 ItemRow 는 이 값을 안 채운다(빈 문자열). 그쪽은 이미 닫힌 항목이라
	// 이 축이 겨냥하는 인구가 아니다 — 롤백된 선언은 항목이 **아직 열려 있을 때만** 사고다.
	CloseDeclared string
}

// QueuePanel 은 섹션 ③ 이다.
type QueuePanel struct {
	Panel
	Items    []ItemRow
	Empty    string
	Stats    RejectionStats
	StatsErr string
	Window   string

	// Targets 는 항목 폐기 폼의 선택지다. **줄 전체를 나른다** — id 만 나르면 폐기 폼이
	// 같은 사실을 다시 계산해야 하고, 두 자리에서 따로 계산하는 순간 같은 이름이
	// 표면마다 다른 사실을 내는 어긋남이 재현된다(설계 §4-⑤).
	Targets []ItemRow
}

// SnapshotRow 는 섹션 ④ 의 스냅숏 한 줄이다. 낡음 판정이 붙는다.
type SnapshotRow struct {
	Key      string
	Value    string
	Method   string
	Evidence string
	Computed string
	Verdict  SnapshotVerdict

	// 아래는 Value 가 진척 모양의 JSON 일 때만 채워진다(ParseProgress).
	// 원문 Value 는 그대로 남는다 — **표시를 위해 원본을 바꾸지 않는다.**
	Progress *Progress
}

// Progress 는 진척 수치 하나다. 화면에만 쓰는 표시 타입이다.
type Progress struct {
	Pct     int
	Owner   string
	State   string
	Delta   string
	Done    int // 완료 항목 수
	Partial int // 부분
	Nothing int // 없음
	Total   int
}

// ParseProgress 는 스냅숏 값이 진척 모양이면 그것을 읽는다. 순수 함수다.
//
// ★ 값이 JSON 이라고 무조건 풀지 않는다 — `pct` 가 있을 때만 진척으로 본다.
// 그 조건이 없으면 다른 종류의 스냅숏(그냥 수치나 문자열)이 우연히 JSON 이라는 이유로
// 진척 막대로 그려지고, 그러면 화면이 없는 뜻을 지어내게 된다.
//
// 못 읽으면 nil 을 돌려주고 호출자는 원문을 그대로 보인다 —
// **표시를 못 한다고 값을 감추지 않는다.**
func ParseProgress(value string) *Progress {
	var m map[string]any
	if err := json.Unmarshal([]byte(value), &m); err != nil {
		return nil
	}
	pct, ok := m["pct"].(float64)
	if !ok {
		return nil
	}
	num := func(k string) int {
		if v, ok := m[k].(float64); ok {
			return int(v)
		}
		return 0
	}
	str := func(k string) string {
		if v, ok := m[k].(string); ok {
			return v
		}
		return ""
	}
	g := &Progress{
		Pct: int(pct), Owner: str("owner"), State: str("state"), Delta: str("delta"),
		Done: num("d"), Partial: num("q"), Nothing: num("n"),
	}
	g.Total = g.Done + g.Partial + g.Nothing
	return g
}

// LandingPanel 은 섹션 ④ 다.
type LandingPanel struct {
	Panel
	Closed    []ItemRow
	Snapshots []SnapshotRow
	SnapErr   string
	Empty     string
	Tier      string
	Current   string // 현재 입력(기본 브랜치 HEAD) — 스냅숏 대조의 상대
	// 레인 절. **0건·못 읽음·어긋남을 각각 자기 문자열로 가진다** — 한 자리에 접으면
	// "질의가 안 돌았다"와 "아무도 안 섰다"가 화면에서 같아진다.
	Lane      []LaneRow
	LaneErr   string
	LaneEmpty string
	LaneWarn  string
}

// LaneRow 는 랜딩 줄의 한 자리다.
//
// ★ 회수 판정은 **두 숫자**로 한다: 얼마나 오래 쥐고 있나(Held)와 마지막 신호가
// 얼마나 낡았나(Signal). 자동 만료를 안 만든 근거가 "사람이 이 나이들을 보고 판정한다"
// 이므로 한 축이 빠지면 판정의 근거가 없다 — 어긋남 행에서도 반드시 둘 다 낸다.
type LaneRow struct {
	RowID   int64
	Session string
	Waiting string // 대기 경과 — 줄에 선 뒤 지난 시간
	Signal  string // 마지막 신호 나이. 빈 문자열 = 관측 실패이거나 신호가 없다
	Holder  bool   // 지금 레인을 쥐고 있나
	Held    string // 획득 경과. 점유자만 채운다
	Missing bool   // 점유자인데 줄에 행이 없다(정합 어긋남) — 회수 번호가 없는 행이다
}

// HoldRow 는 자원 점유 한 줄이다.
type HoldRow struct {
	Resource string
	Holder   string
	Since    string
	Age      string
}

// BlockedPanel 은 섹션 ⑤ 다.
type BlockedPanel struct {
	Panel
	Notes    []NoteRow
	Asks     []NoteRow
	Sessions []SessionRow
	Held     []HoldRow
	Disk     ResourceAlert
	Empty    string
}

// NoteRow 는 판단 한 줄이다.
type NoteRow struct {
	ID      string
	Kind    string
	At      string
	Age     string
	Title   string
	Body    string
	Session string
	Links   []string
}

// SearchPanel 은 섹션 ⑥ 이다.
type SearchPanel struct {
	Panel
	Query   string
	Mode    string
	Results []NoteRow
	Empty   string
}

// ProjectRow 는 프로젝트 줄의 한 칸이다.
type ProjectRow struct {
	ID       string
	On       bool // 지금 보고 있는 것
	Pinned   bool
	Archived bool
	// LastSession 은 마지막 세션의 나이다("3일 전"). 보관 목록에서만 쓴다 —
	// 보관해 둔 것이 다시 돌기 시작하면 그 사실이 보여야 사람이 풀 수 있다.
	LastSession string
}

// ProjectNav 는 헤더의 프로젝트 줄 전부다.
//
// ★ 접는 것과 지우는 것은 다르다. 접힌 것도 Folded·Archived 에 그대로 실려 HTML 에
// 나가고 <details> 가 닫아 둘 뿐이다 — 열면 보이고, ?project= 로는 언제나 열린다.
type ProjectNav struct {
	Shown    []ProjectRow // 핀 + 지금 보고 있는 것
	Folded   []ProjectRow // 핀도 보관도 아닌 것
	Archived []ProjectRow
	// FoldedLine 은 "나머지 3 (보관 1 포함)" 이다. 비면 접을 것이 없다는 뜻이다.
	FoldedLine string
	// NoPins 는 핀이 하나도 없을 때의 안내다. 그때는 아무것도 안 접는다.
	NoPins string
}

// Page 는 렌더 한 장의 전부다.
type Page struct {
	Now string
	// RenderedAt 은 이 장을 그린 시각(unix)이다. 쓰기 폼의 멱등 키가 여기서 나온다 —
	// WriteKey 를 보라.
	RenderedAt int64
	Title      string
	Projects   []model.Project
	// Nav 는 위 Projects 를 화면 모양으로 접은 것이다. 템플릿은 이쪽만 읽는다.
	Nav        ProjectNav
	Project    model.Project
	HasProject bool
	Notice     string
	Error      string
	// NotFound 는 **요청한 프로젝트가 없다**는 뜻이다. "등록된 프로젝트가 아직 하나도 없다"와
	// 가른다 — 뒤쪽은 서버가 정상이고 아무도 안 붙은 것뿐이라 404 로 답하면 거짓말이 된다.
	NotFound   bool
	Health     service.Health
	HealthLine string
	Disk       ResourceAlert
	Refresh    int
	SSEPath    string
	Live       LivePanel
	Unacked    UnackedPanel
	Queue      QueuePanel
	Landing    LandingPanel
	Blocked    BlockedPanel
	Search     SearchPanel
}

// WriteKey 는 쓰기 폼 하나의 멱등 키다. 템플릿이 폼 action 의 쿼리에 싣고,
// api 의 withScreenWrite 가 그것을 Idempotency-Key 헤더로 올린다.
//
// **쓰기 종류마다 다른 값이어야 한다.** 한 장이 키 하나를 공유하면 회수를 누른 뒤
// 폐기를 누를 때 같은 키가 되어, 두 번째가 첫 번째의 재시도로 접힌다.
//
// 렌더 시각을 쓰는 이유: 더블클릭은 같은 장이라 같은 키 → 접힌다.
// 새로고침하면 새 장이라 새 키 → 다시 눌린다. 멱등이 원래 원하는 의미 그대로다.
func (p Page) WriteKey(kind string) string {
	return "web:" + kind + ":" + strconv.FormatInt(p.RenderedAt, 10)
}

// buildProjectNav 는 프로젝트 목록을 화면 모양으로 접는다. 순수 함수다.
//
// ★ 핀이 0이면 아무것도 안 접는다. 이것이 이 화면의 정직함이다 — 핀이 없다는 사실을
// 자동 판정(활동이 있는 것만 편다)으로 덮으면, 사람이 접은 것과 규칙이 접은 것이 화면에서
// 같은 모양이 되고 "왜 사라졌나"에 답할 수 없다.
//
// ★ 지금 보고 있는 것은 핀이 아니어도 편다. 안 그러면 화면이 자기 위치를 안 말한다.
func buildProjectNav(projects []model.Project, current string, ages map[string]string) ProjectNav {
	var nav ProjectNav
	pinned := 0
	for _, p := range projects {
		if !p.PinnedAt.IsZero() {
			pinned++
		}
	}

	for _, p := range projects {
		row := ProjectRow{
			ID:          p.ID,
			On:          p.ID == current,
			Pinned:      !p.PinnedAt.IsZero(),
			Archived:    !p.ArchivedAt.IsZero(),
			LastSession: ages[p.ID],
		}
		// ★ row.Pinned 를 row.Archived 보다 먼저 본다. projectView 핸들러(actions.go)가
		// pin 을 찍으면 archived 를 지우고 archive 를 찍으면 pinned 를 지워 상호배타를
		// 만들지만, SetProjectView 자체는 두 축을 안 묶는다 — 원장은 둘 다 켜진 조합을
		// 여전히 담을 수 있다(옛 값·수동 UPDATE·경합). 그 조합이 실제로 생겨도 화면이
		// 결정론적으로 한쪽을 골라야 하므로 여기서 순서로 정한다. Shown 쪽을 이기게
		// 두는 이유는, 접힌 것을 펴는 대가(줄이 붐빈다)가 펼 것을 접는 대가(찾던 것이
		// 사라진 줄 안다)보다 싸기 때문이다.
		switch {
		case pinned == 0:
			nav.Shown = append(nav.Shown, row)
		case row.Pinned || row.On:
			nav.Shown = append(nav.Shown, row)
		case row.Archived:
			nav.Archived = append(nav.Archived, row)
		default:
			nav.Folded = append(nav.Folded, row)
		}
	}

	if pinned == 0 {
		nav.NoPins = "핀이 없다 — ★ 로 남길 것을 고르면 나머지가 접힌다"
		return nav
	}
	if n := len(nav.Folded) + len(nav.Archived); n > 0 {
		nav.FoldedLine = fmt.Sprintf("나머지 %d", n)
		if a := len(nav.Archived); a > 0 {
			nav.FoldedLine += fmt.Sprintf(" (보관 %d 포함)", a)
		}
	}
	return nav
}

// archivedSessionAges 는 **보관된 프로젝트만** 마지막 세션 나이를 읽는다.
//
// ★ 전건을 안 읽는다. 이 줄은 페이지 머리라 매 렌더 도는데, 프로젝트가 늘수록 질의가
// 함께 느는 자리를 머리에 두면 화면 전체가 그 비용을 문다. 보관 목록에만 필요한 값이다 —
// 보관해 둔 것이 다시 돌기 시작하면 사람이 그것을 보고 풀어야 하기 때문이다.
func (h *handler) archivedSessionAges(ctx context.Context, projects []model.Project,
	now time.Time) map[string]string {

	ages := map[string]string{}
	for _, p := range projects {
		if p.ArchivedAt.IsZero() {
			continue
		}
		at, err := h.svc.Store().LastSessionAt(ctx, p.ID)
		if err != nil {
			h.log.ErrorContext(ctx, "마지막 세션 시각 조회 실패",
				"project", Clip(p.ID, 64), "error", err.Error())
			ages[p.ID] = "모름"
			continue
		}
		if at.IsZero() {
			ages[p.ID] = "세션 없음"
			continue
		}
		// ★ 여기서 " 전" 을 덧붙이지 않는다. web.Age 는 이미 "3일 전" 처럼 접미사를
		// 포함해서 낸다(format.go:64) — 밖에서 다시 붙이면 "3일 전 전" 이 된다.
		// 이 페이지의 다른 나이 표시(OpenedAge·Signal·Held 등)도 전부 Age 를 이렇게
		// 그대로 쓴다. 새 관례를 만들지 않는다.
		ages[p.ID] = Age(now.Sub(at))
	}
	return ages
}

// buildPage 는 화면 한 장을 조립한다.
//
// ★ 한 축이 실패해도 나머지는 낸다. 조정 화면이 파생 실패로 통째로 사라지면
// 그 순간 사람이 추측으로 돌아가고, 그 추측이 이 제품이 막으려는 사고의 시작이다.
// 다만 **침묵하지 않는다** — 실패한 축은 패널의 Err 로 화면에 남는다.
func (h *handler) buildPage(ctx context.Context, req pageRequest) Page {
	now := h.now()
	p := Page{
		Now:        now.Format("2006-01-02 15:04:05 MST"),
		RenderedAt: now.Unix(),
		Title:      "flightdeck",
		Refresh:    h.refresh,
		SSEPath:    h.ssePath,
		Notice:     req.notice,
	}

	st := h.svc.Store()
	projects, err := st.ListProjects(ctx)
	if err != nil {
		p.Error = "프로젝트 목록 조회 실패: " + Clip(err.Error(), 400)
		h.log.ErrorContext(ctx, "대시보드 프로젝트 목록 실패", "error", err.Error())
		return p
	}
	p.Projects = projects
	// Nav 는 요청한 프로젝트를 못 찾아 아래에서 일찍 return 하는 갈래(NotFound)에서도
	// 헤더의 프로젝트 줄이 그대로 나가야 하므로 그 판정보다 앞에서 만든다. (프로젝트가
	// 0건인 갈래는 이 자리와 무관하다 — dashboard.gohtml 의 {{if .Projects}} 가드가
	// 그때는 <nav> 를 통째로 지운다.)
	//
	// ★ req.project **원문**이 아니라 pickProject 와 같은 규칙으로 해결한 값을 넘긴다.
	// req.project 는 질의 문자열 그대로라 "/" 로 들어오면 ""(web.go) 인데, 실제로
	// 그려지는 프로젝트는 pickProject 가 정한 id 순 첫 번째다. 원문을 그대로 넘기면
	// 그 프로젝트의 On 이 전부 false 가 되어, 핀이 하나라도 있고 id 순 첫 프로젝트가
	// 핀이 아닐 때 "지금 보드가 그려진 바로 그 프로젝트"가 스스로 접힌 <details> 안에
	// 들어간다 — 화면이 자기 위치를 안 말하는 사고(리뷰 Important-1)다.
	current := req.project
	if current == "" && len(projects) > 0 {
		current = projects[0].ID
	}
	p.Nav = buildProjectNav(projects, current, h.archivedSessionAges(ctx, projects, now))

	// 건강은 프로젝트와 무관하게 항상 낸다 — 프로젝트가 하나도 없을 때가
	// 오히려 "서버는 사는데 아무도 안 붙었다"를 확인해야 하는 순간이다.
	p.Health = h.svc.Health(ctx)
	p.Disk = JudgeDisk(p.Health.DiskKnown, p.Health.DiskFreePct)
	p.HealthLine = healthLine(p.Health)

	if len(projects) == 0 {
		p.Error = "등록된 프로젝트가 없다 — 세션이 한 번 열리면(fd session open) 그때 등록된다."
		return p
	}

	proj, ok := pickProject(projects, req.project)
	if !ok {
		p.Error = fmt.Sprintf("프로젝트 %q 가 등록돼 있지 않다 — 위 목록에서 고르라.", Clip(req.project, 64))
		p.NotFound = true
		return p
	}
	p.Project, p.HasProject = proj, true
	p.Title = "flightdeck — " + proj.ID

	board, boardErr := h.svc.Board(ctx, proj.ID, service.BoardOptions{
		IncludeQueue: true, IncludeNotes: true, NoteLimit: 20,
	})
	if boardErr != nil {
		h.log.ErrorContext(ctx, "보드 조회 실패",
			"project", proj.ID, "error", boardErr.Error())
	}

	dbFresh := DBFreshness(now)
	dbLabel := DerivedLabel(now, dbFresh, 0)
	gitLabel := DerivedLabel(now, board.Freshness, len(board.Failures))

	p.Live = h.livePanel(now, board, boardErr, gitLabel)
	p.Unacked = h.unackedPanel(ctx, proj.ID, dbLabel)
	p.Queue = h.queuePanel(ctx, proj.ID, board, boardErr, dbLabel)
	p.Landing = h.landingPanel(ctx, proj, dbLabel, now)
	p.Blocked = h.blockedPanel(now, board, boardErr, p.Disk, gitLabel)
	p.Search = h.searchPanel(ctx, proj.ID, req.query, dbLabel)

	// 회수 대상은 큐 쪽(살아 있는 선점 행)이 정본이다 — 살아 있는 세션 목록에서 뽑으면
	// **창 밖 세션이 쥔 선점**이 화면에서 통째로 사라지고, 그것이 바로 회수가 가장 필요한 경우다.
	p.Live.Targets = p.Queue.claimTargets(board)
	return p
}

// pageRequest 는 질의 문자열에서 온 것 전부다.
type pageRequest struct {
	project string
	query   string
	notice  string
}

// pickProject 는 요청한 프로젝트를 고른다. 없으면 첫 번째다.
// 요청했는데 없으면 **조용히 첫 번째로 넘기지 않는다**(ok=false) — 넘기면
// 사용자는 자기가 다른 프로젝트를 보고 있다는 것을 모른 채 판단한다.
func pickProject(projects []model.Project, want string) (model.Project, bool) {
	if want == "" {
		return projects[0], true
	}
	for _, p := range projects {
		if p.ID == want {
			return p, true
		}
	}
	return model.Project{}, false
}

func healthLine(hz service.Health) string {
	s := "api v" + hz.APIVersion
	if hz.DBOK {
		s += " · DB 정상"
	} else {
		s += " · DB 실패: " + Clip(hz.DBError, 200)
	}
	return s
}

// ─────────────────────────────────────────────────────────────────────────────
// ① 지금 — 잡혀 있는 작업(선점을 든 카드만)
//
// ★ 이 섹션은 "누가 살아 있나"에 **답하지 않는다.** "어느 작업이 잡혀 있나"에 답한다.
// 선점이 필터고 창은 아니다 — 창을 함께 걸면 회수가 가장 필요한 카드가 먼저 사라진다.
// ─────────────────────────────────────────────────────────────────────────────

func (h *handler) livePanel(now time.Time, board service.BoardView, boardErr error, label string) LivePanel {
	pan := LivePanel{Panel: Panel{Derived: label, Fail: board.Failures}}
	pan.Window = "창 " + span(board.Window)
	if boardErr != nil {
		pan.Err = "보드 조회 실패: " + Clip(boardErr.Error(), 400)
		pan.Empty = "세션 표를 못 만들었다 — 위 사유를 보라. 빈 표가 아니라 실패다."
		return pan
	}
	// ★★ 선점 필터. 이 섹션이 답하는 질문은 "누가 살아 있나"가 아니라
	// **"어느 작업이 잡혀 있나"** 다. 선점 없는 카드는 안 낸다 — 자기 카드도 예외가 없다.
	//
	// ★ 필터가 **여기**여야 하는 이유. board.Sessions 를 줄이면 같은 슬라이스를 먹는
	// 겹침 꼬리(judge.OverlapsWithLive)·cc 표류 배너(DriftedTwins)·⑤ 막힘 패널·
	// 회수 폼 대상·dashboard.json 이 함께 죽는다. 그중 겹침은 **선점 없이 편집하는
	// 세션**을 잡는 축이라, 정확히 이번에 숨기려는 그 세션이 남의 화면에서도 사라진다 —
	// 조용한 오탐이 아니라 조용한 미탐이다. sessionRow 도 ⑤가 함께 쓰므로 거기도 아니다.
	folded := 0
	for _, c := range board.Sessions {
		if len(c.View.Claims) == 0 {
			folded++
			continue
		}
		pan.Sessions = append(pan.Sessions, sessionRow(now, c))
	}
	// ★ 창 밖인데 항목을 쥔 세션. **창을 이 섹션에 걸지 않는다** — 걸면 회수가 가장
	// 필요한 카드(오래 조용한데 쥐고 있는 것)가 정확히 창 때문에 사라진다.
	// 이 줄들은 git 파생을 안 읽었다(Derived=false) — 창 밖까지 파생하면 세션 수만큼 터진다.
	for _, v := range board.OutsideClaims {
		pan.Sessions = append(pan.Sessions, outsideClaimRow(now, v))
	}
	if len(pan.Sessions) == 0 {
		// ★ 0건을 빈칸으로 두지 않는다. **그리고 이제 0건은 정상 상태다** —
		//   아무도 항목을 안 쥐고 있다는 뜻이지 서버가 죽었다는 뜻이 아니다.
		pan.Empty = "잡혀 있는 작업 0건 — 지금 아무 세션도 큐 항목을 쥐고 있지 않다. 서버 장애가 아니다."
	}
	if folded > 0 {
		pan.Folded = fmt.Sprintf(
			"선점 없는 세션 %d건은 안 낸다 — 겹침 처방은 그 세션들도 그대로 본다.", folded)
	}
	// 창 밖으로 잘린 것을 침묵시키지 않는다. 창은 표시 구간이지 생존 판정이 아니다(설계 §4) —
	// MCP board 는 이미 말하는데 이 화면만 빠뜨리면 같은 사실이 표면마다 다르게 읽힌다.
	if board.OutOfWindow > 0 {
		pan.OutOfWindow = fmt.Sprintf("창 밖 %d건", board.OutOfWindow)
		if !board.OldestOutside.IsZero() {
			pan.OutOfWindow += fmt.Sprintf(" (가장 오래된 신호 %s)", Age(now.Sub(board.OldestOutside)))
		}
		pan.OutOfWindow += " — 창은 표시 구간이지 생존 판정이 아니다."
	}
	return pan
}

func sessionRow(now time.Time, c service.SessionCard) SessionRow {
	v := c.View
	r := SessionRow{
		ID:          v.Session.ID,
		Short:       short(v.Session.ID),
		Label:       v.Session.Label,
		State:       string(v.Session.State),
		BlockedWhy:  v.Session.BlockedWhy,
		Worktree:    v.Session.Worktree,
		Signals:     SignalAges(now, v.Signals),
		Claims:      v.Claims,
		Derived:     true,
		DeriveError: c.DeriveError,
		IsSelf:      c.IsSelf,
		HasPaths:    v.HasFootprint,
	}
	if !v.Session.OpenedAt.IsZero() {
		r.OpenedAge = Age(now.Sub(v.Session.OpenedAt))
	}
	if c.BranchKnown {
		r.Branch = v.Branch
		if r.Branch == "" {
			r.BranchNote = "브랜치 없음(detached HEAD)"
		}
	} else {
		r.BranchNote = "브랜치 못 읽음"
	}
	if c.AheadKnown {
		r.Ahead = fmt.Sprintf("+%d", v.AheadMain)
	} else {
		r.AheadNote = "ahead 못 읽음"
	}
	// ★ 발자국 없음을 빈칸으로 두지 않는다(설계 §5). 그 세션은 경로 축에서 아무도 안 막고,
	//   **안 막는다는 사실이 화면에 있어야** 한다.
	if !v.HasFootprint {
		r.Footprint = "발자국 없음"
	} else {
		r.Footprint = fmt.Sprintf("경로 %d건", len(v.Paths))
		const cap = 12
		if len(v.Paths) > cap {
			r.Paths, r.MorePaths = v.Paths[:cap], len(v.Paths)-cap
		} else {
			r.Paths = v.Paths
		}
	}
	if v.LastNote != nil {
		n := noteRow(now, *v.LastNote)
		r.LastNote = &n
	}
	r.HasActivity, r.Activity = ActivityOf(now, v.Signals)
	return r
}

// outsideClaimRow 는 **창 밖인데 항목을 쥔 세션** 한 줄이다.
//
// ★ 카드가 아니라 줄이다. git 파생(브랜치·ahead·미커밋)이 없다 — 창 밖까지 파생하면
// 카드당 git 호출 1~4회가 세션 수만큼 터진다(gitreader 에 캐시가 없다). 그래서
// Derived=false 로 두고 **화면이 "이 축은 안 읽었다"를 말한다.** 0값과 미관측을
// 뭉개지 않는 것이 이 패키지의 규율이다.
//
// 그래도 반드시 낸다: 이 줄이 없으면 회수가 가장 필요한 카드가 정확히 창 때문에
// 사라진다(실측: 활동 709분 전 세션이 항목 하나를 12시간째 쥐고 있었다).
func outsideClaimRow(now time.Time, v model.SessionView) SessionRow {
	r := SessionRow{
		ID:         v.Session.ID,
		Short:      short(v.Session.ID),
		Label:      v.Session.Label,
		State:      string(v.Session.State),
		BlockedWhy: v.Session.BlockedWhy,
		Worktree:   v.Session.Worktree,
		Signals:    SignalAges(now, v.Signals),
		Claims:     v.Claims,
		BranchNote: "창 밖이라 파생을 안 읽었다",
		AheadNote:  "안 읽음",
		Footprint:  "안 읽음(창 밖)",
		Derived:    false,
	}
	if !v.Session.OpenedAt.IsZero() {
		r.OpenedAge = Age(now.Sub(v.Session.OpenedAt))
	}
	r.HasActivity, r.Activity = ActivityOf(now, v.Signals)
	return r
}

// ─────────────────────────────────────────────────────────────────────────────
// ② 미확인 결과 (Tier B)
// ─────────────────────────────────────────────────────────────────────────────

func (h *handler) unackedPanel(ctx context.Context, project, label string) UnackedPanel {
	pan := UnackedPanel{Panel: Panel{Derived: label}}
	pan.Tier = "Tier B — 러너가 아직 없다. 그래도 잡 표를 실제로 조회한다(안 보면 " +
		"\"결과가 없다\"와 \"이 화면이 그 축을 안 본다\"가 구분되지 않는다)."
	jobs, err := unackedJobs(ctx, h.svc.Store().DB(), project, 50)
	if err != nil {
		pan.Err = Clip(err.Error(), 400)
		h.log.ErrorContext(ctx, "미확인 잡 조회 실패", "project", project, "error", err.Error())
		return pan
	}
	for _, j := range jobs {
		row := UnackedJobRow{
			ID: j.ID, Kind: j.Kind, ItemID: j.ItemID, State: j.State,
			FailKind: j.FailKind, LogTail: Clip(j.LogTail, 400),
		}
		if !j.EndedAt.IsZero() {
			row.Ended = j.EndedAt.Format("01-02 15:04")
		}
		pan.Jobs = append(pan.Jobs, row)
	}
	if len(pan.Jobs) == 0 {
		pan.Empty = "미확인 결과 0건 — 잡 표를 조회했고 확인 대기 중인 행이 없다."
	}
	return pan
}

// ─────────────────────────────────────────────────────────────────────────────
// ③ 큐 — 항목 · 탈락 사유 분포 · 의존
// ─────────────────────────────────────────────────────────────────────────────

func (h *handler) queuePanel(ctx context.Context, project string, board service.BoardView,
	boardErr error, label string) QueuePanel {

	pan := QueuePanel{Panel: Panel{Derived: label}}
	st := h.svc.Store()

	if boardErr != nil {
		pan.Err = "열린 항목을 못 읽었다: " + Clip(boardErr.Error(), 400)
	}
	// 종료 선언 축은 **여기서 한 번만** 읽는다. 줄마다 읽으면 큐 길이만큼 원장을 긁는다.
	//
	// ★ 실패를 pan.Err 에 담지 않는다. 이유 둘 —
	//   ① pan.Err 은 ③의 머리말이라 **회수 폼(섹션 ①)에는 원리적으로 안 닿는다.** 이 축은
	//      그 폼의 <option> 에도 실리는데, 배너로만 두면 되돌릴 수 없는 그 한 줄이 침묵한 채
	//      "선언 없음"과 구분되지 않는다.
	//   ② pan.Err 이 차면 아래 `len(pan.Items) == 0 && pan.Err == ""` 가 막혀 "큐가 비었다"라는
	//      **참인 문장**이 사라진다. 원장 한 축을 못 읽은 것이 큐가 비었다는 사실까지 지우면
	//      안 된다.
	// 대신 줄마다 센티널(CloseDeclUnread)을 싣고 사유 전문은 서버 로그에 남긴다.
	decls, declsRead := h.closeDeclarations(ctx, st, project)

	for _, it := range board.OpenItems {
		pan.Items = append(pan.Items, h.itemRow(ctx, st, it, "", time.Time{}, decls, declsRead))
	}

	// 선점된 항목은 ListOpen 에 안 들어온다(state='claimed'). 빼면 진행 중인 일이
	// 큐 화면에서 통째로 사라지므로 선점 행을 직접 읽어 붙인다.
	holds, err := claimHolders(ctx, st.DB(), project)
	if err != nil {
		pan.Err = joinErr(pan.Err, "선점 목록을 못 읽었다: "+Clip(err.Error(), 400))
		h.log.ErrorContext(ctx, "선점 목록 조회 실패", "project", project, "error", err.Error())
	}
	for _, hd := range holds {
		it, err := st.GetItem(ctx, project, hd.ItemID)
		if err != nil {
			pan.Err = joinErr(pan.Err, fmt.Sprintf("선점된 항목 %s 를 못 읽었다: %s",
				Clip(hd.ItemID, 64), Clip(err.Error(), 200)))
			continue
		}
		pan.Items = append(pan.Items, h.itemRow(ctx, st, it, hd.SessionID, hd.At, decls, declsRead))
	}

	for _, it := range pan.Items {
		pan.Targets = append(pan.Targets, it)
	}
	if len(pan.Items) == 0 && pan.Err == "" {
		pan.Empty = "큐가 비었다 — 열린 항목도 선점된 항목도 없다."
	}

	// 탈락 사유 분포. 없으면 "분포 없음"이 아니라 **판정 기록이 없다**고 쓴다 —
	// 둘은 다른 사실이고, 뭉개면 큐가 다시 블랙박스가 된다.
	evals, err := pickEvals(ctx, st.DB(), project, 200)
	if err != nil {
		pan.StatsErr = Clip(err.Error(), 400)
		h.log.ErrorContext(ctx, "큐 판정 기록 조회 실패", "project", project, "error", err.Error())
		return pan
	}
	pan.Stats = RejectionDistribution(evals)
	if pan.Stats.Evals == 0 {
		pan.StatsErr = "큐 판정 기록이 0건이다 — 아직 아무도 인자 없이 pick 을 부르지 않았다(지정 선점은 이 원장에 안 남는다)."
	} else {
		pan.Window = fmt.Sprintf("최근 판정 %d회(%s ~ %s)", pan.Stats.Evals,
			pan.Stats.Since.Format("01-02 15:04"), pan.Stats.Until.Format("01-02 15:04"))
	}
	return pan
}

// closeDeclarations 는 종료 선언 축을 한 번 읽는다.
//
// 두 번째 반환값이 false 면 **못 읽은 것**이고, 그때 화면은 0건이 아니라 센티널을 낸다.
// (값, bool) 두 반환값을 고른 이유는 nil 맵이 Go 에서 zero 를 내기 때문이다 — nil 을
// "안 읽음"으로 쓰면 빈 맵과 바이트 단위로 같은 출력이 되어 가를 관측점이 없어진다.
// service/pick.go 가 SiblingIndex 에서 같은 판단을 이미 내렸다.
func (h *handler) closeDeclarations(ctx context.Context, st *store.Store,
	project string) (map[string]model.CloseDeclaration, bool) {

	d, err := st.CloseDeclarationsByItem(ctx, project)
	if err != nil {
		// 삼키지 않는다. 다만 이 한 축 때문에 큐 표를 통째로 버리지도 않는다 —
		// 역인덱스 실패가 항목 줄을 안 버리는 것과 같은 자리다.
		h.log.WarnContext(ctx, "종료 선언 축 조회 실패",
			"project", Clip(project, 64), "error", err.Error())
		return nil, false
	}
	return d, true
}

func (h *handler) itemRow(ctx context.Context, st *store.Store, it model.Item,
	holder string, since time.Time,
	decls map[string]model.CloseDeclaration, declsRead bool) ItemRow {

	r := ItemRow{
		ID: it.ID, Title: it.Title, Body: Clip(it.Body, 300),
		State: string(it.State), Paths: it.Paths, Labels: it.Labels, Holder: holder,
		// ★ 앵커는 여기서 건다. store 는 원자료만 내고 "지웠다 다시 만든 id 의 옛 선언이
		// 그대로 들어 있다"고 doc 에 적어 뒀다 — 그 판정은 items 를 손에 쥔 이쪽 일이다.
		CloseDeclared: CloseDeclaredLabel(decls[it.ID], declsRead, it.CreatedAt),
	}
	if !since.IsZero() {
		r.Since = since.Format("01-02 15:04")
	}
	for _, a := range it.After {
		r.After = append(r.After, AfterLabel(a))
	}
	n, err := st.Dependents(ctx, it.Project, it.ID)
	if err != nil {
		// 삼키지 않는다. 다만 이 한 축 때문에 항목 줄을 통째로 버리지도 않는다.
		h.log.WarnContext(ctx, "역인덱스 조회 실패",
			"project", it.Project, "item", Clip(it.ID, 64), "error", err.Error())
		r.Dependents = -1
	} else {
		r.Dependents = n
	}
	return r
}

// claimTargets 는 회수 폼의 선택지를 만든다.
func (q QueuePanel) claimTargets(board service.BoardView) []ClaimTarget {
	live := map[string]string{}
	for _, c := range board.Sessions {
		label := c.View.Session.Label
		if label == "" {
			label = short(c.View.Session.ID)
		}
		live[c.View.Session.ID] = label
	}
	var out []ClaimTarget
	for _, it := range q.Items {
		if it.Holder == "" {
			continue
		}
		// ★ 여기서 다시 계산하지 않는다. ItemRow 가 정본이고 이 줄은 옮기기만 한다 —
		//   두 표면이 같은 이름으로 다른 사실을 내는 것이 이 항목이 고치는 병이다.
		t := ClaimTarget{ItemID: it.ID, Title: it.Title, Holder: it.Holder, Since: it.Since,
			Live: "창 밖 세션", CloseDeclared: it.CloseDeclared}
		if label, ok := live[it.Holder]; ok {
			t.Holder, t.Live = label, "창 안 세션"
		}
		out = append(out, t)
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// ④ 랜딩 이력 (Tier B) + 스냅숏
// ─────────────────────────────────────────────────────────────────────────────

func (h *handler) landingPanel(ctx context.Context, proj model.Project, label string, now time.Time) LandingPanel {
	pan := LandingPanel{Panel: Panel{Derived: label}}
	pan.Tier = "Tier B — landed_ref 는 **러너가 실제로 fast-forward 한 sha 만** 들어간다. " +
		"러너가 없는 지금은 비어 있고, 그 자리를 지금 HEAD 로 메우지 않는다."

	st := h.svc.Store()
	items, err := closedItems(ctx, st.DB(), proj.ID, 30)
	if err != nil {
		pan.Err = Clip(err.Error(), 400)
		h.log.ErrorContext(ctx, "종료된 항목 조회 실패", "project", proj.ID, "error", err.Error())
	}
	for _, it := range items {
		row := ItemRow{ID: it.ID, Title: it.Title, State: string(it.State), Body: Clip(it.CloseReason, 200)}
		if it.ClosedAt != nil {
			row.Since = it.ClosedAt.Format("01-02 15:04")
		}
		if it.LandedRef != "" {
			row.Holder = short(it.LandedRef) // 표의 "랜딩 sha" 칸
		}
		pan.Closed = append(pan.Closed, row)
	}
	if len(pan.Closed) == 0 && pan.Err == "" {
		pan.Empty = "종료된 항목 0건 — 아직 아무 항목도 끝나거나 폐기되지 않았다."
	}

	// 현재 입력 = 기본 브랜치의 마지막 관측 sha. 보드가 방금 갱신한 값이다.
	// 못 읽으면 **지어내지 않는다** — 스냅숏 판정이 unknown 으로 떨어진다.
	current := ""
	if r, err := st.GetRefState(ctx, proj.ID, proj.DefaultBranch); err == nil {
		current = r.SHA
		pan.Current = fmt.Sprintf("현재 입력: %s@%s (%s 관측)", proj.DefaultBranch, short(r.SHA), Age(now.Sub(r.At)))
	} else if errors.Is(err, store.ErrNotFound) {
		pan.Current = "현재 입력: 기본 브랜치 " + proj.DefaultBranch + " 의 관측이 아직 없다 — 낡음 대조를 못 한다"
	} else {
		pan.Current = "현재 입력: 못 읽었다 — " + Clip(err.Error(), 200)
		h.log.ErrorContext(ctx, "기본 브랜치 관측 조회 실패",
			"project", proj.ID, "error", err.Error())
	}

	sns, err := st.ListSnapshots(ctx, proj.ID)
	if err != nil {
		pan.SnapErr = Clip(err.Error(), 400)
		h.log.ErrorContext(ctx, "스냅숏 조회 실패", "project", proj.ID, "error", err.Error())
		return pan
	}
	for _, sn := range sns {
		pan.Snapshots = append(pan.Snapshots, SnapshotRow{
			Key: sn.Key, Value: sn.Value, Method: string(sn.Method),
			Evidence: Clip(sn.Evidence, 300),
			Computed: sn.ComputedAt.Format("01-02 15:04"),
			Verdict:  JudgeSnapshot(sn.InputDigest, current),
			Progress: ParseProgress(sn.Value),
		})
	}
	if len(pan.Snapshots) == 0 && pan.SnapErr == "" {
		pan.SnapErr = "스냅숏 0건 — 전수 판정 수치가 아직 하나도 보관되지 않았다."
	}
	h.fillLane(ctx, &pan, proj.ID, now)
	return pan
}

// fillLane 은 랜딩 줄을 패널에 채운다.
//
// ★ **조회는 service.LandingLane 하나로 한다.** query.go 에 생 SQL 을 두 번째로
// 만들면 판정이 두 자리에 생기고, 한쪽만 고치는 순간 화면과 보드가 조용히 어긋난다
// (board.go 가 같은 규율을 적어 뒀다). 그 함수는 점유자·줄의 정합 재확인까지 한다.
//
// ★ **생존 창으로 거르지 않는다.** 창 밖 세션이 맨 앞에서 막고 있는 상황이야말로
// 사람이 봐야 하는 상황이고, 거르면 화면이 "줄이 비었는데 아무도 못 잡는다"가 된다.
func (h *handler) fillLane(ctx context.Context, pan *LandingPanel, project string, now time.Time) {
	lane, err := h.svc.LandingLane(ctx, project)
	if err != nil {
		// 못 읽은 것을 0건으로 적지 않는다. 빈 표가 아니라 실패다.
		pan.LaneErr = "랜딩 줄을 못 읽었다: " + Clip(err.Error(), 400)
		h.log.ErrorContext(ctx, "랜딩 줄 조회 실패", "project", project, "error", err.Error())
		return
	}

	holderSeen := false
	for _, e := range lane.Entries {
		row := LaneRow{
			RowID:   e.RowID,
			Session: e.SessionID,
			Waiting: Age(now.Sub(e.EnqueuedAt)),
			Signal:  signalAge(now, e.LastSignalAt),
		}
		if lane.Holder != nil && lane.Holder.SessionID == e.SessionID {
			row.Holder = true
			row.Held = Age(now.Sub(lane.Holder.AcquiredAt))
			holderSeen = true
		}
		pan.Lane = append(pan.Lane, row)
	}

	// 점유자가 줄에 없다 — 정합 어긋남이다. **행을 지우지 않고 낸다.**
	// 이 행이 화면에서 사라지면 "레인은 물렸는데 화면은 비었다"가 되고,
	// 그때가 정확히 사람이 회수해야 하는 순간이다.
	if lane.Holder != nil && !holderSeen {
		pan.Lane = append(pan.Lane, LaneRow{
			Session: lane.Holder.SessionID,
			Waiting: "", // 줄 행이 없으므로 대기 경과라는 것이 없다
			Signal:  signalAge(now, lane.Holder.LastSignalAt),
			Holder:  true,
			Held:    Age(now.Sub(lane.Holder.AcquiredAt)),
			Missing: true,
		})
		pan.LaneWarn = "정합 어긋남 — 레인을 쥔 세션의 줄 행이 없다. " +
			"회수는 줄 행 번호로 하는데 그 번호가 없으므로, 이 자리는 CLI 로도 화면으로도 못 푼다 — " +
			"점유자가 land 로 빠지거나 서버가 다시 읽어 스스로 아무는 것이 정상 경로다."
	}

	if len(pan.Lane) == 0 {
		pan.LaneEmpty = "줄이 비었다 — 질의는 돌았고 아무도 안 섰다."
	}
}

// signalAge 는 마지막 신호의 나이다. nil 이면 빈 문자열이다.
//
// ★ **"관측 실패"와 "신호가 없다"를 화면에서 가르지 않는다.** service.LandingLane 이
// 그 자리에서 이미 그렇게 정했다(둘 다 nil 로 오고 실패 사유는 서버 WARN 에 남는다).
// 화면이 그 둘을 갈라 적으려면 없는 정보를 지어내야 한다. 이 축을 반드시 봐야 하는
// 곳은 불변으로 남는 판단 본문이고, 그쪽(ReleaseLaneRow)은 두 경우를 다른 문장으로 적는다.
func signalAge(now time.Time, at *time.Time) string {
	if at == nil {
		return ""
	}
	return Age(now.Sub(*at))
}

// ─────────────────────────────────────────────────────────────────────────────
// ⑤ 막힘 — 닫히지 않은 blocked + 자원 임계
// ─────────────────────────────────────────────────────────────────────────────

func (h *handler) blockedPanel(now time.Time, board service.BoardView, boardErr error,
	disk ResourceAlert, label string) BlockedPanel {

	pan := BlockedPanel{Panel: Panel{Derived: label, Fail: board.Failures}, Disk: disk}
	if boardErr != nil {
		pan.Err = "막힘·자원을 못 읽었다: " + Clip(boardErr.Error(), 400)
		return pan
	}
	for _, j := range board.Blocked {
		pan.Notes = append(pan.Notes, noteRow(now, j))
	}
	for _, j := range board.Asks {
		pan.Asks = append(pan.Asks, noteRow(now, j))
	}
	for _, c := range board.Sessions {
		if c.View.Session.State == model.SessionBlocked {
			pan.Sessions = append(pan.Sessions, sessionRow(now, c))
		}
	}
	for _, hd := range board.Held {
		holder := hd.SessionID
		if holder == "" {
			holder = "잡 " + hd.JobID
		}
		pan.Held = append(pan.Held, HoldRow{
			Resource: hd.Resource, Holder: holder,
			Since: hd.AcquiredAt.Format("01-02 15:04"),
			Age:   Age(now.Sub(hd.AcquiredAt)),
		})
	}
	if len(pan.Notes) == 0 && len(pan.Asks) == 0 && len(pan.Sessions) == 0 && len(pan.Held) == 0 {
		pan.Empty = "막힘으로 남은 판단도, blocked 세션도, 쥐어진 자원도 없다."
	}
	return pan
}

// ─────────────────────────────────────────────────────────────────────────────
// ⑥ 판단 검색
// ─────────────────────────────────────────────────────────────────────────────

func (h *handler) searchPanel(ctx context.Context, project, q, label string) SearchPanel {
	pan := SearchPanel{Panel: Panel{Derived: label}, Query: q}
	st := h.svc.Store()
	now := h.now()

	if q == "" {
		pan.Mode = "최근 판단(검색어 없음)"
		var all []model.Judgment
		for _, kind := range []model.JudgmentKind{model.JudgmentHandoff, model.JudgmentDecision} {
			js, err := st.ListJudgmentsByKind(ctx, project, kind, 10)
			if err != nil {
				pan.Err = joinErr(pan.Err, Clip(err.Error(), 400))
				h.log.ErrorContext(ctx, "판단 목록 조회 실패",
					"project", project, "error", err.Error())
				continue
			}
			all = append(all, js...)
		}
		sort.Slice(all, func(i, j int) bool { return all[i].At.After(all[j].At) })
		if len(all) > 10 {
			all = all[:10]
		}
		for _, j := range all {
			pan.Results = append(pan.Results, noteRow(now, j))
		}
		if len(pan.Results) == 0 && pan.Err == "" {
			pan.Empty = "핸드오프·결정 판단이 아직 0건이다."
		}
		return pan
	}

	pan.Mode = fmt.Sprintf("검색 %q", q)
	js, err := st.SearchJudgments(ctx, project, q, 30)
	if err != nil {
		pan.Err = Clip(err.Error(), 400)
		h.log.ErrorContext(ctx, "판단 검색 실패",
			"project", project, "q", Clip(q, 120), "error", err.Error())
		return pan
	}
	for _, j := range js {
		pan.Results = append(pan.Results, noteRow(now, j))
	}
	if len(pan.Results) == 0 {
		pan.Empty = "검색 결과 0건 — 질의는 정상으로 돌았고 맞는 판단이 없다."
	}
	return pan
}

func noteRow(now time.Time, j model.Judgment) NoteRow {
	r := NoteRow{
		ID: j.ID, Kind: string(j.Kind), Title: Clip(j.Title, 200),
		Body: Clip(j.Body, 600), Session: short(j.SessionID),
	}
	if !j.At.IsZero() {
		r.At, r.Age = j.At.Format("01-02 15:04"), Age(now.Sub(j.At))
	}
	for _, l := range j.Links {
		r.Links = append(r.Links, l.TargetKind+"/"+l.TargetID)
	}
	return r
}

// ─────────────────────────────────────────────────────────────────────────────

// holderRow 는 살아 있는 선점 한 행이다.
type holderRow struct {
	ItemID    string
	SessionID string
	At        time.Time
}

// claimHolders 는 반납되지 않은 선점을 읽는다.
//
// ★ 살아 있는 세션 목록에서 뽑지 않는다. 창 밖 세션이 쥔 선점이야말로 회수가 필요한 경우인데,
// 세션에서 뽑으면 그것이 화면에서 통째로 사라진다.
func claimHolders(ctx context.Context, db *sql.DB, project string) ([]holderRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT item_id, session_id, at FROM claim
		WHERE project = ? AND released_at IS NULL ORDER BY at`, project)
	if err != nil {
		return nil, fmt.Errorf("선점 조회 실패(project=%q): %w", Clip(project, 64), err)
	}
	defer rows.Close()

	var out []holderRow
	for rows.Next() {
		var (
			r  holderRow
			at string
		)
		if err := rows.Scan(&r.ItemID, &r.SessionID, &at); err != nil {
			return nil, fmt.Errorf("선점 행 해석 실패: %w", err)
		}
		if r.At, err = parseStamp(at); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("선점 목록 순회 실패: %w", err)
	}
	return out, nil
}

func joinErr(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + " · " + b
}
