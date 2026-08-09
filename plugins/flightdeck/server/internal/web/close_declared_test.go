package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// 이 파일이 재는 것은 **두 시점**이다.
//
// 롤백된 finish 는 화면에서 두 번 읽혀야 한다. 한 번은 회수 폼에서(되돌릴 수 없는 행위를
// 저지르기 직전), 또 한 번은 큐 표에서(회수한 **뒤**). 회수 폼의 줄은 회수하는 순간
// 사라지는데 **사고는 그 다음에 난다** — open 이 된 그 항목을 pick 이 나이순 1순위로 냈다.
// 큐 표가 두 시점을 잇는 유일한 표면이다(설계 §4-⑤).
//
// ★ 이벤트를 **손으로 심는다.** 실물 원장에는 open/claimed + item.finish 조합이 지금 0건이라
// (실측: item.finish 384건 중 항목이 done 305 · dropped 75 · 행 없음 3, open/claimed 은 0)
// 실제 롤백으로는 이 자리를 못 밟는다. "롤백돼도 이벤트가 흘러간다"는 전제 자체는
// store 층이 실물 경로(선점 없는 finish)로 민다 — 여기서 겹쳐 밟지 않는다.

// declared 는 선점된 항목 하나에 **롤백된 종료 선언**을 심은 픽스처다.
func declared(t *testing.T, itemID string) (*fixture, string) {
	t.Helper()
	f := newFixture(t).withRepo("feat")
	sess := f.openSession("cc-1", "트랙2")
	f.claimOne(sess.ID, itemID) // 항목 등록 + 선점 (제목은 "<id> 제목")

	// payload 의 item·mode 키는 service/finish.go 가 실제로 싣는 것과 같다.
	// 여기서 어긋나면 store 가 그 행을 조용히 안 세고 시험이 초록으로 거짓말한다.
	if err := f.st.TryLogEvent(context.Background(), "item.finish", testProject, sess.ID,
		map[string]any{"item": itemID, "mode": "done", "count": 0, "bytes": 10300}); err != nil {
		t.Fatalf("종료 선언 이벤트 심기 실패: %v", err)
	}
	return f, sess.ID
}

// queueTableOf 는 ③ 안에서 **항목 표만** 잘라낸다 — 탈락 사유 분포 절과 폐기 폼 앞에서 끊는다.
//
// ★ 페이지 전체에 단정을 걸면 다른 절이 우연히 같은 문자열을 내는 순간 조용히 거짓 초록이
// 된다. 이 패키지가 실제로 그 값을 치렀다(claim_filter_test·lane_panel_test 의 머리말).
func queueTableOf(t *testing.T, html string) string {
	t.Helper()
	i := strings.Index(html, `<section id="queue">`)
	if i < 0 {
		t.Fatal("섹션 ③이 화면에 없다")
	}
	sec := html[i:]
	j := strings.Index(sec, "탈락 사유 분포")
	if j < 0 {
		t.Fatal("③의 항목 표 끝(탈락 사유 분포 절)을 못 찾았다 — 이 헬퍼의 전제가 깨졌다")
	}
	return sec[:j]
}

// dropFormOf 는 폐기 폼 하나만 잘라낸다. 큐 표와 **다른 표면**이라 따로 잰다 —
// 시점 B 의 올바른 처분 경로가 폐기다.
func dropFormOf(t *testing.T, html string) string {
	t.Helper()
	i := strings.Index(html, `action="actions/drop`)
	if i < 0 {
		t.Fatal("폐기 폼이 화면에 없다")
	}
	sec := html[i:]
	j := strings.Index(sec, "</form>")
	if j < 0 {
		t.Fatal("폐기 폼이 안 닫혔다")
	}
	return sec[:j]
}

// ─────────────────────────── 시점 A — 롤백 직후(claimed) ───────────────────────────

// 회수 폼은 **되돌릴 수 없는 행위를 저지르는 마지막 한 줄**이다. 그 줄이 침묵하면
// 사람은 "놀고 있는 선점"을 회수하고, 그 다음 pick 이 그것을 1순위로 낸다.
func TestReclaimFormNamesTheRolledBackFinish(t *testing.T) {
	f, sess := declared(t, "it-rolled")

	_, html := f.get("")
	now := nowSectionOf(t, html)

	// 전제 — 그 항목이 회수 대상으로 실제로 올라와 있다. 안 올라와 있으면 아래 단정들은
	// "표기가 붙었다"가 아니라 "줄이 애초에 없다"를 통과시킨다.
	mustContain(t, now, `<option value="it-rolled">it-rolled ←`,
		"전제 실패 — 선점이 회수 폼에 없다")

	mustContain(t, now, "종료 선언 최소 1건",
		"회수 폼이 롤백된 마무리를 침묵한다 — 이 줄이 그것을 말할 마지막 자리다")
	mustContain(t, now, "done 1 · dropped 0",
		"mode 별 수가 없다 — done 과 dropped 는 처방이 갈린다")
	mustContain(t, now, "· 세션 "+short(sess),
		"누가 선언했는지가 없다 — 그 세션의 판단이 죽은 본문을 되짚을 유일한 실마리다")
	mustContain(t, now, "it-rolled 제목",
		"회수 폼 줄에 제목이 없다 — id 만으로는 무엇을 회수하는지 사람이 모른다")

	// 그리고 이 수가 정확한 수인 척하지 않는다(문구는 ③의 폐기 폼 꼬리에 있다).
	mustContain(t, html, "그 수는 하한이다",
		"원장에 안 써진 마무리가 있을 수 있다는 사실이 화면 어디에도 없다")
}

