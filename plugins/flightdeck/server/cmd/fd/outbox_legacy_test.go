package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 이 파일이 지키는 것은 하나다: **어느 채널에서 쌓인 판단도 결국 나간다.**
//
// ★ 옮기지 않는다. 재생이 각 큐를 제자리에서 돌려 전송으로 비우고, 마지막 줄까지
// 나가면 keep() 이 그 파일을 지운다. 앞선 판에서는 os.Rename 청구로 고정 자리에
// 흡수하려 했는데 그 설계가 반증됐다 — 스펙 §4 "왜 옮기지 않기로 했나"를 보라.

// seedQueue 는 옛 자리 하나와 그 안의 대기열 파일을 만든다.
func seedQueue(t *testing.T, dir string, keys ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("옛 자리를 못 만들었다(%s): %v", dir, err)
	}
	var b strings.Builder
	for _, k := range keys {
		buf, err := json.Marshal(entry(k))
		if err != nil {
			t.Fatalf("직렬화 실패: %v", err)
		}
		b.Write(buf)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, pendingName), []byte(b.String()), 0o600); err != nil {
		t.Fatalf("옛 대기열을 못 썼다: %v", err)
	}
}

// queuedKeys 는 큐에 남은 키를 순서대로 낸다.
//
// ★ 이름에 주의. keysOf 는 plugin_test.go 에 제네릭으로 이미 있어서 못 쓴다
// (같은 패키지라 재선언 오류가 난다).
func queuedKeys(t *testing.T, o *Outbox) []string {
	t.Helper()
	es, err := o.List()
	if err != nil {
		t.Fatalf("대기열을 못 읽었다: %v", err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Key)
	}
	return out
}

// ── 옛 자리 큐가 전송으로 비고 파일이 사라진다 ───────────────────────────────

func TestFlushDrainsLegacyQueuesBySending(t *testing.T) {
	h := newHarness(t)

	legacyA := filepath.Join(t.TempDir(), "chanA", "outbox")
	legacyB := filepath.Join(t.TempDir(), "chanB", "outbox")
	seedQueue(t, legacyA, "a1", "a2")
	seedQueue(t, legacyB, "b1")

	cli := newClient(ResolveStateDir(envOf(h.env), h.home), envOf(h.env), h.home, quietLogger())
	cli.Legacy = []*Outbox{newOutboxAt(legacyA), newOutboxAt(legacyB)}

	var sent []string
	// ★ sentFrom 은 각 키가 **어느 큐에서** send 로 넘어왔는지를 잰다. 이것 없이
	// len(sent)==3·res.Sent==3·고정 자리 비어 있음만 보면, 반증된 os.Rename 설계
	// (옛 파일을 고정 자리로 옮긴 뒤 거기서 보내는 것)도 이 값들을 똑같이 낸다 —
	// 그 설계에서도 결국 3건이 나가고 legacyA·legacyB 파일은 사라지고 고정 자리엔
	// pending.jsonl 이 안 남는다(옮겨진 뒤 다 나갔으므로). 어느 디렉토리에서
	// 보냈는지를 확인해야만 "제자리에서 보냈다"와 "옮긴 뒤 보냈다"가 갈린다.
	sentFrom := map[string]string{}
	res := cli.flushAll(t.Context(), func(o *Outbox, e OutboxEntry) error {
		sent = append(sent, e.Key)
		sentFrom[e.Key] = o.Dir()
		return nil
	})
	if len(sent) != 3 {
		t.Fatalf("보낸 것이 %d건이다 — 옛 자리 큐가 안 돌았다: %v", len(sent), sent)
	}
	if res.Sent != 3 {
		t.Errorf("Sent 가 %d 다 — 3 이어야 한다", res.Sent)
	}
	for _, k := range []string{"a1", "a2"} {
		if sentFrom[k] != legacyA {
			t.Errorf("%s 가 %s 에서 나갔다 — legacyA(%s)에서 나갔어야 한다(제자리에서 보낸다)",
				k, sentFrom[k], legacyA)
		}
	}
	if sentFrom["b1"] != legacyB {
		t.Errorf("b1 이 %s 에서 나갔다 — legacyB(%s)에서 나갔어야 한다(제자리에서 보낸다)",
			sentFrom["b1"], legacyB)
	}

	// ★ 큐 파일이 **사라진다** — keep() 의 기존 동작이다.
	for _, d := range []string{legacyA, legacyB} {
		if _, err := os.Stat(filepath.Join(d, pendingName)); !os.IsNotExist(err) {
			t.Errorf("%s 의 큐가 안 비었다(err=%v)", d, err)
		}
	}
	// ★ 고정 자리에는 **아무것도 안 생긴다** — 옮기는 게 아니라 보내는 것이다.
	if got := queuedKeys(t, cli.Outbox); len(got) != 0 {
		t.Errorf("고정 자리에 %v 가 생겼다 — 옮기지 않기로 했다", got)
	}
}

