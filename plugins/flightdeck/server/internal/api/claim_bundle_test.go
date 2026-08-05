package api

import (
	"net/http"
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
