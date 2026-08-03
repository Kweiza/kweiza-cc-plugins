package api

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// /metrics — 프로메테우스 텍스트 포맷을 직접 쓴다(의존성 0).
//
// 라벨은 **패턴 문자열**만 쓴다. 실제 경로를 라벨에 넣으면 항목 id·세션 id 가
// 그대로 시계열 이름이 되어 카디널리티가 요청 수만큼 늘고, 동시에
// 인증 없이 열려 있는 표면으로 도메인 좌표가 새어 나간다.

// RequestKey 는 요청 카운터의 라벨 조합이다.
type RequestKey struct {
	Route  string
	Status int
}

// MetricsSnapshot 은 어느 시점의 계측 값 전부다.
//
// 렌더링을 **스냅숏의 순수 함수**로 두는 이유: 잠금을 쥔 채 문자열을 만들면
// 시험이 그 출력을 직접 만들 수 없고, 그러면 포맷 단정이 서버를 띄워야만 가능해진다.
type MetricsSnapshot struct {
	Requests      map[RequestKey]uint64
	DurationSum   map[string]float64
	DurationCount map[string]uint64
	Unauthorized  uint64
	RateLimited   uint64
	Panics        uint64
	IdemReplays   uint64
	IdemConflicts uint64
	SSEDropped    uint64
	SSESubs       int

	// 세션 카드 파생 — **요청 지표와 다른 축이다.** 그쪽은 라우트별 총 시간이고
	// 이쪽은 그중 git 저장소 전수 훑기가 먹은 몫이라, 둘을 겹쳐 봐야
	// "느린 것이 파생인가 다른 것인가"가 갈린다.
	//
	// 이 축이 없던 동안 MCP 꼬리가 도구 호출마다 이 파생을 한 번씩 더 돌렸는데
	// 그 사실이 **어느 화면에도 안 떴다.** 계측이 없으면 비용이 존재하지 않는 것처럼 보인다.
	DeriveRuns    uint64
	DeriveCards   uint64
	DeriveSeconds float64
}

// RenderMetrics 는 스냅숏을 프로메테우스 텍스트 포맷으로 옮긴다. 순수 함수다.
//
// 출력 순서를 정렬해 고정한다 — 맵 순회 순서가 그대로 나가면 같은 상태에서
// 매번 다른 문서가 나오고, 그러면 시험이 diff 로 단정할 수 없다.
func RenderMetrics(s MetricsSnapshot) string {
	var b strings.Builder

	b.WriteString("# HELP flightdeck_requests_total 게이트를 통과한 요청 수(라우트 패턴·상태코드별)\n")
	b.WriteString("# TYPE flightdeck_requests_total counter\n")
	keys := make([]RequestKey, 0, len(s.Requests))
	for k := range s.Requests {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Route != keys[j].Route {
			return keys[i].Route < keys[j].Route
		}
		return keys[i].Status < keys[j].Status
	})
	for _, k := range keys {
		fmt.Fprintf(&b, "flightdeck_requests_total{route=\"%s\",status=\"%d\"} %d\n",
			escapeLabel(k.Route), k.Status, s.Requests[k])
	}

	b.WriteString("# HELP flightdeck_request_duration_seconds 처리 시간 합과 건수(라우트 패턴별)\n")
	b.WriteString("# TYPE flightdeck_request_duration_seconds summary\n")
	routes := make([]string, 0, len(s.DurationCount))
	for r := range s.DurationCount {
		routes = append(routes, r)
	}
	sort.Strings(routes)
	for _, r := range routes {
		fmt.Fprintf(&b, "flightdeck_request_duration_seconds_sum{route=\"%s\"} %.6f\n",
			escapeLabel(r), s.DurationSum[r])
		fmt.Fprintf(&b, "flightdeck_request_duration_seconds_count{route=\"%s\"} %d\n",
			escapeLabel(r), s.DurationCount[r])
	}

	// ★ 아래 넷은 액세스 로그에 줄이 안 남는 축이다.
	//   401·429 를 건별로 로그에 남기면 초과 트래픽이 그대로 로그 증폭이 되므로,
	//   그 축의 **유일한 원천**이 이 카운터다. 여기서 빠지면 관측이 통째로 사라진다.
	for _, m := range []struct {
		name, help string
		val        uint64
	}{
		{"flightdeck_unauthorized_total", "인증 게이트에서 거절된 요청 수(로그 줄 없음)", s.Unauthorized},
		{"flightdeck_rate_limited_total", "한도 초과로 거절된 요청 수(로그 줄 없음)", s.RateLimited},
		{"flightdeck_panics_total", "핸들러 패닉 수", s.Panics},
		{"flightdeck_idempotent_replays_total", "같은 Idempotency-Key 로 재생한 응답 수", s.IdemReplays},
		{"flightdeck_idempotent_conflicts_total", "같은 키에 다른 요청이 와서 거절한 수", s.IdemConflicts},
		{"flightdeck_sse_dropped_total", "구독자 버퍼가 차서 버린 이벤트 수", s.SSEDropped},
	} {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", m.name, m.help, m.name, m.name, m.val)
	}

	b.WriteString("# HELP flightdeck_session_card_derives_total 세션 카드 파생을 돌린 횟수(git 저장소 전수 훑기)\n")
	b.WriteString("# TYPE flightdeck_session_card_derives_total counter\n")
	fmt.Fprintf(&b, "flightdeck_session_card_derives_total %d\n", s.DeriveRuns)
	b.WriteString("# HELP flightdeck_session_cards_total 그 파생이 훑은 세션 수 누계\n")
	b.WriteString("# TYPE flightdeck_session_cards_total counter\n")
	fmt.Fprintf(&b, "flightdeck_session_cards_total %d\n", s.DeriveCards)
	b.WriteString("# HELP flightdeck_session_card_derive_seconds_total 그 파생에 든 시간 합\n")
	b.WriteString("# TYPE flightdeck_session_card_derive_seconds_total counter\n")
	fmt.Fprintf(&b, "flightdeck_session_card_derive_seconds_total %.6f\n", s.DeriveSeconds)

	b.WriteString("# HELP flightdeck_sse_subscribers 지금 붙어 있는 SSE 구독자 수\n")
	b.WriteString("# TYPE flightdeck_sse_subscribers gauge\n")
	fmt.Fprintf(&b, "flightdeck_sse_subscribers %d\n", s.SSESubs)

	return b.String()
}

