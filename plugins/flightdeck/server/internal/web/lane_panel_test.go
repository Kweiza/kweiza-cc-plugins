package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 랜딩 레인 절 — 화면이 줄을 그리고 사람이 거기서 회수한다.
//
// 좌표계는 이 패키지의 다른 시험과 같다: **렌더된 HTML 문자열**이다.
// 화면 모델을 단정하면 "구조체에는 있는데 템플릿이 안 찍는다"를 원리적으로 못 본다.

// clock 은 시험이 손으로 미는 시계다. 경과를 숫자로 단정하려면 시계가 서야 한다.
type clock struct {
	mu sync.Mutex
	at time.Time
}

func newClock(base time.Time) *clock { return &clock{at: base} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// laneFixture 는 줄에 둘을 세운 화면이다. 앞이 점유자, 뒤가 대기자다.
//
// 경과 셋을 **서로 다른 값**으로 벌려 둔다 — 같은 값이면 패널이 한 숫자를
// 세 칸에 찍어도 시험이 못 본다.
type laneFixture struct {
	*fixture
	clk            *clock
	holder, waiter string
}

func newLaneFixture(t *testing.T) *laneFixture {
	t.Helper()
	clk := newClock(time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC))
	f := newFixture(t, withClock(clk.now)).withRepo("feat")
	ctx := context.Background()

	holder := f.openSession("cc-holder", "앞").ID
	// 점유자가 3분 먼저 줄에 선다 → 대기 경과·획득 경과가 3분에서 시작한다.
	if _, err := f.svc.Land(ctx, service.LandInput{Project: testProject, SessionID: holder}); err != nil {
		t.Fatalf("점유자 줄 서기 실패: %v", err)
	}

	clk.advance(3 * time.Minute)
	waiter := f.openSession("cc-waiter", "뒤").ID
	if _, err := f.svc.Land(ctx, service.LandInput{Project: testProject, SessionID: waiter}); err != nil {
		t.Fatalf("대기자 줄 서기 실패: %v", err)
	}

	// 점유자만 신호를 한 번 더 남긴다 → 신호 나이가 대기 경과와 **갈린다**.
	// 안 갈라 두면 패널이 대기 경과를 신호 칸에 그대로 찍어도 시험이 초록이다.
	clk.advance(3 * time.Minute)
	if err := f.svc.Beat(ctx, holder, model.SignalTool, nil); err != nil {
		t.Fatalf("점유자 신호 실패: %v", err)
	}

	// ★ 대기자의 신호도 픽스처가 세운다. 예전에는 세션 열기가 찍던 mcp 비트가
	//   이 자리를 대신했는데, 열기는 도구 호출이 아니므로 더는 안 찍는다.
	//
	//   시각을 대기 경과(7분)와 **일부러 벌린다.** 같은 값이면 패널이 대기 경과를
	//   신호 칸에 그대로 찍어도 시험이 초록이다 — 바로 위에서 점유자 행에 대해
	//   막은 것과 같은 결함이 대기자 행에만 남는다.
	clk.advance(2 * time.Minute)
	if err := f.svc.Beat(ctx, waiter, model.SignalTool, nil); err != nil {
		t.Fatalf("대기자 신호 실패: %v", err)
	}

	// 최종 좌표: 점유자 대기·획득 = 10분 · 점유자 신호 = 4분 · 대기자 대기 = 7분 · 대기자 신호 = 2분
	//
	// ★ 신호 둘은 **픽스처가 직접 찍은 값**이다(전에는 세션 열기의 mcp 비트가
	//   대기자 쪽을 대신했다). 넷이 전부 다른 값인 것이 이 픽스처의 계약이다.
	//
	// ★ 대기·획득도 이제 이 시계로 찍힌다(store 가 시각을 주입받는다). 전에는 저장층이
	//   실시계를 찍어 화면과 갈렸고, 그래서 그 두 축은 한 번도 검증된 적이 없었다.
	//   다만 점유자의 두 값은 **구조적으로 같다** — 줄 서기와 취득이 같은 트랜잭션이다.
	//   값이 갈리는 경로는 TestLanePanelSplitsWaitingFromHeld 가 따로 세운다.
	clk.advance(2 * time.Minute)
	return &laneFixture{fixture: f, clk: clk, holder: holder, waiter: waiter}
}

