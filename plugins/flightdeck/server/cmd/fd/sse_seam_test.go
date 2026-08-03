package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 이 시험이 닫는 것은 **이음매**다.
//
// 앞선 판에서 발행 쪽 시험은 프레임 문자열에 `event: session.open` 이 있는지 보고,
// 구독 쪽 시험은 HTML 에 `new EventSource` 와 `onmessage` 라는 문자열이 있는지 봤다.
// 둘 다 초록이었고 **합치면 화면이 영원히 정지했다** — SSE 규약상 `event:` 가 붙은 프레임은
// 브라우저의 `onmessage` 를 발화시키지 않기 때문이다.
//
// 두 반쪽을 각자 고정하는 시험은 그 사이의 틈을 원리적으로 못 본다.
// 그래서 여기서는 **같은 조립기가 만든 서버**에서 실제 프레임을 받아,
// 그 프레임이 화면이 실제로 거는 핸들러를 발화시킬 모양인지 단정한다.
func TestSSEFrameMatchesWhatTheDashboardListensFor(t *testing.T) {
	h := newHarness(t)

	// ── 구독자 쪽: 화면이 무엇을 거는가를 HTML 에서 읽어 온다(가정하지 않는다) ──
	page := httpGet(t, h, "/")
	usesOnMessage := strings.Contains(page, "onmessage")
	named := namedListeners(page)
	if !usesOnMessage && len(named) == 0 {
		t.Fatal("전제가 깨졌다 — 화면이 SSE 핸들러를 아무것도 안 건다")
	}

	// ── 발행자 쪽: 실제 프레임을 받는다 ──
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", h.srv.URL+"/events", nil)
	res, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("구독 실패: %v", err)
	}
	defer res.Body.Close()

	go func() {
		time.Sleep(150 * time.Millisecond)
		// 이벤트를 하나 만든다. REST 를 쓴다 — 정본 표면이고 발행 주체이기도 하다.
		// 세션 열기 하나면 충분하다(프로젝트도 이 호출이 만든다).
		body := `{"project":"` + h.project + `","project_path":"` + h.state +
			`","machine_id":"m1","hostname":"h1","worktree":"` + h.state +
			`","cc_session_id":"cc-seam","label":"이음매 시험"}`
		req, _ := http.NewRequest("POST", h.srv.URL+"/api/v1/sessions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "seam:1") // 모든 쓰기에 필요하다
		res, err := h.srv.Client().Do(req)
		if err != nil {
			t.Logf("이벤트 유발 POST 오류: %v", err)
			return
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode >= 300 {
			t.Logf("이벤트 유발 POST 상태=%d 본문=%s", res.StatusCode, string(b))
		}
	}()

	frame := readFrame(t, res.Body)
	if frame == "" {
		t.Fatal("전제가 깨졌다 — 프레임을 하나도 못 받았다")
	}

	// ── 이음매 단정 ──
	evLine := ""
	for _, ln := range strings.Split(frame, "\n") {
		if strings.HasPrefix(ln, "event:") {
			evLine = strings.TrimSpace(strings.TrimPrefix(ln, "event:"))
		}
	}
	if evLine == "" {
		// 이름 없는 프레임 → 브라우저 type 이 "message" → onmessage 가 발화한다.
		if !usesOnMessage {
			t.Errorf("프레임에 event: 가 없는데 화면은 onmessage 를 안 건다(이름 있는 것만 %v) — 아무것도 안 받는다", named)
		}
		return
	}
	// 이름 있는 프레임 → onmessage 는 발화하지 않는다. 화면이 그 이름을 명시로 걸어야 한다.
	if !contains(named, evLine) {
		t.Errorf("프레임이 event: %q 로 오는데 화면은 그 이름을 안 건다(거는 것: %v, onmessage=%v) — "+
			"onmessage 는 이름 있는 프레임에 발화하지 않으므로 화면이 정지한다", evLine, named, usesOnMessage)
	}
}

func httpGet(t *testing.T, h *harness, path string) string {
	t.Helper()
	res, err := h.srv.Client().Get(h.srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s 실패: %v", path, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("본문 읽기 실패: %v", err)
	}
	return string(b)
}

func namedListeners(page string) []string {
	var out []string
	for _, part := range strings.Split(page, "addEventListener(")[1:] {
		if i := strings.IndexAny(part, `'"`); i >= 0 {
			q := part[i]
			if j := strings.IndexByte(part[i+1:], q); j >= 0 {
				out = append(out, part[i+1:i+1+j])
			}
		}
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// readFrame 은 주석(하트비트·`: connected`)이 아닌 첫 프레임을 읽는다.
func readFrame(t *testing.T, r interface{ Read([]byte) (int, error) }) string {
	t.Helper()
	sc := bufio.NewScanner(r)
	var buf []string
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if len(buf) > 0 {
				return strings.Join(buf, "\n")
			}
			continue
		}
		if strings.HasPrefix(line, ":") { // 주석 = 하트비트
			continue
		}
		buf = append(buf, line)
	}
	return ""
}
