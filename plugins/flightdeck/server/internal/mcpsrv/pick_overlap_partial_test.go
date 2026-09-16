package mcpsrv

import (
	"context"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// TestPickTailSaysOverlapPartialWhenRosterFails 는 pick 이 liveOverlapSessions
// (board.go)를 AmendItem 과 공유한다는 사실을 그대로 잰다 — amend_tail_test.go 의
// TestAmendBodyAndTailAgreeWhenRosterFails 와 같은 실패 실물(project_member 표 제거)을
// pick 경로에 태워, 두 동사가 같은 답(같은 셋째 상태)을 내는지 본다.
//
// toolPick 은 RenderPick 본문에 겹침 목록을 안 싣는다 — 그 절은 전부 꼬리(RenderTail)
// 몫이다(mcpsrv.go 의 toolPick 이 tailOpts 를 조립하는 자리). 그래서 이 시험은 몸이
// 아니라 꼬리에서 셋째 상태를 확인한다.
func TestPickTailSaysOverlapPartialWhenRosterFails(t *testing.T) {
	repo := newRepo(t)
	svc, st := newSvc(t)
	ctx := context.Background()

	// 프로젝트를 먼저 등록해야 AddItem 의 FK 가 산다(OpenSession 이 project 행을 만든다).
	if _, err := svc.OpenSession(ctx, service.OpenSessionInput{
		Project: "repo", ProjectPath: repo, MachineID: "m1", Hostname: "h",
		Worktree: repo, CCSessionID: "cc-seed",
	}); err != nil {
		t.Fatalf("씨앗 세션 열기 실패: %v", err)
	}
	if _, err := svc.AddItem(ctx, service.AddItemInput{
		Project: "repo", ID: "it1", Title: "제목", Body: "본문", Paths: []string{"shared/x.go"},
	}); err != nil {
		t.Fatalf("항목 등록 실패: %v", err)
	}

	// 이 프로젝트 안에 실제 겹침 하나를 만든다 — 명부가 죽어도 이 프로젝트 것은
	// 그대로 세어야 한다는 것이 이 시험의 핵심이다.
	other, err := svc.OpenSession(ctx, service.OpenSessionInput{
		Project: "repo", ProjectPath: repo, MachineID: "m1", Hostname: "h",
		Worktree: repo, CCSessionID: "cc-other",
	})
	if err != nil {
		t.Fatalf("남 세션 열기 실패: %v", err)
	}
	if err := svc.Beat(ctx, other.Session.ID, model.SignalTool, []string{"shared/x.go"}); err != nil {
		t.Fatalf("남 세션 신호 실패: %v", err)
	}

	// Roster 조회만 부순다(project_member 표) — sessionCards 가 쓰는 signal 표는 안 건드린다.
	if _, err := st.DB().ExecContext(ctx, "DROP TABLE project_member"); err != nil {
		t.Fatalf("project_member 표 제거 실패: %v", err)
	}

	srv := newServer(t, svc, repo, fullEnv(repo))
	frames := serve(t, srv, call("pick", map[string]any{"item_id": "it1"}))
	body, isErr := toolText(t, frames[0])
	if isErr {
		t.Fatalf("pick 이 실패했다 — 명부 조회 실패가 결과를 죽이면 안 된다:\n%s", body)
	}
	if !strings.Contains(body, "겹침 1건") {
		t.Errorf("이 프로젝트 안의 겹침(1건)이 꼬리에 안 보인다:\n%s", body)
	}
	if !strings.Contains(body, "형제 프로젝트는 못 봤다") {
		t.Errorf("덜 쟀다는 사실이 꼬리에 안 붙었다:\n%s", body)
	}
	if strings.Contains(body, "겹침: 없음") {
		t.Errorf("이 프로젝트 안에 실제 겹침이 있는데 '없음'을 말한다:\n%s", body)
	}
}
