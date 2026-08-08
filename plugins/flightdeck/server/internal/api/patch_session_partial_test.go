package api

import (
	"net/http"
	"testing"
)

// handlePatchSession 의 되읽기 실패 갈래는 **결정론적으로 못 밟는다** — 그 사실을 적어 둔다.
//
// ★ 왜 못 밟나. Service.SetState 가 트랜잭션 **안에서** 같은 t.GetSession 을 먼저 부른다
// (이벤트에 실을 project 를 얻으려고). 그래서 세션 행을 못 읽게 만들면 — 표를 숨기든
// opened_at 을 오염시키든 — 쓰기가 **먼저** 죽고, 그것은 이 부류가 아니라 정상적인 거절이다
// (아무것도 안 썼으므로 오류를 올리는 것이 옳다).
//
// ★ 그 사실이 이 자리의 위험도를 말해 준다: 핸들러의 되읽기가 실패하려면 같은 요청 안에서
// SetState 의 조회가 성공한 **뒤에** DB 가 깨져야 한다. 그래도 D 를 남기지 않는 이유는
// 비용이 0이기 때문이다 — 아는 사실로 200 을 내는 데 드는 것이 몇 줄이고, D 로 두면
// DESIGN §5 의 규칙에 이 파일만 예외가 된다(그리고 바로 아래 handleSignal 이 같은 조회를
// 이미 무르게 한다 — 한 파일 안의 정반대 정책이 이 항목이 고발한 모양이다).
//
// 아래 시험이 잠그는 것은 **정상 경로**다: partial 이 상시 점등이 아니고, 전체 행이 온다.
func TestPatchSessionFullResponseOnHappyPath(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")

	w := e.write(http.MethodPatch, "/api/v1/sessions/"+sess, map[string]any{
		"state": "blocked", "why": "계약 개정을 기다린다",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("상태 전이가 %d 다: %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["partial"] != nil {
		t.Fatalf("정상 경로인데 partial 이 켜져 있다 — 상시 점등은 판별력이 0이다:\n%s",
			w.Body.String())
	}
	got, ok := body["session"].(map[string]any)
	if !ok {
		t.Fatalf("session 절이 없다:\n%s", w.Body.String())
	}
	// 전체 행이 왔다는 증거 — 요청에 없던 필드가 채워져 있다.
	if got["Project"] == nil && got["project"] == nil {
		t.Fatalf("정상 경로인데 세션 행이 요청으로 아는 두 값뿐이다: %+v", got)
	}
}
