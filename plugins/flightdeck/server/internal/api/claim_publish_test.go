package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 선점이 발행하는 이벤트의 **세 값**을 못박는다 — 종류(res.Mode) · 어느 항목 · 겹침 몇 건.
//
// 셋 다 상수로 바꿔도 전 스위트가 초록이었다(실측 2026-08-09, 항목
// fd-pick-response-lines-still-unpinned). SSE 소비자가 payload 를 안 읽고 전체 reload
// 하는 지금은 값이 낮지만, 이건 **와이어 계약**이라 소비자가 하나만 늘어도 바로 값이 생긴다.
// 그리고 그때는 이미 그 세 값이 거짓인 채 굳어 있게 된다.

// nextEvent 는 구독자가 받은 다음 이벤트 한 건이다.
//
// 좌표계는 **와이어 한 줄**이다(이 패키지의 규율) — 허브의 내부 구조체가 아니라
// 구독자에게 실제로 나간 바이트를 되읽는다. 그래서 직렬화가 값을 떨어뜨리면 그것도 잡힌다.
func nextEvent(t *testing.T, sub *Sub) Event {
	t.Helper()
	select {
	case raw := <-sub.ch:
		for _, line := range strings.Split(string(raw), "\n") {
			data, ok := strings.CutPrefix(line, "data: ")
			if !ok {
				continue
			}
			var ev Event
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				t.Fatalf("이벤트 data 를 JSON 으로 못 읽었다: %v\n%s", err, data)
			}
			return ev
		}
		t.Fatalf("프레임에 data 줄이 없다: %q", raw)
	case <-time.After(2 * time.Second):
		t.Fatal("이벤트가 안 왔다")
	}
	return Event{}
}

// TestClaimEventNamesTheModeTheItemAndTheOverlapCount 는 선점 이벤트가 나르는 세 값이
// 전부 **그 요청의 실제 값**임을 못박는다.
//
//	kind     — "item."+res.Mode. 새 선점과 재개는 다른 사건이다(PickMode 의 계약:
//	           뭉개면 "쥐고 있다"와 "방금 잡았다"가 같은 화면이 된다).
//	item     — 경로의 항목 id. 상수면 소비자가 **어느 카드를 다시 그릴지** 모른다.
//	overlaps — 이 선점이 남과 부딪힌 건수.
//
// 세 축 모두 짝으로 본다 — 값이 서로 다른 두 요청을 나란히 재지 않으면, 한쪽 값을
// 우연히 담은 상수가 전부 통과한다.
func TestClaimEventNamesTheModeTheItemAndTheOverlapCount(t *testing.T) {
	e := newEnv(t, nil)
	mine := e.openSession("cc-mine")
	theirs := e.openSession("cc-theirs")

	add := func(id, sess string, paths []string) {
		t.Helper()
		w := e.write(http.MethodPost, "/api/v1/items", map[string]any{
			"project": testProject, "session_id": sess, "id": id,
			"title": id + " 제목", "body": id + " 본문", "paths": paths,
		})
		if w.Code != http.StatusCreated && w.Code != http.StatusOK {
			t.Fatalf("항목 등록 실패(%s): %d %s", id, w.Code, w.Body.String())
		}
	}
	claim := func(id, sess string) *httptest.ResponseRecorder {
		t.Helper()
		w := e.write(http.MethodPost, "/api/v1/items/"+id+"/claim", map[string]any{
			"project": testProject, "session_id": sess,
		})
		if w.Code != http.StatusOK {
			t.Fatalf("선점 실패(%s): %d %s", id, w.Code, w.Body.String())
		}
		return w
	}
	overlapCount := func(w *httptest.ResponseRecorder) float64 {
		t.Helper()
		list, _ := decodeBody(t, w)["overlaps"].([]any)
		return float64(len(list))
	}

	// 같은 경로를 선언한 항목 둘. 남이 하나를 쥐면 그 경로가 남의 발자국이 되고,
	// 내가 나머지를 쥘 때 겹침이 **1건 이상**으로 관측된다.
	add("theirs-item", theirs, []string{"shared/x.go"})
	add("mine-item", mine, []string{"shared/x.go"})
	add("second-item", mine, nil) // 경로가 없어 겹치지 않는다 — 겹침 축의 짝
	claim("theirs-item", theirs)

	// 준비가 끝난 뒤에 구독한다 — 그래야 아래 세 이벤트만 큐에 있다.
	sub := e.srv.hub.Subscribe(testProject)
	defer e.srv.hub.Unsubscribe(sub)

	// ① 새 선점 — 겹치는 판이다.
	w := claim("mine-item", mine)
	want := overlapCount(w)
	// 대조 먼저: 겹침이 실제로 생겼나. 0 이면 아래 단정이 "0 을 상수로 박은" 변이에
	// 대해 공짜로 참이 되고, 이 시험은 겹침 축을 아예 안 재게 된다.
	if want == 0 {
		t.Fatalf("겹침이 0건이다 — 이 시험의 대조축이 성립하지 않았다: %s", w.Body.String())
	}
	ev := nextEvent(t, sub)
	if ev.Kind != "item.claimed" {
		t.Fatalf("새 선점이 %q 로 발행됐다(기대 item.claimed)", ev.Kind)
	}
	if ev.Detail["item"] != "mine-item" {
		t.Fatalf("이벤트가 가리키는 항목이 %v 다(기대 mine-item)", ev.Detail["item"])
	}
	if ev.Detail["overlaps"] != want {
		t.Fatalf("이벤트의 겹침이 %v 인데 응답은 %v 다 — 같은 요청의 두 표면이 갈렸다",
			ev.Detail["overlaps"], want)
	}

	// ② 재개 — **쓰기가 없는 사건**이라 이름이 달라야 한다. 같은 이름으로 접히면
	// 소비자는 아무도 아무것도 안 잡은 순간을 새 선점으로 읽는다.
	claim("mine-item", mine)
	resumed := nextEvent(t, sub)
	if resumed.Kind != "item.resumed" {
		t.Fatalf("재개가 %q 로 발행됐다(기대 item.resumed) — 라벨이 mode 를 안 따른다", resumed.Kind)
	}
	if resumed.Detail["item"] != "mine-item" {
		t.Fatalf("재개 이벤트가 가리키는 항목이 %v 다(기대 mine-item)", resumed.Detail["item"])
	}

	// ③ 다른 항목 · 겹침 없음 — item 과 overlaps 두 축의 짝이다.
	other := claim("second-item", mine)
	otherWant := overlapCount(other)
	if otherWant != 0 {
		t.Fatalf("경로 없는 항목의 겹침이 %v 다(기대 0)", otherWant)
	}
	ev3 := nextEvent(t, sub)
	if ev3.Detail["item"] != "second-item" {
		t.Fatalf("이벤트가 가리키는 항목이 %v 다(기대 second-item) — 항목 id 가 상수다",
			ev3.Detail["item"])
	}
	if ev3.Detail["overlaps"] != otherWant {
		t.Fatalf("겹치지 않는 선점의 이벤트 겹침이 %v 다(기대 %v)",
			ev3.Detail["overlaps"], otherWant)
	}
}
