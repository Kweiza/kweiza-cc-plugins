package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// 쓰기 둘의 소비자 좌표계는 **응답 상태·Location 헤더·되돌아온 화면의 문자열**이다.
// store 를 직접 들여다보고 끝내면 "DB 는 바뀌었는데 화면은 옛 상태를 보여준다"를 못 본다.

// resolveFrom 은 Location 을 **브라우저처럼** 푼다.
//
// ★ Location 을 요청 URL 로 그대로 쓰면 안 된다. 이 서버의 리다이렉트는 상대 참조라
// (`../?notice=…`) 요청 경로로 성립하지 않는다. 브라우저는 문서 URL 을 기준으로 그것을
// 풀고, url.ResolveReference 가 그 규칙(RFC 3986)의 구현이다.
//
// ★ 접두를 일부러 씌운다. 접두 뒤 배포에서 착지가 접두 **안**인지가 이 축의 전부이고,
// 접두 없이 풀면 그 사실을 영영 안 재게 된다.
func resolveFrom(t *testing.T, docPath, loc string) *url.URL {
	t.Helper()
	const prefix = "/dcp-dev-board"
	if loc == "" {
		t.Fatal("Location 이 비었다")
	}
	base, err := url.Parse("http://fd.example" + prefix + docPath)
	if err != nil {
		t.Fatalf("문서 URL 파싱 실패(%q): %v", docPath, err)
	}
	ref, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("Location %q 파싱 실패: %v", loc, err)
	}
	got := base.ResolveReference(ref)
	if !strings.HasPrefix(got.Path, prefix+"/") {
		t.Fatalf("Location %q 가 %q 에 착지한다 — 접두 %q 밖이다. "+
			"쓰기는 됐는데 화면으로 못 돌아온다", loc, got.Path, prefix)
	}
	return got
}

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
	//   (폐기 폼에도 같은 id 의 option 이 있으므로 **회수 폼의 줄 모양**으로 단정한다.
	//   그 줄 모양에 더해 ① 안에서 재면 두 표면이 두 축으로 갈린다)
	_, html := f.get("")
	if !strings.Contains(nowSectionOf(t, html), `<option value="t5-a" data-revision="`) ||
		!strings.Contains(nowSectionOf(t, html), `">t5-a ←`) {
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
	// 그리고 선점은 그대로 살아 있다 — 회수 폼(①)에서 잰다.
	_, html := f.get("")
	mustContain(t, nowSectionOf(t, html), `<option value="t5-a" data-revision="`, "거절됐는데 선점이 사라졌다")
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
	landed := resolveFrom(t, "/actions/reclaim", loc)
	if landed.Path != "/dcp-dev-board/" || !strings.Contains(landed.RawQuery, "notice=reclaim") {
		t.Fatalf("Location = %q → %q — 화면으로 되돌리지 않았다", loc, landed)
	}

	// 리다이렉트를 따라간 화면이 소비자 좌표계다. 접두를 벗긴 자리가 서버가 보는 경로다.
	req := httptest.NewRequest(http.MethodGet, "/"+strings.TrimPrefix(landed.RequestURI(), "/dcp-dev-board/"), nil)
	rec2 := httptest.NewRecorder()
	f.h.ServeHTTP(rec2, req)
	html := rec2.Body.String()

	// 알림은 <main> 밖(header 바로 아래)이라 절이 없다 — 페이지 전체가 맞다.
	mustContain(t, html, "선점을 회수했다", "회수 알림이 화면에 없다")
	// 회수 행위 자체가 추가 전용 판단으로 남는다(설계 §4).
	// 질의가 없으면 ⑥이 "최근 판단"을 내므로 그 절에서 잰다 — 판단은 ⑤(막힘·요청)에도
	// 같은 note 템플릿으로 찍히니 페이지 전체에 걸면 어느 절이 말했는지가 안 정해진다.
	search := sectionOf(t, html, "search")
	mustContain(t, search, "선점 회수: t5-a", "회수가 판단(decision)으로 안 남았다")
	mustContain(t, search, "[decision]", "판단 종류가 decision 이 아니다")
	mustContain(t, search, "근거 다섯 축을 보고 회수한다", "회수 사유가 원장에 안 남았다")
	// 그리고 선점은 사라지고(① 회수 폼) 항목은 큐에 다시 열린다(③ 표).
	mustNotContain(t, nowSectionOf(t, html), `<option value="t5-a">t5-a ←`, "회수했는데 선점이 남아 있다")
	mustContain(t, sectionOf(t, html, "queue"), "t5-a", "항목 자체가 큐에서 사라졌다 — 회수는 폐기가 아니다")
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

	landed := resolveFrom(t, "/actions/drop", rec.Header().Get("Location"))
	req := httptest.NewRequest(http.MethodGet, "/"+strings.TrimPrefix(landed.RequestURI(), "/dcp-dev-board/"), nil)
	rec2 := httptest.NewRecorder()
	f.h.ServeHTTP(rec2, req)
	html := rec2.Body.String()

	// 알림은 절 밖이라 페이지 전체가 맞다.
	mustContain(t, html, "항목을 폐기했다", "폐기 알림이 화면에 없다")
	// 폐기된 항목은 ④ 랜딩 이력의 종료 표로 간다.
	mustContain(t, sectionOf(t, html, "landing"), "dropped", "폐기 상태가 ④ 이력에 없다")
	// 사유는 두 자리에 남는다 — ④의 사유 칸과 ⑥의 판단. 각각을 그 자리에서 잰다.
	mustContain(t, sectionOf(t, html, "landing"), "설계에서 빠진 축이라",
		"폐기 사유가 ④ 이력에 없다 — 사유 없는 폐기는 되짚을 수 없다")
	mustContain(t, sectionOf(t, html, "search"), "항목 폐기: t5-c", "폐기가 판단(decision)으로 안 남았다")
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

	// 그리고 원래 결과가 안 덮였다. ④의 이력 표가 그 사실을 내는 자리다 —
	// "done" 은 세 글자라 페이지 전체에 걸면 어느 절의 어느 낱말이든 만족시킨다.
	_, html := f.get("")
	mustContain(t, sectionOf(t, html, "landing"), "done", "종료 결과가 폐기로 덮였다")
}

