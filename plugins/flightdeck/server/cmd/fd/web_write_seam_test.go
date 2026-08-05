package main

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 대시보드 쓰기가 **조립된 서버에서** 실제로 통하는가.
//
// ★ 이 시험이 없어서 버튼 둘이 죽은 채로 전 스위트가 초록이었다.
// internal/web/actions_test.go 는 게이트 사슬 **밖**의 web.New 핸들러를 직접 눌러서
// 원리적으로 이 축을 못 본다. cmd/fd/auth_gate_test.go 는 조립 서버를 누르지만
// 무인증 401 만 보므로, 인증을 통과한 뒤의 사슬(withIdempotency)에는 도달하지 않는다.
//
// 여기서만 보이는 것: api.go 가 화면을 mux **안**에 넣어 게이트를 전부 타게 했는데
// (그 자체는 인가 우회를 닫은 의도된 수정이다), 그 사슬의 withIdempotency 가
// 모든 쓰기에 Idempotency-Key 헤더를 요구하고 **평범한 <form> 은 헤더를 못 싣는다.**

// formBlock 은 렌더된 한 장에서 action 이 주어진 폼 블록을 떼어낸다.
//
// 손으로 만든 요청이 아니라 **화면이 실제로 그린 폼**을 눌러야 한다 —
// 미들웨어만 고치고 폼에 키를 안 실으면 손요청 시험은 초록인데 브라우저는 계속 400 이다.
func formBlock(t *testing.T, html, actionSuffix string) (action string, fields url.Values) {
	t.Helper()
	re := regexp.MustCompile(`(?s)<form[^>]*method="post"[^>]*>.*?</form>`)
	for _, blk := range re.FindAllString(html, -1) {
		m := regexp.MustCompile(`action="([^"]*)"`).FindStringSubmatch(blk)
		if m == nil || !strings.Contains(m[1], actionSuffix) {
			continue
		}
		vals := url.Values{}
		for _, in := range regexp.MustCompile(`<input[^>]*>`).FindAllString(blk, -1) {
			name := regexp.MustCompile(`name="([^"]*)"`).FindStringSubmatch(in)
			if name == nil {
				continue
			}
			val := regexp.MustCompile(`value="([^"]*)"`).FindStringSubmatch(in)
			if val != nil {
				vals.Set(name[1], val[1])
			}
		}
		return m[1], vals
	}
	t.Fatalf("action 에 %q 를 담은 POST 폼이 화면에 없다:\n%s", actionSuffix, clipHTML(html))
	return "", nil
}

func clipHTML(s string) string {
	if len(s) > 3000 {
		return s[:3000] + "\n…(잘림)"
	}
	return s
}

