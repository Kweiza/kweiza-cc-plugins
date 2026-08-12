package web

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestParseProjectAxis 는 버튼 값의 해석이다. 순수 함수라 표로 본다.
func TestParseProjectAxis(t *testing.T) {
	for _, c := range []struct {
		raw, axis, target string
	}{
		{"pin:kweiza-cc-plugins", "pin", "kweiza-cc-plugins"},
		{"unpin:a", "unpin", "a"},
		{"archive:machine-probe", "archive", "machine-probe"},
		{"unarchive:x", "unarchive", "x"},
		// 프로젝트 id 에 콜론이 들어와도 축은 첫 콜론에서만 갈린다.
		{"pin:a:b", "pin", "a:b"},
		{"", "", ""},
		{"pin", "", ""},
		{":x", "", ""},
		{"pin:", "", ""},
	} {
		axis, target := ParseProjectAxis(c.raw)
		if axis != c.axis || target != c.target {
			t.Fatalf("ParseProjectAxis(%q) = (%q,%q), 기대 (%q,%q)", c.raw, axis, target, c.axis, c.target)
		}
	}
}

// TestJudgeProjectViewFillsReason 은 **거절 사유가 항상 다르다**는 단정이다.
//
// ★ 불리언으로 접으면 "그런 프로젝트가 없다"와 "축이 이상하다"와 "빈 값이다"가 같은 실패가
// 되고, 화면은 세 경우에 똑같은 말을 한다. ActionVerdict 가 사유를 담는 이유와 같다.
func TestJudgeProjectViewFillsReason(t *testing.T) {
	known := []string{"a", "b"}
	seen := map[string]string{}
	for name, in := range map[string]ProjectViewInput{
		"빈 축":       {Project: "a", Target: "a", Axis: ""},
		"모르는 축":     {Project: "a", Target: "a", Axis: "delete"},
		"빈 대상":      {Project: "a", Target: "", Axis: "pin"},
		"없는 대상":     {Project: "a", Target: "없다", Axis: "pin"},
		"빈 현재 프로젝트": {Project: "", Target: "a", Axis: "pin"},
	} {
		v := JudgeProjectView(in, known)
		if v.OK {
			t.Fatalf("%s: 통과했다 — 거절해야 한다", name)
		}
		if strings.TrimSpace(v.Reason) == "" {
			t.Fatalf("%s: 사유가 비었다 — 사유 없는 거절은 화면에서 원인이 안 보인다", name)
		}
		if prev, dup := seen[v.Reason]; dup {
			t.Fatalf("%s 와 %s 가 같은 사유를 낸다(%q) — 세 경우가 한 실패로 접혔다", name, prev, v.Reason)
		}
		seen[v.Reason] = name
	}

	for _, in := range []ProjectViewInput{
		{Project: "a", Target: "a", Axis: "pin"},
		{Project: "a", Target: "b", Axis: "unpin"},
		{Project: "a", Target: "b", Axis: "archive"},
		{Project: "a", Target: "b", Axis: "unarchive"},
	} {
		if v := JudgeProjectView(in, known); !v.OK {
			t.Fatalf("%+v 가 거절됐다: %s", in, v.Reason)
		}
	}
}

// TestProjectViewWritePinsAndRedirects 는 실제 쓰기 한 바퀴다.
func TestProjectViewWritePinsAndRedirects(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	// ★ 브리프 원문은 이 줄이 없었다 — testProject 를 원장에 등록하지 않은 채 그것을
	// pin 대상으로 썼다. withRepo 는 git 저장소만 만들 뿐 프로젝트 행을 안 넣으므로
	// (project_nav_test.go 의 addProject 주석과 같은 사실) 그 상태로 돌리면 JudgeProjectView 가
	// "등록돼 있지 않다"로 400 을 냈다(실측). 판정·핸들러가 아니라 시험 픽스처의 누락이라
	// 여기서 등록을 보탠다.
	f.addProject(testProject)
	f.addProject("dead")

	rec := f.post("/actions/project-view", url.Values{
		"project": {testProject},
		"axis":    {"pin:" + testProject},
	})
	if rec.Code != 303 {
		t.Fatalf("응답 %d, 기대 303 — 쓰기는 리다이렉트로 돌아온다(더블 제출 방지)", rec.Code)
	}
	// ★ 이 단정이 없으면 이 자리의 되돌림 수정에 회귀 방어가 하나도 없다. 실측:
	// h.seeOther 를 옛 http.Redirect 로 되돌려도 전 스위트가 초록이었다 —
	// back() 이 낳는 시험 둘만 Location 을 보고 있었고 이 갈래는 아무도 안 봤다.
	if landed := resolveFrom(t, "/actions/project-view", rec.Header().Get("Location")); landed.Path != "/dcp-dev-board/" {
		t.Fatalf("Location 이 %q 에 착지한다 — 접두 안의 뿌리여야 한다", landed.Path)
	}

	_, html := f.get("")
	nav := navOf(t, html)
	before, _, found := strings.Cut(nav, "<details")
	if !found {
		t.Fatal("핀을 찍었는데 접는 자리가 안 생겼다")
	}
	if !strings.Contains(before, testProject) {
		t.Fatalf("핀을 찍은 %q 가 펴진 쪽에 없다", testProject)
	}
	if strings.Contains(before, "dead") {
		t.Fatal("핀이 아닌 dead 가 펴진 쪽에 있다")
	}
}

