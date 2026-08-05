package api

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// 계약 시험 — POST /items/{id}/claim 이 본문의 item_ids 를 실제로 읽는가.
//
// 태스크 9 까지는 이 도달이 없었다: 묶음 선점이 internal/service 안에서만 완성돼 있고,
// claimRequest 에 담을 자리가 없어 handleClaimItem 이 경로 id 만 읽었다. 그 상태에서도
// mcpsrv 의 시험은 초록이었다 — 그쪽 하네스가 service 를 직접 주입해서다. 여기서는
// 실물 HTTP 요청을 보내고 **서버가 무엇을 갖게 됐는지**(store 의 claim 행)를 본다.

// TestClaimBundleClaimsAllMembers 는 단정 ①이다: item_ids 를 주면 선두를 포함해
// 나열한 전부가 선점된다(claim 행이 개수만큼 생긴다).
func TestClaimBundleClaimsAllMembers(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-bundle-1")

	for _, id := range []string{"bundle-lead", "bundle-m1"} {
		add := e.write(http.MethodPost, "/api/v1/items", map[string]any{
			"project": testProject, "session_id": sess, "id": id,
			"title": id + " 제목", "body": id + " 본문",
		})
		if add.Code != http.StatusCreated {
			t.Fatalf("항목 등록 실패(%s): %d %s", id, add.Code, add.Body.String())
		}
	}

	claim := e.write(http.MethodPost, "/api/v1/items/bundle-lead/claim", map[string]any{
		"project": testProject, "session_id": sess,
		"item_ids": []string{"bundle-lead", "bundle-m1"},
	})
	if claim.Code != http.StatusOK {
		t.Fatalf("묶음 선점 실패: %d %s", claim.Code, claim.Body.String())
	}
	cb := decodeBody(t, claim)
	if _, has := cb["bundle"]; !has {
		t.Fatalf("응답에 bundle 절이 없다 — item_ids 가 서버에 안 닿았다: %s", claim.Body.String())
	}

	for _, id := range []string{"bundle-lead", "bundle-m1"} {
		cl, err := e.st.GetClaim(t.Context(), testProject, id)
		if err != nil {
			t.Fatalf("항목 %s 의 선점 행이 없다: %v", id, err)
		}
		if cl.ReleasedAt != nil {
			t.Fatalf("항목 %s 의 선점 행이 이미 반납 상태다: %+v", id, cl)
		}
		if cl.SessionID != sess {
			t.Fatalf("항목 %s 의 선점 세션이 %q 다(기대 %q)", id, cl.SessionID, sess)
		}
	}
}

