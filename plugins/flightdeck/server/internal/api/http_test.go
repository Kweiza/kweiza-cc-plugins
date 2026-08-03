package api

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// 소비자 좌표계 시험 — 실제 HTTP 응답과 구조화 로그 실물 한 줄로만 단정한다.

// ★ 이 시험의 **대조가 먼저다.** 통과한 요청이 정말 줄을 남기는지를 먼저 단정하지 않으면,
// 로그 수집이 통째로 깨져 있어도 "401 에 줄이 없다"가 초록으로 나온다 —
// 대조가 조용히 실패하면서 기대한 숫자를 그대로 내는 실패가 실재한다.
func TestUnauthenticatedRequestIs401AndLeavesNoAccessLog(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Token = "s3cret" })

	// ① 대조: 루프백은 토큰 없이 통과하고, 그 요청은 액세스 로그에 **남는다**.
	pass := e.do(http.MethodGet, "/metrics", nil, loopback(), withHeader("X-Request-Id", "probe-pass"))
	if pass.Code != http.StatusOK {
		t.Fatalf("루프백이 막혔다: %d %s", pass.Code, pass.Body.String())
	}
	if n := len(e.logs.served(t, "probe-pass")); n != 1 {
		t.Fatalf("게이트를 통과한 요청의 액세스 로그가 %d줄이다 — 대조가 성립하지 않아 아래 단정은 무의미하다", n)
	}

	// ② 본시험: 토큰 없는 원격 요청은 401 이고 **줄을 남기지 않는다**.
	deny := e.do(http.MethodGet, "/metrics", nil, withHeader("X-Request-Id", "probe-401"))
	if deny.Code != http.StatusUnauthorized {
		t.Fatalf("원격 무인증 요청이 %d 다", deny.Code)
	}
	if got := deny.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Fatalf("WWW-Authenticate 헤더가 없다: %q", got)
	}
	if got := deny.Header().Get("X-Request-Id"); got != "probe-401" {
		t.Fatalf("401 응답에 상관키가 없다: %q", got)
	}
	if n := len(e.logs.served(t, "probe-401")); n != 0 {
		t.Fatalf("401 이 액세스 로그에 %d줄 남았다 — 초과 트래픽이 그대로 로그 증폭이 된다", n)
	}
	body := errorOf(t, deny)
	if body["code"] != "unauthorized" || body["request_id"] != "probe-401" {
		t.Fatalf("401 본문이 다르다: %v", body)
	}

	// ③ 그 축의 정본은 카운터다.
	m := e.do(http.MethodGet, "/metrics", nil, loopback())
	if !strings.Contains(m.Body.String(), "flightdeck_unauthorized_total 1") {
		t.Fatalf("401 이 카운터에도 안 잡혔다 — 그러면 이 축의 원천이 하나도 없다:\n%s", m.Body.String())
	}
}

func TestRateLimitedRequestLeavesNoAccessLog(t *testing.T) {
	fixed := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	e := newEnv(t, func(o *Options) {
		o.RatePerMinute = 60
		o.Burst = 1
		o.Clock = func() time.Time { return fixed } // 시간이 안 흐르므로 토큰이 안 찬다
	})

	first := e.do(http.MethodGet, "/metrics", nil, withRemote("127.0.0.1:1"), withHeader("X-Request-Id", "rl-1"))
	if first.Code != http.StatusOK {
		t.Fatalf("첫 요청이 막혔다: %d", first.Code)
	}
	if n := len(e.logs.served(t, "rl-1")); n != 1 {
		t.Fatalf("대조 실패: 통과한 요청의 줄이 %d개다", n)
	}

	second := e.do(http.MethodGet, "/metrics", nil, withRemote("127.0.0.1:2"), withHeader("X-Request-Id", "rl-2"))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("한도 초과가 %d 다", second.Code)
	}
	if n := len(e.logs.served(t, "rl-2")); n != 0 {
		t.Fatalf("429 가 액세스 로그에 %d줄 남았다", n)
	}
	// 다른 주소는 자기 버킷을 쓴다.
	other := e.do(http.MethodGet, "/metrics", nil, withRemote("127.0.0.2:9"))
	if other.Code != http.StatusOK {
		t.Fatalf("다른 주소가 남의 한도에 걸렸다: %d", other.Code)
	}
	if !strings.Contains(other.Body.String(), "flightdeck_rate_limited_total 1") {
		t.Fatalf("429 가 카운터에 안 잡혔다:\n%s", other.Body.String())
	}
}