// escapeLabel 은 라벨 값을 노출 포맷 규칙대로 이스케이프한다. 순수 함수다.
// 역슬래시·큰따옴표·개행 셋만 규칙에 있다.
func escapeLabel(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

// metrics 는 잠금이 붙은 누산기다.
type metrics struct {
	mu   sync.Mutex
	snap MetricsSnapshot
}

func newMetrics() *metrics {
	return &metrics{snap: MetricsSnapshot{
		Requests:      map[RequestKey]uint64{},
		DurationSum:   map[string]float64{},
		DurationCount: map[string]uint64{},
	}}
}

// observe 는 게이트를 통과한 요청 1건을 센다.
func (m *metrics) observe(route string, status int, seconds float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snap.Requests[RequestKey{Route: route, Status: status}]++
	m.snap.DurationSum[route] += seconds
	m.snap.DurationCount[route]++
}

func (m *metrics) incUnauthorized() { m.mu.Lock(); m.snap.Unauthorized++; m.mu.Unlock() }
func (m *metrics) incRateLimited()  { m.mu.Lock(); m.snap.RateLimited++; m.mu.Unlock() }
func (m *metrics) incPanic()        { m.mu.Lock(); m.snap.Panics++; m.mu.Unlock() }
func (m *metrics) incReplay()       { m.mu.Lock(); m.snap.IdemReplays++; m.mu.Unlock() }
func (m *metrics) incConflict()     { m.mu.Lock(); m.snap.IdemConflicts++; m.mu.Unlock() }

// snapshot 은 렌더링에 넘길 값 사본이다. 맵을 복사하지 않으면
// 렌더링 중에 다른 요청이 같은 맵을 쓴다.
func (m *metrics) snapshot(sseSubs int, sseDropped uint64) MetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := MetricsSnapshot{
		Requests:      make(map[RequestKey]uint64, len(m.snap.Requests)),
		DurationSum:   make(map[string]float64, len(m.snap.DurationSum)),
		DurationCount: make(map[string]uint64, len(m.snap.DurationCount)),
		Unauthorized:  m.snap.Unauthorized,
		RateLimited:   m.snap.RateLimited,
		Panics:        m.snap.Panics,
		IdemReplays:   m.snap.IdemReplays,
		IdemConflicts: m.snap.IdemConflicts,
		SSEDropped:    sseDropped,
		SSESubs:       sseSubs,
	}
	for k, v := range m.snap.Requests {
		out.Requests[k] = v
	}
	for k, v := range m.snap.DurationSum {
		out.DurationSum[k] = v
	}
	for k, v := range m.snap.DurationCount {
		out.DurationCount[k] = v
	}
	return out
}