// TestClaimSingleUnchangedWhenItemIDsEmpty 는 단정 ②다: item_ids 가 비면 오늘과
// 똑같이 경로 id 단독 선점이다 — 이것이 이 태스크의 유일한 호환성 이야기다.
//
// ★ 리뷰 라운드 2 finding 5 로 **무엇을 단정하는지가 바뀌었다.**
//
// 처음 이 시험은 호환성의 대리물로 "응답 JSON 에 bundle 키가 **없다**"를 썼다.
// 그런데 그 대리물이 곧 결함이었다: 렌더는 bundle 부재를 "이 응답은 그 축을
// 읽지 않았다 — 낡은 캐시이거나 서버가 이 축을 모르는 판이다"로 찍는데,
// 이 응답은 **현행 서버의 신선한 온라인 응답**이다. 즉 이 시험은 관측 안 함과
// 값을 뭉개는 상태를 계약으로 못박고 있었고, 그것은 이 저장소가 다른 모든
// 자리에서 금지하는 실패다(QueueOpen·PathCheck 이 포인터인 이유와 같다).
//
// 그래서 대리물을 **진짜 호환성**으로 바꾼다 — 기존 호출자가 실제로 기대는 것들:
// 같은 mode · 같은 branch · 같은 워크트리 준비 명령 · 같은 claim 행, 그리고
// 이 요청이 item_ids 를 **안 실었다**는 사실. 모르는 필드가 하나 늘어난 것은
// 깨짐이 아니다: Go 디코더는 DisallowUnknownFields 를 켜지 않는 한 모르는 키를
// 무시하고, cmd/fd 는 이 본문을 service.PickResult 로 푸는데 거기엔 그 필드가 있다.
//
// 새 계약을 **양쪽으로** 못박는다: bundle 절은 있어야 하고(축을 읽었다),
// 구성원은 0건이어야 한다(묶지 않았다). 한쪽만 보면 "item_ids 를 안 줬는데
// 남을 묶어 왔다"가 이 시험을 통과한다.
func TestClaimSingleUnchangedWhenItemIDsEmpty(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-bundle-2")

	add := e.write(http.MethodPost, "/api/v1/items", map[string]any{
		"project": testProject, "session_id": sess, "id": "solo-item",
		"title": "단독", "body": "본문",
	})
	if add.Code != http.StatusCreated {
		t.Fatalf("항목 등록 실패: %d %s", add.Code, add.Body.String())
	}

	// ★ 본문에 item_ids 를 **안 싣는다.** 그것이 이 갈래의 정의다.
	body := map[string]any{"project": testProject, "session_id": sess}
	if _, has := body["item_ids"]; has {
		t.Fatal("전제가 깨졌다 — 이 시험은 item_ids 없는 요청이어야 한다")
	}
	claim := e.write(http.MethodPost, "/api/v1/items/solo-item/claim", body)
	if claim.Code != http.StatusOK {
		t.Fatalf("단독 선점 실패: %d %s", claim.Code, claim.Body.String())
	}
	cb := decodeBody(t, claim)

	// ── 호환성 본체: 기존 호출자가 읽는 축이 전부 그대로인가 ──
	if cb["mode"] != "claimed" {
		t.Fatalf("mode 가 %v 다: %s", cb["mode"], claim.Body.String())
	}
	if cb["branch"] != "solo-item" {
		t.Fatalf("branch 가 %v 다 — 항목 id 그대로여야 한다: %s", cb["branch"], claim.Body.String())
	}
	setup, ok := cb["setup"].([]any)
	if !ok || len(setup) != 3 {
		t.Fatalf("워크트리 준비 명령이 3줄이 아니다(%v): %s", cb["setup"], claim.Body.String())
	}
	if first, _ := setup[0].(string); !strings.HasPrefix(first, "cd ") {
		t.Fatalf("준비 명령 첫 줄이 cd 가 아니다: %v", setup[0])
	}
	if item, _ := cb["item"].(map[string]any); item == nil || item["ID"] != "solo-item" {
		t.Fatalf("응답의 항목이 solo-item 이 아니다: %s", claim.Body.String())
	}

	// ── 새 계약: 축을 읽었고(non-nil), 아무도 안 묶었다(구성원 0건) ──
	bundle, has := cb["bundle"].(map[string]any)
	if !has {
		t.Fatalf("bundle 절이 없다 — 부재는 '이 응답은 그 축을 안 읽었다'는 뜻이고, "+
			"현행 서버의 신선한 응답이 그렇게 보이면 안 된다: %s", claim.Body.String())
	}
	if members, _ := bundle["members"].([]any); len(members) != 0 {
		t.Fatalf("item_ids 를 안 줬는데 구성원 %d건을 묶어 왔다: %s", len(members), claim.Body.String())
	}
	// 구성원 0건이 "묶을 게 없다"로 오독되지 않도록 왜 0건인지가 실려야 한다.
	if scope, _ := bundle["scope"].(string); strings.TrimSpace(scope) == "" {
		t.Fatalf("구성원 0건인데 그 0건의 뜻(범위)이 비어 있다: %s", claim.Body.String())
	}

	// ── 좌표계는 원장이다: 선점 행 하나, 그것도 이 항목에만 ──
	cl, err := e.st.GetClaim(t.Context(), testProject, "solo-item")
	if err != nil || cl.ReleasedAt != nil {
		t.Fatalf("선점 행이 없다: %+v %v", cl, err)
	}
	held, err := e.st.ClaimedItems(t.Context(), sess)
	if err != nil || len(held) != 1 || held[0] != "solo-item" {
		t.Fatalf("단독 선점인데 이 세션이 쥔 항목이 %v 다(%v)", held, err)
	}
}

