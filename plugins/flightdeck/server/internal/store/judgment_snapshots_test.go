package store

import (
	"context"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 스냅숏 나열은 키 순이고 프로젝트로 갈린다. 이 함수가 없던 동안 유일한 나열 SQL 이
// internal/web 안에 있었고, 원장 내보내기가 그것을 또 적으면 두 벌이 된다.
func TestListSnapshotsIsKeyOrderedAndProjectScoped(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p1")
	seed(t, s, "p2")

	put := func(project, key, value string, method model.SnapshotMethod, evidence string) {
		t.Helper()
		if err := s.PutSnapshot(ctx, model.Snapshot{
			Project: project, Key: key, Value: value, Method: method, Evidence: evidence,
		}); err != nil {
			t.Fatalf("스냅숏 저장 실패(%s/%s): %v", project, key, err)
		}
	}
	put("p1", "zeta", "3", model.SnapshotCommand, "")
	put("p1", "alpha", "1", model.SnapshotManual, "손으로 셌다")
	put("p2", "other", "9", model.SnapshotCommand, "")

	got, err := s.ListSnapshots(ctx, "p1")
	if err != nil {
		t.Fatalf("ListSnapshots 실패: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("p1 스냅숏이 %d개다 — 2개를 기대한다: %+v", len(got), got)
	}
	if got[0].Key != "alpha" || got[1].Key != "zeta" {
		t.Errorf("키 순이 아니다: %q, %q", got[0].Key, got[1].Key)
	}
	if got[0].Project != "p1" {
		t.Errorf("project 가 안 채워졌다: %q", got[0].Project)
	}
	if got[0].Evidence != "손으로 셌다" {
		t.Errorf("evidence 가 유실됐다: %q", got[0].Evidence)
	}
	if got[1].Evidence != "" {
		t.Errorf("NULL evidence 가 %q 로 나왔다 — str() 이 빈 문자열로 접어야 한다", got[1].Evidence)
	}
}

// 없는 프로젝트는 오류가 아니라 빈 목록이다 — GetSnapshot 은 notFound 를 내지만
// 나열은 "아직 없다"와 "그런 프로젝트가 없다"를 구분할 필요가 없다.
func TestListSnapshotsEmptyIsNotAnError(t *testing.T) {
	s := newStore(t)
	got, err := s.ListSnapshots(context.Background(), "없는프로젝트")
	if err != nil {
		t.Fatalf("빈 목록이 오류가 됐다: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("%d개가 나왔다", len(got))
	}
}
