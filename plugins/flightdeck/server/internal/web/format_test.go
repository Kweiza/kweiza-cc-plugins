package web

import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// 이 파일의 시험은 **판정 함수를 직접** 부른다.
// 렌더된 HTML 로만 단정하면 표 밖 입력(음수 경과·빈 해시·모르는 상태)을 만들 길이 없어
// 그 축이 원리적으로 안 보인다. 반대로 여기만 있으면 소비자 좌표계가 없다 —
// 그래서 render_test.go 가 같은 축을 HTML 문자열로 다시 단정한다.

func TestAge(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"0", 0, "방금"},
		{"1초 미만", 900 * time.Millisecond, "방금"},
		{"12초", 12 * time.Second, "12초 전"},
		{"59초", 59 * time.Second, "59초 전"},
		{"정확히 1분", time.Minute, "1분 전"},
		{"3분", 3 * time.Minute, "3분 전"},
		{"정확히 1시간", time.Hour, "1시간 전"},
		{"2시간 5분", 2*time.Hour + 5*time.Minute, "2시간 5분 전"},
		{"정확히 하루", 24 * time.Hour, "1일 전"},
		{"3일 2시간", 74 * time.Hour, "3일 2시간 전"},
		// 표 밖 — 시계가 어긋난 머신. 0 으로 접으면 가장 이상한 상태가 "방금"으로 보인다.
		{"미래", -30 * time.Second, "미래 30초 (시계 어긋남)"},
		{"먼 미래", -50 * time.Hour, "미래 2일 2시간 (시계 어긋남)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Age(c.d); got != c.want {
				t.Fatalf("Age(%v) = %q, 기대 %q", c.d, got, c.want)
			}
		})
	}
}

func TestSignalAgesKeepsAbsenceVisible(t *testing.T) {
	now := time.Date(2026, 8, 3, 14, 31, 0, 0, time.UTC)
	got := SignalAges(now, map[model.SignalKind]time.Time{
		model.SignalMCP:  now.Add(-12 * time.Second),
		model.SignalTool: now.Add(-3 * time.Minute),
	})

	// ① 종류를 빼지 않는다 — 빼면 "안 온 신호"와 "이 화면이 그 축을 안 본다"가 같아진다.
	if len(got) != 5 {
		t.Fatalf("신호 %d종, 기대 5종(prompt·tool·mcp·commit·push): %+v", len(got), got)
	}
	wantOrder := []model.SignalKind{
		model.SignalPrompt, model.SignalTool, model.SignalMCP, model.SignalCommit, model.SignalPush,
	}
	for i, k := range wantOrder {
		if got[i].Kind != k {
			t.Fatalf("%d번째가 %q, 기대 %q — 순서가 흔들리면 같은 화면을 다르게 읽는다", i, got[i].Kind, k)
		}
	}
	// ② 0값과 부재를 가른다.
	if got[0].Known || got[0].Age != "없음" {
		t.Fatalf("안 온 신호가 값이 있는 것처럼 나왔다: %+v", got[0])
	}
	if !got[2].Known || got[2].Age != "12초 전" {
		t.Fatalf("mcp 나이가 틀렸다: %+v", got[2])
	}
	if got[1].Age != "3분 전" {
		t.Fatalf("tool 나이가 틀렸다: %+v", got[1])
	}
	// ③ "죽었다"류 판정 어휘가 없다.
	for _, s := range got {
		if strings.Contains(s.Age, "죽") {
			t.Fatalf("생존 판정 어휘가 신호 표시에 들어갔다: %+v", s)
		}
	}
}

