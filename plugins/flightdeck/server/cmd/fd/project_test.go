package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
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
func TestProjectRmWithoutYesOnlyCounts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.st.UpsertProject(ctx, model.Project{
		ID: "junk", Path: "/tmp/junk", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("등록 실패: %v", err)
	}

	code, out := h.run("", "project", "rm", "--project", "junk", "--reason", "워크트리 잔해다")
	if code != 1 {
		t.Fatalf("종료코드 %d, 기대 1(안 지웠다)\n%s", code, out)
	}
	if !strings.Contains(out, "--yes") {
		t.Fatalf("어떻게 실제로 지우는지를 안 말한다\n%s", out)
	}
	// ★ 실물 서버라 여기서 원장을 직접 본다 — "안 지웠다"가 출력이 아니라 사실이어야 한다.
	if _, err := h.st.GetProject(ctx, "junk"); err != nil {
		t.Fatalf("--yes 가 없는데 지워졌다: %v", err)
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
