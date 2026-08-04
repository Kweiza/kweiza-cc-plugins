package service

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// 보드 — "지금 누가 무엇을 만지고 있나".
//
// ★ 이 파일은 "죽었다"를 만들지 않는다. 신호 넷의 시각을 그대로 담고 나이 계산은 표시 계층 몫이다.
// 불리언을 만드는 순간 그것이 회수·회피·탈락 셋의 상류가 되고, 그 판정은 실측에서 두 번 틀렸다 —
// 죽었다고 판정한 세션이 그 뒤 6커밋을 랜딩했고, 419분 무갱신으로 표시된 세션이 실제로는 17초 전이었다.

// BoardOptions 는 보드 조회의 선택 인자다.
type BoardOptions struct {
	// Window 는 "이 구간 안에 신호가 있었나"로 자르는 지점이다. 0 이면 DefaultLiveWindow.
	// **생존 판정이 아니다** — 결과에는 각 신호의 시각이 그대로 실린다.
	Window time.Duration
	// Self 는 요청한 세션 id 다. 표시 전용이고 **어떤 배제 판정에도 안 쓴다**.
	Self string
	// IncludeQueue 는 열린 항목을 함께 낸다.
	IncludeQueue bool
	// IncludeNotes 는 막힘·요청 판단을 함께 낸다.
	IncludeNotes bool
	// NoteLimit 는 IncludeNotes 일 때 종류별로 가져올 판단 수다. 0 이면 20.
	NoteLimit int
}

// SessionCard 는 세션 하나의 보드 표시분이다.
//
// BranchKnown·AheadKnown 이 있는 이유: 둘 다 0값이 유효한 값이라
// **"못 읽었다"와 "0이다"가 구분되지 않기 때문이다.** 그 구분이 없으면
// git 이 죽은 화면과 브랜치가 main 과 같은 화면이 똑같이 보인다.
type SessionCard struct {
	View        model.SessionView `json:"view"`
	BranchKnown bool              `json:"branch_known"`
	AheadKnown  bool              `json:"ahead_known"`
	DeriveError string            `json:"derive_error,omitempty"`
	IsSelf      bool              `json:"is_self"`
}

// BoardView 는 보드 한 장이다.
type BoardView struct {
	Project   model.Project        `json:"project"`
	At        time.Time            `json:"at"`
	Window    time.Duration        `json:"window"`
	Sessions  []SessionCard        `json:"sessions"`
	OpenItems []model.Item         `json:"open_items,omitempty"`
	Blocked   []model.Judgment     `json:"blocked,omitempty"`
	Asks      []model.Judgment     `json:"asks,omitempty"`
	Held      []model.ResourceHold `json:"held,omitempty"`
	// OutOfWindow 는 창 밖이라 카드가 안 나간 세션 수다. **화면이 반드시 말한다** —
	// 침묵하면 "그런 세션이 없다"와 "안 보여 준다"가 구분되지 않는다.
	OutOfWindow int `json:"out_of_window,omitempty"`
	// OldestOutside 는 창 밖 세션 중 가장 오래된 마지막 신호 시각이다.
	OldestOutside time.Time `json:"oldest_outside,omitempty"`
	Derived
}