// TestLanePanelDrawsEveryRowWithItsAxes 는 줄이 **전부** 나오고,
// 각 행이 판정에 필요한 축을 다 갖는지 본다.
//
// 생존 창으로 거르지 않는 것이 핵심이다 — 창 밖 세션이 맨 앞에서 막고 있는 상황이야말로
// 사람이 봐야 하는 상황이고, 거르면 화면에서 "줄이 비었는데 아무도 못 잡는다"가 된다.
func TestLanePanelDrawsEveryRowWithItsAxes(t *testing.T) {
	lf := newLaneFixture(t)

	code, html := lf.get("?project=" + testProject)
	if code != 200 {
		t.Fatalf("화면이 %d 다", code)
	}

	// 레인 절이 ④ 안에 있다는 것 자체는 ④에서 잰다(절의 존재는 절 하나 아래에서 못 잰다).
	mustContain(t, sectionOf(t, html, "landing"), "랜딩 레인",
		"레인 절이 ④에 없다 — 줄을 볼 자리가 없다")
	// 세션 id 는 ①의 카드에도 찍히므로 **줄 표 안에서** 잰다. 페이지 전체에 걸면
	// 줄 행이 통째로 빠져도 ①의 카드가 이 단정을 만족시킨다.
	mustContain(t, laneSectionOf(t, html), lf.holder, "점유자 세션이 줄 표에 없다")
	mustContain(t, laneSectionOf(t, html), lf.waiter, "대기자 세션이 줄 표에 없다 — 줄은 전부 내야 한다")

	// ★ 다섯을 전부 **레인 절 안에서** 잰다. 페이지 전체에 걸었다가 섹션 ①의 신호 배지가
	// 레인의 대기 경과인 척한 사고가 이 파일에서 실제로 났다(사연은 아래 laneSectionOf 에 있다).
	//
	// ★ 대기와 획득은 **칸의 표시까지** 못박는다. 이 픽스처에서 점유자의 두 값은 둘 다 10분이다 —
	// 줄 서기와 취득이 같은 트랜잭션(service.Land)이라 구조적으로 같은 시각이고, 값만으로는 두 칸을
	// 못 가른다. 그냥 `"10분 전"` 하나를 걸면 대기 칸이 두 단정을 다 만족시켜 **획득 축은 여전히
	// 미검증으로 남는다** — 이 항목이 고친 결함이 정확히 "그 축이 한 번도 검증된 적이 없다"이다.
	// 템플릿이 획득 칸만 <b> 로 감싸므로 그것을 근거로 가른다.
	//
	// 두 값이 **서로 다른 시각**이 되는 경로(대기하다 나중에 취득)는 값으로 갈리므로
	// TestLanePanelSplitsWaitingFromHeld 가 따로 본다. 이 시험은 같은 값일 때의 칸 배치를 지킨다.
	lane := laneSectionOf(t, html)
	mustContain(t, lane, "<td>10분 전</td>",
		"점유자의 대기 경과(10분)가 레인 절에 없다 — 줄 행 시각이 주입된 시계를 안 쓰면 이 칸이 실시계로 벌어진다")
	mustContain(t, lane, "<b>10분 전</b>",
		"점유자의 획득 경과(10분)가 없다 — 이 칸은 resource_hold.acquired_at 이라 대기 경과와 다른 표에서 온다")
	mustContain(t, lane, "<td>4분 전</td>",
		"점유자의 마지막 신호 나이(4분)가 없다 — 대기 경과를 그 칸에 찍은 것이다")
	mustContain(t, lane, "<td>7분 전</td>",
		"대기자의 대기 경과(7분)가 없다 — 점유자보다 3분 늦게 줄을 섰는데 그 차이가 화면에 없다")
	mustContain(t, lane, "<td>2분 전</td>",
		"대기자의 마지막 신호 나이(2분)가 없다 — 대기 경과(7분)가 아니다. 그 둘이 같은 값이면 이 단정은 아무것도 안 지킨다")

	// 점유자와 대기자가 화면에서 구분돼야 한다. 안 그러면 "누가 지금 쥐고 있나"를 못 읽는다.
	mustContain(t, lane, "lane-holder", "점유자 행이 표시로 구분되지 않는다")
}