func TestIdempotentRetryReturnsSameResult(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")

	body := map[string]any{
		"project": testProject, "session_id": sess,
		"id": "t9-idem", "title": "멱등 시험", "body": "같은 키로 두 번 부른다",
	}
	first := e.do(http.MethodPost, "/api/v1/items", body, withKey("cc-1:7"))
	if first.Code != http.StatusCreated {
		t.Fatalf("첫 등록이 실패했다: %d %s", first.Code, first.Body.String())
	}
	second := e.do(http.MethodPost, "/api/v1/items", body, withKey("cc-1:7"))
	if second.Code != first.Code {
		t.Fatalf("재시도 상태코드가 다르다: %d vs %d (%s)", second.Code, first.Code, second.Body.String())
	}
	if second.Body.String() != first.Body.String() {
		t.Fatalf("재시도 본문이 다르다:\n%s\n%s", first.Body.String(), second.Body.String())
	}
	if second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("재생 표식이 없다 — 소비자가 '두 번 만들어졌나'를 구분할 수 없다")
	}

	// ★ 대조: 키가 다르면 같은 요청이 그대로 하류에 닿아 **다른 결과**가 나온다.
	//   이 단정이 없으면 위 두 줄이 "그냥 두 번 성공했다"와 구분되지 않는다.
	//
	// 그 "다른 결과"는 409(duplicate)다 — 저장 계층이 제약 위반을 타입 있는 오류로
	// 올리고 ClassifyError 가 그것을 접는다. 500 이 아닌 것이 중요하다:
	// 500 은 멱등 표에 저장되지 않는 등급이라 재시도가 계속 하류로 들어간다.
	// 그 축의 전문은 conflict_test.go 가 본다.
	third := e.do(http.MethodPost, "/api/v1/items", body, withKey("cc-1:8"))
	if third.Code != http.StatusConflict {
		t.Fatalf("다른 키로 온 같은 항목이 %d 다 — 409 를 기대했다: %s", third.Code, third.Body.String())
	}
	if strings.Contains(strings.ToLower(third.Body.String()), "unique") {
		t.Fatalf("제약 위반 문구가 응답에 새어 나왔다: %s", third.Body.String())
	}
}

func TestIdempotencyKeyReuseWithDifferentBodyIsRejected(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")

	mk := func(id string) map[string]any {
		return map[string]any{"project": testProject, "session_id": sess,
			"id": id, "title": "제목", "body": "본문"}
	}
	if w := e.do(http.MethodPost, "/api/v1/items", mk("t9-a"), withKey("cc-1:1")); w.Code != http.StatusCreated {
		t.Fatalf("첫 등록 실패: %d %s", w.Code, w.Body.String())
	}
	w := e.do(http.MethodPost, "/api/v1/items", mk("t9-b"), withKey("cc-1:1"))
	if w.Code != http.StatusConflict {
		t.Fatalf("키 재사용이 %d 다 — 남의 응답을 받게 된다", w.Code)
	}
	if errorOf(t, w)["code"] != "idempotency_conflict" {
		t.Fatalf("충돌 코드가 다르다: %s", w.Body.String())
	}
	// 그리고 두 번째 항목은 만들어지지 않았어야 한다(거절은 하류에 닿기 전이다).
	if _, err := e.st.GetItem(t.Context(), testProject, "t9-b"); err == nil {
		t.Fatal("충돌로 거절했는데 항목이 만들어졌다")
	}
}

