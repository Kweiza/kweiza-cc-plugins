package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

// SSE 시험 — 단정의 좌표계는 **와이어 한 줄**과 **/metrics 한 줄**이다.
// 허브의 내부 카운터를 직접 읽으면 "구독자가 정리됐다"를 자기가 자기에게 묻는 셈이 된다.

// waitFor 는 조건이 참이 될 때까지 기다린다. 실패하면 마지막 상태를 찍고 세운다.
func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: %v 안에 성립하지 않았다", what, d)
}

// subscribers 는 /metrics 가 말하는 구독자 수다(소비자 좌표계).
func (e *env) subscribers() string {
	e.t.Helper()
	for _, line := range strings.Split(e.do(http.MethodGet, "/metrics", nil).Body.String(), "\n") {
		if strings.HasPrefix(line, "flightdeck_sse_subscribers ") {
			return strings.TrimPrefix(line, "flightdeck_sse_subscribers ")
		}
	}
	e.t.Fatal("/metrics 에 구독자 수 축이 없다")
	return ""
}

func TestSSEDeliversEventAndCleansUpOnDisconnect(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Heartbeat = 50 * time.Millisecond })
	ts := httptest.NewServer(e.h)
	defer ts.Close()

	client := &http.Client{Transport: &http.Transport{}}
	defer client.CloseIdleConnections()

	// ★ 구독 취소를 ts.Close() **뒤에** 등록한다(defer 는 LIFO). 시험이 중간에 서면
	// 열린 SSE 응답이 남아 httptest 의 Close 가 영원히 기다린다 — 실패가 hang 으로 둔갑한다.
	var cancels []context.CancelFunc
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()

	// 준비 요청 한 번으로 커넥션·풀 고루틴을 데운다. 그래야 기준선이 흔들리지 않는다.
	if w := e.do(http.MethodGet, "/healthz", nil); w.Code != http.StatusOK {
		t.Fatalf("준비 요청 실패: %d", w.Code)
	}
	runtime.GC()
	before := runtime.NumGoroutine()

	const n = 5
	readers := make([]*bufio.Reader, 0, n)
	bodies := make([]interface{ Close() error }, 0, n)
	for i := 0; i < n; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events?project="+testProject, nil)
		if err != nil {
			t.Fatalf("요청 생성 실패: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("구독 실패: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("구독이 %d 다", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
			t.Fatalf("Content-Type 이 %q 다", ct)
		}
		br := bufio.NewReader(resp.Body)
		// 첫 줄은 **무엇을 구독했는지**까지 말한다 — 범위를 안 말하면 뒤이은 침묵이
		// "아무 일도 없다"인지 "필터가 아무것도 안 맞춘다"인지 구분되지 않는다.
		if first := readFrame(t, br); !strings.HasPrefix(first, ": connected") {
			t.Fatalf("구독 성립 줄이 %q 다", first)
		}
		readers = append(readers, br)
		bodies = append(bodies, resp.Body)
	}

	// ★ 대조: 구독이 정말 성립했는가. 이것을 먼저 단정하지 않으면
	//   아래의 "정리됐다"가 "애초에 안 붙었다"와 구분되지 않는다.
	waitFor(t, "구독자 5명이 붙는다", 2*time.Second, func() bool { return e.subscribers() == "5" })
	during := runtime.NumGoroutine()
	if during < before+n {
		t.Fatalf("구독 고루틴이 안 생겼다: before=%d during=%d", before, during)
	}

	// 쓰기 하나가 이벤트가 되어 전 구독자에게 간다.
	sess := e.openSession("cc-sse")
	for i, br := range readers {
		// ★ 하트비트를 건너뛴다. 이 시험은 Heartbeat=50ms 로 돌고, 위 waitFor(구독자 5명)가
		// 그보다 오래 걸리면 **이벤트보다 하트비트가 먼저 큐에 있다** — 그러면 이 단언이
		// 내용과 무관하게 실패한다. main 에서 12회 중 4회 재현했다(2026-08-05).
		// 이 시험이 재려는 것은 "이벤트가 전 구독자에게 간다"이지 프레임 순서가 아니다.
		frame := readEventFrame(t, br)
		if !strings.Contains(frame, `"kind":"session.open"`) {
			t.Fatalf("%d번 구독자가 받은 프레임이 다르다:\n%s", i, frame)
		}
		if !strings.Contains(frame, `"session_id":"`+sess+`"`) {
			t.Fatalf("%d번 프레임에 세션 좌표가 없다:\n%s", i, frame)
		}
		if !strings.Contains(frame, `"kind":"session.open"`) {
			t.Fatalf("%d번 프레임 data 가 JSON 한 줄이 아니다:\n%s", i, frame)
		}
	}

	// 하트비트가 온다 — 조용한 연결은 프록시가 끊는다.
	if got := readFrame(t, readers[0]); !strings.Contains(got, ": heartbeat") {
		t.Fatalf("하트비트가 안 왔다: %q", got)
	}

	// 끊는다.
	for i := range cancels {
		cancels[i]()
		if err := bodies[i].Close(); err != nil {
			t.Logf("본문 닫기: %v", err) // 취소된 뒤라 오류가 정상이다. 삼키지 않고 남긴다
		}
	}

	waitFor(t, "구독자가 0이 된다", 3*time.Second, func() bool { return e.subscribers() == "0" })
	waitFor(t, "고루틴이 기준선으로 돌아온다", 3*time.Second, func() bool {
		return runtime.NumGoroutine() <= before+2
	})
}

