package store

import (
	"context"
	"testing"
	"time"
)

// LastSessionAt 의 유일한 소비자는 웹 화면의 보관된 프로젝트 줄이다(web.archivedSessionAges).
// 이 시험들은 그 화면 시험(internal/web/project_nav_test.go)과 별개로 이 질의 자체를
// 단위로 잰다 — 리뷰가 지적한 자리다: 유일한 화면 시험이 세션을 한 번도 안 연 프로젝트를
// 대상으로 삼아 이 질의의 정상 갈래(세션 있음·최신 것 고르기·프로젝트로 가른다)가
// 무보호였다.

// TestLastSessionAtIsZeroWithNoSessions 는 세션이 0건이면 제로값이라는 단정이다.
// 못 읽은 것과 "없다"를 가르는 것은 오류 반환이 맡는다 — 세션이 없는 것 자체는
// 오류가 아니라 사실이므로 err==nil·time.Time{} 이어야 한다.
func TestLastSessionAtIsZeroWithNoSessions(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p1")

	at, err := s.LastSessionAt(context.Background(), "p1")
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if !at.IsZero() {
		t.Fatalf("세션이 0건인데 %v 를 냈다 — 제로값이어야 한다", at)
	}
}

// TestLastSessionAtPicksTheLatestOpenedSession 은 여러 세션 중 **가장 늦게 열린** 것을
// 고른다는 단정이다. max(opened_at) 이 다른 집계(가장 먼저 연 것·행 개수)로 슬쩍
// 바뀌어도 이 시험이 잡는다.
func TestLastSessionAtPicksTheLatestOpenedSession(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p1")
	ctx := context.Background()

	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	if _, _, err := s.OpenSession(ctx, "p1", "m1", "/w/a", "cc-a", "", early); err != nil {
		t.Fatalf("세션 열기 실패(a): %v", err)
	}
	if _, _, err := s.OpenSession(ctx, "p1", "m1", "/w/b", "cc-b", "", late); err != nil {
		t.Fatalf("세션 열기 실패(b): %v", err)
	}

	at, err := s.LastSessionAt(ctx, "p1")
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if !at.Equal(late) {
		t.Fatalf("마지막 세션 시각 = %v, want %v(더 늦게 연 쪽)", at, late)
	}
}

// TestLastSessionAtIsScopedToProject 는 다른 프로젝트의 세션이 안 샌다는 단정이다.
// WHERE project = ? 하나가 이 함수의 전부인 만큼, 그 조건이 빠지거나 뒤집혀도
// 컴파일도 다른 시험도 못 잡는 자리다.
func TestLastSessionAtIsScopedToProject(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p1")
	seed(t, s, "p2") // seed 는 project·machine 을 upsert 할 뿐이라 두 번째 호출도 안전하다
	ctx := context.Background()

	other := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	if _, _, err := s.OpenSession(ctx, "p2", "m1", "/w/other", "cc-other", "", other); err != nil {
		t.Fatalf("세션 열기 실패: %v", err)
	}

	got, err := s.LastSessionAt(ctx, "p1")
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("다른 프로젝트(p2)의 세션이 p1 의 마지막 세션 시각으로 샜다: %v", got)
	}
}