// 옛 큐가 막혀도 고정 큐는 나간다. 한 큐의 정체가 다른 큐를 인질로 잡지 않는다.
//
// ★ 옛 큐가 **둘**이어야 한다. flushAll 은 append([]*Outbox{c.Outbox}, c.Legacy...) 로
// 도므로 고정 큐는 항상 0번째다 — 옛 큐가 하나뿐이면 "fixed1 이 나갔다"는 그 뒤에서
// 무슨 일이 나도(가령 "막힌 큐를 보면 나머지를 통째로 건너뛴다"는 코드가 들어와도)
// 계속 참으로 남는다. 순서상 fixed1 은 막힌 큐보다 **먼저** 처리되기 때문이다.
// 막힌 옛 큐(legacyStuck) *뒤에* 오는 둘째 옛 큐(legacyOK)가 실제로 나가는지를 봐야만
// "한 큐가 막혀도 그 뒤가 인질로 안 잡힌다"를 잰다.
func TestStuckLegacyQueueDoesNotBlockTheFixedQueue(t *testing.T) {
	h := newHarness(t)
	legacyStuck := filepath.Join(t.TempDir(), "chanStuck", "outbox")
	legacyOK := filepath.Join(t.TempDir(), "chanOK", "outbox")
	seedQueue(t, legacyStuck, "stuck1", "stuck2")
	seedQueue(t, legacyOK, "ok1")

	cli := newClient(ResolveStateDir(envOf(h.env), h.home), envOf(h.env), h.home, quietLogger())
	cli.Legacy = []*Outbox{newOutboxAt(legacyStuck), newOutboxAt(legacyOK)}
	if err := cli.Outbox.Append(entry("fixed1")); err != nil {
		t.Fatalf("고정 큐에 못 쌓았다: %v", err)
	}

	var sent []string
	cli.flushAll(t.Context(), func(o *Outbox, e OutboxEntry) error {
		if o.Dir() == legacyStuck {
			return ErrUnreachable // 첫 옛 큐만 막는다
		}
		sent = append(sent, e.Key)
		return nil
	})
	// 순서는 결정적이다: 고정 큐(0번째) → legacyStuck(막힘, 아무것도 안 보냄) → legacyOK.
	if len(sent) != 2 || sent[0] != "fixed1" || sent[1] != "ok1" {
		t.Fatalf("보낸 것이 %v 다 — [fixed1 ok1] 이어야 한다(고정 큐도, 막힌 큐 뒤의 "+
			"둘째 옛 큐도 나가야 한다)", sent)
	}
	// ★ legacyOK 는 실제로 나갔으니 파일도 사라져야 한다 — "나갔다"는 주장을 파일로도 잰다.
	if _, err := os.Stat(filepath.Join(legacyOK, pendingName)); !os.IsNotExist(err) {
		t.Errorf("legacyOK 의 큐가 안 비었다(err=%v) — 나갔다면 keep() 이 지웠어야 한다", err)
	}
	if got := queuedKeys(t, newOutboxAt(legacyStuck)); len(got) != 2 {
		t.Errorf("막힌 옛 큐가 %v 다 — 2건 그대로 남아야 한다", got)
	}
}

