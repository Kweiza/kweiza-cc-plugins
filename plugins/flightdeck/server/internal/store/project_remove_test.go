package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// ★ 브리프(TestProjectRefTablesCoverSchema, schema.sql 를 정규식으로 훑는 시험)는 여기 없다.
// 앞 태스크가 이미 더 나은 것을 커밋했다 — project_ref_counts_test.go 의
// TestProjectRefTablesCoverEveryProjectColumn 이 **살아 있는 DB 의 스키마**(sqlite_master +
// PRAGMA table_info)로 같은 대조를 하고, landing_queue 가 증분에만 있어서 정규식이 놓쳤던
// 바로 그 결함을 막는다. 중복해서 만들지 않는다.

// TestRemoveProjectRefusesWithItems 는 항목이 있으면 안 지운다는 단정이다.
func TestRemoveProjectRefusesWithItems(t *testing.T) {
	ok, reason := JudgeProjectRemoval(map[string]int{"item": 1, "judgment": 0})
	if ok {
		t.Fatal("항목이 있는데 통과했다 — 639항목짜리를 한 명령으로 날리는 길이 열린다")
	}
	if !strings.Contains(reason, "항목") {
		t.Fatalf("사유가 무엇이 막았는지를 안 말한다: %q", reason)
	}
}

// TestRemoveProjectRefusesWithJudgments 는 판단이 있으면 안 지운다는 단정이다.
//
// ★ 이것은 정책이 아니라 **원장이 정한 제약**이다. judgment_no_delete 트리거가 판단 삭제를
// 원리적으로 막고, judgment.project 가 FK 라 프로젝트 행을 붙잡는다. 우회하지 않는다.
func TestRemoveProjectRefusesWithJudgments(t *testing.T) {
	ok, reason := JudgeProjectRemoval(map[string]int{"item": 0, "judgment": 3})
	if ok {
		t.Fatal("판단이 있는데 통과했다 — FK 위반으로 죽는다")
	}
	if !strings.Contains(reason, "판단") {
		t.Fatalf("사유가 무엇이 막았는지를 안 말한다: %q", reason)
	}
}

// TestRemoveProjectRefusesWithForeignJudgment 는 **다른 프로젝트의 판단**이 이 프로젝트의
// 세션을 가리키고 있어도 거절한다는 단정이다.
//
// ★ judgment.session_id 는 session(id) 를 CASCADE 없이 참조하고 judgment.project 와는
// 독립 컬럼이다(schema.sql:230-231) — 그래서 남의 프로젝트의 판단이 이 프로젝트의 세션을
// 가리킬 수 있다(실물 경로: service.NoteInput 은 Project 와 SessionID 를 각자 받고 서로
// 검증하지 않는다). 이 프로젝트 자신의 "judgment" 카운트(project = id)는 그 판단을 못
// 잡는다 — project 컬럼이 다르기 때문이다. counts["judgment_foreign"] 이 그 축이다.
func TestRemoveProjectRefusesWithForeignJudgment(t *testing.T) {
	ok, reason := JudgeProjectRemoval(map[string]int{"item": 0, "judgment": 0, "judgment_foreign": 2})
	if ok {
		t.Fatal("다른 프로젝트의 판단이 이 프로젝트의 세션을 가리키는데 통과했다 — " +
			"session 삭제가 FK 위반으로 죽는다")
	}
	if !strings.Contains(reason, "판단") {
		t.Fatalf("사유가 무엇이 막았는지를 안 말한다: %q", reason)
	}
}