// TestLanePanelSplitsWaitingFromHeld 는 대기 경과와 획득 경과가 **서로 다른 시각에서**
// 오는지 본다. 형제 시험의 픽스처는 둘이 같은 값이라(줄 서기와 취득이 같은 트랜잭션이다)
// 표시 자리로만 갈랐다 — 여기서는 값으로 가른다.
//
// 이 경로가 운영의 보통 경로다: 남이 쥐고 있는 동안 줄에 서 있다가 나중에 차례를 받는다.
// 그때 두 숫자는 **반드시 달라야 한다**. 같게 나오면 화면이 한 시각을 두 칸에 찍고 있는
// 것이고, 그러면 "얼마나 기다렸나"와 "얼마나 쥐고 있나"라는 서로 다른 판정 근거가
// 하나로 뭉개진다 — 회수 판정이 정확히 그 둘을 갈라 본다.
func TestLanePanelSplitsWaitingFromHeld(t *testing.T) {
	clk := newClock(time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC))
	f := newFixture(t, withClock(clk.now)).withRepo("feat")
	ctx := context.Background()

	first := f.openSession("cc-first", "먼저").ID
	if _, err := f.svc.Land(ctx, service.LandInput{Project: testProject, SessionID: first}); err != nil {
		t.Fatalf("선행 세션 줄 서기 실패: %v", err)
	}

	// 3분 뒤 뒷사람이 줄에 선다 — 이 순간이 대기 경과의 기준이다.
	clk.advance(3 * time.Minute)
	later := f.openSession("cc-later", "나중").ID
	if _, err := f.svc.Land(ctx, service.LandInput{Project: testProject, SessionID: later}); err != nil {
		t.Fatalf("후행 세션 줄 서기 실패: %v", err)
	}

	// 다시 4분 뒤 앞사람이 빠지고, 뒷사람이 **그때** 차례를 받는다(지연 부여 — 차례를 미는
	// 주체는 다음 호출이다). 여기서 획득 시각이 대기 시작보다 7분 늦게 찍힌다.
	clk.advance(4 * time.Minute)
	if _, err := f.svc.LandLeave(ctx, service.LandLeaveInput{
		Project: testProject, SessionID: first, Detail: "자리를 넘긴다"}); err != nil {
		t.Fatalf("선행 세션 이탈 실패: %v", err)
	}
	got, err := f.svc.Land(ctx, service.LandInput{Project: testProject, SessionID: later})
	if err != nil {
		t.Fatalf("후행 세션 취득 실패: %v", err)
	}
	if got.State != "turn" {
		t.Fatalf("후행 세션이 %q 다 — 앞이 빠졌으므로 차례여야 한다. 이 전제가 깨지면 아래 두 숫자는 아무것도 안 잰다", got.State)
	}

	// 렌더 시점을 다시 3분 민다(12:10). 뒷사람은 12:03 에 줄을 서서 12:07 에 받았으므로
	// 대기 = 7분, 획득 = 3분이다. **두 숫자가 달라야 한다** — 이 픽스처의 존재 이유다.
	clk.advance(3 * time.Minute)
	code, html := f.get("?project=" + testProject)
	if code != 200 {
		t.Fatalf("화면이 %d 다", code)
	}
	lane := laneSectionOf(t, html)
	mustContain(t, lane, "<td>7분 전</td>",
		"대기 경과(7분)가 없다 — 차례를 받은 시각으로 덮였다면 여기가 3분으로 찍힌다")
	mustContain(t, lane, "<b>3분 전</b>",
		"획득 경과(3분)가 없다 — 줄 선 시각을 획득 칸에 찍었다면 여기가 7분으로 찍힌다")
}

