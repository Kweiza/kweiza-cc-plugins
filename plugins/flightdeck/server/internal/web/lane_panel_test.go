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
	//   대기 경과는 여전히 실시계라 화면에서 안 맞는다 —
	//   후속 `fd-lane-timestamps-ignore-injected-clock`.
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

	mustContain(t, html, "랜딩 레인", "레인 절이 화면에 없다 — 줄을 볼 자리가 없다")
	mustContain(t, html, lf.holder, "점유자 세션이 줄 표에 없다")
	mustContain(t, html, lf.waiter, "대기자 세션이 줄 표에 없다 — 줄은 전부 내야 한다")

	// ★★ 여기 있던 분 단위 단정 넷 중 둘은 **거짓 초록이었다.**
	//
	// `mustContain(html, "10분 전")`(점유자 대기 경과)와 짝인 `"10분"`(획득 경과)은
	// 레인 표가 아니라 **섹션 ①의 카드**가 내던 값이었다(`mcp 10분 전` 배지).
	// 실측으로 확인했다: 레인 절 안의 값은 `4시간 3분 전` 이고 `10분 전` 은 그 절에 없다.
	//
	// 원인은 형식이 아니라 **시계 어긋남**이다. `internal/store/landing.go` 가
	// `EnqueuedAt`·`AcquiredAt` 을 `nowStamp()`(실시계)로 찍어 주입된 시계를 안 쓰는데,
	// 페이지는 픽스처 시계로 그린다. 운영에서는 양쪽 다 실시계라 **영향이 없고**,
	// 시험에서만 갈린다 — 그래서 이 두 축은 지금까지 한 번도 검증된 적이 없다.
	//
	// ★ 픽스처에 선점을 넣어 ①의 카드를 되살리는 방식으로 통과시키지 마라.
	//   그것은 거짓 초록을 그대로 복원하는 것이다. 후속 항목:
	//   `fd-lane-timestamps-ignore-injected-clock`.
	//
	// 그때까지, 검증 가능한 축은 **레인 절 안에서** 본다. 그리고 신호 나이(4분)는
	// 서비스 시계로 찍히므로 지금도 참이다 — 대기 경과를 신호 칸에 찍는 오류는 이것이 잡는다.
	lane := laneSectionOf(t, html)
	mustContain(t, lane, "4분 전", "점유자의 마지막 신호 나이(4분)가 없다 — 대기 경과를 그 칸에 찍은 것이다")
	mustContain(t, lane, "2분 전", "대기자의 마지막 신호 나이(2분)가 없다 — 대기 경과(7분)가 아니다. 그 둘이 같은 값이면 이 단정은 아무것도 안 지킨다")
	if strings.Contains(lane, "10분 전") {
		t.Fatal("레인 절이 10분 전을 찍는다 — 위 결함이 고쳐졌다는 뜻이다. " +
			"이 갈래를 지우고 분 단위 단정 넷을 되살려라(fd-lane-timestamps-ignore-injected-clock)")
	}

	// 점유자와 대기자가 화면에서 구분돼야 한다. 안 그러면 "누가 지금 쥐고 있나"를 못 읽는다.
	mustContain(t, html, "lane-holder", "점유자 행이 표시로 구분되지 않는다")
}

// TestLanePanelOffersReclaimPerRow 는 회수 대상이 **레인이 아니라 줄 행**임을 화면이
// 그대로 반영하는지 본다 — 대기 중 좀비도 같은 문법으로 빠져야 한다.
func TestLanePanelOffersReclaimPerRow(t *testing.T) {
	lf := newLaneFixture(t)

	_, html := lf.get("?project=" + testProject)

	mustContain(t, html, "actions/lane-release", "레인 회수 폼이 없다")
	// 줄 행 둘이 **각각** 회수 대상으로 골라져야 한다. 점유자만 고를 수 있으면
	// 대기 중 좀비를 빼는 길이 화면에 없다.
	for _, want := range []string{`value="1"`, `value="2"`} {
		mustContain(t, html, want, "줄 행 "+want+" 을 회수 대상으로 고를 수 없다")
	}
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
	lane, err := lf.svc.LandingLane(ctx, testProject)
	if err != nil {
		t.Fatalf("줄 조회 실패: %v", err)
	}
	for _, e := range lane.Entries {
		if e.RowID == 2 {
			t.Fatalf("줄 행 2 가 아직 줄에 있다 — 303 만 내고 아무것도 안 했다")
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

	mustContain(t, html, "랜딩 레인", "레인 절이 없다")
	mustContain(t, html, "줄이 비었다", "0건 문장이 없다 — 빈 표는 아무 말도 하지 않는다")
	// 0건 문장은 **자기 절이** 가진다. ④ 전체의 Empty 로 뭉개면 종료 항목 0건과
	// 줄 0건이 한 문장으로 접힌다.
	if strings.Contains(html, "종료된 항목 0건 — 아직 아무 항목도 끝나거나 폐기되지 않았다. 줄이 비었다") {
		t.Fatal("레인 0건 문장이 ④ 전체 Empty 에 뭉개졌다")
	}
}

// laneSectionOf 는 렌더된 페이지에서 **레인 절만** 잘라낸다.
//
// ★ 이 헬퍼가 있는 이유가 이 파일의 교훈이다. 단정을 페이지 전체에 걸면 다른 절이
// 우연히 같은 문자열을 내는 순간 그 단정이 조용히 거짓 초록이 된다 — 이 파일이
// 실제로 그것을 들고 있었다(섹션 ①의 mcp 신호 배지가 레인의 대기 경과인 척했고,
// ①을 접자마자 드러났다). 레인 축은 레인 절 안에서 재라.
func laneSectionOf(t *testing.T, html string) string {
	t.Helper()
	i := strings.Index(html, "랜딩 레인")
	if i < 0 {
		t.Fatal("레인 절이 화면에 없다")
	}
	sec := html[i:]
	if j := strings.Index(sec, "<section"); j > 0 {
		sec = sec[:j]
	}
	return sec
}