// TestClaimBundleLeadMismatchRefuses 는 단정 ③이다: item_ids[0] 이 경로 id 와 다르면
// 거절하고 **아무것도 안 쓴다**(claim 행 0개). 합치거나 한쪽을 우선하면 "무엇을
// 집었는가"가 모호해지고, 그 사실은 pick 응답이 나르는 것 중 가장 중요한 하나다.
func TestClaimBundleLeadMismatchRefuses(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-bundle-3")

	for _, id := range []string{"path-item", "other-lead", "mismatch-m1"} {
		add := e.write(http.MethodPost, "/api/v1/items", map[string]any{
			"project": testProject, "session_id": sess, "id": id,
			"title": id + " 제목", "body": id + " 본문",
		})
		if add.Code != http.StatusCreated {
			t.Fatalf("항목 등록 실패(%s): %d %s", id, add.Code, add.Body.String())
		}
	}

	// 경로는 path-item 인데 item_ids 의 선두는 other-lead 다 — 어긋난다.
	claim := e.write(http.MethodPost, "/api/v1/items/path-item/claim", map[string]any{
		"project": testProject, "session_id": sess,
		"item_ids": []string{"other-lead", "mismatch-m1"},
	})
	if claim.Code != http.StatusBadRequest {
		t.Fatalf("경로·선두 어긋남이 거절되지 않았다: %d %s", claim.Code, claim.Body.String())
	}
	ce := errorOf(t, claim)
	if ce["code"] != "refused" {
		t.Fatalf("오류 코드가 %v 다(refused 를 기대)", ce["code"])
	}
	// 코드만 보면 안 된다 — `RefusedError{What:"claim"}` 처럼 Reason·Guidance 를
	// 빼먹어도 code 는 여전히 "refused"다. 사유가 **두 id 를 다 담는지**(어느 쪽이
	// 경로고 어느 쪽이 item_ids 선두인지 짐작하지 않게)와, 처방이 **비어 있지
	// 않은지**를 따로 본다. 처방이 없으면 에이전트는 같은 어긋난 호출을 영원히
	// 반복한다 — 그것이 RefusedError.Guidance 가 존재하는 이유다.
	msg, _ := ce["message"].(string)
	if !strings.Contains(msg, "path-item") || !strings.Contains(msg, "other-lead") {
		t.Fatalf("거절 사유에 두 id(경로 path-item·선두 other-lead)가 다 없다: %q", msg)
	}
	guidance, _ := ce["guidance"].(string)
	if strings.TrimSpace(guidance) == "" {
		t.Fatalf("거절에 처방(guidance)이 없다 — 사유만 주면 무엇을 고쳐야 하는지 모른 채 같은 호출을 반복한다: %v", ce)
	}

	for _, id := range []string{"path-item", "other-lead", "mismatch-m1"} {
		if _, err := e.st.GetClaim(t.Context(), testProject, id); err == nil {
			t.Fatalf("거절됐는데 항목 %s 에 선점 행이 생겼다", id)
		}
	}
}

