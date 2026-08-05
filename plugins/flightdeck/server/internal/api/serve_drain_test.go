package api

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 이 파일은 종료 계약 하나를 붙든다: **드레인은 인플라이트를 마무리하고, 수명이 정해지지
// 않은 응답은 그것을 안 붙든다.** 두 시험이 짝일 때만 뜻이 있다 —
// 하나만 고치면 다른 하나가 빨개지도록 짜여 있다.
//
// 시계 단정이 없다. 판별은 전부 인과(채널 악수)와 로그 한 줄의 유무로 한다.

// serveAddrFromLog 는 Serve 가 실제로 잡은 주소를 로그에서 읽는다.
//
// ★ ":0" 을 미리 잡았다 놓고 재사용하면 TOCTOU 가 남는다(그 사이 남이 잡을 수 있다).
// 로그의 "서버 기동" 줄은 **실제로 잡힌** 주소라 그 창이 없다.
func serveAddrFromLog(t *testing.T, logs *syncBuffer) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, r := range logs.records(t) {
			if r["msg"] == "서버 기동" {
				if s, ok := r["route"].(string); ok && strings.TrimSpace(s) != "" {
					return s
				}
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("서버 기동 로그가 안 왔다 — Serve 가 안 떴다")
	return ""
}

// hasLogMsg 는 그 msg 를 가진 줄이 있는가다.
func hasLogMsg(t *testing.T, logs *syncBuffer, msg string) bool {
	t.Helper()
	for _, r := range logs.records(t) {
		if r["msg"] == msg {
			return true
		}
	}
	return false
}

const graceExceeded = "유예 안에 마무리를 못 했다 — 인플라이트를 끊고 강제로 닫는다"

// waitUntil 은 조건이 참이 될 때까지 기다린다. **단조 조건에만 쓴다** —
// 한 번 참이면 계속 참인 것만 여기서 기다려야 흔들리지 않는다.
func waitUntil(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("%s: 시간 안에 성립하지 않았다", what)
}

// TestDrainFinishesInflightWrite 는 이 항목의 본체다.
//
// 드레인이 걸린 뒤에 **실제 쓰기**를 하는 요청이 200 으로 끝나고, 그 쓰기가 원장에
// 도착하는지를 단언한다. 고치기 전에는 500(context canceled)이고 원장에 한 행도 안 남는다.
//
// ★ 핸들러가 sleep 만 하고 200 을 쓰면 판별력이 없다 — 취소는 협력적이라 고치기 **전에도**
// 200 이 나간다. 컨텍스트를 실제로 소비하는 코드(service 호출)에서만 판별이 난다.
func TestDrainFinishesInflightWrite(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-drain")

	entered := make(chan struct{})
	release := make(chan struct{})

	mux := e.srv.routes()
	mux.HandleFunc("GET /zz-slow", func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		if err := e.svc.Beat(r.Context(), sess, model.SignalPrompt, nil); err != nil {
			http.Error(w, "ctx-dead: "+err.Error(), http.StatusInternalServerError)
			return
		}
		io.WriteString(w, "ok")
	})
	e.srv.mux = mux
	h := surface{Handler: e.srv.chain(mux), srv: e.srv}

	serveCtx, drain := context.WithCancel(context.Background())
	ret := make(chan error, 1)
	go func() { ret <- Serve(serveCtx, "127.0.0.1:0", h, e.srv.log) }()
	addr := serveAddrFromLog(t, e.logs)

	// ① 요청이 인플라이트임이 보장된다.
	resp := make(chan *http.Response, 1)
	errc := make(chan error, 1)
	go func() {
		r, err := http.Get("http://" + addr + "/zz-slow")
		if err != nil {
			errc <- err
			return
		}
		resp <- r
	}()
	select {
	case <-entered:
	case err := <-errc:
		t.Fatalf("요청이 아예 못 붙었다: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("핸들러에 안 들어왔다")
	}

	// ② 드레인.
	drain()

	// ③ 리스너가 실제로 닫힐 때까지 기다린다. Shutdown 은 리스너를 **먼저** 닫으므로,
	//    새 연결 거절은 "취소가 이미 전파됐다"는 happens-after 간선이다. 단조 조건이다.
	waitUntil(t, "리스너가 닫힌다", 5*time.Second, func() bool {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return true
		}
		c.Close()
		return false
	})

	// ④ ③ 덕에 "취소보다 빨라서 살아남았다"는 갈래가 원리적으로 없다.
	close(release)

	var got *http.Response
	select {
	case got = <-resp:
	case err := <-errc:
		t.Fatalf("드레인이 요청을 끊었다: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("응답이 안 왔다")
	}
	defer got.Body.Close()
	body, _ := io.ReadAll(got.Body)

	if got.StatusCode != http.StatusOK {
		t.Fatalf("드레인 중 요청이 %d 로 끝났다(본문 %q) — 마무리가 아니라 절단이다",
			got.StatusCode, strings.TrimSpace(string(body)))
	}
	if strings.TrimSpace(string(body)) != "ok" {
		t.Fatalf("본문이 %q 다", string(body))
	}

	select {
	case err := <-ret:
		if err != nil {
			t.Fatalf("Serve 가 오류로 끝났다: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve 가 안 돌아왔다")
	}

	// **원장 좌표계.** "요청이 안 끊겼다"가 아니라 "서버가 그 쓰기를 실제로 갖게 됐다"를
	// 단언한다 — 그것이 이 고침이 사는 이유다.
	if n := beatRows(t, e, sess); n != 1 {
		t.Fatalf("session.beat 이벤트가 %d 행이다 — 쓰기가 원장에 안 닿았다", n)
	}
	if hasLogMsg(t, e.logs, graceExceeded) {
		t.Fatal("유예를 넘겼다 — 마무리가 아니라 강제 종료로 끝났다")
	}
}

// beatRows 는 이 세션에 prompt 신호가 원장에 남았는가다(0 또는 1).
func beatRows(t *testing.T, e *env, sess string) int {
	t.Helper()
	sig, err := e.st.Signals(context.Background(), sess)
	if err != nil {
		t.Fatalf("신호 조회 실패: %v", err)
	}
	if _, ok := sig[model.SignalPrompt]; ok {
		return 1
	}
	return 0
}

// TestSSEStreamDoesNotHoldShutdown 은 짝이 되는 시험이다.
//
// BaseContext 만 떼고 종료 통지를 안 만들면 이 시험이 **빨개진다** — SSE 구독이
// 유예를 통째로 쓰고 "유예 안에 마무리를 못 했다" ERROR 가 뜬다.
//
// ★ 시계 단정이 없다. 유예 소진 여부는 로그 한 줄의 유무로 이진 판별된다.
func TestSSEStreamDoesNotHoldShutdown(t *testing.T) {
	e := newEnv(t, nil)
	h := surface{Handler: e.h, srv: e.srv}

	serveCtx, drain := context.WithCancel(context.Background())
	ret := make(chan error, 1)
	go func() { ret <- Serve(serveCtx, "127.0.0.1:0", h, e.srv.log) }()
	addr := serveAddrFromLog(t, e.logs)

	sseResp, err := http.Get("http://" + addr + "/api/v1/events")
	if err != nil {
		t.Fatalf("SSE 구독 실패: %v", err)
	}
	defer sseResp.Body.Close()

	// **대조**: 첫 줄을 실제로 받는다. 안 받으면 "스트림이 안 붙어서 안 막았다"와 구분이 안 된다.
	br := bufio.NewReader(sseResp.Body)
	first, err := br.ReadString('\n')
	if err != nil || !strings.HasPrefix(first, ": connected") {
		t.Fatalf("구독 첫 줄이 %q (err %v)", first, err)
	}
	waitUntil(t, "구독자가 1", 5*time.Second, func() bool { return e.subscribers() == "1" })

	drain()

	select {
	case err := <-ret:
		if err != nil {
			t.Fatalf("Serve 가 오류로 끝났다: %v", err)
		}
	case <-time.After(ShutdownGrace + 5*time.Second):
		// 판정이 아니라 hang 방지다. 매달림이 통과로 둔갑하면 안 된다.
		t.Fatal("Serve 가 안 돌아왔다 — SSE 가 종료를 붙들고 있다")
	}

	if hasLogMsg(t, e.logs, graceExceeded) {
		t.Fatal("SSE 구독 하나가 셧다운 유예를 통째로 썼다")
	}
	if !hasLogMsg(t, e.logs, "서버 종료") {
		t.Fatal("정상 종료 갈래를 안 지났다")
	}

	// 소비자 좌표계: 조용한 EOF 가 아니라 **사유**를 받는다.
	rest, _ := io.ReadAll(br)
	if !strings.Contains(string(rest), ": bye") {
		t.Fatalf("종료를 소비자에게 안 말했다 — 남은 스트림 %q", string(rest))
	}
}

// TestGraceExceededCutsAndSaysSo 는 **2단 절단 갈래**를 실제로 지난다.
//
// ★ 이 시험이 없으면 위 두 시험의 `graceExceeded` **부재 단언이 조용히 항진명제가 된다** —
// 프로덕션 문구가 바뀌면 그 줄은 영영 안 나타나고 둘 다 초록으로 남는다. 여기서 그 문구를
// **양쪽에서 못 박는다**: 이쪽은 있어야 하고 저쪽은 없어야 한다.
//
// 유예를 실제로 소진하므로 ShutdownGrace 만큼 걸린다. 그것이 이 시험의 값이다 —
// 한 번도 안 돌아 본 갈래를 남기지 않는다.
func TestGraceExceededCutsAndSaysSo(t *testing.T) {
	e := newEnv(t, nil)

	entered := make(chan struct{})
	release := make(chan struct{})
	cutSeen := make(chan error, 1)

	mux := e.srv.routes()
	mux.HandleFunc("GET /zz-stuck", func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		// ★ 요청 컨텍스트를 **안 본다.** 유예 안에는 안 끊기는 것이 계약이므로, 여기서
		// ctx 를 보면 이 시험이 1단을 재는 것이 되어 버린다. 2단이 실제로 끊는지를 재려면
		// 유예를 넘겨서 끊긴 뒤에 확인해야 한다.
		<-release
		cutSeen <- r.Context().Err()
	})
	e.srv.mux = mux
	h := surface{Handler: e.srv.chain(mux), srv: e.srv}

	serveCtx, drain := context.WithCancel(context.Background())
	ret := make(chan error, 1)
	go func() { ret <- Serve(serveCtx, "127.0.0.1:0", h, e.srv.log) }()
	addr := serveAddrFromLog(t, e.logs)

	go func() {
		resp, err := http.Get("http://" + addr + "/zz-stuck")
		if err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("핸들러에 안 들어왔다")
	}

	drain()

	select {
	case err := <-ret:
		if err != nil {
			t.Fatalf("Serve 가 오류로 끝났다: %v", err)
		}
	case <-time.After(ShutdownGrace + 10*time.Second):
		t.Fatal("Serve 가 안 돌아왔다 — 2단 절단이 아예 안 돈다")
	}

	if !hasLogMsg(t, e.logs, graceExceeded) {
		t.Fatalf("유예를 넘겼는데 그 사실을 안 말했다 — %q 가 로그에 없다", graceExceeded)
	}

	// **2단이 정말 끊었는가.** 로그만 보면 "말은 했는데 안 끊었다"와 구분이 안 된다.
	close(release)
	select {
	case err := <-cutSeen:
		if err == nil {
			t.Fatal("유예 초과를 말해 놓고 인플라이트 컨텍스트를 안 끊었다")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("핸들러가 안 돌아왔다")
	}
}

// TestServeShutdownLogsDrainMs 는 관측 이음매가 실제로 남는지 본다.
//
// 성공한 자기 갱신은 exec 로 프로세스가 갈아치워져 원장에 한 행도 안 남는다.
// 이 필드가 이 축을 사후에 잴 수 있는 유일한 자리라, 그것이 사라지면 아무도 못 본다.
func TestServeShutdownLogsDrainMs(t *testing.T) {
	e := newEnv(t, nil)
	h := surface{Handler: e.h, srv: e.srv}

	serveCtx, drain := context.WithCancel(context.Background())
	ret := make(chan error, 1)
	go func() { ret <- Serve(serveCtx, "127.0.0.1:0", h, e.srv.log) }()
	serveAddrFromLog(t, e.logs)
	drain()
	if err := <-ret; err != nil {
		t.Fatalf("Serve 가 오류로 끝났다: %v", err)
	}

	for _, r := range e.logs.records(t) {
		if r["msg"] != "서버 종료" {
			continue
		}
		if _, ok := r["drain_ms"].(float64); ok {
			return
		}
		t.Fatalf("서버 종료 줄에 drain_ms 가 없다: %v", r)
	}
	t.Fatal("서버 종료 줄이 없다")
}
