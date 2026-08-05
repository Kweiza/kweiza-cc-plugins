package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 재기동을 넘는 멱등.
//
// ★ 앞 판의 이 축은 **거짓 초록**이었다. 재전송이 아예 안 일어나는 상태에서
// "판단 수가 그대로다"를 근거로 멱등을 주장했고, 그 단정은 "아무 일도 안 일어남"과
// 구분되지 않았다. 여기서는 **실제로 같은 키의 요청을 재기동 뒤에 다시 보낸다.**

// restart 는 **같은 DB 위에 새 서버를 조립한다.** 프로세스 재기동과 같은 상태다 —
// 메모리 멱등 표가 비고, 남는 것은 DB 뿐이다.
func (e *env) restart() {
	e.t.Helper()
	srv := newServer(e.svc, Options{Log: e.srv.opt.Log})
	// ── 대조 전제: 새 서버의 메모리 표가 정말 비었는가 ──
	// 이것이 성립하지 않으면 아래의 "재생됐다"는 DB 가 아니라 메모리가 낸 것이고,
	// 그러면 이 시험은 자기가 지킨다는 축을 원리적으로 못 본다.
	srv.idem.mu.Lock()
	n := len(srv.idem.entries)
	srv.idem.mu.Unlock()
	if n != 0 {
		e.t.Fatalf("전제가 깨졌다 — 새 서버의 메모리 멱등 표에 %d건이 있다", n)
	}
	if srv.idem.db == nil {
		e.t.Fatal("전제가 깨졌다 — 새 서버에 멱등 영속 계층이 안 붙었다")
	}
	e.srv = srv
	e.h = srv.chain(srv.routes())
}

func (e *env) judgmentCount(kind model.JudgmentKind) int {
	e.t.Helper()
	js, err := e.st.ListJudgmentsByKind(context.Background(), testProject, kind, 500)
	if err != nil {
		e.t.Fatalf("판단 조회 실패: %v", err)
	}
	return len(js)
}

// ★ 재기동 뒤 같은 키로 다시 보내면 **재생**이어야 한다.
//
// 이 조합이 나는 상황이 정확히 설계 §7 이 겨냥한 시나리오다 — 서버가 죽어 아웃박스가
// 쌓이고, 살아나서 재생이 돈다. 그때 서버는 방금 재기동해 기억이 비어 있고,
// 판단은 추가 전용이라 그때 들어간 중복은 되돌릴 수 없다.
func TestIdempotencyOutlivesServerRestartForJudgments(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")

	body := map[string]any{
		"project": testProject, "session_id": sess,
		"kind": "decision", "body": "되돌릴 수 없는 판단",
	}
	first := e.do(http.MethodPost, "/api/v1/judgments", body, withKey("cc-1:42"))
	if first.Code != http.StatusCreated {
		t.Fatalf("첫 판단이 %d 다: %s", first.Code, first.Body.String())
	}
	// ── 대조 전제: 정말 한 건 들어갔는가 ──
	if got := e.judgmentCount(model.JudgmentDecision); got != 1 {
		t.Fatalf("전제가 깨졌다 — 첫 요청 뒤 판단이 %d건이다", got)
	}

	e.restart()

	second := e.do(http.MethodPost, "/api/v1/judgments", body, withKey("cc-1:42"))
	if second.Code != first.Code {
		t.Fatalf("재기동 뒤 재시도가 %d 다(첫 응답 %d): %s", second.Code, first.Code, second.Body.String())
	}
	if second.Header().Get("Idempotency-Replayed") != "true" {
		t.Error("재기동 뒤 재시도에 재생 표식이 없다 — 소비자가 '두 번 들어갔나'를 구분할 수 없다")
	}
	if second.Body.String() != first.Body.String() {
		t.Errorf("재생 본문이 다르다:\n%s\n%s", first.Body.String(), second.Body.String())
	}
	// 진짜 축은 여기다: 서버가 **실제로 갖게 된** 판단 수.
	if got := e.judgmentCount(model.JudgmentDecision); got != 1 {
		t.Fatalf("재기동 뒤 재시도로 판단이 %d건이 됐다 — 판단은 추가 전용이라 되돌릴 수 없다", got)
	}

	// ── 대조 ②: 키가 다르면 그대로 하류에 닿아 **한 건이 더 들어간다.**
	//   이 단정이 없으면 위가 "재기동 뒤에는 쓰기가 아예 안 된다"와 구분되지 않는다.
	third := e.do(http.MethodPost, "/api/v1/judgments", body, withKey("cc-1:43"))
	if third.Code != http.StatusCreated {
		t.Fatalf("다른 키의 쓰기가 %d 다: %s", third.Code, third.Body.String())
	}
	if got := e.judgmentCount(model.JudgmentDecision); got != 2 {
		t.Fatalf("다른 키인데 판단이 %d건이다 — 대조가 성립하지 않았다", got)
	}
}

