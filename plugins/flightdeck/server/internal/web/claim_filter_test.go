package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 섹션 ①은 **선점을 든 카드만** 낸다.
//
// ★ 이 화면이 답하는 질문이 바뀌었다: "누가 살아 있나"가 아니라 "어느 작업이 잡혀 있나"다.
// 선점은 `fd finish` 말고는 풀리는 길이 없어서 세션이 죽어도 남는다 — 그래서 이 화면은
// 생존을 말하지 않는다. 라벨과 0건 문구가 그 사실을 말해야 한다.
func TestNowSectionShowsOnlyClaimedCards(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	ctx := context.Background()

	held := f.openSession("cc-held", "쥔 쪽")
	f.claimOne(held.ID, "it-held")
	if err := f.svc.Beat(ctx, held.ID, model.SignalPrompt, nil); err != nil {
		t.Fatalf("신호 실패: %v", err)
	}

	// 선점 없이 **실제로 일하는** 세션. 지금 화면에서 사라지는 쪽이다.
	free := f.openSession("cc-free", "큐 밖")
	if err := f.svc.Beat(ctx, free.ID, model.SignalTool, nil); err != nil {
		t.Fatalf("신호 실패: %v", err)
	}

	_, html := f.get("")
	now := nowSectionOf(t, html)

	mustContain(t, html, "① 지금 — 잡혀 있는 작업",
		"라벨이 옛 이름이면 화면이 '생존'을 말하는 셈이 된다")
	// 카드는 짧은 id 로 찍는다(format.short). 좌표계를 화면에 맞춰 잰다.
	mustContain(t, now, short(held.ID), "선점을 든 카드가 없다")
	mustContain(t, now, "it-held", "무엇을 쥐고 있는지가 카드에 없다 — 이 화면의 필터가 화면에 안 나타난다")
	if strings.Contains(now, short(free.ID)) {
		t.Fatal("선점 없는 세션이 ①에 나왔다")
	}

	// ★ 접은 것을 침묵하지 않는다. 그리고 **조율에서 빠진 게 아니라는 사실**을 함께 말한다 —
	//   안 그러면 사람이 "저 세션은 아무도 안 본다"로 잘못 읽는다.
	mustContain(t, now, "선점 없는 세션 1건은 안 낸다", "접은 수를 침묵하면 '없다'와 '안 보여 준다'가 같아진다")
	mustContain(t, now, "겹침 처방은 그 세션들도 그대로 본다", "조율에서 빠졌다고 잘못 읽힌다")
}

// 자기 카드도 **예외가 없다.** 규칙을 하나로 둔다.
//
// cc 표류 진단은 안 죽는다 — 표류 배너는 거르지 않은 목록(DriftedTwins)에서 만들어진다.
func TestNowSectionGivesSelfNoException(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	me := f.openSession("cc-1", "나")

	_, html := f.get("?self=" + me.ID)
	now := nowSectionOf(t, html)

	if strings.Contains(now, short(me.ID)) {
		t.Fatal("선점 없는 자기 카드가 ①에 나왔다 — 규칙에 예외를 두지 않기로 했다")
	}
	mustContain(t, now, "잡혀 있는 작업 0건", "0건 문장이 없다")
	mustContain(t, now, "서버 장애가 아니다", "0건이 정상 상태라는 것을 화면이 말해야 한다")
}

// 활동 배지 — 선점은 있는데 조용한 카드가 이 화면의 주된 값어치다.
func TestClaimedCardCarriesActivityBadge(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	ctx := context.Background()

	quiet := f.openSession("cc-quiet", "조용")
	f.claimOne(quiet.ID, "it-quiet")
	// mcp 뿐인 카드다 — activityKinds 가 mcp 를 빼므로(format.go) "mcp 뿐"이 이 화면
	// 전 경로(서비스 → 보드 → 렌더)에서 "활동 없음"으로 나오는지를 여기서 잰다.
	if err := f.svc.Beat(ctx, quiet.ID, model.SignalMCP, nil); err != nil {
		t.Fatalf("신호 실패: %v", err)
	}

	busy := f.openSession("cc-busy", "바쁨")
	f.claimOne(busy.ID, "it-busy")
	if err := f.svc.Beat(ctx, busy.ID, model.SignalPrompt, nil); err != nil {
		t.Fatalf("신호 실패: %v", err)
	}

	_, html := f.get("")
	now := nowSectionOf(t, html)

	mustContain(t, now, "○ 활동 없음",
		"선점만 있고 조용한 카드를 안 가르면 이 화면의 주된 값어치가 사라진다(회수 후보)")
	mustContain(t, now, "● 활동", "일하는 카드의 배지가 없다")
}

// nowSectionOf 는 렌더된 페이지에서 **섹션 ①만** 잘라낸다.
//
// ★ 단정을 페이지 전체에 걸면 다른 절이 우연히 같은 문자열을 내는 순간 조용히
// 거짓 초록이 된다 — 이 패키지가 실제로 그것을 겪었다(lane_panel_test 머리말).
func nowSectionOf(t *testing.T, html string) string {
	t.Helper()
	i := strings.Index(html, `<section id="now">`)
	if i < 0 {
		t.Fatal("섹션 ①이 화면에 없다")
	}
	sec := html[i+1:]
	if j := strings.Index(sec, "<section"); j > 0 {
		sec = sec[:j]
	}
	return sec
}

// 창 밖인데 항목을 쥔 세션이 **①에 나온다.**
//
// ★ 이 시험이 "창을 이 섹션에 걸지 않는다"의 유일한 가드다. 창을 함께 걸면 회수가 가장
// 필요한 카드(오래 조용한데 쥐고 있는 것)가 정확히 창 때문에 사라진다.
//
// 그리고 그 줄은 git 파생을 **안 읽었다** — 창 밖까지 파생하면 카드당 git 호출 1~4회가
// 세션 수만큼 터진다. 0값과 미관측을 뭉개지 않는 것이 이 패키지의 규율이라, 화면이
// "안 읽었다"를 말해야 한다.
func TestNowSectionKeepsClaimHoldersOutsideTheWindow(t *testing.T) {
	f := newFixture(t).withRepo("feat")

	stale := f.openSession("cc-stale", "오래 조용")
	f.claimOne(stale.ID, "it-stuck")
	// 개시 시각을 창(기본 2시간) 밖으로 되돌린다.
	// ★ 신호는 안 심는다 — openSession + claimOne(pick) 둘 다 Beat 를 안 부르므로
	// 이 세션은 애초에 signal 행이 0건이다. opened_at 만으로 ListLive 의 창 판정과
	// ActivityOf 의 "활동 없음" 판정이 둘 다 완결된다.
	old := time.Now().UTC().Add(-12 * time.Hour).Format("2006-01-02T15:04:05.000000Z")
	if _, err := f.st.DB().Exec(`UPDATE session SET opened_at = ? WHERE id = ?`, old, stale.ID); err != nil {
		t.Fatalf("개시 시각 되돌리기 실패: %v", err)
	}

	_, html := f.get("")
	now := nowSectionOf(t, html)

	mustContain(t, now, "it-stuck",
		"창 밖 선점자가 ①에서 사라졌다 — 회수가 가장 필요한 카드가 정확히 창 때문에 안 보인다")
	mustContain(t, now, "파생 안 읽음",
		"파생을 안 읽은 줄을 그렇다고 안 말하면 0값과 미관측이 뭉개진다")
	mustContain(t, now, "○ 활동 없음", "12시간 조용한 카드가 활동 있음으로 나왔다")
}
