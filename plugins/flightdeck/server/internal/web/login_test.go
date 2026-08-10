package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginScreenRendersForm(t *testing.T) {
	rec := httptest.NewRecorder()
	LoginScreen(rec, httptest.NewRequest("GET", "/", nil),
		LoginView{Error: "토큰이 일치하지 않는다", Next: "/?project=kweiza", Action: "login"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("상태가 %d 다 — 401 이어야 한다", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type 이 %q 다", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control 이 %q 다 — 로그인 화면이 캐시되면 안 된다", cc)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`method="post"`,
		`action="login"`,  // 상대경로다 — 프록시 접두 뒤에서도 맞는 자리를 가리킨다
		`type="password"`, // 어깨너머로 안 보인다
		`name="token"`,
		`name="next"`,
		"토큰이 일치하지 않는다",
		`value="/?project=kweiza"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("본문에 %q 가 없다", want)
		}
	}
}

// TestLoginScreenEscapes 는 사유와 next 가 HTML 로 새지 않는지 본다.
func TestLoginScreenEscapes(t *testing.T) {
	rec := httptest.NewRecorder()
	LoginScreen(rec, httptest.NewRequest("GET", "/", nil),
		LoginView{Error: `<script>alert(1)</script>`, Next: `/"><script>x</script>`})

	body := rec.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatal("사유가 이스케이프 없이 나갔다")
	}
	if strings.Contains(body, `"><script>x</script>`) {
		t.Fatal("next 가 속성 밖으로 샜다")
	}
}

// TestLoginScreenFirstVisitHasNoError 는 첫 방문에 빈 오류 자리가 안 뜨는지 본다.
func TestLoginScreenFirstVisitHasNoError(t *testing.T) {
	rec := httptest.NewRecorder()
	LoginScreen(rec, httptest.NewRequest("GET", "/", nil), LoginView{Next: "/", Action: "login"})
	if strings.Contains(rec.Body.String(), `class="err"`) {
		t.Fatal("사유가 없는데 오류 자리가 떴다")
	}
}

// TestLoginScreenUsesGivenAction 은 폼 action 이 **주어진 값**인지 본다.
//
// ★ 이 패키지가 깊이를 세면 안 된다. 값을 박아 두면 뿌리가 아닌 자리에서 뜬 폼이
// /actions/login 처럼 없는 곳을 가리키고, 그 자리도 세션 이전 경로가 아니라 다시 401 이
// 난다 — 사람은 토큰을 정확히 쳐도 같은 폼을 무한히 다시 본다.
// 값을 만드는 자리는 api 의 JudgeLoginAction 이고, 그 값과 로그인 라우트를 잇는 왕복
// 시험은 api/login_test.go 의 TestLoginFormActionReachesLoginRoute 다.
func TestLoginScreenUsesGivenAction(t *testing.T) {
	for _, action := range []string{"login", "../login", "../../../login"} {
		rec := httptest.NewRecorder()
		LoginScreen(rec, httptest.NewRequest("GET", "/", nil), LoginView{Next: "/", Action: action})
		if want := `action="` + action + `"`; !strings.Contains(rec.Body.String(), want) {
			t.Errorf("폼에 %q 가 없다 — 템플릿이 준 값을 안 쓴다", want)
		}
	}
}

// TestDashboardHasLogout 은 대시보드에 쿠키를 지울 길이 있는지 본다.
//
// ★ 로그아웃이 없으면 쿠키를 버릴 수단이 브라우저 설정뿐이다. 수명이 10년이라 그 길이
// 없으면 남의 머신에서 한 번 본 것이 사실상 영구히 남는다.
func TestDashboardHasLogout(t *testing.T) {
	src, err := files.ReadFile("dashboard.gohtml")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, `action="logout"`) {
		t.Error(`로그아웃 폼이 없다 (action="logout" — 상대경로여야 한다)`)
	}
	if !strings.Contains(body, `method="post"`) {
		t.Error("로그아웃이 POST 가 아니다 — GET 이면 링크 프리페치로 눌린다")
	}
}