// TestClaimBundleLeadTrimsBeforeCompare 는 단정 ④다: 선두 id 에 공백이 붙어도
// 경로 id 와 **같은 것으로 본다** — service 가 이미 그렇게 보기 때문이다.
//
// ★ 이 시험이 막는 것은 "REST 가 거절한다"가 아니라 **계층 간 비대칭**이다.
//
// service 의 dedupeIDs 는 item_ids 를 TrimSpace 해서 다룬다(pick.go). 그런데
// handleClaimItem 의 경로·선두 비교는 바이트 비교였다. 그래서 같은 요청이
// 어느 문으로 들어오느냐에 따라 답이 갈렸다:
//
//	REST     → 400 refused ("경로의 항목과 item_ids 의 선두가 다르다")
//	service  → 통과. 선두는 트림된 값이고 그게 곧 브랜치가 된다
//
// 둘 다 같은 Pick 을 부르는데 한쪽만 문 앞에서 걷어낸다. 이런 빈 칸은 어느
// diff 에도 안 나타난다 — 두 파일 중 어느 쪽을 봐도 그 자리만 보면 옳다.
//
// 그래서 여기서는 **정규화된 값이 실제로 어디에 붙었는지**까지 본다:
// 응답의 branch·항목 id 가 트림된 값인가(원문 바이트가 브랜치 이름으로
// 새 나가지 않는가), 구성원은 자기 id 를 갖는가(선두가 복사된 게 아닌가),
// 원장에 유령 행(공백 붙은 id)이 생기지 않았는가.
func TestClaimBundleLeadTrimsBeforeCompare(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-bundle-4")

	for _, id := range []string{"trim-lead", "trim-m1"} {
		add := e.write(http.MethodPost, "/api/v1/items", map[string]any{
			"project": testProject, "session_id": sess, "id": id,
			"title": id + " 제목", "body": id + " 본문",
		})
		if add.Code != http.StatusCreated {
			t.Fatalf("항목 등록 실패(%s): %d %s", id, add.Code, add.Body.String())
		}
	}

	// 선두에만 공백을 붙인다 — 경로는 깨끗한 trim-lead 다.
	const rawLead = "  trim-lead\t"
	claim := e.write(http.MethodPost, "/api/v1/items/trim-lead/claim", map[string]any{
		"project": testProject, "session_id": sess,
		"item_ids": []string{rawLead, "trim-m1"},
	})
	if claim.Code != http.StatusOK {
		t.Fatalf("공백만 다른 선두가 거절됐다 — service 는 같은 값으로 보는데 REST 만 걷어낸다: %d %s",
			claim.Code, claim.Body.String())
	}
	cb := decodeBody(t, claim)

	// ── 정규화된 값이 실제로 붙는 자리 ──
	// branch 는 선두 id 그 자체다. 원문이 새면 워크트리 이름에 공백이 들어간다.
	if cb["branch"] != "trim-lead" {
		t.Fatalf("branch 가 %q 다 — 트림된 선두여야 한다: %s", cb["branch"], claim.Body.String())
	}
	if item, _ := cb["item"].(map[string]any); item == nil || item["ID"] != "trim-lead" {
		t.Fatalf("응답의 항목이 trim-lead 가 아니다: %s", claim.Body.String())
	}

	// ── 구성원은 **자기 값**인가 (선두가 복사돼 온 게 아닌가) ──
	bundle, has := cb["bundle"].(map[string]any)
	if !has {
		t.Fatalf("bundle 절이 없다 — item_ids 가 묶음 경로에 안 닿았다: %s", claim.Body.String())
	}
	members, _ := bundle["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("구성원이 1건이 아니다(%d): %s", len(members), claim.Body.String())
	}
	m0, _ := members[0].(map[string]any)
	if mi, _ := m0["item"].(map[string]any); mi == nil || mi["ID"] != "trim-m1" {
		t.Fatalf("구성원의 항목이 trim-m1 이 아니다(선두가 복사됐나): %v", m0["item"])
	}
	if link, _ := m0["link"].(map[string]any); link == nil || link["Item"] != "trim-m1" {
		t.Fatalf("구성원의 link.Item 이 trim-m1 이 아니다: %v", m0["link"])
	}
	if claimed, _ := m0["claimed"].(bool); !claimed {
		t.Fatalf("구성원이 안 집혔다: %s", claim.Body.String())
	}

	// ── 좌표계는 원장이다: 트림된 두 id 만, 공백 붙은 유령 행은 없다 ──
	for _, id := range []string{"trim-lead", "trim-m1"} {
		cl, err := e.st.GetClaim(t.Context(), testProject, id)
		if err != nil {
			t.Fatalf("항목 %s 의 선점 행이 없다: %v", id, err)
		}
		if cl.ReleasedAt != nil || cl.SessionID != sess {
			t.Fatalf("항목 %s 의 선점 행이 이 세션의 살아 있는 선점이 아니다: %+v", id, cl)
		}
	}
	if _, err := e.st.GetClaim(t.Context(), testProject, rawLead); err == nil {
		t.Fatalf("공백 붙은 원문 id(%q)로 선점 행이 생겼다 — 정규화가 비교에만 쓰이고 저장에는 안 쓰였다", rawLead)
	}
	held, err := e.st.ClaimedItems(t.Context(), sess)
	if err != nil {
		t.Fatalf("세션이 쥔 항목을 못 읽었다: %v", err)
	}
	got := append([]string(nil), held...)
	sort.Strings(got)
	if len(got) != 2 || got[0] != "trim-lead" || got[1] != "trim-m1" {
		t.Fatalf("이 세션이 쥔 항목이 %v 다(trim-lead·trim-m1 둘만이어야 한다)", held)
	}
}

