//go:build linux

// ★ 이 파일이 리눅스 전용인 이유. 비콘 대조는 window.StartedOf 로 조상의 시작 시각을
// 읽어야 성립하는데, 그 함수는 리눅스 밖에서 ErrUnsupported 를 낸다(proc_other.go).
// 즉 이 기능 자체가 리눅스에서만 도는 것이라, 다른 플랫폼에서 t.Skip 으로 초록을 내는 대신
// **빌드 대상에서 뺀다** — 스킵은 "돌았는데 통과했다"와 구분이 안 가서 초록의 뜻을 흐린다.
package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/window"
)

// ★ sessionStartPayload 는 여기 없다 — hook_test.go 에 있다. 이 파일이 리눅스 전용이라
// 여기 두면 그 헬퍼도 리눅스 전용이 되고, 실제로 bincache_test.go(무태그)가 그것을 부르는
// 순간 리눅스 시험은 초록인데 `GOOS=darwin go vet` 만 빨간불이 났다. 훅 stdin 한 벌은
// 플랫폼 축과 아무 상관이 없으므로 무태그 자리에 산다.

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

// ★★ **MCP 가 늦게 심으면 첫 /clear 가 고아를 만든다.**
//
// 비콘의 session_id 를 적는 것은 훅뿐이고(window.SaveIdentity), 그 훅은 비콘을 **찾은 뒤에만**
// 적는다. 그래서 심기가 첫 SessionStart 보다 늦으면 그 자리는 빈 채로 남는다. 늦는 것은
// 가정이 아니다 — 설계 개정 ②의 실측이 `fd mcp` 가 부모 claude 보다 2,374,680틱(≈6.6시간)
// 늦게 뜬 것을 재고 있다(플러그인 갱신 등으로 MCP 만 다시 뜬다).
//
// 그 상태에서 /clear 가 오면 rekey 가 **대상 카드를 몰라** 건너뛰고 카드가 두 장이 된다.
// 둘째 전환부터는 스스로 낫지만, 그때 고아가 되는 카드가 하필 **첫 구간의 선점과 판단을
// 든 카드**다. 비콘은 옛 cc 를 알고 있고, 3중키 upsert 는 그 cc 로 부르면 카드 A 를
// 그대로 돌려준다 — 대상을 못 찾을 이유가 없다.
func TestALatePlantStillMergesTheFirstClear(t *testing.T) {
	h := newHarness(t)
	cwd := t.TempDir()

	app := newApp(envOf(h.env), quietLogger(), cwd, strings.NewReader(""))
	wt := app.proj.Worktree
	if app.beaconDir == "" || wt == "" {
		t.Fatal("대조 전제가 깨졌다 — 비콘 디렉토리나 워크트리가 비었다")
	}

	// ① 첫 SessionStart. **비콘이 아직 없다** — MCP 가 안 떴다.
	if code, out := h.run(sessionStartPayload("cc-old", cwd), "hook", "session-start"); code != 0 {
		t.Fatalf("첫 SessionStart 훅 종료코드 %d: %s", code, out)
	}
	cards := cardsFor(t, h, app.machine, wt)
	if len(cards) != 1 {
		t.Fatalf("첫 훅 뒤 카드가 %d장이다 — 전제가 안 섰다: %+v", len(cards), cards)
	}
	cardA := cards[0]

	// ② 이제서야 MCP 가 뜨고 심는다. 심기는 병합이라 정체 두 필드는 자기 env cc 만 채운다 —
	//    session_id 는 훅의 자리이고 그 훅은 이미 지나갔다.
	pid := os.Getpid()
	started, err := window.StartedOf(pid)
	if err != nil {
		t.Fatalf("이 프로세스의 시작 시각을 못 읽었다: %v", err)
	}
	key := window.Key{MachineID: app.machine, ClaudePID: pid, Started: started}
	if _, err := window.Plant(app.beaconDir, key, wt, "cc-old", time.Now()); err != nil {
		t.Fatalf("비콘 심기 실패: %v", err)
	}
	b, err := window.Load(app.beaconDir, key)
	if err != nil {
		t.Fatalf("비콘을 못 읽었다: %v", err)
	}
	if b.SessionID != "" || b.CCSessionID != "cc-old" {
		t.Fatalf("대조 전제가 깨졌다 — 늦은 심기의 비콘이 cc=%q session=%q 다. "+
			"session_id 는 비고 cc 는 옛 값이어야 이 시험이 무언가를 지킨다", b.CCSessionID, b.SessionID)
	}

	// ③ /clear.
	if code, out := h.run(sessionStartPayload("cc-new", cwd), "hook", "session-start"); code != 0 {
		t.Fatalf("둘째 SessionStart 훅 종료코드 %d: %s", code, out)
	}

	// ④ 카드는 한 장이고, 그것이 카드 A 다.
	cards = cardsFor(t, h, app.machine, wt)
	if len(cards) != 1 {
		ids := make([]string, 0, len(cards))
		for _, c := range cards {
			ids = append(ids, c.ID+"/"+c.CCSessionID)
		}
		t.Fatalf("늦게 심긴 비콘으로 /clear 를 넘겼는데 카드가 %d장이다(%s) — "+
			"훅이 빈 session_id 를 보고 rekey 를 통째로 건너뛰었다는 신호다",
			len(cards), strings.Join(ids, " "))
	}
	if cards[0].ID != cardA.ID || cards[0].CCSessionID != "cc-new" {
		t.Fatalf("남은 카드가 %s/%s 다 — %s/cc-new 여야 한다. 첫 구간의 선점과 판단이 든 카드가 그것이다",
			cards[0].ID, cards[0].CCSessionID, cardA.ID)
	}

	// ⑤ 다음 전환의 재료도 갖췄다 — 비콘이 이제 카드 id 를 든다.
	if b, err = window.Load(app.beaconDir, key); err != nil {
		t.Fatalf("둘째 훅 뒤 비콘을 못 읽었다: %v", err)
	}
	if b.CCSessionID != "cc-new" || b.SessionID != cardA.ID {
		t.Fatalf("비콘이 cc=%q session=%q 다 — cc-new/%s 를 기대했다", b.CCSessionID, b.SessionID, cardA.ID)
	}
}

