package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

func TestMissingItemIsNotSilentSuccess(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "fd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.UpsertProject(ctx, model.Project{ID: "p", Path: "/p", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	// 대조 전제: 이 항목 id 는 정말로 없다. 확인하고 시작한다.
	if _, err := s.GetItem(ctx, "p", "없는항목"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("전제가 깨졌다 — 없는 항목이 조회된다: %v", err)
	}
	for _, c := range []struct {
		name string
		call func() error
	}{
		{"SetItemState", func() error { return s.SetItemState(ctx, "p", "없는항목", model.ItemDropped, "사유") }},
		{"SetLandedRef", func() error { return s.SetLandedRef(ctx, "p", "없는항목", "9d2ada8") }},
		{"FinishItem", func() error { return s.FinishItem(ctx, "p", "없는항목", "s1", model.ItemDone, "") }},
	} {
		if err := c.call(); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s 가 없는 항목에 %v 를 냈다 — ErrNotFound 여야 한다", c.name, err)
		}
	}
}