// TestRemoveProjectDeletesChildrenAndKeepsEvents 는 실제 삭제 한 바퀴다.
//
// ★ 브리프 원안은 project 등록 + 이벤트 하나만 두고 지웠다 — session·claim·landing_queue·
// footprint 는 만들지 않았다. 그러면 이 시험은 "삭제 순서가 옳다"를 사실상 안 잰다:
// 항목 6번째인 session 앞에 CASCADE 없는 FK 넷(landing_queue·claim·resource_hold·job)이
// 오게 만든 바로 그 순서 결함(앞 태스크 8a7ca7f 의 리뷰 사유)이 이 시험으로는 안 걸린다.
// 태스크 지시("삭제가 실제로 도는 것을 실물로 확인하라")대로 세션·랜딩 줄 행·발자국을
// 실제로 만들고, 지운 뒤 그것들이 전부 없는지 직접 잰다.
//
// ★ 리뷰 #1 수정 이후 **item·claim 은 이 시험에 없다.** RemoveProject 가 이제 자기
// 트랜잭션 안에서 JudgeProjectRemoval 을 다시 평가하므로(재-판정), item 이 하나라도
// 있으면 직접 호출도 거절된다 — 그리고 claim 은 (project, item_id) → item(project, id)
// 가 ON DELETE CASCADE 라 item 없이는 존재할 수 없다(claim.session_id 자체는 별개 FK지만
// 그 행이 생기려면 먼저 item 이 있어야 한다 — ClaimItem 의 전제). 즉 "item=0 인데 claim>0"
// 인 상태는 스키마 제약상 존재할 수 없고, item 이 있으면 애초에 재-판정이 막는다 — claim
// 이 실제로 지워지는 것을 검증할 조합이 원리적으로 없다. claim 을 지우는 DELETE 문 자체는
// projectRefTables 루프에 그대로 남아 있다(순서가 틀리면 여전히 FK 위반을 내야 하므로)ㅡ
// 다만 이 성공 경로 시험으로는 그 문이 항상 0행에 대해 도는 것만 확인된다.
func TestRemoveProjectDeletesChildrenAndKeepsEvents(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if err := s.UpsertProject(ctx, model.Project{
		ID: "junk", Path: "/tmp/junk", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("등록 실패: %v", err)
	}
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m1", Hostname: "h"}); err != nil {
		t.Fatalf("머신 등록 실패: %v", err)
	}
	sess, _, err := s.OpenSession(ctx, "junk", "m1", "/tmp/junk", "cc1", "", time.Time{})
	if err != nil {
		t.Fatalf("세션 열기 실패: %v", err)
	}
	if err := s.Tx(ctx, func(tx *Tx) error {
		_, err := tx.EnqueueLanding("junk", sess.ID, time.Now().UTC())
		return err
	}); err != nil {
		t.Fatalf("랜딩 줄 등록 실패: %v", err)
	}
	if err := s.Touch(ctx, sess.ID, "services/foo.go", model.OriginObserved, time.Now().UTC()); err != nil {
		t.Fatalf("발자국 기록 실패: %v", err)
	}
	s.LogEvent(ctx, "project.test", "junk", "", map[string]any{"why": "삭제 뒤에도 남아야 한다"})

	if err := s.RemoveProject(ctx, "junk"); err != nil {
		t.Fatalf("삭제 실패: %v", err)
	}
	if _, err := s.GetProject(ctx, "junk"); err == nil {
		t.Fatal("프로젝트가 그대로 있다")
	}

	// ⑵ 자식 행이 전부 없다 — session_workspace·landing_queue·session 은 projectRefTables
	// 가 직접 지우고, footprint 는 session ON DELETE CASCADE 로 딸려 사라져야 한다.
	for _, q := range []struct{ tbl, sql string }{
		{"session", `SELECT count(*) FROM session WHERE project = 'junk'`},
		{"landing_queue", `SELECT count(*) FROM landing_queue WHERE project = 'junk'`},
		{"footprint", `SELECT count(*) FROM footprint WHERE session_id = '` + sess.ID + `'`},
	} {
		var n int
		if err := s.db.QueryRowContext(ctx, q.sql).Scan(&n); err != nil {
			t.Fatalf("%s 조회 실패: %v", q.tbl, err)
		}
		if n != 0 {
			t.Fatalf("%s 에 %d행이 남았다 — 삭제 순서나 CASCADE 가 안 지켜졌다", q.tbl, n)
		}
	}

	// ★ event 는 남는다. event.project 는 FK 가 아니라 컬럼이라 FK 도 안 울고,
	//   "이런 프로젝트가 있었다"가 원장에 남는 유일한 길이다.
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM event WHERE project = ?`, "junk").Scan(&n); err != nil {
		t.Fatalf("이벤트 조회 실패: %v", err)
	}
	if n == 0 {
		t.Fatal("삭제가 이벤트까지 가져갔다 — 그러면 지웠다는 사실 자체가 원장에서 사라진다")
	}
}

// TestRemoveProjectRejudgesInsideTransactionCatchesLateItem 은 리뷰 #1 이 지적한 경합을
// 결정론적으로 재현한다: service.RemoveProject 가 이미 "항목 0건"으로 판정한 **뒤**,
// 실제 삭제 트랜잭션이 열리기 **전**에 다른 세션이 fd add 로 항목을 넣으면 어떻게 되는지를
// 순서를 고정해 재현한다(진짜 고루틴 경합은 타이밍에 의존해 들쭉날쭉하다 — 이 순서 자체가
// 곧 경합이 뜻하는 바이므로 순차 재현으로 충분하다).
//
// ★ 이 시험이 실제로 재는 것은 「RemoveProject 가 (호출 밖의 사전 판정과 별개로) 자기
// 안에서 다시 세고 다시 판정한다」는 것뿐이다 — 재-셈을 s.Tx **밖**(예: 이 스토어 메서드
// 진입 직후, 트랜잭션을 열기 전)에 둬도 순서(①사전 판정 ②late item 추가 ③RemoveProject
// 호출)는 그대로라 이 시험은 그대로 초록이다. 「재-셈이 s.Tx 트랜잭션 경계 **안**이다」는
// 더 강한 성질(재-셈과 실제 DELETE 사이에 다른 트랜잭션이 못 끼어든다 — RemoveProject 의
// 함수 주석과 _txlock=immediate 근거)이고, 이 시험은 고루틴 경합이 없는 순차 재현이라 그
// 성질을 검증하지 않는다. 지금 그 경계 성질을 지키는 것은 **코드 리뷰뿐**이다 — 재-셈
// 호출(t.ProjectRefCounts)이 s.Tx 의 클로저 본문 안에 있는지 읽는 사람이 확인해야 한다.
func TestRemoveProjectRejudgesInsideTransactionCatchesLateItem(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if err := s.UpsertProject(ctx, model.Project{
		ID: "junk", Path: "/tmp/junk", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("등록 실패: %v", err)
	}

	// ① service.RemoveProject 가 부를 자리 — "항목 0건" 을 확인한다(호출 밖 판정).
	counts, err := s.ProjectRefCounts(ctx, "junk")
	if err != nil {
		t.Fatalf("사전 조회 실패: %v", err)
	}
	if counts["item"] != 0 {
		t.Fatalf("전제가 깨졌다 — item %d, 기대 0", counts["item"])
	}
	if ok, why := JudgeProjectRemoval(counts); !ok {
		t.Fatalf("전제가 깨졌다 — 사전 판정이 이미 거절했다: %s", why)
	}

	// ② 그 사이 다른 세션이 fd add 로 항목을 넣는다.
	if err := s.AddItem(ctx, model.Item{
		Project: "junk", ID: "late", Title: "t", Body: "b", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("항목 등록 실패: %v", err)
	}

	// ③ confirm=true 로 실제 삭제를 부른다 — 트랜잭션 안의 재-판정이 새 항목을 봐야 한다.
	err = s.RemoveProject(ctx, "junk")
	var raced *RemovalRefusedError
	if !errors.As(err, &raced) {
		t.Fatalf("RemovalRefusedError 를 기대했는데 %v(타입 %T) 다", err, err)
	}
	if !strings.Contains(raced.Reason, "항목") {
		t.Fatalf("사유가 무엇이 막았는지를 안 말한다: %q", raced.Reason)
	}
	if raced.Counts["item"] != 1 {
		t.Fatalf("재-판정 카운트가 새 항목을 못 봤다: item=%d", raced.Counts["item"])
	}

	// 프로젝트도 새 항목도 그대로 있어야 한다 — 트랜잭션이 롤백됐다는 뜻이다.
	if _, gerr := s.GetProject(ctx, "junk"); gerr != nil {
		t.Fatalf("거절됐는데 프로젝트가 지워졌다: %v", gerr)
	}
	if _, gerr := s.GetItem(ctx, "junk", "late"); gerr != nil {
		t.Fatalf("거절됐는데 항목이 사라졌다: %v", gerr)
	}
}

// TestRemoveProjectRefusesRaceViaInternalRejudge 는 RemoveProject 의 **재-판정**(리뷰 #1)
// 이 사전 판정(JudgeProjectRemoval 의 호출 밖 평가)을 아예 건너뛴 직접 호출도 잡아낸다는
// 단정이다. "other" 프로젝트의 판단이 "junk" 의 세션을 가리키는 채로 store.RemoveProject
// 를 곧장 부른다 — 트랜잭션 안의 재-판정이 judgment_foreign 카운트로 이것도 잡아서
// *RemovalRefusedError 로 깔끔하게 거절해야 한다.
//
// ★ 리뷰 #1 이전에는 이 시나리오가 실제 FK 위반까지 가서 RemovalBlockedError(2차 방어,
// 옛 이름 removalFKMessage)로 번역됐었다. 이제는 안 그런다 — _txlock=immediate 가 재-셈과
// DELETE 사이의 끼어듦 자체를 막으므로(RemoveProject 의 그 주석), judgment_foreign 축은
// 더는 2차 방어에 도달하지 않는다. 2차 방어가 실제로 잡는 것은 ProjectRefCounts 가 미리
// 안 세는 나머지 넷(claim·resource_hold·job·landing_queue) 뿐이다 —
// TestRemoveProjectTranslatesForeignLandingQueueFKViolation 이 그 경로를 잰다.
//
// ★ 이 시험은 ProjectRefCounts 가 실제로 judgment_foreign 을 세는지도 함께 잰다(리뷰 #3) —
// 그 질의가 항상 0을 내도록 퇴행하면 사전 판정도 이 재-판정도 이 교차 판단을 못 본 채
// 통과시킨다.
func TestRemoveProjectRefusesRaceViaInternalRejudge(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	for _, id := range []string{"junk", "other"} {
		if err := s.UpsertProject(ctx, model.Project{
			ID: id, Path: "/tmp/" + id, DefaultBranch: "main",
		}); err != nil {
			t.Fatalf("등록 실패(%s): %v", id, err)
		}
	}
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m1", Hostname: "h"}); err != nil {
		t.Fatalf("머신 등록 실패: %v", err)
	}
	sess, _, err := s.OpenSession(ctx, "junk", "m1", "/tmp/junk", "cc1", "", time.Time{})
	if err != nil {
		t.Fatalf("세션 열기 실패: %v", err)
	}
	// "other" 프로젝트의 판단이 "junk" 의 세션을 가리킨다 — 교차 참조.
	if _, err := s.AddJudgment(ctx, model.Judgment{
		Project: "other", SessionID: sess.ID, Kind: model.JudgmentDecision,
		Body: "junk 의 세션을 가리키는 남의 판단",
	}); err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}

	// ProjectRefCounts 가 이 교차 참조를 실제로 세는지(리뷰 #3).
	counts, err := s.ProjectRefCounts(ctx, "junk")
	if err != nil {
		t.Fatalf("카운트 조회 실패: %v", err)
	}
	if counts["judgment_foreign"] != 1 {
		t.Fatalf("judgment_foreign 이 %d, 기대 1 — 이 질의가 퇴행하면 판정이 이 교차 판단을 "+
			"못 본 채 통과시킨다", counts["judgment_foreign"])
	}

	err = s.RemoveProject(ctx, "junk") // 사전 판정을 거치지 않고 직접 부른다
	var raced *RemovalRefusedError
	if !errors.As(err, &raced) {
		t.Fatalf("RemovalRefusedError 를 기대했는데 %v(타입 %T) 다", err, err)
	}
	if !strings.Contains(raced.Reason, "판단") {
		t.Fatalf("사유가 무엇이 막았는지를 안 말한다: %q", raced.Reason)
	}
	// junk 프로젝트는 실패 뒤에도 그대로 있어야 한다(트랜잭션 롤백).
	if _, gerr := s.GetProject(ctx, "junk"); gerr != nil {
		t.Fatalf("실패했는데 프로젝트가 지워졌다: %v", gerr)
	}
}

// TestRemoveProjectTranslatesForeignLandingQueueFKViolation 은 RemoveProject 의 진짜
// **2차 방어**(RemovalBlockedError)를 잰다. judgment_foreign 축은 이제 재-판정(1차 방어,
// 리뷰 #1)이 트랜잭션 진입 직후 잡으므로 여기 안 온다 — 2차 방어가 실제로 잡는 것은
// ProjectRefCounts 가 미리 안 세는 나머지 넷(claim·resource_hold·job·landing_queue,
// ProjectRefCounts 의 그 주석) 이다. landing_queue 를 골랐다 — EnqueueLanding(project,
// sessionID, at) 이 judgment 의 NoteInput 과 같은 모양으로 project 와 sessionID 를 각자
// 받고 서로 검증하지 않는다.
func TestRemoveProjectTranslatesForeignLandingQueueFKViolation(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	for _, id := range []string{"junk", "other"} {
		if err := s.UpsertProject(ctx, model.Project{
			ID: id, Path: "/tmp/" + id, DefaultBranch: "main",
		}); err != nil {
			t.Fatalf("등록 실패(%s): %v", id, err)
		}
	}
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m1", Hostname: "h"}); err != nil {
		t.Fatalf("머신 등록 실패: %v", err)
	}
	sess, _, err := s.OpenSession(ctx, "junk", "m1", "/tmp/junk", "cc1", "", time.Time{})
	if err != nil {
		t.Fatalf("세션 열기 실패: %v", err)
	}
	// "other" 프로젝트의 랜딩 줄 행이 "junk" 의 세션을 가리킨다 — 교차 참조.
	if err := s.Tx(ctx, func(tx *Tx) error {
		_, err := tx.EnqueueLanding("other", sess.ID, time.Now().UTC())
		return err
	}); err != nil {
		t.Fatalf("랜딩 줄 등록 실패: %v", err)
	}

	err = s.RemoveProject(ctx, "junk")
	var blocked *RemovalBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("RemovalBlockedError 를 기대했는데 %v(타입 %T) 다", err, err)
	}
	if strings.Contains(blocked.Reason, "FOREIGN KEY") {
		t.Fatalf("원문 FK 오류가 사유에 새어 나왔다: %q", blocked.Reason)
	}
	if blocked.Table != "session" {
		t.Fatalf("표가 %q, 기대 session — junk 자신의 landing_queue 는 비어 있어 그 줄은 "+
			"무해하게 지워지고, 실제로 걸리는 것은 session 삭제여야 한다", blocked.Table)
	}
	// junk 프로젝트는 실패 뒤에도 그대로 있어야 한다(트랜잭션 롤백).
	if _, gerr := s.GetProject(ctx, "junk"); gerr != nil {
		t.Fatalf("실패했는데 프로젝트가 지워졌다: %v", gerr)
	}
}