// 항목도 같은 축이다. 중복 id 는 이제 409 인데, 재기동 뒤 재시도가
// **201 이 아니라 409** 를 받으면 클라이언트는 "만들지 못했다"로 읽는다.
func TestIdempotencyOutlivesServerRestartForItems(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")

	body := map[string]any{
		"project": testProject, "session_id": sess,
		"id": "t9-restart", "title": "제목", "body": "본문",
	}
	first := e.do(http.MethodPost, "/api/v1/items", body, withKey("cc-1:1"))
	if first.Code != http.StatusCreated {
		t.Fatalf("첫 등록이 %d 다: %s", first.Code, first.Body.String())
	}

	e.restart()

	second := e.do(http.MethodPost, "/api/v1/items", body, withKey("cc-1:1"))
	if second.Code != http.StatusCreated {
		t.Fatalf("재기동 뒤 재시도가 %d 다 — 재생이면 201 이어야 한다: %s",
			second.Code, second.Body.String())
	}
	if second.Header().Get("Idempotency-Replayed") != "true" {
		t.Error("재생 표식이 없다")
	}
}

// ★ 재기동을 안 넘겨야 하는 쓰기는 **안 넘긴다.** 이것이 위 시험들의 대조다 —
// 이 단정이 없으면 "전부 저장한다"와 "고른 것만 저장한다"가 구분되지 않는다.
//
// 선점 응답은 **지금 상태**라 재생하면 남이 반납한 뒤에도 옛 거절이 나간다.
func TestNonPersistedRouteIsNotReplayedAfterRestart(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")
	if w := e.write(http.MethodPost, "/api/v1/items", map[string]any{
		"project": testProject, "session_id": sess,
		"id": "t9-np", "title": "제목", "body": "본문",
	}); w.Code != http.StatusCreated {
		t.Fatalf("전제가 깨졌다 — 항목 등록이 %d 다: %s", w.Code, w.Body.String())
	}

	claim := map[string]any{"project": testProject, "session_id": sess}
	if w := e.do(http.MethodPost, "/api/v1/items/t9-np/claim", claim, withKey("cc-1:9")); w.Code != http.StatusOK {
		t.Fatalf("선점이 %d 다: %s", w.Code, w.Body.String())
	}

	e.restart()

	w := e.do(http.MethodPost, "/api/v1/items/t9-np/claim", claim, withKey("cc-1:9"))
	if w.Header().Get("Idempotency-Replayed") == "true" {
		t.Fatalf("선점 응답이 재기동을 넘어 재생됐다 — 남이 반납한 뒤에도 옛 답이 나간다: %s",
			w.Body.String())
	}
	// DB 에도 안 남아 있어야 한다.
	if _, err := e.st.GetIdemRecord(context.Background(), "cc-1:9"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("선점 응답이 멱등 표에 저장됐다: %v", err)
	}
}

// 5xx 는 어느 층에도 저장되지 않는다 — 일시 장애를 영구 응답으로 굳히면 안 된다.
func TestServerErrorIsNotPersisted(t *testing.T) {
	e := newEnv(t, nil)
	// 판단 대상 프로젝트를 아예 안 만든 채 저장 계층을 닫아 500 을 만든다.
	if err := e.st.Close(); err != nil {
		t.Fatalf("DB 닫기 실패: %v", err)
	}
	w := e.do(http.MethodPost, "/api/v1/judgments", map[string]any{
		"project": testProject, "session_id": "s", "kind": "decision", "body": "본문",
	}, withKey("cc-1:5xx"))
	// ── 대조 전제: 정말 5xx 인가 ──
	if w.Code < 500 {
		t.Skipf("전제가 성립하지 않았다 — 닫힌 DB 가 %d 를 냈다(이 시험은 5xx 를 만들어야 한다)", w.Code)
	}
	// 저장 계층이 닫혔으니 조회도 실패한다. 여기서 볼 것은 "저장이 시도되지 않았다"이므로
	// 경고 로그가 아니라 **기록 저장 실패 로그가 없다**로 본다.
	for _, r := range e.logs.records(t) {
		if msg, _ := r["msg"].(string); strings.Contains(msg, "멱등 기록 저장 실패") {
			t.Fatalf("5xx 를 저장하려 했다: %v", r)
		}
	}
}

// 영속 계층이 고장 나도 요청은 죽지 않는다. 다만 **삼키지도 않는다.**
type failingBacking struct{}

func (failingBacking) GetIdemRecord(context.Context, string) (store.IdemRecord, error) {
	return store.IdemRecord{}, errors.New("저장소가 고장났다")
}
func (failingBacking) PutIdemRecord(context.Context, store.IdemRecord, time.Duration, int) error {
	return errors.New("저장소가 고장났다")
}

