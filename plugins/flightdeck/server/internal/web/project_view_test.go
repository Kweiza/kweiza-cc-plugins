package web

import (
	"net/url"
	"strings"
	"testing"
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

// projectViewFormOpen 은 표시 축 폼의 여는 태그 통째다.
// ★ 클래스만 세면 이 폼이 GET 으로 바뀌어도 하나로 세어져, render_test.go 의 POST 뺄셈이
// 하나 어긋나 **이 폼과 무관한 자리에서** 빨개진다(로그아웃이 같은 함정을 겪었다).
const projectViewFormOpen = `<form class="pview" method="post" action="actions/project-view`
