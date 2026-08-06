package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// 계약 시험 — POST /items/{id}/claim/release 가 사람의 선점 회수를 나른다.
//
// 로직의 정본은 service.ReclaimClaim 이고 web 대시보드와 같은 함수다. 여기서 보는 것은
// 표면 계약이다: 사유 없는 회수의 거절, 결과의 좌표(항목·점유자·판단), 액세스 로그의
// 세션 축(요청 본문에 세션이 없어서 이 결선이 아니면 "누구의 선점이 끊겼나"가 로그에
// 안 남는다), 그리고 죽은 선점 재회수의 404.
func TestClaimReleaseEndpointReclaimsWithLedgerAndLog(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-cr1")

	add := e.write(http.MethodPost, "/api/v1/items", map[string]any{
		"project": testProject, "id": "cr-1", "title": "회수 대상", "body": "b",
	})
	if add.Code != http.StatusCreated {
		t.Fatalf("항목 등록 실패: %d %s", add.Code, add.Body.String())
	}
	claim := e.write(http.MethodPost, "/api/v1/items/cr-1/claim", map[string]any{
		"project": testProject, "session_id": sess,
	})
	if claim.Code != http.StatusOK {
		t.Fatalf("선점 실패: %d %s", claim.Code, claim.Body.String())
	}

	// ① 사유 없는 회수는 거절 — 처방까지 와야 한다.
	refuse := e.write(http.MethodPost, "/api/v1/items/cr-1/claim/release", map[string]any{
		"project": testProject, "actor": "운영자",
	})
	if refuse.Code != http.StatusBadRequest {
		t.Fatalf("사유 없는 회수가 %d 다(기대 400): %s", refuse.Code, refuse.Body.String())
	}
	if g, _ := errorOf(t, refuse)["guidance"].(string); !strings.Contains(g, "사유") {
		t.Fatalf("거절에 처방이 없다: %q", g)
	}

	// ② 회수 — 결과가 좌표 셋(항목·점유자·판단)을 다 낸다.
	rel := e.write(http.MethodPost, "/api/v1/items/cr-1/claim/release", map[string]any{
		"project": testProject, "actor": "운영자", "reason": "무신호 20시간 — 보드 근거로 회수",
	}, withHeader("X-Request-Id", "cr-rel-1"))
	if rel.Code != http.StatusOK {
		t.Fatalf("회수 실패: %d %s", rel.Code, rel.Body.String())
	}
	rb := decodeBody(t, rel)
	if rb["item"] != "cr-1" || rb["holder"] != sess {
		t.Fatalf("결과 좌표가 다르다: %v (점유자 기대 %s)", rb, sess)
	}
	jid, _ := rb["judgment_id"].(string)
	if jid == "" {
		t.Fatalf("판단 id 가 안 왔다 — 회수가 기록 없이 지나간 것처럼 보인다: %v", rb)
	}
	// id 가 원장의 실물과 이어져 있다 — "비어 있지 않다"만 보면 가짜 id 가 통과한다
	// (AddJudgment 를 지우고 상수를 넣는 뮤테이션에 이 시험이 초록이었다).
	js, err := e.st.JudgmentsForItem(context.Background(), testProject, "cr-1")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, j := range js {
		if j.ID == jid {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("응답의 judgment_id %s 가 원장에 없다: %+v", jid, js)
	}

	// 액세스 로그 — 요청 본문에 세션이 없으므로, 회수당한 세션이 로그의 세션 축에
	// 실리는 결선은 여기서만 검증된다.
	lines := e.logs.served(t, "cr-rel-1")
	if len(lines) != 1 {
		t.Fatalf("회수 요청의 액세스 로그가 %d줄이다", len(lines))
	}
	if lines[0]["session_id"] != sess {
		t.Fatalf("액세스 로그의 세션 좌표가 회수당한 세션이 아니다: %v", lines[0]["session_id"])
	}
	if lines[0]["route"] != "POST /api/v1/items/{id}/claim/release" {
		t.Fatalf("라우트가 패턴이 아니다(카디널리티가 터진다): %v", lines[0]["route"])
	}

	// ③ 이미 풀린 선점의 재회수는 404 — 없는 대상과 서버 결함은 처방이 다르다.
	again := e.write(http.MethodPost, "/api/v1/items/cr-1/claim/release", map[string]any{
		"project": testProject, "actor": "운영자", "reason": "둘째 회수",
	})
	if again.Code != http.StatusNotFound {
		t.Fatalf("재회수가 %d 다(기대 404): %s", again.Code, again.Body.String())
	}
}