// 옛 큐의 영구 거절은 **그 자리의** 격리 파일로 간다. 보관소가 제 큐 옆에 남는다.
func TestLegacyQueueQuarantinesIntoItsOwnDir(t *testing.T) {
	h := newHarness(t)
	legacy := filepath.Join(t.TempDir(), "chan", "outbox")
	seedQueue(t, legacy, "bad1")

	cli := newClient(ResolveStateDir(envOf(h.env), h.home), envOf(h.env), h.home, quietLogger())
	cli.Legacy = []*Outbox{newOutboxAt(legacy)}

	res := cli.flushAll(t.Context(), func(*Outbox, OutboxEntry) error {
		return &APIError{Status: 409, Message: "이미 있다"}
	})

	rej, err := newOutboxAt(legacy).Rejected()
	if err != nil {
		t.Fatalf("옛 자리 격리를 못 읽었다: %v", err)
	}
	if len(rej) != 1 {
		t.Fatalf("옛 자리에 격리가 %d건이다 — 1건이어야 한다", len(rej))
	}
	if fixed, _ := cli.Outbox.Rejected(); len(fixed) != 0 {
		t.Errorf("고정 자리에 격리가 %d건 생겼다 — 보관소는 제 큐 옆에 남아야 한다", len(fixed))
	}

	// ★ 여기부터가 §9 의 "완전 침묵" 공백을 막는 부분이다. flushAll 에서
	// `case res.Remaining > 0 || res.Rejected > 0:` 가지를 지워도 위 파일 단정은
	// 전부 그대로 초록이다(quarantine 은 Outbox.Replay 안에서 일어나지 total 집계와
	// 무관하다) — 그러니 집계·사유 보고 자체를 재는 단정이 따로 있어야 한다.
	if res.Rejected != 1 {
		t.Errorf("res.Rejected 가 %d 다 — 1 이어야 한다(옛 큐의 격리가 총합에 안 잡혔다)", res.Rejected)
	}
	if !strings.Contains(res.Detail, legacy) {
		t.Errorf("res.Detail(%q)에 옛 자리(%s)가 안 보인다 — 어느 큐가 격리했는지 이름을 대야 한다",
			res.Detail, legacy)
	}
}

// ── newClient 자신이 옛 자리를 채운다 ─────────────────────────────────────────

// 위의 세 시험은 전부 cli.Legacy 를 손으로 덮어쓴다. 그래서 newClient 안의
// LegacyOutboxDirs 루프와 `Legacy: legacy` 필드(client.go 의 newClient 본문)를
// 지워도 위 세 시험은 하나도 안 빨개진다 — 이 과제가 프로덕션에서는 조용한
// 아무 일도 안 하는 no-op 이 될 수 있다는 뜻이다. 이 시험이 그 배선 자체를 잰다.
//
// ★ 하네스는 env["HOME"]=h.home · env["FD_STATE_DIR"]=h.state 를 고정한다
// (harness_test.go 의 hs.env 조립부). 그러면:
//   - 고정 자리(OutboxPath)는 FD_STATE_DIR 이 이겨서 h.state/outbox 다.
//   - LegacyOutboxDirs 는 CLAUDE_PLUGIN_DATA·XDG_STATE_HOME 이 없으므로(하네스가 안 채움)
//     home 을 근거로 ~/.local/state/flightdeck/outbox 를 후보에 얹는다(env.go 의
//     `if strings.TrimSpace(home) != "" { add(...".local","state","flightdeck","outbox") }`).
//     이 경로는 h.state/outbox 와 다른 자리라 목표와 겹쳐 걸러지지 않는다.
//
// 그래서 ~/.local/state/flightdeck/outbox 는 이 하네스 안에서 **진짜** 옛 자리 후보다.
func TestNewClientWiresLegacyOutboxFromHome(t *testing.T) {
	h := newHarness(t)
	legacyHome := filepath.Join(h.home, ".local", "state", "flightdeck", "outbox")
	seedQueue(t, legacyHome, "home1")

	// ★ cli.Legacy 를 손대지 않는다 — newClient 자신의 배선을 잰다.
	cli := newClient(ResolveStateDir(envOf(h.env), h.home), envOf(h.env), h.home, quietLogger())

	var found bool
	for _, ob := range cli.Legacy {
		if ob.Dir() == legacyHome {
			found = true
			break
		}
	}
	if !found {
		var dirs []string
		for _, ob := range cli.Legacy {
			dirs = append(dirs, ob.Dir())
		}
		t.Fatalf("newClient 가 %s 를 Legacy 에 안 넣었다 — 실제로 넣은 것: %v", legacyHome, dirs)
	}

	var sent []string
	res := cli.flushAll(t.Context(), func(_ *Outbox, e OutboxEntry) error {
		sent = append(sent, e.Key)
		return nil
	})
	if len(sent) != 1 || sent[0] != "home1" {
		t.Fatalf("newClient 로 연결된 옛 큐가 안 나갔다: sent=%v", sent)
	}
	if res.Sent != 1 {
		t.Errorf("res.Sent 가 %d 다 — 1 이어야 한다", res.Sent)
	}
	if _, err := os.Stat(filepath.Join(legacyHome, pendingName)); !os.IsNotExist(err) {
		t.Errorf("%s 의 큐가 안 비었다(err=%v)", legacyHome, err)
	}
}