// ─────────────────────────── 시점 B — 회수 후(open) ───────────────────────────

// 사고는 **여기서** 났다. 회수 폼의 줄은 사라졌고 항목은 open 이 됐다.
func TestQueueTableKeepsTheDeclarationAfterReclaim(t *testing.T) {
	f, _ := declared(t, "it-rolled")

	rec := f.post("/actions/reclaim", url.Values{
		"project": {testProject}, "item": {"it-rolled"},
		"reason": {"세션이 사라졌고 발자국도 없다 — 근거 축을 보고 회수한다"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("회수 status = %d, 기대 303\n%s", rec.Code, rec.Body.String())
	}

	_, html := f.get("")

	// 전제 — 회수가 실제로 됐다(항목은 open·무점유). 회수 폼의 줄이 사라진 것으로 잰다.
	mustNotContain(t, nowSectionOf(t, html), `<option value="it-rolled">it-rolled ←`,
		"전제 실패 — 회수했는데 선점이 남아 있다")

	table := queueTableOf(t, html)
	mustContain(t, table, "it-rolled", "회수된 항목이 큐 표에서 사라졌다 — 회수는 폐기가 아니다")
	mustContain(t, table, "종료 선언 최소 1건",
		"회수 뒤 큐 표가 침묵한다 — 회수 폼의 줄은 사라졌고 사고는 바로 이 다음에 난다")
	mustContain(t, table, "mode=done",
		"무엇으로 닫으려 했는지가 없다 — done 과 dropped 는 처방이 갈린다")

	// 폐기 폼도 **같은 문장**을 얻는다. 이 시점의 올바른 처분 경로가 폐기인데
	// 그 select 가 id 만 내면 사람은 같은 사실을 두 번째 표면에서 다시 못 읽는다.
	drop := dropFormOf(t, html)
	mustContain(t, drop, "it-rolled", "폐기 폼에 그 항목이 없다")
	mustContain(t, drop, "it-rolled 제목", "폐기 폼 줄에 제목이 없다")
	mustContain(t, drop, "종료 선언 최소 1건", "폐기 폼이 id 만 낸다 — 두 번째 표면이 침묵한다")
}

// ─────────────────────────── 못 읽음은 0이 아니다 ───────────────────────────

func TestUnreadCloseDeclarationAxisIsNotFoldedIntoZero(t *testing.T) {
	f, _ := declared(t, "it-unread")

	// 원장을 통째로 감춘다 — 이 축의 조회를 실패시키는 유일한 길이고,
	// service 층 시험이 같은 관용구를 쓴다(finish_partial_test.go 의 RENAME TO … _hidden).
	if _, err := f.st.DB().Exec(`ALTER TABLE event RENAME TO event_hidden`); err != nil {
		t.Fatalf("원장 감추기 실패: %v", err)
	}

	_, html := f.get("")
	table := queueTableOf(t, html)

	mustContain(t, table, "종료 선언 축을 못 읽었다",
		"못 읽은 축을 '선언 없음'으로 접었다 — 0값과 미관측을 뭉갠 것이다")
	mustNotContain(t, table, "종료 선언 최소",
		"못 읽었는데 수를 지어냈다")
	// 그리고 큐 표는 통째로 안 죽는다 — 한 축의 실패가 화면을 지우면 사람이 추측으로 돌아간다.
	mustContain(t, table, "it-unread", "한 축을 못 읽었다고 항목 줄이 사라졌다")
}

// 이 시험이 "실패를 pan.Err 에 안 담는다"는 판단의 유일한 가드다.
//
// queuePanel 은 `len(pan.Items) == 0 && pan.Err == ""` 일 때만 "큐가 비었다"를 쓴다.
// 종료 선언 조회 실패를 pan.Err 에 담으면 **원장 한 축을 못 읽은 것이 큐가 비었다는
// 참인 문장까지 지운다.**
func TestUnreadAxisDoesNotEraseTheEmptyQueueSentence(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.openSession("cc-1", "트랙2")
	if _, err := f.st.DB().Exec(`ALTER TABLE event RENAME TO event_hidden`); err != nil {
		t.Fatalf("원장 감추기 실패: %v", err)
	}

	_, html := f.get("")
	mustContain(t, queueTableOf(t, html), "큐가 비었다 — 열린 항목도 선점된 항목도 없다",
		"종료 선언 축을 못 읽은 것이 '큐가 비었다'는 참인 문장을 지웠다 — 그래서 이 실패는 pan.Err 이 아니다")
}