// TestClaimBundleLeadMismatchSurvivesTrim 은 단정 ⑤이고, 단정 ④의 **짝**이다:
// 트림하고 나서도 **진짜로 다른** 선두는 여전히 거절된다.
//
// ★ 이 짝이 없으면 단정 ④가 위험하다. 비교를 통째로 지우는 변경(또는 항상
// 참으로 만드는 변경)이 ④를 초록으로 통과시키기 때문이다 — "느슨하게 고쳐서
// 통과시켰다"와 "정규화해서 맞췄다"가 구분되지 않는다. 단정 ③이 같은 축을
// 보지만 거기 id 들에는 공백이 없어, 트림을 도입한 **뒤에** 그 정규화가
// 비교를 무너뜨리지 않았는지는 못 본다.
func TestClaimBundleLeadMismatchSurvivesTrim(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-bundle-5")

	for _, id := range []string{"trim-path", "trim-other", "trim-m2"} {
		add := e.write(http.MethodPost, "/api/v1/items", map[string]any{
			"project": testProject, "session_id": sess, "id": id,
			"title": id + " 제목", "body": id + " 본문",
		})
		if add.Code != http.StatusCreated {
			t.Fatalf("항목 등록 실패(%s): %d %s", id, add.Code, add.Body.String())
		}
	}

	// 공백을 걷어내도 trim-other 는 trim-path 가 아니다.
	claim := e.write(http.MethodPost, "/api/v1/items/trim-path/claim", map[string]any{
		"project": testProject, "session_id": sess,
		"item_ids": []string{"  trim-other  ", "trim-m2"},
	})
	if claim.Code != http.StatusBadRequest {
		t.Fatalf("트림 후에도 다른 선두가 거절되지 않았다 — 비교가 헐거워졌다: %d %s",
			claim.Code, claim.Body.String())
	}
	ce := errorOf(t, claim)
	if ce["code"] != "refused" {
		t.Fatalf("오류 코드가 %v 다(refused 를 기대)", ce["code"])
	}
	// 사유는 **트림된 두 값**을 담아야 한다. 원문 바이트를 그대로 찍으면 화면에서
	// 두 값이 똑같아 보이는데 거절된 꼴이 되고, 그때 세션은 고칠 곳을 못 찾는다.
	msg, _ := ce["message"].(string)
	if !strings.Contains(msg, "(trim-path)") || !strings.Contains(msg, "(trim-other)") {
		t.Fatalf("거절 사유가 트림된 두 id 를 그대로 담지 않았다: %q", msg)
	}
	if guidance, _ := ce["guidance"].(string); strings.TrimSpace(guidance) == "" {
		t.Fatalf("거절에 처방(guidance)이 없다: %v", ce)
	}

	for _, id := range []string{"trim-path", "trim-other", "trim-m2"} {
		if _, err := e.st.GetClaim(t.Context(), testProject, id); err == nil {
			t.Fatalf("거절됐는데 항목 %s 에 선점 행이 생겼다", id)
		}
	}
}

