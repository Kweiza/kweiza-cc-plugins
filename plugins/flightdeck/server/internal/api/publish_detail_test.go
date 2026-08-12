package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// claim **밖의** 라우트가 발행하는 이벤트의 detail 을 못박는다.
//
// `claim_publish_test.go` 가 선점 라우트의 세 값(kind·item·overlaps)을 닫았고, 그 판단이
// "같은 부류가 다른 라우트에도 있을 것"이라고 **추정**만 남겨 두었다. 그 추정을 사실로 받아
// 적지 않고 전수로 쟀다 — claim 밖 publish 15곳의 detail 축 **35개를 한꺼번에 상수로
// 바꾸고 전 스위트를 돌렸더니 초록**이었다(실측 2026-08-11). 추정은 참이었고 범위는
// 추정보다 넓었다. 이 파일이 그 35축을 닫는다.
//
// ★ 이 축은 "코드는 맞고 시험이 없다"라 보통의 RED 가 안 난다. 그래서 시험을 쓰기 전에
// 변이를 먼저 넣어 전 스위트가 초록인 것을 눈으로 봤고, 쓴 뒤에는 축마다 변이를 하나씩
// 되넣어 빨강을 확인했다. 그 순서를 뒤집으면 "이 시험이 무엇을 잡는가"를 알 방법이 없다.
//
// 좌표계는 **와이어 한 줄**이다 — nextEvent(claim_publish_test.go)가 구독자에게 실제로
// 나간 바이트를 되읽는다.
//
// ★ 35축 중 **하나를 일부러 안 닫았다**: handlePatchSession 의 되읽기 실패 갈래가 내는
// session.state(detail.state). 그 갈래는 **결정론적으로 못 밟는다** — Service.SetState 가
// 트랜잭션 안에서 같은 조회를 먼저 하므로, 세션 행을 못 읽게 만들면 쓰기가 먼저 죽는다.
// 근거 전문은 patch_session_partial_test.go 의 머리 주석에 있다. 스윕에서 그 축의 변이만
// 살아남는 것은 **구멍이 아니라 그 사실의 관측**이다. 밟을 길이 생기기 전에는 못 닫는다.

// pairAxis 는 detail 축 하나를 **짝으로** 못박는다.
//
// 짝이 없으면 한쪽 값을 우연히 담은 상수가 전부 통과한다. 그래서 두 기대값이 같으면
// 단정 전에 시험을 세운다 — 그 순간 이 축은 아무것도 안 재고 있는 것이다(대조축).
func pairAxis(t *testing.T, axis string, a, b Event, wantA, wantB any) {
	t.Helper()
	wa, wb := wire(wantA), wire(wantB)
	if wa == wb {
		t.Fatalf("[%s] 짝이 안 갈렸다 — 두 요청의 기대값이 둘 다 %#v 다. "+
			"이 축은 그 값을 박은 상수도 통과하므로 지금 아무것도 안 재고 있다", axis, wa)
	}
	if got := a.Detail[axis]; got != wa {
		t.Errorf("[%s] 첫 이벤트가 %#v 를 실었다(기대 %#v)", axis, got, wa)
	}
	if got := b.Detail[axis]; got != wb {
		t.Errorf("[%s] 둘째 이벤트가 %#v 를 실었다(기대 %#v)", axis, got, wb)
	}
}

// pairKind 는 이벤트 **이름**을 짝으로 못박는다. 라우트가 mode 로 갈리는 자리(lane.<mode>)의 축이다.
func pairKind(t *testing.T, a, b Event, wantA, wantB string) {
	t.Helper()
	if wantA == wantB {
		t.Fatalf("[kind] 짝이 안 갈렸다 — 두 기대값이 둘 다 %q 다", wantA)
	}
	if a.Kind != wantA {
		t.Errorf("[kind] 첫 이벤트가 %q 로 발행됐다(기대 %q)", a.Kind, wantA)
	}
	if b.Kind != wantB {
		t.Errorf("[kind] 둘째 이벤트가 %q 로 발행됐다(기대 %q)", b.Kind, wantB)
	}
}

