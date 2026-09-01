package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// 전선 계약 — **판이 섞여 돌 때** 세션 열기가 안 깨지는가.
//
// 이 함대는 항상 섞여 있다(플러그인 판올림이 머신마다 어긋난다). 013 이 요청 본문에
// harness 를 더했으므로 두 방향을 재야 한다:
//
//	① 옛 클라이언트 → 새 서버 : 필드가 **없다**. 빈 값이어야 하고 claude 로 접히면 안 된다.
//	② 새 클라이언트 → 옛 서버 : 필드를 **모른다**. 무시돼야 하고 400 이 되면 안 된다.
//
// ★ ②는 옛 서버를 이 시험에서 띄울 수 없으므로 **대리 측정**이다: 같은 디코더가
// 「모르는 필드」를 어떻게 다루는지를 미래 필드로 잰다. 옛 서버의 디코더도 이
// 코드베이스의 같은 자리(respond.go 의 s.decode)에서 왔으므로 대리가 성립한다.
// 이 대리가 깨지는 유일한 경우는 그 사이 디코더가 DisallowUnknownFields 로 바뀌는
// 것인데, 아래 ② 시험이 정확히 그 변경을 빨갛게 만든다.

// okOpen 은 세션 열기 성공 코드다 — 신규는 201, 재개는 200 이다.
// 그 둘을 가르는 것은 이 시험의 축이 아니므로 함께 받는다.
func okOpen(code int) bool { return code == 200 || code == 201 }

// sessionHarnessOf 는 세션 열기 응답에서 하네스를 꺼낸다.
func sessionHarnessOf(t *testing.T, raw string) string {
	t.Helper()
	var res struct {
		Session struct {
			Harness string
		}
	}
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("세션 응답 해석 실패: %v\n%s", err, raw)
	}
	return res.Session.Harness
}

// ① 옛 클라이언트 — harness 필드가 아예 없다.
func TestOpenSessionWithoutHarnessFieldStaysUnknown(t *testing.T) {
	e := newEnv(t, nil)
	body := `{"project":"p","project_path":"` + e.repo + `","machine_id":"m1",` +
		`"hostname":"box","worktree":"/w/a","cc_session_id":"cc-old","label":""}`

	rec := e.write("POST", "/api/v1/sessions", body)
	if !okOpen(rec.Code) {
		t.Fatalf("옛 클라이언트 본문이 %d 로 거절됐다 — 판올림이 옛 클라이언트를 끊는다:\n%s",
			rec.Code, rec.Body.String())
	}
	if got := sessionHarnessOf(t, rec.Body.String()); got != "" {
		t.Fatalf("필드가 없는데 하네스가 %q 다 — 「미상」은 빈 값이고 claude 로 접지 않는다", got)
	}
}

// 새 클라이언트 — 선언이 그대로 실린다.
func TestOpenSessionCarriesTheDeclaredHarness(t *testing.T) {
	e := newEnv(t, nil)
	body := `{"project":"p","project_path":"` + e.repo + `","machine_id":"m1",` +
		`"hostname":"box","worktree":"/w/b","cc_session_id":"cc-new","label":"","harness":"codex"}`

	rec := e.write("POST", "/api/v1/sessions", body)
	if !okOpen(rec.Code) {
		t.Fatalf("세션 열기가 %d 로 끝났다:\n%s", rec.Code, rec.Body.String())
	}
	if got := sessionHarnessOf(t, rec.Body.String()); got != "codex" {
		t.Fatalf("응답의 하네스가 %q 다 — codex 여야 한다", got)
	}
}

// ② 새 클라이언트 → 옛 서버의 대리 — **모르는 필드는 무시된다.**
//
// ★ 이 시험이 빨개지면 판올림 하나가 옛 클라이언트 전부를 400 으로 끊는다는 뜻이다.
// 그때 고칠 것은 이 시험이 아니라 디코더다.
func TestUnknownRequestFieldsAreIgnoredNotRejected(t *testing.T) {
	e := newEnv(t, nil)
	body := `{"project":"p","project_path":"` + e.repo + `","machine_id":"m1",` +
		`"hostname":"box","worktree":"/w/c","cc_session_id":"cc-future","label":"",` +
		`"harness":"claude","some_axis_from_a_later_version":"x","another":{"n":1}}`

	rec := e.write("POST", "/api/v1/sessions", body)
	if !okOpen(rec.Code) {
		t.Fatalf("모르는 필드가 있다고 %d 로 거절했다 — 섞여 도는 함대에서 이것은 "+
			"판올림이 서로를 끊는다는 뜻이다:\n%s", rec.Code, rec.Body.String())
	}
	// 아는 필드는 그대로 살아야 한다 — 무시가 "본문 전체를 버린다"로 번지면 안 된다.
	if got := sessionHarnessOf(t, rec.Body.String()); got != "claude" {
		t.Fatalf("모르는 필드와 함께 온 harness 가 %q 다 — claude 여야 한다", got)
	}
	if !strings.Contains(rec.Body.String(), "cc-future") {
		t.Fatalf("응답이 이 세션의 것이 아니다:\n%s", rec.Body.String())
	}
}
