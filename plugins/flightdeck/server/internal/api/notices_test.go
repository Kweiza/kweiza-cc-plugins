package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// 꼬리 전용 표면 — 소비자 좌표계는 **HTTP 응답 본문**과 **/metrics 문서**다.
//
// ★ 이 시험이 막는 것: MCP 응답 꼬리가 알림을 가져오려고 화면 표면(dashboard.json)을 쳐서
// **도구 호출 1회마다** 세션 카드 파생(git worktree list · ChangedPaths · UncommittedPaths · UncommittedDelta)이
// 통째로 한 번 더 도는 것. 지금은 무해하지만 세션·워크트리가 늘면
// 모든 도구 응답 지연에 저장소 전수 훑기가 얹힌다.

var deriveCounterRe = regexp.MustCompile(`(?m)^flightdeck_session_card_derives_total (\d+)$`)

// deriveRuns 는 /metrics 문서에서 파생 횟수를 읽는다. **응답 본문에서 읽는다** —
// 서버 내부 카운터를 들여다보면 "지표에 안 뜬다"는 축을 원리적으로 못 본다.
func (e *env) deriveRuns() uint64 {
	e.t.Helper()
	w := e.do(http.MethodGet, "/metrics", nil)
	if w.Code != http.StatusOK {
		e.t.Fatalf("/metrics 가 %d 다", w.Code)
	}
	m := deriveCounterRe.FindStringSubmatch(w.Body.String())
	if m == nil {
		e.t.Fatalf("/metrics 에 세션 카드 파생 축이 없다 — 그 비용이 어느 화면에도 안 뜬다:\n%s", w.Body.String())
	}
	n, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		e.t.Fatalf("파생 횟수를 못 읽었다(%q): %v", m[1], err)
	}
	return n
}

func TestNoticesSkipsSessionCardDerivation(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-notices-1")

	// 꼬리에 실릴 알림 하나를 넣는다. 응답이 비어 있으면 "싸다"가 아니라
	// "아무것도 안 한다"가 되고, 그러면 이 시험은 비용 축만 보고 값 축은 안 본다.
	w := e.do(http.MethodPost, "/api/v1/judgments", map[string]any{
		"project": testProject, "session_id": sess,
		"kind": "ask", "title": "물어볼 것", "body": "계약 문안을 누가 확정하나",
	}, withKey("cc-notices-1:1"))
	if w.Code != http.StatusCreated {
		t.Fatalf("전제가 깨졌다 — 판단 등록이 %d 다: %s", w.Code, w.Body.String())
	}

	// ── 대조가 성립했는지 먼저 단정한다 ─────────────────────────────────────
	// 화면 표면을 치면 파생 횟수가 **반드시 올라야** 한다. 이것이 거짓이면
	// (카운터가 안 물려 있거나 지표에 안 뜨면) 아래 "안 올랐다"는 단정은
	// 조용히 성립하면서 아무것도 검사하지 않는다.
	before := e.deriveRuns()
	if d := e.do(http.MethodGet, "/api/v1/dashboard.json?project="+testProject, nil); d.Code != http.StatusOK {
		t.Fatalf("전제가 깨졌다 — dashboard.json 이 %d 다: %s", d.Code, d.Body.String())
	}
	afterDash := e.deriveRuns()
	if afterDash <= before {
		t.Fatalf("전제가 깨졌다 — 화면 표면을 쳤는데 파생 횟수가 안 올랐다(%d→%d). "+
			"계측이 안 물려 있으면 아래 단정은 아무것도 안 본다", before, afterDash)
	}

	// ── 본 단정: 꼬리 표면은 그 파생을 안 돈다 ──────────────────────────────
	n := e.do(http.MethodGet, "/api/v1/notices?project="+testProject+"&limit=20", nil)
	if n.Code != http.StatusOK {
		t.Fatalf("notices 가 %d 다: %s", n.Code, n.Body.String())
	}
	if got := e.deriveRuns(); got != afterDash {
		t.Errorf("꼬리 표면이 세션 카드 파생을 돌렸다(%d→%d) — 도구 호출마다 저장소를 전수 훑는다",
			afterDash, got)
	}

	// 값 축: 넣은 알림이 실제로 나와야 한다.
	body := decodeBody(t, n)
	notes, ok := body["notes"].([]any)
	if !ok {
		t.Fatalf("응답에 notes 절이 없다: %s", n.Body.String())
	}
	if len(notes) != 1 {
		t.Fatalf("알림이 %d건이다 — 1건이어야 한다: %s", len(notes), n.Body.String())
	}
	if !strings.Contains(n.Body.String(), "계약 문안을 누가 확정하나") {
		t.Errorf("알림 본문이 응답에 없다: %s", n.Body.String())
	}
}

// 파생 축이 /metrics 문서에 **세 줄 전부** 뜬다(횟수·카드 수·시간).
// 횟수만 있으면 "자주 도는가"는 답하되 "얼마나 비싼가"는 답하지 못한다.
func TestMetricsExposesDerivationCost(t *testing.T) {
	e := newEnv(t, nil)
	e.openSession("cc-notices-2")
	if d := e.do(http.MethodGet, "/api/v1/dashboard.json?project="+testProject, nil); d.Code != http.StatusOK {
		t.Fatalf("전제가 깨졌다 — dashboard.json 이 %d 다", d.Code)
	}

	out := e.do(http.MethodGet, "/metrics", nil).Body.String()
	for _, want := range []string{
		"flightdeck_session_card_derives_total",
		"flightdeck_session_cards_total",
		"flightdeck_session_card_derive_seconds_total",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("지표 문서에 %q 가 없다:\n%s", want, out)
		}
	}
	// 라우트 라벨이 갈려야 꼬리 트래픽과 화면 트래픽을 구분할 수 있다.
	if e.do(http.MethodGet, "/api/v1/notices?project="+testProject, nil).Code != http.StatusOK {
		t.Fatal("notices 호출이 실패했다")
	}
	out = e.do(http.MethodGet, "/metrics", nil).Body.String()
	want := fmt.Sprintf("flightdeck_requests_total{route=%q,status=\"200\"}", "GET /api/v1/notices")
	if !strings.Contains(out, want) {
		t.Errorf("지표 문서에 꼬리 표면의 라우트 라벨이 없다 — 화면 트래픽과 한 덩어리로 남는다:\n%s", out)
	}
}
