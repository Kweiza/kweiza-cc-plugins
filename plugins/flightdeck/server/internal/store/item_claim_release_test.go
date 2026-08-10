package store

import (
	"context"
	"errors"
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
	sess, _, err := s.OpenSession(ctx, "p", "m", "/wt", "cc1", "라벨", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddItem(ctx, model.Item{Project: "p", ID: "x", Title: "t", Body: "b", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimItem(ctx, "p", "x", sess.ID, time.Time{}); err != nil {
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

// LiveClaim 은 살아 있는 선점만 낸다 — GetClaim(이력도 냄)과 갈리는 지점이 정체 확정의
// 전부다: 반납된 행을 살아 있다고 내면 회수가 옛 점유자를 정체로 적는다.
func TestLiveClaimDistinguishesLiveFromReleasedAndMissing(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "fd.db"))
	defer s.Close()
	ctx := context.Background()
	_ = s.UpsertProject(ctx, model.Project{ID: "p", Path: "/p", DefaultBranch: "main"})
	_ = s.UpsertMachine(ctx, model.Machine{ID: "m", Hostname: "h"})
	sess, _, err := s.OpenSession(ctx, "p", "m", "/wt", "cc1", "라벨", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddItem(ctx, model.Item{Project: "p", ID: "x", Title: "t", Body: "b", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	// 행이 아예 없을 때도 "살아 있는 선점 없음"이다.
	if err := s.Tx(ctx, func(tx *Tx) error {
		_, err := tx.LiveClaim("p", "x")
		return err
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("선점 행이 없는데 NotFound 가 아니다: %v", err)
	}

	if _, err := s.ClaimItem(ctx, "p", "x", sess.ID, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(ctx, func(tx *Tx) error {
		c, err := tx.LiveClaim("p", "x")
		if err != nil {
			return err
		}
		if c.SessionID != sess.ID {
			t.Errorf("살아 있는 선점의 점유자가 다르다: %s", c.SessionID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ForceReleaseClaim(ctx, "p", "x", "시험 회수"); err != nil {
		t.Fatal(err)
	}
	// 반납된 행은 GetClaim 에는 남지만(이력) LiveClaim 에는 없다.
	if c, err := s.GetClaim(ctx, "p", "x"); err != nil || c.ReleasedAt == nil {
		t.Fatalf("이력 행이 안 남았다: %+v %v", c, err)
	}
	if err := s.Tx(ctx, func(tx *Tx) error {
		_, err := tx.LiveClaim("p", "x")
		return err
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("반납된 선점이 살아 있다고 나왔다: %v", err)
	}
}
