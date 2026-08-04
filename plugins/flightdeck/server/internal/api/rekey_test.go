package api

import (
	"context"
	"net/http"
	"testing"
)

// rekey 표면 — 훅이 /clear·compact 로 갈린 대화의 새 cc 를 카드에 반영하는 경로다.
//
// ★ 소비자 좌표계로만 단정한다: HTTP 상태코드·응답 본문, 그리고 저장소를 직접 쳐서
// "정말 옮겨졌는가"를 본다(렌더된 문자열이 아니라).

func TestRekeyMovesTheCardAndReturnsIt(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-old")

	res := e.write(http.MethodPost, "/api/v1/sessions/"+sess+"/rekey",
		map[string]any{"cc_session_id": "cc-new"})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", res.Code, res.Body.String())
	}
	body := decodeBody(t, res)
	if got, _ := body["CCSessionID"].(string); got != "cc-new" {
		t.Errorf("응답의 cc = %q, want %q — %s", got, "cc-new", res.Body.String())
	}

	// ★ 저장소를 직접 쳐서 단정한다 — 응답이 옳아도 실제로 안 옮겨졌을 수 있다.
	again, err := e.st.GetSession(context.Background(), sess)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if again.CCSessionID != "cc-new" {
		t.Fatalf("저장소의 cc = %q, want %q", again.CCSessionID, "cc-new")
	}
}

// ★ 같은 env 안의 openSession 호출은 machine_id·worktree 를 공유한다(helper_test.go 의
// openSession 이 e.repo 를 worktree 로 고정한다) — 그래서 cc 만 다른 두 세션을 열면
// UNIQUE(machine_id, worktree, cc_session_id) 충돌이 그대로 재현된다.
func TestRekeyToATakenCCIs409(t *testing.T) {
	e := newEnv(t, nil)
	a := e.openSession("cc-a")
	_ = e.openSession("cc-b")

	res := e.write(http.MethodPost, "/api/v1/sessions/"+a+"/rekey",
		map[string]any{"cc_session_id": "cc-b"})
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 — 훅이 폴백 사유를 구분 못 한다\n%s", res.Code, res.Body.String())
	}
}

func TestRekeyOnAMissingCardIs404(t *testing.T) {
	e := newEnv(t, nil)
	res := e.write(http.MethodPost, "/api/v1/sessions/no-such/rekey",
		map[string]any{"cc_session_id": "cc-new"})
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404\n%s", res.Code, res.Body.String())
	}
	if got := errorOf(t, res)["code"]; got != "not_found" {
		t.Errorf("code = %v, want not_found", got)
	}
}

// ★ 빈 cc 는 스토어 계층에서 fmt.Errorf 로 나온다(store.ConflictError·NotFoundError 가
// 아니다) — ClassifyError 의 화이트리스트에 안 걸리면 폴백은 500 이다. 훅에게는
// 4xx(재시도해도 안 바뀜)와 500(서버 결함, 재시도 대상)이 다른 이야기이므로 이 시험이 막는다.
func TestRekeyWithAnEmptyCCIs4xx(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-old")
	res := e.write(http.MethodPost, "/api/v1/sessions/"+sess+"/rekey",
		map[string]any{"cc_session_id": ""})
	if res.Code < 400 || res.Code >= 500 {
		t.Fatalf("status = %d, want 4xx\n%s", res.Code, res.Body.String())
	}
}
