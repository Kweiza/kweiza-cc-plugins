package service

import (
	"encoding/json"
	"testing"

	"github.com/kweiza/flightdeck/internal/store"
)

// 세 갈래는 **JSON 경계를 건넌다** — 판정은 서버에서 나지만 문장은 클라이언트가 찍는다.
//
// ★ 배선이 그렇다: api 가 FinishResult 를 그대로 직렬화하고(handlers_items.go), 별개
// 바이너리인 cmd/fd 가 같은 타입으로 디코드해 RenderFinish 를 부른다(cmds.go·mcpbackend.go).
// 그래서 **wire 위의 모양이 곧 판정의 입력**이고, 그 경계를 미는 시험이 없으면 스큐 구간의
// 거동을 아무도 안 본다. 이 저장소는 그 값을 이미 치렀다
// (fd-ack-reach-silent-under-client-skew — 새 JSON 을 구 클라이언트가 못 읽어 지표가 침묵했다).
func TestReproVerdictSurvivesJSONRoundTrip(t *testing.T) {
	for _, c := range []struct {
		name string
		in   *QueueBalance
		want RateVerdict
	}{
		{"원자료 없음", &QueueBalance{Closed: 1, Open: 3, ReproWindow: 20}, RateUnmeasured},
		{"표본 0", &QueueBalance{Closed: 1, Open: 3, ReproWindow: 20,
			Repro: &store.Reproduction{}}, RateNoSample},
		{"값 있음", &QueueBalance{Closed: 1, Open: 3, ReproWindow: 20,
			Repro: &store.Reproduction{Finishes: 4, Followups: 2, Adds: 1}}, RateMeasured},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw, err := json.Marshal(c.in)
			if err != nil {
				t.Fatalf("직렬화: %v", err)
			}
			var got QueueBalance
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("역직렬화: %v", err)
			}
			if _, v := got.Rate(); v != c.want {
				t.Fatalf("wire 를 건너자 판정이 %v 로 바뀌었다(원하는 것 %v)\nwire: %s", v, c.want, raw)
			}
		})
	}
}

// ★ **옛 서버가 보내는 모양**을 그대로 디코드해 본다.
//
// 0.10~0.12 의 서버는 Repro 가 값 타입이라 집계가 **실패해도** 제로값을 실어 보냈다:
//
//	{"repro":{"Finishes":0,"Followups":0,"Adds":0}, ...}
//
// 신 클라이언트는 그것을 non-nil 로 받는다 — 즉 **원자료의 존재는 "집계가 성공했다"의
// 대리값이 아니다.** 그래서 화면 문장이 그 원인을 단정하면 안 되고(render.go 의 ★),
// 이 시험은 그 wire 가 실제로 RateNoSample 로 떨어진다는 사실을 못박아 둔다 —
// 그 사실을 알고 문장을 골라야 하기 때문이다.
func TestOldServerZeroReproDecodesAsSampleNotFailure(t *testing.T) {
	const oldWire = `{"closed":1,"added":0,"open":3,` +
		`"repro":{"Finishes":0,"Followups":0,"Adds":0},"repro_window":20}`
	var got QueueBalance
	if err := json.Unmarshal([]byte(oldWire), &got); err != nil {
		t.Fatalf("옛 서버 모양 역직렬화 실패: %v", err)
	}
	if got.Repro == nil {
		t.Fatal("옛 서버가 실어 보낸 제로값이 nil 로 들어왔다 — 이 시험의 전제가 틀렸다")
	}
	if _, v := got.Rate(); v != RateNoSample {
		t.Fatalf("옛 서버의 제로값이 %v 로 판정됐다(원하는 것 RateNoSample) — "+
			"이 사실이 바뀌면 화면 문장을 다시 골라야 한다", v)
	}
}
