package mcpsrv

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 이 파일이 잠그는 것: **판단이 남의 프로젝트 항목에 걸렸다는 사실이 화면에 온다.**
//
// 근거는 새로 만든 규율이 아니라 이미 있는 것이다 — RenderAdd 의 ★ 가 같은 병을 적어 뒀다.
// 프로젝트 좌표를 화면이 한 글자도 안 내는 바람에 항목 10건이 남의 프로젝트에 등록됐고,
// 그중 하나(fd-item-move)는 id 가 전역 유일이라 회수되지 않아 이름이 영구히 죽었다.
// note 는 그보다 더 나쁘다: 판단은 추가 전용이라 잘못 걸려도 **되돌릴 수 없다.**

func crossResult() service.NoteResult {
	return service.NoteResult{
		Judgment:     model.Judgment{ID: "01J", Kind: model.JudgmentDecision, Body: "본문"},
		CrossProject: "context-platform",
	}
}

// 교차로 걸렸으면 **어느 프로젝트**인지를 말한다.
func TestRenderNoteNamesTheProjectACrossLinkLandedIn(t *testing.T) {
	got := RenderNote(crossResult())
	if !strings.Contains(got, "context-platform") {
		t.Fatalf("남의 프로젝트 항목에 걸렸는데 화면이 그 좌표를 안 낸다:\n%s", got)
	}
}

// 자기 프로젝트면 그 줄이 **안** 나온다 — 항상 찍으면 배경이 되고 배경은 아무도 안 읽는다
// (render.go:597 의 그 규율).
func TestRenderNoteStaysQuietWhenTheLinkIsLocal(t *testing.T) {
	r := crossResult()
	r.CrossProject = ""
	if got := RenderNote(r); strings.Contains(got, "프로젝트") {
		t.Fatalf("자기 프로젝트 판단인데 교차 문구가 나왔다:\n%s", got)
	}
}

// 수신자 축이 실패해도 교차 사실은 남는다.
//
// ★ 이 갈래가 이 시험의 존재 이유다. 실패 갈래는 **조기 반환**이라, 교차 줄을 그 뒤에
// 놓으면 화면에서 조용히 사라진다 — 하필 "무언가 잘못됐다"를 보는 순간에 좌표가 없어진다.
func TestRenderNoteKeepsCrossProjectWhenRecipientsUnread(t *testing.T) {
	r := crossResult()
	r.Failures = []service.DerivedFailure{{Axis: "recipients", Detail: "signal 표를 못 읽었다"}}
	got := RenderNote(r)
	if !strings.Contains(got, "context-platform") {
		t.Fatalf("수신자 실패 갈래에서 교차 좌표가 사라졌다:\n%s", got)
	}
	if !strings.Contains(got, "다시 부르지 마라") {
		t.Fatalf("실패 갈래의 재호출 금지 경고까지 잃었다:\n%s", got)
	}
}

// 받을 세션이 있어도 교차 사실은 함께 온다 — 그쪽도 조기 반환 갈래다.
func TestRenderNoteKeepsCrossProjectAlongsideRecipients(t *testing.T) {
	r := crossResult()
	r.Recipients = []string{"01SESSIONAAAAAAAAAAAAAAAAA"}
	got := RenderNote(r)
	if !strings.Contains(got, "context-platform") {
		t.Fatalf("수신자가 있는 갈래에서 교차 좌표가 빠졌다:\n%s", got)
	}
	if !strings.Contains(got, "읽는다") {
		t.Fatalf("수신자 줄이 사라졌다:\n%s", got)
	}
}
