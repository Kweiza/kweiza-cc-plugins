package mcpsrv

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// noteSpyBackend 는 Note 가 **불렸는지**만 본다. finishSpyBackend 와 같은 규율이다 —
// Backend 를 embed 하고 이 경로에서 불려야 하는 것만 덮는다.
type noteSpyBackend struct {
	Backend
	called bool
	beats  int
}

func (b *noteSpyBackend) Note(context.Context, service.NoteInput) (service.NoteResult, error) {
	b.called = true
	return service.NoteResult{Judgment: model.Judgment{ID: "J1", Kind: model.JudgmentAsk, Body: "본문"}}, nil
}

func (b *noteSpyBackend) Beat(context.Context, string, model.SignalKind, []string) error {
	b.beats++
	return nil
}

func (b *noteSpyBackend) RecentNotes(context.Context, string, int) ([]model.Judgment, error) {
	return nil, nil
}

// gatedServer 는 정체가 온전한 서버다. sessionID 를 미리 채워 ensureSession 이
// 백엔드를 안 타게 한다 — 이 시험이 재는 것은 세션 열기가 아니라 **관문 배선**이다.
func gatedServer(be Backend) *Server {
	s := newSpyServer(be)
	s.id = Identity{ProjectID: "p", MachineID: "m", CCSessionID: "cc-1", Worktree: "/tmp/wt"}
	s.sessionID = "sess-1"
	return s
}

// ★★ **이 시험이 없으면 callTool 의 관문 세 줄을 지워도 전 패키지가 초록이다.**
// judgeArgSwallowed 의 단위 시험은 순수 함수만 재므로 callTool 이 그것을 **부르는지**는
// 안 잡는다. followups 관문이 같은 이유로 같은 모양의 배선 시험을 이미 진다.
//
// note 로 재는 이유: 원장에서 이 모양으로 죽은 19건이 전부 판단이고, 그 중 18건이
// item_id 를 잃어 **어느 항목에도 안 걸렸다.** 되돌릴 수 없는 쪽이 그쪽이다.
func TestCallToolRefusesASwallowedArgBeforeTouchingTheBackend(t *testing.T) {
	spy := &noteSpyBackend{}
	s := gatedServer(spy)

	raw := json.RawMessage(`{"kind":"ask",` +
		`"body":"본문이다.</body>\n<parameter name=\"item_id\">e2e-sa-owners-roundtrip"}`)
	res := s.callTool(context.Background(), "note", raw)

	if spy.called {
		t.Fatal("거절해야 하는 호출인데 백엔드 Note 가 불렸다 — 항목 링크 없는 판단이 원장에 남는다. " +
			"관문은 되돌릴 수 없는 쪽보다 **앞**에 서야 한다")
	}
	if !res.IsError {
		t.Errorf("거절인데 오류로 안 표시됐다:\n%s", resText(res))
	}
	for _, want := range []string{"body", "item_id", "아무것도 쓰지 않았다"} {
		if !strings.Contains(resText(res), want) {
			t.Errorf("사유에 %q 가 없다:\n%s", want, resText(res))
		}
	}
	// ★ 거절당한 호출도 **살아 있는 세션이 낸 것**이다. 관문이 mcp 신호보다 앞으로 밀리면
	//   마크업을 못 고치는 세션이 보드에서 침묵하고 사람이 죽었다고 읽는다 —
	//   "창은 표시 구간이지 생존 판정이 아니다"가 성립하려면 신호 자체는 참이어야 한다.
	if spy.beats == 0 {
		t.Error("거절하면서 mcp 신호를 안 남겼다 — 살아 있는 세션이 보드에서 침묵한다. " +
			"관문은 신호보다 **뒤**, 디스패치보다 **앞**이다")
	}
}

// 반대 갈래 — 정상 호출은 그대로 흘러야 한다.
// 이것이 없으면 관문을 "항상 거절"로 바꿔도 위 시험만으로는 안 잡힌다.
func TestCallToolPassesThroughACleanNote(t *testing.T) {
	spy := &noteSpyBackend{}
	s := gatedServer(spy)

	raw := json.RawMessage(`{"kind":"ask","body":"본문이다","item_id":"e2e-sa-owners-roundtrip"}`)
	res := s.callTool(context.Background(), "note", raw)

	if !spy.called {
		t.Fatalf("정상 판단을 막았다 — 관문이 벽이 됐다:\n%s", resText(res))
	}
	if res.IsError {
		t.Errorf("정상 판단이 오류로 표시됐다:\n%s", resText(res))
	}
}