func TestDerivedLabel(t *testing.T) {
	now := time.Date(2026, 8, 3, 14, 31, 12, 0, time.UTC)
	obs := now.Add(-12 * time.Second)

	cases := []struct {
		name     string
		f        model.Freshness
		failures int
		want     string
	}{
		{"git 최신", model.Freshness{Source: "git", ObservedAt: obs}, 0,
			"(파생: git@14:31 · 12초 전)"},
		{"git 낡음", model.Freshness{Source: "git", ObservedAt: obs, Stale: true}, 0,
			"(파생: git@14:31 · 12초 전 · 낡음)"},
		{"실패 축 있음", model.Freshness{Source: "git", ObservedAt: obs, Stale: true}, 2,
			"(파생: git@14:31 · 12초 전 · 낡음 · 못 읽은 축 2)"},
		{"db", model.Freshness{Source: "db", ObservedAt: obs, Stale: true}, 0,
			"(파생: db@14:31 · 12초 전 · 낡음)"},
		// 표 밖 — 관측 시각이 아예 없는 경우. 시각을 지어내면 죽은 화면이 현재인 척한다.
		{"관측 시각 없음", model.Freshness{Source: "git"}, 0,
			"(파생: git · 관측 시각 없음 — 이 값이 언제 것인지 모른다)"},
		{"출처도 없음", model.Freshness{}, 0,
			"(파생: 미상 · 관측 시각 없음 — 이 값이 언제 것인지 모른다)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DerivedLabel(now, c.f, c.failures); got != c.want {
				t.Fatalf("DerivedLabel = %q, 기대 %q", got, c.want)
			}
		})
	}
}

func TestJudgeSnapshot(t *testing.T) {
	cases := []struct {
		name       string
		in, cur    string
		want       SnapshotState
		wantInText string
	}{
		{"같다", "abc123", "abc123", SnapshotCurrent, "같다"},
		{"다르다", "abc123", "def456", SnapshotStale, "이 숫자는 낡았다"},
		{"보관 해시 없음", "", "def456", SnapshotUnknown, "대조할 축이 없다"},
		{"현재 해시 없음", "abc123", "", SnapshotUnknown, "대조할 축이 없다"},
		{"둘 다 없음", "", "", SnapshotUnknown, "말할 수 없다"},
		// 표 밖 — 저장 과정에서 붙은 공백. 이것으로 낡음이 되면 상시 점등이 된다.
		{"공백만 다르다", " abc123\n", "abc123", SnapshotCurrent, "같다"},
		{"공백뿐인 해시", "   ", "abc123", SnapshotUnknown, "대조할 축이 없다"},
		// 표 밖 — 짧은 sha 는 접두가 같아도 같다고 보지 않는다(무엇을 대조했는지가 흐려진다).
		{"접두만 같다", "abc123", "abc1234567", SnapshotStale, "이 숫자는 낡았다"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := JudgeSnapshot(c.in, c.cur)
			if got.State != c.want {
				t.Fatalf("state = %q, 기대 %q (사유: %s)", got.State, c.want, got.Reason)
			}
			if got.Reason == "" {
				t.Fatal("사유가 비었다 — 공허한 판정은 왜 그런지 답하지 못한다")
			}
			if !strings.Contains(got.Reason, c.wantInText) {
				t.Fatalf("사유 %q 에 %q 가 없다", got.Reason, c.wantInText)
			}
			// unknown 이 "현재"로 읽히면 근거 없는 숫자가 근거 있는 척한다.
			if want := c.want != SnapshotCurrent; got.Warn() != want {
				t.Fatalf("Warn() = %v, 기대 %v (state=%s)", got.Warn(), want, got.State)
			}
		})
	}
}

func TestJudgeDisk(t *testing.T) {
	cases := []struct {
		name  string
		known bool
		pct   float64
		want  string
	}{
		{"넉넉", true, 62.5, "ok"},
		{"주의 경계 위", true, 15, "ok"},
		{"주의", true, 14.9, "warn"},
		{"임계 경계 위", true, 5, "warn"},
		{"임계", true, 4.9, "crit"},
		{"가득 참", true, 0, "crit"},
		// 표 밖 — 못 잰 것을 0% 로 접으면 그 플랫폼에서 상시 빨간불이 된다.
		{"못 쟀다", false, 0, "unknown"},
		{"못 쟀는데 값이 남아 있다", false, 62.5, "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := JudgeDisk(c.known, c.pct)
			if got.Level != c.want {
				t.Fatalf("level = %q, 기대 %q (%s)", got.Level, c.want, got.Text)
			}
			if got.Text == "" {
				t.Fatal("설명이 비었다")
			}
		})
	}
	// 0% 와 "못 쟀다"가 같은 문장을 내면 둘이 화면에서 구분되지 않는다.
	if JudgeDisk(true, 0).Text == JudgeDisk(false, 0).Text {
		t.Fatal("가득 참과 못 쟀음이 같은 문장이다")
	}
}