// addItems 는 항목 여럿을 등록한다. 등록 자체가 이 파일의 단정이 아니라 전제라서
// 실패하면 그 자리에서 세운다 — 전제가 깨진 채로 본 단정을 읽으면 거짓 초록이 난다.
func (e *env) addItems(sess string, ids ...string) {
	e.t.Helper()
	for _, id := range ids {
		add := e.write(http.MethodPost, "/api/v1/items", map[string]any{
			"project": testProject, "session_id": sess, "id": id,
			"title": id + " 제목", "body": id + " 본문",
		})
		if add.Code != http.StatusCreated {
			e.t.Fatalf("항목 등록 실패(%s): %d %s", id, add.Code, add.Body.String())
		}
	}
}

// assertLeadMismatch 는 선두 어긋남 거절 하나를 **값이 제 이름표 옆에 붙었는지**까지 본다.
//
// ★ 순서를 보는 이유: 사유는 두 값을 나르는데, 어느 쪽이 경로고 어느 쪽이 item_ids 의
// 선두인지가 그 문장의 **전부**다. 두 값을 뒤바꿔 찍어도 "둘 다 들어 있다"는 단정은
// 초록으로 지나가고(단정 ③·⑤가 그렇다), 그러면 세션은 처방("경로와 item_ids[0] 을
// 같게 맞춰라")을 받아 들고 **멀쩡한 쪽을 고치러 간다** — 사유가 없는 것보다 나쁘다.
//
// 문구 자체는 안 고정한다. 보는 것은 "경로 … <경로 id> … 선두 … <선두 id>" 라는
// 이름표-값 결합뿐이라, 문장을 다듬는 정상 변경은 여기서 붉어지지 않는다.
func assertLeadMismatch(t *testing.T, w *httptest.ResponseRecorder, wantPath, wantLead string) {
	t.Helper()
	if w.Code != http.StatusBadRequest {
		t.Fatalf("선두 어긋남이 거절되지 않았다: %d %s", w.Code, w.Body.String())
	}
	ce := errorOf(t, w)
	if ce["code"] != "refused" {
		t.Fatalf("오류 코드가 %v 다(refused 를 기대)", ce["code"])
	}
	msg, _ := ce["message"].(string)
	order := []string{"경로", wantPath, "선두", wantLead}
	at := -1
	for _, want := range order {
		i := strings.Index(msg, want)
		if i < 0 {
			t.Fatalf("거절 사유에 %q 가 없다: %q", want, msg)
		}
		if i < at {
			t.Fatalf("거절 사유가 값을 제 이름표에 안 붙였다 — %q 가 제자리에 없다(기대 순서 %v): %q",
				want, order, msg)
		}
		at = i
	}
	if guidance, _ := ce["guidance"].(string); strings.TrimSpace(guidance) == "" {
		t.Fatalf("거절에 처방(guidance)이 없다: %v", ce)
	}
}

// assertNoClaims 는 나열한 항목 어디에도 선점 행이 안 생겼음을 본다.
func (e *env) assertNoClaims(ids ...string) {
	e.t.Helper()
	for _, id := range ids {
		if _, err := e.st.GetClaim(e.t.Context(), testProject, id); err == nil {
			e.t.Fatalf("거절됐는데 항목 %s 에 선점 행이 생겼다", id)
		}
	}
}

