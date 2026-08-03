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
		if view.Blocked, err = s.st.ListJudgmentsByKind(ctx, project, model.JudgmentBlocked, limit); err != nil {
			return BoardView{}, err
		}
		if view.Asks, err = s.st.ListJudgmentsByKind(ctx, project, model.JudgmentAsk, limit); err != nil {
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

// sessionCards 는 살아 있는 세션 각각에 파생 사실을 붙인다.
//
// 붙이는 것은 셋이다 — 브랜치·HEAD(워크트리 목록) · ahead(기본 브랜치 대비) ·
// 경로(footprint ∪ change_set ∪ 미커밋). 셋 다 실패해도 세션 행은 남는다.
func (s *Service) sessionCards(ctx context.Context, proj model.Project, cut time.Time, self string, d *derive) ([]SessionCard, error) {
	live, err := s.st.ListLive(ctx, proj.ID, cut)
	if err != nil {
		return nil, err
	}

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
