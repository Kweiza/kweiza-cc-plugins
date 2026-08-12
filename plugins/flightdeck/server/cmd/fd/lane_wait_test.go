package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/api"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/web"
)

// `fd lane wait` 시험 — 브리프(task-10-brief.md)의 넷 그대로.
//
// ★ judgeLaneWait 는 순수 함수라 서버 없이 표로 찌른다(①·②). 폴링 루프(runLaneWaitWith)는
// 접착만 하므로 그것을 재는 시험은 **실물 서버**(이 패키지의 seam 방식)에 laneWaitDeps 로
// 시계를 주입해 sleep 을 없앤다(③·④) — time.Sleep 을 실제로 부르면 시험이 몇 초씩 느려지고,
// 그 몇 초가 CI 에서 수십 배로 불어난다.

// ─────────────────────────────────────────────────────────────────────────────
// ① 차례 판정 — 자원별 표
// ─────────────────────────────────────────────────────────────────────────────

func TestLaneWaitTurnJudgement(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	sig3mAgo := now.Add(-3 * time.Minute)

	cases := []struct {
		name      string
		view      service.LaneView
		myRow     int64
		mySession string
		wantTurn  bool
		wantLine  string
	}{
		{
			name: "맨 앞이고 아무도 안 쥐었다 — 통과",
			view: service.LaneView{Resources: []service.ResourceLane{
				{Resource: "landing", Entries: []service.LaneEntry{{RowID: 1, SessionID: "me"}}},
			}},
			myRow: 1, mySession: "me",
			wantTurn: true, wantLine: "landing: 1번째",
		},
		{
			name: "내가 이미 쥐었다 — front 위치와 무관하게 재진입 통과",
			view: service.LaneView{Resources: []service.ResourceLane{
				{Resource: "landing",
					Holder: &service.LaneHolder{SessionID: "me"},
					Entries: []service.LaneEntry{
						{RowID: 2, SessionID: "other"},
						{RowID: 1, SessionID: "me"},
					}},
			}},
			myRow: 1, mySession: "me",
			wantTurn: true, wantLine: "landing: 2번째",
		},
		{
			name: "남이 쥐었다 — 막힘, 점유자·신호 나이가 실린다",
			view: service.LaneView{Resources: []service.ResourceLane{
				{Resource: "landing",
					Holder: &service.LaneHolder{SessionID: "s-front", LastSignalAt: &sig3mAgo},
					Entries: []service.LaneEntry{
						{RowID: 5, SessionID: "s-front"},
						{RowID: 9, SessionID: "me"},
					}},
			}},
			myRow: 9, mySession: "me",
			wantTurn: false, wantLine: "landing: 2번째·점유 s-front·신호 3분 전",
		},
		{
			name: "아무도 안 쥐었지만 내가 맨 앞이 아니다 — 막힘, 앞 줄 행으로 표시",
			view: service.LaneView{Resources: []service.ResourceLane{
				{Resource: "r2",
					Entries: []service.LaneEntry{
						{RowID: 3, SessionID: "frontS", EnqueuedAt: now.Add(-1 * time.Minute)},
						{RowID: 9, SessionID: "me"},
					}},
			}},
			myRow: 9, mySession: "me",
			wantTurn: false, wantLine: "r2: 2번째·앞 줄 행 3(frontS)·신호 없음",
		},
		{
			name: "두 자원 — 하나는 막히고 하나는 통과, 전체는 막힘",
			view: service.LaneView{Resources: []service.ResourceLane{
				{Resource: "r1",
					Holder: &service.LaneHolder{SessionID: "s-front", LastSignalAt: &sig3mAgo},
					Entries: []service.LaneEntry{
						{RowID: 5, SessionID: "s-front"},
						{RowID: 9, SessionID: "me"},
					}},
				{Resource: "r2",
					Entries: []service.LaneEntry{{RowID: 9, SessionID: "me"}}},
			}},
			myRow: 9, mySession: "me",
			wantTurn: false, wantLine: "r1: 2번째·점유 s-front·신호 3분 전 | r2: 1번째",
		},
		{
			name: "내 것이 아닌 자원은 판정·화면 둘 다에서 빠진다",
			view: service.LaneView{Resources: []service.ResourceLane{
				{Resource: "other", Entries: []service.LaneEntry{{RowID: 99, SessionID: "stranger"}}},
				{Resource: "landing", Entries: []service.LaneEntry{{RowID: 1, SessionID: "me"}}},
			}},
			myRow: 1, mySession: "me",
			wantTurn: true, wantLine: "landing: 1번째",
		},
		{
			// ★ 브리프의 계약은 "전부 통과면 myTurn=true" 인데, 그것을 공집합에 문자 그대로
			// 적용하면(빈 집합의 전칭명제는 항상 참이다) 내 줄 행이 어디에도 없을 때도
			// "차례다"가 된다. 회수(fd lane release)로 내 행이 사라진 뒤 폴링이 그 자리다 —
			// 그때 "차례다"를 화면에 내면 거짓 안내다(취득 POST 는 안전하게 waiting 으로
			// 되돌아오지만, 그 전에 사람이 읽는 줄이 이미 거짓말을 한 뒤다). 그래서 haveMine
			// 가드를 더한다 — brief 문면의 정정 각주.
			name: "내 행이 어디에도 없다 — 통과할 것이 없으므로 차례가 아니다",
			view: service.LaneView{Resources: []service.ResourceLane{
				{Resource: "landing", Entries: []service.LaneEntry{{RowID: 5, SessionID: "other"}}},
			}},
			myRow: 999, mySession: "me",
			wantTurn: false, wantLine: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := judgeLaneWait(c.view, c.myRow, c.mySession, now)
			if got.myTurn != c.wantTurn {
				t.Errorf("myTurn = %v, 기대 %v (line=%q)", got.myTurn, c.wantTurn, got.line)
			}
			if got.line != c.wantLine {
				t.Errorf("line = %q, 기대 %q", got.line, c.wantLine)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ② stale 폴백 — 신호(LastSignalAt) 우선, nil 이면 대기 시작(EnqueuedAt) 나이로
// ─────────────────────────────────────────────────────────────────────────────

func TestLaneWaitStaleUsesSignalThenEnqueueAge(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)

	t.Run("신호가 있으면 신호 나이를 쓴다", func(t *testing.T) {
		sig := now.Add(-5 * time.Minute)
		view := service.LaneView{Resources: []service.ResourceLane{
			{Resource: "r1",
				Holder: &service.LaneHolder{SessionID: "s1", LastSignalAt: &sig},
				Entries: []service.LaneEntry{
					{RowID: 2, SessionID: "s1", EnqueuedAt: now.Add(-2 * time.Hour)},
					{RowID: 9, SessionID: "me"},
				}},
		}}
		got := judgeLaneWait(view, 9, "me", now)
		if got.staleFor != 5*time.Minute {
			t.Errorf("staleFor = %v, 기대 5분(신호 나이)", got.staleFor)
		}
		if got.staleWho != "s1(r1)" {
			t.Errorf("staleWho = %q, 기대 %q", got.staleWho, "s1(r1)")
		}
		if got.staleRow != 2 {
			t.Errorf("staleRow = %d, 기대 2(점유자의 줄 행)", got.staleRow)
		}
	})

	t.Run("신호가 nil 이면 점유자 줄 행의 EnqueuedAt 나이로 접는다", func(t *testing.T) {
		view := service.LaneView{Resources: []service.ResourceLane{
			{Resource: "r1",
				Holder: &service.LaneHolder{SessionID: "s1", LastSignalAt: nil},
				Entries: []service.LaneEntry{
					{RowID: 2, SessionID: "s1", EnqueuedAt: now.Add(-40 * time.Minute)},
					{RowID: 9, SessionID: "me"},
				}},
		}}
		got := judgeLaneWait(view, 9, "me", now)
		if got.staleFor != 40*time.Minute {
			t.Errorf("staleFor = %v, 기대 40분(EnqueuedAt 폴백)", got.staleFor)
		}
		if got.staleRow != 2 {
			t.Errorf("staleRow = %d, 기대 2", got.staleRow)
		}
	})

	t.Run("점유자가 없어도(앞 줄 행만) 신호 nil 이면 EnqueuedAt 나이로 접는다", func(t *testing.T) {
		view := service.LaneView{Resources: []service.ResourceLane{
			{Resource: "r1",
				Entries: []service.LaneEntry{
					{RowID: 3, SessionID: "front2", EnqueuedAt: now.Add(-90 * time.Minute)},
					{RowID: 9, SessionID: "me"},
				}},
		}}
		got := judgeLaneWait(view, 9, "me", now)
		if got.staleFor != 90*time.Minute {
			t.Errorf("staleFor = %v, 기대 90분", got.staleFor)
		}
		if got.staleWho != "front2(r1)" {
			t.Errorf("staleWho = %q, 기대 %q", got.staleWho, "front2(r1)")
		}
		if got.staleRow != 3 {
			t.Errorf("staleRow = %d, 기대 3", got.staleRow)
		}
	})

	t.Run("막는 자원이 둘이면 가장 오래 막는 쪽을 고른다", func(t *testing.T) {
		sig10m := now.Add(-10 * time.Minute)
		view := service.LaneView{Resources: []service.ResourceLane{
			{Resource: "r1",
				Holder: &service.LaneHolder{SessionID: "s1", LastSignalAt: &sig10m},
				Entries: []service.LaneEntry{
					{RowID: 2, SessionID: "s1"},
					{RowID: 9, SessionID: "me"},
				}},
			{Resource: "r2",
				Entries: []service.LaneEntry{
					{RowID: 4, SessionID: "s2", EnqueuedAt: now.Add(-50 * time.Minute)},
					{RowID: 9, SessionID: "me"},
				}},
		}}
		got := judgeLaneWait(view, 9, "me", now)
		if got.staleFor != 50*time.Minute {
			t.Errorf("staleFor = %v, 기대 50분(r2 가 더 오래 막는다)", got.staleFor)
		}
		if got.staleWho != "s2(r2)" {
			t.Errorf("staleWho = %q, 기대 %q", got.staleWho, "s2(r2)")
		}
		if got.staleRow != 4 {
			t.Errorf("staleRow = %d, 기대 4", got.staleRow)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// ③ 취득은 정확히 필요할 때만 — 서버 핸들러 호출 계수로 단정
// ─────────────────────────────────────────────────────────────────────────────

// installLaneWaitCounter 는 h.srv 를 **같은 주소**로 다시 띄우되, landingPath 로 오는
// **취득**(mode=acquire) POST 만 센다. h.up() 의 재기동 갈래와 같은 방식(listenOn)이다 —
// 이 패키지의 seam 시험 방식(sse_seam_test.go·land_seam_test.go)을 그대로 베낀 것이지
// 가짜 핸들러가 아니다.
//
// ★ mode 로 가른다 — landingPath 는 acquire·report·leave 셋이 공유하는 한 경로다
// (wire.go 의 landReq 주석). 앞사람이 반납(report, `--ok`)하는 호출도 같은 경로를 타므로
// 경로만 보면 "내가 시도한 취득"과 "남이 반납한 것"이 안 갈린다 — 이 시험이 재려는 축은
// 전자뿐이다.
//
// ★ **반드시 앞사람이 레인을 쥔 뒤에 부른다.** 먼저 부르면 그 land 호출 자체가 이 계수기를
// 통과하며 1을 더해, "차례가 아닌 동안 POST 가 0번"이라는 단정의 기준선이 흔들린다.
func installLaneWaitCounter(t *testing.T, h *harness) *int32 {
	t.Helper()
	h.srv.Close()

	var posts int32
	quiet := quietLogger()
	real := buildHandler(h.svc, web.New(h.svc, web.WithLogger(quiet)), api.Options{Log: quiet})
	counting := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == landingPath {
			body, rerr := io.ReadAll(r.Body)
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			if rerr == nil && bytes.Contains(body, []byte(`"mode":"acquire"`)) {
				atomic.AddInt32(&posts, 1)
			}
		}
		real.ServeHTTP(w, r)
	})
	srv := httptest.NewUnstartedServer(counting)
	ln, err := listenOn(h.env["FD_URL"])
	if err != nil {
		t.Fatalf("같은 주소로 못 띄웠다: %v", err)
	}
	srv.Listener = ln
	srv.Start()
	h.srv = srv
	t.Cleanup(srv.Close)
	return &posts
}

func TestLaneWaitAcquiresOnlyWhenItLooksLikeMyTurn(t *testing.T) {
	h := newHarness(t)

	// 앞사람이 먼저 레인을 쥔다 — 내 land 는 waiting 으로 시작해야 폴링 루프를 탄다.
	// 계수기를 달기 **전**이다(위 installLaneWaitCounter 의 ★).
	if code, out := h.runAs("cc-lw-front", "land"); code != 0 {
		t.Fatalf("전제가 깨졌다 — 앞사람이 레인을 못 잡았다(%d):\n%s", code, out)
	}
	posts := installLaneWaitCounter(t, h)

	env := map[string]string{}
	for k, v := range h.env {
		env[k] = v
	}
	env["CLAUDE_CODE_SESSION_ID"] = "cc-lw-wait"
	app := newApp(envOf(env), quietLogger(), h.home, strings.NewReader(""))

	clock := time.Now().UTC()
	var sleeps int
	deps := laneWaitDeps{
		now: func() time.Time { return clock },
		sleep: func(d time.Duration) {
			clock = clock.Add(d)
			sleeps++
			switch sleeps {
			case 1, 2:
				// 아직 앞사람이 쥐고 있다 — 이 시점까지 POST 는 최초 acquire 1건뿐이어야 한다.
				if got := atomic.LoadInt32(posts); got != 1 {
					t.Errorf("폴링 %d회차 전 POST 누적이 %d다 — 차례가 아닌데 취득을 시도했다", sleeps, got)
				}
			case 3:
				// 앞사람이 반납한다 — 다음 조회부터 내 차례로 보여야 한다.
				if code, out := h.runAs("cc-lw-front", "land", "--ok"); code != 0 {
					t.Fatalf("앞사람 반납이 %d 로 끝났다:\n%s", code, out)
				}
			}
		},
	}

	var out bytes.Buffer
	code := app.runLaneWaitWith(context.Background(), nil, &out, deps)
	if code != 0 {
		t.Fatalf("lane wait 가 %d 로 끝났다(기대 0 — 차례가 와야 한다):\n%s", code, out.String())
	}
	if got := atomic.LoadInt32(posts); got != 2 {
		t.Fatalf("landing POST 누적이 %d다 — 기대 2(최초 acquire 1 + 차례로 보인 뒤 재확인 1). "+
			"조회만으로 취득하거나(0), 조회마다 취득을 시도했다면(3 이상) 이 축이 깨진다", got)
	}
	mustContain(t, "lane wait stdout", out.String(), "네 차례다")
}

// ─────────────────────────────────────────────────────────────────────────────
// ④ 타임아웃 — 종료코드 1(land 계열과 같은 규약)
// ─────────────────────────────────────────────────────────────────────────────

func TestLaneWaitTimeoutExitsOne(t *testing.T) {
	h := newHarness(t)
	if code, out := h.runAs("cc-lw-front-to", "land"); code != 0 {
		t.Fatalf("전제가 깨졌다 — 앞사람이 레인을 못 잡았다(%d):\n%s", code, out)
	}

	env := map[string]string{}
	for k, v := range h.env {
		env[k] = v
	}
	env["CLAUDE_CODE_SESSION_ID"] = "cc-lw-wait-to"
	app := newApp(envOf(env), quietLogger(), h.home, strings.NewReader(""))

	// ★ 앞사람이 계속 쥐고 있으므로 myTurn 은 영영 안 온다. sleep 이 시계만 밀 뿐 실제로는
	// 안 잔다 — 타임아웃을 재려고 몇 분을 실제로 기다리지 않는다.
	clock := time.Now().UTC()
	deps := laneWaitDeps{
		now:   func() time.Time { return clock },
		sleep: func(d time.Duration) { clock = clock.Add(d) },
	}

	var out bytes.Buffer
	// interval(기본 2s)보다 짧은 timeout 을 줘서 첫 sleep 한 번으로 데드라인을 넘긴다.
	code := app.runLaneWaitWith(context.Background(), []string{"--timeout", "1s", "--stale", "999h"}, &out, deps)
	if code != 1 {
		t.Fatalf("타임아웃인데 종료코드가 %d 다(기대 1):\n%s", code, out.String())
	}
	mustContain(t, "타임아웃 stdout", out.String(), "다시 불러라")
}

// ─────────────────────────────────────────────────────────────────────────────
// ⑤ 회귀락 — 대기 중 내 줄 행이 회수되면 "사라졌다"로 끝난다(리뷰 Important, 2026-08-12)
// ─────────────────────────────────────────────────────────────────────────────

// TestLaneWaitDetectsRowReclaimedWhileWaiting 는 리뷰어가 실측 재현한 결함을 잠근다:
// 대기 중 사람이 `fd lane release` 로 내 줄 행을 회수하면, 고치기 전에는 judgeLaneWait 가
// myTurn=false 만 내고 그 사실("차례가 아니다"·정상 대기)과 "회수됐다"(내 자리 자체가
// 없어졌다)를 구분하지 못했다 — stale 안전망도 우회된 채(그 자원을 아예 안 훑는다) 기본
// --timeout(9분)을 다 채우고 사실과 다른 사유("아직 차례가 아니다")로 끝났다.
// rowGone 을 두 번 연속 관측하면 "사라졌다"로 끝나야 한다.
func TestLaneWaitDetectsRowReclaimedWhileWaiting(t *testing.T) {
	h := newHarness(t)
	if code, out := h.runAs("cc-lw-front-gone", "land"); code != 0 {
		t.Fatalf("전제가 깨졌다 — 앞사람이 레인을 못 잡았다(%d):\n%s", code, out)
	}
	frontRow := laneLive(t, h)[0]

	env := map[string]string{}
	for k, v := range h.env {
		env[k] = v
	}
	env["CLAUDE_CODE_SESSION_ID"] = "cc-lw-gone"
	app := newApp(envOf(env), quietLogger(), h.home, strings.NewReader(""))

	clock := time.Now().UTC()
	released := false
	deps := laneWaitDeps{
		now: func() time.Time { return clock },
		sleep: func(d time.Duration) {
			clock = clock.Add(d)
			if released {
				return
			}
			// 내 줄 행(front 의 것이 아닌 살아 있는 행)을 사람이 강제 회수한다.
			var mine int64
			for _, r := range laneLive(t, h) {
				if r.ID != frontRow.ID {
					mine = r.ID
				}
			}
			if mine == 0 {
				return // 아직 초기 acquire 의 커밋이 안 보인다 — 다음 sleep 에서 다시 본다
			}
			if code, out := h.run("", "lane", "release", "--row", itoa(mine),
				"--reason", "리뷰 회귀락 — 대기 중 강제 회수"); code != 0 {
				t.Fatalf("회수가 %d 로 끝났다:\n%s", code, out)
			}
			released = true
		},
	}

	var out bytes.Buffer
	code := app.runLaneWaitWith(context.Background(), nil, &out, deps)
	if code != 1 {
		t.Fatalf("회수된 뒤인데 종료코드가 %d 다(기대 1):\n%s", code, out.String())
	}
	mustContain(t, "회수 감지 stdout", out.String(), "사라졌다")
}
