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
	res := cli.flushAll(t.Context(), func(_ *Outbox, e OutboxEntry) error {
		sent = append(sent, e.Key)
		return nil
	})
	if len(sent) != 3 {
		t.Fatalf("보낸 것이 %d건이다 — 옛 자리 큐가 안 돌았다: %v", len(sent), sent)
	}
	if res.Sent != 3 {
		t.Errorf("Sent 가 %d 다 — 3 이어야 한다", res.Sent)
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
func TestStuckLegacyQueueDoesNotBlockTheFixedQueue(t *testing.T) {
	h := newHarness(t)
	legacy := filepath.Join(t.TempDir(), "chan", "outbox")
	seedQueue(t, legacy, "stuck1", "stuck2")

	cli := newClient(ResolveStateDir(envOf(h.env), h.home), envOf(h.env), h.home, quietLogger())
	cli.Legacy = []*Outbox{newOutboxAt(legacy)}
	if err := cli.Outbox.Append(entry("fixed1")); err != nil {
		t.Fatalf("고정 큐에 못 쌓았다: %v", err)
	}

	var sent []string
	cli.flushAll(t.Context(), func(o *Outbox, e OutboxEntry) error {
		if o.Dir() == legacy {
			return ErrUnreachable // 옛 큐만 막는다
		}
		sent = append(sent, e.Key)
		return nil
	})
	if len(sent) != 1 || sent[0] != "fixed1" {
		t.Errorf("고정 큐가 %v 를 보냈다 — 옛 큐가 막혔다고 고정 큐가 막히면 안 된다", sent)
	}
	if got := queuedKeys(t, newOutboxAt(legacy)); len(got) != 2 {
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

	cli.flushAll(t.Context(), func(*Outbox, OutboxEntry) error {
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
}