// readFrame 은 빈 줄까지의 SSE 한 덩어리를 읽는다.
//
// ★ 프레임 끝의 빈 줄까지 **반드시 소비한다**. 안 그러면 다음 호출이 그 빈 줄을 보고
// 빈 프레임을 돌려주고, 그 빈 문자열이 "다음 이벤트가 안 왔다"로 오독된다
// (이 시험을 처음 쓸 때 실제로 그렇게 헛다리를 짚었다).
func readFrame(t *testing.T, br *bufio.Reader) string {
	t.Helper()
	var b strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("프레임을 읽는 중 끊겼다: %v (지금까지: %q)", err, b.String())
		}
		if line == "\n" {
			if b.Len() == 0 {
				continue // 앞 프레임의 잔여 빈 줄
			}
			return b.String()
		}
		b.WriteString(line)
	}
}

// readEventFrame 은 다음 **이벤트** 프레임을 읽는다 — 주석 줄(하트비트·`: bye` 등)은 건너뛴다.
//
// ★ 하트비트는 티커가 내므로 도착 시점이 시험의 통제 밖이다. 그것을 이벤트 자리에서 받으면
// 시험이 내용이 아니라 **스케줄러를 재게 된다.** 주석 줄을 단정해야 하는 시험은
// readFrame 을 그대로 쓴다.
func readEventFrame(t *testing.T, br *bufio.Reader) string {
	t.Helper()
	for i := 0; i < 64; i++ {
		frame := readFrame(t, br)
		if !strings.HasPrefix(frame, ":") {
			return frame
		}
	}
	t.Fatal("주석 줄만 64개가 왔다 — 이벤트가 아예 안 온다")
	return ""
}