// TestDashboardWriteFormsGoThroughTheAssembledServer 는 화면이 그린 폼을
// 브라우저가 보내는 그대로 보내고, 조립된 서버가 그것을 **받아들이는지** 본다.
func TestDashboardWriteFormsGoThroughTheAssembledServer(t *testing.T) {
	h := newHarness(t)

	const item = "victim-item"
	if code, out := h.run("", "add", "--id", item, "--title", "회수 대상",
		"--body", "조립 서버 쓰기 시험의 대상이다"); code != 0 {
		t.Fatalf("항목 추가 실패(code=%d): %s", code, out)
	}
	if code, out := h.run("", "pick", item); code != 0 {
		t.Fatalf("선점 실패(code=%d): %s", code, out)
	}

	// 리다이렉트를 따라가면 303 과 200 이 구분되지 않는다. 여기서 멈춘다.
	cli := h.srv.Client()
	cli.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	pageURL := h.srv.URL + "/?project=" + url.QueryEscape(h.project)
	res, err := cli.Get(pageURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("전제가 깨졌다 — 화면이 %d 다", res.StatusCode)
	}
	html := string(body)

	base, err := url.Parse(pageURL)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ name, suffix, item string }{
		{"선점 회수", "actions/reclaim", item},
		{"항목 폐기", "actions/drop", item},
	} {
		t.Run(c.name, func(t *testing.T) {
			action, fields := formBlock(t, html, c.suffix)

			// select 는 <input> 이 아니라서 위에서 안 잡힌다. 브라우저가 고른 값을 넣는다.
			fields.Set("item", c.item)
			fields.Set("reason", "조립 서버 쓰기 경로를 재는 시험이다")
			if fields.Get("project") == "" {
				t.Fatalf("폼에 project 히든이 없다 — 브라우저가 보낼 수 없는 폼이다:\n%s", action)
			}

			ref, err := url.Parse(action)
			if err != nil {
				t.Fatal(err)
			}
			target := base.ResolveReference(ref).String()

			req, _ := http.NewRequest(http.MethodPost, target, strings.NewReader(fields.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			// 브라우저가 같은 출처 폼 전송에 싣는 것 전부. 그 이상은 안 싣는다 —
			// Idempotency-Key 를 손으로 실으면 이 결함이 원리적으로 안 보인다.
			req.Header.Set("Origin", h.srv.URL)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set("Referer", pageURL)

			res, err := cli.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(res.Body)
			res.Body.Close()

			if res.StatusCode != http.StatusSeeOther {
				t.Fatalf("화면이 그린 폼을 그대로 보냈는데 %d 다 — 303 이어야 한다.\n응답: %s",
					res.StatusCode, clipHTML(string(got)))
			}
		})
	}
}

// TestScreenWriteFromAnotherSiteIsRefused 는 위 수정이 **없앤 것의 대체물**을 지킨다.
//
// ★ 이 레포에는 CSRF 토큰·SameSite·Origin 검사가 하나도 없다. 지금까지 그 역할을
// 우연히 대신한 것이 "쓰기에 Idempotency-Key 헤더가 필요하다"였다 — 외부 사이트의
// <form> 은 헤더를 못 싣기 때문이다. 키를 쿼리로 받는 순간 그 우연한 방어가 사라진다:
// 키는 web:<종류>:<unix> 라 **추측이 자명하고**, 폼 action 의 쿼리는 누구나 쓸 수 있다.
//
// 즉 이 시험이 없으면 위 수정은 결함 하나를 고치면서 더 나쁜 것을 연다 —
// 아무 웹사이트나 사람이 열어 둔 대시보드에 항목 폐기를 낼 수 있다.
func TestScreenWriteFromAnotherSiteIsRefused(t *testing.T) {
	h := newHarness(t)

	const item = "csrf-victim"
	if code, out := h.run("", "add", "--id", item, "--title", "외부 출처 시험 대상",
		"--body", "이 항목이 외부 사이트에서 폐기되면 안 된다"); code != 0 {
		t.Fatalf("항목 추가 실패(code=%d): %s", code, out)
	}

	cli := h.srv.Client()
	cli.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	// 공격자가 쓸 수 있는 것 전부: 경로·필드 이름·키 모양. 전부 공개돼 있다.
	target := h.srv.URL + "/actions/drop?key=web:drop:1"
	form := url.Values{
		"project": {h.project},
		"item":    {item},
		"reason":  {"외부 사이트가 낸 폐기다"},
	}

	for _, c := range []struct {
		name    string
		headers map[string]string
	}{
		{"다른 출처가 밝혀진 경우", map[string]string{
			"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}},
		{"Origin 만 다른 경우", map[string]string{
			"Origin": "https://evil.example"}},
		{"출처를 아예 안 밝힌 경우", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			res, err := cli.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(res.Body)
			res.Body.Close()

			if res.StatusCode != http.StatusForbidden {
				t.Errorf("외부 출처 쓰기가 %d 다 — 403 이어야 한다.\n응답: %s",
					res.StatusCode, clipHTML(string(got)))
			}
		})
	}

	// ★ 단정의 좌표계는 상태코드가 아니라 **서버가 실제로 갖게 된 것**이다.
	// 403 을 내면서 폐기가 들어갔으면 위 단정 셋은 아무것도 안 지킨 것이다.
	it, err := h.st.GetItem(t.Context(), h.project, item)
	if err != nil {
		t.Fatalf("대상 항목 조회 실패: %v", err)
	}
	if it.State != model.ItemOpen {
		t.Fatalf("외부 출처 요청 뒤 항목 상태가 %q 다 — open 이어야 한다. 폐기가 실제로 들어갔다", it.State)
	}
}