func TestWriteWithoutIdempotencyKeyIsRejected(t *testing.T) {
	e := newEnv(t, nil)
	w := e.do(http.MethodPost, "/api/v1/sessions", map[string]any{
		"project": testProject, "project_path": e.repo, "machine_id": "m1",
		"worktree": e.repo, "cc_session_id": "cc-x",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("키 없는 쓰기가 %d 다: %s", w.Code, w.Body.String())
	}
	d := errorOf(t, w)
	if d["code"] != "idempotency_key_required" {
		t.Fatalf("코드가 다르다: %v", d)
	}
	if !strings.Contains(d["guidance"].(string), "Idempotency-Key") {
		t.Fatalf("처방에 무엇을 실어야 하는지가 없다: %v", d)
	}
	// 읽기는 키 없이 통과한다.
	if got := e.do(http.MethodGet, "/healthz", nil); got.Code != http.StatusOK {
		t.Fatalf("읽기가 키를 요구했다: %d", got.Code)
	}
}

// 오류 응답에 내부 좌표가 새면 안 된다 — 원인 전문은 **로그로만** 간다.
func TestInternalErrorDoesNotLeakInternalNames(t *testing.T) {
	e := newEnv(t, nil)
	if err := e.st.Close(); err != nil {
		t.Fatalf("DB 닫기 실패: %v", err)
	}

	w := e.write(http.MethodPost, "/api/v1/sessions", map[string]any{
		"project": testProject, "project_path": e.repo, "machine_id": "m1",
		"worktree": e.repo, "cc_session_id": "cc-1",
	}, withHeader("X-Request-Id", "leak-probe"))

	// ★ 전제부터 단정한다: 정말 500 이 났는가. 안 났으면 아래 grep 은 공허하다.
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("전제 실패: 하류가 죽었는데 응답이 %d 다 (%s)", w.Code, w.Body.String())
	}

	body := w.Body.String()
	for _, needle := range []string{
		e.dbPath, "sqlite", "SQL", "database", "store.", "service.", ".go", "/tmp/",
	} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(needle)) {
			t.Fatalf("오류 응답에 내부 이름 %q 가 새어 나왔다:\n%s", needle, body)
		}
	}
	d := errorOf(t, w)
	if d["request_id"] != "leak-probe" {
		t.Fatalf("응답과 로그를 이을 열쇠가 없다: %v", d)
	}

	// 그리고 원인 전문은 **로그에 있어야** 한다. 없으면 아무 데도 없는 것이다.
	found := false
	for _, r := range e.logs.records(t) {
		if r["request_id"] == "leak-probe" && r["level"] == "ERROR" {
			if msg, _ := r["error"].(string); strings.Contains(msg, "closed") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("원인 전문이 로그에도 없다 — 응답에서 뺀 것이 어디에도 안 남았다:\n%s", e.logs.String())
	}
}