// wire 는 기대값을 **와이어 좌표계**로 옮긴다 — JSON 왕복에서 정수는 전부 float64 가 된다.
func wire(v any) any {
	switch n := v.(type) {
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return v
}

// skipEvent 는 준비 때문에 끼어드는 이벤트 하나를 **확인하고** 흘린다.
// 그냥 흘리면 순서가 바뀐 날 시험이 조용히 엉뚱한 이벤트를 재게 된다.
func skipEvent(t *testing.T, sub *Sub, wantKind string) {
	t.Helper()
	if ev := nextEvent(t, sub); ev.Kind != wantKind {
		t.Fatalf("흘려보낼 이벤트가 %q 다(기대 %q) — 이 시험이 읽는 순서가 어긋났다", ev.Kind, wantKind)
	}
}

// bodyOf 는 응답을 맵으로 읽고 상태코드를 확인한다.
func (e *env) okBody(t *testing.T, w *httptest.ResponseRecorder, what string) map[string]any {
	t.Helper()
	if w.Code != http.StatusOK && w.Code != http.StatusCreated && w.Code != http.StatusAccepted {
		t.Fatalf("%s 실패: %d %s", what, w.Code, w.Body.String())
	}
	return decodeBody(t, w)
}

// countOf 는 응답 본문의 배열 길이다. 서버가 정한 수는 요청이 아니라 **응답**에서 읽는다 —
// 두 표면이 갈리는 것이 이 시험이 잡으려는 사고다.
func countOf(t *testing.T, body map[string]any, key string) int {
	t.Helper()
	list, _ := body[key].([]any)
	return len(list)
}

// openLabeled 는 라벨을 지정해 세션을 연다. helper 의 openSession 은 라벨이 고정이라
// label 축의 짝을 만들 수 없다.
func (e *env) openLabeled(t *testing.T, cc, label string) (string, *httptest.ResponseRecorder) {
	t.Helper()
	w := e.write(http.MethodPost, "/api/v1/sessions", map[string]any{
		"project": testProject, "project_path": e.repo, "machine_id": "m1",
		"hostname": "host1", "worktree": e.repo, "cc_session_id": cc, "label": label,
	})
	body := e.okBody(t, w, "세션 열기")
	sess, _ := body["session"].(map[string]any)
	id, _ := sess["ID"].(string)
	if id == "" {
		t.Fatalf("세션 id 가 비었다: %s", w.Body.String())
	}
	return id, w
}

// openIn 은 **다른 프로젝트**에 세션을 하나 연다.
//
// 프로젝트 등록이 여기서만 일어나기 때문이다 — 발번·스냅숏·이동은 좌표가 등록돼 있지
// 않으면 409(missing_ref)로 거절된다. 그 거절 자체가 이 저장소의 규율이라 시험이
// 우회하지 않고 정문으로 등록한다.
func (e *env) openIn(t *testing.T, project, cc string) string {
	t.Helper()
	w := e.write(http.MethodPost, "/api/v1/sessions", map[string]any{
		"project": project, "project_path": e.repo, "machine_id": "m1",
		"hostname": "host1", "worktree": e.repo, "cc_session_id": cc, "label": "시험 세션",
	})
	body := e.okBody(t, w, "세션 열기("+project+")")
	sess, _ := body["session"].(map[string]any)
	id, _ := sess["ID"].(string)
	if id == "" {
		t.Fatalf("세션 id 가 비었다: %s", w.Body.String())
	}
	return id
}

// ─────────────────────────────────────────────────────────────────────────────
// 세션 표면
// ─────────────────────────────────────────────────────────────────────────────

// TestSessionOpenEventCarriesCreatedAndLabel 는 session.open 의 두 값을 못박는다.
//
//	created — 신규인가 재개인가. 상수면 소비자가 **아무도 아무것도 안 연 순간**을 새 세션으로 읽는다.
//	label   — 카드에 뜨는 이름.
func TestSessionOpenEventCarriesCreatedAndLabel(t *testing.T) {
	e := newEnv(t, nil)
	sub := e.srv.hub.Subscribe(testProject)
	defer e.srv.hub.Unsubscribe(sub)

	// ① 신규 · 라벨 가
	_, w1 := e.openLabeled(t, "cc-open-a", "라벨-가")
	if w1.Code != http.StatusCreated {
		t.Fatalf("신규 세션이 %d 로 답했다(기대 201) — created 축의 대조가 성립하지 않았다", w1.Code)
	}
	ev1 := nextEvent(t, sub)

	// ② 같은 3중키로 다시 — **재개**다. 쓰기가 없는 사건이라 created 가 갈려야 한다.
	_, w2 := e.openLabeled(t, "cc-open-a", "라벨-가")
	if w2.Code != http.StatusOK {
		t.Fatalf("재개가 %d 로 답했다(기대 200) — created 축의 대조가 성립하지 않았다", w2.Code)
	}
	ev2 := nextEvent(t, sub)
	pairAxis(t, "created", ev1, ev2, true, false)

	// ③ 다른 세션 · 라벨 나 — label 축의 짝이다.
	e.openLabeled(t, "cc-open-b", "라벨-나")
	ev3 := nextEvent(t, sub)
	pairAxis(t, "label", ev1, ev3, "라벨-가", "라벨-나")
}

// TestSessionStateEventCarriesTheRequestedState 는 session.state 가 **요청한 그 상태**를 나르는지 본다.
func TestSessionStateEventCarriesTheRequestedState(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-state")

	sub := e.srv.hub.Subscribe(testProject)
	defer e.srv.hub.Unsubscribe(sub)

	e.okBody(t, e.write(http.MethodPatch, "/api/v1/sessions/"+sess,
		map[string]any{"state": "paused"}), "상태 변경(paused)")
	ev1 := nextEvent(t, sub)

	// blocked 는 why 가 필수다(스키마 CHECK).
	e.okBody(t, e.write(http.MethodPatch, "/api/v1/sessions/"+sess,
		map[string]any{"state": "blocked", "why": "관문이 안 뚫린다"}), "상태 변경(blocked)")
	ev2 := nextEvent(t, sub)

	pairAxis(t, "state", ev1, ev2, "paused", "blocked")
}

// TestSessionRekeyEventCarriesTheNewCCSessionID 는 session.rekey 가 **새 cc** 를 나르는지 본다.
//
// 상수면 /clear 로 갈린 대화가 어느 cc 로 옮겨갔는지 아무도 모른다 — 이 이벤트 말고는
// 그 사실을 미는 자리가 없다.
func TestSessionRekeyEventCarriesTheNewCCSessionID(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-rekey-0")

	sub := e.srv.hub.Subscribe(testProject)
	defer e.srv.hub.Unsubscribe(sub)

	e.okBody(t, e.write(http.MethodPost, "/api/v1/sessions/"+sess+"/rekey",
		map[string]any{"cc_session_id": "cc-rekey-1"}), "rekey 1")
	ev1 := nextEvent(t, sub)

	e.okBody(t, e.write(http.MethodPost, "/api/v1/sessions/"+sess+"/rekey",
		map[string]any{"cc_session_id": "cc-rekey-2"}), "rekey 2")
	ev2 := nextEvent(t, sub)

	pairAxis(t, "cc_session_id", ev1, ev2, "cc-rekey-1", "cc-rekey-2")
}

// TestSessionSignalEventCarriesKindAndPathCount 는 session.signal 의 두 값을 못박는다.
//
// 신호는 **사실**이지 판정이 아니다 — 종류가 뭉개지면 prompt 하나와 commit 하나가
// 같은 화면이 되고, 그것이 이 저장소가 신호를 넷으로 나눠 쌓는 이유를 지운다.
func TestSessionSignalEventCarriesKindAndPathCount(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-signal")

	sub := e.srv.hub.Subscribe(testProject)
	defer e.srv.hub.Unsubscribe(sub)

	e.okBody(t, e.write(http.MethodPost, "/api/v1/sessions/"+sess+"/signals",
		map[string]any{"kind": "prompt"}), "신호(prompt)")
	ev1 := nextEvent(t, sub)

	paths := []string{filepath.Join(e.repo, "a.go"), filepath.Join(e.repo, "b.go")}
	e.okBody(t, e.write(http.MethodPost, "/api/v1/sessions/"+sess+"/signals",
		map[string]any{"kind": "commit", "paths": paths}), "신호(commit)")
	ev2 := nextEvent(t, sub)

	pairAxis(t, "kind", ev1, ev2, "prompt", "commit")
	pairAxis(t, "count", ev1, ev2, 0, len(paths))
}

// TestSessionWorkspaceEventCarriesIsPrimary 는 session.workspace 의 is_primary 를 못박는다.
func TestSessionWorkspaceEventCarriesIsPrimary(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-ws")

	sub := e.srv.hub.Subscribe(testProject)
	defer e.srv.hub.Unsubscribe(sub)

	e.okBody(t, e.write(http.MethodPost, "/api/v1/sessions/"+sess+"/workspaces", map[string]any{
		"project": testProject, "path": filepath.Join(e.repo, "docs"), "is_primary": true,
	}), "작업 트리 추가(primary)")
	ev1 := nextEvent(t, sub)

	e.okBody(t, e.write(http.MethodPost, "/api/v1/sessions/"+sess+"/workspaces", map[string]any{
		"project": testProject, "path": filepath.Join(e.repo, "code"), "is_primary": false,
	}), "작업 트리 추가(보조)")
	ev2 := nextEvent(t, sub)

	pairAxis(t, "is_primary", ev1, ev2, true, false)
}

// ─────────────────────────────────────────────────────────────────────────────
// 판단 · 발번 · 스냅숏 표면
// ─────────────────────────────────────────────────────────────────────────────

// TestJudgmentNoteEventCarriesKindRecipientsAndBytes 는 judgment.note 의 세 값을 못박는다.
//
//	mode  — 판단 종류. ask 와 decision 은 다른 사건이다(ask 는 남이 읽어야 하는 요청이다).
//	count — 이 판단을 받을 살아 있는 세션 수.
//	bytes — 본문 크기.
func TestJudgmentNoteEventCarriesKindRecipientsAndBytes(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-note-a")

	sub := e.srv.hub.Subscribe(testProject)
	defer e.srv.hub.Unsubscribe(sub)

	body1 := "짧은 근거"
	got1 := e.okBody(t, e.write(http.MethodPost, "/api/v1/judgments", map[string]any{
		"project": testProject, "session_id": sess, "kind": "decision",
		"title": "결정 하나", "body": body1,
	}), "판단(decision)")
	ev1 := nextEvent(t, sub)

	// 살아 있는 세션을 하나 늘린다 — count 축의 짝은 수신자 수가 갈려야 생긴다.
	e.openSession("cc-note-b")
	skipEvent(t, sub, "session.open")

	body2 := strings.Repeat("긴 근거 ", 20)
	got2 := e.okBody(t, e.write(http.MethodPost, "/api/v1/judgments", map[string]any{
		"project": testProject, "session_id": sess, "kind": "ask",
		"title": "요청 하나", "body": body2,
	}), "판단(ask)")
	ev2 := nextEvent(t, sub)

	pairAxis(t, "mode", ev1, ev2, "decision", "ask")
	pairAxis(t, "bytes", ev1, ev2, len(body1), len(body2))
	// 서버가 센 수는 **응답에서** 읽는다. 같은 요청의 두 표면(SSE·REST)이 갈리는 것이
	// 이 단정이 잡으려는 사고다.
	pairAxis(t, "count", ev1, ev2, countOf(t, got1, "recipients"), countOf(t, got2, "recipients"))
}

// TestCounterAllocEventCarriesNameAndValue 는 counter.alloc 의 두 값을 못박는다.
//
// 발번은 되돌릴 수 없다 — 어느 카운터가 몇 번을 내줬는지가 틀리면 그 사실을 되짚을 자리가 없다.
func TestCounterAllocEventCarriesNameAndValue(t *testing.T) {
	e := newEnv(t, nil)
	e.openSession("cc-counter") // 프로젝트 등록

	sub := e.srv.hub.Subscribe(testProject)
	defer e.srv.hub.Unsubscribe(sub)

	alloc := func(name string) map[string]any {
		t.Helper()
		return e.okBody(t, e.write(http.MethodPost, "/api/v1/counters/"+name+"/next",
			map[string]any{"project": testProject}), "발번("+name+")")
	}

	first := alloc("rev-a")
	ev1 := nextEvent(t, sub)
	second := alloc("rev-a") // 같은 카운터 — 값이 갈린다
	ev2 := nextEvent(t, sub)
	other := alloc("rev-b") // 다른 카운터 — 이름이 갈린다
	ev3 := nextEvent(t, sub)

	pairAxis(t, "count", ev1, ev2, first["value"], second["value"])
	pairAxis(t, "mode", ev1, ev3, "rev-a", "rev-b")
	_ = other
}

// TestSnapshotPutEventCarriesMethodAndKey 는 snapshot.put 의 두 값을 못박는다.
//
// method 는 "그 숫자를 어떻게 얻었나"다 — command 와 manual 이 같은 화면이 되면
// 근거 없는 숫자를 걸러 낼 축이 사라진다.
func TestSnapshotPutEventCarriesMethodAndKey(t *testing.T) {
	e := newEnv(t, nil)
	e.openSession("cc-snapshot") // 프로젝트 등록

	sub := e.srv.hub.Subscribe(testProject)
	defer e.srv.hub.Unsubscribe(sub)

	e.okBody(t, e.write(http.MethodPut, "/api/v1/snapshots/queue.open", map[string]any{
		"project": testProject, "value": "9", "method": "command",
		"evidence": "fd status 의 큐 절", "input_digest": "d1",
	}), "스냅숏(command)")
	ev1 := nextEvent(t, sub)

	e.okBody(t, e.write(http.MethodPut, "/api/v1/snapshots/lane.turns", map[string]any{
		"project": testProject, "value": "3", "method": "manual",
		"evidence": "원장을 손으로 셌다", "input_digest": "d2",
	}), "스냅숏(manual)")
	ev2 := nextEvent(t, sub)

	pairAxis(t, "mode", ev1, ev2, "command", "manual")
	pairAxis(t, "item", ev1, ev2, "queue.open", "lane.turns")
}

// ─────────────────────────────────────────────────────────────────────────────
// 큐 표면
// ─────────────────────────────────────────────────────────────────────────────

// addItem 은 항목 하나를 등록한다.
func (e *env) addItem(t *testing.T, id, sess string, extra map[string]any) map[string]any {
	t.Helper()
	req := map[string]any{
		"project": testProject, "session_id": sess, "id": id,
		"title": id + " 제목", "body": id + " 본문",
	}
	for k, v := range extra {
		req[k] = v
	}
	return e.okBody(t, e.write(http.MethodPost, "/api/v1/items", req), "항목 등록("+id+")")
}

// TestItemAddEventNamesTheItem 는 item.add 가 **어느 항목이 생겼는지**를 나르는지 본다.
//
// 항목이 지목한 바로 그 자리다 — it.ID 를 상수로 바꿔도 전 스위트가 초록이었다(실측 2026-08-09).
func TestItemAddEventNamesTheItem(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-add")

	sub := e.srv.hub.Subscribe(testProject)
	defer e.srv.hub.Unsubscribe(sub)

	e.addItem(t, "add-first", sess, nil)
	ev1 := nextEvent(t, sub)
	e.addItem(t, "add-second", sess, nil)
	ev2 := nextEvent(t, sub)

	pairAxis(t, "item", ev1, ev2, "add-first", "add-second")
}

// TestClaimReclaimEventCarriesItemAndActor 는 claim.reclaim 의 두 값을 못박는다.
//
// 회수는 남의 선점을 끊는 쓰기다. **어느 선점을 누가 끊었나**가 상수면 원장의 판단 행과
// 화면이 갈리고, 그 둘이 갈린 것을 아무도 못 본다.
func TestClaimReclaimEventCarriesItemAndActor(t *testing.T) {
	e := newEnv(t, nil)
	holder := e.openSession("cc-held")
	e.addItem(t, "rc-one", holder, nil)
	e.addItem(t, "rc-two", holder, nil)
	for _, id := range []string{"rc-one", "rc-two"} {
		e.okBody(t, e.write(http.MethodPost, "/api/v1/items/"+id+"/claim",
			map[string]any{"project": testProject, "session_id": holder}), "선점("+id+")")
	}

	sub := e.srv.hub.Subscribe(testProject)
	defer e.srv.hub.Unsubscribe(sub)

	e.okBody(t, e.write(http.MethodPost, "/api/v1/items/rc-one/claim/release", map[string]any{
		"project": testProject, "actor": "사람-가", "reason": "신호가 3일 없다",
	}), "선점 회수(rc-one)")
	ev1 := nextEvent(t, sub)

	e.okBody(t, e.write(http.MethodPost, "/api/v1/items/rc-two/claim/release", map[string]any{
		"project": testProject, "actor": "사람-나", "reason": "워크트리가 지워졌다",
	}), "선점 회수(rc-two)")
	ev2 := nextEvent(t, sub)

	pairAxis(t, "item", ev1, ev2, "rc-one", "rc-two")
	pairAxis(t, "actor", ev1, ev2, "사람-가", "사람-나")
}

// TestItemFinishEventCarriesItemModeFollowupsAndReleased 는 item.finish 의 네 값을 못박는다.
//
//	item     — 어느 항목이 닫혔나.
//	mode     — done 과 dropped 는 다른 사건이다(폐기는 사유가 필수다).
//	count    — 이 마무리가 **실제로 만든** 후속 수. 요청한 수가 아니다.
//	released — 함께 반납한 자원 수. 레인을 쥔 채 마무리한 것과 아닌 것이 갈린다.
func TestItemFinishEventCarriesItemModeFollowupsAndReleased(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-finish")
	e.addItem(t, "fin-one", sess, nil)
	e.addItem(t, "fin-two", sess, nil)
	for _, id := range []string{"fin-one", "fin-two"} {
		e.okBody(t, e.write(http.MethodPost, "/api/v1/items/"+id+"/claim",
			map[string]any{"project": testProject, "session_id": sess}), "선점("+id+")")
	}
	// 레인을 쥐고 마무리한다 — released 축의 짝은 반납한 자원 수가 갈려야 생긴다.
	e.okBody(t, e.write(http.MethodPost, "/api/v1/landing", map[string]any{
		"project": testProject, "session_id": sess, "mode": LandModeAcquire,
	}), "레인 취득")

	sub := e.srv.hub.Subscribe(testProject)
	defer e.srv.hub.Unsubscribe(sub)

	got1 := e.okBody(t, e.write(http.MethodPost, "/api/v1/items/fin-one/finish", map[string]any{
		"project": testProject, "session_id": sess, "outcome": "done",
		"title": "하나를 닫았다", "body": "landed: 표면 하나. 기각한 것은 라우트를 늘리는 안이다",
		"followups": []map[string]any{
			{"id": "fin-follow-a", "title": "후속 가", "body": "여기서 이어서 한다"},
			{"id": "fin-follow-b", "title": "후속 나", "body": "이것도 이어서 한다"},
		},
	}), "마무리(done)")
	ev1 := nextEvent(t, sub)

	got2 := e.okBody(t, e.write(http.MethodPost, "/api/v1/items/fin-two/finish", map[string]any{
		"project": testProject, "session_id": sess, "outcome": "dropped",
		"title": "둘째는 폐기했다", "body": "not-done: 전제가 틀렸다",
		"close_reason": "전제가 거짓으로 밝혀졌다",
	}), "마무리(dropped)")
	ev2 := nextEvent(t, sub)

	pairAxis(t, "item", ev1, ev2, "fin-one", "fin-two")
	pairAxis(t, "mode", ev1, ev2, "done", "dropped")
	pairAxis(t, "count", ev1, ev2, countOf(t, got1, "followups"), countOf(t, got2, "followups"))
	pairAxis(t, "released", ev1, ev2, countOf(t, got1, "released"), countOf(t, got2, "released"))
}

// TestItemMoveEventCarriesItemFromToAndCrossRefs 는 item.move 의 네 값을 못박는다.
//
// 이동은 원장에 남기려고 발행하는 사건이다("왜 이 항목이 여기 있나"에 답할 자리).
// 좌표 셋 중 하나라도 상수면 그 답이 거짓이 된다.
func TestItemMoveEventCarriesItemFromToAndCrossRefs(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-move")
	// 이동 대상 프로젝트를 먼저 등록한다 — 없는 좌표로는 못 옮긴다.
	for _, p := range []string{"px", "py", "pz"} {
		e.openIn(t, p, "cc-move-"+p)
	}
	e.addItem(t, "mv-one", sess, nil)
	e.addItem(t, "mv-two", sess, nil)
	// mv-one 을 선행으로 건 항목 — 옮기면 옛 프로젝트에 교차 참조가 남는다.
	e.addItem(t, "mv-waiter", sess, map[string]any{
		"after": []map[string]any{{"item": "mv-one"}},
	})

	// ★ 전 프로젝트를 구독한다. 셋째 이동은 **출발 프로젝트가 py** 라, cp 만 구독하면
	// 허브가 그 이벤트를 걸러 낸다 — publish 의 project 인자는 라우팅에 쓰이는 값이다.
	sub := e.srv.hub.Subscribe("")
	defer e.srv.hub.Unsubscribe(sub)

	got1 := e.okBody(t, e.write(http.MethodPost, "/api/v1/items/mv-one/move", map[string]any{
		"project": testProject, "session_id": sess, "to": "px",
	}), "이동(mv-one)")
	ev1 := nextEvent(t, sub)

	got2 := e.okBody(t, e.write(http.MethodPost, "/api/v1/items/mv-two/move", map[string]any{
		"project": testProject, "session_id": sess, "to": "py",
	}), "이동(mv-two)")
	ev2 := nextEvent(t, sub)

	// 셋째 이동은 **from 축의 짝**이다 — 앞의 둘은 출발지가 같아 from 이 안 갈린다.
	e.okBody(t, e.write(http.MethodPost, "/api/v1/items/mv-two/move", map[string]any{
		"project": "py", "session_id": sess, "to": "pz",
	}), "이동(mv-two 재이동)")
	ev3 := nextEvent(t, sub)

	pairAxis(t, "item", ev1, ev2, "mv-one", "mv-two")
	pairAxis(t, "to", ev1, ev2, "px", "py")
	pairAxis(t, "count", ev1, ev2, intOf(t, got1, "cross_refs"), intOf(t, got2, "cross_refs"))
	pairAxis(t, "from", ev2, ev3, testProject, "py")
}

// intOf 는 응답 본문의 수 하나다(JSON 왕복 뒤 float64).
func intOf(t *testing.T, body map[string]any, key string) int {
	t.Helper()
	n, ok := body[key].(float64)
	if !ok {
		t.Fatalf("응답에 %s 가 수로 없다: %#v", key, body[key])
	}
	return int(n)
}

// TestAfterCutEventCarriesItemAndRemainingCount 는 item.after.cut 의 두 값을 못박는다.
//
// 이 쓰기는 **무엇이 걸려 있었는지를 지우는** 유일한 동사다. 보고 있는 세션이 자기 항목의
// 전제가 방금 바뀐 것을 즉시 알아야 하는데, 남은 선행 수가 상수면 그 화면이 거짓이 된다.
func TestAfterCutEventCarriesItemAndRemainingCount(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-cut")
	e.addItem(t, "cut-two-deps", sess, map[string]any{
		"after": []map[string]any{{"item": "dep-a"}, {"item": "dep-b"}},
	})
	e.addItem(t, "cut-one-dep", sess, map[string]any{
		"after": []map[string]any{{"item": "dep-c"}},
	})

	sub := e.srv.hub.Subscribe(testProject)
	defer e.srv.hub.Unsubscribe(sub)

	e.okBody(t, e.write(http.MethodPost, "/api/v1/items/cut-two-deps/after/cut", map[string]any{
		"project": testProject, "session_id": sess, "dep": map[string]any{"item": "dep-a"},
	}), "선행 절단(둘 중 하나)")
	ev1 := nextEvent(t, sub)

	e.okBody(t, e.write(http.MethodPost, "/api/v1/items/cut-one-dep/after/cut", map[string]any{
		"project": testProject, "session_id": sess, "dep": map[string]any{"item": "dep-c"},
	}), "선행 절단(하나 중 하나)")
	ev2 := nextEvent(t, sub)

	pairAxis(t, "item", ev1, ev2, "cut-two-deps", "cut-one-dep")
	pairAxis(t, "count", ev1, ev2, 1, 0) // 남은 선행 수
}

// ─────────────────────────────────────────────────────────────────────────────
// 랜딩 레인 표면
// ─────────────────────────────────────────────────────────────────────────────

// TestLaneEventCarriesModeRowStateAndPosition 는 lane.<mode> 의 네 값을 못박는다.
//
//	kind  — acquire·report·leave 는 **다른 사건**이다. 한 이름으로 접히면 줄에 선 순간과
//	        반납한 순간이 같은 화면이 된다(claim 의 mode 축과 정확히 같은 부류).
//	row   — 어느 줄 행인가. 회수 표면이 이 번호를 대상으로 받는다.
//	state — turn 과 waiting 이 갈려야 "지금 내 차례인가"가 답해진다.
//	count — 줄에서 몇 번째인가.
func TestLaneEventCarriesModeRowStateAndPosition(t *testing.T) {
	e := newEnv(t, nil)
	first := e.openSession("cc-lane-a")
	second := e.openSession("cc-lane-b")

	sub := e.srv.hub.Subscribe(testProject)
	defer e.srv.hub.Unsubscribe(sub)

	land := func(sess, mode string, extra map[string]any) map[string]any {
		t.Helper()
		req := map[string]any{"project": testProject, "session_id": sess, "mode": mode}
		for k, v := range extra {
			req[k] = v
		}
		return e.okBody(t, e.write(http.MethodPost, "/api/v1/landing", req), "랜딩("+mode+")")
	}

	got1 := land(first, LandModeAcquire, nil) // 맨 앞 — turn
	ev1 := nextEvent(t, sub)
	got2 := land(second, LandModeAcquire, nil) // 뒤 — waiting
	ev2 := nextEvent(t, sub)

	pairAxis(t, "row", ev1, ev2, intOf(t, got1, "row_id"), intOf(t, got2, "row_id"))
	pairAxis(t, "state", ev1, ev2, got1["state"], got2["state"])
	pairAxis(t, "count", ev1, ev2, intOf(t, got1, "position"), intOf(t, got2, "position"))

	// 반납은 다른 사건이다 — kind 축의 짝.
	land(first, LandModeReport, map[string]any{"kind": "ok"})
	ev3 := nextEvent(t, sub)
	pairKind(t, ev1, ev3, "lane.acquire", "lane.report")
}

// TestLaneReleaseEventCarriesRowHeldAndActor 는 lane.release 의 세 값을 못박는다.
//
//	row          — 어느 줄 행을 끊었나.
//	held_release — 점유까지 풀렸나. 대기 중 행 회수와 점유 중 행 회수는 **다른 결과**다.
//	actor        — 누가 끊었나.
func TestLaneReleaseEventCarriesRowHeldAndActor(t *testing.T) {
	e := newEnv(t, nil)
	holder := e.openSession("cc-rel-a")
	waiter := e.openSession("cc-rel-b")

	land := func(sess string) map[string]any {
		t.Helper()
		return e.okBody(t, e.write(http.MethodPost, "/api/v1/landing", map[string]any{
			"project": testProject, "session_id": sess, "mode": LandModeAcquire,
		}), "랜딩(acquire)")
	}
	held := land(holder) // 맨 앞 — 레인을 쥔다
	wait := land(waiter) // 뒤 — 대기만 한다

	sub := e.srv.hub.Subscribe(testProject)
	defer e.srv.hub.Unsubscribe(sub)

	release := func(body map[string]any, rowID int, actor string) {
		t.Helper()
		e.okBody(t, e.write(http.MethodPost,
			"/api/v1/landing/rows/"+itoa(rowID)+"/release", map[string]any{
				"project": testProject, "actor": actor, "reason": "신호가 없다",
			}), "줄 행 회수")
		_ = body
	}

	release(held, intOf(t, held, "row_id"), "사람-가")
	ev1 := nextEvent(t, sub)
	release(wait, intOf(t, wait, "row_id"), "사람-나")
	ev2 := nextEvent(t, sub)

	pairAxis(t, "row", ev1, ev2, intOf(t, held, "row_id"), intOf(t, wait, "row_id"))
	pairAxis(t, "held_release", ev1, ev2, true, false) // 점유 회수 / 대기 행 회수
	pairAxis(t, "actor", ev1, ev2, "사람-가", "사람-나")
}
