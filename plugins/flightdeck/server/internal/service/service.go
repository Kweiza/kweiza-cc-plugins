// Package service 는 store·judge·gitreader 를 조합하는 **유일한** 계층이다.
//
// REST·MCP·웹·CLI 가 전부 이 계층만 부른다(설계 원칙 ③: "REST 가 정본, MCP 는 얇은 껍데기.
// 둘 다 같은 순수 함수를 부른다"). 표면마다 조합을 다시 쓰면 같은 판정이 두 벌이 되고,
// 두 벌은 반드시 표류한다 — 그때 어느 쪽이 참인지 말해 주는 자리가 없다.
//
// 이 패키지가 지키는 것 넷:
//
//  1. **파생값을 인자로 받지 않는다.** branch·head·sha·랜딩 이력·변경 경로는 gitreader 가 읽는다.
//     호출자가 줄 수 있는 인자로 두면 틀린 값이 들어온다 — 그것이 이 제품의 1번 원칙이다.
//  2. **판정은 순수 함수에 두고 사유를 돌려준다.** 본문에 흩어지면 시험이 사본을 단정하게 되고,
//     불리언만 돌려주면 "조건 A 때문"과 "이 축을 안 본다"가 구분되지 않는다.
//  3. **git 조회 실패가 조정 기능을 죽이지 않는다.** 파생 실패는 그 필드를 비우고 사유를 담되
//     전체 응답은 낸다. 다만 **침묵하지 않는다** — Freshness 와 Failures 로 표면에 낸다.
//  4. **"죽었다"를 만들지 않는다.** 신호 넷의 시각을 그대로 담고 나이 계산은 표시 계층 몫이다.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/gitreader"
	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

const (
	// APIVersion 은 /healthz 가 알리는 서버 계약 버전이다.
	// 플러그인은 자동 갱신되므로 스큐는 운영자가 아무것도 안 해도 발생한다(설계 §7).
	APIVersion = "1"

	// DefaultLiveWindow 는 Board 가 "이 안에 신호가 있었나"를 자르는 기본 구간이다.
	//
	// ★ 이것은 생존 **판정**이 아니다. 자르는 지점일 뿐이고, 결과에는 각 신호의 시각이
	// 그대로 실린다. 나이를 숫자로만 내는 것이 설계 §4 의 요구다 —
	// 불리언을 만드는 순간 그것이 회수·회피·탈락 셋의 상류가 되고, 그 판정은 실측에서 두 번 틀렸다.
	DefaultLiveWindow = 8 * time.Hour
)

// GitReader 는 이 계층이 git 에서 읽는 사실의 전부다.
//
// *gitreader.Reader 가 이것을 만족한다. 인터페이스로 둔 이유는 시험이
// **실패하는 리더**를 주입해 "파생이 죽어도 조정은 산다"를 단정할 수 있게 하기 위해서다
// (실물 저장소 시험이 정본이고, 이 주입은 실물로 만들기 어려운 실패만 덮는다).
type GitReader interface {
	Refs(ctx context.Context) ([]model.RefState, error)
	Ref(ctx context.Context, ref string) (model.RefState, error)
	Worktrees(ctx context.Context) ([]gitreader.Worktree, error)
	ChangedPaths(ctx context.Context, base, head string) ([]string, error)
	UncommittedPaths(ctx context.Context, worktree string) ([]string, error)
	AheadBehind(ctx context.Context, ref, base string) (ahead, behind int, err error)
	Ancestry(ctx context.Context, sha, tip string) (judge.AncestryResult, error)
}

// GitFactory 는 저장소 경로 하나를 읽는 리더를 만든다.
type GitFactory func(repoPath string) GitReader

// Service 는 조합 계층 하나다.
type Service struct {
	st     *store.Store
	log    *slog.Logger
	now    func() time.Time
	git    GitFactory
	window time.Duration
	getenv func(string) (string, bool)
}

// Option 은 Service 의 선택 설정이다.
type Option func(*Service)

// WithClock 은 시계를 바꾼다. 시험이 시간을 고정할 때 쓴다. nil 은 무시한다.
func WithClock(f func() time.Time) Option {
	return func(s *Service) {
		if f != nil {
			s.now = f
		}
	}
}

// WithGitFactory 는 git 리더 팩토리를 바꾼다. nil 은 무시한다.
func WithGitFactory(f GitFactory) Option {
	return func(s *Service) {
		if f != nil {
			s.git = f
		}
	}
}

// WithLiveWindow 는 Board 가 자르는 구간을 바꾼다. 0 이하는 무시한다.
func WithLiveWindow(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.window = d
		}
	}
}