// ★ 서버가 죽어 있으면 rekey 실패를 화면에 **또** 얹지 않는다.
//
// 미도달이면 rekey 는 정의상 매번 실패하고, 그 사실은 배너가 이미 말하고 있다.
// 일곱 줄 아래 OpenSession 실패가 `reachable` 로 가려지는 것이 같은 이유이고,
// 그 규율에서 이 줄만 빠져 있었다 — 그러면 서버가 내려간 동안 /clear 마다 배너 위에
// 같은 말이 한 줄씩 쌓인다.
func TestRekeyFailureIsQuietWhileTheServerIsDown(t *testing.T) {
	h := newHarness(t)
	cwd := t.TempDir()

	app := newApp(envOf(h.env), quietLogger(), cwd, strings.NewReader(""))
	wt := app.proj.Worktree
	pid := os.Getpid()
	started, err := window.StartedOf(pid)
	if err != nil {
		t.Fatalf("이 프로세스의 시작 시각을 못 읽었다: %v", err)
	}
	key := window.Key{MachineID: app.machine, ClaudePID: pid, Started: started}
	if _, err := window.Plant(app.beaconDir, key, wt, "cc-old", time.Now()); err != nil {
		t.Fatalf("비콘 심기 실패: %v", err)
	}
	if code, out := h.run(sessionStartPayload("cc-old", cwd), "hook", "session-start"); code != 0 {
		t.Fatalf("첫 SessionStart 훅 종료코드 %d: %s", code, out)
	}
	// 대조 전제: 비콘이 카드 id 를 들고 있어야 아래에서 rekey 가 **실제로 시도된다.**
	b, err := window.Load(app.beaconDir, key)
	if err != nil || b.SessionID == "" {
		t.Fatalf("전제가 깨졌다 — 비콘에 카드 id 가 없다(err=%v, beacon=%+v)", err, b)
	}

	h.down()

	code, out := h.run(sessionStartPayload("cc-new", cwd), "hook", "session-start")
	if code != 0 {
		t.Fatalf("서버가 죽었는데 훅이 종료코드 %d 를 냈다: %s", code, out)
	}
	// 대조: 침묵을 "아무것도 안 냈다"로 얻지 않았다 — 배너는 그대로 온다.
	if !strings.Contains(out, "미도달") {
		t.Fatalf("전제가 깨졌다 — 미도달 배너가 화면에 없다:\n%s", out)
	}
	if strings.Contains(out, "못 합쳤다") {
		t.Errorf("서버가 죽었는데 rekey 실패를 배너 위에 또 얹는다:\n%s", out)
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

// 비콘의 워크트리가 내 것과 다르면 **남의 창이다 — 수리하지 않는다**(설계 §2 둘째 층).
//
// ★ 이 시험이 없으면 사각이 정확히 무엇인가. window.Find 가 맞추는 것은
// (머신·조상 pid·시작 시각) 셋뿐이고 **워크트리는 그 대조에 없다.** 그런데 rekey 가 고치는
// 카드의 키는 3중키(머신·워크트리·cc)다. 그래서 한 창 안에서 두 채널이 워크트리를 다르게
// 풀면, 훅이 **남의 워크트리 카드의 cc 를 갈아엎고** 아래 upsert 는 내 워크트리로 새 카드를
// 또 만든다 — 그 카드의 선점이 아무 잘못도 없는 워크트리 쪽에 고아로 남는다.
// 이 기능이 없애려는 바로 그 결과를, 이 기능이 만들어 내는 것이다.
//
// 좌표축이 채널마다 갈리는 사고는 이 레포에서 이미 났다(internal/window/dir.go 머리말 ·
// 한 세션이 카드 3장). 그래서 이것은 가정이 아니라 재발 방지다.
func TestABeaconFromAnotherWorktreeIsNotOurs(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	mine := t.TempDir()  // 이번 훅이 도는 워크트리
	other := t.TempDir() // 같은 창의 비콘이 가리키는 **다른** 워크트리

	app := newApp(envOf(h.env), quietLogger(), mine, strings.NewReader(""))
	myTree := app.proj.Worktree
	otherApp := newApp(envOf(h.env), quietLogger(), other, strings.NewReader(""))
	otherTree := otherApp.proj.Worktree
	if myTree == otherTree {
		t.Fatalf("대조 전제가 깨졌다 — 두 워크트리가 같은 경로다(%s)", myTree)
	}

	// ① 다른 워크트리 쪽에 카드가 하나 있고, 항목 하나를 쥐고 있다.
	//    운영 경로 그대로 훅으로 만든다 — 손으로 넣으면 프로젝트·머신 행의 전제가 갈린다.
	if code, out := h.run(sessionStartPayload("cc-old", other), "hook", "session-start"); code != 0 {
		t.Fatalf("다른 워크트리의 SessionStart 훅 종료코드 %d: %s", code, out)
	}
	otherCards := cardsFor(t, h, app.machine, otherTree)
	if len(otherCards) != 1 {
		t.Fatalf("다른 워크트리의 카드가 %d장이다 — 전제가 안 섰다", len(otherCards))
	}
	otherCard := otherCards[0]

	const itemID = "t12-other-tree-item"
	if err := h.st.AddItem(ctx, model.Item{
		Project: h.project, ID: itemID, Title: "남의 트리 항목", Body: "본문", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("항목 등록 실패: %v", err)
	}
	if _, err := h.st.ClaimItem(ctx, h.project, itemID, otherCard.ID); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}

	// ② 이 창의 비콘이 **그 다른 워크트리**를 가리키게 심는다.
	//    Find 는 이 비콘을 맞춘다 — 좌표 셋(머신·pid·시작 시각)이 전부 내 것이기 때문이다.
	pid := os.Getpid()
	started, err := window.StartedOf(pid)
	if err != nil {
		t.Fatalf("이 프로세스의 시작 시각을 못 읽었다: %v", err)
	}
	key := window.Key{MachineID: app.machine, ClaudePID: pid, Started: started}
	if _, err := window.Plant(app.beaconDir, key, otherTree, "cc-old", time.Now()); err != nil {
		t.Fatalf("비콘 심기 실패: %v", err)
	}
	if _, err := window.SaveIdentity(app.beaconDir, key, "cc-old", otherCard.ID, time.Now()); err != nil {
		t.Fatalf("비콘에 카드 id 를 못 적었다: %v", err)
	}

	// ③ **내** 워크트리에서 새 cc 로 SessionStart.
	code, out := h.run(sessionStartPayload("cc-new", mine), "hook", "session-start")
	if code != 0 {
		t.Fatalf("SessionStart 훅 종료코드 %d: %s", code, out)
	}

	// ④ 남의 카드는 cc 가 그대로다 — rekey 가 안 갔다.
	stored, err := h.st.GetSession(ctx, otherCard.ID)
	if err != nil {
		t.Fatalf("다른 워크트리의 카드를 못 읽었다: %v", err)
	}
	if stored.CCSessionID != "cc-old" {
		t.Fatalf("다른 워크트리 카드의 cc 가 %q 로 바뀌었다 — 훅이 남의 트리 카드를 rekey 했다. "+
			"그 트리의 세션은 이제 자기 카드를 못 찾는다", stored.CCSessionID)
	}
	if stored.Worktree != otherTree {
		t.Fatalf("카드의 워크트리가 %q 다 — 전제가 깨졌다", stored.Worktree)
	}

	// ⑤ 그 카드의 선점도 그대로 붙어 있다.
	claimed, err := h.st.ClaimedItems(ctx, otherCard.ID)
	if err != nil {
		t.Fatalf("선점 조회 실패: %v", err)
	}
	if len(claimed) != 1 || claimed[0] != itemID {
		t.Fatalf("다른 워크트리 카드의 선점이 %v 다 — [%s] 를 기대했다. 고아가 됐다", claimed, itemID)
	}

	// ⑥ 그리고 내 워크트리는 오늘 거동 그대로 자기 카드를 얻는다(거절은 오류가 아니다).
	myCards := cardsFor(t, h, app.machine, myTree)
	if len(myCards) != 1 || myCards[0].CCSessionID != "cc-new" {
		t.Fatalf("내 워크트리의 카드가 %d장(%+v)이다 — cc-new 1장이어야 한다. "+
			"워크트리 불일치는 거절이지 실패가 아니다", len(myCards), myCards)
	}

	// ⑦ 비콘도 안 건드렸다 — 남의 트리 비콘에 내 정체를 적으면 그것도 오염이다.
	b, err := window.Load(app.beaconDir, key)
	if err != nil {
		t.Fatalf("비콘을 못 읽었다: %v", err)
	}
	if b.CCSessionID != "cc-old" || b.SessionID != otherCard.ID {
		t.Fatalf("비콘이 cc=%q session=%q 로 덮였다 — 남의 트리 비콘에 내 정체를 적었다",
			b.CCSessionID, b.SessionID)
	}

	// ⑧ **그리고 이 사실이 화면에 온다.** 두 채널이 좌표를 다르게 풀었다는 것은 다른 데의
	//    진짜 결함이라, 아무도 안 켜는 로그 레벨에 묻히면 지난번처럼 오래 안 보인다.
	//    (카드·선점 단정은 위에서 전부 store 로 했다 — 이 한 줄만이 화면 축이고, 그것이 요구다)
	if !strings.Contains(out, "다른 워크트리") {
		t.Fatalf("워크트리 불일치가 화면에 안 나온다:\n%s", out)
	}
}

// ★★ **비콘 가지치기를 훅이 정말 부르는가 — 그리고 어느 훅에서 부르는가.**
//
// 형제인 `a.pruneBinCache()` 는 앞 라운드에 잠겼는데 바로 위 `a.pruneWindows()` 한 줄만
// 안 잠겨 있었다: 그 줄을 지워도 cmd/fd 가 통째로 초록이었다(앞 라운드 실측). 앞 브랜치가
// 만든 결함이 아니라 선재 결함인데, 이제 옆에 잠긴 형제가 있어 비대칭이 선명하다.
//
// 안 잠기면 무엇이 사는가. pruneWindows 는 반환값이 없고 사유를 Debug 로만 남긴다 —
// 그것이 정한 바다(청소가 세션 시작을 막으면 안 된다). 그래서 호출이 리베이스에서 떨어져
// 나가도 화면·로그·종료코드 어디에도 신호가 없고, ~/.flightdeck/windows/ 에는 창이 죽을
// 때마다 파일이 하나씩 상한 없이 남는다. doctor 는 이 디렉토리의 **자리와 사유**만 찍지
// 항목 수를 안 찍으므로, 침묵으로 사는 회귀다.
//
// ★ **부르는가만 재지 않고 자리도 잰다.** 부르는가만 재면 runHook 머리에 한 줄 넣는 판이
// 초록이 되는데, 그것은 매 프롬프트(2초)·매 턴 끝(3초)마다 디렉토리를 훑는 것이다.
// 왜 안 되는지의 판정은 hook.go 의 pruneWindows 머리말에 있다 — 여기서 다시 적지 않는다.
// 표의 false 행 다섯이 그 판을 빨간불로 만든다.
//
// ★ 이 표가 **일부러 안 재는 것**: "남의 머신 비콘은 안 건드린다" · "못 읽는 파일은 안
// 지운다" · "죽은 것만 지운다"는 window.Prune 자신의 계약이고 internal/window/find_test.go
// 가 이미 세 갈래로 잠갔다. 여기서 또 재면 한 판정이 두 화면에 산다. 이 표의 좌표는
// **훅 이음매** 하나다 — 살아 있는 창을 함께 심는 것만 예외인데, 그것 없이는 alive 자리에
// `func(int) bool { return false }` 를 넘기는 배선(=디렉토리를 통째로 비운다)도 초록이기
// 때문이다. 그 배선의 손해는 되돌릴 수 없다(window.Prune 머리말).
//
// ★ 진짜 홈을 안 건드린다: 하네스가 FD_STATE_DIR 를 못박으므로 비콘 자리는 `h.state/windows`
// 다. 그 전제를 결과보다 **먼저** 단정한다 — 이 시험은 지우는 쪽이라, 자리가 어긋나면
// 개발자의 살아 있는 창 비콘을 날리고도 초록일 수 있다.
func TestOnlySessionStartHookPrunesWindowBeacons(t *testing.T) {
	cases := []struct {
		event  string
		prunes bool
	}{
		{"session-start", true},
		{"user-prompt", false},
		{"post-tool", false},
		{"pre-compact", false},
		{"stop", false},
		{"session-end", false},
	}
	for _, c := range cases {
		t.Run(c.event, func(t *testing.T) {
			h := newHarness(t)
			cwd := t.TempDir()

			app := newApp(envOf(h.env), quietLogger(), cwd, strings.NewReader(""))
			if app.beaconDir == "" {
				t.Fatal("대조 전제가 깨졌다 — App.beaconDir 가 비었다")
			}
			if !strings.HasPrefix(app.beaconDir, h.state) {
				t.Fatalf("비콘 디렉토리가 하네스 밖이다(%s) — 이 시험은 지우는 쪽이라 "+
					"사용자의 진짜 창 비콘을 날린다", app.beaconDir)
			}

			// ★ Started 를 일부러 **틱이 아닌 문자열**로 둔다. window.StartedOf 는 /proc 의
			// 22번 필드라 언제나 숫자이므로, 이러면 심은 둘의 파일 이름이 findWindow 가
			// 조립하는 이름과 절대 안 겹친다. 겹치면 훅이 이 비콘으로 표류 수리를 시작하고
			// (rekey · SaveIdentity), 그러면 이 시험이 재는 것이 가지치기가 아니게 된다.
			dead := window.Key{MachineID: app.machine, ClaudePID: freePID(t), Started: "틱이아니다"}
			live := window.Key{MachineID: app.machine, ClaudePID: os.Getpid(), Started: "틱이아니다"}
			for _, k := range []window.Key{dead, live} {
				if _, err := window.Plant(app.beaconDir, k, app.proj.Worktree, "cc-딴창", time.Now()); err != nil {
					t.Fatalf("비콘 심기 실패(pid %d): %v", k.ClaudePID, err)
				}
				if _, err := os.Stat(filepath.Join(app.beaconDir, k.FileName())); err != nil {
					t.Fatalf("대조 전제가 깨졌다 — 심은 비콘이 자리에 없다(%s): %v", k.FileName(), err)
				}
			}

			if code, out := h.run(sessionStartPayload("cc-session-uuid-1", cwd), "hook", c.event); code != 0 {
				t.Fatalf("%s 훅 종료코드 %d:\n%s", c.event, code, out)
			}

			_, err := os.Stat(filepath.Join(app.beaconDir, dead.FileName()))
			switch {
			case c.prunes && err == nil:
				t.Errorf("죽은 창(pid %d)의 비콘이 남았다 — 훅이 가지치기를 안 불렀다. "+
					"그 한 줄이 없으면 %s 에 파일이 상한 없이 쌓이고, 실패를 Debug 로만 남기는 "+
					"설계라 어느 화면에도 신호가 안 뜬다", dead.ClaudePID, app.beaconDir)
			case !c.prunes && err != nil:
				t.Errorf("%s 훅이 죽은 창의 비콘을 지웠다 — 가지치기는 session-start 의 일이다. "+
					"이 이벤트는 훨씬 자주 돌거나 예산이 훨씬 작다(hooks.json 의 타임아웃·async 표시). "+
					"왜 session-start 뿐인지는 hook.go 의 pruneWindows 머리말에 있다", c.event)
			}
			if _, err := os.Stat(filepath.Join(app.beaconDir, live.FileName())); err != nil {
				t.Errorf("살아 있는 창(pid %d)의 비콘이 사라졌다 — 청소가 아니라 파괴다. "+
					"그 창은 다음 /clear 에서 표류를 못 고치고, 잃었다는 신호도 없다: %v",
					live.ClaudePID, err)
			}
		})
	}
}

// freePID 는 프로세스 표에 없는 pid 하나다. pid_max 위는 커널이 ESRCH 로 답한다.
//
// ★ 짧게 살다 죽은 자식의 pid 를 안 쓴다 — 거둔 pid 는 커널이 되쓸 수 있어서, 그 판은
// 아주 가끔 "죽은 창인데 살아 있다"로 빨간불이 난다. 재현이 안 되는 빨간불은 그 시험을
// 아무도 안 믿게 만든다.
//
// ★ internal/window/proc_linux_test.go 에 같은 이름의 헬퍼가 있다. 부르지 못한다 —
// 시험 헬퍼는 패키지 밖으로 안 나가고, 이 네 줄을 위해 window 에 시험 전용 API 를
// 뚫지는 않는다. 같은 상수(pid_max)를 읽으므로 두 사본이 갈릴 여지는 없다.
func freePID(t *testing.T) int {
	t.Helper()
	raw, err := os.ReadFile("/proc/sys/kernel/pid_max")
	if err != nil {
		t.Fatalf("pid_max 를 못 읽었다: %v", err)
	}
	max, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("pid_max 가 수가 아니다(%q): %v", raw, err)
	}
	return max + 1
}
