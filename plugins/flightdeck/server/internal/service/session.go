package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// eventSessionProjectMismatch 는 §I-2 의 이벤트 kind 다. 상수로 두는 이유는 발화부
// (아래)와 억제 조회(alreadyLoggedProjectMismatch)가 같은 문자열을 봐야 하기 때문이다 —
// 리터럴 둘을 두면 한쪽만 고쳐질 때 억제가 조용히 죽는다.
const eventSessionProjectMismatch = "session.project.mismatch"

// 세션 — 열기·신호·상태.
//
// 이 파일에는 branch·head·sha 를 **받는** 인자가 하나도 없다. 전부 gitreader 가 읽는다.
// 인자로 두면 틀린 값이 들어오고, 그것이 기존 도구에서 "메인 트리의 지금 HEAD"가
// 남의 랜딩 sha 로 박히던 결함이었다(3회 관측).

// OpenSessionInput 은 세션을 여는 데 필요한 **정체**다.
//
// 여기 있는 것은 전부 서버가 파생할 수 없는 값이다 — 서버는 클라이언트 머신을 못 본다.
// cc_session_id 는 MCP 서버 환경의 CLAUDE_CODE_SESSION_ID, worktree 는 cwd 다(설계 §13).
type OpenSessionInput struct {
	Project       string // 프로젝트 id
	ProjectPath   string // 서버 머신에서의 저장소 절대경로. 미등록 프로젝트를 등록할 때만 쓴다
	DefaultBranch string // 비면 git 에서 고른다(PickDefaultBranch)
	MachineID     string // 클라이언트가 생성해 로컬에 보관하는 안정 id
	Hostname      string
	Worktree      string // 이 세션의 작업 트리 절대경로
	CCSessionID   string // Claude Code 세션 UUID
	Label         string // 표시 전용. 어떤 필터의 축도 아니다
}

// SessionResult 는 세션 하나를 연 결과다.
type SessionResult struct {
	Session model.Session `json:"session"`
	Created bool          `json:"created"` // 재개와 신규를 가른다 — 배너 문구가 달라진다
	Project model.Project `json:"project"`
	Branch  string        `json:"branch"` // 파생. 못 읽었으면 빈 문자열이고 Failures 에 사유가 있다
	HeadSHA string        `json:"head_sha"`
	Claims  []string      `json:"claims,omitempty"` // 이 세션이 이미 쥐고 있는 항목(재개 경로의 첫 줄)
	Derived
}

// SessionVerdict 는 세션 열기 요청의 판정이다. 사유는 항상 채운다.
type SessionVerdict struct {
	OK     bool
	Reason string
}

// JudgeOpenSession 은 세션 정체가 성립하는지 판정한다. 순수 함수다.
//
// 불리언이 아니라 **사유**를 돌려준다 — "안 됐다"만 알면 무엇을 안 준 것인지 알 수 없고,
// 이 네 값은 전부 클라이언트 환경에서 오므로 빠진 축이 곧 탐지가 깨진 축이다(설계 §13).
func JudgeOpenSession(in OpenSessionInput) SessionVerdict {
	wt := judge.JudgePathCoordinate(in.Worktree)
	switch {
	case strings.TrimSpace(in.Project) == "":
		// ★ 문구가 **원인과 탈출구를 함께** 말한다. 앞선 판은 "어느 프로젝트의 세션인지 없이는
		// 큐도 보드도 좌표가 없다" 로 끝났는데, 그것은 **결과의 서술**이지 고칠 거리가 아니다.
		//
		// 이 축이 비어 오는 경로는 하나로 좁혀져 있다: 클라이언트가 `git rev-parse` 로 주
		// 저장소를 못 찾으면 프로젝트 id 를 **일부러 안 짓는다**(cmd/fd 의 resolveProject —
		// 옛 동작은 디렉토리 이름을 지어냈고 그것이 원장에 유령 프로젝트를 남겼다).
		// 서버는 클라이언트 머신의 cwd 를 볼 수 없으니 그 실패를 직접 관측할 수는 없지만,
		// **어디를 봐야 하는지**는 말할 수 있다.
		//
		// 그리고 여기까지 왔다는 것은 OpenSession 의 3중키 되찾기도 빈손이었다는 뜻이다 —
		// 그 사실을 적어야 사람이 "이미 열린 세션인데 왜"를 되묻지 않는다.
		return SessionVerdict{Reason: "project 가 비었다 — 클라이언트가 프로젝트 좌표를 못 풀었다는 뜻이다" +
			"(git 저장소가 아니거나 `git rev-parse` 가 실패했다). 이 3중키로 열린 세션도 없어 되찾을 과거가 없다. " +
			"지어내지 않는다 — git 저장소 안에서 부르거나 FD_PROJECT 로 프로젝트를 명시해라"}
	case strings.TrimSpace(in.MachineID) == "":
		return SessionVerdict{Reason: "machine_id 가 비었다 — 세션 정체는 (machine, worktree, cc_session) 3중키다"}
	case strings.TrimSpace(in.Worktree) == "":
		return SessionVerdict{Reason: "worktree 가 비었다 — MCP 서버의 cwd 가 그 값이다(설계 §13)"}
	// ★ 이 절이 IsAbs 보다 **앞**이어야 한다. Linux 서버의 filepath.IsAbs 는 "C:\repo" 를
	// 절대경로로 안 보므로, 순서가 뒤바뀌면 Windows 사용자가 "절대경로가 아니다"라는
	// 사실이되 원인이 아닌 사유를 받는다 — 그 사유로는 고칠 수 없다.
	case !wt.OK:
		return SessionVerdict{Reason: wt.Reason}
	case !filepath.IsAbs(in.Worktree):
		return SessionVerdict{Reason: fmt.Sprintf(
			"worktree %q 가 절대경로가 아니다 — 상대경로는 서버와 세션이 서로 다른 곳을 가리킨다",
			clip(in.Worktree, 200))}
	case strings.TrimSpace(in.CCSessionID) == "":
		return SessionVerdict{Reason: "cc_session_id 가 비었다 — CLAUDE_CODE_SESSION_ID 를 못 읽었다면 " +
			"그 탐지가 깨진 것이다(fd doctor 가 그 축을 잰다). 지어내지 않는다"}
	default:
		return SessionVerdict{OK: true, Reason: "3중키와 프로젝트가 전부 있다"}
	}
}

