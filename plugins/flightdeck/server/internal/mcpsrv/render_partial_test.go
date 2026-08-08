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
	// ★ 재호출 금지 경고는 이 갈래의 존재 이유다 — 판단은 커밋됐으므로 다시 부르면
	// 추가 전용 표에 중복이 남는다. 이 문장이 빠지면 세션이 "못 읽었다"를 보고 같은
	// note 를 다시 부른다.
	if !strings.Contains(got, "다시 부르지 마라") {
		t.Fatalf("재호출 금지 경고가 화면에 없다:\n%s", got)
	}
}

// note 쪽 실패 절단도 소리 내어 말한다 — 침묵 절단은 "다 보여줬다"로 읽힌다.
func TestRenderNoteClipsFailuresAloud(t *testing.T) {
	r := service.NoteResult{
		Judgment: model.Judgment{ID: "01J", Kind: model.JudgmentDecision, Body: "본문"},
	}
	r.Failures = []service.DerivedFailure{
		{Axis: "recipients", Detail: "d1"}, {Axis: "a2", Detail: "d2"},
		{Axis: "a3", Detail: "d3"}, {Axis: "a4", Detail: "d4"},
	}
	got := RenderNote(r)
	if !strings.Contains(got, "못 읽은 파생 4축") {
		t.Fatalf("실패 총수가 화면에 없다:\n%s", got)
	}
	if !strings.Contains(got, "1축 더") {
		t.Fatalf("한도 절단이 침묵했다 — 4축 중 3축만 내면 그 사실을 말해야 한다:\n%s", got)
	}
}

// 다른 축의 파생 실패가 섞여도 수신자 갈래는 안 바뀐다 — 판정은 recipients 축**만** 본다.
//
// ★ 이 갈래가 없으면 hasFailureAxis 판정을 `len(Failures) > 0` 으로 바꾸는 변이가
// 살아남는다. 그러면 카드 파생 중 워크트리 하나를 못 읽었을 뿐인 note(수신자는 정상)가
// "받을 세션은 못 읽었다 — 다시 부르지 마라"를 내어, 멀쩡히 관측한 수신자 목록을 버린다.
func TestRenderNoteIgnoresUnrelatedFailureAxes(t *testing.T) {
	r := service.NoteResult{
		Judgment:   model.Judgment{ID: "01J", Kind: model.JudgmentDecision, Body: "본문"},
		Recipients: []string{"01KZAAAAAAAAAAAAAAAAAAAAAA"},
	}
	r.Failures = []service.DerivedFailure{{Axis: "worktrees", Detail: "트리 하나를 못 읽었다"}}
	got := RenderNote(r)
	if !strings.Contains(got, "1건이 읽는다") {
		t.Fatalf("수신자는 정상 관측됐는데 그 줄이 사라졌다 — 판정이 recipients 축이 아니라 "+
			"실패 유무 전체를 본 것이다:\n%s", got)
	}
	if strings.Contains(got, "다시 부르지 마라") {
		t.Fatalf("무관한 축의 실패에 재호출 금지 경고가 떴다:\n%s", got)
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

// 실패가 한도(3)를 넘으면 절단을 **말한다** — 침묵 절단은 "다 보여줬다"로 읽힌다.
func TestRenderFinishClipsFailuresAloud(t *testing.T) {
	r := service.FinishResult{
		Item:     model.Item{ID: "aux1", State: model.ItemDone},
		Judgment: model.Judgment{ID: "01J", Kind: model.JudgmentHandoff, Body: "본문"},
	}
	r.Failures = []service.DerivedFailure{
		{Axis: "a1", Detail: "d1"}, {Axis: "a2", Detail: "d2"},
		{Axis: "a3", Detail: "d3"}, {Axis: "a4", Detail: "d4"},
	}
	got := RenderFinish(r)
	if !strings.Contains(got, "못 읽은 파생 4축") {
		t.Fatalf("실패 총수가 화면에 없다:\n%s", got)
	}
	if !strings.Contains(got, "1축 더") {
		t.Fatalf("한도 절단이 침묵했다 — 4축 중 3축만 내면 그 사실을 말해야 한다:\n%s", got)
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
