package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// pin 은 프로젝트 하나를 핀으로 만든다.
func (f *fixture) pin(id string) {
	f.t.Helper()
	if err := f.st.SetProjectView(context.Background(), id, time.Now().UTC(), time.Time{}); err != nil {
		f.t.Fatalf("핀 실패(%s): %v", id, err)
	}
}

// archive 는 프로젝트 하나를 보관한다.
func (f *fixture) archive(id string) {
	f.t.Helper()
	if err := f.st.SetProjectView(context.Background(), id, time.Time{}, time.Now().UTC()); err != nil {
		f.t.Fatalf("보관 실패(%s): %v", id, err)
	}
}

// TestProjectNavShowsAllWhenNoPins 는 **핀이 0이면 아무것도 안 접는다**는 단정이다.
//
// ★ 이것이 이 화면의 정직함이다. 핀이 없다는 사실을 자동 판정(활동이 있는 것만 편다)으로
// 덮으면, 사람이 접은 것과 규칙이 접은 것이 화면에서 같은 모양이 되고 "왜 사라졌나"에
// 답할 수 없게 된다.
func TestProjectNavShowsAllWhenNoPins(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	// testProject 도 명시로 등록한다 — withRepo 는 git 저장소만 만들 뿐 원장에
	// 프로젝트 행을 넣지 않는다(그것은 세션이 열릴 때만 일어난다).
	f.addProject(testProject)
	f.addProject("other-a")
	f.addProject("other-b")

	_, html := f.get("")

	nav := navOf(t, html)
	for _, want := range []string{testProject, "other-a", "other-b"} {
		if !strings.Contains(nav, want) {
			t.Fatalf("핀이 0인데 %q 가 줄에 없다 — 접으면 안 된다", want)
		}
	}
	mustContain(t, nav, "핀이 없다",
		"핀이 하나도 없다는 사실을 화면이 말해야 한다 — 안 그러면 이 축이 있는 줄 모른다")
	if strings.Contains(nav, "<details") {
		t.Fatal("핀이 0인데 접는 자리가 생겼다")
	}
}

// TestProjectNavFoldsUnpinnedAndSaysHowMany 는 접은 수를 반드시 말한다는 단정이다.
//
// ★ §웹UI 가 OutOfWindow·Folded 에 이미 건 규율과 같은 것이다 — 몇 건인지를 감추면
// "없다"와 "접혀 있다"가 구분되지 않는다.
func TestProjectNavFoldsUnpinnedAndSaysHowMany(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.addProject(testProject)
	f.addProject("keep")
	f.addProject("dead-a")
	f.addProject("dead-b")
	f.addProject("gone")
	f.pin(testProject)
	f.pin("keep")
	f.archive("gone")

	_, html := f.get("")
	nav := navOf(t, html)

	mustContain(t, nav, "<details", "핀이 있으면 나머지가 접혀야 한다")
	// 접힌 것은 셋이다: dead-a · dead-b · gone(보관).
	mustContain(t, nav, "나머지 3", "접은 수를 말해야 한다")
	mustContain(t, nav, "보관 1", "그중 보관이 몇 건인지도 말해야 한다")
	// 접힌 것들도 HTML 에는 있다(열면 보인다) — "없다"가 아니라 "접혀 있다"여야 한다.
	for _, want := range []string{"dead-a", "dead-b", "gone"} {
		if !strings.Contains(nav, want) {
			t.Fatalf("접힌 %q 가 HTML 에서 통째로 사라졌다 — 접는 것과 지우는 것은 다르다", want)
		}
	}
}

// TestProjectNavAlwaysShowsCurrentProject 는 보고 있는 프로젝트가 핀이 아니어도
// 줄에 있다는 단정이다. 없으면 화면이 자기가 어디 있는지를 안 말한다.
func TestProjectNavAlwaysShowsCurrentProject(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.addProject(testProject)
	f.addProject("pinned-one")
	// 핀도 아니고 지금 보는 것도 아닌 프로젝트가 하나는 있어야 <details> 가 생긴다 —
	// 안 그러면 이 시험이 "접힌 쪽에 없다"를 잴 접힌 쪽 자체가 없다.
	f.addProject("dead-c")
	f.pin("pinned-one")
	// testProject 는 핀이 아니다. 그런데 지금 보고 있는 것이다.

	_, html := f.get("?project=" + testProject)
	nav := navOf(t, html)

	before, _, found := strings.Cut(nav, "<details")
	if !found {
		t.Fatal("접는 자리가 없다 — 이 시험의 전제가 깨졌다")
	}
	if !strings.Contains(before, testProject) {
		t.Fatalf("보고 있는 프로젝트 %q 가 접힌 쪽에 있다 — 화면이 자기 위치를 안 말한다", testProject)
	}
}

// TestArchivedProjectStillOpens 는 **보관이 접근 차단이 아니라는** 단정이다.
//
// ★ 이 단정이 이 축을 "표시 계층"이라 부를 수 있는 근거고, render_test.go 의 폼 락이
// 이 폼을 상한에서 빼는 판정이 그 위에 선다. 여기가 빨개지면 그 판정도 함께 무너진다.
func TestArchivedProjectStillOpens(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.addProject(testProject)
	f.archive(testProject)

	code, html := f.get("?project=" + testProject)
	if code != 200 {
		t.Fatalf("보관된 프로젝트를 여니 %d 다 — 보관은 목록에서 빼는 것이지 접근 차단이 아니다", code)
	}
	mustContain(t, html, "① 지금", "보관된 프로젝트도 페이지 전체가 그대로 나와야 한다")
}

// navOf 는 헤더의 프로젝트 줄만 잘라 낸다. 페이지 다른 곳의 프로젝트 id 언급(카드·표)이
// 이 시험들의 단정을 우연히 통과시키는 것을 막는다.
func navOf(t *testing.T, html string) string {
	t.Helper()
	_, rest, ok := strings.Cut(html, `<nav`)
	if !ok {
		t.Fatal("HTML 에 <nav> 가 없다 — 프로젝트 줄이 사라졌다")
	}
	nav, _, ok := strings.Cut(rest, `</nav>`)
	if !ok {
		t.Fatal("<nav> 가 안 닫혔다")
	}
	return nav
}

// addProject 는 프로젝트 하나를 원장에 등록한다. 세션도 항목도 없는 빈 프로젝트다 —
// 접기 규율은 실적을 안 보므로 그것으로 족하다.
func (f *fixture) addProject(id string) {
	f.t.Helper()
	if err := f.st.UpsertProject(context.Background(), model.Project{
		ID: id, Path: "/tmp/" + id, DefaultBranch: "main",
	}); err != nil {
		f.t.Fatalf("프로젝트 등록 실패(%s): %v", id, err)
	}
}