func TestPanicIsRecoveredWith500AndStackInLog(t *testing.T) {
	e := newEnv(t, nil)
	// 같은 게이트 사슬에 터지는 핸들러를 끼운다 — 정상 라우트로는 만들 수 없는 축이다.
	h := e.srv.chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("시험용 패닉: /secret/path/fd.db")
	}))
	e.h = h

	w := e.do(http.MethodGet, "/api/v1/dashboard.json", nil, withHeader("X-Request-Id", "boom"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("패닉이 %d 로 나갔다: %s", w.Code, w.Body.String())
	}
	d := errorOf(t, w)
	if d["code"] != "panic" {
		t.Fatalf("코드가 다르다: %v", d)
	}
	if strings.Contains(w.Body.String(), "/secret/path") || strings.Contains(w.Body.String(), "goroutine") {
		t.Fatalf("패닉 값·스택이 응답에 실렸다: %s", w.Body.String())
	}

	var panicked, served map[string]any
	for _, r := range e.logs.records(t) {
		if r["request_id"] != "boom" {
			continue
		}
		switch r["msg"] {
		case "request panicked":
			panicked = r
		case "request served":
			served = r
		}
	}
	if panicked == nil {
		t.Fatalf("패닉 로그 줄이 없다:\n%s", e.logs.String())
	}
	if s, _ := panicked["stack"].(string); !strings.Contains(s, "goroutine") {
		t.Fatalf("스택이 없다 — 이 줄이 발생 지점의 유일한 원천이다: %v", panicked["stack"])
	}
	if s, _ := panicked["error"].(string); !strings.Contains(s, "시험용 패닉") {
		t.Fatalf("패닉 값이 안 실렸다: %v", panicked["error"])
	}
	// recover 가 accessLog 안쪽이라는 사실을 소비자 좌표계(로그 줄)로 단정한다.
	if served == nil {
		t.Fatal("패닉 요청의 액세스 로그가 없다")
	}
	if got, _ := served["status"].(float64); int(got) != http.StatusInternalServerError {
		t.Fatalf("액세스 로그의 상태코드가 %v 다 — 패닉이 지표에서 사라진다", served["status"])
	}
}

func TestUnmatchedRouteAnswersJSONWithoutEchoingPath(t *testing.T) {
	e := newEnv(t, nil)
	w := e.do(http.MethodGet, "/api/v1/nope/<script>", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("%d 가 나왔다: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type 이 %q 다 — 오류 파싱 경로가 두 벌이 된다", ct)
	}
	if strings.Contains(w.Body.String(), "nope") || strings.Contains(w.Body.String(), "script") {
		t.Fatalf("경로가 응답에 되비쳤다(반사형 노출 통로다): %s", w.Body.String())
	}
	m := e.do(http.MethodGet, "/metrics", nil)
	if !strings.Contains(m.Body.String(), `route="GET unmatched"`) {
		t.Fatalf("못 맞춘 요청의 라벨이 경로로 새거나 없다:\n%s", m.Body.String())
	}
}

func TestHealthzIsReachableWithoutTokenAndAnnouncesAuth(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Token = "s3cret" })
	// 원격 + 토큰 없음. 다른 표면이면 401 인 조건이다.
	w := e.do(http.MethodGet, "/healthz", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/healthz 가 %d 다 — 배너 훅이 서버 상태를 물을 창이 사라진다: %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	auth, ok := body["auth"].(map[string]any)
	if !ok {
		t.Fatalf("auth 절이 없다: %s", w.Body.String())
	}
	if auth["token_set"] != true || auth["loopback_open"] != true {
		t.Fatalf("인증 설정이 안 실렸다: %v", auth)
	}
	if s, _ := auth["notice"].(string); !strings.Contains(s, "루프백") {
		t.Fatalf("사람이 읽을 한 줄이 없다: %v", auth)
	}
	if strings.Contains(w.Body.String(), e.dbPath) || strings.Contains(w.Body.String(), "db_path") {
		t.Fatalf("무인증 표면에 DB 경로가 실렸다: %s", w.Body.String())
	}

	// 토큰 미설정 서버는 그 사실을 말해야 한다 — 조용한 무인증은 안 된다.
	open := newEnv(t, nil).do(http.MethodGet, "/healthz", nil)
	oa := decodeBody(t, open)["auth"].(map[string]any)
	if oa["token_set"] != false || !strings.Contains(oa["notice"].(string), "무인증") {
		t.Fatalf("무인증 상태를 안 알린다: %v", oa)
	}
}

