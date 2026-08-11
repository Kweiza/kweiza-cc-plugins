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

// TestCountPinnedIsTheOneCount 는 핀 세기가 한 벌이라는 단정이다. 순수 함수라 표로 본다.
//
// ★ 이 수를 두 자리가 본다 — buildProjectNav(접을지 정한다)와 buildPage(보관 나이 조회를
// 아예 돌릴지 정한다). 두 자리가 각자 세면 한쪽만 고쳐지는 날 「접기는 안 하는데 조회는
// 돈다」거나 그 반대가 되고, 그 어긋남은 화면에 안 뜬다 — 느려질 뿐이다.
func TestCountPinnedIsTheOneCount(t *testing.T) {
	at := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		in   []model.Project
		want int
	}{
		"빈 목록":      {nil, 0},
		"핀 없음":      {[]model.Project{{ID: "a"}, {ID: "b"}}, 0},
		"핀 하나":      {[]model.Project{{ID: "a", PinnedAt: at}, {ID: "b"}}, 1},
		"전부 핀":      {[]model.Project{{ID: "a", PinnedAt: at}, {ID: "b", PinnedAt: at}}, 2},
		"보관은 안 센다":  {[]model.Project{{ID: "a", ArchivedAt: at}, {ID: "b"}}, 0},
		"핀이면서 보관이면": {[]model.Project{{ID: "a", PinnedAt: at, ArchivedAt: at}}, 1},
	} {
		if got := CountPinned(c.in); got != c.want {
			t.Fatalf("%s: CountPinned = %d, 기대 %d", name, got, c.want)
		}
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

// TestProjectNavNoPinsMentionsArchivedCount 는 핀이 0일 때 보관된 프로젝트가 **조용히**
// 줄에 섞이지 않는다는 단정이다(최종 리뷰 Minor-1).
//
// ★ pinned == 0 이면 buildProjectNav 의 switch 가 row.Archived 를 안 보고 전원을
// nav.Shown 으로 보낸다 — 사람이 보관을 걸어 둔 뒤 핀을 전부 풀면 보관해 둔 것들이
// 아무 표시 없이 되돌아온 것처럼 보이는 자리다. OutOfWindow·Folded 가 이미 지키는
// "0건과 접힘을 침묵으로 뭉개지 않는다"는 규율을 이 축에도 건다.
func TestProjectNavNoPinsMentionsArchivedCount(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.addProject(testProject)
	f.addProject("was-archived")
	f.archive("was-archived") // 핀은 전부 풀린 상태 — pinned == 0.

	_, html := f.get("")
	nav := navOf(t, html)

	mustContain(t, nav, "핀이 없다", "핀이 0이라는 사실은 그대로 말해야 한다")
	mustContain(t, nav, "보관 1건", "보관된 것이 조용히 펴졌다는 사실을 말해야 한다")
	if !strings.Contains(nav, "was-archived") {
		t.Fatal("보관된 프로젝트가 줄에서 사라졌다 — 핀 0이면 전부 편다는 규율과 어긋난다")
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

// TestProjectNavUnfoldsResolvedCurrentProjectOnBareEntry 는 "/" 로 들어왔을 때
// (?project= 없이) 실제로 그려지는 프로젝트가 접힌 쪽으로 밀리지 않는다는 단정이다.
//
// ★ req.project 는 질의 문자열 원문이라 "/" 에서는 빈 문자열이다. 그런데 실제로
// 보드가 그려지는 프로젝트는 pickProject 가 정하는 **id 순 첫 번째**다. Nav 가
// 이 해결값이 아니라 질의 원문을 current 로 쓰면, 핀이 있고 id 순 첫 프로젝트가
// 핀이 아닐 때 "지금 보드가 그려진 바로 그 프로젝트"가 스스로 <details> 안에
// 접힌다 — 화면이 자기 위치를 안 말하는 사고다(리뷰 Important-1).
func TestProjectNavUnfoldsResolvedCurrentProjectOnBareEntry(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.addProject("aa-current") // id 순 첫째. 핀이 아니다 — "/" 로 들어오면 이것이 현재 프로젝트다.
	f.addProject("mm-folded")  // 핀도 현재도 아니다 — 접힐 것이 하나는 있어야 <details> 가 생긴다.
	f.addProject("zz-pinned")
	f.pin("zz-pinned")

	_, html := f.get("") // ?project= 를 안 준다 — 기본 진입이다.
	nav := navOf(t, html)

	before, _, found := strings.Cut(nav, "<details")
	if !found {
		t.Fatal("접는 자리가 없다 — 이 시험의 전제가 깨졌다")
	}
	if !strings.Contains(before, "aa-current") {
		t.Fatalf(`"/" 로 들어왔을 때 pickProject 가 고른 id 순 첫 프로젝트 %q 가 접힌 쪽에 있다 — `+
			`Nav 가 질의 원문("")을 그대로 썼지 실제로 그려지는 프로젝트를 모른다`, "aa-current")
	}
}

// TestProjectNavHighlightsResolvedCurrentProjectOnBareEntry 는 같은 상황에서
// on 강조가 실제로 그려지는 프로젝트에 붙는다는 단정이다.
//
// ★ 옛 템플릿은 `{{if eq .ID $.Project.ID}}` 로 **해결된** 프로젝트를 강조했다.
// 새 템플릿은 `{{if .On}}` = `p.ID == current` 라, current 를 질의 원문으로 잘못
// 넘기면 "/" 에서 아무것도 강조되지 않는다 — 이 강조 클래스를 단정하는 시험이
// 이전에는 저장소에 하나도 없어 그 회귀가 조용히 통과했다(리뷰 Important-1).
func TestProjectNavHighlightsResolvedCurrentProjectOnBareEntry(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.addProject("aa-current")
	f.addProject("mm-folded")
	f.addProject("zz-pinned")
	f.pin("zz-pinned")

	_, html := f.get("")
	nav := navOf(t, html)

	mustContain(t, nav, `class="on" href="?project=aa-current"`,
		`"/" 로 들어왔을 때 실제로 그려지는 프로젝트(id 순 첫째)에 on 강조가 없다`)
}

// TestArchivedProjectLastSessionAgeHasExactlyOneSuffix 는 보관 목록의 마지막 세션
// 나이 문구가 "전" 접미사를 두 번 안 붙인다는 단정이다.
//
// ★ web.Age 는 이미 "3일 전" 처럼 접미사를 포함해서 낸다(format.go:64).
// archivedSessionAges 가 그 위에 또 " 전" 을 붙이면 "3일 전 전" 이 된다 — 이
// 태스크가 화면에 새로 낸 유일한 파생 문자열이라 여기가 비면 축 하나가 통째로
// 무보호로 남는다(리뷰 Important-2). "gone" 처럼 세션이 아예 없는 보관 프로젝트는
// "세션 없음" 갈래로 빠져 이 문제를 못 잡으므로, 실제로 세션을 하나 연다.
func TestArchivedProjectLastSessionAgeHasExactlyOneSuffix(t *testing.T) {
	fixedNow := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	f := newFixture(t, withClock(func() time.Time { return fixedNow })).withRepo("feat")

	sess := f.openSession("cc-arch", "옛 세션")
	// 열린 시각을 사흘 전으로 되돌린다 — "방금"·"세션 없음" 갈래를 피하고 span 이
	// 실제로 "N일" 모양으로 나오게 한다. render_test.go 의 다른 시험들과 같은
	// SQL 우회다(TestDashboardSaysWhatTheWindowCutOff 를 보라).
	oldOpened := fixedNow.Add(-3 * 24 * time.Hour).Format("2006-01-02T15:04:05.000000Z")
	if _, err := f.st.DB().Exec(`UPDATE session SET opened_at = ? WHERE id = ?`,
		oldOpened, sess.ID); err != nil {
		t.Fatalf("세션 개시 시각 되돌리기 실패: %v", err)
	}
	// openSession 이 이미 testProject 를 원장에 upsert 했으므로 archive 는 바로 된다.
	f.archive(testProject)
	f.addProject("keep")
	f.pin("keep")

	// testProject 를 보지 않는다 — 보고 있으면 On 이 이겨 Shown 으로 가고, 나이 문구가
	// 실리는 Archived 하위 목록(<details> 안)을 안 거친다.
	_, html := f.get("?project=keep")
	nav := navOf(t, html)

	mustContain(t, nav, "3일 전", "보관된 프로젝트의 마지막 세션 나이가 안 보인다")
	if strings.Contains(nav, "전 전") {
		t.Fatal(`나이 접미사가 두 번 붙었다("… 전 전") — Age 가 이미 "전" 을 포함하므로 밖에서 또 붙이면 안 된다`)
	}
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