// TestProjectViewWriteRefusesUnknownTarget 은 없는 프로젝트를 400 으로 거절한다는 단정이다.
func TestProjectViewWriteRefusesUnknownTarget(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	rec := f.post("/actions/project-view", url.Values{
		"project": {testProject},
		"axis":    {"pin:없는프로젝트"},
	})
	if rec.Code != 400 {
		t.Fatalf("응답 %d, 기대 400", rec.Code)
	}
}

// TestProjectViewButtonsWorkOnTheNotFoundPage 는 **없는 프로젝트를 요청한 화면에서도
// 축 버튼이 실제로 도는지**를 왕복으로 잰다.
//
// ★ 이 갈래가 왜 지원되는 상태인가: buildPage 가 「요청한 프로젝트를 못 찾아도 헤더의
// 프로젝트 줄은 그대로 나간다」를 명시로 약속한다(그 갈래는 p.Project 가 제로값인 채
// 일찍 return 한다). 그래서 사람이 오타 난 URL 에서도 별을 누를 수 있어야 한다.
//
// ★ 무엇이 회귀하면 빨개지나: 템플릿의 hidden project 필드가 해결값(Page.Current)이 아니라
// {{.Project.ID}} 로 되돌아가면 그 값이 빈 문자열이 되고, JudgeProjectView 가 「돌아갈
// 프로젝트가 비었다」로 400 을 낸다 — **그 화면의 버튼 전부가 죽는다.** 조용하지는 않지만
// (누르면 400 이 보인다) 그것을 재는 시험이 없어서 최종 리뷰가 이 자리를 지적했다.
func TestProjectViewButtonsWorkOnTheNotFoundPage(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.addProject(testProject)
	f.addProject("junk")

	// 없는 프로젝트를 요청한다 — 페이지는 나오고 프로젝트 줄도 나온다.
	code, html := f.get("?project=없는것")
	if code != 404 {
		t.Fatalf("없는 프로젝트 요청에 %d — 이 갈래는 404 다", code)
	}

	// ★ **템플릿이 실제로 낸 값을 뽑아서 그것으로 보낸다.** 여기서 값을 손으로 적으면
	//   이 시험은 템플릿과 무관해져 아무것도 안 재게 된다(실측: hidden 을 {{.Project.ID}}
	//   로 되돌려도 초록이었다). hidden 은 <form> 바로 뒤·<nav> 앞이라 navOf 로도 안 잡힌다.
	m := regexp.MustCompile(`<form class="pview"[\s\S]*?name="project" value="([^"]*)"`).
		FindStringSubmatch(html)
	if m == nil {
		t.Fatal("표시 축 폼의 hidden project 필드를 못 찾았다 — 폼 구조가 바뀌었다")
	}
	sent := m[1]
	if sent == "" {
		t.Fatal("NotFound 화면의 hidden project 가 비었다 — 이 화면의 축 버튼이 전부 400 이 된다")
	}

	// 그 화면에서 별을 누른 것과 같은 요청을 보낸다. 400 이면 회귀다.
	rec := f.post("/actions/project-view", url.Values{
		"project": {sent},
		"axis":    {"pin:junk"},
	})
	if rec.Code != 303 {
		t.Fatalf("NotFound 화면에서 온 축 요청에 %d, 기대 303 (보낸 project=%q)", rec.Code, sent)
	}
	got, err := f.st.GetProject(context.Background(), "junk")
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if got.PinnedAt.IsZero() {
		t.Fatal("303 을 냈는데 핀이 안 켜졌다")
	}
}

