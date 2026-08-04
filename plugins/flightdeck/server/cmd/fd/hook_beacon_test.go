//go:build linux

// ★ 이 파일이 리눅스 전용인 이유. 비콘 대조는 window.StartedOf 로 조상의 시작 시각을
// 읽어야 성립하는데, 그 함수는 리눅스 밖에서 ErrUnsupported 를 낸다(proc_other.go).
// 즉 이 기능 자체가 리눅스에서만 도는 것이라, 다른 플랫폼에서 t.Skip 으로 초록을 내는 대신
// **빌드 대상에서 뺀다** — 스킵은 "돌았는데 통과했다"와 구분이 안 가서 초록의 뜻을 흐린다.
package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/window"
)

// sessionStartPayload 는 SessionStart 훅 stdin 한 벌이다.
// /clear 는 **같은 창에 새 session_id** 로 온다 — 그것이 이 시험이 흉내내는 것 전부다.
func sessionStartPayload(cc, cwd string) string {
	raw, err := json.Marshal(map[string]string{
		"session_id":      cc,
		"cwd":             cwd,
		"hook_event_name": "SessionStart",
		"source":          "clear",
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// cardsFor 는 이 (machine, worktree) 의 카드를 **서버가 실제로 갖게 된 것**에서 센다.
//
// ★ 렌더된 배너로 세지 않는다. 배너 문자열로 단정하면 판정기가 도는 순간 전제도 함께
// 통과하는 순환 전제가 되고(수리가 아무것도 안 해도 초록이다), 직전 세션이 여기서 걸렸다.
func cardsFor(t *testing.T, h *harness, machine, worktree string) []model.Session {
	t.Helper()
	all, err := h.st.ListSessions(context.Background(), h.project)
	if err != nil {
		t.Fatalf("세션 전수 조회 실패: %v", err)
	}
	var out []model.Session
	for _, s := range all {
		if s.MachineID == machine && s.Worktree == worktree {
			out = append(out, s)
		}
	}
	return out
}

// ★ 이 기능의 인수 시험이다. /clear 를 훅 페이로드의 session_id 를 바꿔 흉내낸다.
//
// 단정은 **서비스를 직접 쳐서** 한다(cardsFor · ClaimedItems).
func TestClearKeepsOneCardAndItsClaim(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	cwd := t.TempDir() // 두 훅이 **같은 워크트리**여야 3중키가 성립한다

	// 하네스가 실제로 볼 좌표를 App 에서 그대로 읽는다 — 시험이 사본을 만들면 어긋난다.
	app := newApp(envOf(h.env), quietLogger(), cwd, strings.NewReader(""))
	if app.beaconDir == "" {
		t.Fatal("대조 전제가 깨졌다 — App.beaconDir 가 비었다")
	}
	if !strings.HasPrefix(app.beaconDir, h.state) {
		t.Fatalf("비콘 디렉토리가 하네스 밖이다(%s) — 시험이 사용자의 진짜 홈을 건드린다",
			app.beaconDir)
	}
	wt := app.proj.Worktree
	if wt == "" {
		t.Fatal("대조 전제가 깨졌다 — 워크트리가 비었다")
	}

	// ① MCP 심기를 흉내낸다. 조상 pid 는 이 시험 프로세스 자신이다
	//    (window.Ancestors 는 자기 자신을 사슬에 포함한다).
	pid := os.Getpid()
	started, err := window.StartedOf(pid)
	if err != nil {
		t.Fatalf("이 프로세스의 시작 시각을 못 읽었다: %v", err)
	}
	key := window.Key{MachineID: app.machine, ClaudePID: pid, Started: started}
	if _, err := window.Plant(app.beaconDir, key, wt, "cc-old", time.Now()); err != nil {
		t.Fatalf("비콘 심기 실패: %v", err)
	}

	// ② SessionStart 를 cc-old 로 한 번.
	if code, out := h.run(sessionStartPayload("cc-old", cwd), "hook", "session-start"); code != 0 {
		t.Fatalf("첫 SessionStart 훅 종료코드 %d: %s", code, out)
	}
	cards := cardsFor(t, h, app.machine, wt)
	if len(cards) != 1 {
		t.Fatalf("첫 훅 뒤 카드가 %d장이다 — 전제가 안 섰다: %+v", len(cards), cards)
	}
	cardA := cards[0]
	if cardA.CCSessionID != "cc-old" {
		t.Fatalf("카드 A 의 cc 가 %q 다", cardA.CCSessionID)
	}
	// 훅이 비콘에 카드 id 를 적어 둬야 다음 전환에서 그것이 rekey 대상이 된다.
	b, err := window.Load(app.beaconDir, key)
	if err != nil {
		t.Fatalf("첫 훅 뒤 비콘을 못 읽었다: %v", err)
	}
	if b.SessionID != cardA.ID {
		t.Fatalf("비콘의 session_id 가 %q 다 — 카드 %q 를 기대했다. "+
			"훅이 window.SaveIdentity 로 정체를 안 적었다는 신호다", b.SessionID, cardA.ID)
	}

	// ③ 카드 A 로 항목 하나를 선점한다.
	const itemID = "t12-drift-item"
	if err := h.st.AddItem(ctx, model.Item{
		Project: h.project, ID: itemID, Title: "표류 수리", Body: "본문", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("항목 등록 실패: %v", err)
	}
	if _, err := h.st.ClaimItem(ctx, h.project, itemID, cardA.ID); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	if got, _ := h.st.ClaimedItems(ctx, cardA.ID); len(got) != 1 {
		t.Fatalf("전제가 깨졌다 — 카드 A 의 선점이 없다: %v", got)
	}

	// ④ /clear — 같은 창, 새 대화 id 로 SessionStart 를 다시.
	if code, out := h.run(sessionStartPayload("cc-new", cwd), "hook", "session-start"); code != 0 {
		t.Fatalf("둘째 SessionStart 훅 종료코드 %d: %s", code, out)
	}

	// ⑤ 이 (machine, worktree) 의 카드는 **한 장**이다.
	cards = cardsFor(t, h, app.machine, wt)
	if len(cards) != 1 {
		ids := make([]string, 0, len(cards))
		for _, c := range cards {
			ids = append(ids, c.ID+"/"+c.CCSessionID)
		}
		t.Fatalf("/clear 뒤 카드가 %d장이다(%s) — 훅이 OpenSession 앞에서 rekey 로 "+
			"기존 카드를 안 고쳤다는 신호다", len(cards), strings.Join(ids, " "))
	}

	// ⑥ 그 카드는 **카드 A 그대로**이고 cc 만 새것이다.
	if cards[0].ID != cardA.ID {
		t.Fatalf("남은 카드가 %q 다 — 카드 A(%q)가 아니다. 새 카드가 생기고 A 가 사라진 것이라면 "+
			"선점과 판단이 통째로 딴 카드에 붙는다", cards[0].ID, cardA.ID)
	}
	if cards[0].CCSessionID != "cc-new" {
		t.Fatalf("카드의 cc 가 %q 다 — cc-new 를 기대했다", cards[0].CCSessionID)
	}

	// ⑦ 선점이 그대로 그 카드에 붙어 있다.
	claimed, err := h.st.ClaimedItems(ctx, cardA.ID)
	if err != nil {
		t.Fatalf("선점 조회 실패: %v", err)
	}
	if len(claimed) != 1 || claimed[0] != itemID {
		t.Fatalf("/clear 뒤 카드 A 의 선점이 %v 다 — [%s] 를 기대했다", claimed, itemID)
	}

	// 다음 전환의 재료: 비콘이 이번 cc 를 들고 있어야 한다.
	if b, err = window.Load(app.beaconDir, key); err != nil {
		t.Fatalf("둘째 훅 뒤 비콘을 못 읽었다: %v", err)
	}
	if b.CCSessionID != "cc-new" || b.SessionID != cardA.ID {
		t.Fatalf("비콘이 cc=%q session=%q 다 — cc-new/%s 를 기대했다. "+
			"다음 /clear 에서 이 값이 rekey 대상이다", b.CCSessionID, b.SessionID, cardA.ID)
	}
}

// 비콘이 없으면 오늘 거동 그대로 카드가 두 장이다.
//
// ★ 이 시험이 지키는 것은 **새 실패 모드가 없다**는 것이다. 비콘을 못 찾는 것은 오류가
// 아니라 폴백이고(설계 §5), 그 자리에서 훅이 조용히 죽거나 카드를 0장으로 만들면
// 오늘보다 나빠진다. 두 장은 이 기능이 고치려는 결함이지 이 커밋이 만든 결함이 아니다.
func TestNoBeaconMeansTodaysBehaviour(t *testing.T) {
	h := newHarness(t)
	cwd := t.TempDir()

	app := newApp(envOf(h.env), quietLogger(), cwd, strings.NewReader(""))
	// 대조 전제: 비콘이 정말 하나도 없다. 있으면 이 시험은 아무것도 안 지킨다.
	if ents, err := os.ReadDir(app.beaconDir); err == nil && len(ents) > 0 {
		t.Fatalf("전제가 깨졌다 — 비콘 디렉토리(%s)에 %d개가 있다", app.beaconDir, len(ents))
	}
	wt := app.proj.Worktree

	if code, out := h.run(sessionStartPayload("cc-old", cwd), "hook", "session-start"); code != 0 {
		t.Fatalf("첫 SessionStart 훅 종료코드 %d: %s", code, out)
	}
	if code, out := h.run(sessionStartPayload("cc-new", cwd), "hook", "session-start"); code != 0 {
		t.Fatalf("둘째 SessionStart 훅 종료코드 %d: %s", code, out)
	}

	cards := cardsFor(t, h, app.machine, wt)
	if len(cards) != 2 {
		t.Fatalf("비콘 없이 cc 를 갈았는데 카드가 %d장이다 — 2장(오늘 거동)이어야 한다. "+
			"비콘 없는 경로에서 훅이 뭔가를 더 하고 있다는 신호다", len(cards))
	}
	// 그리고 훅은 여전히 아무 비콘도 안 만든다 — 심는 것은 MCP 의 일이다.
	if ents, err := os.ReadDir(app.beaconDir); err == nil && len(ents) > 0 {
		t.Fatalf("비콘이 없던 자리에 훅이 %d개를 만들었다 — 심기는 MCP 의 일이다", len(ents))
	}
}
