package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 이 파일이 지키는 것은 하나다: **SessionStart 배너가 옛 자리 큐를 본다.**
//
// 배너는 사람이 아무것도 묻지 않고도 보는 유일한 표면이다. 그리고 업그레이드 직후 —
// 판단은 전부 옛 자리에 있고 고정 큐는 빈 상태, 즉 **가장 흔한 상태** — 배너가 옛 자리를
// 안 세면 화면이 완전히 조용해진다. 그 상태를 지키는 시험이 여기 있다.
//
// ★ 왜 degrade_path_test.go 의 시험으로 부족한가.
// TestSessionStartUnderL1ReportsPendingJudgmentsAndStaysFailOpen 은 `fd note` 로
// **고정 큐**에만 2건을 쌓고 "판단 2건"을 단정한다. 하네스의 가짜 홈은 갓 만든 빈
// 디렉토리라 옛 자리 후보(~/.local/state/flightdeck/outbox)가 아예 없고, 그래서 옛 자리
// 기여분이 정확히 0 이다. 즉 hook.go 의 옛 자리 합산 루프를 통째로 지워도 그 시험의
// "2건"은 여전히 2건이다 — 전 시험이 초록인 채로 배너가 침묵할 수 있다.
// 같은 모양의 결함을 doctor 쪽에서 이미 한 번 겪었고(outbox_legacy_test.go:305-310 의
// 주석), 그쪽은 TestDoctorReportsLegacyLeftoverLine 이 막았다. 훅 쪽 짝이 이것이다.

// legacyHomeQueue 는 하네스의 가짜 홈 아래 **진짜** 옛 자리 후보 경로다.
//
// 이 자리가 newClient 에 의해 Legacy 로 잡힌다는 것은 이미
// TestNewClientWiresLegacyOutboxFromHome 이 붙들고 있다 — 여기서는 그 위에 올라탄다.
func legacyHomeQueue(h *harness) string {
	return filepath.Join(h.home, ".local", "state", "flightdeck", "outbox")
}

// 옛 자리에만 판단이 남은 상태 — 업그레이드 직후의 가장 흔한 모양 — 에서 배너가 센다.
func TestSessionStartCountsLegacyQueueInBanner(t *testing.T) {
	h := newHarness(t)
	legacy := legacyHomeQueue(h)
	seedQueue(t, legacy, "old1", "old2")

	// ── 대조 전제 ① 고정 큐는 비어 있다. 그래야 배너의 "2건"이 옛 자리에서만 온다.
	// 이것 없이는 이 시험이 무엇을 셌는지 알 수 없다.
	if pend, err := newOutbox(envOf(h.env), h.home).List(); err != nil || len(pend) != 0 {
		t.Fatalf("전제가 깨졌다 — 고정 큐가 %d건이다(err=%v)", len(pend), err)
	}
	// ── 대조 전제 ② 서버를 죽인다. 살아 있으면 hookSessionStart 의 첫 줄
	// (a.cli.Flush)이 옛 큐를 **실제로 보내 비우고**, 그러면 셀 것이 남지 않아
	// 이 시험은 아무것도 안 지킨 채 빨개진다.
	h.down()

	code, out := h.run(`{"session_id":"cc-session-uuid-1","cwd":"/tmp","hook_event_name":"SessionStart"}`,
		"hook", "session-start")
	if code != 0 {
		t.Fatalf("SessionStart 훅이 종료코드 %d 를 냈다 — 훅은 세션을 막으면 안 된다:\n%s", code, out)
	}

	ctxText := additionalContext(t, out)
	if !strings.Contains(ctxText, "아직 못 보낸 판단 2건") {
		t.Errorf("배너가 옛 자리 대기 2건을 안 세고 침묵했다 — 업그레이드 직후의 가장 흔한 상태에서 "+
			"화면이 조용해진다(옛 자리 %s):\n%s", legacy, ctxText)
	}

	// ★ 배너는 **읽기만** 한다. 서버가 죽어 있으므로 옛 큐가 그대로 남아야 한다.
	// 남지 않았다면 위 "2건"은 배너가 센 것이 아니라 재생이 지나간 흔적일 수 있다.
	if _, err := os.Stat(filepath.Join(legacy, pendingName)); err != nil {
		t.Errorf("옛 큐가 사라졌다 — 미도달인데 무엇이 비웠나: %v", err)
	}
}

