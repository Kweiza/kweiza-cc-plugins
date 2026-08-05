package store

import (
	"context"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

func TestAckReachSplitsDenominatorByJudgmentPresence(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	// ★ 브리프 원문에는 이 등록이 없었다 — 세션의 FK 대상(project·machine)이 먼저
	// 있어야 하고, 없으면 OpenSession 이 787(missing_ref)로 죽는다(Step 2 확인 중
	// 실측). seed() 는 machine 을 "m1"로 등록하는데 브리프의 OpenSession 호출은
	// machine id 로 "m"을 쓰므로 seed() 를 그대로 못 쓴다 — 그 값을 "m1"로
	// 바꾸지 않고(브리프 값을 그대로 쓴다), 대신 "m"을 직접 등록한다.
	if err := s.UpsertProject(ctx, model.Project{ID: "p", Path: "/repo/p"}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m", Hostname: "dev"}); err != nil {
		t.Fatalf("머신 등록 실패: %v", err)
	}

	// 카드 셋: id1 은 판단이 있고 ack 도 했다 · id2 는 발화만 받고 판단이 0이다 ·
	// id3 은 발화 자체가 없다.
	s1, _, err := s.OpenSession(ctx, "p", "m", "/wt1", "cc-1", "")
	if err != nil {
		t.Fatalf("OpenSession wt1: %v", err)
	}
	s2, _, err := s.OpenSession(ctx, "p", "m", "/wt2", "cc-2", "")
	if err != nil {
		t.Fatalf("OpenSession wt2: %v", err)
	}
	if _, _, err := s.OpenSession(ctx, "p", "m", "/wt3", "cc-3", ""); err != nil {
		t.Fatalf("OpenSession wt3: %v", err)
	}

	s.LogEvent(ctx, "prescribe", "p", s1.ID, map[string]any{"key": "k"})
	s.LogEvent(ctx, "prescribe", "p", s2.ID, map[string]any{"key": "k"})
	s.LogEvent(ctx, "prescribe_ack", "p", s1.ID, map[string]any{"keys": []string{"k"}})

	// id1 만 판단을 가진다 — 이것이 분모를 가르는 축이다.
	if _, err := s.AddJudgment(ctx, model.Judgment{
		Project: "p", SessionID: s1.ID, Kind: model.JudgmentDecision,
		Title: "t", Body: "b",
	}); err != nil {
		t.Fatalf("AddJudgment: %v", err)
	}

	emitted, reachable, acked, err := s.AckReach(ctx, "p")
	if err != nil {
		t.Fatalf("AckReach: %v", err)
	}
	if emitted != 2 {
		t.Errorf("발화 카드 %d, 원하는 것 2", emitted)
	}
	if reachable != 1 {
		t.Errorf("판단 가진 카드 %d, 원하는 것 1 — 분모는 이쪽이다", reachable)
	}
	if acked != 1 {
		t.Errorf("ack 한 카드 %d, 원하는 것 1", acked)
	}
	// 이 시험의 존재 이유 — 두 분모가 **다른 답**을 낸다.
	if emitted == reachable {
		t.Fatal("두 분모가 같으면 이 지표는 아무것도 안 가른다")
	}
}
