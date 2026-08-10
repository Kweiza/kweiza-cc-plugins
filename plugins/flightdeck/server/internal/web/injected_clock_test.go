package web

import (
	"testing"
	"time"
)

// 저장층이 스스로 찍던 두 시각이 **주입된 시계를 탄다**.
//
// ★ 이 파일이 있는 이유. 앞선 작업이 레인의 두 시각(줄 서기·자원 획득)을 주입 가능하게
// 만들었는데, 같은 부류가 저장층에 더 남아 있었다: `session.opened_at`(Tx.OpenSession)과
// `claim.at`(Tx.ClaimItem). 둘 다 화면이 읽는 값인데 주입 경로가 **아예 없었다** —
// 그래서 시험은 화면 시계로, 저장층은 실시계로 재고 있었고, 그 어긋남은 두 좌표계를
// 나란히 놓는 자리에서만 드러난다. 화면은 그런 자리를 안 만든다.
//
// 시험이 지금까지 그 자리를 `UPDATE session SET opened_at = …` 로 우회했다는 것이
// 증거다(render_test·claim_filter_test). 우회는 저장층이 무엇을 찍는지에 대해
// **아무것도 말하지 않는다** — SQL 로 덮어쓴 값을 화면이 읽었을 뿐이다.
//
// 두 축을 **서로 다른 값**으로 벌려 둔다. 같은 값이면 화면이 한 시각을 두 칸에 찍어도
// 시험이 초록이다(레인의 대기·획득이 정확히 그 모양이었다).
func TestClaimedCardTimesRideTheInjectedClock(t *testing.T) {
	clk := newClock(time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC))
	f := newFixture(t, withClock(clk.now)).withRepo("feat")

	sess := f.openSession("cc-1", "트랙2") // 12:00 개시 — session.opened_at
	clk.advance(9 * time.Minute)
	f.claimOne(sess.ID, "it-clock") // 12:09 선점 — claim.at
	clk.advance(4 * time.Minute)    // 렌더 시점 12:13

	_, html := f.get("")
	now := nowSectionOf(t, html)

	// ① 개시는 **경과**로 찍힌다(카드의 `열림 …`). 저장층이 실시계를 찍으면 주입된 지금
	//    (2026-08-05 12:13)보다 한참 뒤가 되어 "미래 … (시계 어긋남)"으로 떨어진다.
	mustContain(t, now, "열림 13분 전",
		"세션 개시 경과가 주입된 시계를 안 탄다 — 저장층이 실시계를 찍으면 이 칸이 미래가 된다")

	// ② 선점은 **절대 시각**으로 찍힌다(회수 폼의 `…부터`). 좌표계가 ①과 달라서
	//    한쪽이 다른 쪽을 대신할 수 없다 — 그것이 이 두 단정을 함께 두는 이유다.
	mustContain(t, now, "08-05 12:09부터",
		"선점 시각이 주입된 시계를 안 탄다 — claim.at 은 session.opened_at 과 다른 표에서 온다")
}

// 창 판정도 같은 컬럼을 읽는다.
//
// ★ 이 시험이 없으면 위 시험 하나로는 모자란다. `session.opened_at` 은 화면의 표기
// (열림 N)만 만드는 것이 아니라 **ListLive 의 창 절단**을 통째로 정한다. 표기는 맞는데
// 절단이 실시계를 보면 "창 밖 N건"이 조용히 0이 되고, 그 침묵은 사고가 나기 전까지
// 사실과 구분되지 않는다.
func TestWindowCutRidesTheInjectedClockToo(t *testing.T) {
	clk := newClock(time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC))
	f := newFixture(t, withClock(clk.now)).withRepo("feat")

	f.openSession("cc-old", "창 밖으로 밀려날 세션") // 12:00 개시
	clk.advance(3 * time.Hour)              // 창(기본 2시간)을 넘긴다 — 15:00
	f.openSession("cc-new", "창 안 세션")

	_, html := f.get("")
	// ★ SQL 로 opened_at 을 되돌리지 않았다. 시계만 밀었다 — 그것이 이 시험의 전부다.
	mustContain(t, nowSectionOf(t, html), "창 밖 1건",
		"창 절단이 주입된 시계를 안 탄다 — 시계를 3시간 밀었는데 아무도 창 밖으로 안 나갔다")
}
