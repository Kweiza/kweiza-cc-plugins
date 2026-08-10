package mcpsrv

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// finishSpyBackend 는 Finish 가 **불렸는지**만 본다.
//
// ★ Backend 를 embed 하고 둘만 덮는다 — 나머지는 이 경로에서 안 불려야 하고,
// 불리면 nil 역참조로 시끄럽게 죽는 것이 맞다(조용히 통과하는 것보다 낫다).
type finishSpyBackend struct {
	Backend
	called bool
}

func (b *finishSpyBackend) Finish(context.Context, service.FinishInput) (service.FinishResult, error) {
	b.called = true
	return service.FinishResult{
		Item:     model.Item{ID: "x", State: model.ItemDone},
		Judgment: model.Judgment{ID: "J1", Kind: model.JudgmentHandoff, Body: "본문"},
	}, nil
}

func (b *finishSpyBackend) RecentNotes(context.Context, string, int) ([]model.Judgment, error) {
	return nil, nil
}

// resText 는 도구 결과의 본문이다. 단정은 **소비자의 좌표계**로 쓴다(설계 §12) —
// 그 좌표계가 content 블록의 텍스트다.
func resText(r toolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}

func newSpyServer(be Backend) *Server {
	return &Server{
		be:  be,
		log: slog.New(slog.DiscardHandler),
		now: func() time.Time { return time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC) },
	}
}

// ★★ **이 시험이 없으면 관문 배선 한 줄을 지워도 전 패키지가 초록이다.**
// judgeFollowupsArrived 의 단위 시험은 순수 함수만 재므로 toolFinish 가 그것을 **부르는지**는
// 안 잡는다. 직전 브랜치의 전체 리뷰가 정확히 이 부류(배선 두 줄이 시험 밖)를 잡았다 —
// 같은 자리를 다시 열어 두지 않는다.
func TestToolFinishRefusesBeforeTouchingTheBackend(t *testing.T) {
	spy := &finishSpyBackend{}
	s := newSpyServer(spy)

	raw := json.RawMessage(`{"item_id":"x","outcome":"done","body":"b","followups":[]}`)
	res := s.toolFinish(context.Background(), "sess-1", raw)

	if spy.called {
		t.Fatal("거절해야 하는 호출인데 백엔드 Finish 가 불렸다 — 항목이 닫혔을 수 있다. " +
			"관문은 되돌릴 수 없는 쪽보다 **앞**에 서야 한다")
	}
	if !res.IsError {
		t.Errorf("거절인데 오류로 안 표시됐다:\n%s", resText(res))
	}
	if !strings.Contains(resText(res), "항목을 닫지 않았다") {
		t.Errorf("무엇이 안 됐는지를 안 말한다:\n%s", resText(res))
	}
}

// 반대 갈래 — 키가 없으면 관문이 안 막고 그대로 흘러야 한다.
// 이것이 없으면 관문을 "항상 거절"로 바꿔도 위 시험만으로는 안 잡힌다.
func TestToolFinishPassesThroughWhenFollowupsKeyIsAbsent(t *testing.T) {
	spy := &finishSpyBackend{}
	s := newSpyServer(spy)

	raw := json.RawMessage(`{"item_id":"x","outcome":"done","body":"b"}`)
	res := s.toolFinish(context.Background(), "sess-1", raw)

	if !spy.called {
		t.Fatalf("후속이 정말 없는 정상 마무리를 막았다 — 관문이 벽이 됐다:\n%s", resText(res))
	}
	if res.IsError {
		t.Errorf("정상 마무리가 오류로 표시됐다:\n%s", resText(res))
	}
}
