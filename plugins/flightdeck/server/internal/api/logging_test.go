package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// service.name 은 **한 줄에 한 번만** 찍혀야 한다.
//
// ★ 왜 실물 JSON 한 줄을 파싱하나: 이 축은 텍스트 핸들러로는 원리적으로 안 보인다.
// 그리고 map 으로 Unmarshal 해도 안 보인다 — encoding/json 은 중복 키를 조용히
// 마지막 값으로 접기 때문에, 파싱된 map 을 아무리 단정해도 "두 번 찍혔다"가 사라진다.
// 그래서 **토큰 흐름으로 최상위 키를 직접 센다.** 소비자(로그 수집기)가 보는 것이 그것이다.
//
// 중복이 왜 위험한가: JSON 중복 키의 처리는 파서마다 다르다(마지막이 이기기도,
// 첫째가 이기기도, 배열로 접기도 한다). 값이 서로 다르면 — MCP 경로가 그랬다 —
// 수집기 판올림 한 번에 "어느 프로세스가 무엇을 했나"라는 축이 조용히 사라진다.

// countTopLevelKey 는 JSON 한 줄에서 최상위 키 하나가 몇 번 나오는지 센다.
func countTopLevelKey(t *testing.T, line, key string) int {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(line))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("로그 줄이 JSON 이 아니다: %v\n%s", err, line)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		t.Fatalf("로그 줄이 객체가 아니다: %s", line)
	}
	n := 0
	depth := 0
	for dec.More() || depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("로그 줄 토큰 해석 실패: %v\n%s", err, line)
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth < 0 {
					return n // 최상위 객체가 닫혔다
				}
			}
			continue
		}
		if depth > 0 {
			continue // 중첩 안의 값. 키/값 짝을 여기서 세지 않는다
		}
		// depth==0 에서 나오는 문자열 토큰은 키다. 그 뒤의 값은 위 분기가 소비한다.
		if s, ok := tok.(string); ok && s == key {
			n++
		}
		// 키에 딸린 스칼라 값 하나를 건너뛴다.
		if _, ok := tok.(string); ok {
			v, err := dec.Token()
			if err != nil {
				t.Fatalf("로그 줄 값 해석 실패: %v\n%s", err, line)
			}
			if d, ok := v.(json.Delim); ok {
				switch d {
				case '{', '[':
					depth++
				case '}', ']':
					depth--
				}
			}
		}
	}
	return n
}

// countTopLevelKey 자체가 맞는지 먼저 본다 — 검사 도구가 틀리면 그 아래 단정이 전부 무의미하다.
func TestCountTopLevelKey(t *testing.T) {
	cases := []struct {
		line string
		want int
	}{
		{`{"a":1,"service.name":"x"}`, 1},
		{`{"service.name":"x","service.name":"y"}`, 2},
		{`{"a":{"service.name":"중첩은 안 센다"},"b":2}`, 0},
		{`{"a":["service.name"],"b":2}`, 0},
		{`{"msg":"service.name","b":2}`, 0},
		{`{}`, 0},
	}
	for _, c := range cases {
		if got := countTopLevelKey(t, c.line, "service.name"); got != c.want {
			t.Errorf("%s → %d, want %d", c.line, got, c.want)
		}
	}
}

// ★ 진입점이 건 로거로 전 계층(store·service·api)을 조립해도
// 한 줄에 service.name 은 하나뿐이어야 한다.
func TestServiceNameIsNotDuplicatedAcrossLayers(t *testing.T) {
	logs := &syncBuffer{}
	// 진입점과 **같은 모양**: JSON 핸들러 + service.name 한 번.
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})).
		With("service.name", "flightdeck")

	dir := t.TempDir()
	st, err := store.OpenWithLogger(dir+"/fd.db", log)
	if err != nil {
		t.Fatalf("DB 열기 실패: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	svc := service.New(st, log)
	srv := newServer(svc, Options{Log: log})
	h := srv.chain(srv.routes())

	// 요청 하나로 액세스 로그 줄을 낸다.
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/healthz 가 %d 다: %s", rec.Code, rec.Body.String())
	}

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	// ── 대조 전제: 볼 줄이 실제로 있는가 ──
	seen := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		seen++
		if n := countTopLevelKey(t, line, "service.name"); n != 1 {
			t.Errorf("한 줄에 service.name 이 %d번이다:\n%s", n, line)
		}
	}
	if seen == 0 {
		t.Fatal("전제가 깨졌다 — 로그 줄이 하나도 안 나왔다. 이 상태로는 위 단정이 무의미하다")
	}
	// 액세스 로그가 실제로 나왔는지도 본다(줄이 마이그레이션 로그뿐이면 축을 안 본 것이다).
	if !strings.Contains(logs.String(), `"msg":"request served"`) {
		t.Fatal("전제가 깨졌다 — 액세스 로그 줄이 없다")
	}
}
