package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

func TestClosingAnItemReleasesItsClaim(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "fd.db"))
	defer s.Close()
	ctx := context.Background()
	_ = s.UpsertProject(ctx, model.Project{ID: "p", Path: "/p", DefaultBranch: "main"})
	_ = s.UpsertMachine(ctx, model.Machine{ID: "m", Hostname: "h"})
	sess, _, err := s.OpenSession(ctx, "p", "m", "/wt", "cc1", "라벨")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddItem(ctx, model.Item{Project: "p", ID: "x", Title: "t", Body: "b", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimItem(ctx, "p", "x", sess.ID); err != nil {
		t.Fatal(err)
	}
	// 대조 전제: 지금은 정말로 선점돼 있다.
	if got, _ := s.ClaimedItems(ctx, sess.ID); len(got) != 1 {
		t.Fatalf("전제가 깨졌다 — 선점이 없다: %v", got)
	}
	if err := s.SetItemState(ctx, "p", "x", model.ItemDone, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ClaimedItems(ctx, sess.ID); len(got) != 0 {
		t.Errorf("끝난 항목의 선점이 살아 있다: %v — board 가 이것을 영구히 '쥐고 있다'로 표시한다", got)
	}
}
