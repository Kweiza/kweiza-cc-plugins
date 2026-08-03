package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/store"
)

// 제약 위반이 500 이 아니라 409 로 나가는지 — **소비자 좌표계(HTTP 응답)로만** 단정한다.

// ★ 항목 id 중복. 오타·재등록에서 흔한 정상적인 4xx 인데 500 으로 나가면
// ① 조사가 서버 쪽으로 돌고 ② 500 은 멱등 표에 안 남아 재시도가 계속 하류로 들어간다.
func TestDuplicateItemIDIsConflictNotInternal(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")

	body := map[string]any{
		"project": testProject, "session_id": sess,
		"id": "t9-dup", "title": "제목", "body": "본문",
	}
	// ── 대조 전제: 첫 등록이 201 인가 ──
	if w := e.write(http.MethodPost, "/api/v1/items", body); w.Code != http.StatusCreated {
		t.Fatalf("전제가 깨졌다 — 첫 등록이 %d 다: %s", w.Code, w.Body.String())
	}

	// ── 본 판정: 다른 멱등 키로 같은 id 를 다시 ──
	w := e.write(http.MethodPost, "/api/v1/items", body)
	if w.Code != http.StatusConflict {
		t.Fatalf("중복 id 가 %d 로 나갔다 — 409 를 기대했다: %s", w.Code, w.Body.String())
	}
	d := errorOf(t, w)
	if d["code"] != "duplicate" {
		t.Errorf("코드가 %v 다", d["code"])
	}
	msg, _ := d["message"].(string)
	if !strings.Contains(msg, "t9-dup") {
		t.Errorf("무엇이 중복인지가 응답에 없다: %q", msg)
	}
	guide, _ := d["guidance"].(string)
	if !strings.Contains(guide, "pick") {
		t.Errorf("처방이 무엇을 하면 되는지 말하지 않는다: %q", guide)
	}
	if d["request_id"] == "" {
		t.Error("request_id 가 비었다 — 응답과 로그를 잇는 열쇠다")
	}

	// 내부 좌표가 새면 안 된다. 드라이버 문구·SQL·파일 경로 전부.
	low := strings.ToLower(w.Body.String())
	for _, leak := range []string{"unique", "constraint", "sqlite", "insert into", ".db"} {
		if strings.Contains(low, leak) {
			t.Errorf("응답에 내부 문자열 %q 가 새어 나왔다: %s", leak, w.Body.String())
		}
	}

	// 4xx 는 로그 의무가 없다 — 오타 한 번에 ERROR 가 쌓이면 안 된다.
	for _, r := range e.logs.records(t) {
		if r["msg"] == "요청 처리 실패" {
			t.Errorf("4xx 인데 내부 오류 로그가 남았다: %v", r)
		}
	}
}

// ★ 등록 안 된 프로젝트로 판단을 쓰면 FK 위반이다. 중복과 **처방이 정반대**라 코드를 가른다.
func TestJudgmentToUnknownProjectIsConflictNotInternal(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")

	// ── 대조 전제: 등록된 프로젝트로는 201 인가 ──
	ok := e.write(http.MethodPost, "/api/v1/judgments", map[string]any{
		"project": testProject, "session_id": sess, "kind": "decision", "body": "대조",
	})
	if ok.Code != http.StatusCreated && ok.Code != http.StatusOK {
		t.Fatalf("전제가 깨졌다 — 정상 판단이 %d 다: %s", ok.Code, ok.Body.String())
	}

	// ── 본 판정 ──
	w := e.write(http.MethodPost, "/api/v1/judgments", map[string]any{
		"project": "등록안된프로젝트", "session_id": sess, "kind": "decision", "body": "본문",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("없는 프로젝트가 %d 로 나갔다 — 409 를 기대했다: %s", w.Code, w.Body.String())
	}
	d := errorOf(t, w)
	if d["code"] != "missing_ref" {
		t.Errorf("코드가 %v 다 — 중복과 같은 코드로 접으면 처방이 뒤집힌다", d["code"])
	}
	msg, _ := d["message"].(string)
	if !strings.Contains(msg, "등록안된프로젝트") {
		t.Errorf("무엇을 가리켰는지가 응답에 없다: %q", msg)
	}
	guide, _ := d["guidance"].(string)
	if strings.TrimSpace(guide) == "" {
		t.Error("처방이 비었다 — 무엇을 먼저 등록해야 하는지가 없으면 고칠 수 없다")
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "foreign key") {
		t.Errorf("드라이버 문구가 새어 나왔다: %s", w.Body.String())
	}
}

// ★ 표면이 대상 **전부**에 문구를 갖는지 전수로 본다.
//
// 하나만 빠져도 그 자리는 기본 문구로 나가고, 기본 문구는 무엇을 고칠지 말하지 못한다.
// 목록을 저장 계층이 갖고 시험이 그것을 도는 이유가 이것이다 — 여기 손으로 적으면
// 대상이 늘어도 시험이 안 늘어 원리적으로 못 본다.
func TestConflictWordsCoverEveryTarget(t *testing.T) {
	for _, tg := range store.ConflictTargets() {
		w, ok := conflictWordTable[tg]
		if !ok {
			t.Errorf("대상 %q 의 문구가 없다 — 기본 문구로 새어 나간다", tg)
			continue
		}
		if strings.TrimSpace(w.Name) == "" || strings.TrimSpace(w.Dup) == "" || strings.TrimSpace(w.Ref) == "" {
			t.Errorf("대상 %q 의 문구가 반쪽이다: %+v", tg, w)
		}
	}
	// 반대 방향도 본다 — 없어진 대상의 문구가 남아 있으면 죽은 코드다.
	live := map[store.ConflictTarget]bool{}
	for _, tg := range store.ConflictTargets() {
		live[tg] = true
	}
	for tg := range conflictWordTable {
		if !live[tg] {
			t.Errorf("문구는 있는데 저장 계층이 안 쓰는 대상이다: %q", tg)
		}
	}
}

// ConflictAdvice 는 순수 함수다. 표 밖 케이스로 **접기 종류가 늘었는데 여기가 안 는** 경우를 본다.
func TestConflictAdviceOutsideTheTable(t *testing.T) {
	// 모르는 대상 — 기본 문구로 나가되 **409 는 유지한다**(500 으로 접으면 서버 결함으로 오분류된다).
	c := ConflictAdvice(&store.ConflictError{
		Kind: store.ConflictDuplicate, Target: "새로생긴표", ID: "x"})
	if c.Status != http.StatusConflict {
		t.Errorf("모르는 대상이 %d 로 나갔다", c.Status)
	}
	if strings.TrimSpace(c.Guidance) == "" {
		t.Error("모르는 대상의 처방이 비었다")
	}

	// 모르는 종류 — 사실대로 500 이다. 아는 척하면 그 자리에 새 거짓이 생긴다.
	c = ConflictAdvice(&store.ConflictError{Kind: "새로생긴종류", Target: store.TargetItem})
	if c.Status != http.StatusInternalServerError || !c.Internal {
		t.Errorf("모르는 종류를 %d(internal=%v) 로 냈다 — 아는 척하면 안 된다", c.Status, c.Internal)
	}

	if got := ConflictAdvice(nil); got.Status != http.StatusOK {
		t.Errorf("nil 이 %d 를 냈다", got.Status)
	}
}
