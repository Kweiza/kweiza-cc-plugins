package mcpsrv

import (
	"context"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 이 파일은 `amend` 도구의 **본문(RenderAmend)과 꼬리(RenderTail)가 같은 사실을
// 말하는지**를 실제 tools/call 왕복으로 잠근다.
//
// render_amend_test.go 는 RenderAmend 만 부른다 — 꼬리는 toolAmend(mcpsrv.go)가
// 따로 조립하므로 그 시험은 원리적으로 본문·꼬리 불일치를 못 본다. 리뷰 I-1 이
// 잡은 결함과 재리뷰가 잡은 네 번째 상태(paths: [] + 겹침 파생 실패)가 둘 다 이
// 이음매에서 났다 — 그래서 여기서 왕복 전체를 시험한다.
//
// 네 상태를 전부 덮는다: ① paths 를 안 고침 ② paths 를 빈 목록으로 고침(계산을
// 안 돌린다, service.AmendItem 의 가드) ③ paths 를 고쳤는데 겹침 파생이 실패함
// ④ paths 를 고쳤고 계산도 성공해 겹침이 실제로 있음.

// setupAmendServer 는 항목 하나와 그 프로젝트가 있는 서버·저장소·서비스를 만든다.
func setupAmendServer(t *testing.T) (srv *Server, svc *service.Service, repo string) {
	t.Helper()
	repo = newRepo(t)
	svc, _ = newSvc(t)
	ctx := context.Background()

	// 프로젝트를 먼저 등록해야 AddItem 의 FK 가 산다(OpenSession 이 project 행을 만든다).
	if _, err := svc.OpenSession(ctx, service.OpenSessionInput{
		Project: "repo", ProjectPath: repo, MachineID: "m1", Hostname: "h",
		Worktree: repo, CCSessionID: "cc-seed",
	}); err != nil {
		t.Fatalf("씨앗 세션 열기 실패: %v", err)
	}

	if _, err := svc.AddItem(ctx, service.AddItemInput{
		Project: "repo", ID: "it1", Title: "제목", Body: "본문", Paths: []string{"old/"},
	}); err != nil {
		t.Fatalf("항목 등록 실패: %v", err)
	}

	return newServer(t, svc, repo, fullEnv(repo)), svc, repo
}

// TestAmendBodyAndTailAgreeWhenPathsUnchanged 는 ① paths 를 안 고쳤을 때
// 본문이 겹침을 언급 안 하고 꼬리가 "안 읽었다"를 말하는지 본다 — 둘 다
// "이 축은 안 건드렸다"는 같은 사실이다.
func TestAmendBodyAndTailAgreeWhenPathsUnchanged(t *testing.T) {
	srv, _, _ := setupAmendServer(t)
	frames := serve(t, srv, call("amend", map[string]any{
		"item_id": "it1", "title": "새 제목", "reason": "제목만",
	}))
	body, isErr := toolText(t, frames[0])
	if isErr {
		t.Fatalf("amend 가 실패했다:\n%s", body)
	}
	// ★ "── 꼬리 ──" 가 render.go 가 쓰는 실제 경계 표식이다(RenderTail) — 본문만
	// 떼어 보지 않으면 꼬리의 "경로 축을 읽지 않았다"는 안내문 자체가 "경로"라는
	// 낱말을 담고 있어 단정이 오탐한다.
	renderBody, _, _ := strings.Cut(body, "── 꼬리 ──")
	if strings.Contains(renderBody, "경로") {
		t.Errorf("paths 를 안 고쳤는데 본문이 경로를 언급한다:\n%s", renderBody)
	}
	if !strings.Contains(body, "겹침: 이 도구는 경로 축을 읽지 않았다") {
		t.Errorf("꼬리가 '안 읽었다'를 안 말한다:\n%s", body)
	}
}

// TestAmendBodyAndTailAgreeWhenPathsEmptied 는 ② paths 를 빈 목록으로 고쳤을 때
// (재리뷰 4번째 상태) 본문·꼬리가 부딪히지 않는지 본다.
//
// service.AmendItem 이 이 상태에서 겹침 계산 자체를 안 돌리므로(amend.go 의 가드),
// hasFailureAxis 가 거짓이라 toolAmend 의 꼬리 갈래는 default(observed:true, 빈
// overlaps)로 떨어진다 — 꼬리가 "겹침: 없음"을 내고, 본문은 "경로가 없으면 겹침
// 축에 안 잡힌다"를 낸다. 둘 다 "0"을 말하되 **다른 근거**(하나는 "셀 것이 없다",
// 하나는 "셌더니 0")를 갖는다 — 그 근거가 서로 부정하지 않는지가 이 시험의 핵심이다.
func TestAmendBodyAndTailAgreeWhenPathsEmptied(t *testing.T) {
	srv, svc, _ := setupAmendServer(t)

	// 실제로 계산을 시도하면 반드시 실패하는 상태를 만든다(재현 모양은
	// service/amend_test.go 의 TestAmendSkipsOverlapComputationWhenPathsAreEmptied 와
	// 같다) — 그런데도 이 상태에서는 계산 자체가 안 돌아야 한다.
	if _, err := svc.Store().DB().ExecContext(context.Background(), "DROP TABLE signal"); err != nil {
		t.Fatalf("signal 표 제거 실패: %v", err)
	}

	frames := serve(t, srv, call("amend", map[string]any{
		"item_id": "it1", "paths": []string{}, "reason": "경로를 비운다",
	}))
	body, isErr := toolText(t, frames[0])
	if isErr {
		t.Fatalf("amend 가 실패했다 — signal 표 제거는 item 과 무관해야 한다:\n%s", body)
	}
	if !strings.Contains(body, "경로 0 — 경로가 없으면 이 항목은 겹침 축에 안 잡힌다") {
		t.Errorf("본문이 경로 0 을 RenderAdd 와 다르게 말한다:\n%s", body)
	}
	if !strings.Contains(body, "겹침: 없음") {
		t.Errorf("꼬리가 '없음'을 안 말한다 — 계산을 안 돌렸으면 observed:true·빈 overlaps 여야 한다:\n%s", body)
	}
	// ★ 이것이 재리뷰가 잡은 그 모순이다 — 나오면 안 된다.
	if strings.Contains(body, "못 셌다") {
		t.Errorf("경로가 없는데 '못 셌다'를 낸다 — 계산을 시도했다는 뜻이고, 그러면 본문의 '볼 것도 없다'와 부딪힌다:\n%s", body)
	}
}

// TestAmendBodyAndTailAgreeWhenOverlapDerivationFails 는 ③ paths 를 고쳤고 새
// 경로가 있는데 겹침 파생이 실패했을 때, 본문·꼬리 둘 다 "못 셌다"를 말하고
// 어느 쪽도 "0" 이라고 넘겨짚지 않는지 본다.
//
// I-1 원 리뷰가 고발한 결함과 같은 자리이고, render_amend_test.go 만으로는 이
// 왕복(toolAmend 의 꼬리 조립)을 못 본다 — 그래서 여기서 다시 잠근다.
func TestAmendBodyAndTailAgreeWhenOverlapDerivationFails(t *testing.T) {
	srv, svc, _ := setupAmendServer(t)

	if _, err := svc.Store().DB().ExecContext(context.Background(), "DROP TABLE signal"); err != nil {
		t.Fatalf("signal 표 제거 실패: %v", err)
	}

	frames := serve(t, srv, call("amend", map[string]any{
		"item_id": "it1", "paths": []string{"new/"}, "reason": "파생 실패 시연",
	}))
	body, isErr := toolText(t, frames[0])
	if isErr {
		t.Fatalf("amend 가 실패했다 — 겹침 파생 실패가 결과를 죽이면 안 된다:\n%s", body)
	}
	if !strings.Contains(body, "못 셌다") {
		t.Errorf("본문이 '못 셌다'를 안 말한다:\n%s", body)
	}
	// 꼬리도 같은 사실을 말해야 한다 — overlapsNote 가 실린 자리다.
	if !strings.Contains(body, "이 수정으로 겹치게 된 세션을 못 셌다(overlaps 축 파생 실패)") {
		t.Errorf("꼬리가 파생 실패 사유를 안 말한다:\n%s", body)
	}
	if strings.Contains(body, "겹침: 없음") || strings.Contains(body, "지금 이 경로를 만지는 다른 세션은 없다") {
		t.Errorf("못 셌는데 0건인 것처럼 말한다:\n%s", body)
	}
}

// TestAmendBodyAndTailAgreeWhenOverlapsAreNonZero 는 ④ paths 를 고쳤고 계산도
// 성공해 실제로 겹침이 있을 때, 본문·꼬리가 같은 개수·같은 결론을 내는지 본다.
func TestAmendBodyAndTailAgreeWhenOverlapsAreNonZero(t *testing.T) {
	srv, svc, repo := setupAmendServer(t)
	ctx := context.Background()

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

	frames := serve(t, srv, call("amend", map[string]any{
		"item_id": "it1", "paths": []string{"shared/x.go"}, "reason": "겹침 시연",
	}))
	body, isErr := toolText(t, frames[0])
	if isErr {
		t.Fatalf("amend 가 실패했다:\n%s", body)
	}
	if !strings.Contains(body, "세션 1개와 경로가 겹친다") {
		t.Errorf("본문이 겹침 개수를 실문구로 안 말한다:\n%s", body)
	}
	if !strings.Contains(body, "겹침 1건") {
		t.Errorf("꼬리가 같은 개수를 안 말한다:\n%s", body)
	}
	if strings.Contains(body, "겹침: 없음") || strings.Contains(body, "못 셌다") {
		t.Errorf("겹침이 있는데 없다거나 못 셌다고 말한다:\n%s", body)
	}
}
