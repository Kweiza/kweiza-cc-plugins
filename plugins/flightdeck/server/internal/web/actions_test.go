package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 쓰기 둘의 소비자 좌표계는 **응답 상태·Location 헤더·되돌아온 화면의 문자열**이다.
// store 를 직접 들여다보고 끝내면 "DB 는 바뀌었는데 화면은 옛 상태를 보여준다"를 못 본다.

// claimed 는 선점된 항목 하나가 있는 픽스처를 만든다.
func claimed(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t).withRepo("feat")
	sess := f.openSession("cc-1", "트랙2")
	f.addItem("t5-a", "회수 시험용 항목", []string{"internal/web/"}, nil)
	if _, err := f.svc.Pick(context.Background(), service.PickInput{
		Project: testProject, SessionID: sess.ID, ItemID: "t5-a",
	}); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	// ★ 전제 단정 — 선점이 실제로 걸렸는가. 안 걸렸으면 아래 회수 시험은
	//   "회수됐다"가 아니라 "애초에 없었다"를 통과시킨다.
	//   (폐기 폼에도 같은 id 의 option 이 있으므로 **회수 폼의 줄 모양**으로 단정한다)
	_, html := f.get("")
	if !strings.Contains(html, `<option value="t5-a">t5-a ←`) {
		t.Fatalf("전제 실패 — 선점이 회수 대상으로 화면에 없다")
	}
	return f, sess.ID
}

func TestReclaimWithoutReasonIsRefusedAndClaimSurvives(t *testing.T) {
	f, _ := claimed(t)

	for _, reason := range []string{"", "   ", "ㅇ"} {
		rec := f.post("/actions/reclaim", url.Values{
			"project": {testProject}, "item": {"t5-a"}, "reason": {reason},
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("사유 %q 로 status = %d, 기대 400", reason, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "사유") {
			t.Fatalf("거절 사유가 응답에 없다: %q", rec.Body.String())
		}
	}
	// 그리고 선점은 그대로 살아 있다.
	_, html := f.get("")
	mustContain(t, html, `<option value="t5-a">t5-a ←`, "거절됐는데 선점이 사라졌다")
}

func TestReclaimReleasesClaimAndLeavesJudgment(t *testing.T) {
	f, _ := claimed(t)

	rec := f.post("/actions/reclaim", url.Values{
		"project": {testProject}, "item": {"t5-a"},
		"reason": {"창 밖 세션이 쥐고 있고 발자국도 없다 — 근거 다섯 축을 보고 회수한다"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, 기대 303\n%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/?") || !strings.Contains(loc, "notice=reclaim") {
		t.Fatalf("Location = %q — 화면으로 되돌리지 않았다", loc)
	}

	// 리다이렉트를 따라간 화면이 소비자 좌표계다.
	req := httptest.NewRequest(http.MethodGet, loc, nil)
	rec2 := httptest.NewRecorder()
	f.h.ServeHTTP(rec2, req)
	html := rec2.Body.String()

	mustContain(t, html, "선점을 회수했다", "회수 알림이 화면에 없다")
	// 회수 행위 자체가 추가 전용 판단으로 남는다(설계 §4).
	mustContain(t, html, "선점 회수: t5-a", "회수가 판단(decision)으로 안 남았다")
	mustContain(t, html, "[decision]", "판단 종류가 decision 이 아니다")
	mustContain(t, html, "근거 다섯 축을 보고 회수한다", "회수 사유가 원장에 안 남았다")
	// 그리고 선점은 사라지고 항목은 다시 열린다.
	mustNotContain(t, html, `<option value="t5-a">t5-a ←`, "회수했는데 선점이 남아 있다")
	mustContain(t, html, "t5-a", "항목 자체가 큐에서 사라졌다 — 회수는 폐기가 아니다")
}

func TestReclaimOfItemWithoutLiveClaimIs404(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.openSession("cc-1", "트랙2")
	f.addItem("t5-b", "선점되지 않은 항목", nil, nil)

	rec := f.post("/actions/reclaim", url.Values{
		"project": {testProject}, "item": {"t5-b"}, "reason": {"살아 있는 선점이 없다"},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, 기대 404 — 없는 대상과 서버 결함은 처방이 다르다\n%s",
			rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), "없다", "무엇이 없는지를 안 말했다")
}

func TestDropMarksItemDroppedWithReason(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.openSession("cc-1", "트랙2")
	f.addItem("t5-c", "폐기 시험용", nil, nil)

	rec := f.post("/actions/drop", url.Values{
		"project": {testProject}, "item": {"t5-c"},
		"reason": {"설계에서 빠진 축이라 이 항목은 성립하지 않는다"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, 기대 303\n%s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, rec.Header().Get("Location"), nil)
	rec2 := httptest.NewRecorder()
	f.h.ServeHTTP(rec2, req)
	html := rec2.Body.String()

	mustContain(t, html, "항목을 폐기했다", "폐기 알림이 화면에 없다")
	mustContain(t, html, "dropped", "폐기 상태가 이력에 없다")
	mustContain(t, html, "설계에서 빠진 축이라", "폐기 사유가 화면에 없다 — 사유 없는 폐기는 되짚을 수 없다")
	mustContain(t, html, "항목 폐기: t5-c", "폐기가 판단(decision)으로 안 남았다")
}

func TestDropRefusesAlreadyClosedItem(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	sess := f.openSession("cc-1", "트랙2")
	f.addItem("t5-d", "이미 끝난 항목", nil, nil)
	if _, err := f.svc.Pick(context.Background(), service.PickInput{
		Project: testProject, SessionID: sess.ID, ItemID: "t5-d",
	}); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	if _, err := f.svc.Finish(context.Background(), service.FinishInput{
		Project: testProject, SessionID: sess.ID, ItemID: "t5-d",
		Outcome: model.ItemDone, Title: "끝", Body: "무엇을 왜 했는지 여기 적었다",
	}); err != nil {
		t.Fatalf("종료 실패: %v", err)
	}

	rec := f.post("/actions/drop", url.Values{
		"project": {testProject}, "item": {"t5-d"}, "reason": {"다시 폐기해 본다"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, 기대 400 — 종료된 항목을 다시 닫으면 이력이 조용히 거짓이 된다\n%s",
			rec.Code, rec.Body.String())
	}
	mustContain(t, rec.Body.String(), "이미 종료된 항목", "거절 사유가 없다")

	// 그리고 원래 결과가 안 덮였다.
	_, html := f.get("")
	mustContain(t, html, "done", "종료 결과가 폐기로 덮였다")
}

func TestGetOnActionPathIs404(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.openSession("cc-1", "트랙2")

	req := httptest.NewRequest(http.MethodGet, "/actions/reclaim", nil)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, 기대 404 또는 405 — 쓰기 경로는 GET 으로 안 열린다", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/없는경로", nil)
	rec = httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, 기대 404", rec.Code)
	}
	mustContain(t, rec.Body.String(), "대시보드는 / 한 장이다", "왜 없는지를 안 말했다")
}
