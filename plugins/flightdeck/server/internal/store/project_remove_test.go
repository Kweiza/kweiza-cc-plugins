package store

import (
	"context"
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
// 태스크 지시("삭제가 실제로 도는 것을 실물로 확인하라")대로 세션·선점·랜딩 줄 행·발자국을
// 실제로 만들고, 지운 뒤 그 넷이 전부 없는지 직접 잰다.
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
	if err := s.AddItem(ctx, model.Item{
		Project: "junk", ID: "i1", Title: "t", Body: "b", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("항목 등록 실패: %v", err)
	}
	if _, err := s.ClaimItem(ctx, "junk", "i1", sess.ID, time.Time{}); err != nil {
		t.Fatalf("선점 실패: %v", err)
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

	// ⑵ 자식 행이 전부 없다 — session_workspace·claim·landing_queue·session 은 projectRefTables
	// 가 직접 지우고, footprint 는 session ON DELETE CASCADE 로 딸려 사라져야 한다.
	for _, q := range []struct{ tbl, sql string }{
		{"session", `SELECT count(*) FROM session WHERE project = 'junk'`},
		{"claim", `SELECT count(*) FROM claim WHERE project = 'junk'`},
		{"landing_queue", `SELECT count(*) FROM landing_queue WHERE project = 'junk'`},
		{"item", `SELECT count(*) FROM item WHERE project = 'junk'`},
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

// TestRemoveProjectTranslatesForeignJudgmentFKViolation 은 RemoveProject 자신의 방어 2중을
// 잰다: 사전 판정(JudgeProjectRemoval)을 건너뛰고(또는 판정과 삭제 사이에 레이스가 나서)
// 다른 프로젝트의 판단이 이 프로젝트의 세션을 가리키는 채로 삭제를 시도하면, 드라이버
// 원문("FOREIGN KEY constraint failed") 대신 사람이 읽을 수 있는 사유가 나와야 한다.
//
// ★ 정상 경로에서는 JudgeProjectRemoval 이 judgment_foreign 카운트로 여기 닿기 전에 이미
// 거절한다(TestRemoveProjectRefusesWithForeignJudgment). 이 시험은 그 1차 방어를 우회해
// store.RemoveProject 를 직접 불러 **2차 방어**(FK 오류 번역)만 단독으로 잰다.
func TestRemoveProjectTranslatesForeignJudgmentFKViolation(t *testing.T) {
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

	err = s.RemoveProject(ctx, "junk") // 사전 판정을 거치지 않고 직접 부른다
	if err == nil {
		t.Fatal("FK 위반이 나야 하는데 성공했다")
	}
	if !strings.Contains(err.Error(), "참조 무결성") {
		t.Fatalf("원문 FK 오류만 나왔다 — 무엇이 막았는지 사람이 못 읽는다: %v", err)
	}
	// junk 프로젝트는 실패 뒤에도 그대로 있어야 한다(트랜잭션 롤백).
	if _, gerr := s.GetProject(ctx, "junk"); gerr != nil {
		t.Fatalf("실패했는데 프로젝트가 지워졌다: %v", gerr)
	}
}