// Board 는 살아 있는 세션과 그들이 만지는 경로를 낸다.
//
// git 파생이 통째로 실패해도 **응답은 낸다**. 조정(누가 살아 있나·누가 무엇을 선점했나)은
// DB 만으로 완결되고, 그것이 이 도구의 존재 이유이기 때문이다.
// 다만 침묵하지 않는다 — Freshness.Stale 과 Failures 가 "이 값은 못 읽었다"를 표면에 낸다.
func (s *Service) Board(ctx context.Context, project string, opt BoardOptions) (BoardView, error) {
	now := s.now()
	d := &derive{}

	proj, err := s.st.GetProject(ctx, project)
	if err != nil {
		// 프로젝트 미등록은 파생 실패가 아니라 설정 오류다. 접지 않고 그대로 올린다.
		return BoardView{}, err
	}

	window := opt.Window
	if window <= 0 {
		window = s.window
	}
	cards, err := s.sessionCards(ctx, proj, s.cut(now, window), opt.Self, d)
	if err != nil {
		return BoardView{}, err
	}

	view := BoardView{Project: proj, At: now, Window: window, Sessions: cards}

	// 창 밖 건수 — 카드를 안 만든다. 세는 것만 한다(파생 비용을 안 늘린다).
	if all, aerr := s.st.ListLive(ctx, proj.ID, time.Time{}); aerr != nil {
		d.fail("out-of-window", aerr) // 못 세면 침묵하지 않고 파생 실패로 남긴다
	} else {
		view.OutOfWindow = len(all) - len(cards)
		cut := s.cut(now, window)
		for _, v := range all {
			for _, at := range v.Signals {
				if at.Before(cut) && (view.OldestOutside.IsZero() || at.Before(view.OldestOutside)) {
					view.OldestOutside = at
				}
			}
		}
	}

	if opt.IncludeQueue {
		items, err := s.st.ListOpen(ctx, project)
		if err != nil {
			return BoardView{}, err
		}
		view.OpenItems = items
	}
	if opt.IncludeNotes {
		limit := opt.NoteLimit
		if limit <= 0 {
			limit = 20
		}
		// ★ 여기서도 **같은 함수**를 부른다. 앞선 판은 이 자리가 직접 질의해서
		//   RecentNotes 만 고쳤을 때 훅으로 나가는 경로가 그대로 넘쳤다 —
		//   판정을 두 자리에 두면 한쪽만 고치는 순간 조용히 어긋난다는 것을
		//   이 파일이 스스로 실증한 셈이다.
		if view.Blocked, err = s.liveNotesOfKind(ctx, project, model.JudgmentBlocked, limit); err != nil {
			return BoardView{}, err
		}
		if view.Asks, err = s.liveNotesOfKind(ctx, project, model.JudgmentAsk, limit); err != nil {
			return BoardView{}, err
		}
	}
	if view.Held, err = s.st.ListHeld(ctx, project); err != nil {
		return BoardView{}, err
	}

	view.Derived = d.result(now)
	s.log.InfoContext(ctx, "보드 조회",
		"project", project, "count", len(cards), "stale", view.Freshness.Stale,
		"skipped", len(view.Failures))
	return view, nil
}

// RecentNotes 는 프로젝트의 최근 ask·blocked 판단이다(종류별 limit 건).
//
// Board 를 통째로 부르지 않고 이 축만 여는 이유: 이것을 부르는 자리(MCP 응답 꼬리)는
// 세션 카드도 큐도 안 쓰는데, Board 는 그것을 내려고 git 을 여러 번 읽는다.
// 꼬리 하나 때문에 매 도구 호출이 저장소 전체를 훑게 두면 첫 명령이 그만큼 느려진다.
//
// **누가 남겼는지로 거르지 않는다.** "내가 쓴 것은 알림이 아니다"는 표시 계층의 판정이고,
// 그 축을 여기서 접으면 같은 목록을 다른 '나'로 다시 볼 수 없다.
// liveNotesOfKind 는 **지금 살아 있는 세션이 남긴** 그 종류의 판단이다.
//
// ★ 알림이 답하는 질문은 "지금 누가 나에게 무엇을 요청했나"이지 "무슨 일이 있었나"가 아니다.
// 생존 범위가 없으면 이관 직후 옛 판단 수십 건이 전부 "미확인"으로 잡혀 매 프롬프트에 실린다 —
// 실제로 그렇게 났다(ask 36 + blocked 36, 제목이 옛 절 이름이라 **전부 같은 문구**였다).
// 그러면 이 채널은 첫날부터 노이즈가 되고, 노이즈가 된 채널은 아무도 안 읽는다.
// 지난 일은 사라지지 않는다 — 판단 검색(설계 §6 ⑥)이 그 자리다.
func (s *Service) liveNotesOfKind(ctx context.Context, project string,
	kind model.JudgmentKind, limit int) ([]model.Judgment, error) {
	if limit <= 0 {
		limit = 20
	}
	live, err := s.st.ListLive(ctx, project, s.now().Add(-s.window))
	if err != nil {
		return nil, err
	}
	alive := make(map[string]bool, len(live))
	for _, sess := range live {
		alive[sess.Session.ID] = true
	}
	// 살아 있는 것만 남기므로 넉넉히 읽고 거른다.
	js, err := s.st.ListJudgmentsByKind(ctx, project, kind, limit*4)
	if err != nil {
		return nil, err
	}
	out := make([]model.Judgment, 0, limit)
	for _, j := range js {
		if !alive[j.SessionID] {
			continue
		}
		if out = append(out, j); len(out) >= limit {
			break
		}
	}
	return out, nil
}

