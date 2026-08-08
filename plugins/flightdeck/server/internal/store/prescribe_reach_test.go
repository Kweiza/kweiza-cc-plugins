package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// ackWindow 는 이 파일의 시험이 쓰는 절단 폭이다. 운영값(service.AckWindow)과
// 같은 24시간이지만 store 는 그 상수를 모른다 — 절단은 호출부가 정한다.
const ackWindow = 24 * time.Hour

// logEventAt 은 event 를 **과거 시각으로** 넣는다. 창 밖 픽스처를 만드는 유일한 길이다.
//
// ★ LogEvent 로는 못 만든다 — event.go 의 INSERT 가 `fmtTime(time.Now())` 를 박고
// at 를 받는 인자가 없다. 넣은 뒤 미는 길도 막혀 있다(event_no_update·event_no_delete
// 트리거, schema.sql). 서비스의 WithClock 도 여기 안 닿는다 — 그 시계는 service 계층
// 전용이고 store 의 이 경로는 time.Now() 를 직접 부른다. 그래서 원시 INSERT 다.
func logEventAt(t *testing.T, s *Store, at time.Time, kind, project, sessionID string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO event(at, project, session_id, kind, payload) VALUES (?, ?, ?, ?, '{}')`,
		fmtTime(at), project, sessionID, kind); err != nil {
		t.Fatalf("이벤트 백데이트 삽입 실패(kind=%s at=%s): %v", kind, fmtTime(at), err)
	}
}

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

	all, recent, err := s.AckReach(ctx, "p", time.Now().Add(-ackWindow))
	if err != nil {
		t.Fatalf("AckReach: %v", err)
	}
	if all.Emitted != 2 {
		t.Errorf("발화 카드 %d, 원하는 것 2", all.Emitted)
	}
	if all.Reachable != 1 {
		t.Errorf("판단 가진 카드 %d, 원하는 것 1 — 분모는 이쪽이다", all.Reachable)
	}
	if all.Acked != 1 {
		t.Errorf("ack 한 카드 %d, 원하는 것 1", all.Acked)
	}
	// 이 시험의 존재 이유 — 두 분모가 **다른 답**을 낸다.
	if all.Emitted == all.Reachable {
		t.Fatal("두 분모가 같으면 이 지표는 아무것도 안 가른다")
	}
	// 이 픽스처는 전부 방금 만든 행이라 24시간 창 안에 있다 — 두 구간이 같아야 한다.
	// 갈리면 절단이 창 **안** 행까지 잘랐다는 뜻이다.
	if recent != all {
		t.Errorf("최근 %+v · 전 역사 %+v — 창 안 픽스처인데 갈렸다", recent, all)
	}
}

// 한 세션이 같은 이벤트를 여러 번 남겨도 **카드 한 장**으로 센다.
// DISTINCT 가 없으면 이벤트 수가 곧 카드 수가 되어 분모가 통째로 부풀고,
// 실측에서 그 왜곡이 4배였다(emitted 31 → 125).
func TestAckReachCountsCardsNotEvents(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	// ★ 위 시험과 같은 이유로 등록한다(브리프 원문에는 없다) — 787(missing_ref) 회피.
	if err := s.UpsertProject(ctx, model.Project{ID: "p", Path: "/repo/p"}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m", Hostname: "dev"}); err != nil {
		t.Fatalf("머신 등록 실패: %v", err)
	}

	s1, _, err := s.OpenSession(ctx, "p", "m", "/wt1", "cc-1", "")
	if err != nil {
		t.Fatalf("OpenSession wt1: %v", err)
	}
	s2, _, err := s.OpenSession(ctx, "p", "m", "/wt2", "cc-2", "")
	if err != nil {
		t.Fatalf("OpenSession wt2: %v", err)
	}

	// s1 은 같은 이벤트를 **두 번씩** 남긴다 — 실물에서 흔한 모양이다.
	s.LogEvent(ctx, "prescribe", "p", s1.ID, map[string]any{"key": "k1"})
	s.LogEvent(ctx, "prescribe", "p", s1.ID, map[string]any{"key": "k2"})
	s.LogEvent(ctx, "prescribe_ack", "p", s1.ID, map[string]any{"keys": []string{"k1"}})
	s.LogEvent(ctx, "prescribe_ack", "p", s1.ID, map[string]any{"keys": []string{"k2"}})
	// s2 는 한 번만. 판단은 없다.
	s.LogEvent(ctx, "prescribe", "p", s2.ID, map[string]any{"key": "k"})

	if _, err := s.AddJudgment(ctx, model.Judgment{
		Project: "p", SessionID: s1.ID, Kind: model.JudgmentDecision, Title: "t", Body: "b",
	}); err != nil {
		t.Fatalf("AddJudgment: %v", err)
	}

	all, recent, err := s.AckReach(ctx, "p", time.Now().Add(-ackWindow))
	if err != nil {
		t.Fatalf("AckReach: %v", err)
	}
	// 이벤트는 5건이지만 카드는 2장이다.
	if all.Emitted != 2 {
		t.Errorf("발화 카드 %d, 원하는 것 2 — 이벤트 수(3)를 센 것이 아닌지 보라", all.Emitted)
	}
	if all.Reachable != 1 {
		t.Errorf("판단 가진 카드 %d, 원하는 것 1 — s1 의 prescribe 가 둘이라 2가 나오면 DISTINCT 가 빠진 것이다", all.Reachable)
	}
	if all.Acked != 1 {
		t.Errorf("ack 한 카드 %d, 원하는 것 1 — ack 이벤트가 둘이라 2가 나오면 DISTINCT 가 빠진 것이다", all.Acked)
	}
	// ★ DISTINCT 는 **두 구간 모두** 지켜야 한다. 최근 벌의 세 부질의에서 DISTINCT 가
	// 빠지면 전 역사 벌만 보는 위 단정들은 전부 초록이다.
	if recent != all {
		t.Errorf("최근 %+v · 전 역사 %+v — 창 안 픽스처인데 갈렸다. 최근 벌의 DISTINCT 를 보라", recent, all)
	}
}

// 창 밖 카드는 최근 벌에서 빠지고 전 역사 벌에는 남는다.
//
// 이것이 이 축의 전부다 — 절단이 없으면 emitted 가 프로젝트 전 역사를 누적해,
// 갈림이 고쳐져도 옛 카드가 분모에 영영 남는다. 그러면 "지금 규율이 나아졌나"를
// 물을 수 없다(DESIGN §10 이 요구한 재측정).
//
// ★ 픽스처는 **세 부질의를 각각 독립으로** 잠근다. 여섯 수가 전부 다르므로
// (전 역사 6·5·3 · 최근 3·2·1) 셋 중 하나만 절단을 빠뜨려도 그 수 하나만 틀린다 —
// 서로 상쇄되지 않는다. 위치 뒤바뀜(Emitted↔Reachable)도 같은 이유로 잡힌다.
//
// ★ s6 이 이 시험의 가장 비싼 자리다 — **처방은 창 밖인데 ack 은 창 안**인 카드.
// ack 을 그 이벤트 자신의 시각으로만 자르면 이 카드가 Acked 에는 들어가고
// Emitted·Reachable 에는 안 들어가, `Acked ⊆ Reachable ⊆ Emitted` 가 깨진다.
// 그러면 꼬리 문장의 "그중"이 거짓이 되고 확인율이 100%를 넘어 찍힌다. 실물 원장에서
// 실제로 나는 모양이다(처방은 턴 끝에, ack 은 그 뒤 note·finish 에 나므로 절단선이
// 둘 사이에 떨어질 수 있다 — 관측된 간격 최댓값 0.85시간).
func TestAckReachCutsRecentByWindow(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if err := s.UpsertProject(ctx, model.Project{ID: "p", Path: "/repo/p"}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m", Hostname: "dev"}); err != nil {
		t.Fatalf("머신 등록 실패: %v", err)
	}

	sess := make([]model.Session, 0, 6)
	for i := 1; i <= 6; i++ {
		got, _, err := s.OpenSession(ctx, "p", "m", fmt.Sprintf("/wt%d", i), fmt.Sprintf("cc-%d", i), "")
		if err != nil {
			t.Fatalf("OpenSession wt%d: %v", i, err)
		}
		sess = append(sess, got)
	}
	s1, s2, s3, s4, s5, s6 := sess[0], sess[1], sess[2], sess[3], sess[4], sess[5]

	outside := time.Now().Add(-48 * time.Hour) // 창 밖 — 24시간보다 확실히 오래됐다

	// 창 안: s1 은 발화·판단·ack 전부 · s4 는 판단이 0 · s5 는 판단은 있고 ack 이 없다.
	s.LogEvent(ctx, "prescribe", "p", s1.ID, map[string]any{"key": "k"})
	s.LogEvent(ctx, "prescribe_ack", "p", s1.ID, map[string]any{"keys": []string{"k"}})
	s.LogEvent(ctx, "prescribe", "p", s4.ID, map[string]any{"key": "k"})
	s.LogEvent(ctx, "prescribe", "p", s5.ID, map[string]any{"key": "k"})

	// 창 밖: s2 는 발화만 · s3 은 발화와 ack 둘 다. 둘 다 판단은 있다 —
	// 판단이 없으면 reachable 절단이 빠졌을 때도 값이 안 움직여 시험이 헛돈다.
	logEventAt(t, s, outside, "prescribe", "p", s2.ID)
	logEventAt(t, s, outside, "prescribe", "p", s3.ID)
	logEventAt(t, s, outside, "prescribe_ack", "p", s3.ID)

	// 절단선에 걸친 카드 — 처방은 창 밖, ack 은 창 안이다. 세 수의 포함관계를 지키는지가
	// 여기서 갈린다. 이 카드는 **어느 수에도 안 들어가야 한다**(최근 벌 기준): 이 구간에
	// 처방을 받은 적이 없으므로 분모에 없고, 분모에 없는 카드가 분자에만 있으면 안 된다.
	logEventAt(t, s, outside, "prescribe", "p", s6.ID)
	s.LogEvent(ctx, "prescribe_ack", "p", s6.ID, map[string]any{"keys": []string{"k"}})

	for _, id := range []string{s1.ID, s2.ID, s3.ID, s5.ID, s6.ID} {
		if _, err := s.AddJudgment(ctx, model.Judgment{
			Project: "p", SessionID: id, Kind: model.JudgmentDecision, Title: "t", Body: "b",
		}); err != nil {
			t.Fatalf("AddJudgment(%s): %v", id, err)
		}
	}

	all, recent, err := s.AckReach(ctx, "p", time.Now().Add(-ackWindow))
	if err != nil {
		t.Fatalf("AckReach: %v", err)
	}

	wantAll := AckCounts{Emitted: 6, Reachable: 5, Acked: 3}
	if all != wantAll {
		t.Errorf("전 역사 %+v, 원하는 것 %+v — 전 역사 벌에 절단이 새어 들어간 것이 아닌지 보라", all, wantAll)
	}
	wantRecent := AckCounts{Emitted: 3, Reachable: 2, Acked: 1}
	if recent != wantRecent {
		t.Errorf("최근 %+v, 원하는 것 %+v — 어느 부질의의 at 절단이 빠졌는지 갈린 수를 보라"+
			"(Emitted 6 면 발화 · Reachable 5 면 도달 가능 · Acked 2 면 ack 을 그 이벤트 자신의"+
			" 시각으로만 자른 것이다 — s6 이 분모 없이 분자에 들어갔다)", recent, wantRecent)
	}
	// ★ 포함관계는 이 지표의 문장이 성립하는 조건이다. 꼬리가 "발화 카드 N · **그중** ack 이
	// 닿을 수 있는 카드 M · 실제 ack K" 라고 말하므로, K > M 이거나 M > N 이면 그 문장이
	// 거짓이 되고 확인율이 100%를 넘어 찍힌다.
	for _, c := range []struct {
		label string
		v     AckCounts
	}{{"전 역사", all}, {"최근", recent}} {
		if c.v.Acked > c.v.Reachable || c.v.Reachable > c.v.Emitted {
			t.Errorf("%s %+v — 포함관계가 깨졌다(Acked ⊆ Reachable ⊆ Emitted 여야 한다). "+
				"꼬리 문장의 \"그중\"이 거짓이 된다", c.label, c.v)
		}
	}
}

// 한 대화가 카드 둘로 갈려도 **대화 한 개**로 센다 — 그리고 판단이 형제 카드에 있어도
// 도달 가능으로 잡힌다.
//
// ★ 이것이 이 지표가 무엇을 재는지를 정하는 자리다. 이 저장소의 규율은 "작업은 워크트리로
// 연다"이고, 카드 정체가 3중키 (machine, worktree, cc) 라 **규율을 지키는 대화는 반드시
// 갈린다**(설계다 — DESIGN §3, 되돌릴 수 없다). 카드로 세면 그 갈림이 분모를 부풀려
// 확인율이 규율이 아니라 갈림을 잰다. 앞 항목이 분모를 가른 뒤에도 남아 있던 몫이 이것이다.
//
// ★ 그리고 갈림은 분모만 부풀린 것이 아니라 **미확인을 지우고 있었다.** 처방이 A 카드에
// 뜨고 판단이 B 카드에 쌓이면 A 는 판단이 0이라 도달 가능에서 빠진다 — 즉 그 대화의
// 미확인이 관측에서 사라진다. 실측(2026-08-08 전 역사)에서 카드로 세면 13/13 = 100%,
// 대화로 세면 15개 중 13 = 87% 였다. 100%는 규율이 완벽해서가 아니었다.
func TestAckReachCountsConversationsNotCards(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if err := s.UpsertProject(ctx, model.Project{ID: "p", Path: "/repo/p"}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m", Hostname: "dev"}); err != nil {
		t.Fatalf("머신 등록 실패: %v", err)
	}

	// 한 대화("cc-1")가 워크트리를 옮겨 카드 둘이 됐다 — 이 레포에서 매일 나는 모양이다.
	// 처방은 먼저 열린 카드에 떴고, 판단은 워크트리로 들어간 뒤 쌓였다.
	first, _, err := s.OpenSession(ctx, "p", "m", "/repo/p", "cc-1", "")
	if err != nil {
		t.Fatalf("OpenSession 저장소 루트: %v", err)
	}
	moved, _, err := s.OpenSession(ctx, "p", "m", "/repo/p/.flightdeck/worktrees/x", "cc-1", "")
	if err != nil {
		t.Fatalf("OpenSession 워크트리: %v", err)
	}
	if first.ID == moved.ID {
		t.Fatal("카드가 안 갈렸다 — 3중키가 바뀌었으면 이 시험의 전제를 다시 써야 한다")
	}
	// 처방은 훅이 쏘고 훅은 두 카드 모두에 닿으므로 **양쪽에 뜬다** — 실물이 그렇다.
	// 판단은 MCP 가 쓰고 그것은 옮겨간 카드에만 쌓인다.
	s.LogEvent(ctx, "prescribe", "p", first.ID, map[string]any{"key": "k"})
	s.LogEvent(ctx, "prescribe", "p", moved.ID, map[string]any{"key": "k"})
	if _, err := s.AddJudgment(ctx, model.Judgment{
		Project: "p", SessionID: moved.ID, Kind: model.JudgmentDecision, Title: "t", Body: "b",
	}); err != nil {
		t.Fatalf("AddJudgment(형제 카드): %v", err)
	}

	// 갈리지 않은 대화 하나를 나란히 둔다 — 값이 전부 1이 되어 시험이 퇴화하는 것을 막는다.
	solo, _, err := s.OpenSession(ctx, "p", "m", "/repo/p/.flightdeck/worktrees/y", "cc-2", "")
	if err != nil {
		t.Fatalf("OpenSession 단독: %v", err)
	}
	s.LogEvent(ctx, "prescribe", "p", solo.ID, map[string]any{"key": "k"})

	// ★ 머신 축을 함께 잠근다. cc_session_id 는 Claude Code 가 발급하는 값이라 **머신을
	// 가로질러 유일하지 않다** — 키에서 machine_id 를 빼면 다른 노트북의 같은 id 를 가진
	// 대화가 한 개로 접혀, 두 사람의 규율이 한 수에 섞인다. 여기서 "cc-1" 을 다른 머신에
	// 다시 두어 그것이 **셋째 대화**로 세지는지 본다.
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m2", Hostname: "other"}); err != nil {
		t.Fatalf("둘째 머신 등록 실패: %v", err)
	}
	elsewhere, _, err := s.OpenSession(ctx, "p", "m2", "/repo/p", "cc-1", "")
	if err != nil {
		t.Fatalf("OpenSession 다른 머신: %v", err)
	}
	s.LogEvent(ctx, "prescribe", "p", elsewhere.ID, map[string]any{"key": "k"})

	all, recent, err := s.AckReach(ctx, "p", time.Now().Add(-ackWindow))
	if err != nil {
		t.Fatalf("AckReach: %v", err)
	}
	// 처방이 뜬 카드는 넷이지만 대화는 셋이다(갈린 것 · 단독 · 다른 머신의 동명 대화).
	want := AckCounts{Emitted: 3, Reachable: 1, Acked: 0}
	if all != want {
		t.Errorf("전 역사 %+v, 원하는 것 %+v — 발화 4면 카드를 센 것이고, 2면 머신 축이 빠져 "+
			"다른 머신의 같은 cc 가 한 대화로 접힌 것이다. 도달 가능 0이면 형제 카드의 판단을 "+
			"못 봤다", all, want)
	}
	if recent != want {
		t.Errorf("최근 %+v, 원하는 것 %+v — 최근 벌만 카드 단위로 남았다", recent, want)
	}
}

// 두 벌 모두 자기 프로젝트 밖을 안 센다.
//
// ★ 이 파일의 앞선 시험들은 프로젝트를 하나만 만들어서, 여섯 부질의의 `e.project=?` 를
// 통째로 지워도 전부 초록이었다. 절단이 들어오며 그 결손의 범위가 두 배가 됐다 —
// 새면 kweiza-cc-plugins 보드가 다른 프로젝트의 처방까지 합산해 규율 차이가 관측에서
// 사라진다. 여기서 두 벌을 함께 잠근다.
func TestAckReachIsScopedToItsProject(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	for _, p := range []string{"p", "q"} {
		if err := s.UpsertProject(ctx, model.Project{ID: p, Path: "/repo/" + p}); err != nil {
			t.Fatalf("프로젝트 등록 실패(%s): %v", p, err)
		}
	}
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m", Hostname: "dev"}); err != nil {
		t.Fatalf("머신 등록 실패: %v", err)
	}

	// p 는 한 장, q 는 두 장. 새면 p 조회가 q 의 것까지 세어 수가 커진다.
	mine, _, err := s.OpenSession(ctx, "p", "m", "/wt1", "cc-1", "")
	if err != nil {
		t.Fatalf("OpenSession wt1: %v", err)
	}
	s.LogEvent(ctx, "prescribe", "p", mine.ID, map[string]any{"key": "k"})
	s.LogEvent(ctx, "prescribe_ack", "p", mine.ID, map[string]any{"keys": []string{"k"}})
	for i, cc := range []string{"cc-2", "cc-3"} {
		other, _, err := s.OpenSession(ctx, "q", "m", fmt.Sprintf("/wtq%d", i), cc, "")
		if err != nil {
			t.Fatalf("OpenSession %s: %v", cc, err)
		}
		s.LogEvent(ctx, "prescribe", "q", other.ID, map[string]any{"key": "k"})
		s.LogEvent(ctx, "prescribe_ack", "q", other.ID, map[string]any{"keys": []string{"k"}})
		if _, err := s.AddJudgment(ctx, model.Judgment{
			Project: "q", SessionID: other.ID, Kind: model.JudgmentDecision, Title: "t", Body: "b",
		}); err != nil {
			t.Fatalf("AddJudgment(q): %v", err)
		}
	}
	if _, err := s.AddJudgment(ctx, model.Judgment{
		Project: "p", SessionID: mine.ID, Kind: model.JudgmentDecision, Title: "t", Body: "b",
	}); err != nil {
		t.Fatalf("AddJudgment(p): %v", err)
	}

	all, recent, err := s.AckReach(ctx, "p", time.Now().Add(-ackWindow))
	if err != nil {
		t.Fatalf("AckReach: %v", err)
	}
	want := AckCounts{Emitted: 1, Reachable: 1, Acked: 1}
	if all != want {
		t.Errorf("전 역사 %+v, 원하는 것 %+v — 3이 섞였으면 project 조건이 샌 것이다", all, want)
	}
	if recent != want {
		t.Errorf("최근 %+v, 원하는 것 %+v — 3이 섞였으면 최근 벌의 project 조건이 샌 것이다", recent, want)
	}
}

// 판단의 **나이**는 절단하지 않는다 — 자르는 것은 처방이 발화된 시각뿐이다.
//
// ★ 이것이 이 변경에서 가장 틀리기 쉬운 자리다. reachable 부질의의
// `EXISTS (SELECT 1 FROM judgment j …)` 에도 `j.at >= ?` 를 걸고 싶어지지만 틀렸다.
// reachable 이 묻는 것은 "이 카드에 ack 이 원리적으로 닿을 수 있나"이고, 그 답은
// 카드가 판단을 **가졌는가**이지 그 판단이 언제 쓰였는가가 아니다. 어제 판단을 남긴
// 세션이 오늘 처방을 받으면 그 카드는 여전히 ack 할 수 있다 — 판단을 자르면 그 카드가
// 분모에서 빠져 확인율이 근거 없이 올라간다.
func TestAckReachDoesNotCutJudgmentAge(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if err := s.UpsertProject(ctx, model.Project{ID: "p", Path: "/repo/p"}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m", Hostname: "dev"}); err != nil {
		t.Fatalf("머신 등록 실패: %v", err)
	}
	s1, _, err := s.OpenSession(ctx, "p", "m", "/wt1", "cc-1", "")
	if err != nil {
		t.Fatalf("OpenSession wt1: %v", err)
	}

	// 발화는 창 안, 판단은 창 밖이다.
	s.LogEvent(ctx, "prescribe", "p", s1.ID, map[string]any{"key": "k"})
	if _, err := s.AddJudgment(ctx, model.Judgment{
		Project: "p", SessionID: s1.ID, Kind: model.JudgmentDecision, Title: "t", Body: "b",
		At: time.Now().Add(-72 * time.Hour),
	}); err != nil {
		t.Fatalf("AddJudgment: %v", err)
	}

	all, recent, err := s.AckReach(ctx, "p", time.Now().Add(-ackWindow))
	if err != nil {
		t.Fatalf("AckReach: %v", err)
	}
	if recent.Reachable != 1 {
		t.Errorf("최근 도달 가능 %d, 원하는 것 1 — 판단 EXISTS 에 j.at 절단을 건 것이다. "+
			"판단이 사흘 전이어도 그 카드는 ack 할 수 있다", recent.Reachable)
	}
	// ★ 전 역사 벌도 함께 단정한다. 최근 벌만 보면 전 역사 쪽 EXISTS 에 `j.at >= e.at` 을
	// 거는 변이가 살아남는다 — 판단을 처방보다 **먼저** 남긴 카드가 조용히 분모에서 빠지고,
	// 그것이 이 항목이 잰 "갈림 비율"의 기준선을 옮긴다.
	if all.Reachable != 1 {
		t.Errorf("전 역사 도달 가능 %d, 원하는 것 1 — 전 역사 벌의 판단 EXISTS 에 나이 조건이 붙었다",
			all.Reachable)
	}
}
