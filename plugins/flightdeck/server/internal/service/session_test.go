package service

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

func TestOpenSessionResumesSameTripleAndSplitsOnNewCCSession(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)

	first := openSession(t, s, "p", repo, repo, "cc-1", "트랙2")
	if !first.Created {
		t.Fatalf("첫 호출은 신규여야 한다: %+v", first.Session)
	}

	// 재개 — 같은 3중키면 같은 세션이다. 컨텍스트가 날아가 같은 워크트리로 돌아온 경우다.
	again := openSession(t, s, "p", repo, repo, "cc-1", "트랙2")
	if again.Created {
		t.Fatalf("같은 3중키 재호출은 신규가 아니어야 한다")
	}
	if again.Session.ID != first.Session.ID {
		t.Fatalf("같은 3중키인데 세션 id 가 갈렸다: %s vs %s", again.Session.ID, first.Session.ID)
	}

	// cc_session_id 가 다르면 **새 세션**이다. 워크트리 경로는 규율상 재사용되므로
	// 경로만 키로 쓰면 옛 세션 행과 합쳐진다.
	other := openSession(t, s, "p", repo, repo, "cc-2", "트랙3")
	if !other.Created {
		t.Fatalf("cc_session_id 가 다르면 신규여야 한다")
	}
	if other.Session.ID == first.Session.ID {
		t.Fatalf("cc_session_id 가 다른데 같은 세션으로 합쳐졌다: %s", other.Session.ID)
	}

	// 파생 — 브랜치·HEAD 는 인자가 아니라 git 에서 온다.
	if first.Branch != "main" {
		t.Fatalf("브랜치가 파생되지 않았다: %q (실패: %+v)", first.Branch, first.Failures)
	}
	if first.HeadSHA == "" {
		t.Fatalf("HEAD sha 가 비었다 (실패: %+v)", first.Failures)
	}
	if first.Freshness.Source != "git" || first.Freshness.Stale {
		t.Fatalf("git 을 다 읽었는데 신선도가 %+v 다", first.Freshness)
	}
	// 프로젝트가 자동 등록되고 기본 브랜치가 파생됐는가
	if first.Project.DefaultBranch != "main" || first.Project.Path != repo {
		t.Fatalf("프로젝트 파생이 틀렸다: %+v", first.Project)
	}
}

func TestOpenSessionRefusesIncompleteIdentityWithReason(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	base := OpenSessionInput{
		Project: "p", ProjectPath: repo, MachineID: "m1",
		Worktree: repo, CCSessionID: "cc-1",
	}
	cases := []struct {
		name   string
		mutate func(*OpenSessionInput)
		wantIn string
	}{
		{"project 없음", func(in *OpenSessionInput) { in.Project = "" }, "project"},
		{"machine 없음", func(in *OpenSessionInput) { in.MachineID = "" }, "machine_id"},
		{"worktree 없음", func(in *OpenSessionInput) { in.Worktree = "" }, "worktree"},
		{"cc_session 없음", func(in *OpenSessionInput) { in.CCSessionID = "" }, "cc_session_id"},
		// 표 밖 케이스 — 값은 있지만 상대경로다. 서버와 세션이 서로 다른 곳을 가리키게 된다.
		{"worktree 가 상대경로", func(in *OpenSessionInput) { in.Worktree = "relative/dir" }, "절대경로"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			c.mutate(&in)
			_, err := s.OpenSession(ctx(), in)
			if err == nil {
				t.Fatalf("거절돼야 한다")
			}
			if !strings.Contains(err.Error(), c.wantIn) {
				t.Fatalf("거절 사유에 %q 가 없다: %v", c.wantIn, err)
			}
		})
	}
}

func TestOpenSessionSurvivesGitFailureButSaysSo(t *testing.T) {
	s, _ := newSvc(t)
	dir := tmpBase(t) // git 저장소가 아니다

	res := openSession(t, s, "p", dir, dir, "cc-1", "트랙2")
	if res.Session.ID == "" {
		t.Fatalf("git 이 없다고 세션 등록이 죽으면 안 된다")
	}
	if res.Branch != "" || res.HeadSHA != "" {
		t.Fatalf("못 읽은 파생을 지어냈다: branch=%q head=%q", res.Branch, res.HeadSHA)
	}
	if !res.Freshness.Stale || res.Freshness.Source != "db" {
		t.Fatalf("파생 실패가 신선도에 안 나타났다: %+v", res.Freshness)
	}
	if len(res.Failures) == 0 {
		t.Fatalf("실패 사유가 비었다 — 침묵하면 '값이 없다'와 '이 축을 안 본다'가 구분되지 않는다")
	}
	var axes []string
	for _, f := range res.Failures {
		axes = append(axes, f.Axis)
		if f.Detail == "" {
			t.Fatalf("축 %q 에 사유 전문이 없다", f.Axis)
		}
	}
	if !contains(axes, "refs") || !contains(axes, "worktrees") {
		t.Fatalf("못 읽은 축이 이름으로 안 나왔다: %v", axes)
	}
}

func TestBeatNormalizesAbsolutePathsToRepoRelative(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-1", "트랙2")

	abs := filepath.Join(repo, "tools", "x.sh")
	if err := s.Beat(ctx(), sess.Session.ID, model.SignalTool, []string{abs}); err != nil {
		t.Fatalf("비트 실패: %v", err)
	}

	paths, err := st.FootprintPaths(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("발자국 조회 실패: %v", err)
	}
	want := filepath.Join("tools", "x.sh")
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("발자국 = %v, 기대 [%s] — 절대경로를 그대로 두면 git 이 주는 상대경로와 "+
			"좌표계가 달라 겹침 축이 조용히 죽는다", paths, want)
	}

	sig, err := st.Signals(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("신호 조회 실패: %v", err)
	}
	if _, ok := sig[model.SignalTool]; !ok {
		t.Fatalf("tool 신호가 안 남았다: %v", sig)
	}
	// 신호 넷을 **합치지 않는다** — prompt 는 안 왔으므로 키가 없어야 한다.
	if _, ok := sig[model.SignalPrompt]; ok {
		t.Fatalf("오지 않은 prompt 신호가 생겼다 — 0값과 부재를 뭉개면 나이 표시가 거짓이 된다")
	}
}

func TestBeatRefusesUnknownSignalKind(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-1", "")
	err := s.Beat(ctx(), sess.Session.ID, model.SignalKind("heartbeat"), nil)
	if err == nil || !strings.Contains(err.Error(), "heartbeat") {
		t.Fatalf("열거 밖 신호는 사유와 함께 거절돼야 한다: %v", err)
	}
}

func TestSetStateRequiresReasonForBlocked(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-1", "")

	if err := s.SetState(ctx(), sess.Session.ID, model.SessionBlocked, ""); err == nil {
		t.Fatalf("blocked 에 사유가 없으면 거절돼야 한다")
	} else if !strings.Contains(err.Error(), "사유") {
		t.Fatalf("거절 사유가 공허하다: %v", err)
	}

	if err := s.SetState(ctx(), sess.Session.ID, model.SessionBlocked, "계약 세션의 개정 대기"); err != nil {
		t.Fatalf("사유가 있으면 통과해야 한다: %v", err)
	}
	got, err := st.GetSession(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("세션 조회 실패: %v", err)
	}
	if got.State != model.SessionBlocked || got.BlockedWhy != "계약 세션의 개정 대기" {
		t.Fatalf("상태가 안 남았다: %+v", got)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
