package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
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
