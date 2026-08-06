package store

import (
	"context"
	"errors"
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

// GetSnapshot 이 snapshotCols·scanSnapshot 을 쓰도록 고친 뒤 리뷰가 잡은 자리다 —
// SELECT 문과 Scan 순서를 공유 함수로 옮겼으니 7개 필드가 여전히 제자리에 오는지,
// evidence·input_digest 가 채워진 것과 NULL 인 것 둘 다에서 확인한다.
func TestGetSnapshotRoundTripsAllFields(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p1")

	if err := s.PutSnapshot(ctx, model.Snapshot{
		Project: "p1", Key: "with-evidence", Value: "42", Method: model.SnapshotManual,
		Evidence: "손으로 셌다", InputDigest: "sha256:abc",
	}); err != nil {
		t.Fatalf("스냅숏 저장 실패: %v", err)
	}
	if err := s.PutSnapshot(ctx, model.Snapshot{
		Project: "p1", Key: "no-evidence", Value: "7", Method: model.SnapshotCommand,
	}); err != nil {
		t.Fatalf("스냅숏 저장 실패: %v", err)
	}

	got, err := s.GetSnapshot(ctx, "p1", "with-evidence")
	if err != nil {
		t.Fatalf("GetSnapshot 실패: %v", err)
	}
	if got.Project != "p1" {
		t.Errorf("project 가 안 맞다: %q", got.Project)
	}
	if got.Key != "with-evidence" {
		t.Errorf("key 가 안 맞다: %q", got.Key)
	}
	if got.Value != "42" {
		t.Errorf("value 가 안 맞다: %q", got.Value)
	}
	if got.Method != model.SnapshotManual {
		t.Errorf("method 가 안 맞다: %q", got.Method)
	}
	if got.Evidence != "손으로 셌다" {
		t.Errorf("evidence 가 유실됐다: %q", got.Evidence)
	}
	if got.InputDigest != "sha256:abc" {
		t.Errorf("input_digest 가 유실됐다: %q", got.InputDigest)
	}
	if got.ComputedAt.IsZero() {
		t.Errorf("computed_at 이 안 채워졌다: %+v", got)
	}

	got2, err := s.GetSnapshot(ctx, "p1", "no-evidence")
	if err != nil {
		t.Fatalf("GetSnapshot 실패: %v", err)
	}
	if got2.Evidence != "" {
		t.Errorf("NULL evidence 가 %q 로 나왔다 — str() 이 빈 문자열로 접어야 한다", got2.Evidence)
	}
	if got2.InputDigest != "" {
		t.Errorf("NULL input_digest 가 %q 로 나왔다 — str() 이 빈 문자열로 접어야 한다", got2.InputDigest)
	}
	if got2.Method != model.SnapshotCommand {
		t.Errorf("method 가 안 맞다: %q", got2.Method)
	}
}

// 없는 스냅숏은 notFound 다. scanSnapshot 이 Scan 오류를
// fmt.Errorf("...: %w", err) 로 감싸도 errors.Is(err, sql.ErrNoRows) 판정이
// GetSnapshot 안에서 %w 체인을 뚫고 통과하는지가 이 시험의 핵심이다 —
// 안 뚫리면 없는 스냅숏이 notFound 대신 일반 오류로 나가는 회귀다.
func TestGetSnapshotMissingIsNotFound(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p1")
	_, err := s.GetSnapshot(context.Background(), "p1", "없는키")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("notFound 가 아니다: %v", err)
	}
}