// ── doctor 가 옛 자리 잔량과 못 보는 범위를 찍는다 ─────────────────────────────

// doctor 가 옛 자리 잔량을 세되 **아무것도 보내지 않는다.** 진단이 부작용을 가지면
// "찍어 봤더니 상태가 달라졌다"가 되고, 그러면 진단을 믿을 수 없다.
func TestLegacyLeftoversCountsWithoutSending(t *testing.T) {
	h := newHarness(t)
	legacy := filepath.Join(t.TempDir(), "chan", "outbox")
	seedQueue(t, legacy, "k1", "k2")

	cli := newClient(ResolveStateDir(envOf(h.env), h.home), envOf(h.env), h.home, quietLogger())
	cli.Legacy = []*Outbox{newOutboxAt(legacy)}

	got := cli.LegacyLeftovers()
	if len(got) != 1 {
		t.Fatalf("잔량 보고가 %d건이다 — 1건이어야 한다: %+v", len(got), got)
	}
	if got[0].Pending != 2 {
		t.Errorf("대기 %d건으로 셌다 — 2건이어야 한다", got[0].Pending)
	}
	if _, err := os.Stat(filepath.Join(legacy, pendingName)); err != nil {
		t.Errorf("큐가 사라졌다 — 진단이 보냈다: %v", err)
	}
}

// 빈 자리는 안 찍는다. 없는 것을 찍으면 사람이 헛것을 쫓는다.
func TestLegacyLeftoversIsEmptyWhenNothingLeft(t *testing.T) {
	h := newHarness(t)
	cli := newClient(ResolveStateDir(envOf(h.env), h.home), envOf(h.env), h.home, quietLogger())
	cli.Legacy = []*Outbox{newOutboxAt(filepath.Join(t.TempDir(), "nope", "outbox"))}
	if got := cli.LegacyLeftovers(); len(got) != 0 {
		t.Errorf("빈 자리를 %d건으로 보고했다: %+v", len(got), got)
	}
}

// doctor 는 자리와 **못 보는 범위**를 함께 찍는다.
//
// ★ 후자가 이 시험의 핵심이다. 옛 자리 목록은 이 채널이 계산할 수 있는 것만이라
// 다른 채널의 자리는 원리적으로 안 보인다. 그 사실을 안 찍으면 "0건"이 '깨끗하다'로
// 읽히고, 그것은 안 잰 축을 잰 척하는 것이다(§13).
func TestDoctorReportsOutboxPlaceAndItsOwnBlindness(t *testing.T) {
	h := newHarness(t)
	code, out := h.run("", "doctor")
	if code != 0 {
		t.Fatalf("doctor 가 %d 로 끝났다:\n%s", code, out)
	}
	dir, _ := OutboxPath(envOf(h.env), homeDir(envOf(h.env)))
	if !strings.Contains(out, dir) {
		t.Errorf("doctor 가 아웃박스 자리(%s)를 안 찍었다:\n%s", dir, out)
	}
	// ★ 단정 문자열은 **그 줄 고유의 문구**여야 한다. "채널" 로 단정하면 기존
	//   `처방 채널   Stop 훅 stdout` 줄(cmds.go:446)에 걸려서, 사각 문장을 통째로
	//   빼먹어도 초록이 된다 — 이 레포가 반복해서 경계한 '전 시험 초록 상태로 사는 결함'이다.
	if !strings.Contains(out, "옛 자리 탐색") {
		t.Errorf("doctor 가 못 보는 범위를 안 말한다 — 0건이 '깨끗하다'로 읽힌다:\n%s", out)
	}
}
