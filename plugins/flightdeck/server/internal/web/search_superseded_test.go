package web

import (
	"context"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 판단 검색(⑥)은 정정된 것도 **낸다**. 다만 정정됐다고 **말해야** 한다.
//
// 거르는 것은 알림 축(service.liveNotesOfKind)의 몫이고 여기는 "무슨 일이 있었나"를
// 답하는 이력 축이라 전수가 맞다(4dd0306 이 그 둘을 갈랐다). 그런데 전수를 내면서
// 표식이 없으면, 사람이 **이미 철회된 판단을 읽고 틀린 전제로 간다** — 이 저장소에서
// 실제로 난 사고의 형태다(옛 절 이름으로 좌표를 잡았다가 네 번 밀린 일).
func TestSearchMarksSupersededJudgments(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	sess := f.openSession("cc-1", "트랙2")
	ctx := context.Background()

	first, err := f.svc.Note(ctx, service.NoteInput{
		Project: testProject, SessionID: sess.ID, Kind: model.JudgmentHandoff,
		Title: "옛 좌표", Body: "내 세 자리는 §5·§8·§11 이다",
	})
	if err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}
	second, err := f.svc.Note(ctx, service.NoteInput{
		Project: testProject, SessionID: sess.ID, Kind: model.JudgmentHandoff,
		Title: "정정문", Body: "§5 라 부른 것은 실제로 §8 안이다",
		Supersedes: first.Judgment.ID,
	})
	if err != nil {
		t.Fatalf("정정 저장 실패: %v", err)
	}

	code, html := f.get("")
	if code != 200 {
		t.Fatalf("화면이 %d 다", code)
	}
	search := sectionOf(t, html, "search")

	// ① 전수는 유지된다 — 이력 축이라 거르면 안 된다.
	mustContain(t, search, "옛 좌표", "정정당한 판단이 검색에서 사라졌다 — 이력 축은 전수여야 한다")
	mustContain(t, search, "정정문", "정정문이 검색에 없다")

	// ② 옛 행은 정정됐다고 말한다. 이 표식에는 **역참조**가 필요하다 —
	//    옛 행 자체는 자기가 대체됐다는 것을 모른다(supersedes 는 새 행이 건다).
	mustContain(t, search, "정정됨", "정정당한 행에 표식이 없다 — 철회된 판단인지 화면으로 알 길이 없다")
	mustContain(t, search, short(second.Judgment.ID),
		"무엇이 대체했는지가 안 보인다 — 표식만 있고 갈 곳이 없으면 반쪽이다")

	// ③ 정정문 쪽은 무엇을 대체했는지 말한다. mcpsrv/render.go 는 이미 그 한 줄을 낸다.
	mustContain(t, search, short(first.Judgment.ID),
		"정정문이 무엇을 대체했는지 안 밝힌다")
}

// 정정이 없으면 표식도 없다 — 표식이 늘 붙으면 아무 뜻이 없다.
func TestSearchDoesNotMarkPlainJudgments(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	sess := f.openSession("cc-1", "트랙2")

	if _, err := f.svc.Note(context.Background(), service.NoteInput{
		Project: testProject, SessionID: sess.ID, Kind: model.JudgmentHandoff,
		Title: "평범한 핸드오프", Body: "정정한 적도 정정당한 적도 없다",
	}); err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}

	_, html := f.get("")
	search := sectionOf(t, html, "search")
	mustContain(t, search, "평범한 핸드오프", "판단이 검색에 없다")
	mustNotContain(t, search, "정정됨", "정정한 적 없는 판단에 표식이 붙었다")
	mustNotContain(t, search, "를 대체", "정정한 적 없는 판단이 무언가를 대체했다고 말한다")
}
