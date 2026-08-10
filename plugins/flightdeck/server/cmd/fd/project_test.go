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
	if !strings.Contains(out, "판단") {
		t.Fatalf("출력이 삭제의 한계를 안 말한다\n%s", out)
	}
}
