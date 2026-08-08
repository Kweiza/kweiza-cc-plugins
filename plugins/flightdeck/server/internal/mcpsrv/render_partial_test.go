package mcpsrv

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 이 파일이 잠그는 것: 서비스 계층이 "커밋 뒤 보조 조회 실패"를 고백하며 결과를 내게 됐다
// (finish_partial_test.go) — 그 고백이 **화면까지** 와야 한다. 응답 JSON 에만 있으면
// MCP·CLI 세션은 여전히 "받을 세션 없음" · 침묵을 본다.

// 수신자 축이 실패한 note 는 "없다"가 아니라 "못 읽었다"를 말한다.
//
// ★ 이 갈래가 없으면 recipients=nil 이 len==0 으로 접혀 "지금 이 노트를 읽을 다른 세션이
// 없다"가 나간다 — 관측한 적 없는 사실의 단정이고, 0과 못 잼을 가르는 규율의 위반이다.
func TestRenderNoteSaysWhenRecipientsUnread(t *testing.T) {
	r := service.NoteResult{
		Judgment: model.Judgment{ID: "01J", Kind: model.JudgmentDecision, Body: "본문"},
	}
	r.Failures = []service.DerivedFailure{{Axis: "recipients", Detail: "signal 표를 못 읽었다"}}
	got := RenderNote(r)
	if strings.Contains(got, "읽을 다른 세션이 없다") {
		t.Fatalf("수신자를 못 읽었는데 '없다'를 단정했다:\n%s", got)
	}
	if !strings.Contains(got, "못 읽었다") {
		t.Fatalf("수신자 축 실패가 화면에 없다:\n%s", got)
	}
	// 원인 전문이 함께 온다 — renderFailures 가 그것을 나른다.
	if !strings.Contains(got, "recipients") {
		t.Fatalf("어느 축이 실패했는지가 화면에 없다:\n%s", got)
	}
}

// 정상 경로 짝 — 실패가 없으면 옛 문구 그대로다(상시 점등 방지).
func TestRenderNoteUnchangedOnHappyPath(t *testing.T) {
	zero := service.NoteResult{
		Judgment: model.Judgment{ID: "01J", Kind: model.JudgmentDecision, Body: "본문"},
	}
	if got := RenderNote(zero); !strings.Contains(got, "읽을 다른 세션이 없다") {
		t.Fatalf("수신자 0건(정상)인데 '없다' 문구가 사라졌다:\n%s", got)
	}
	some := service.NoteResult{
		Judgment:   model.Judgment{ID: "01J", Kind: model.JudgmentDecision, Body: "본문"},
		Recipients: []string{"01KZAAAAAAAAAAAAAAAAAAAAAA"},
	}
	if got := RenderNote(some); !strings.Contains(got, "1건이 읽는다") {
		t.Fatalf("수신자가 있는데 그 줄이 사라졌다:\n%s", got)
	}
}

// item 축이 실패한 finish 는 커밋된 사실(id·상태)을 말하고 못 읽은 사실을 함께 낸다.
func TestRenderFinishShowsItemAxisFailure(t *testing.T) {
	r := service.FinishResult{
		Item:     model.Item{ID: "aux1", State: model.ItemDone},
		Judgment: model.Judgment{ID: "01J", Kind: model.JudgmentHandoff, Body: "본문"},
	}
	r.Failures = []service.DerivedFailure{{Axis: "item", Detail: "item_after 를 못 읽었다"}}
	got := RenderFinish(r)
	// 커밋된 사실은 그대로 나온다.
	if !strings.Contains(got, "finish · aux1 를 done 로 닫았다") {
		t.Fatalf("커밋된 id·상태가 첫 줄에 없다:\n%s", got)
	}
	// 고백이 나온다.
	if !strings.Contains(got, "못 읽은 파생 1축") || !strings.Contains(got, "item") {
		t.Fatalf("item 축 실패가 화면에 없다:\n%s", got)
	}
}

// 정상 경로 짝 — 실패 0건이면 고백 절이 아예 없다.
func TestRenderFinishSilentWhenNoFailures(t *testing.T) {
	r := service.FinishResult{
		Item:     model.Item{ID: "aux1", State: model.ItemDone, Title: "제목"},
		Judgment: model.Judgment{ID: "01J", Kind: model.JudgmentHandoff, Body: "본문"},
	}
	if got := RenderFinish(r); strings.Contains(got, "못 읽은 파생") {
		t.Fatalf("실패가 없는데 고백 절이 떴다 — 상시 점등은 판별력이 0이다:\n%s", got)
	}
}
