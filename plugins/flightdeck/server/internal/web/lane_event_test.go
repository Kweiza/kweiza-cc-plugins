package web

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"
)

// lane.release 이벤트가 형제들과 **같은 어휘**를 쓰는지 본다.
//
// ★ 왜 이것이 시험할 값인가. event 는 **추가 전용**이라 한 번 쌓인 행은 영원히 그
// 모양으로 남는다. 형제 이벤트(lane.land 등)는 "state" 에 문자열을 싣고 mode 는
// payload 가 아니라 이벤트 kind 에 넣는데(`"lane."+mode`), 회수만 "state" 에 불리언을
// "mode" 에 사람 이름을 실었다. 즉 같은 키가 이벤트마다 다른 것을 뜻하고,
// `str("state")` 로 읽는 이 레포의 관용적 소비자는 조용히 빈 문자열을 받는다.
//
// 지금 고치지 않으면 옛 모양 행이 계속 늘어난다 — 미루는 것 자체가 비용이다.
func TestLaneReleaseEventUsesDistinctKeys(t *testing.T) {
	lf := newLaneFixture(t)
	ctx := context.Background()

	rec := lf.post("/actions/lane-release", url.Values{
		"project": {testProject},
		"item":    {"2"},
		"reason":  {"이벤트 모양을 재는 회수다"},
	})
	if rec.Code != 303 {
		t.Fatalf("회수가 %d 다: %s", rec.Code, rec.Body.String())
	}

	evs, err := lf.st.ListEvents(ctx, "lane.release", time.Time{}, 10)
	if err != nil {
		t.Fatalf("이벤트 조회 실패: %v", err)
	}
	if len(evs) == 0 {
		t.Fatal("lane.release 이벤트가 원장에 없다")
	}

	var p map[string]any
	if err := json.Unmarshal([]byte(evs[0].Payload), &p); err != nil {
		t.Fatalf("payload 를 못 읽었다(%q): %v", evs[0].Payload, err)
	}

	// ① 사람 이름은 "actor" 에 실린다. "mode" 는 형제 이벤트에서 acquire|report|leave 를
	//    뜻하는 자리라, 거기에 사람을 실으면 같은 키가 두 가지를 뜻하게 된다.
	if _, bad := p["mode"]; bad {
		t.Errorf(`payload 에 "mode" 가 있다 — 형제 이벤트에서 그 키는 acquire|report|leave 다. `+
			`사람은 "actor" 로 실어라: %v`, p)
	}
	if _, ok := p["actor"]; !ok {
		t.Errorf(`payload 에 "actor" 가 없다: %v`, p)
	}

	// ② 줄 행 번호는 남아야 한다 — 이 이벤트가 무엇에 대한 것인지가 그 값이다.
	if _, ok := p["row"]; !ok {
		t.Errorf(`payload 에 "row" 가 없다 — 어느 줄 행인지가 사라졌다: %v`, p)
	}
}
