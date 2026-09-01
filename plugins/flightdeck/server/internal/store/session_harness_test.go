package store

import (
	"context"
	"testing"
	"time"
)

// session.harness — 하네스가 **원장에 남는가**.
//
// 지금까지 하네스는 관측되고 배너에 뜨지만 원장에 안 남았다. 그래서 보드를 보는 사람이
// "저건 codex 다"를 모르고, 잘못 귀속된 카드를 나중에 가려낼 축이 0이다.
//
// ★ 이 칼럼은 **좌표가 아니라 속성**이다. 3중키(machine, worktree, cc_session)에 넣지
// 않는다 — 두 하네스의 세션 id 가 둘 다 UUID 라 유일성은 이미 성립하고, 키에 넣으면
// 표기 하나가 바뀔 때 같은 세션이 다른 카드가 된다.

var harnessAt = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

// 선언을 실으면 그 값이 원장에 남고 다시 읽힌다.
func TestOpenSessionAsRecordsTheHarness(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p1")
	ctx := context.Background()

	got, _, err := s.OpenSessionAs(ctx, "p1", "m1", "/w/a", "cc-a", "", "codex", harnessAt)
	if err != nil {
		t.Fatalf("세션 열기 실패: %v", err)
	}
	if got.Harness != "codex" {
		t.Fatalf("연 세션의 하네스가 %q 다 — codex 여야 한다", got.Harness)
	}

	// 원장에서 **다시 읽어** 단정한다. 돌려준 구조체만 보면 저장이 실제로 됐는지 모른다.
	again, err := s.FindSession(ctx, "m1", "/w/a", "cc-a")
	if err != nil {
		t.Fatalf("세션 조회 실패: %v", err)
	}
	if again.Harness != "codex" {
		t.Fatalf("원장에서 읽은 하네스가 %q 다 — codex 여야 한다", again.Harness)
	}
}

// 선언이 없으면 **빈 채로 남는다** — claude 로 접지 않는다.
//
// 접으면 codex 설치가 인자를 빠뜨린 날 그 카드가 조용히 claude 로 들어가고,
// 그것이 곧 지어내는 것이다(원칙 ①).
func TestOpenSessionWithoutDeclarationLeavesHarnessEmpty(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p1")
	ctx := context.Background()

	got, _, err := s.OpenSession(ctx, "p1", "m1", "/w/a", "cc-a", "", harnessAt)
	if err != nil {
		t.Fatalf("세션 열기 실패: %v", err)
	}
	if got.Harness != "" {
		t.Fatalf("선언이 없는데 하네스가 %q 다 — 「미상」은 빈 값이다", got.Harness)
	}
}

// ★ 재개의 병합 규칙 — **선언은 덮어쓰고, 미선언은 기존 값을 안 지운다.**
//
// 이 갈래를 안 정하면 우연히 정해진다. 지금 함대는 Claude 설치물이 --harness 를 아직
// 안 싣는 상태로 돌아가므로, 미선언 호출이 기존 값을 지우게 두면 codex 카드의 하네스가
// 다음 신호 한 번에 사라진다 — 그리고 그 소실은 어느 화면에도 안 뜬다.
func TestReopenMergesHarnessWithoutErasing(t *testing.T) {
	ctx := context.Background()

	t.Run("미선언이 기존 값을 안 지운다", func(t *testing.T) {
		s := newStore(t)
		seed(t, s, "p1")
		if _, _, err := s.OpenSessionAs(ctx, "p1", "m1", "/w/a", "cc-a", "", "codex", harnessAt); err != nil {
			t.Fatalf("첫 열기 실패: %v", err)
		}
		// 같은 3중키로 **선언 없이** 다시 연다(오늘 Claude 쪽 설치물의 모양이다).
		got, _, err := s.OpenSession(ctx, "p1", "m1", "/w/a", "cc-a", "", harnessAt)
		if err != nil {
			t.Fatalf("재개 실패: %v", err)
		}
		if got.Harness != "codex" {
			t.Fatalf("미선언 재개가 하네스를 %q 로 만들었다 — codex 가 남아야 한다", got.Harness)
		}
	})

	t.Run("선언은 덮어쓴다", func(t *testing.T) {
		s := newStore(t)
		seed(t, s, "p1")
		if _, _, err := s.OpenSession(ctx, "p1", "m1", "/w/a", "cc-a", "", harnessAt); err != nil {
			t.Fatalf("첫 열기 실패: %v", err)
		}
		got, _, err := s.OpenSessionAs(ctx, "p1", "m1", "/w/a", "cc-a", "", "claude", harnessAt)
		if err != nil {
			t.Fatalf("재개 실패: %v", err)
		}
		if got.Harness != "claude" {
			t.Fatalf("선언이 왔는데 하네스가 %q 다 — claude 로 갱신돼야 한다", got.Harness)
		}
	})
}

// OpenSession 은 OpenSessionAs 의 **얇은 껍데기**여야 한다 — 판정이 두 벌이면 반드시 표류한다.
// (선례: mcpsrv 의 ResolveIdentity / ResolveIdentityAs.)
//
// 두 경로가 하네스 말고 **모든 것을 같게** 만드는지를 본다.
func TestOpenSessionIsAThinWrapper(t *testing.T) {
	ctx := context.Background()

	s1 := newStore(t)
	seed(t, s1, "p1")
	plain, created1, err := s1.OpenSession(ctx, "p1", "m1", "/w/a", "cc-a", "라벨", harnessAt)
	if err != nil {
		t.Fatalf("OpenSession 실패: %v", err)
	}

	s2 := newStore(t)
	seed(t, s2, "p1")
	as, created2, err := s2.OpenSessionAs(ctx, "p1", "m1", "/w/a", "cc-a", "라벨", "", harnessAt)
	if err != nil {
		t.Fatalf("OpenSessionAs 실패: %v", err)
	}

	if created1 != created2 {
		t.Fatalf("created 가 갈린다: %v vs %v", created1, created2)
	}
	// id 는 ULID 라 다르다 — 그 외 축이 같아야 한다.
	if plain.Project != as.Project || plain.MachineID != as.MachineID ||
		plain.Worktree != as.Worktree || plain.CCSessionID != as.CCSessionID ||
		plain.Label != as.Label || plain.State != as.State {
		t.Fatalf("두 경로가 다른 세션을 만든다:\n  plain=%+v\n  as   =%+v", plain, as)
	}
}
