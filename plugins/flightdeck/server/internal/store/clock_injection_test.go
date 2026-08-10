package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 주입한 시각이 **Store 짝을 지나서도 살아남는지**, 그리고 **다시 읽어도 같은 값인지**.
//
// ★ 왜 두 가지를 한 자리에서 재나. 둘은 서로 다른 실패다.
//
//	① Store 짝(단발 트랜잭션)이 at 을 Tx 짝에 안 넘기면 "Tx 로 잡으면 주입한 시계,
//	   Store 로 잡으면 실시계"라는 비대칭이 남는다. 그 비대칭은 두 경로의 시각을
//	   나란히 놓는 자리에서만 드러나는데, 운영에는 그런 자리가 없다 — 둘 다 실시계라
//	   결과가 같다. 그래서 시험이 아니면 영영 안 보인다.
//
//	② 받은 시각을 atStamp 없이 그대로 구조체에 담으면, 돌려준 값(나노초)과 행에 저장된
//	   값(마이크로초 — timeLayout)이 갈린다. "방금 만든 것"과 "다시 읽은 것"을 비교하는
//	   자리가 조용히 틀어지고, 재개 판정이 정확히 그 비교다.
//
// 그래서 주입하는 시각에 **나노초를 일부러 담는다**(…, 123456789). atStamp 를 빼면
// ②만 빨개지고 ①은 초록이다 — 두 단정이 서로를 대신하지 못한다.
func TestStorePairCarriesTheInjectedTimeAndItSurvivesAReRead(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "fd.db"))
	defer s.Close()
	ctx := context.Background()
	if err := s.UpsertProject(ctx, model.Project{ID: "p", Path: "/p", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m", Hostname: "h"}); err != nil {
		t.Fatal(err)
	}

	// 나노초가 든 시각이다. 저장 해상도(마이크로초)와 일부러 어긋나 있다.
	opened := time.Date(2024, 3, 1, 9, 0, 0, 123456789, time.UTC)
	claimed := time.Date(2024, 3, 1, 9, 7, 0, 987654321, time.UTC)
	want := func(at time.Time) time.Time { return at.Truncate(time.Microsecond) }

	sess, created, err := s.OpenSession(ctx, "p", "m", "/wt", "cc1", "라벨", opened)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("전제 실패 — 새 세션이 아니다. 재개 경로는 opened_at 을 안 쓰므로 이 시험이 아무것도 안 잰다")
	}
	if !sess.OpenedAt.Equal(want(opened)) {
		t.Fatalf("돌려준 opened_at = %v, 기대 %v — Store 짝이 주입된 시각을 안 넘겼거나 해상도를 안 맞췄다",
			sess.OpenedAt, want(opened))
	}
	// 다시 읽는다. 여기서 갈리면 "방금 만든 것 vs 다시 읽은 것" 비교가 조용히 틀어진다.
	reread, err := s.FindSession(ctx, "m", "/wt", "cc1")
	if err != nil {
		t.Fatal(err)
	}
	if !reread.OpenedAt.Equal(sess.OpenedAt) {
		t.Fatalf("다시 읽은 opened_at = %v, 방금 돌려준 값 = %v — 두 값이 갈리면 재개 판정이 틀어진다",
			reread.OpenedAt, sess.OpenedAt)
	}

	if err := s.AddItem(ctx, model.Item{Project: "p", ID: "it", Title: "t", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimItem(ctx, "p", "it", sess.ID, claimed)
	if err != nil {
		t.Fatal(err)
	}
	if !c.At.Equal(want(claimed)) {
		t.Fatalf("돌려준 claim.at = %v, 기대 %v — Store 짝이 주입된 시각을 안 넘겼거나 해상도를 안 맞췄다",
			c.At, want(claimed))
	}
	got, err := s.GetClaim(ctx, "p", "it")
	if err != nil {
		t.Fatal(err)
	}
	if !got.At.Equal(c.At) {
		t.Fatalf("다시 읽은 claim.at = %v, 방금 돌려준 값 = %v", got.At, c.At)
	}
	// 그리고 두 시각은 **서로 다른 값**이다 — 한 시각을 두 표에 찍고 있으면 위 단정 둘이
	// 서로를 대신할 수 있다.
	if c.At.Equal(sess.OpenedAt) {
		t.Fatal("선점 시각과 개시 시각이 같다 — 이 픽스처는 두 축을 가르지 못한다")
	}
}

// 영값은 **지금**이다. Beat·Touch 와 같은 문법이고, 시험 호출부 대부분이 이 갈래를 탄다.
func TestZeroTimeStillMeansNow(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "fd.db"))
	defer s.Close()
	ctx := context.Background()
	if err := s.UpsertProject(ctx, model.Project{ID: "p", Path: "/p", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m", Hostname: "h"}); err != nil {
		t.Fatal(err)
	}

	before := nowStamp()
	sess, _, err := s.OpenSession(ctx, "p", "m", "/wt", "cc1", "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	after := nowStamp()
	if sess.OpenedAt.Before(before) || sess.OpenedAt.After(after) {
		t.Fatalf("영값을 줬는데 opened_at = %v 가 [%v, %v] 밖이다 — 영값이 지금이 아니면 "+
			"호출부 대부분(시험·반입 경로)이 1970년에 열린 세션을 만든다", sess.OpenedAt, before, after)
	}
}
