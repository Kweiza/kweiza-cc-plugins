package service

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// Windows 경로를 주면 "절대경로가 아니다"가 아니라 **원인**을 말해야 한다.
// 사용자는 자기가 준 것이 절대경로라고 알고 있어서, 그 사유로는 고칠 수 없다.
func TestJudgeOpenSessionNamesWindowsPathAsTheCause(t *testing.T) {
	base := OpenSessionInput{
		Project: "p", MachineID: "m", CCSessionID: "cc",
	}
	cases := []struct {
		name     string
		worktree string
		want     string
	}{
		{"드라이브 절대경로", `C:\Users\a\repo`, "드라이브 절대경로"},
		{"UNC", `\\host\share\repo`, "UNC"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			in.Worktree = c.worktree
			v := JudgeOpenSession(in)
			if v.OK {
				t.Fatal("Windows 경로를 통과시켰다")
			}
			if !strings.Contains(v.Reason, c.want) {
				t.Fatalf("사유 %q 가 원인(%q)을 안 짚는다", v.Reason, c.want)
			}
			if !strings.Contains(v.Reason, "WSL") {
				t.Errorf("사유에 처방(WSL)이 없다: %s", v.Reason)
			}
		})
	}
}

// 좌표계 판정이 기존 축을 가리면 안 된다 — 상대경로는 여전히 상대경로 사유를 받는다.
func TestJudgeOpenSessionKeepsExistingAxes(t *testing.T) {
	base := OpenSessionInput{Project: "p", MachineID: "m", CCSessionID: "cc"}

	in := base
	in.Worktree = "relative/path"
	if v := JudgeOpenSession(in); v.OK || !strings.Contains(v.Reason, "절대경로") {
		t.Errorf("상대경로 사유가 바뀌었다: ok=%v reason=%s", v.OK, v.Reason)
	}

	in = base
	in.Worktree = ""
	if v := JudgeOpenSession(in); v.OK || !strings.Contains(v.Reason, "worktree 가 비었다") {
		t.Errorf("빈 worktree 사유가 바뀌었다: ok=%v reason=%s", v.OK, v.Reason)
	}

	in = base
	in.Worktree = "/home/a/repo"
	if v := JudgeOpenSession(in); !v.OK {
		t.Errorf("정상 POSIX 경로를 거절했다: %s", v.Reason)
	}
}

