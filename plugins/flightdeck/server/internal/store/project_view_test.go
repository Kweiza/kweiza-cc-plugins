package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// openTestStore 는 빈 DB 를 하나 연다.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "fd.db"))
	if err != nil {
		t.Fatalf("DB 열기 실패: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestProjectViewAxisSurvivesUpsert 는 이 증분의 **유일한 함정**을 잡는다.
//
// ★ UpsertProject 는 세션이 열릴 때마다 돈다(service/session.go 의 자동 등록). 핀·보관을
// 그 ON CONFLICT DO UPDATE 목록에 넣으면 훅이 세션을 열 때마다 사람이 고른 것이 날아가고,
// 그 손실은 어느 화면에도 안 뜬다 — 다음에 볼 때 그냥 안 켜져 있을 뿐이다.
// created_at 이 같은 이유로 이미 그 목록 밖에 있다.
func TestProjectViewAxisSurvivesUpsert(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	base := model.Project{ID: "p1", Path: "/tmp/p1", DefaultBranch: "main"}
	if err := s.UpsertProject(ctx, base); err != nil {
		t.Fatalf("첫 등록 실패: %v", err)
	}

	pin := time.Date(2026, 8, 11, 3, 4, 5, 0, time.UTC)
	if err := s.SetProjectView(ctx, "p1", pin, time.Time{}); err != nil {
		t.Fatalf("핀 설정 실패: %v", err)
	}

	// 세션이 다시 열린 것과 같다 — 경로가 바뀐 재등록.
	again := model.Project{ID: "p1", Path: "/tmp/p1-moved", DefaultBranch: "main"}
	if err := s.UpsertProject(ctx, again); err != nil {
		t.Fatalf("재등록 실패: %v", err)
	}

	got, err := s.GetProject(ctx, "p1")
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if !got.PinnedAt.Equal(pin) {
		t.Fatalf("upsert 가 핀을 지웠다: %v (기대 %v) — "+
			"ON CONFLICT DO UPDATE 목록에 pinned_at 이 들어갔는지 보라", got.PinnedAt, pin)
	}
	if got.Path != "/tmp/p1-moved" {
		t.Fatalf("upsert 가 path 를 안 고쳤다: %q — 이 시험이 전제하는 재등록이 안 일어났다", got.Path)
	}
}

// TestProjectViewRoundTrip 은 두 값이 목록 조회에서도 제자리에 오는지 본다.
//
// ★ projectCols 와 Scan 순서가 어긋나면 전부 문자열이라 타입 오류 없이 조용히 엉뚱한 값이
// 들어간다(그 상수의 주석이 경고하는 실패다). 컬럼을 더한 이 회차가 정확히 그 부류다.
func TestProjectViewRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	pin := time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC)
	arc := time.Date(2026, 8, 12, 4, 5, 6, 0, time.UTC)
	for _, p := range []model.Project{
		{ID: "a", Path: "/tmp/a", DefaultBranch: "main"},
		{ID: "b", Path: "/tmp/b", DefaultBranch: "main"},
	} {
		if err := s.UpsertProject(ctx, p); err != nil {
			t.Fatalf("등록 실패: %v", err)
		}
	}
	if err := s.SetProjectView(ctx, "a", pin, time.Time{}); err != nil {
		t.Fatalf("핀 실패: %v", err)
	}
	if err := s.SetProjectView(ctx, "b", time.Time{}, arc); err != nil {
		t.Fatalf("보관 실패: %v", err)
	}

	list, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("목록 실패: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("프로젝트 %d건, 기대 2건", len(list))
	}
	byID := map[string]model.Project{}
	for _, p := range list {
		byID[p.ID] = p
	}
	if !byID["a"].PinnedAt.Equal(pin) || !byID["a"].ArchivedAt.IsZero() {
		t.Fatalf("a 의 축이 틀렸다: pinned=%v archived=%v", byID["a"].PinnedAt, byID["a"].ArchivedAt)
	}
	if !byID["b"].ArchivedAt.Equal(arc) || !byID["b"].PinnedAt.IsZero() {
		t.Fatalf("b 의 축이 틀렸다: pinned=%v archived=%v", byID["b"].PinnedAt, byID["b"].ArchivedAt)
	}
	// path 가 그대로인지도 본다 — 컬럼 순서가 밀리면 여기가 시각 문자열로 오염된다.
	if byID["a"].Path != "/tmp/a" {
		t.Fatalf("a 의 path 가 %q 다 — projectCols 와 Scan 순서가 어긋났다", byID["a"].Path)
	}
}

// TestSetProjectViewClearsWithZero 는 제로값이 NULL 로 간다는 단정이다 — 핀 해제의 경로다.
func TestSetProjectViewClearsWithZero(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.UpsertProject(ctx, model.Project{ID: "p", Path: "/tmp/p", DefaultBranch: "main"}); err != nil {
		t.Fatalf("등록 실패: %v", err)
	}
	pin := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	if err := s.SetProjectView(ctx, "p", pin, time.Time{}); err != nil {
		t.Fatalf("핀 실패: %v", err)
	}
	if err := s.SetProjectView(ctx, "p", time.Time{}, time.Time{}); err != nil {
		t.Fatalf("해제 실패: %v", err)
	}
	got, err := s.GetProject(ctx, "p")
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if !got.PinnedAt.IsZero() {
		t.Fatalf("핀이 안 풀렸다: %v", got.PinnedAt)
	}
}

// TestSetProjectViewUnknownProject 는 없는 프로젝트에 쓰면 ErrNotFound 라는 단정이다.
// UPDATE 는 0행이어도 성공하므로 이 확인이 없으면 오타가 조용히 성공한다.
func TestSetProjectViewUnknownProject(t *testing.T) {
	s := openTestStore(t)
	err := s.SetProjectView(context.Background(), "없다", time.Now().UTC(), time.Time{})
	if err == nil {
		t.Fatal("없는 프로젝트에 쓰는데 성공했다 — UPDATE 0행을 확인하지 않았다")
	}
}