// PickDefaultBranch 는 관측된 로컬 브랜치에서 기본 브랜치를 고른다. 순수 함수다.
//
// 선언값이 있으면 그것이 이긴다(사람이 정한 것을 파생이 덮지 않는다).
// 없으면 main → master 순으로 보고, 둘 다 없으면 HEAD 가 가리키는 브랜치를 쓴다.
// 그것도 없으면 "main" 이다 — 커밋이 하나도 없는 저장소가 그 경우이고,
// 그때 무엇을 골라도 틀릴 수 없다(비교 대상 커밋이 아직 없다).
func PickDefaultBranch(declared string, refs []model.RefState, headBranch string) string {
	if d := strings.TrimSpace(declared); d != "" {
		return d
	}
	have := map[string]bool{}
	for _, r := range refs {
		if r.Ref != "" && r.Ref != "HEAD" {
			have[r.Ref] = true
		}
	}
	for _, cand := range []string{"main", "master"} {
		if have[cand] {
			return cand
		}
	}
	if hb := strings.TrimSpace(headBranch); hb != "" {
		return hb
	}
	return "main"
}

// OpenSession 은 세션을 열거나(신규) 그대로 돌려준다(재개).
//
// 같은 3중키면 **같은 세션**이다. 그 판정은 store 가 하고 여기서는 흉내 내지 않는다.
func (s *Service) OpenSession(ctx context.Context, in OpenSessionInput) (SessionResult, error) {
	var res SessionResult

	// ★ 프로젝트 좌표가 **비어 오면 3중키로 되찾는다** — 판정보다 앞이다.
	//
	// 되찾기는 지어내기가 아니다. 세션 정체는 (machine, worktree, cc_session) 3중키이고
	// **project 가 그 키에 안 들어가므로**, 서버는 프로젝트를 몰라도 이 세션이 누구인지 안다.
	// 여기서 읽는 값은 **이 세션이 처음 열릴 때 스스로 등록한** 좌표다 — 클라이언트가 방금
	// 못 푼 것을 원장이 대신 기억하고 있는 것뿐이다.
	//
	// ★ **없으면 이 갈래가 필요한 이유.** 클라이언트가 git 실패 시 id 를 안 짓게 되면서
	// (cmd/fd 의 resolveProject) 그 빈 값이 여기 온다. 무조건 거절하면 git 이 **일시적으로**
	// 안 읽히는 순간 — 워크트리가 막 만들어지는 중이거나 지워지는 중 — 에 훅이 물었을 때
	// 살아 있는 세션의 신호가 조용히 사라진다. 옛 동작에서는 (엉뚱한 이름으로나마) 성공하던
	// 쓰기라, 그것은 이 브랜치가 **새로 만드는 회귀**다. a168c20 이 정확히 같은 모양의 회귀를
	// 한 번 만들었고 리뷰가 잡았다(고아를 막자 후속 note·add 가 FK 에서 죽었다).
	//
	// ★ **아래 자동 등록 앞의 3중키 조회(② 소절)와 형제이되 다른 축이다.** 그쪽은 "이름이
	// **틀리게** 왔다"를 다루고 여기는 "이름이 **안** 왔다"를 다룬다. 그쪽은 세션 등록
	// 트랜잭션 안에서 돌아야 하지만(자동 등록과 경합한다), 이쪽은 JudgeOpenSession 을 통과할
	// 값을 만드는 일이라 판정보다 앞에 있어야 한다 — 그래서 자리가 갈린다.
	//
	// 못 찾으면 **아무 말 없이 빈 채로 둔다.** 거절 문구가 그 사실까지 말한다(JudgeOpenSession).
	if strings.TrimSpace(in.Project) == "" {
		if prior, err := s.st.FindSession(ctx, in.MachineID, in.Worktree, in.CCSessionID); err == nil &&
			strings.TrimSpace(prior.Project) != "" {

			s.log.InfoContext(ctx, "프로젝트 좌표가 안 와 3중키로 되찾았다",
				"project", clip(prior.Project, 64), "session", clip(prior.ID, 64),
				"worktree", clip(in.Worktree, 200))
			in.Project = prior.Project
		}
	}

	if v := JudgeOpenSession(in); !v.OK {
		return res, &RefusedError{What: "session open", Reason: v.Reason}
	}
	now := s.now()
	d := &derive{}

	// ① git 파생 — 트랜잭션 **밖에서** 먼저 한다. 실패해도 세션 등록은 진행한다.
	//    조정(누가 살아 있나)이 파생 실패로 죽으면 이 도구의 존재 이유가 사라진다.
	repo := strings.TrimSpace(in.ProjectPath)
	if repo == "" {
		if p, err := s.st.GetProject(ctx, in.Project); err == nil {
			repo = p.Path
		}
	}
	branch, head := "", ""
	var refs []model.RefState
	if repo != "" {
		g := s.git(repo)
		if rs, err := g.Refs(ctx); err != nil {
			d.fail("refs", err)
		} else {
			d.ok()
			refs = rs
		}
		branch, head = s.worktreeFacts(ctx, g, in.Worktree, d)
	} else {
		d.note("project-path", "프로젝트 경로를 모른다 — git 파생을 아예 시도하지 않았다")
	}

	// ② 등록. 프로젝트·머신이 FK 대상이라 같은 트랜잭션 안에서 먼저 만든다.
	var sess model.Session
	var created bool
	var proj model.Project
	var divergences []Divergence
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		// 시도를 먼저 예약한다 — 롤백돼도 남는다.
		t.LogEvent("session.open", in.Project, "", map[string]any{
			"worktree": clip(in.Worktree, 200), "machine": clip(in.MachineID, 64),
			"cc_session": clip(in.CCSessionID, 64),
		})
		p, err := t.GetProject(in.Project)
		switch {
		case err == nil:
			proj = p
		case errors.Is(err, store.ErrNotFound):
			// ★ **자동 등록 전에 3중키 세션을 본다.** 이 순서가 이 소절의 전부다.
			//
			// 세션 정체는 (machine, worktree, cc_session) 3중키이고 **project 가 안 들어간다.**
			// 그래서 아래 t.OpenSession 은 같은 3중키 행이 이미 있으면 **재개**한다(created=false)
			// — 그 행의 project 는 처음 열릴 때 정해진 것 그대로다. 그런데 자동 등록이 그보다
			// 앞이라, 잘못된 project 로 요청이 오면 **프로젝트만 새로 만들어지고 세션은
			// 남의 프로젝트 것을 재개한다.** 트랜잭션은 성공하므로 롤백도 없고 아무 흔적도
			// 안 남는다 — 고아 프로젝트가 조용히 하나 는다.
			//
			// 실측(원장): console-screen-landing · t6-console-notice-kind-split ·
			// upload-staging-live-verification 셋이 정확히 이 모양으로 생겼다. 셋 다 세션 행은
			// 전부 context-platform 으로 정상인데 워크트리 이름의 프로젝트에는 session.open
			// 이벤트만 있고 세션이 0건이다. 두 달 뒤 원장을 뒤져서야 경위를 알 수 있었다.
			//
			// 거절하지 않고 **기존 세션의 프로젝트로 진행**한다: 이 경로는 훅이 프롬프트마다
			// 무는 자리라 거절하면 세션이 안 열리고, 그 대가로 얻는 것이 없다(잘못된 것은
			// 클라이언트가 보낸 이름이지 이 세션의 정체가 아니다). 대신 **관측을 남긴다** —
			// 지금까지 이 자리에 아무 흔적이 없던 것이 이 항목이 늦게 발견된 이유다.
			if prior, perr := t.FindSession(in.MachineID, in.Worktree, in.CCSessionID); perr == nil &&
				prior.Project != "" && prior.Project != in.Project {

				// ★ I-2(최종 리뷰): 이 이벤트를 무조건 남기면 안 된다. 이 갈래는 3중키가
				// 살아 있는 한 OpenSession 이 불릴 때마다(훅 진입점 넷~다섯 자리, 그것도
				// 프롬프트마다) 매번 다시 탄다 — 요청 프로젝트는 클라이언트 cwd 의 함수라
				// 세션 생애 동안 안 바뀐다. 20줄 아래 DivergentSessions 의 `if created` 접기가
				// 정확히 이 위험을 "건별로 남기면 원장이 프롬프트마다 4배로 증폭된다"고
				// 이름 붙였는데, 이 이벤트에는 그 가드가 없었다. 그리고 event 는
				// event_no_delete 트리거로 **삭제가 안 된다**(schema.sql) — 억제가 없으면
				// 지울 수 없는 행이 무한히 는다.
				//
				// ★ 억제 단위는 (prior.ID, requested) 다. 어법은 judge/prescribe.go 의
				// emittedKeys 와 같다 — 세션의 과거 이벤트를 kind 로 읽고 payload 를 파싱해
				// "이미 낸 키가 있나"를 판정한다(아래 alreadyLoggedProjectMismatch). 그
				// 함수와 다른 점 하나: emittedKeys 는 세션이 **열린 시각 이후**만 보는데
				// (그 억제는 "판단을 남기면 다시 뜬다"는 재발화 규칙이 있어서 시각 창이
				// 필요하다), 여기는 시각 창 없이 **전 기간**을 본다 — 이 축에는 재발화
				// 규칙이 없고(요청 프로젝트는 다시 안 바뀐다) 한 번 남기면 그걸로 충분하다.
				//
				// ★ 완전한 원자성은 아니다. 조회는 Tx 밖(별도 커넥션의 s.st)에서 하는데,
				// 그래도 안전한 이유는 이 이벤트 자체가 Tx.LogEvent 로 **예약만** 되고
				// 커밋 뒤에야 별도 커넥션으로 flush 되기 때문이다(store.go 의
				// flushDeferred) — 그래서 t.tx 로 봐도 s.db 로 봐도 "지금까지 커밋+flush
				// 된 것"이라는 같은 답을 얻는다(store.go 의 Tx 주석: "읽기는 Tx 를 안
				// 거치고 Store 의 조회 메서드가 s.db 로 바로 질의한다. WAL 이라 안
				// 막힌다"). 남는 위험은 커밋~flush 사이의 아주 좁은 창에 동시 프로세스가
				// 끼는 것뿐이고, 그때도 최대 2건이지 무한 증폭이 아니다 — 완전한 원자성을
				// 사려면 event 표에 UNIQUE 제약을 더하는 새 마이그레이션이 들고, 그러면
				// "추가 전용 감사 원장"이라는 지금 스키마 계약을 벗어나 이 항목의 범위를
				// 넘는다.
				if !s.alreadyLoggedProjectMismatch(ctx, prior.ID, in.Project) {
					t.LogEvent(eventSessionProjectMismatch, prior.Project, prior.ID, map[string]any{
						"requested": clip(in.Project, 64), "using": clip(prior.Project, 64),
						"worktree": clip(in.Worktree, 200), "cc_session": clip(in.CCSessionID, 64),
					})
				}
				// 로그는 **매번** 남긴다 — WarnContext 는 회전하는 인프라이지 지울 수 없는
				// 원장이 아니다. 접는 것은 이벤트뿐이다(위 이유).
				s.log.WarnContext(ctx, "요청한 프로젝트가 이 3중키의 기존 세션과 다르다 — 기존 것을 쓴다",
					"requested", clip(in.Project, 64), "using", clip(prior.Project, 64),
					"session", clip(prior.ID, 64))

				existing, gerr := t.GetProject(prior.Project)
				if gerr != nil {
					return gerr
				}
				proj = existing
				in.Project = prior.Project // 아래 OpenSession·Divergent 가 같은 좌표를 쓰게
				break
			}
			if repo == "" {
				return &RefusedError{What: "session open",
					Reason:   fmt.Sprintf("프로젝트 %q 가 등록돼 있지 않고 project_path 도 안 왔다", clip(in.Project, 64)),
					Guidance: "프로젝트 경로를 함께 보내라 — 서버는 클라이언트 머신의 cwd 를 볼 수 없다."}
			}
			proj = model.Project{
				ID:            in.Project,
				Path:          repo,
				DefaultBranch: PickDefaultBranch(in.DefaultBranch, refs, branch),
				CreatedAt:     now,
			}
			if err := t.UpsertProject(proj); err != nil {
				return err
			}
			s.log.InfoContext(ctx, "프로젝트 자동 등록",
				"project", proj.ID, "path", clip(proj.Path, 200), "default_branch", proj.DefaultBranch)
		default:
			return err
		}

		if err := t.UpsertMachine(model.Machine{
			ID: in.MachineID, Hostname: in.Hostname, LastSeen: now,
		}); err != nil {
			return err
		}

		sess, created, err = t.OpenSession(in.Project, in.MachineID, in.Worktree, in.CCSessionID, in.Label, now)
		if err != nil {
			return err
		}

		// 같은 대화에 다른 machine·project 가 들어왔는지 **관측한다**(divergence.go).
		//
		// ★ created 일 때만 본다. 이것이 항목이 요구한 접기다 — 훅은 프롬프트 하나에
		// 최대 4프로세스로 세션을 여는데(hook.go), 같은 3중키로는 **첫 프로세스만**
		// 행을 만들고 나머지는 재개라 created=false 다. 그래서 별도 상태 없이
		// (세션, 들어온 machine) 조합당 정확히 한 번이 된다. 건별로 남기면 원장이
		// 프롬프트마다 4배로 증폭된다.
		if created {
			others, derr := t.DivergentSessions(in.CCSessionID, in.Project, in.MachineID)
			if derr != nil {
				// 관측 실패가 세션 등록을 죽이면 안 된다 — 조정이 관측 때문에 멈추면
				// 이 도구의 존재 이유가 사라진다. 삼키지 않고 사유만 남긴다.
				d.fail("identity-divergence", derr)
			} else if ds := JudgeIdentityDivergence(in, others); len(ds) > 0 {
				divergences = ds
				t.LogEvent("session.identity_divergence", in.Project, sess.ID,
					divergencePayload(in, ds))
			}
		}
		if err := t.AddWorkspace(model.Workspace{
			SessionID: sess.ID, Project: in.Project, Path: in.Worktree, IsPrimary: true,
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		s.logFail(ctx, "session.open", in.Project, "", err, failAbout{})
		s.log.ErrorContext(ctx, "세션 열기 실패",
			"project", clip(in.Project, 64), "worktree", clip(in.Worktree, 200), "error", err.Error())
		return res, err
	}

	// ★ 커밋 뒤에 남긴다. 원장 이벤트는 트랜잭션과 함께 가고(LogEvent 는 커밋 후 흘린다),
	// 로그는 롤백된 시도까지 말하면 안 되기 때문이다 — "갈렸다"고 적어 놓고 그 행이
	// 실제로는 안 들어갔으면 다음 사람이 없는 행을 찾는다.
	if len(divergences) > 0 {
		s.log.WarnContext(ctx, "같은 대화에 다른 정체가 들어왔다 — 보드에 카드가 여러 장으로 뜬다",
			"session_id", sess.ID, "cc_session", clip(in.CCSessionID, 64),
			"project", clip(in.Project, 64), "machine", clip(in.MachineID, 64),
			"worktree", clip(in.Worktree, 200), "reason", RenderDivergence(divergences))
	}

	claims, err := s.st.ClaimedItems(ctx, sess.ID)
	if err != nil {
		// 선점 목록은 조정 정보라 파생이 아니다. 실패는 그대로 올린다.
		return res, err
	}

	res = SessionResult{
		Session: sess, Created: created, Project: proj,
		Branch: branch, HeadSHA: head, Claims: claims,
		Derived: d.result(now),
	}
	s.log.InfoContext(ctx, "세션 열림",
		"project", proj.ID, "session_id", sess.ID, "created", created,
		"branch", branch, "stale", res.Freshness.Stale)
	return res, nil
}

// alreadyLoggedProjectMismatch 는 이 세션이 이 요청 프로젝트에 대한
// session.project.mismatch 이벤트를 이미 냈는지 본다(I-2 억제 판정).
//
// judge/prescribe.go 의 emittedKeys 와 같은 어법이다 — 세션 하나의 과거 이벤트를 kind 로
// 걸러 읽고 payload 를 파싱해 "이미 낸 키가 있나"를 본다. 그 함수와 다른 점: emittedKeys 는
// 세션이 **열린 시각 이후**만 보는데, 그 억제는 판단을 남기면 다시 뜨는 재발화 규칙이 있어
// 시각 창이 필요하다. 여기는 그런 규칙이 없다(요청 프로젝트가 다시 바뀔 이유가 없다) —
// 그래서 시각 창 없이 세션 생애 **전체**를 본다.
func (s *Service) alreadyLoggedProjectMismatch(ctx context.Context, sessionID, requested string) bool {
	evs, err := s.st.ListSessionEvents(ctx, sessionID, eventSessionProjectMismatch, time.Time{})
	if err != nil {
		// 조회 실패가 세션 등록을 죽이면 안 된다 — 억제를 포기하고 그대로 남긴다.
		// 이벤트가 한 건 더 남는 것이 세션을 못 여는 것보다 훨씬 싼 대가다.
		s.log.WarnContext(ctx, "mismatch 이벤트 억제 조회 실패 — 억제 없이 남긴다",
			"session_id", clip(sessionID, 64), "error", err.Error())
		return false
	}
	requested = clip(requested, 64) // 발화부와 같은 자름(clip(in.Project, 64)) — 안 맞추면 억제가 못 맞는다
	for _, e := range evs {
		var p struct {
			Requested string `json:"requested"`
		}
		if json.Unmarshal([]byte(e.Payload), &p) != nil {
			// 해석 실패를 조용히 버리면 그 키가 안 눌린 것으로 보여 이벤트가 다시 뜬다 —
			// prescribe.go 의 emittedKeys 와 같은 판단이다. 그쪽처럼 WARN 으로 남긴다.
			s.log.WarnContext(ctx, "mismatch 이벤트 payload 해석 실패",
				"session_id", clip(sessionID, 64), "payload", e.Payload)
			continue
		}
		if p.Requested == requested {
			return true
		}
	}
	return false
}

// worktreeFacts 는 워크트리 하나의 브랜치와 HEAD sha 를 관측한다.
//
// `worktree list` 한 번으로 전부 얻는다 — 세션마다 따로 물으면 git 호출이 세션 수만큼 는다.
// 목록에서 못 찾으면(심볼릭 링크 등) HEAD 를 직접 관측해 sha 만이라도 채운다.
// 못 채운 축은 **비운 채로 사유를 남긴다** — 0값으로 채우면 "안 봤다"가 사라진다.
func (s *Service) worktreeFacts(ctx context.Context, g GitReader, worktree string, d *derive) (branch, head string) {
	if worktree == "" {
		return "", ""
	}
	wts, err := g.Worktrees(ctx)
	if err != nil {
		d.fail("worktrees", err)
	} else {
		d.ok()
		want := filepath.Clean(worktree)
		for _, w := range wts {
			if filepath.Clean(w.Path) != want {
				continue
			}
			if w.Locked {
				s.log.WarnContext(ctx, "워크트리가 잠겨 있다",
					"worktree", clip(w.Path, 200), "reason", clip(w.LockReason, 200))
			}
			if w.Prunable {
				s.log.WarnContext(ctx, "워크트리가 prunable 이다",
					"worktree", clip(w.Path, 200), "reason", clip(w.PrunableReason, 200))
			}
			return w.ShortBranch(), w.HEAD
		}
		d.note("worktree:"+clip(worktree, 200), "worktree list 에 이 경로가 없다 — 이 저장소의 워크트리가 아니거나 경로가 다르게 해석된다")
	}
	// 목록에서 못 찾았거나 목록 자체가 실패했다. sha 만이라도 건진다.
	r, rerr := g.Ref(ctx, "HEAD")
	if rerr != nil {
		d.fail("head:"+clip(worktree, 200), rerr)
		return "", ""
	}
	d.ok()
	return "", r.SHA
}

// Beat 는 생존 신호 하나와(있으면) 미커밋 발자국을 기록한다.
//
// ★ 신호는 **사실**이지 판정이 아니다. 넷을 나란히 쌓고 합치지 않는다 —
// 하나만 보면 "에이전트가 긴 도구를 돌리는 중"과 "사람이 읽기만 하는 중" 둘 중 하나를 반드시 오판한다.
//
// paths 는 PostToolUse 훅이 준 편집 대상이다. 절대경로로 오므로 **세션 워크트리 기준으로
// 상대화한다** — git 이 주는 변경 경로와 좌표계가 다르면 겹침 축이 조용히 죽는다.
func (s *Service) Beat(ctx context.Context, sessionID string, kind model.SignalKind, paths []string) error {
	if strings.TrimSpace(sessionID) == "" {
		return &RefusedError{What: "beat", Reason: "session_id 가 비었다"}
	}
	switch kind {
	case model.SignalPrompt, model.SignalTool, model.SignalMCP, model.SignalCommit, model.SignalPush:
	default:
		return &RefusedError{What: "beat",
			Reason: fmt.Sprintf("신호 종류 %q 가 열거에 없다(prompt|tool|mcp|commit|push)", clip(string(kind), 32))}
	}
	now := s.now()
	// ★ 좌표계가 다른 경로는 버린다 — 여기서 거절하면 훅이 죽고 세션 생존 신호가 끊긴다.
	// 그것이 침묵보다 나쁘다. 대신 버린 건수와 경로 일부를 event 원장에 남긴다
	// (응답은 훅이 삼킨다 — 원장이 이 표면에서 유일하게 남는 관측 자리다).
	//
	// ★ judge.FilterPathCoordinate 를 직접 부르지 않고 FilterFootprintPaths 를 거친다 —
	// 같은 패키지 안에서 같은 연산에 이름이 둘 생기는 것을 막는다(service.go 의
	// FilterFootprintPaths 주석: "존재 이유는 계층뿐이다").
	//
	// ★ 이것은 **좌표계 축**이다. 포함 축("이 경로가 이 카드의 트리 안인가")은 여기서
	// 판정할 수 없다 — 기준 트리가 세션 카드에 있고 그것은 아래 Tx 안에서 읽는다.
	// 두 축을 한 이름으로 뭉개면 "걸러졌다"가 문자 집합만 걸렀다는 뜻이 된다.
	kept, rejected := FilterFootprintPaths(paths)

	// 포함 축 판정 결과. Tx 안에서 세션의 워크트리를 읽어야 나오므로 여기 담아
	// Tx 뒤의 경고 로그가 읽는다.
	var outside []string

	err := s.st.Tx(ctx, func(t *store.Tx) error {
		sess, err := t.GetSession(sessionID)
		if err != nil {
			return err
		}

		// ★ 포함 축 — 좌표계를 통과한 것 중 **카드의 워크트리 밖**을 가른다.
		//
		// 이 관문이 없어서 서브에이전트가 `cp -r` 로 뜬 저장소 사본
		// (`/tmp/…/scratchpad/mut/repo/…`)이 발자국으로 들어왔고, Stop 훅이 그것을
		// 근거로 "항목을 선점하지 않고 고치고 있다"는 처방을 쐈다 — 그 순간 실제
		// 저장소와 그 세션의 워크트리는 **둘 다 `git status` 0줄**이었다.
		//
		// 규율은 발자국 쪽 기존 규약 그대로다: **버리되 남긴다.** 거절하지 않는 이유는
		// 위 좌표계 축과 같고(훅이 죽으면 생존 신호가 끊긴다), 조용히 지우지 않는
		// 이유는 그것이 이 함수가 없애려는 침묵 그 자체이기 때문이다.
		//
		// ★ 기준 트리는 **세션의 워크트리**다(프로젝트 루트가 아니다). 셋을 재고 골랐다:
		//
		//	① RelPath 가 이미 그 기준을 쓴다. 프로젝트 루트로 바꾸면 rel 좌표계가 둘이
		//	   되고, 겹침 축 전체가 "모든 rel 은 같은 기준"이라는 전제 위에 서 있다.
		//	② 형제 워크트리와 주 저장소의 같은 파일은 **다른 파일**이다. 워크트리 기준일
		//	   때만 둘 다 `cmd/fd/hook.go` 로 접혀 병합 충돌 축과 일치한다(5ccf915 가
		//	   "같은 repo-상대 경로를 만지면 실제로 충돌한다 — 진짜 겹침이다"로 못박았다).
		//	③ 워크트리 밖 경로는 **이미 죽은 데이터**다. rel 로 못 옮겨 절대경로로 남고,
		//	   그러면 judge/prescribe.go 의 comparablePath 가 "비교 불가능"으로 걸러낸다.
		//	   즉 버리는 것은 정보 손실이 아니라 **겹침 축에서 못 쓰던 것을 원장으로
		//	   옮기는 것**이다 — 아래 dropped_paths 에 경로가 그대로 남는다.
		//
		// 접두 일치는 안 쓴다. RelPathWithin 이 filepath.Rel 로 성분 단위로 계산한다
		// (DESIGN §3 이 조상 트리 등록을 일부러 없앤 것과 같은 이유다).
		inside := make([]string, 0, len(kept))
		outside = outside[:0]
		for _, p := range kept {
			rel, within := RelPathWithin(sess.Worktree, p)
			if rel == "" {
				continue
			}
			if !within {
				outside = append(outside, p)
				continue
			}
			// ★ 포함 축의 둘째 겹 — **상대화한 뒤에도** 워크트리 접두가 남았으면 트리 밖이다.
			//
			// filepath.Rel 은 파일시스템 포함을 재는데 링크 워크트리는
			// `<repo>/.flightdeck/worktrees/<id>` 라 저장소 루트의 **물리적 자손**이라
			// within=true 가 나온다. 그러면 rel 이 접두를 인 채 발자국이 되고, 그 문자열은
			// 어떤 선언 경로와도 **원리적으로** 안 겹친다(pathRelated 는 성분 0번부터 맞추는데
			// `.flightdeck` vs `plugins` 에서 즉시 갈린다). 더 나쁜 것은 절대경로가 아니라
			// judge.comparablePath 를 통과한다는 것이다 — 비교 가능한 척하며 100% 안 덮인
			// 것으로 세어진다. 실측: 그런 행 111건, 그것을 인용한 처방 22건 전부 `outside:` 키.
			//
			// 규율은 위와 같다 — **버리되 남긴다**(dropped_paths 와 경고 로그로 간다).
			if judge.CarriesWorktreePrefix(rel) {
				outside = append(outside, p)
				continue
			}
			inside = append(inside, rel)
		}

		// ★ session.beat 의 payload 에 원본 경로 전부를 실으면 무한히 커질 수 있으므로
		// 앞 5개만 자르고, 잘렸다는 사실이 드러나도록 총 건수(rejected·outside)를
		// payload 에 함께 둔다. 사유 전체까지 실을 필요는 없다 — 경로가 무엇이었는지가
		// 핵심이고, 왜는 로그(아래)가 낸다.
		//
		// 자르는 규율은 service.go 의 clipDroppedPaths 하나다 — item.claim 도 같은 것을
		// 쓴다. 여기 로컬 const 로 두면 두 자리의 상한이 조용히 갈린다.
		rejectedPaths := make([]string, 0, len(rejected))
		for _, r := range rejected {
			rejectedPaths = append(rejectedPaths, r.Path)
		}
		droppedPaths := clipDroppedPaths(rejectedPaths, outside)

		// ★ count 의 의미가 **두 번** 바뀌었다 — len(paths)(제출 전부) → len(kept)
		// (좌표계 통과) → 지금은 len(inside)(포함 축까지 통과, 즉 실제로 Touch 한 수).
		// 기존 원장 질의가 세 정의를 걸치게 된다.
		// count + rejected + outside 로 맨 처음 값(len(paths))을 복원할 수 있다.
		t.LogEvent("session.beat", sess.Project, sessionID, map[string]any{
			"kind": string(kind), "count": len(inside),
			"rejected": len(rejected), "outside": len(outside),
			"dropped_paths": droppedPaths,
		})
		if err := t.Beat(sessionID, kind, now); err != nil {
			return err
		}
		for _, rel := range inside {
			// origin=observed. "선언했으나 안 건드림"과 "선언 없이 건드림"을 뭉개지 않는다.
			if err := t.Touch(sessionID, rel, model.OriginObserved, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.logFail(ctx, "session.beat", "", sessionID, err, failAbout{Mode: string(kind)})
		s.log.ErrorContext(ctx, "신호 기록 실패",
			"session_id", clip(sessionID, 64), "kind", string(kind), "error", err.Error())
		return err
	}
	// ★ 이 블록은 err == nil 분기 **안**에 있다(전에는 Tx 결과 확인보다 앞이었다) —
	// 트랜잭션이 롤백되면 아무것도 실제로는 안 버려진(기록되지 않은) 것인데, 그 앞에서
	// "버렸다"고 찍으면 사실과 다른 경보가 된다. 사유는 로그로 낸다 — 원장에는
	// 건수와 경로 일부만 남고, 무엇이 왜 버려졌는지 전문은 여기서만 볼 수 있다.
	if len(rejected) > 0 {
		s.log.WarnContext(ctx, "발자국 경로를 좌표계 관문이 버렸다",
			"session_id", clip(sessionID, 64), "dropped", len(rejected),
			"first_reason", rejected[0].Reason)
	}
	// ★ 좌표계 거절과 **따로** 낸다. 둘은 다른 축이고, 합치면 "무엇이 왜 사라졌나"가
	// 다시 뭉개진다 — 이 항목이 없애려던 것이 정확히 그 뭉갬이다.
	// 이 줄이 뜨는 흔한 원인은 서브에이전트가 저장소 사본을 스크래치패드에 뜬 것이다.
	if len(outside) > 0 {
		s.log.WarnContext(ctx, "발자국 경로가 카드의 워크트리 밖이라 버렸다",
			"session_id", clip(sessionID, 64), "dropped", len(outside),
			"first_path", clip(outside[0], 200))
	}
	return nil
}

// SetState 는 세션 상태를 바꾼다.
//
// active 는 파생이고 paused·blocked 만 사람이 쓴다(관측으로 구분 불가하므로).
// blocked 에는 사유가 필수다 — 그 판정은 store.ValidateSessionState 가 하고 여기서 흉내 내지 않는다.
func (s *Service) SetState(ctx context.Context, sessionID string, st model.SessionState, why string) error {
	if strings.TrimSpace(sessionID) == "" {
		return &RefusedError{What: "session state", Reason: "session_id 가 비었다"}
	}
	if err := store.ValidateSessionState(st, why); err != nil {
		return &RefusedError{What: "session state", Reason: err.Error()}
	}
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		sess, err := t.GetSession(sessionID)
		if err != nil {
			return err
		}
		t.LogEvent("session.state", sess.Project, sessionID, map[string]any{
			"state": string(st), "reason": clip(why, 200),
		})
		if err := t.SetSessionState(sessionID, st, why); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		s.logFail(ctx, "session.state", "", sessionID, err, failAbout{Mode: string(st)})
		s.log.ErrorContext(ctx, "세션 상태 변경 실패",
			"session_id", clip(sessionID, 64), "state", string(st), "error", err.Error())
	}
	return err
}

// Rekey 는 카드 하나의 cc_session_id 를 갈아끼운다.
//
// ★ 왜 서비스에 판단이 없나. 이 갈래는 "무엇이 옳은가"를 정하지 않는다 — 어느 카드를 어떤 cc 로
// 옮길지는 훅이 계보 대조로 이미 정했고, 여기서 다시 물으면 같은 판단이 두 자리에 산다.
// 서버가 지키는 것은 3중키 무결성뿐이고 그건 UNIQUE 가 한다.
//
// ★ 빈 cc 는 여기서 미리 거절한다. store.Rekey 도 같은 것을 막지만 그 오류는 평범한
// fmt.Errorf 라 ClassifyError 화이트리스트에 안 걸리고 500 으로 접힌다 — 화이트리스트를
// 넓히는 대신(그러면 그 갈래가 다른 오류까지 삼킬 수 있다) SetState 의 session_id 빈값 검사와
// 같은 자리에서 RefusedError 로 미리 접는다.
func (s *Service) Rekey(ctx context.Context, sessionID, ccSessionID string) (model.Session, error) {
	if strings.TrimSpace(ccSessionID) == "" {
		return model.Session{}, &RefusedError{What: "session rekey",
			Reason: "cc_session_id 가 비었다 — 정체 없는 카드를 만들 수 없다"}
	}
	out, err := s.st.Rekey(ctx, sessionID, ccSessionID)
	if err != nil {
		s.logFail(ctx, "session.rekey", "", sessionID, err, failAbout{})
		return model.Session{}, err
	}
	return out, nil
}

// FindSession 은 세션을 **찾기만** 한다. 없으면 만들지 않는다.
//
// OpenSession 과 달리 파생(branch·head·ahead)을 안 붙인다 — 이 조회를 부르는 자리는
// 복구 갈래이고 거기서 필요한 것은 "그 카드가 있느냐"와 그 id 뿐이다. 파생을 붙이면
// 가장 잦은 훅 경로에 git 호출이 는다.
func (s *Service) FindSession(ctx context.Context, machineID, worktree, ccSessionID string) (model.Session, error) {
	return s.st.FindSession(ctx, machineID, worktree, ccSessionID)
}

// SessionByID 는 카드 한 장을 **id 로** 읽는다. 좌표 해석이 없다.
//
// ★ 왜 3중키 조회 옆에 이것이 따로 필요한가. cc 축은 rekey 를 못 견딘다 —
// `/clear` 가 오면 훅이 카드의 cc 를 새 값으로 옮기는데(Rekey 는 cc 컬럼만 UPDATE 한다)
// 이미 뜬 MCP 프로세스의 environ 은 안 바뀐다. 그러면 그 프로세스가 인쇄한 cc 는
// **어떤 카드도 갖지 않은 값**이 되고 3중키 조회는 아무것도 못 찾는다.
// id 는 그 전환을 건너 보존되므로(설계 제약 ⑥) 사람이 카드를 지목할 수 있는 축은 그것뿐이다.
//
// FindSession 과 같은 이유로 파생(branch·head·ahead)을 안 붙인다 — 이 조회를 부르는 자리가
// 원하는 것은 "그 카드가 있느냐"와 **그것이 무엇을 쥐고 있느냐**뿐이다.
//
// ★ **선점을 함께 낸다.** 이 조회의 유일한 호출자(`fd close --session`)가 카드를 닫기 전에
// 반드시 물어야 하는 것이 그것이라서다. 따로 부르게 하면 두 호출 사이가 창이 되고,
// 무엇보다 **묻는 것을 잊은 갈래**가 가드를 우회한다 — OpenSession 이 Claims 를 같은
// 응답에 실어 보내는 것과 정확히 같은 이유다(그 응답 하나로 닫을지 판정한다).
func (s *Service) SessionByID(ctx context.Context, id string) (model.Session, []string, error) {
	sess, err := s.st.GetSession(ctx, id)
	if err != nil {
		return model.Session{}, nil, err
	}
	claims, err := s.st.ClaimedItems(ctx, sess.ID)
	if err != nil {
		// 선점 목록은 조정 정보라 파생이 아니다. 못 세면 그대로 올린다 —
		// 여기서 빈 목록으로 접으면 호출자가 "선점 0건"으로 읽고 카드를 닫는다.
		return model.Session{}, nil, err
	}
	return sess, claims, nil
}

// 시간 비교의 기준점. Board 가 자르는 지점을 만든다.
func (s *Service) cut(now time.Time, window time.Duration) time.Time {
	if window <= 0 {
		window = s.window
	}
	return now.Add(-window)
}
