package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// TestCountOpenIsTheSamePredicateAsListOpen 은 두 질의가 **같은 술어**임을 단정한다.
//
// 갈리면 pick 과 board 가 같은 이름(`큐 열림 N건`)으로 다른 수를 내고,
// 그 어긋남은 두 화면을 나란히 놓기 전에는 안 보인다.
func TestCountOpenIsTheSamePredicateAsListOpen(t *testing.T) {
	dbp := filepath.Join(t.TempDir(), "fd.db")
	mustMigrate(t, dbp)
	s, _ := Open(dbp)
	defer s.Close()
	ctx := context.Background()
	if err := s.UpsertProject(ctx, model.Project{ID: "p", Path: "/p", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m", Hostname: "h"}); err != nil {
		t.Fatal(err)
	}
	sess, _, err := s.OpenSession(ctx, "p", "m", "/wt", "cc1", "라벨", time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	// 빈 큐를 먼저 본다 — 0 은 "못 셌다"가 아니라 진짜 0이어야 한다.
	if n, err := s.CountOpen(ctx, "p"); err != nil || n != 0 {
		t.Fatalf("빈 큐에서 n=%d err=%v — 기대 0, nil", n, err)
	}

	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if err := s.AddItem(ctx, model.Item{
			Project: "p", ID: id, Title: "t", Body: "b", CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// c 는 선점(claimed), d 는 종료(done), e 는 폐기(dropped). 셋 다 열림이 아니다.
	// dropped 는 close_reason 이 비면 스키마 CHECK 가 막는다(schema.sql:155).
	if _, err := s.ClaimItem(ctx, "p", "c", sess.ID, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemState(ctx, "p", "d", model.ItemDone, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemState(ctx, "p", "e", model.ItemDropped, "중복이라 접었다"); err != nil {
		t.Fatal(err)
	}

	open, err := s.ListOpen(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.CountOpen(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if n != len(open) {
		t.Fatalf("CountOpen=%d 인데 ListOpen=%d 다 — 술어가 갈렸다", n, len(open))
	}
	if n != 2 {
		t.Fatalf("열림은 a·b 둘이어야 한다: %d", n)
	}
}

// TestCountOpenIsScopedToItsProject 는 남의 프로젝트를 안 센다는 것이다.
// 이 서버는 한 DB 에 여러 프로젝트를 담는다 — 스코프가 새면 pick 이 남의 큐를 자기 것으로 센다.
func TestCountOpenIsScopedToItsProject(t *testing.T) {
	dbp := filepath.Join(t.TempDir(), "fd.db")
	mustMigrate(t, dbp)
	s, _ := Open(dbp)
	defer s.Close()
	ctx := context.Background()
	for _, id := range []string{"p", "q"} {
		if err := s.UpsertProject(ctx, model.Project{ID: id, Path: "/" + id, DefaultBranch: "main"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddItem(ctx, model.Item{Project: "p", ID: "a", Title: "t", Body: "b", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"x", "y", "z"} {
		if err := s.AddItem(ctx, model.Item{Project: "q", ID: id, Title: "t", Body: "b", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.CountOpen(ctx, "p"); err != nil || n != 1 {
		t.Fatalf("프로젝트 p 에서 n=%d err=%v — 기대 1, nil (q 의 3건을 셌다면 스코프가 샌 것이다)", n, err)
	}
}