// TestProjectViewArchiveThenUnarchive 는 되돌리는 길이 실제로 도는지 본다.
// 보관에 사유를 안 받는 근거가 "클릭 하나로 돌아온다"이므로 그 문장을 시험이 든다.
func TestProjectViewArchiveThenUnarchive(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	// ★ 같은 누락(위 TestProjectViewWritePinsAndRedirects 주석 참고) — f.pin 이
	// SetProjectView 를 직접 부르는데 testProject 가 원장에 없으면 그 자리에서 바로
	// t.Fatalf 로 죽는다(실측: "핀 실패(cp): 프로젝트 cp 가 없다").
	f.addProject(testProject)
	f.addProject("junk")
	f.pin(testProject)

	if rec := f.post("/actions/project-view", url.Values{
		"project": {testProject}, "axis": {"archive:junk"},
	}); rec.Code != 303 {
		t.Fatalf("보관 응답 %d, 기대 303", rec.Code)
	}
	_, html := f.get("")
	mustContain(t, navOf(t, html), "보관 1", "보관 뒤에는 그 수가 줄에 나야 한다")

	if rec := f.post("/actions/project-view", url.Values{
		"project": {testProject}, "axis": {"unarchive:junk"},
	}); rec.Code != 303 {
		t.Fatalf("되돌리기 응답 %d, 기대 303", rec.Code)
	}
	_, html = f.get("")
	nav := navOf(t, html)
	if strings.Contains(nav, "보관 1") {
		t.Fatal("되돌렸는데 보관 수가 그대로다")
	}
	mustContain(t, nav, "나머지 1", "되돌린 것은 보통으로 돌아가 접힌 쪽에 남는다")
}

// TestProjectViewButtonsCarryDistinctIdempotencyKeys 는 같은 렌더 안의 버튼들이 서로 다른
// 멱등 키를 낸다는 단정이다(리뷰 Important-2).
//
// ★ 왜 필요한가: Page.WriteKey 는 렌더 시각(초 단위)만 넣는다. 표시 축 폼(.pview)은
// 버튼이 여럿인 폼 하나라, 폼 action 의 키 하나를 전부가 공유하면 303 리다이렉트 뒤
// 같은 초 안에 그려진 새 페이지에서 **다른** 버튼을 눌렀을 때 같은 키·다른 본문이 되어
// Fingerprint 불일치로 409(Conflict)가 난다(실측). 회수·폐기는 사유 입력 때문에 왕복이
// 길어 이 함정을 안 겪었지만, 핀·보관은 줄을 정리하려고 연달아 누르는 것이 정상 사용
// 흐름이다. 그래서 버튼마다 formaction 으로 "project-view:<축>:<대상 id>" 꼴의 키를 낸다.
func TestProjectViewButtonsCarryDistinctIdempotencyKeys(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.addProject(testProject)
	f.addProject("dead") // 핀도 현재도 아니다 — 접힌 쪽에서 pin·archive 버튼 둘을 함께 낸다.
	f.pin(testProject)

	_, html := f.get("")
	nav := navOf(t, html)

	// 같은 프로젝트("dead")의 pin 버튼과 archive 버튼이 같은 렌더 안에 함께 있다 —
	// 축까지 키에 넣지 않고 대상 id 만 넣었다면 이 둘이 같은 키를 냈을 자리다.
	pinKey := formactionKeyAfter(t, nav, `value="pin:dead"`)
	archiveKey := formactionKeyAfter(t, nav, `value="archive:dead"`)
	unpinKey := formactionKeyAfter(t, nav, `value="unpin:`+testProject+`"`)

	if pinKey == archiveKey {
		t.Fatalf("dead 의 pin 버튼과 archive 버튼이 같은 멱등 키(%q)를 쓴다 — "+
			"대상 id 만 넣고 축을 안 넣으면 같은 렌더에서 하나를 누른 뒤 다른 것을 눌러도 "+
			"같은 키·다른 본문으로 409 다", pinKey)
	}
	if pinKey == unpinKey || archiveKey == unpinKey {
		t.Fatalf("서로 다른 대상의 버튼이 같은 키(pin=%q archive=%q unpin=%q)를 쓴다", pinKey, archiveKey, unpinKey)
	}
	// 명명 규약도 함께 잠근다 — "project-view:<축>:<대상 id>:<렌더 시각>" 꼴이다.
	// html/template 은 URL 질의값의 콜론을 퍼센트 인코딩하므로(%3a) 원문을 그대로
	// 비교하지 않고 디코드한 값을 본다 — 서버는 net/url 이 자동으로 디코드해 읽으므로
	// 인코딩 자체는 기능에 무해하다.
	for name, got := range map[string]string{"pin": pinKey, "archive": archiveKey, "unpin": unpinKey} {
		if !strings.Contains(got, "project-view") {
			t.Fatalf("%s 키(%q)가 project-view 계열이 아니다", name, got)
		}
	}
}