func TestRejectionDistribution(t *testing.T) {
	at := time.Date(2026, 8, 3, 14, 0, 0, 0, time.UTC)
	evals := []model.PickEval{
		{At: at, Picked: "a-1", Rejected: []model.Rejection{
			{Item: "b-1", Reason: "claimed", Detail: "세션 S1 가 선점했다"},
			{Item: "c-1", Reason: judge.RejectNotTop, Detail: "적격이지만 추천 2순위다"},
		}},
		{At: at.Add(time.Hour), Rejected: []model.Rejection{
			{Item: "b-1", Reason: "claimed", Detail: "세션 S1 가 선점했다"},
			{Item: "d-1", Reason: "after-unknown", Detail: "dep_item=x 의 상태를 조회하지 않았다"},
			{Item: "e-1", Reason: "", Detail: "코드가 없는 줄"},
			{Item: "f-1", Reason: "claimed", Detail: "세션 S2 가 선점했다"},
			{Item: "g-1", Reason: "claimed", Detail: "세션 S2 가 선점했다"},
		}},
	}
	st := RejectionDistribution(evals)

	if st.Evals != 2 || st.Picked != 1 || st.None != 1 {
		t.Fatalf("판정 집계가 틀렸다: %+v", st)
	}
	// not-top 은 거르는 축이 아니라 원장 완결용이라 분포에서 뺀다. 다만 버리지도 않는다.
	if st.NotTop != 1 {
		t.Fatalf("not-top = %d, 기대 1", st.NotTop)
	}
	if st.Total != 6 {
		t.Fatalf("탈락 줄 %d, 기대 6(not-top 제외)", st.Total)
	}
	if len(st.Reasons) != 3 {
		t.Fatalf("사유 %d종, 기대 3: %+v", len(st.Reasons), st.Reasons)
	}
	if st.Reasons[0].Reason != "claimed" || st.Reasons[0].Count != 4 {
		t.Fatalf("가장 흔한 사유가 틀렸다: %+v", st.Reasons[0])
	}
	if len(st.Reasons[0].Items) != 3 {
		t.Fatalf("예시가 %d건, 기대 3건 상한: %+v", len(st.Reasons[0].Items), st.Reasons[0].Items)
	}
	if st.Reasons[0].Example == "" {
		t.Fatal("상세가 비었다 — 사유 코드만으로는 무엇을 고쳐야 하는지 모른다")
	}
	// 표 밖 — 사유 코드가 빈 줄. 버리면 "사유를 안 남긴 판정"이 통계에서 사라진다.
	var sawEmpty bool
	for _, r := range st.Reasons {
		if r.Reason == "(사유 코드 없음)" {
			sawEmpty = true
		}
	}
	if !sawEmpty {
		t.Fatalf("사유 코드 없는 줄이 조용히 사라졌다: %+v", st.Reasons)
	}
	if !st.Since.Equal(at) || !st.Until.Equal(at.Add(time.Hour)) {
		t.Fatalf("구간이 틀렸다: %v ~ %v", st.Since, st.Until)
	}

	// 표 밖 — 입력이 아예 없다. "분포 0"과 "판정 기록 0"은 다른 사실이다.
	empty := RejectionDistribution(nil)
	if empty.Evals != 0 || len(empty.Reasons) != 0 || !empty.Since.IsZero() {
		t.Fatalf("빈 입력에서 값을 지어냈다: %+v", empty)
	}
}

func TestAfterLabel(t *testing.T) {
	cases := []struct {
		in   model.After
		want string
	}{
		{model.After{Item: "t5-x"}, "항목 t5-x"},
		{model.After{Job: "j-1"}, "잡 j-1"},
		{model.After{SHA: "0123456789abcdef0123"}, "커밋 0123456789ab…@landed"},
		// 표 밖 — 스키마 CHECK 를 우회해 들어온 빈 행. 침묵하면 의존이 없는 것처럼 보인다.
		{model.After{}, "빈 선행"},
	}
	for _, c := range cases {
		got := AfterLabel(c.in)
		if !strings.HasPrefix(got, c.want) {
			t.Fatalf("AfterLabel(%+v) = %q, 기대 접두 %q", c.in, got, c.want)
		}
	}
}