// TestLanePanelOffersReclaimPerRow 는 회수 대상이 **레인이 아니라 줄 행**임을 화면이
// 그대로 반영하는지 본다 — 대기 중 좀비도 같은 문법으로 빠져야 한다.
func TestLanePanelOffersReclaimPerRow(t *testing.T) {
	lf := newLaneFixture(t)

	_, html := lf.get("?project=" + testProject)
	lane := laneSectionOf(t, html)

	mustContain(t, lane, "actions/lane-release", "레인 회수 폼이 없다")
	// 줄 행 둘이 **각각** 회수 대상으로 골라져야 한다. 점유자만 고를 수 있으면
	// 대기 중 좀비를 빼는 길이 화면에 없다.
	//
	// ★ `value="1"` 은 항목 id 도 문장도 아닌 짧은 조각이라 다른 절의 어떤 <option>·
	//    <input> 이든 우연히 만족시킬 수 있다. **다만 지금은 아니다** — 실측했다:
	//    레인 폼의 option 을 통째로 없애면 페이지 전체에 걸어도 빨개진다(이 화면에서
	//    value="1"·value="2" 를 내는 자리가 여기뿐이다). 그러니 이 좁히기가 고친 것은
	//    지금 있는 거짓 초록이 아니라, **줄 행 말고 다른 것이 1·2 를 값으로 갖게 되는 날**
	//    조용히 생길 거짓 초록이다.
	for _, want := range []string{`value="1"`, `value="2"`} {
		mustContain(t, lane, want, "줄 행 "+want+" 을 회수 대상으로 고를 수 없다")
	}
	mustContain(t, lane, `value="1" data-revision="`, "점유 줄의 의미 지문이 없다")
	mustContain(t, lane, `value="2" data-revision="`, "대기 줄의 의미 지문이 없다")
}

func TestLaneRevisionHandlesSessionWithoutAnySignal(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	session := f.openSession("cc-no-signal", "신호 없는 줄")
	if _, err := f.svc.Land(context.Background(), service.LandInput{
		Project: testProject, SessionID: session.ID,
	}); err != nil {
		t.Fatalf("줄 서기 실패: %v", err)
	}

	code, html := f.get("?project=" + testProject)
	if code != http.StatusOK {
		t.Fatalf("화면이 %d 다", code)
	}
	lane := laneSectionOf(t, html)
	mustContain(t, lane, `data-revision="`, "신호 없는 줄에도 의미 지문이 없다")
	mustContain(t, lane, `|none|held|`, "신호 부재가 원시 지문에서 다른 상태와 구분되지 않는다")
}

