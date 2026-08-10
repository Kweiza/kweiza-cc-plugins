package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// ★ 이 파일은 별도의 openTestStore 를 안 둔다. store_test.go 의 newStore 가 이미 같은 일을
// 한다 — 다만 OpenWithLogger(..., io.Discard) 로 로그를 버린다(그 함수 주석 참고: "마이그레이션
// INFO 가 시험 출력을 덮는다"). 처음엔 브리프가 준 Open 기반 openTestStore 를 그대로 옮겼는데,
// 그러면 이 파일의 시험 넷 × 증분 여섯 = 스물넉 줄의 INFO 소음이 매 실행마다 낀다 —
// parseNullTime 에서 기존 헬퍼를 재사용한 것과 같은 이유로 중복을 걷었다.

// TestProjectViewAxisSurvivesUpsert 는 이 증분의 **유일한 함정**을 잡는다.
//
// ★ UpsertProject 는 세션이 열릴 때마다 돈다(service/session.go 의 자동 등록). 핀·보관을
// 그 ON CONFLICT DO UPDATE 목록에 넣으면 훅이 세션을 열 때마다 사람이 고른 것이 날아가고,
// 그 손실은 어느 화면에도 안 뜬다 — 다음에 볼 때 그냥 안 켜져 있을 뿐이다.
// created_at 이 같은 이유로 이미 그 목록 밖에 있다.
func TestProjectViewAxisSurvivesUpsert(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

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
	s := newStore(t)

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
	s := newStore(t)

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
//
// ★ errors.As(*NotFoundError) 로 잰다 — errors.Is(err, ErrNotFound) 만 보면 fmt.Errorf 로
// sentinel 만 감싸도 통과한다. getProject 가 쓰는 notFound(NFProject, ...) 와 같은 길로
// 왔는지, 즉 internal/api 의 errors.As(*store.NotFoundError) 가 좌표·처방을 붙일 수 있는
// 모양인지를 이 시험이 실제로 잰다(notfound.go 의 타입 주석 참고).
func TestSetProjectViewUnknownProject(t *testing.T) {
	s := newStore(t)
	err := s.SetProjectView(context.Background(), "없다", time.Now().UTC(), time.Time{})
	if err == nil {
		t.Fatal("없는 프로젝트에 쓰는데 성공했다 — UPDATE 0행을 확인하지 않았다")
	}
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("오류가 *NotFoundError 가 아니다: %v(%T) — sentinel 로만 감싸면 "+
			"internal/api 가 좌표·처방을 못 붙인다", err, err)
	}
	if nf.Kind != NFProject || nf.ID != "없다" {
		t.Fatalf("좌표가 틀렸다: kind=%q id=%q", nf.Kind, nf.ID)
	}
}

// TestSetProjectViewOverwritesBothAxesTogether 는 두 축이 함께 있는 프로젝트에 한 축만
// 새로 실어 불러도 다른 축이 조용히 풀린다는 것을 **의도로** 못박는다.
//
// ★ 왜 필요한가. SetProjectView 의 UPDATE 는 행 전체를 덮어쓴다(그 함수 주석 참고) —
// "핀만 바꾸고 싶다"는 호출자의 뜻이지 이 함수의 계약이 아니다. 이 시험이 없으면 후속
// 태스크(핀 토글 처리기)가 `SetProjectView(ctx, id, time.Now(), time.Time{})` 라고만 써서
// 보관을 조용히 풀 수 있다 — 그 손실은 UpsertProject 에 대해 이미 막은 것과 정확히 같은
// 모양이고 같은 이유로 어느 화면에도 안 뜬다. TestSetProjectViewClearsWithZero 는 **같은
// 축의 해제**만 본다 — 이 시험은 **다른 축**이 딸려서 풀리는 쪽을 본다.
func TestSetProjectViewOverwritesBothAxesTogether(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if err := s.UpsertProject(ctx, model.Project{ID: "p", Path: "/tmp/p", DefaultBranch: "main"}); err != nil {
		t.Fatalf("등록 실패: %v", err)
	}
	pin := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	arc := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	if err := s.SetProjectView(ctx, "p", pin, arc); err != nil {
		t.Fatalf("초기 설정 실패: %v", err)
	}

	// 핀만 새로 바꿔 부른다 — archived 자리에는 습관적으로 제로값을 넘긴다(호출자가
	// "핀만 건드린다"고 생각하는 바로 그 실수).
	newPin := pin.Add(time.Hour)
	if err := s.SetProjectView(ctx, "p", newPin, time.Time{}); err != nil {
		t.Fatalf("핀 갱신 실패: %v", err)
	}

	got, err := s.GetProject(ctx, "p")
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if !got.PinnedAt.Equal(newPin) {
		t.Fatalf("핀이 갱신되지 않았다: %v (기대 %v)", got.PinnedAt, newPin)
	}
	if !got.ArchivedAt.IsZero() {
		t.Fatalf("보관이 남아 있다: %v — SetProjectView 는 행 전체를 덮어쓰므로 "+
			"이번 호출처럼 archived 를 제로값으로 넘기면 풀려야 한다. 이 값이 안 풀렸다면 "+
			"계약이 이 시험이 아는 것과 달라졌다는 뜻이니 문서·후속 호출부를 같이 봐라", got.ArchivedAt)
	}
}