// 큐 한 바퀴 — 열기 → 등록 → 추천 → 선점 → 마무리. 전부 소비자 좌표계로 단정한다.
func TestQueueRoundTrip(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")

	add := e.write(http.MethodPost, "/api/v1/items", map[string]any{
		"project": testProject, "session_id": sess, "id": "t9-round",
		"title": "한 바퀴", "body": "추천되고 선점되고 끝난다", "paths": []string{"internal/api/"},
	})
	if add.Code != http.StatusCreated {
		t.Fatalf("항목 등록 실패: %d %s", add.Code, add.Body.String())
	}

	// ① 추천 — **선점하지 않는다**. 그 사실이 응답 문장에 있어야 한다.
	next := e.do(http.MethodGet, "/api/v1/items/next?project="+testProject+"&session_id="+sess, nil)
	if next.Code != http.StatusOK {
		t.Fatalf("추천 실패: %d %s", next.Code, next.Body.String())
	}
	nb := decodeBody(t, next)
	if nb["mode"] != "recommended" {
		t.Fatalf("mode 가 %v 다", nb["mode"])
	}
	if _, has := nb["claim"]; has {
		t.Fatalf("추천인데 선점 행이 실렸다: %s", next.Body.String())
	}
	if s, _ := nb["reason"].(string); !strings.Contains(s, "아직 선점하지 않았다") {
		t.Fatalf("추천이 선점으로 읽힐 수 있다: %v", nb["reason"])
	}
	if s, _ := nb["scope"].(string); s == "" {
		t.Fatal("무엇을 후보로 봤는지가 없다 — 안 본 것을 침묵하면 안 된다")
	}

	// ② 선점.
	claim := e.write(http.MethodPost, "/api/v1/items/t9-round/claim", map[string]any{
		"project": testProject, "session_id": sess,
	}, withHeader("X-Request-Id", "claim-1"))
	if claim.Code != http.StatusOK {
		t.Fatalf("선점 실패: %d %s", claim.Code, claim.Body.String())
	}
	cb := decodeBody(t, claim)
	if cb["mode"] != "claimed" {
		t.Fatalf("mode 가 %v 다: %s", cb["mode"], claim.Body.String())
	}
	if cb["branch"] != "t9-round" {
		t.Fatalf("브랜치 이름(=항목 id)이 안 왔다: %v", cb["branch"])
	}
	// ★ 액세스 로그 한 줄이 다섯 축을 다 실어야 한다. session_id 는 본문을 읽은 뒤에
	//   채우는 축이라 여기가 그 결선이 살아 있는지 보는 유일한 자리다.
	lines := e.logs.served(t, "claim-1")
	if len(lines) != 1 {
		t.Fatalf("선점 요청의 액세스 로그가 %d줄이다", len(lines))
	}
	for _, f := range []string{"route", "status", "duration", "request_id", "session_id"} {
		if v, ok := lines[0][f]; !ok || v == "" {
			t.Fatalf("액세스 로그에 %s 축이 없다: %v", f, lines[0])
		}
	}
	if lines[0]["session_id"] != sess {
		t.Fatalf("액세스 로그의 세션 좌표가 다르다: %v", lines[0]["session_id"])
	}
	if lines[0]["route"] != "POST /api/v1/items/{id}/claim" {
		t.Fatalf("라우트가 패턴이 아니라 실제 경로다(카디널리티가 터진다): %v", lines[0]["route"])
	}

	// ③ 재개 — 이미 자기 것이면 거절이 아니라 맥락 재출력이다.
	again := e.write(http.MethodPost, "/api/v1/items/t9-round/claim", map[string]any{
		"project": testProject, "session_id": sess,
	})
	if again.Code != http.StatusOK || decodeBody(t, again)["mode"] != "resumed" {
		t.Fatalf("재개 경로가 아니다: %d %s", again.Code, again.Body.String())
	}

	// ④ body 없이 마무리하면 무엇을 적어야 하는지가 **그 자리에서** 온다.
	empty := e.write(http.MethodPost, "/api/v1/items/t9-round/finish", map[string]any{
		"project": testProject, "session_id": sess, "outcome": "done",
	})
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("본문 없는 마무리가 %d 다", empty.Code)
	}
	if g, _ := errorOf(t, empty)["guidance"].(string); !strings.Contains(g, "일부러 안 한 것") {
		t.Fatalf("처방이 안 왔다: %q", g)
	}

	// ⑤ 마무리 — 판단 + 후속이 한 호출이다.
	fin := e.write(http.MethodPost, "/api/v1/items/t9-round/finish", map[string]any{
		"project": testProject, "session_id": sess, "outcome": "done",
		"title": "한 바퀴 마무리", "body": "landed: 표면을 얹었다. 기각한 것은 프레임워크 도입이다",
		"followups": []map[string]any{{
			"id": "t9-followup", "title": "후속", "body": "여기서 이어서 한다",
		}},
	})
	if fin.Code != http.StatusOK {
		t.Fatalf("마무리 실패: %d %s", fin.Code, fin.Body.String())
	}
	fb := decodeBody(t, fin)
	item, _ := fb["item"].(map[string]any)
	if item["State"] != "done" {
		t.Fatalf("항목이 안 닫혔다: %v", item)
	}
	if len(fb["followups"].([]any)) != 1 {
		t.Fatalf("후속이 같은 호출에서 안 들어갔다: %s", fin.Body.String())
	}

	// ⑥ 판단은 검색으로 도달할 수 있어야 한다.
	found := e.do(http.MethodGet, "/api/v1/judgments?project="+testProject+"&q=landed", nil)
	if found.Code != http.StatusOK {
		t.Fatalf("검색 실패: %d %s", found.Code, found.Body.String())
	}
	if n, _ := decodeBody(t, found)["count"].(float64); n < 1 {
		t.Fatalf("방금 남긴 판단이 안 잡힌다: %s", found.Body.String())
	}
}