func TestJudgeAction(t *testing.T) {
	ok := ActionInput{Kind: ActionReclaim, Project: "p", Item: "t5-x", Reason: "세션이 창 밖이고 발자국도 없다"}

	cases := []struct {
		name   string
		in     ActionInput
		wantOK bool
		want   string
	}{
		{"정상 회수", ok, true, "성립"},
		{"정상 폐기", ActionInput{Kind: ActionDrop, Project: "p", Item: "t5-x", Reason: "설계에서 빠졌다"}, true, "성립"},
		{"사유 없음", ActionInput{Kind: ActionReclaim, Project: "p", Item: "t5-x"}, false, "사유가 비었다"},
		{"공백 사유", ActionInput{Kind: ActionReclaim, Project: "p", Item: "t5-x", Reason: "   "}, false, "사유가 비었다"},
		{"너무 짧은 사유", ActionInput{Kind: ActionReclaim, Project: "p", Item: "t5-x", Reason: "ㅇㅇ"}, false, "너무 짧다"},
		{"대상 없음", ActionInput{Kind: ActionDrop, Project: "p", Reason: "사유는 있다"}, false, "대상 항목이 비었다"},
		{"프로젝트 없음", ActionInput{Kind: ActionDrop, Item: "t5-x", Reason: "사유는 있다"}, false, "프로젝트가 비었다"},
		// 표 밖 — Tier B 버튼을 폼으로 만들어 보냈을 때. "모르는 것"으로 뭉개지 않는다.
		{"레인 정지", ActionInput{Kind: "lane-stop", Project: "p", Item: "x", Reason: "사유는 있다"}, false, "Tier B"},
		{"잡 우회", ActionInput{Kind: "bypass", Project: "p", Item: "x", Reason: "사유는 있다"}, false, "Tier B"},
		// 표 밖 — 아예 모르는 종류.
		{"모르는 종류", ActionInput{Kind: "delete-everything", Project: "p", Item: "x", Reason: "사유는 있다"},
			false, "모르는 쓰기 종류"},
		// 표 밖 — 경계값과 상한.
		{"사유 딱 4자", ActionInput{Kind: ActionDrop, Project: "p", Item: "x", Reason: "1234"}, true, "성립"},
		{"사유 초과", ActionInput{Kind: ActionDrop, Project: "p", Item: "x",
			Reason: strings.Repeat("가", reasonMax+1)}, false, "너무 길다"},
		{"항목 id 초과", ActionInput{Kind: ActionDrop, Project: "p", Item: strings.Repeat("x", 201),
			Reason: "사유는 있다"}, false, "너무 길다"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := JudgeAction(c.in)
			if got.OK != c.wantOK {
				t.Fatalf("OK = %v, 기대 %v (사유: %s)", got.OK, c.wantOK, got.Reason)
			}
			if got.Reason == "" {
				t.Fatal("사유가 비었다 — 성공도 왜 통과했는지 남겨야 검증이 된다")
			}
			if !strings.Contains(got.Reason, c.want) {
				t.Fatalf("사유 %q 에 %q 가 없다", got.Reason, c.want)
			}
		})
	}
}

func TestJudgeDropTarget(t *testing.T) {
	cases := []struct {
		state  model.ItemState
		wantOK bool
		want   string
	}{
		{model.ItemOpen, true, "열려 있다"},
		{model.ItemClaimed, true, "열려 있다"},
		{model.ItemDone, false, "이미 종료된 항목"},
		{model.ItemDropped, false, "이미 종료된 항목"},
		// 표 밖 — 스키마와 코드가 어긋났을 때. 통과시키면 이력이 조용히 거짓이 된다.
		{model.ItemState("landed"), false, "모르는 항목 상태"},
		{model.ItemState(""), false, "모르는 항목 상태"},
	}
	for _, c := range cases {
		got := JudgeDropTarget(c.state)
		if got.OK != c.wantOK {
			t.Fatalf("state=%q OK=%v, 기대 %v (%s)", c.state, got.OK, c.wantOK, got.Reason)
		}
		if !strings.Contains(got.Reason, c.want) {
			t.Fatalf("state=%q 사유 %q 에 %q 가 없다", c.state, got.Reason, c.want)
		}
	}
}