func TestBrokenBackingDoesNotFailTheRequestButIsLogged(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.idem.db = failingBacking{}
	sess := e.openSession("cc-1")

	w := e.do(http.MethodPost, "/api/v1/judgments", map[string]any{
		"project": testProject, "session_id": sess, "kind": "decision", "body": "본문",
	}, withKey("cc-1:broken"))
	if w.Code != http.StatusCreated {
		t.Fatalf("영속 계층 고장이 요청을 죽였다: %d %s", w.Code, w.Body.String())
	}
	warned := 0
	for _, r := range e.logs.records(t) {
		msg, _ := r["msg"].(string)
		if strings.Contains(msg, "멱등 기록") && r["level"] == "WARN" {
			warned++
			if _, ok := r["error"]; !ok {
				t.Errorf("경고에 원인 전문이 없다: %v", r)
			}
		}
	}
	if warned == 0 {
		t.Error("영속 계층이 고장났는데 아무 줄도 안 남았다 — 보장이 꺼진 사실이 어디에도 없다")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 어느 라우트를 남기나 — 순수 함수
// ─────────────────────────────────────────────────────────────────────────────

func TestJudgePersistIdempotency(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
		wantInReason string
	}{
		{http.MethodPost, "/api/v1/items", true, "PK"},
		{http.MethodPost, "/api/v1/items/t9-a/finish", true, "추가 전용"},
		{http.MethodPost, "/api/v1/judgments", true, "추가 전용"},
		{http.MethodPost, "/api/v1/counters/contract_revision/next", true, "되돌릴 수 없다"},

		// 되돌릴 수 있거나(upsert) 응답이 지금 상태인 것들.
		{http.MethodPost, "/api/v1/sessions", false, "메모리 표로 충분"},
		{http.MethodPatch, "/api/v1/sessions/s1", false, "메모리 표로 충분"},
		{http.MethodPost, "/api/v1/sessions/s1/signals", false, "메모리 표로 충분"},
		{http.MethodPost, "/api/v1/footprints", false, "메모리 표로 충분"},
		{http.MethodPost, "/api/v1/items/t9-a/claim", false, "메모리 표로 충분"},
		{http.MethodPut, "/api/v1/snapshots/k", false, "메모리 표로 충분"},

		// 랜딩 레인 둘. 안 남기는 것이 판정이고, **사유가 서로 다르다** —
		// 하나는 응답이 지금 상태라서, 하나는 두 번째 호출이 구조적으로 거절돼서다.
		{http.MethodPost, "/api/v1/landing", false, "지금 내 차례인가"},
		{http.MethodPost, "/api/v1/landing/rows/12/release", false, "중복이 원리적으로 안 생긴다"},
		// 표 밖: 경계는 구조로 잡는다(접두 문자열이면 아래가 통과한다).
		{http.MethodPost, "/api/v1/landingX", false, "메모리 표로 충분"},
		{http.MethodPost, "/api/v1/landing/rows/12", false, "메모리 표로 충분"},
		{http.MethodPost, "/api/v1/landing/rows/12/released", false, "메모리 표로 충분"},

		// 읽기.
		{http.MethodGet, "/api/v1/items/next", false, "읽기 요청"},
		{http.MethodGet, "/api/v1/judgments", false, "읽기 요청"},

		// ★ 표 밖: 접두 문자열로 맞추면 아래가 통과한다. 경계는 구조로 잡는다.
		{http.MethodPost, "/api/v1/itemsXYZ", false, "메모리 표로 충분"},
		{http.MethodPost, "/api/v1/items/a/b/c", false, "메모리 표로 충분"},
		{http.MethodPost, "/api/v1/items/a/finished", false, "메모리 표로 충분"},
		{http.MethodPost, "/api/v2/judgments", false, "메모리 표로 충분"},
		{http.MethodPost, "/judgments", false, "메모리 표로 충분"},
		{http.MethodPost, "", false, "메모리 표로 충분"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			got := JudgePersistIdempotency(c.method, c.path)
			if got.Persist != c.want {
				t.Errorf("Persist = %v, want %v (사유: %s)", got.Persist, c.want, got.Reason)
			}
			if !strings.Contains(got.Reason, c.wantInReason) {
				t.Errorf("사유에 %q 가 없다: %q", c.wantInReason, got.Reason)
			}
		})
	}
}

// 표 밖 케이스: 사유는 **어떤 입력에도** 비면 안 된다.
func TestJudgePersistIdempotencyAlwaysGivesReason(t *testing.T) {
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "", "post"}
	paths := []string{"", "/", "//", "/api", "/api/v1", "/api/v1/items", "/api/v1/items/",
		"/api/v1/items//finish", "/api/v1/counters//next", strings.Repeat("/x", 40)}
	for _, m := range methods {
		for _, p := range paths {
			if strings.TrimSpace(JudgePersistIdempotency(m, p).Reason) == "" {
				t.Fatalf("사유가 비었다: %q %q", m, p)
			}
		}
	}
	// 소문자 메서드도 같은 판정이어야 한다 — 대소문자로 축이 갈리면 우회가 생긴다.
	if JudgePersistIdempotency("post", "/api/v1/judgments").Persist !=
		JudgePersistIdempotency("POST", "/api/v1/judgments").Persist {
		t.Fatal("메서드 대소문자에 따라 판정이 갈린다")
	}
}