// TestClaimBundlePathIDTrimsToo 는 단정 ⑥이고, 단정 ④의 **거울**이다: 공백이
// 경로 쪽에 붙어도 같은 것으로 본다.
//
// ★ 이 시험이 없으면 무엇이 깨지나: 트림을 **한쪽에만** 거는 변경(선두만 트림하고
// 경로는 원문 바이트)이 전 스위트를 초록으로 지나간다 — 단정 ④는 경로가 깨끗한
// 요청만 보기 때문이다. 그런데 그 반쪽 트림은 이 브랜치가 메운 계층 비대칭을
// 방향만 뒤집어 그대로 되살린다: service 는 item_ids 를 트림해서 다루므로 이
// 요청을 받아 선두 trim6-lead 를 집는데, REST 만 문 앞에서 400 으로 걷어낸다.
// 게다가 그 거절 사유는 화면에서 두 값이 **똑같아 보이는데** 다르다고 말한다.
//
// 경로에 공백이 실제로 실릴 수 있음을 이 시험 자체가 보인다 — %20 은 라우터가
// 풀어 PathValue 로 그대로 넘긴다(문자열 리터럴로 " trim6-lead" 를 넣는 게 아니라
// 진짜 URL 로 보낸다).
func TestClaimBundlePathIDTrimsToo(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-bundle-6")
	e.addItems(sess, "trim6-lead", "trim6-m1")

	// 경로에만 공백을 붙인다 — item_ids 는 깨끗하다(단정 ④의 정반대).
	claim := e.write(http.MethodPost, "/api/v1/items/%20trim6-lead%20/claim", map[string]any{
		"project": testProject, "session_id": sess,
		"item_ids": []string{"trim6-lead", "trim6-m1"},
	})
	if claim.Code != http.StatusOK {
		t.Fatalf("공백만 다른 경로 id 가 거절됐다 — 트림이 선두 쪽에만 걸렸다: %d %s",
			claim.Code, claim.Body.String())
	}
	cb := decodeBody(t, claim)

	// 브랜치·항목은 트림된 값이다. 원문이 새면 워크트리 디렉토리 이름에 공백이 들어간다.
	if cb["branch"] != "trim6-lead" {
		t.Fatalf("branch 가 %q 다 — 트림된 선두여야 한다: %s", cb["branch"], claim.Body.String())
	}
	if item, _ := cb["item"].(map[string]any); item == nil || item["ID"] != "trim6-lead" {
		t.Fatalf("응답의 항목이 trim6-lead 가 아니다: %s", claim.Body.String())
	}
	bundle, has := cb["bundle"].(map[string]any)
	if !has {
		t.Fatalf("bundle 절이 없다 — item_ids 가 묶음 경로에 안 닿았다: %s", claim.Body.String())
	}
	members, _ := bundle["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("구성원이 1건이 아니다(%d): %s", len(members), claim.Body.String())
	}
	m0, _ := members[0].(map[string]any)
	if mi, _ := m0["item"].(map[string]any); mi == nil || mi["ID"] != "trim6-m1" {
		t.Fatalf("구성원의 항목이 trim6-m1 이 아니다: %v", m0["item"])
	}

	// 좌표계는 원장이다: 트림된 두 id 만, 공백 붙은 유령 행은 없다.
	held, err := e.st.ClaimedItems(t.Context(), sess)
	if err != nil {
		t.Fatalf("세션이 쥔 항목을 못 읽었다: %v", err)
	}
	got := append([]string(nil), held...)
	sort.Strings(got)
	if len(got) != 2 || got[0] != "trim6-lead" || got[1] != "trim6-m1" {
		t.Fatalf("이 세션이 쥔 항목이 %v 다(trim6-lead·trim6-m1 둘만이어야 한다)", held)
	}
	e.assertNoClaims(" trim6-lead ")
}

