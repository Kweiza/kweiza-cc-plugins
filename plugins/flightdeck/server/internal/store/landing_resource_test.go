package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// landing_queue 가 줄 행의 자원 집합을 읽고 쓰는 축. 픽스처(newStore·seed·mustSession)는
// landing_test.go 의 것을 그대로 쓴다 — 이 파일은 openTestStore 를 따로 두지 않는다
// (project_view_test.go 의 같은 결정과 같은 이유: newStore 가 이미 로그를 버린다).

// equalStrings 는 이 파일에만 필요한 얕은 비교다. slices.Equal 을 안 쓰는 이유는
// 이 패키지의 다른 파일이 아직 "slices" 를 안 쓰기 때문 — import 하나를 새로 들이는
// 대신 세 줄로 충분하다.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestEnqueueLandingCarriesItsResourceSet(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	var row model.LandingRow
	if err := s.Tx(ctx, func(tx *Tx) error {
		var err error
		row, err = tx.EnqueueLanding("p", a.ID, []string{"path:x.go", "landing"}, time.Time{})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// 정렬해 저장·반환된다 — 집합 대조(service)가 순서에 흔들리면 안 된다.
	if got, want := row.Resources, []string{"landing", "path:x.go"}; !equalStrings(got, want) {
		t.Fatalf("자원 집합이 %v 다 — want %v", got, want)
	}
}

// TestEnqueueLandingReentryReturnsTheSameRowWithItsOriginalResources — 같은 세션이
// **다른** 집합으로 다시 서도 store 는 기존 행+기존 집합을 그대로 낸다. 거절은
// service 의 몫이다(집합 대조) — store 가 거절하면 재진입 안전이 깨진다.
func TestEnqueueLandingReentryReturnsTheSameRowWithItsOriginalResources(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	var first model.LandingRow
	if err := s.Tx(ctx, func(tx *Tx) error {
		var err error
		first, err = tx.EnqueueLanding("p", a.ID, []string{"landing"}, time.Time{})
		return err
	}); err != nil {
		t.Fatalf("첫 서기 실패: %v", err)
	}

	var second model.LandingRow
	if err := s.Tx(ctx, func(tx *Tx) error {
		var err error
		second, err = tx.EnqueueLanding("p", a.ID, []string{"path:y.go"}, time.Time{})
		return err
	}); err != nil {
		t.Fatalf("재진입 호출 자체가 오류를 냈다(재진입은 거절이 아니라 기존 행 반환이어야 한다): %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("재진입이 새 행을 만들었다: 처음 id=%d 재진입 id=%d", first.ID, second.ID)
	}
	if got, want := second.Resources, []string{"landing"}; !equalStrings(got, want) {
		t.Fatalf("재진입이 자원 집합을 바꿨다: got=%v want=%v(기존 집합 그대로여야 한다 — "+
			"요청 집합과 다른지의 판정은 service 몫이다)", got, want)
	}
}

// TestEnqueueLandingRefusesEmptyOrBadResourceNames — 빈 집합·빈 이름·상한 초과·
// 허용되지 않는 문자를 store 가 1차로 거절한다.
func TestEnqueueLandingRefusesEmptyOrBadResourceNames(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	tooLong := strings.Repeat("x", 201)
	for _, c := range []struct {
		name      string
		resources []string
	}{
		{"빈 집합", []string{}},
		{"빈 문자열 포함", []string{"landing", ""}},
		{"201자 이름", []string{tooLong}},
		{"공백 포함 이름", []string{"a b"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := s.Tx(ctx, func(tx *Tx) error {
				_, e := tx.EnqueueLanding("p", a.ID, c.resources, time.Time{})
				return e
			})
			if err == nil {
				t.Fatalf("%v 가 통과했다", c.resources)
			}
		})
	}

	// 거절 뒤에도 줄 행이 안 남아야 한다 — 절반만 적용된 삽입은 없다.
	if _, err := s.LiveLandingRow(ctx, "p", a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("거절됐는데 줄 행이 생겼다: %v", err)
	}

	// ValidateResourceName 을 직접도 찌른다 — path:<경로> 규약과 하이픈은 통과해야 한다.
	for _, name := range []string{"path:internal/api/x.go", "deploy-staging"} {
		if err := ValidateResourceName(name); err != nil {
			t.Errorf("%q 가 거절됐다: %v", name, err)
		}
	}
}

// TestFrontLandingRowForSplitsByResource — 순서 집행이 자원마다 갈린다.
func TestFrontLandingRowForSplitsByResource(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")
	b := mustSession(t, s, "p", "cc-B")

	var rowA model.LandingRow
	if err := s.Tx(ctx, func(tx *Tx) error {
		var err error
		rowA, err = tx.EnqueueLanding("p", a.ID, []string{"r1"}, time.Time{})
		return err
	}); err != nil {
		t.Fatalf("a 줄 서기 실패: %v", err)
	}
	var rowB model.LandingRow
	if err := s.Tx(ctx, func(tx *Tx) error {
		var err error
		rowB, err = tx.EnqueueLanding("p", b.ID, []string{"r2"}, time.Time{})
		return err
	}); err != nil {
		t.Fatalf("b 줄 서기 실패: %v", err)
	}

	frontR1, err := s.FrontLandingRowFor(ctx, "p", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if frontR1.ID != rowA.ID || frontR1.SessionID != a.ID {
		t.Fatalf("r1 의 맨 앞이 a 의 행이 아니다: %+v (기대 id=%d session=%s)", frontR1, rowA.ID, a.ID)
	}

	frontR2, err := s.FrontLandingRowFor(ctx, "p", "r2")
	if err != nil {
		t.Fatal(err)
	}
	if frontR2.ID != rowB.ID || frontR2.SessionID != b.ID {
		t.Fatalf("r2 의 맨 앞이 b 의 행이 아니다: %+v (기대 id=%d session=%s)", frontR2, rowB.ID, b.ID)
	}

	if _, err := s.FrontLandingRowFor(ctx, "p", "r3"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("아무도 안 선 자원의 맨 앞 조회가 ErrNotFound 가 아니다: %v", err)
	}

	// a 의 행을 닫으면 r1 의 맨 앞이 사라진다.
	if err := s.CloseLandingRow(ctx, "p", rowA.ID, model.LandingLeftOK, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FrontLandingRowFor(ctx, "p", "r1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a 의 행을 닫았는데 r1 의 맨 앞이 여전히 나온다: %v", err)
	}
}

// TestListLandingQueueAttachesResources — 두 행을 넣고 ListLandingQueue 로 읽으면
// 각 행의 Resources 가 넣은 그대로다(한 질의로 붙는 것은 attachResources 의 IN 질의
// 주석이 잠근다 — 여기서는 결과만 본다).
func TestListLandingQueueAttachesResources(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")
	b := mustSession(t, s, "p", "cc-B")

	if err := s.Tx(ctx, func(tx *Tx) error {
		_, err := tx.EnqueueLanding("p", a.ID, []string{"landing"}, time.Time{})
		return err
	}); err != nil {
		t.Fatalf("a 줄 서기 실패: %v", err)
	}
	if err := s.Tx(ctx, func(tx *Tx) error {
		_, err := tx.EnqueueLanding("p", b.ID, []string{"path:y.go", "path:x.go"}, time.Time{})
		return err
	}); err != nil {
		t.Fatalf("b 줄 서기 실패: %v", err)
	}

	got, err := s.ListLandingQueue(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("큐 길이가 2 여야 하는데 %d 다: %+v", len(got), got)
	}
	if !equalStrings(got[0].Resources, []string{"landing"}) {
		t.Fatalf("a 의 자원 집합이 %v 다 — want [landing]", got[0].Resources)
	}
	if !equalStrings(got[1].Resources, []string{"path:x.go", "path:y.go"}) {
		t.Fatalf("b 의 자원 집합이 %v 다 — want [path:x.go path:y.go](정렬)", got[1].Resources)
	}
}
