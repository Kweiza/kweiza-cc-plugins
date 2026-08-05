package api

import (
	"net/http"
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

	claim := e.write(http.MethodPost, "/api/v1/items/solo-item/claim", map[string]any{
		"project": testProject, "session_id": sess,
	})
	if claim.Code != http.StatusOK {
		t.Fatalf("단독 선점 실패: %d %s", claim.Code, claim.Body.String())
	}
	cb := decodeBody(t, claim)
	if cb["mode"] != "claimed" {
		t.Fatalf("mode 가 %v 다: %s", cb["mode"], claim.Body.String())
	}
	if _, has := cb["bundle"]; has {
		t.Fatalf("item_ids 없이 불렀는데 bundle 절이 실렸다: %s", claim.Body.String())
	}

	cl, err := e.st.GetClaim(t.Context(), testProject, "solo-item")
	if err != nil || cl.ReleasedAt != nil {
		t.Fatalf("선점 행이 없다: %+v %v", cl, err)
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

	for _, id := range []string{"path-item", "other-lead", "mismatch-m1"} {
		if _, err := e.st.GetClaim(t.Context(), testProject, id); err == nil {
			t.Fatalf("거절됐는데 항목 %s 에 선점 행이 생겼다", id)
		}
	}
}