// Beat 는 훅이 부른다. 좌표계가 틀린 경로가 섞여 와도 신호 자체는 살아야 한다 —
// 여기서 오류를 내면 세션이 보드에서 사라지고 그 인과를 아무도 못 짚는다.
// 좋은 경로는 그대로 들어가고, 나쁜 것만 조용히가 아니라 **원장에 건수를 남기고** 빠진다.
func TestBeatDropsBadCoordinatePathsButKeepsSignal(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-beat-coord", "좌표계")

	good := filepath.Join(repo, "tools", "x.sh")
	if err := s.Beat(ctx(), sess.Session.ID, model.SignalTool, []string{
		good,
		`C:\other\y.go`,
		`z\w.go`,
	}); err != nil {
		t.Fatalf("좌표계가 틀린 경로 때문에 신호가 죽었다: %v", err)
	}

	// 신호는 살아 있다.
	sig, err := st.Signals(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("신호 조회 실패: %v", err)
	}
	if _, ok := sig[model.SignalTool]; !ok {
		t.Fatalf("tool 신호가 안 남았다 — 나쁜 경로 하나가 신호 전체를 죽였다: %v", sig)
	}

	// ★ 이름이 약속한 셋 중 나머지 둘 — "좋은 경로가 실제로 발자국으로 남았는가"와
	// "원장에 버린 건수가 기록됐는가" — 도 함께 본다. 신호만 보면 필터가 전부 버려도
	// 이 시험은 초록이었다.
	fps, err := st.FootprintPaths(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("발자국 조회 실패: %v", err)
	}
	if len(fps) != 1 || fps[0] != "tools/x.sh" {
		t.Fatalf("좋은 경로가 발자국으로 안 남았다: %v", fps)
	}

	// 원장 — session.beat payload 에 rejected 건수와 kept 건수가 남았는지.
	// 선례: internal/service/prescribe_test.go:36 의 ListSessionEvents 사용을 그대로 따른다.
	evs, err := st.ListSessionEvents(ctx(), sess.Session.ID, "session.beat", time.Time{})
	if err != nil {
		t.Fatalf("이벤트 조회 실패: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("session.beat 이벤트가 %d건이다, want 1건: %+v", len(evs), evs)
	}
	payload := evs[0].Payload
	if !strings.Contains(payload, `"rejected":2`) {
		t.Fatalf("원장에 버린 건수(2)가 안 남았다: %s", payload)
	}
	if !strings.Contains(payload, `"count":1`) {
		t.Fatalf("원장에 통과 건수(1)가 안 남았다: %s", payload)
	}
	// ★ 버린 경로 자체도 남아야 한다(스펙 §4.2 정정 — 살아 있는 유일한 표면인 Beat 에서
	// 버린 경로가 어디에도 안 남는 문제). 사유 전체는 안 실어도 되지만 경로는 실어야 한다.
	// payload 는 JSON 이라 백슬래시가 이스케이프된다(`\` → `\\`) — 원본 경로가 아니라
	// JSON 인코딩된 형태로 단정한다.
	if !strings.Contains(payload, `dropped_paths`) {
		t.Fatalf("원장에 dropped_paths 필드가 없다: %s", payload)
	}
	if !strings.Contains(payload, `C:\\other\\y.go`) {
		t.Fatalf("원장에 버린 경로가 안 남았다: %s", payload)
	}
	if !strings.Contains(payload, `z\\w.go`) {
		t.Fatalf("원장에 버린 경로 둘째 건이 안 남았다: %s", payload)
	}
}

// 세션을 열거나 상태를 바꾸는 것은 **도구 호출이 아니다.**
//
// 그 둘이 mcp 를 찍던 동안 화면은 "mcp 0초"라고 내면서 실제로는 MCP 도구를 한 번도
// 안 부른 세션을 가리켰다 — 설계 §4 의 신호 표가 mcp 를 "도구 호출"로 정의하는데
// 그것과 어긋났다(실측: 카드 26장 중 16장이 mcp 하나뿐이었다).
//
// ★ **다른 종류로 옮겼는지가 아니라 안 찍는지를 본다.** 종류를 더하려면 schema.sql 의
// CHECK 를 바꿔야 하고, SQLite 에서 그것은 표 재생성이며, 재생성은 declaredTables 와
// destructiveOps 양쪽 가드에 걸린다. "언제 열렸나"는 session.opened_at 이 이미 담고,
// ListLive 의 창 판정도 그 컬럼을 따로 본다 — signal 표에 같은 사실을 두 벌 둘 이유가 없다.
func TestOpenSessionAndSetStateLeaveNoSignal(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-quiet", "조용")

	sig, err := st.Signals(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("신호 조회 실패: %v", err)
	}
	if len(sig) != 0 {
		t.Fatalf("세션을 열기만 했는데 신호가 %v 다 — 화면이 '이 세션이 도구를 불렀다'고 거짓말한다", sig)
	}

	if err := s.SetState(ctx(), sess.Session.ID, model.SessionBlocked, "막힌 사유"); err != nil {
		t.Fatalf("상태 변경 실패: %v", err)
	}
	sig, err = st.Signals(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("신호 조회 실패: %v", err)
	}
	if _, ok := sig[model.SignalMCP]; ok {
		t.Fatalf("상태를 바꿨더니 mcp 가 생겼다 — 상태 전이는 도구 호출이 아니다: %v", sig)
	}
}