func TestSignalsAndFootprintsShareOneCoordinateSystem(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")

	sig := e.write(http.MethodPost, "/api/v1/sessions/"+sess+"/signals", map[string]any{
		"kind": "tool", "paths": []string{e.repo + "/internal/api/api.go"},
	})
	if sig.Code != http.StatusAccepted {
		t.Fatalf("신호 기록이 %d 다: %s", sig.Code, sig.Body.String())
	}

	fp := e.write(http.MethodPost, "/api/v1/footprints", map[string]any{
		"session_id": sess, "origin": "declared",
		"paths": []string{e.repo + "/internal/api/sse.go", "internal/api/api.go"},
	})
	if fp.Code != http.StatusOK {
		t.Fatalf("발자국 기록이 %d 다: %s", fp.Code, fp.Body.String())
	}
	paths, _ := decodeBody(t, fp)["paths"].([]any)
	// ★ 절대경로가 저장소 상대로 옮겨져야 한다 — 좌표계가 어긋나면 겹침 축이 조용히 죽는다.
	for _, p := range paths {
		if strings.HasPrefix(p.(string), "/") {
			t.Fatalf("절대경로가 그대로 저장됐다: %v", paths)
		}
	}

	bad := e.write(http.MethodPost, "/api/v1/footprints", map[string]any{
		"session_id": sess, "origin": "guessed", "paths": []string{"x"},
	})
	if bad.Code != http.StatusBadRequest || errorOf(t, bad)["code"] != "bad_origin" {
		t.Fatalf("모르는 출처가 통과했다: %d %s", bad.Code, bad.Body.String())
	}

	// 상태 전이 — blocked 에는 사유가 필수다(판정은 store 의 순수 함수가 한다).
	noWhy := e.write(http.MethodPatch, "/api/v1/sessions/"+sess, map[string]any{"state": "blocked"})
	if noWhy.Code != http.StatusBadRequest {
		t.Fatalf("사유 없는 blocked 가 %d 다: %s", noWhy.Code, noWhy.Body.String())
	}
	ok := e.write(http.MethodPatch, "/api/v1/sessions/"+sess, map[string]any{
		"state": "blocked", "why": "계약 개정을 기다린다",
	})
	if ok.Code != http.StatusOK {
		t.Fatalf("상태 전이가 %d 다: %s", ok.Code, ok.Body.String())
	}
}