// TestLaneReleaseFromScreenLeavesTheHumanJudgment 는 화면 회수가 **CLI 와 같은 함수**를
// 부르는지, 그리고 그 결과가 원장에 사람이 한 회수로 남는지 본다.
//
// 단정의 좌표계는 응답 코드가 아니라 **서버가 실제로 갖게 된 것**이다 — 303 을 내면서
// 줄이 그대로면 아무것도 안 한 것이다.
func TestLaneReleaseFromScreenLeavesTheHumanJudgment(t *testing.T) {
	lf := newLaneFixture(t)
	ctx := context.Background()

	rec := lf.post("/actions/lane-release", url.Values{
		"project": {testProject},
		"item":    {"2"}, // 대기 중인 줄 행 — 좀비도 같은 문법으로 빠져야 한다
		"reason":  {"대기자가 신호를 오래 안 남겨 줄에서 뺀다"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("회수가 %d 다 — 303 이어야 한다. 본문: %s", rec.Code, rec.Body.String())
	}

	// ① 줄에서 실제로 빠졌나.
	//
	// ★ 2026-08-12 자원 개편(Task 6) 정정 각주 — LaneView.Entries 는 이제
	// LaneView.Resources[i].Entries(자원별) 아래에 있다. 이 픽스처는 기본 자원 하나만
	// 쓰므로 전 자원을 순회해도 뜻은 그대로다.
	lane, err := lf.svc.LandingLane(ctx, testProject)
	if err != nil {
		t.Fatalf("줄 조회 실패: %v", err)
	}
	for _, rl := range lane.Resources {
		for _, e := range rl.Entries {
			if e.RowID == 2 {
				t.Fatalf("줄 행 2 가 아직 줄에 있다 — 303 만 내고 아무것도 안 했다")
			}
		}
	}

	// ② 판단이 **사람이 한 회수**로 남았나. actor 를 웹이 빈 문자열로 주는 것이
	//    이 문장을 고른다 — 뭔가를 채우면 불변의 기록에 다른 것이 적힌다.
	js, err := lf.st.ListJudgmentsByKind(ctx, testProject, model.JudgmentDecision, 20)
	if err != nil {
		t.Fatalf("판단 조회 실패: %v", err)
	}
	var found string
	for _, j := range js {
		if strings.Contains(j.Title, "랜딩 줄 행 회수") {
			found = j.Body
			break
		}
	}
	if found == "" {
		t.Fatal("회수 판단이 원장에 없다 — 회수는 사유가 남아야 회수다")
	}
	for _, want := range []string{
		"행위자: 대시보드(사람)",
		"대기자가 신호를 오래 안 남겨 줄에서 뺀다",
		"★ 회수는 자동 만료가 아니다",
	} {
		if !strings.Contains(found, want) {
			t.Errorf("판단 본문에 %q 가 없다:\n%s", want, found)
		}
	}
}

// TestLaneStopAndResumeStayRefusedButSayReleaseIsOpen 는 열린 것과 안 열린 것을
// 거절 문구가 **한 문장에서 다 말하는지** 본다. 뭉개면 사람이 "레인은 화면에서
// 못 만진다"로 읽고 실제로 열린 길을 안 쓴다.
func TestLaneStopAndResumeStayRefusedButSayReleaseIsOpen(t *testing.T) {
	for _, kind := range []ActionKind{"lane", "lane-stop", "lane-resume"} {
		t.Run(string(kind), func(t *testing.T) {
			v := JudgeAction(ActionInput{Kind: kind, Project: testProject, Item: "1", Reason: "사유가 있다"})
			if v.OK {
				t.Fatal("Tier B 인 정지/재개가 통과했다")
			}
			for _, want := range []string{"Tier B", "lane-release"} {
				if !strings.Contains(v.Reason, want) {
					t.Errorf("거절 사유에 %q 가 없다: %s", want, v.Reason)
				}
			}
		})
	}
}

// TestLanePanelSeparatesZeroFromUnread 는 "아무도 안 섰다"와 "질의가 안 돌았다"를
// 화면이 다른 문장으로 내는지 본다. 한 문장으로 접으면 사람이 빈 줄을 사실로 읽는다.
func TestLanePanelSeparatesZeroFromUnread(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.openSession("cc-1", "아무도 줄에 안 섰다")

	_, html := f.get("?project=" + testProject)
	landing := sectionOf(t, html, "landing")

	mustContain(t, landing, "랜딩 레인", "레인 절이 ④에 없다")
	mustContain(t, laneSectionOf(t, html), "줄이 비었다",
		"0건 문장이 없다 — 빈 표는 아무 말도 하지 않는다")
	// 0건 문장은 **자기 절이** 가진다. ④ 전체의 Empty 로 뭉개면 종료 항목 0건과
	// 줄 0건이 한 문장으로 접힌다.
	if strings.Contains(landing, "종료된 항목 0건 — 아직 아무 항목도 끝나거나 폐기되지 않았다. 줄이 비었다") {
		t.Fatal("레인 0건 문장이 ④ 전체 Empty 에 뭉개졌다")
	}
}

// laneSectionOf 는 렌더된 페이지에서 **레인 절만** 잘라낸다.
//
// ★ 이 헬퍼가 있는 이유가 이 파일의 교훈이다. 단정을 페이지 전체에 걸면 다른 절이
// 우연히 같은 문자열을 내는 순간 그 단정이 조용히 거짓 초록이 된다 — 이 파일이
// 실제로 그것을 들고 있었다(섹션 ①의 mcp 신호 배지가 레인의 대기 경과인 척했고,
// ①을 접자마자 드러났다). 레인 축은 레인 절 안에서 재라.
//
// ★ 끝을 **다음 <h3>(스냅숏)**에서 끊는다. 레인은 새 <section> 이 아니라 ④ 안쪽
// <h3> 이라(절 개수가 여섯으로 고정이다) ④ 끝까지 자르면 스냅숏 표가 딸려 온다.
// 그 표도 판정 시각을 같은 "N분 전" 어법으로 찍으므로, 레인의 시각 단정이 스냅숏
// 칸으로 만족되는 길이 열린다 — 이 파일이 이미 한 번 치른 값과 같은 부류다.
func laneSectionOf(t *testing.T, html string) string {
	t.Helper()
	sec := sectionOf(t, html, "landing")
	i := strings.Index(sec, "랜딩 레인")
	if i < 0 {
		t.Fatal("레인 절이 ④ 안에 없다")
	}
	sec = sec[i:]
	if j := strings.Index(sec, "<h3"); j > 0 {
		sec = sec[:j]
	}
	return sec
}