// formactionKeyAfter 는 value="<valueAttr>" 바로 다음에 나오는 버튼의
// formaction="actions/project-view?key=<키>" 에서 <키> 를 뽑아 URL 디코드한다.
func formactionKeyAfter(t *testing.T, html, valueAttr string) string {
	t.Helper()
	i := strings.Index(html, valueAttr)
	if i < 0 {
		t.Fatalf("HTML 에 %s 가 없다", valueAttr)
	}
	rest := html[i:]
	const marker = `formaction="actions/project-view?key=`
	j := strings.Index(rest, marker)
	if j < 0 {
		t.Fatalf("%s 뒤에 formaction 이 없다", valueAttr)
	}
	rest = rest[j+len(marker):]
	raw, _, ok := strings.Cut(rest, `"`)
	if !ok {
		t.Fatalf("%s 의 formaction key 가 안 닫혔다", valueAttr)
	}
	key, err := url.QueryUnescape(raw)
	if err != nil {
		t.Fatalf("%s 의 formaction key(%q) 디코드 실패: %v", valueAttr, raw, err)
	}
	return key
}

// TestProjectViewOnlyTouchesTheAxisItChanges 는 한 축을 바꿀 때 다른 축을 지우지 않는다는
// 단정이다(리뷰 Important-3).
//
// ★ 왜 필요한가: projectView 핸들러는 GetProject 로 현재 값을 먼저 읽어 바꿀 축 하나만
// 갈아 끼운다(actions.go). 누가 그 GetProject 호출을 지우고
// SetProjectView(ctx, target, time.Time{}, time.Time{}) 로 줄여도, 이 시험이 없으면
// **현재 시험 전건이 그대로 통과한다** — unpin 을 부르는 시험이 없고, 기존
// TestProjectViewArchiveThenUnarchive 의 unarchive 대상("junk")은 애초에 핀이 아니라
// 지울 값이 없다. store/project.go 의 SetProjectView 주석("핀 토글 처리기가
// SetProjectView(ctx, id, time.Now(), time.Time{}) 라고만 쓰면 보관이 날아간다")이 이
// 회귀를 이름까지 붙여 예고해 둔 자리다.
//
// 핸들러는 pin/archive 를 상호배타로 만들지만 원장(SetProjectView) 자체는 그 조합을
// 막지 않는다(옛 값·수동 UPDATE·경합으로 생길 수 있다) — 그 조합을 store 를 직접 불러
// 만든 뒤 한 축만 바꿔서, 다른 축이 살아남는지를 원장에서 직접 잰다.
func TestProjectViewOnlyTouchesTheAxisItChanges(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.addProject(testProject)
	f.addProject("both")
	if err := f.st.SetProjectView(context.Background(), "both", time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatalf("표시 축 직접 설정 실패: %v", err)
	}

	if rec := f.post("/actions/project-view", url.Values{
		"project": {testProject}, "axis": {"unarchive:both"},
	}); rec.Code != 303 {
		t.Fatalf("되돌리기 응답 %d, 기대 303", rec.Code)
	}

	p, err := f.st.GetProject(context.Background(), "both")
	if err != nil {
		t.Fatalf("프로젝트 조회 실패: %v", err)
	}
	if p.PinnedAt.IsZero() {
		t.Fatal("unarchive 가 핀도 함께 지웠다 — 바뀐 축(archived) 하나만 갈아 끼워야 한다")
	}
	if !p.ArchivedAt.IsZero() {
		t.Fatal("unarchive 인데 보관이 안 풀렸다")
	}
}

// projectViewFormOpen 은 표시 축 폼의 여는 태그 통째다.
// ★ 클래스만 세면 이 폼이 GET 으로 바뀌어도 하나로 세어져, render_test.go 의 POST 뺄셈이
// 하나 어긋나 **이 폼과 무관한 자리에서** 빨개진다(로그아웃이 같은 함정을 겪었다).
const projectViewFormOpen = `<form class="pview" method="post" action="actions/project-view`