func TestSSEProjectFilterDoesNotHideGlobalEvents(t *testing.T) {
	e := newEnv(t, nil)
	other := e.srv.hub.Subscribe("다른프로젝트")
	mine := e.srv.hub.Subscribe(testProject)
	all := e.srv.hub.Subscribe("")
	defer func() {
		e.srv.hub.Unsubscribe(other)
		e.srv.hub.Unsubscribe(mine)
		e.srv.hub.Unsubscribe(all)
	}()

	if err := e.srv.hub.Publish("1", Event{Kind: "item.add", Project: testProject}); err != nil {
		t.Fatalf("발행 실패: %v", err)
	}
	if len(mine.ch) != 1 || len(all.ch) != 1 {
		t.Fatalf("받아야 할 구독자가 못 받았다: mine=%d all=%d", len(mine.ch), len(all.ch))
	}
	if len(other.ch) != 0 {
		t.Fatal("다른 프로젝트 구독자가 남의 이벤트를 받았다")
	}
	// 표 밖: 프로젝트가 없는 이벤트(발번 등)는 **누구에게도 숨기지 않는다** —
	// 필터로 접으면 전역 사건이 조용히 사라진다.
	if err := e.srv.hub.Publish("2", Event{Kind: "counter.alloc"}); err != nil {
		t.Fatalf("발행 실패: %v", err)
	}
	if len(other.ch) != 1 {
		t.Fatal("프로젝트 없는 이벤트가 필터에 걸렸다")
	}
}

func TestSlowSubscriberDoesNotBlockPublishingAndIsCounted(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.SSEBuffer = 1 })
	slow := e.srv.hub.Subscribe("")
	defer e.srv.hub.Unsubscribe(slow)

	for i := 0; i < 3; i++ {
		done := make(chan error, 1)
		go func() { done <- e.srv.hub.Publish("x", Event{Kind: "item.add", Project: testProject}) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("발행 실패: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("느린 구독자가 발행을 막았다 — 최적화가 정본을 죽인다")
		}
	}
	if !strings.Contains(e.do(http.MethodGet, "/metrics", nil).Body.String(),
		"flightdeck_sse_dropped_total 2") {
		t.Fatalf("버린 이벤트가 안 세어졌다 — 조용한 이벤트가 된다:\n%s",
			e.do(http.MethodGet, "/metrics", nil).Body.String())
	}
}

// 구독 범위가 세 경우에 **서로 다르게** 보여야 한다.
//
// ★ 이 축이 없으면 오타 난 프로젝트로 구독한 사람이 영원히 조용한 스트림을 정상으로 읽는다.
// 그리고 이 시험을 쓰게 된 계기 자체가 그 혼동이었다 — 필터 없는 구독을 재다가
// **구독이 성립하기 전에 측정해** 0건을 보고 "필터 없으면 아무것도 안 온다"고 오진했다.
// 허브는 처음부터 빈 프로젝트를 전 프로젝트로 다뤘다.
func TestSubscriptionScopeDistinguishesSilenceFromBadFilter(t *testing.T) {
	e := newEnv(t, nil)
	e.openSession("cc-1")

	// ★ SSE 핸들러는 클라이언트가 끊을 때까지 돈다. 레코더는 스스로 안 끊기므로
	//   요청 컨텍스트로 끊는다 — 안 그러면 이 시험이 영원히 멈춘다(실제로 그랬다).
	first := func(q string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/events"+q, nil).WithContext(ctx)
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("구독이 %d 다(%s)", w.Code, q)
		}
		line, _, _ := strings.Cut(w.Body.String(), "\n")
		return line
	}

	none := first("")
	known := first("?project=" + testProject)
	unknown := first("?project=없는프로젝트")

	if !strings.Contains(none, "전 프로젝트") {
		t.Errorf("필터 없는 구독이 범위를 안 말한다: %q", none)
	}
	if !strings.Contains(known, testProject) || strings.Contains(known, "등록되지 않은") {
		t.Errorf("등록된 프로젝트를 미등록으로 말한다: %q", known)
	}
	if !strings.Contains(unknown, "등록되지 않은") {
		t.Errorf("모르는 프로젝트인데 그 사실을 안 말한다 — 조용한 스트림이 정상으로 읽힌다: %q", unknown)
	}
	// 셋이 서로 달라야 한다. 같으면 이 줄은 아무것도 구분하지 못한다.
	if none == known || known == unknown {
		t.Errorf("구독 범위 문구가 겹친다: %q · %q · %q", none, known, unknown)
	}
}
