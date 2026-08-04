package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// twoProjects 는 등록된 프로젝트 둘을 만든다.
//
// ★ 세션을 여는 것이 곧 프로젝트 등록이다 — OpenSession 이 GetProject 가 ErrNotFound 일 때
// UpsertProject 한다. 그래서 프로젝트를 따로 등록하는 헬퍼가 이 패키지에 없다.
func twoProjects(t *testing.T, s *Service) (a, b model.Project) {
	t.Helper()
	repoA, repoB := newRepo(t), newRepo(t)
	openSession(t, s, "proj-a", repoA, repoA, "cc-a", "")
	openSession(t, s, "proj-b", repoB, repoB, "cc-b", "")
	pa, err := s.st.GetProject(ctx(), "proj-a")
	if err != nil {
		t.Fatalf("proj-a 조회 실패: %v", err)
	}
	pb, err := s.st.GetProject(ctx(), "proj-b")
	if err != nil {
		t.Fatalf("proj-b 조회 실패: %v", err)
	}
	return pa, pb
}

func TestCheckItemPathsSeesPresentPaths(t *testing.T) {
	s, _ := newSvc(t)
	pa, _ := twoProjects(t, s)
	writeFile(t, pa.Path, "internal/x/y.go", "package x\n")

	v := s.checkItemPaths(ctx(), pa, []string{"internal/x/y.go"})
	if v == nil {
		t.Fatal("판정이 nil 이다 — 이 함수는 nil 을 안 낸다")
	}
	if v.Kind != judge.KindOK {
		t.Fatalf("Kind 가 %q 다 — ok 여야 한다: %s", v.Kind, v.Summary)
	}
}

func TestCheckItemPathsNamesTheOneProjectThatHasThem(t *testing.T) {
	s, _ := newSvc(t)
	pa, pb := twoProjects(t, s)
	writeFile(t, pb.Path, "plugins/flightdeck/server/cmd/fd/migrate.go", "package main\n")

	// pa 에는 없고 pb 에만 있다 → 유일 지목 → 오등록.
	v := s.checkItemPaths(ctx(), pa, []string{"plugins/flightdeck/server/cmd/fd/migrate.go"})
	if v.Kind != judge.KindMisregistered {
		t.Fatalf("Kind 가 %q 다 — misregistered 여야 한다: %s", v.Kind, v.Summary)
	}
	if v.Suggest != pb.ID {
		t.Fatalf("Suggest 가 %q 다 — %q 여야 한다", v.Suggest, pb.ID)
	}
}

func TestCheckItemPathsDoesNotAccuseWhenBothProjectsHaveTheName(t *testing.T) {
	s, _ := newSvc(t)
	pa, pb := twoProjects(t, s)
	// `docs/` 모양 — 흔한 이름이라 여러 레포에 있다. pa 에만 없다.
	writeFile(t, pb.Path, "docs/keep.md", "x\n")
	third := newRepo(t)
	openSession(t, s, "proj-c", third, third, "cc-c", "")
	writeFile(t, third, "docs/keep.md", "x\n")

	v := s.checkItemPaths(ctx(), pa, []string{"docs/"})
	if v.Kind != judge.KindAmbiguous {
		t.Fatalf("Kind 가 %q 다 — ambiguous 여야 한다: %s", v.Kind, v.Summary)
	}
	if v.Suggest != "" {
		t.Fatalf("여럿이 지목됐는데 Suggest 가 %q 로 찍혔다", v.Suggest)
	}
}

func TestCheckItemPathsSaysNowhereWhenNoProjectHasThem(t *testing.T) {
	s, _ := newSvc(t)
	pa, _ := twoProjects(t, s)

	// 실물 결함 모양: 뿌리가 잘린 경로.
	v := s.checkItemPaths(ctx(), pa, []string{"internal/service/service.go"})
	if v.Kind != judge.KindNowhere {
		t.Fatalf("Kind 가 %q 다 — nowhere 여야 한다: %s", v.Kind, v.Summary)
	}
}

// ★ 이 시험이 이 태스크의 핵심이다.
// 루트를 따로 재지 않으면 죽은 프로젝트의 경로가 전부 ErrNotExist 로 와서
// "없다"로 접히고, 그 항목이 nowhere 나 misregistered 로 **고발당한다**.
func TestCheckItemPathsDistinguishesUnreadableRootFromAbsentPath(t *testing.T) {
	s, _ := newSvc(t)
	pa, _ := twoProjects(t, s)
	if err := os.RemoveAll(pa.Path); err != nil {
		t.Fatalf("저장소 제거 실패: %v", err)
	}

	v := s.checkItemPaths(ctx(), pa, []string{"internal/x/y.go"})
	if v.Kind != judge.KindUnknown {
		t.Fatalf("Kind 가 %q 다 — unknown 이어야 한다(루트가 없다): %s", v.Kind, v.Summary)
	}
	if strings.Contains(v.Summary, "없다 —") || strings.Contains(v.Summary, "미등록") {
		t.Fatalf("루트를 못 읽었는데 '없다'로 단정했다: %s", v.Summary)
	}
}

// 다른 프로젝트의 루트가 죽었으면 그 이름이 Unreadable 에 남아야 한다.
// 안 남기면 "유일 지목"이 실제보다 강해 보인다.
func TestCheckItemPathsReportsUnreadableOtherProjects(t *testing.T) {
	s, _ := newSvc(t)
	pa, pb := twoProjects(t, s)
	if err := os.RemoveAll(pb.Path); err != nil {
		t.Fatalf("저장소 제거 실패: %v", err)
	}

	v := s.checkItemPaths(ctx(), pa, []string{"internal/x/y.go"})
	found := false
	for _, u := range v.Unreadable {
		if u == pb.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("못 읽은 프로젝트 %q 가 Unreadable 에 없다: %+v", pb.ID, v.Unreadable)
	}
	if !strings.Contains(v.Summary, pb.ID) {
		t.Fatalf("못 읽었다는 사실이 문장에 없다: %s", v.Summary)
	}
}

// `..` 는 정규화하지 않고 거절한다. filepath.Join 에 그대로 stat 하면 프로젝트 밖을 관측한다.
func TestCheckItemPathsRefusesToLookOutsideTheRoot(t *testing.T) {
	s, _ := newSvc(t)
	pa, _ := twoProjects(t, s)
	outside := filepath.Join(filepath.Dir(pa.Path), "outside.txt")
	if err := os.WriteFile(outside, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("바깥 파일 생성 실패: %v", err)
	}

	// 이 토큰은 실제로 존재하는 바깥 파일을 가리킨다. 그래도 "있다"가 되면 안 된다.
	v := s.checkItemPaths(ctx(), pa, []string{"../outside.txt"})
	if v.Kind == judge.KindOK {
		t.Fatalf("루트 밖 파일을 '있다'로 셌다: %s", v.Summary)
	}
	if v.Kind != judge.KindUnknown {
		t.Fatalf("Kind 가 %q 다 — unknown 이어야 한다(밖은 관측하지 않는다): %s", v.Kind, v.Summary)
	}
}

func TestCheckItemPathsHandlesZeroPaths(t *testing.T) {
	s, _ := newSvc(t)
	pa, _ := twoProjects(t, s)

	v := s.checkItemPaths(ctx(), pa, nil)
	if v.Kind != judge.KindNoPaths {
		t.Fatalf("Kind 가 %q 다 — no-paths 여야 한다: %s", v.Kind, v.Summary)
	}
}
