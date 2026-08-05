//go:build linux

// ★ 리눅스 전용인 이유는 hook_beacon_test.go 머리말과 같다 — 비콘 대조가
// window.StartedOf(조상 시작 시각)에 기대는데 그 함수는 리눅스 밖에서 ErrUnsupported 다.
// 스킵으로 초록을 내는 대신 빌드 대상에서 뺀다(스킵은 "돌았는데 통과"와 구분이 안 간다).
package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/window"
)

// ★ hook.go 의 늦은-심기 복구 갈래(앵커: `beacon.SessionID == "" && beacon.CCSessionID != "" &&
// beacon.CCSessionID != cc`)가 옛 cc 로 카드를 못 찾았을 때 **빈 카드를 만들지 않는지**를 잰다
// (fd-session-lookup-without-upsert 의 성과 — task-7-brief.md 참고).
//
// ★★ 브리프(Step 1)의 원안은 "비콘만 심고 SessionStart 한 번"이었다. 그 원안으로는 이 갈래를
// 구분하지 못한다는 것을 실측으로 확인했다: 옛 cc 카드가 없고 새 cc 도 아무도 안 쓰던 상태에서는
// **버그가 있던 옛 코드도 카드 1장을 낸다** — OpenSession(옛cc,"") 이 만드는 빈 카드가 있어도
// 뒤이은 rekey(옛cc→새cc)가 충돌 없이 성공해 그 빈 카드가 그대로 새 카드로 흡수되기 때문이다
// (브리프가 스스로 적어 둔 셋째 줄과 정확히 같다: "옛 cc 없다 + rekey 성공 → 1장, 손해 없음").
// 그래서 이 시험이 "N → N+1" 로는 옛 코드와 새 코드를 가르지 못한다 — **rekey 가 거절돼야만**
// 옛 코드가 실제로 카드를 하나 더 남긴다. 이 store 의 UNIQUE(machine_id, worktree, cc_session_id)
// 제약(internal/store/schema.sql:67) 아래서 rekey 가 거절되는 유일한 길은 **새 cc 를 이미 다른
// 행이 쥐고 있는 것**뿐이다(internal/api/rekey_test.go 의 TestRekeyToATakenCCIs409 이 같은 조건을
// 쓴다). 그래서 이 시험은 새 cc 의 카드를 **먼저** 만들어 둔다.
//
// 그 결과 "옳은 결과"는 브리프의 N+1 이 아니라 **N(그대로)** 다 — 새 cc 카드가 이미 있으니
// 아무것도 더 생기면 안 된다. 옛 코드는 그 자리에 고아 카드를 하나 더 남겨 N+1 이 된다.
// 이 어긋남이 이 시험의 신호다. (되돌리기로 실측한 결과는 task-7-report.md 에 있다.)
func TestRecoveryBranchDoesNotCreateAStrayCard(t *testing.T) {
	h := newHarness(t)
	cwd := t.TempDir()

	app := newApp(envOf(h.env), quietLogger(), cwd, strings.NewReader(""))
	wt := app.proj.Worktree
	if app.beaconDir == "" || wt == "" {
		t.Fatal("대조 전제가 깨졌다 — 비콘 디렉토리나 워크트리가 비었다")
	}

	// ① 새 cc(cc-new) 의 카드를 먼저 만든다. 비콘이 아직 없으므로 haveBeacon=false 라
	//    복구·rekey 갈래는 안 타고 맨 아래 OpenSession(cc-new) 만 돈다 — 훅은 이때도
	//    아무 비콘도 안 만든다(심는 것은 MCP 의 일, TestNoBeaconMeansTodaysBehaviour 참고).
	if code, out := h.run(sessionStartPayload("cc-new", cwd), "hook", "session-start"); code != 0 {
		t.Fatalf("사전 SessionStart 훅 종료코드 %d: %s", code, out)
	}
	pre := cardsFor(t, h, app.machine, wt)
	if len(pre) != 1 || pre[0].CCSessionID != "cc-new" {
		t.Fatalf("전제가 안 섰다 — 사전 카드 %+v", pre)
	}
	preID := pre[0].ID

	// ② 비콘을 늦게 심는다: session_id 는 비고(=SaveIdentity 를 아직 못 거쳤다),
	//    cc 는 옛 값이다. 그 cc(cc-old) 로는 이 워크트리에 카드가 **없다** — 넷째 갈래의 조건.
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

	before := cardsFor(t, h, app.machine, wt)
	if len(before) != 1 {
		t.Fatalf("대조 전제가 깨졌다 — 비콘 심은 뒤 카드가 %d장이다: %+v", len(before), before)
	}

	// ③ 같은 창에서 cc-new 로 SessionStart(=/clear 흉내). 비콘의 옛 cc(cc-old) 로는
	//    카드가 없어 복구 조회가 못 찾고, 옛 코드가 그 자리에서 만드는 빈 카드는
	//    cc-new 로 rekey 하려는 순간 ①의 카드와 3중키 UNIQUE 에 걸려 거절된다.
	//    서버는 내내 살아 있다 — 이것이 "서버가 닿는 상태에서의 거절"이다.
	code, out := h.run(sessionStartPayload("cc-new", cwd), "hook", "session-start")
	if code != 0 {
		t.Fatalf("훅이 종료코드 %d — 훅은 세션을 막으면 안 된다\n%s", code, out)
	}

	after := cardsFor(t, h, app.machine, wt)
	if len(after) != len(before) {
		ids := make([]string, 0, len(after))
		for _, c := range after {
			ids = append(ids, c.ID+"/"+c.CCSessionID)
		}
		t.Fatalf("카드가 %d → %d 장(%s). cc-new 카드는 ①에서 이미 있으므로 그대로 %d 장이어야 한다 — "+
			"늘었으면 복구 갈래(옛 cc 로 못 찾은 뒤 rekey 거절)가 빈 카드를 고아로 남긴 것이다",
			len(before), len(after), strings.Join(ids, " "), len(before))
	}
	if after[0].ID != preID || after[0].CCSessionID != "cc-new" {
		t.Fatalf("남은 카드가 %s/%s 다 — %s/cc-new(①의 카드 그대로)여야 한다",
			after[0].ID, after[0].CCSessionID, preID)
	}
}
