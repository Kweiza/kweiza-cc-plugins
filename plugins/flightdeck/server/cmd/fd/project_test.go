package main

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// TestProjectLsPrintsAxisAndCounts 는 ls 의 출력 계약이다.
// 사람이 이 표를 보고 무엇을 보관하고 무엇을 지울지 정한다.
func TestProjectLsPrintsAxisAndCounts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	// ★ 브리프 원안은 h.project(harness 의 기본 프로젝트 id) 를 등록하지 않고 출력에서
	// 그 id 를 찾았다 — harness 는 프로젝트를 자동 등록하지 않는다
	// (claim_release_seam_test.go 의 deadClaim 이 같은 이유로 매번 UpsertProject 를 부른다).
	// 등록 없이는 ListProjects 가 h.project 를 안 내서 이 단정이 항상 실패한다.
	if err := h.st.UpsertProject(ctx, model.Project{
		ID: h.project, Path: "/tmp/" + h.project, DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	if err := h.st.UpsertProject(ctx, model.Project{
		ID: "junk", Path: "/tmp/junk", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	if err := h.st.SetProjectView(ctx, "junk", time.Time{}, time.Now().UTC()); err != nil {
		t.Fatalf("보관 실패: %v", err)
	}

	code, out := h.run("", "project", "ls")
	if code != 0 {
		t.Fatalf("종료코드 %d, 기대 0\n%s", code, out)
	}
	for _, want := range []string{h.project, "junk", "보관"} {
		if !strings.Contains(out, want) {
			t.Fatalf("출력에 %q 가 없다 — 사람이 이 표로 판단한다\n%s", want, out)
		}
	}
	// ★ 지울 수 있는지를 출력이 말해야 한다. 안 그러면 사람이 rm 을 쳐 보고서야 안다.
	//
	// "판단" 이 아니라 "지울 수 없다" 로 잰다 — "판단" 은 표 헤더(project.go:62 의 "판단"
	// 열 이름)에도 있어서, 이 삭제-한계 꼬리 문장 두 줄(project.go:81-82)을 통째로 지워도
	// 헤더의 "판단" 하나로 이 단정이 계속 통과했다(리뷰가 지적: 이 단정이 공회전했다).
	// "지울 수 없다" 는 꼬리 문장에만 있고 헤더·행 어디에도 안 나온다 — 검출력을
	// 실측으로 확인했다(아래 report 의 "돌린 명령" 절, project.go 의 81-82행을 지우고
	// 이 시험이 실제로 빨개지는지 봤다).
	if !strings.Contains(out, "지울 수 없다") {
		t.Fatalf("출력이 삭제의 한계를 안 말한다\n%s", out)
	}
}

// TestProjectRmNeedsReason 은 사유 없는 삭제를 CLI 가 먼저 막는다는 단정이다.
func TestProjectRmNeedsReason(t *testing.T) {
	h := newHarness(t)
	code, out := h.run("", "project", "rm", "--project", "junk")
	if code != 2 {
		t.Fatalf("종료코드 %d, 기대 2\n%s", code, out)
	}
	if !strings.Contains(out, "사유") {
		t.Fatalf("무엇이 없어서 막혔는지를 안 말한다\n%s", out)
	}
}

// TestProjectRmWithoutYesOnlyCounts 는 --yes 없이는 세기만 한다는 단정이다.
// 되돌릴 수 없는 일이라 이 한 단계가 이 명령의 절반이다.
//
// ★ 리뷰 #6: 브리프 원안은 자식 행이 하나도 없는 프로젝트를 썼다 — 그러면 카운트 루프가
// 한 줄도 안 찍혀 "무엇이 함께 지워질지 먼저 보여준다"는 이 명령의 절반이 어떤 시험에서도
// 0 아닌 값으로 검증된 적이 없었다. 세션을 하나 열어 표 이름과 수가 실제로 출력에
// 찍히는지, 그리고 event 는 안 지운다는 안내가 실제로 나오는지 함께 잰다.
func TestProjectRmWithoutYesOnlyCounts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.st.UpsertProject(ctx, model.Project{
		ID: "junk", Path: "/tmp/junk", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("등록 실패: %v", err)
	}
	if err := h.st.UpsertMachine(ctx, model.Machine{ID: "m1", Hostname: "h"}); err != nil {
		t.Fatalf("머신 등록 실패: %v", err)
	}
	if _, _, err := h.st.OpenSession(ctx, "junk", "m1", "/tmp/junk", "cc1", "", time.Time{}); err != nil {
		t.Fatalf("세션 열기 실패: %v", err)
	}

	code, out := h.run("", "project", "rm", "--project", "junk", "--reason", "워크트리 잔해다")
	if code != 1 {
		t.Fatalf("종료코드 %d, 기대 1(안 지웠다)\n%s", code, out)
	}
	if !strings.Contains(out, "--yes") {
		t.Fatalf("어떻게 실제로 지우는지를 안 말한다\n%s", out)
	}
	// 무엇이 함께 지워질지 먼저 보여주는 것이 이 명령의 절반이다 — 표 이름과 수가
	// 실제로 출력에 찍히는지 잰다(cmd/fd/project.go 의 "  %-20s %d\n" 형식).
	if !regexp.MustCompile(`(?m)^\s*session\s+1\s*$`).MatchString(out) {
		t.Fatalf("자식 행 카운트(session 1)가 출력에 안 보인다\n%s", out)
	}
	if !strings.Contains(out, "event 는 안 지운다") {
		t.Fatalf("event 를 안 지운다는 안내가 없다\n%s", out)
	}
	// ★ 실물 서버라 여기서 원장을 직접 본다 — "안 지웠다"가 출력이 아니라 사실이어야 한다.
	if _, err := h.st.GetProject(ctx, "junk"); err != nil {
		t.Fatalf("--yes 가 없는데 지워졌다: %v", err)
	}
}

// TestProjectRmRefusesUnknownProjectWithoutSuggestingYes 는 리뷰 #5 다: 등록되지 않은
// 프로젝트는 카운트가 전부 0이라 판정만 보면 통과하는데, 존재 확인이 없으면 "확인이
// 없다 … --yes 를 붙여라"가 그대로 나가 **없는 것을 지우라고 권하는** 꼴이 된다.
func TestProjectRmRefusesUnknownProjectWithoutSuggestingYes(t *testing.T) {
	h := newHarness(t)
	code, out := h.run("", "project", "rm", "--project", "no-such-project", "--reason", "오타 테스트")
	if code != 1 {
		t.Fatalf("종료코드 %d, 기대 1\n%s", code, out)
	}
	if strings.Contains(out, "--yes") {
		t.Fatalf("없는 프로젝트에 --yes 를 권했다\n%s", out)
	}
}

// TestProjectRmRefusesWhenJudgmentsExist 는 판단이 있으면 --yes 로도 안 지워진다는 단정이다.
// 이것은 정책이 아니라 원장이 정한 제약이다(judgment_no_delete + FK).
func TestProjectRmRefusesWhenJudgmentsExist(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	// ★ 브리프 원안은 h.project 를 등록하지 않고 곧장 h.svc.Note 를 불렀다 —
	// judgment.project 가 project(id) 를 FK 로 참조하므로(schema.sql:230), 등록 안 된
	// 프로젝트로 판단을 쓰면 AddJudgment 자체가 FK 위반으로 실패한다(TestProjectLsPrintsAxisAndCounts
	// 가 같은 이유로 이미 고쳐 둔 전제와 같다 — harness 는 프로젝트를 자동 등록하지 않는다).
	if err := h.st.UpsertProject(ctx, model.Project{
		ID: h.project, Path: "/tmp/" + h.project, DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	// 하네스의 기본 프로젝트에 판단을 하나 남긴다.
	// ★ 판단을 만드는 경로는 service 의 공개 API 를 쓴다 — store 직접 INSERT 로 만들면
	//   이 시험이 실제 사용 경로와 다른 모양의 행을 두고 단정하게 된다.
	if _, err := h.svc.Note(ctx, service.NoteInput{
		Project: h.project, Kind: model.JudgmentDecision,
		Title: "판단 하나", Body: "이 프로젝트는 지울 수 없어야 한다",
	}); err != nil {
		t.Fatalf("판단 남기기 실패: %v", err)
	}

	code, out := h.run("", "project", "rm", "--project", h.project,
		"--reason", "지워질 리 없다", "--yes")
	if code != 1 {
		t.Fatalf("종료코드 %d, 기대 1(거절)\n%s", code, out)
	}
	if !strings.Contains(out, "판단") {
		t.Fatalf("무엇이 막았는지를 안 말한다\n%s", out)
	}
	if _, err := h.st.GetProject(ctx, h.project); err != nil {
		t.Fatalf("판단이 있는데 지워졌다: %v", err)
	}
}

// TestProjectRmDeletesJunkProject 는 항목도 판단도 없는 프로젝트가 --yes 로 실제로
// 지워진다는 단정이다 — 이 명령의 존재 이유(잔해를 실제로 지운다)를 CLI 경로로도 잰다.
// store 쪽 실물 확인(TestRemoveProjectDeletesChildrenAndKeepsEvents)과 짝이다: 그쪽은
// store.RemoveProject 를 직접 재고, 이쪽은 CLI → REST → service → store 전체 이음매를 잰다.
func TestProjectRmDeletesJunkProject(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.st.UpsertProject(ctx, model.Project{
		ID: "junk", Path: "/tmp/junk", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("등록 실패: %v", err)
	}

	code, out := h.run("", "project", "rm", "--project", "junk",
		"--reason", "워크트리 잔해다", "--yes")
	if code != 0 {
		t.Fatalf("종료코드 %d, 기대 0(지웠다)\n%s", code, out)
	}
	if !strings.Contains(out, "지웠다") {
		t.Fatalf("지웠다는 사실을 안 말한다\n%s", out)
	}
	if _, err := h.st.GetProject(ctx, "junk"); err == nil {
		t.Fatal("--yes 를 줬는데 프로젝트가 그대로 있다")
	}
}

// TestProjectRmSurfacesBlockedRemovalReasonNotRawFK 는 리뷰 #2 를 **CLI 출력까지** 잰다.
// removalFKMessage(지금은 *store.RemovalBlockedError)가 번역한 사유가 실제로 사람에게
// 닿는지 — 이전에는 타입 없는 fmt.Errorf 라 internal/api 의 ClassifyError 가 못 가리고
// 마지막 default(500 + "서버 내부 오류다")로 떨어져, 공들여 쓴 한글 사유가 서버 로그에만
// 남고 CLI 는 "지우지 못했다: 서버 내부 오류다…"만 찍었다.
//
// ★ "other" 프로젝트의 랜딩 줄 행이 "junk" 의 세션을 가리키게 만든다 — landing_queue 는
// ProjectRefCounts 가 미리 안 세는 축이라(ProjectRefCounts 의 그 주석, 리뷰 #4) 사전
// 판정도 RemoveProject 안의 재-판정(리뷰 #1)도 못 잡고, 실제 DELETE 가 걸려야 2차 방어를
// 탄다 — TestRemoveProjectTranslatesForeignLandingQueueFKViolation 과 같은 픽스처다.
func TestProjectRmSurfacesBlockedRemovalReasonNotRawFK(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for _, id := range []string{"junk", "other"} {
		if err := h.st.UpsertProject(ctx, model.Project{
			ID: id, Path: "/tmp/" + id, DefaultBranch: "main",
		}); err != nil {
			t.Fatalf("등록 실패(%s): %v", id, err)
		}
	}
	if err := h.st.UpsertMachine(ctx, model.Machine{ID: "m1", Hostname: "h"}); err != nil {
		t.Fatalf("머신 등록 실패: %v", err)
	}
	sess, _, err := h.st.OpenSession(ctx, "junk", "m1", "/tmp/junk", "cc1", "", time.Time{})
	if err != nil {
		t.Fatalf("세션 열기 실패: %v", err)
	}
	if err := h.st.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.EnqueueLanding("other", sess.ID, time.Now().UTC())
		return err
	}); err != nil {
		t.Fatalf("랜딩 줄 등록 실패: %v", err)
	}

	code, out := h.run("", "project", "rm", "--project", "junk", "--reason", "지워 본다", "--yes")
	if code != 1 {
		t.Fatalf("종료코드 %d, 기대 1\n%s", code, out)
	}
	low := strings.ToLower(out)
	if strings.Contains(low, "foreign key") {
		t.Fatalf("드라이버 원문(FOREIGN KEY)이 새어 나왔다\n%s", out)
	}
	if strings.Contains(out, "서버 내부 오류") {
		t.Fatalf("500(내부 오류)으로 접혔다 — ClassifyError 가 RemovalBlockedError 를 "+
			"못 가렸다는 뜻이다\n%s", out)
	}
	if !strings.Contains(out, "참조 무결성") {
		t.Fatalf("무엇이 막았는지를 안 말한다\n%s", out)
	}
	// junk 프로젝트는 실패 뒤에도 그대로 있어야 한다(트랜잭션 롤백).
	if _, gerr := h.st.GetProject(ctx, "junk"); gerr != nil {
		t.Fatalf("실패했는데 프로젝트가 지워졌다: %v", gerr)
	}
}