func TestSnapshotManualNeedsEvidenceAndCounterAllocates(t *testing.T) {
	e := newEnv(t, nil)
	e.openSession("cc-1") // 프로젝트 등록

	bad := e.write(http.MethodPut, "/api/v1/snapshots/progress", map[string]any{
		"project": testProject, "value": "62", "method": "manual",
	})
	if bad.Code != http.StatusBadRequest || errorOf(t, bad)["code"] != "bad_snapshot" {
		t.Fatalf("근거 없는 손 기재가 통과했다: %d %s", bad.Code, bad.Body.String())
	}
	good := e.write(http.MethodPut, "/api/v1/snapshots/progress", map[string]any{
		"project": testProject, "value": "62", "method": "manual",
		"evidence": "12파트 전수 판정 2026-08-03", "input_digest": "abc",
	})
	if good.Code != http.StatusOK {
		t.Fatalf("스냅숏 저장이 %d 다: %s", good.Code, good.Body.String())
	}
	got := e.do(http.MethodGet, "/api/v1/snapshots/progress?project="+testProject, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("스냅숏 조회가 %d 다: %s", got.Code, got.Body.String())
	}
	miss := e.do(http.MethodGet, "/api/v1/snapshots/nope?project="+testProject, nil)
	if miss.Code != http.StatusNotFound {
		t.Fatalf("없는 스냅숏이 %d 다: %s", miss.Code, miss.Body.String())
	}

	for want := 1; want <= 2; want++ {
		w := e.write(http.MethodPost, "/api/v1/counters/contract_revision/next",
			map[string]any{"project": testProject})
		if w.Code != http.StatusOK {
			t.Fatalf("발번이 %d 다: %s", w.Code, w.Body.String())
		}
		if v, _ := decodeBody(t, w)["value"].(float64); int(v) != want {
			t.Fatalf("발번 값이 %v 다 — 기대 %d", v, want)
		}
	}
}

func TestDashboardIsReadOnlyAndCarriesFreshness(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")

	w := e.do(http.MethodGet, "/api/v1/dashboard.json?project="+testProject+"&self="+sess, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("대시보드가 %d 다: %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	// ★ 모든 패널에 신선도가 붙는다 — 서버가 죽었을 때 마지막 상태가 현재 사실인 척하는 것을 막는다.
	fr, ok := body["freshness"].(map[string]any)
	if !ok || fr["Source"] == "" {
		t.Fatalf("신선도가 없다: %s", w.Body.String())
	}
	// git 저장소가 아니므로 실패 축이 이름과 함께 실려야 한다.
	fails, _ := body["failures"].([]any)
	if len(fails) == 0 {
		t.Fatalf("파생이 실패했는데 그 사실이 안 실렸다: %s", w.Body.String())
	}
	if sessions, _ := body["sessions"].([]any); len(sessions) != 1 {
		t.Fatalf("세션 카드가 %d개다: %s", len(sessions), w.Body.String())
	}
	// 쓰기 표면이 없다는 것을 소비자 좌표계로 단정한다.
	if got := e.write(http.MethodPost, "/api/v1/dashboard.json", map[string]any{}); got.Code != http.StatusNotFound {
		t.Fatalf("대시보드에 쓰기 표면이 열려 있다: %d", got.Code)
	}
	if got := e.do(http.MethodGet, "/api/v1/dashboard.json", nil); got.Code != http.StatusBadRequest {
		t.Fatalf("project 없는 조회가 %d 다: %s", got.Code, got.Body.String())
	}
}