// RecentNotes 는 프로젝트의 최근 ask·blocked 판단이다(종류별 limit 건).
func (s *Service) RecentNotes(ctx context.Context, project string, limit int) ([]model.Judgment, error) {
	if strings.TrimSpace(project) == "" {
		return nil, nil
	}
	var out []model.Judgment
	for _, k := range []model.JudgmentKind{model.JudgmentAsk, model.JudgmentBlocked} {
		js, err := s.liveNotesOfKind(ctx, project, k, limit)
		if err != nil {
			return nil, err
		}
		out = append(out, js...)
	}
	return out, nil
}

// sessionCards 는 살아 있는 세션 각각에 파생 사실을 붙인다.
//
// 붙이는 것은 셋이다 — 브랜치·HEAD(워크트리 목록) · ahead(기본 브랜치 대비) ·
// 경로(footprint ∪ change_set ∪ 미커밋). 셋 다 실패해도 세션 행은 남는다.
func (s *Service) sessionCards(ctx context.Context, proj model.Project, cut time.Time, self string, d *derive) ([]SessionCard, error) {
	// ★ 이 함수가 이 서버에서 가장 비싼 일이다 — `git worktree list` 한 번 + 살아 있는
	//   세션마다 ChangedPaths·UncommittedPaths. 그 비용을 세는 자리를 여기 둔다.
	//   호출부에 두면 호출부가 늘 때마다 계측이 조용히 빠진다(실제로 그 모양으로
	//   MCP 꼬리가 도구 호출마다 이 파생을 한 번씩 더 돌리고 있었고, 아무 화면에도 안 떴다).
	start := time.Now()
	defer func() {
		s.derives.Add(1)
		s.deriveMicros.Add(uint64(time.Since(start).Microseconds()))
	}()

	live, err := s.st.ListLive(ctx, proj.ID, cut)
	if err != nil {
		return nil, err
	}
	s.deriveCards.Add(uint64(len(live)))

	var g GitReader
	var wts map[string]string // 워크트리 경로 → 브랜치
	var heads map[string]string
	baseSHA := ""
	if strings.TrimSpace(proj.Path) == "" {
		d.note("project-path", "프로젝트 경로가 비어 있다 — git 파생을 아예 시도하지 않았다")
	} else {
		g = s.git(proj.Path)
		wts, heads = s.worktreeIndex(ctx, g, d)
		if r, err := g.Ref(ctx, proj.DefaultBranch); err != nil {
			d.fail("ref:"+proj.DefaultBranch, err)
		} else {
			d.ok()
			baseSHA = r.SHA
			s.rememberRef(ctx, proj.ID, r)
		}
	}

	cards := make([]SessionCard, 0, len(live))
	for _, v := range live {
		card := SessionCard{View: v, IsSelf: v.Session.ID == self}
		var fails []string

		if g != nil {
			wt := filepath.Clean(v.Session.Worktree)
			if br, ok := wts[wt]; ok {
				card.View.Branch, card.BranchKnown = br, true
				card.View.BranchSHA = heads[wt]
			}
			// 변경집합 — 착수 직후 구간은 브랜치 diff 가 정의상 비어 있어 footprint 가 덮는다.
			if card.BranchKnown && card.View.Branch != "" && card.View.Branch != proj.DefaultBranch {
				if paths, err := g.ChangedPaths(ctx, proj.DefaultBranch, card.View.Branch); err != nil {
					d.fail("changed-paths:"+clip(card.View.Branch, 120), err)
					fails = append(fails, "변경 경로를 못 읽었다")
				} else {
					d.ok()
					card.View.Paths = UnionPaths(card.View.Paths, paths)
					s.rememberChangeSet(ctx, proj.ID, baseSHA, card.View.BranchSHA, paths)
				}
				if ahead, _, err := g.AheadBehind(ctx, card.View.Branch, proj.DefaultBranch); err != nil {
					d.fail("ahead-behind:"+clip(card.View.Branch, 120), err)
					fails = append(fails, "ahead 를 못 읽었다")
				} else {
					d.ok()
					card.View.AheadMain, card.AheadKnown = ahead, true
				}
			}
			// 미커밋 — 커밋 전 의도를 나르는 유일한 축이라 조용히 짧아지면 안 된다.
			if unc, err := g.UncommittedPaths(ctx, v.Session.Worktree); err != nil {
				d.fail("uncommitted:"+clip(v.Session.ID, 64), err)
				fails = append(fails, "미커밋 경로를 못 읽었다")
			} else {
				d.ok()
				card.View.Paths = UnionPaths(card.View.Paths, unc)
			}
		} else {
			fails = append(fails, "git 파생을 시도하지 않았다(프로젝트 경로 없음)")
		}

		// ★ 발자국이 없다는 사실을 침묵하지 않는다. false 면 그 세션은 경로 축에서
		//   아무도 안 막고, **안 막는다는 사실이 화면에 있어야** 한다(설계 §5).
		card.View.HasFootprint = len(card.View.Paths) > 0

		if note, err := s.lastNote(ctx, v.Session.ID); err != nil {
			return nil, err
		} else if note != nil {
			card.View.LastNote = note
		}

		card.DeriveError = strings.Join(fails, " · ")
		cards = append(cards, card)
	}
	return cards, nil
}