// ── 셀 수 없는 큐: 0 과 '못 쟀다'를 가른다 ────────────────────────────────────
//
// 여기부터는 위와 **별개 축**이다. 위는 "셌는데 안 더했다"를 막고, 아래는
// "세다 걸렸는데 아무 말도 안 했다"를 막는다.
//
// ★ 이 축이 왜 표시 문제가 아닌가. 큐 하나가 해석 불가가 되면 그 큐는
//   - 재생이 안 된다 — Replay 가 List() 오류에서 곧장 반환한다(outbox.go:343-346).
//   - **새 판단도 못 쌓는다** — Append 가 첫 줄에서 같은 List() 를 부른다(outbox.go:236).
// 즉 배너가 조용한 바로 그 상태가 "판단이 나가지도 쌓이지도 않는" 상태다.
// 그런데 사람이 그것을 알 길은 `fd doctor` 를 **직접 쳐 보는 것**뿐이다.
// 배너는 아무것도 안 묻고 보는 유일한 표면이라, 여기가 침묵하면 사실상 아무도 모른다.

// corruptQueue 는 큐 파일 끝에 해석 불가한 줄을 덧붙인다.
//
// ★ **부분 손상**이 이 축의 진짜 모양이라 일부러 이렇게 만든다. readEntries 는 깨진
// 줄을 조용히 건너뛰지 않고 **읽은 데까지와 함께** 오류를 올리는데(outbox.go:291-292),
// 호출자 셋(Replay·Append·leftover)이 전부 오류를 우선한다. 그래서 앞의 멀쩡한 줄들은
// 나가지도, 세어지지도, 심지어 그 옆에 새 판단이 쌓이지도 못한다.
// 권한 0000 으로 만드는 방법은 안 쓴다 — root 로 도는 CI 에서 조용히 통과한다.
func corruptQueue(t *testing.T, dir string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, pendingName), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("큐를 못 열었다(%s): %v", dir, err)
	}
	defer f.Close()
	if _, err := f.WriteString("{망가진 줄\n"); err != nil {
		t.Fatalf("깨진 줄을 못 썼다: %v", err)
	}
}

// 옛 자리 큐를 셀 수 없으면 배너가 **그 사실을 말한다.**
func TestSessionStartSaysWhenALegacyQueueCannotBeCounted(t *testing.T) {
	h := newHarness(t)
	legacy := legacyHomeQueue(h)
	seedQueue(t, legacy, "old1", "old2")
	corruptQueue(t, legacy)

	// ── 대조 전제: 정말 셀 수 없는 상태이고, 그런데도 멀쩡한 2건이 그 안에 있다.
	// 이 단정이 없으면 "손상시켰다고 생각했는데 사실은 안 됐다"를 못 가른다.
	lo := newOutboxAt(legacy).leftover()
	if lo.Err == "" {
		t.Fatalf("전제가 깨졌다 — 큐가 멀쩡하다(Pending=%d)", lo.Pending)
	}
	if lo.Pending != 0 {
		t.Fatalf("전제가 깨졌다 — 셀 수 없는데 Pending 이 %d 다", lo.Pending)
	}

	h.down()
	code, out := h.run(`{"session_id":"cc-session-uuid-1","cwd":"/tmp","hook_event_name":"SessionStart"}`,
		"hook", "session-start")
	if code != 0 {
		t.Fatalf("SessionStart 훅이 종료코드 %d 를 냈다:\n%s", code, out)
	}

	ctxText := additionalContext(t, out)
	if !strings.Contains(ctxText, legacy) {
		t.Errorf("배너가 셀 수 없는 큐(%s)를 이름으로 안 댔다 — 사람이 어디를 봐야 할지 모른다:\n%s",
			legacy, ctxText)
	}
	if !strings.Contains(ctxText, "못 셌다") {
		t.Errorf("배너가 '못 셌다'를 안 말한다 — 0건과 구별이 안 되고, 0건은 '깨끗하다'로 읽힌다:\n%s",
			ctxText)
	}
}

// 고정 큐를 셀 수 없어도 같다. 지금 그 사유는 로그로만 가고 로그는 아무도 안 본다.
func TestSessionStartSaysWhenTheFixedQueueCannotBeCounted(t *testing.T) {
	h := newHarness(t)
	fixed := newOutbox(envOf(h.env), h.home)
	seedQueue(t, fixed.Dir(), "k1")
	corruptQueue(t, fixed.Dir())

	if _, err := fixed.List(); err == nil {
		t.Fatalf("전제가 깨졌다 — 고정 큐가 멀쩡하다")
	}

	h.down()
	code, out := h.run(`{"session_id":"cc-session-uuid-1","cwd":"/tmp","hook_event_name":"SessionStart"}`,
		"hook", "session-start")
	if code != 0 {
		t.Fatalf("SessionStart 훅이 종료코드 %d 를 냈다:\n%s", code, out)
	}

	ctxText := additionalContext(t, out)
	if !strings.Contains(ctxText, fixed.Dir()) {
		t.Errorf("배너가 셀 수 없는 고정 큐(%s)를 이름으로 안 댔다:\n%s", fixed.Dir(), ctxText)
	}
	if !strings.Contains(ctxText, "못 셌다") {
		t.Errorf("배너가 '못 셌다'를 안 말한다 — 사유가 로그로만 갔고 로그는 아무도 안 본다:\n%s", ctxText)
	}
}