func TestNoticeTextIsAFixedList(t *testing.T) {
	if got := NoticeText("reclaim", "t5-x"); !strings.Contains(got, "선점을 회수했다") ||
		!strings.Contains(got, "t5-x") {
		t.Fatalf("회수 알림이 틀렸다: %q", got)
	}
	if got := NoticeText("drop", "t5-x"); !strings.Contains(got, "폐기했다") {
		t.Fatalf("폐기 알림이 틀렸다: %q", got)
	}
	// 표 밖 — 링크 하나로 아무 문장이나 띄우는 경로가 없어야 한다.
	for _, code := range []string{"", "아무 말", "<b>hi</b>", "RECLAIM"} {
		if got := NoticeText(code, "x"); got != "" {
			t.Fatalf("모르는 코드 %q 가 문장을 냈다: %q", code, got)
		}
	}
}

func TestClipStripsControlCharacters(t *testing.T) {
	got := Clip("a\nb\tc\x00d", 100)
	if strings.ContainsAny(got, "\n\t\x00") {
		t.Fatalf("제어문자가 남았다: %q", got)
	}
	if got := Clip(strings.Repeat("가", 10), 4); got != "가가가가…" {
		t.Fatalf("절단이 룬 단위가 아니다: %q", got)
	}
	if got := Clip("  hi  ", 100); got != "hi" {
		t.Fatalf("양끝 공백이 남았다: %q", got)
	}
}