// worktreeIndex 는 워크트리 경로 → 브랜치·HEAD 두 색인을 만든다.
func (s *Service) worktreeIndex(ctx context.Context, g GitReader, d *derive) (branches, heads map[string]string) {
	branches, heads = map[string]string{}, map[string]string{}
	wts, err := g.Worktrees(ctx)
	if err != nil {
		d.fail("worktrees", err)
		return branches, heads
	}
	d.ok()
	for _, w := range wts {
		p := filepath.Clean(w.Path)
		branches[p] = w.ShortBranch()
		heads[p] = w.HEAD
	}
	return branches, heads
}

// lastNote 는 세션이 마지막으로 남긴 판단이다. 없으면 nil.
func (s *Service) lastNote(ctx context.Context, sessionID string) (*model.Judgment, error) {
	js, err := s.st.ListJudgmentsBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(js) == 0 {
		return nil, nil
	}
	last := js[len(js)-1] // 시간순이라 마지막이 최신이다
	return &last, nil
}

// rememberRef 는 관측한 ref 를 보관한다. **실패해도 조회를 죽이지 않는다** —
// 이 값은 서버가 죽었을 때의 마지막 스냅숏(설계 §7 의 L1)이지 조회 결과가 아니다.
func (s *Service) rememberRef(ctx context.Context, project string, r model.RefState) {
	r.Project = project
	if err := s.st.UpsertRefState(ctx, r); err != nil {
		s.log.WarnContext(ctx, "ref 관측 보관 실패",
			"project", project, "ref", clip(r.Ref, 120), "error", err.Error())
	}
}

// rememberChangeSet 은 변경집합을 불변으로 보관한다(브랜치가 지워져도 남는다).
// sha 를 모르면 보관하지 않는다 — 키가 빈 행은 나중에 무엇의 변경인지 말하지 못한다.
func (s *Service) rememberChangeSet(ctx context.Context, project, baseSHA, headSHA string, paths []string) {
	if baseSHA == "" || headSHA == "" {
		return
	}
	err := s.st.UpsertChangeSet(ctx, model.ChangeSet{
		Project: project, BaseSHA: baseSHA, HeadSHA: headSHA, Paths: paths,
	})
	if err != nil {
		s.log.WarnContext(ctx, "변경집합 보관 실패",
			"project", project, "error", err.Error())
	}
}

// liveFor 는 겹침 판정에 쓸 살아 있는 세션 목록이다.
// judge 의 좌표계(LiveSession)로 옮긴다 — 판정 함수가 보드 타입을 알 필요가 없다.
func liveFor(cards []SessionCard) []judge.LiveSession {
	out := make([]judge.LiveSession, 0, len(cards))
	for _, c := range cards {
		out = append(out, judge.LiveSession{
			ID: c.View.Session.ID, Label: c.View.Session.Label, Paths: c.View.Paths,
		})
	}
	return out
}