// TestClaimBundleLeadIsCaseSensitive 는 단정 ⑦이다: 대소문자만 다른 선두는
// **다른 항목**이므로 거절된다.
//
// ★ 이 시험이 없으면 무엇이 깨지나: 비교를 strings.EqualFold 로 넓히는 변경이
// 전 스위트를 초록으로 지나간다. 그런데 항목 id 는 대소문자를 구분하는 값이고
// (여기서 case-item 과 CASE-ITEM 이 **둘 다 등록된다** — 원장이 그렇게 본다는 뜻이다),
// 그 id 가 곧 브랜치 이름·워크트리 디렉토리 이름이다. 느슨해지면 URL 은 case-item
// 을 가리키는데 서버는 CASE-ITEM 을 집어 그 이름으로 브랜치를 파라고 답한다 —
// "무엇을 집었는가"가 어긋나는 바로 그 실패이고, 이 문지기가 존재하는 이유다.
//
// 그래서 상태코드만 보지 않고 **실재하는 CASE-ITEM 에 선점 행이 안 생겼는지**까지 본다.
// 넓힌 비교의 피해는 400 이 200 이 되는 것이 아니라 남의 항목을 집는 것이다.
func TestClaimBundleLeadIsCaseSensitive(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-bundle-7")
	e.addItems(sess, "case-item", "CASE-ITEM", "case-m1")

	claim := e.write(http.MethodPost, "/api/v1/items/case-item/claim", map[string]any{
		"project": testProject, "session_id": sess,
		"item_ids": []string{"CASE-ITEM", "case-m1"},
	})
	assertLeadMismatch(t, claim, "case-item", "CASE-ITEM")
	e.assertNoClaims("case-item", "CASE-ITEM", "case-m1")
}

// TestClaimBundleGuardsSingleElementItemIDs 는 단정 ⑧이다: item_ids 의 원소가
// **하나뿐이어도** 선두 문지기가 산다.
//
// ★ 이 시험이 없으면 무엇이 깨지나: 문지기 조건을 len(...) > 1 로 좁히는 변경이
// 전 스위트를 초록으로 지나간다 — 단정 ③·⑤·⑦이 전부 원소 2건짜리 요청이기 때문이다.
// 좁혀지면 원소 하나짜리 어긋난 묶음이 통째로 문을 지나 **URL 이 가리키지 않은
// 항목을 집는다**: 브랜치는 solo8-other 가 되는데 요청 URL·이벤트 라벨·멱등 키는
// 여전히 solo8-path 를 말한다. 원소 하나짜리 item_ids 는 fd 가 실제로 보내는
// 모양이다(묶을 이웃이 없는 지정 선점).
//
// 짝을 함께 둔다: **맞는** 선두 하나짜리는 그대로 통과해야 한다. 어긋난 쪽만
// 보면 "원소 하나면 전부 거절"이라는 반대편 고장이 이 시험을 통과한다.
func TestClaimBundleGuardsSingleElementItemIDs(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-bundle-8")
	e.addItems(sess, "solo8-path", "solo8-other")

	// ── 어긋난 하나: 걷어낸다 ──
	bad := e.write(http.MethodPost, "/api/v1/items/solo8-path/claim", map[string]any{
		"project": testProject, "session_id": sess,
		"item_ids": []string{"solo8-other"},
	})
	assertLeadMismatch(t, bad, "solo8-path", "solo8-other")
	e.assertNoClaims("solo8-path", "solo8-other")

	// ── 맞는 하나: 통과하고, 자기 하나만 집는다 ──
	ok := e.write(http.MethodPost, "/api/v1/items/solo8-path/claim", map[string]any{
		"project": testProject, "session_id": sess,
		"item_ids": []string{"solo8-path"},
	})
	if ok.Code != http.StatusOK {
		t.Fatalf("맞는 선두 하나짜리 묶음이 거절됐다: %d %s", ok.Code, ok.Body.String())
	}
	cb := decodeBody(t, ok)
	if cb["branch"] != "solo8-path" {
		t.Fatalf("branch 가 %v 다: %s", cb["branch"], ok.Body.String())
	}
	if bundle, has := cb["bundle"].(map[string]any); !has {
		t.Fatalf("bundle 절이 없다: %s", ok.Body.String())
	} else if members, _ := bundle["members"].([]any); len(members) != 0 {
		t.Fatalf("혼자 지정했는데 구성원 %d건을 묶어 왔다: %s", len(members), ok.Body.String())
	}
	held, err := e.st.ClaimedItems(t.Context(), sess)
	if err != nil || len(held) != 1 || held[0] != "solo8-path" {
		t.Fatalf("이 세션이 쥔 항목이 %v 다(solo8-path 하나여야 한다): %v", held, err)
	}
}