// 폐기는 그 항목의 선점을 **사유째로** 닫는다.
//
// ★ 관측점이 released_at 이 아니라 force_reason 인 이유를 적어 둔다. 안 적으면 다음
// 사람이 "released_at 이 더 직관적인데"라며 단정을 옮기고, 그 순간 이 시험이 아무것도
// 안 잡게 된다.
//
// released_at 은 이 고침 **이전에도** 찍혔다 — SetItemState 가 종료 상태에서 살아 있는
// 선점을 스스로 반납하기 때문이다(store/item.go:562-577). 그래서 released_at 만 보면
// 세 구현이 전부 초록이다: ⓐ ForceReleaseClaim 을 아예 안 부르는 것, ⓑ SetItemState
// **뒤에** 부르는 것(그 자리에서는 UPDATE 가 0행이라 NFLiveClaim 으로 빠져 조용히
// 무동작이 된다), ⓒ **앞에** 부르는 올바른 것. 셋을 가르는 관측점은 force_reason 뿐이다.
//
// ★ 그리고 이 사실은 화면에도 있어야 한다. force_reason 은 지금 어느 표면도 안 읽으므로
// (page.go·dashboard.gohtml 전수 확인) 원장에만 두면 선점을 잃은 쪽에서는 그 사실이
// 영영 안 보인다. 그래서 판단 본문의 한 줄을 함께 단정한다.
func TestDropClosesTheClaimWithTheDropReason(t *testing.T) {
	const reason = "설계에서 빠진 축이라 이 항목은 성립하지 않는다 — 버린다"

	cases := []struct {
		name     string
		item     string
		claim    bool   // 폐기 전에 선점을 걸까
		wantRow  bool   // claim 행이 있어야 하나
		wantWhy  string // 기대하는 claim.force_reason
		wantLine string // 판단 본문이 화면에서 말해야 하는 한 줄
	}{
		{
			name: "선점된 항목", item: "t5-e", claim: true, wantRow: true, wantWhy: reason,
			wantLine: "선점: 살아 있던 선점을 이 폐기와 같은 트랜잭션에서 함께 닫았다",
		},
		{
			name: "선점 없는 항목", item: "t5-f", claim: false, wantRow: false, wantWhy: "",
			wantLine: "선점: 폐기 시점에 살아 있는 선점이 없었다",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t).withRepo("feat")
			sess := f.openSession("cc-1", "트랙2")
			if c.claim {
				f.claimOne(sess.ID, c.item)
			} else {
				f.addItem(c.item, c.item+" 제목", nil, nil)
			}

			rec := f.post("/actions/drop", url.Values{
				"project": {testProject}, "item": {c.item}, "reason": {reason},
			})
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, 기대 303 — 선점 유무가 폐기의 성패를 가르면 안 된다\n%s",
					rec.Code, rec.Body.String())
			}

			ctx := context.Background()
			it, err := f.st.GetItem(ctx, testProject, c.item)
			if err != nil {
				t.Fatalf("항목 조회 실패: %v", err)
			}
			// ★ 순서 가드다. ForceReleaseClaim 은 `UPDATE item SET state='open' …
			//   AND state='claimed'` 를 함께 치므로(store/item.go:857), 폐기를 먼저 찍고
			//   그 뒤에 회수하면 방금 닫은 항목이 다시 열린다.
			if it.State != model.ItemDropped {
				t.Fatalf("항목 상태 = %q, 기대 dropped — 선점 회수가 폐기를 되돌렸다", it.State)
			}

			claim, cerr := f.st.GetClaim(ctx, testProject, c.item)
			switch {
			case !c.wantRow:
				if !errors.Is(cerr, store.ErrNotFound) {
					t.Fatalf("선점 없는 항목에 claim 행이 생겼다(err=%v) — 폐기가 없던 선점을 만들었다", cerr)
				}
			case cerr != nil:
				t.Fatalf("선점 조회 실패: %v", cerr)
			default:
				if claim.ReleasedAt == nil {
					t.Fatal("폐기했는데 claim 행이 released_at = NULL 로 남았다 — " +
						"claim 표에는 만료 컬럼이 없어 그 세션은 닫힌 항목의 선점을 영영 쥔다")
				}
				if claim.ForceReason != c.wantWhy {
					t.Fatalf("claim.force_reason = %q, 기대 %q — 선점을 왜 끊었는지가 원장에 없다",
						claim.ForceReason, c.wantWhy)
				}
			}

			// 그리고 그 사실을 화면이 말한다 — 판단 본문이므로 ⑥에서 잰다.
			_, html := f.get("")
			mustContain(t, sectionOf(t, html, "search"), c.wantLine,
				"선점을 어떻게 처리했는지가 판단에 안 남았다 — force_reason 은 어느 화면도 안 읽는다")
		})
	}
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