// WithGitTimeout 은 git 명령 1회의 상한을 바꾼다. 0 이하는 무시한다.
func WithGitTimeout(d time.Duration) Option {
	return func(s *Service) {
		if d <= 0 {
			return
		}
		log := s.log
		s.git = func(repo string) GitReader {
			return gitreader.New(repo, gitreader.WithLogger(log), gitreader.WithTimeout(d))
		}
	}
}

// WithEnv 는 플랫폼 축 관측의 환경 조회를 바꾼다(Doctor 시험용). nil 은 무시한다.
func WithEnv(get func(string) (string, bool)) Option {
	return func(s *Service) {
		if get != nil {
			s.getenv = get
		}
	}
}

// New 는 Service 를 만든다.
func New(st *store.Store, log *slog.Logger, opts ...Option) *Service {
	if log == nil {
		log = slog.Default()
	}
	log = log.With("service.name", "flightdeck")
	s := &Service{
		st:     st,
		log:    log,
		now:    func() time.Time { return time.Now().UTC() },
		window: DefaultLiveWindow,
		getenv: lookupEnv,
		git: func(repo string) GitReader {
			return gitreader.New(repo, gitreader.WithLogger(log))
		},
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// logFail 은 실패한 시도의 **사유**를 원장에 덧붙인다.
//
// 시도 자체는 트랜잭션 안에서 Tx.LogEvent 로 먼저 예약되므로 롤백돼도 남는다.
// 다만 그 시점에는 결과를 모르므로 "왜 실패했나"를 여기서 따로 남긴다 —
// 원장에 시도만 있고 사유가 없으면 실패율은 세지되 무엇을 고쳐야 하는지는 답하지 못한다.
func (s *Service) logFail(ctx context.Context, kind, project, sessionID string, err error) {
	if err == nil {
		return
	}
	s.st.LogEvent(ctx, kind+".fail", project, sessionID, map[string]any{
		"error": clip(err.Error(), 400),
	})
}

// Store 는 저장 계층 핸들이다. 표면(REST·MCP)이 이 계층을 거치지 않고 쓰라는 뜻이 아니라,
// 서버 기동·진단이 같은 핸들을 공유하기 위한 자리다.
func (s *Service) Store() *store.Store { return s.st }

// ─────────────────────────────────────────────────────────────────────────────
// 파생 신선도 — 모든 결과 타입이 이것을 싣는다
// ─────────────────────────────────────────────────────────────────────────────

// DerivedFailure 는 파생 축 하나를 못 읽었다는 사실이다.
//
// **축 이름과 사유를 함께 나른다.** "git 실패"만 남기면 무엇을 못 읽어서 어느 필드가
// 비었는지 알 수 없고, 그러면 빈 필드가 "값이 0이다"로 읽힌다.
type DerivedFailure struct {
	Axis   string `json:"axis"`   // 못 읽은 축의 이름(예: "worktrees", "changed-paths:feat-x")
	Detail string `json:"detail"` // 원인 전문(하류 stderr 발췌 포함)
}

// Derived 는 결과에 실리는 파생 신선도다.
//
// 설계 §6: 모든 패널에 "(파생: git@14:31, 12초 전)" 이 붙는다 —
// 서버가 죽었을 때 마지막 상태가 현재 사실인 척하는 것을 구조로 막는 축이다.
type Derived struct {
	Freshness model.Freshness  `json:"freshness"`
	Failures  []DerivedFailure `json:"failures,omitempty"`
}

// derive 는 파생 시도를 세는 누산기다. 결과 타입에 실리는 것은 Derived 뿐이다.
type derive struct {
	reads    int
	failures []DerivedFailure
}

// ok 는 성공한 git 읽기 1회를 센다.
func (d *derive) ok() { d.reads++ }

// fail 은 축 하나의 실패를 기록한다. 오류를 삼키지 않고 사유 전문을 담는다.
func (d *derive) fail(axis string, err error) {
	detail := "원인 없음"
	if err != nil {
		detail = err.Error()
	}
	d.failures = append(d.failures, DerivedFailure{Axis: axis, Detail: clip(detail, 600)})
}

// note 는 오류 객체가 없는 파생 결손을 기록한다(예: 선행 항목이 아예 없다).
func (d *derive) note(axis, detail string) {
	d.failures = append(d.failures, DerivedFailure{Axis: axis, Detail: clip(detail, 600)})
}

func (d *derive) result(now time.Time) Derived {
	return Derived{Freshness: FreshnessOf(now, d.reads, len(d.failures)), Failures: d.failures}
}

// FreshnessOf 는 파생 시도 결과를 신선도로 옮긴다. 순수 함수다.
//
// 세 상태를 가른다:
//
//	git 을 한 번도 못 읽었다        → Source="db"  Stale=true   (화면은 저장된 마지막 관측을 쓴다)
//	읽었지만 일부 축이 실패했다      → Source="git" Stale=true   (값이 반쪽이다)
//	전부 읽었다                    → Source="git" Stale=false
//
// **실패 0건과 "이 축을 안 봤다"를 뭉개지 않는 것이 이 함수의 목적이다.**
// 실패 목록(Derived.Failures)이 어느 축인지를 말하고, 이 값은 "지금 화면의 파생을
// 얼마나 믿어도 되나"를 말한다. 둘 중 하나만 있으면 사용자가 판단할 수 없다.
func FreshnessOf(now time.Time, gitReads, gitFailures int) model.Freshness {
	switch {
	case gitReads == 0:
		return model.Freshness{Source: "db", ObservedAt: now, Stale: true}
	case gitFailures > 0:
		return model.Freshness{Source: "git", ObservedAt: now, Stale: true}
	default:
		return model.Freshness{Source: "git", ObservedAt: now, Stale: false}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 거절 — 사유와 처방을 함께 나른다
// ─────────────────────────────────────────────────────────────────────────────

// RefusedError 는 이 계층이 거절한 요청이다.
//
// **Guidance 를 함께 나른다.** 규율 산문을 도구 설명에 싣지 않고 "필요할 때, 그 자리에서"
// 응답에 싣는 것이 설계 §6 이고, 그 자리가 여기다. 사유만 주고 처방을 안 주면
// 호출자(에이전트)는 무엇을 고쳐야 하는지 모른 채 같은 호출을 반복한다.
type RefusedError struct {
	What     string // 무엇이 거절됐나 (finish·pick·add …)
	Reason   string // 왜
	Guidance string // 무엇을 하면 되나. 없을 수 있다
}

func (e *RefusedError) Error() string {
	if e.Guidance == "" {
		return fmt.Sprintf("%s 거절: %s", e.What, e.Reason)
	}
	return fmt.Sprintf("%s 거절: %s\n%s", e.What, e.Reason, e.Guidance)
}

// ─────────────────────────────────────────────────────────────────────────────
// 잡동사니
// ─────────────────────────────────────────────────────────────────────────────

// clip 은 오류·로그에 실을 외부 문자열을 자르고 제어문자를 걷어낸다.
// 로그 주입과 무한장 오류 메시지를 막는다(store.clip 과 같은 규율 — 그쪽은 비공개다).
func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}

// UnionPaths 는 경로 집합들을 합쳐 중복 없이 정렬해 낸다. 순수 함수다.
//
// footprint ∪ change_set ∪ 미커밋 이 한 축으로 합쳐지는 자리다(설계 §5).
// 빈 문자열은 버린다 — 빈 경로는 PathsOverlap 에서 아무것과도 안 겹치는데,
// 그 사실이 "겹침 없음"으로 읽히면 이 축이 조용히 죽는다.
func UnionPaths(sets ...[]string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, set := range sets {
		for _, p := range set {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// RelPath 는 절대 경로를 저장소 기준 상대 경로로 옮긴다. 순수 함수다.
//
// ★ 문자열 접두로 자르지 않는다. 접두로 하면 root="/a/b" 일 때 "/a/bc/d" 가 "c/d" 로
// 둔갑해 **다른 저장소의 파일이 이 저장소의 경로인 척**한다. 그 모양의 결함이
// judge.PathsOverlap 에 실재했다(모든 토큰에 "/" 를 붙여 파일형 토큰이 자기 자신과도 안 겹쳤다).
// 여기서는 filepath.Rel 로 성분 단위로 계산하고, 결과가 밖으로 나가면(".." 로 시작) 원본을 둔다.
//
// 훅이 주는 발자국 경로는 절대경로이고 git 이 주는 변경 경로는 저장소 상대다.
// 둘을 같은 좌표계로 옮기지 않으면 겹침 축이 조용히 죽는다.
func RelPath(root, p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	q := filepath.Clean(p)
	if root == "" || !filepath.IsAbs(q) {
		return q
	}
	rel, err := filepath.Rel(filepath.Clean(root), q)
	if err != nil {
		return q // 볼륨이 다르면 상대화가 불가능하다. 원본을 그대로 둔다
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return q // 저장소 밖이다
	}
	return rel
}