// 스모크가 잡은 것: 0초짜리 미래가 "시계 어긋남"으로 렌더됐다.
// 서버가 시각을 찍고 렌더까지 가는 왕복만으로 몇 밀리초가 뒤집히므로,
// 그 경우에 가장 큰 경고가 붙으면 경고가 상시 점등돼 판별력이 0이 된다.
func TestSubSecondFutureIsNoiseNotSkew(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Millisecond, -300 * time.Millisecond, -999 * time.Millisecond} {
		if got := Age(d); strings.Contains(got, "어긋남") {
			t.Errorf("Age(%v) = %q — 초 미만의 음수는 잡음이지 시계 어긋남이 아니다", d, got)
		}
	}
	// 그래도 **진짜 어긋남은 여전히 말한다.** 접어 버리면 시계가 틀어진 머신의 세션이
	// "방금"으로 보여 가장 이상한 상태가 가장 정상으로 읽힌다.
	for _, d := range []time.Duration{-3 * time.Second, -2 * time.Hour} {
		if got := Age(d); !strings.Contains(got, "어긋남") {
			t.Errorf("Age(%v) = %q — 이 크기는 어긋남으로 말해야 한다", d, got)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────

// TestCloseDeclaredLabel 은 **화면이 세 상태를 세 문장으로 가르는지** 본다:
// 안 읽음 · 선언 없음 · 선언 있음. 이 셋을 둘로 접는 순간 조회가 죽은 화면이
// "이 항목은 깨끗하다"고 말하게 되고, 그 거짓말이 정확히 이 축이 막으려는 사고다.
func TestCloseDeclaredLabel(t *testing.T) {
	created := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	last := time.Date(2026, 8, 4, 23, 54, 37, 0, time.UTC)
	const sess = "01KZ785TQ8VWXYZ0123456789"

	cases := []struct {
		name    string
		d       model.CloseDeclaration
		read    bool
		created time.Time
		want    string
	}{
		{
			name: "못 읽었다 — 0으로 접지 않는다",
			d:    model.CloseDeclaration{}, read: false, created: created,
			want: CloseDeclUnread,
		},
		{
			// 표 밖: 못 읽었는데 값이 딸려 온 경우. 센티널이 이긴다 —
			// 못 읽은 조회가 낸 수는 수가 아니다.
			name: "못 읽었으면 값이 있어도 센티널이 이긴다",
			d:    model.CloseDeclaration{Done: 3, Last: last}, read: false, created: created,
			want: CloseDeclUnread,
		},
		{
			name: "읽었고 선언이 없다 — 아무 말도 안 한다",
			d:    model.CloseDeclaration{}, read: true, created: created,
			want: "",
		},
		{
			name: "done 1건 — 사고 사례의 실제 값",
			d: model.CloseDeclaration{
				Done: 1, Last: last, LastSession: sess, LastMode: "done",
			}, read: true, created: created,
			want: "종료 선언 최소 1건(done 1 · dropped 0) — 마지막 08-04 23:54 · mode=done · 세션 01KZ785TQ8VW…",
		},
		{
			// dropped 를 done 에 합치지 않는다 — 처방이 갈린다(done 은 "이미 랜딩됐을 수 있다",
			// dropped 는 "이미 버리기로 판정됐을 수 있다"). 실측 384건 중 76건이 dropped 다.
			name: "dropped 도 센다",
			d: model.CloseDeclaration{
				Dropped: 1, Last: last, LastSession: sess, LastMode: "dropped",
			}, read: true, created: created,
			want: "종료 선언 최소 1건(done 0 · dropped 1) — 마지막 08-04 23:54 · mode=dropped · 세션 01KZ785TQ8VW…",
		},
		{
			name: "둘 다 — 합은 Count 가 낸다",
			d: model.CloseDeclaration{
				Done: 1, Dropped: 2, Last: last, LastSession: sess, LastMode: "dropped",
			}, read: true, created: created,
			want: "종료 선언 최소 3건(done 1 · dropped 2) — 마지막 08-04 23:54 · mode=dropped · 세션 01KZ785TQ8VW…",
		},
		{
			// item 의 PK 가 (project, id) 라 지웠다 다시 만든 id 가 옛 이벤트를 물려받는다.
			// store 가 그 앵커를 **일부러 안 걸고** 호출자에게 넘긴다고 doc 에 적어 뒀다.
			name: "항목보다 옛 선언은 버린다 — 되살아난 id 의 유산이다",
			d: model.CloseDeclaration{
				Done: 1, Last: created.Add(-time.Hour), LastSession: sess, LastMode: "done",
			}, read: true, created: created,
			want: "",
		},
		{
			// service.closeDeclarations(pick.go:817)의 경계와 글자로 맞춘다 —
			// `!d.Last.After(created)`. 항목이 있어야 닫을 수 있으니 동시각은 이
			// 화신의 선언일 수 없다. 예전에는 여기가 Before 로만 걸러 동시각을
			// 남겨서, 같은 사실에 service 와 web 두 표면이 다른 답을 냈다 —
			// 이 표에 그 경계 갈래가 없어서 아무도 못 봤다.
			name: "생성과 같은 시각은 안 센다 — service 의 동시각 경계와 맞춘다",
			d: model.CloseDeclaration{
				Done: 1, Last: created, LastSession: sess, LastMode: "done",
			}, read: true, created: created,
			want: "",
		},
		{
			name: "항목 생성 시각을 모르면 앵커를 안 건다 — 없는 근거로 버리지 않는다",
			d: model.CloseDeclaration{
				Done: 1, Last: last, LastSession: sess, LastMode: "done",
			}, read: true, created: time.Time{},
			want: "종료 선언 최소 1건(done 1 · dropped 0) — 마지막 08-04 23:54 · mode=done · 세션 01KZ785TQ8VW…",
		},
		{
			// 표 밖 — store 는 Count>0 이면 Last·LastMode 를 반드시 채운다(event.go 의
			// `if d.LastMode == ""` 갈래). 화면은 그 전제에 기대지 않는다: 기대면 store 가
			// 바뀌는 날 빈칸이 사실인 척한다.
			name: "메타를 못 읽어도 수는 낸다",
			d:    model.CloseDeclaration{Done: 1}, read: true, created: created,
			want: "종료 선언 최소 1건(done 1 · dropped 0) — 마지막 시각 미상 · mode 미상 · 세션 미상",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CloseDeclaredLabel(c.d, c.read, c.created); got != c.want {
				t.Fatalf("CloseDeclaredLabel = %q, 기대 %q", got, c.want)
			}
		})
	}
}

// 문구가 **하한이라고 말하는지**를 따로 잠근다.
//
// ★ flushDeferred 는 트랜잭션이 물던 ctx 를 그대로 쓰고 LogEvent 는 쓰기 실패를 WARN 으로만
// 삼키므로, 클라이언트가 끊기면 행이 안 써진다. "정확히 N건"으로 쓰면 화면이 관측하지 않은
// 것을 단정하는 셈이고, 그 문구는 위 표의 want 문자열 안에 묻혀 조용히 지워질 수 있다.
func TestCloseDeclaredLabelSaysTheCountIsALowerBound(t *testing.T) {
	got := CloseDeclaredLabel(model.CloseDeclaration{
		Done: 1, Last: time.Date(2026, 8, 4, 23, 54, 0, 0, time.UTC),
		LastSession: "01KZ785TQ8VWXYZ0123456789", LastMode: "done",
	}, true, time.Time{})
	if !strings.Contains(got, "최소") {
		t.Fatalf("%q — 이 수는 하한인데 문구가 정확한 수인 척한다", got)
	}
}
