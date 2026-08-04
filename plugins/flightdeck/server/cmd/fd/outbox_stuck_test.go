package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 이 파일이 지키는 것은 하나다: **영원히 실패할 줄 하나가 큐를 영원히 막지 않는다.**
//
// ★ 실측(2026-08-04, 이 레포의 개발 머신):
//
//	~/.local/state/flightdeck/outbox/pending.jsonl 에 2026-08-03T12:43 판단 1건이 남아 있었다.
//	· 서버는 살아 있었다(healthz 200, 같은 시각 캐시에 성공 응답이 쌓였다).
//	· 아웃박스 파일 mtime 은 그날 10:31 로 갱신돼 있었다. keep() 은 Replay 만 부르므로
//	  **재생은 돌았다.** 즉 "기회가 없었다"가 아니라 **전송이 거절당했다**.
//	· 그런데 앞선 Replay 는 첫 실패에서 멈추고 뒤엣것을 통째로 남겼다. 그 줄이 영원히
//	  실패하는 줄이면 큐가 영원히 막힌다 — 관측된 상태가 정확히 그것이다.
//
// ★ 함께 측정된 별개의 사실: 아웃박스는 **채널마다 따로 쌓인다.** 이 머신에서 MCP 는
// CLAUDE_PLUGIN_DATA(~/.claude/plugins/data/…) 를, 사용자 셸은 ~/.local/state 를 썼다.
// 막힌 줄은 셸 쪽에 있었고, 그래서 MCP·훅이 아무리 돌아도 그 줄은 재생되지 않는다.
// 그 축은 이 항목의 범위가 아니라 후속으로 냈다 — 여기서는 doctor 가 자리를 함께 찍게만 했다.

func mkOutbox(t *testing.T) *Outbox {
	t.Helper()
	at := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	o := newOutboxAt(filepath.Join(t.TempDir(), "outbox"))
	o.now = func() time.Time { return at }
	return o
}

func entry(key string) OutboxEntry {
	return OutboxEntry{Key: key, At: time.Unix(0, 0).UTC(), Path: "/api/v1/judgments",
		Body: json.RawMessage(`{"kind":"now","body":"x"}`)}
}

// ── 분류 ─────────────────────────────────────────────────────────────────────

func TestJudgeReplayFailureSeparatesPermanentFromTransient(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		tries int
		want  bool // Permanent
	}{
		{"미도달은 다시 보낸다", ErrUnreachable, 0, false},
		{"400 은 영구다", &APIError{Status: 400, Message: "본문이 비었다"}, 0, true},
		{"404 는 영구다", &APIError{Status: 404, Message: "없다"}, 0, true},
		{"409 는 영구다", &APIError{Status: 409, Message: "이미 있다"}, 0, true},
		{"408 은 되무름이라 다시 보낸다", &APIError{Status: 408}, 0, false},
		{"429 도 다시 보낸다", &APIError{Status: 429}, 0, false},
		// ★ 이 두 줄이 실측을 반영한 자리다. 하류 FK 파손은 서버가 500 으로 내는데
		//   500 은 정의상 일시 장애라 상태코드만 보면 영원히 재시도한다.
		{"500 은 처음엔 다시 보낸다", &APIError{Status: 500}, 0, false},
		{"500 도 오래 실패하면 영구로 접는다", &APIError{Status: 500}, maxReplayTries - 1, true},
		{"모르는 실패는 남긴다", errors.New("무언가"), 0, false},
		{"모르는 실패도 오래면 접는다", errors.New("무언가"), maxReplayTries - 1, true},
	}
	for _, c := range cases {
		v := JudgeReplayFailure(c.err, c.tries)
		if v.Permanent != c.want {
			t.Errorf("%s: Permanent=%v 여야 하는데 %v 다 (사유: %s)", c.name, c.want, v.Permanent, v.Reason)
		}
		if strings.TrimSpace(v.Reason) == "" {
			t.Errorf("%s: 사유가 비었다 — 사유 없는 판정은 다음 사람이 못 뒤집는다", c.name)
		}
	}
	if v := JudgeReplayFailure(nil, 0); v.Permanent || v.Reason != "" {
		t.Errorf("오류가 없는데 판정을 냈다: %+v", v)
	}
}

// ── 큐가 실제로 안 막히는가 ─────────────────────────────────────────────────

// 영구 거절 하나가 **뒤엣것을 막지 않는다.** 이 항목의 본체다.
func TestPermanentRejectionDoesNotBlockTheQueue(t *testing.T) {
	ob := mkOutbox(t)
	for _, k := range []string{"bad", "good1", "good2"} {
		if err := ob.Append(entry(k)); err != nil {
			t.Fatalf("적재 실패(%s): %v", k, err)
		}
	}

	var tried []string
	res, err := ob.Replay(context.Background(), func(_ context.Context, e OutboxEntry) error {
		tried = append(tried, e.Key)
		if e.Key == "bad" {
			return &APIError{Status: http.StatusBadRequest, Message: "이 줄은 영원히 거절된다"}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("재생이 오류로 끝났다: %v", err)
	}

	// ── 대조가 성립했는지 먼저 단정한다 ─────────────────────────────────────
	// 막힌 줄 뒤가 **시도조차 안 됐다면** 아래 셈은 큐가 뚫렸는지를 재는 것이 아니다.
	if strings.Join(tried, ",") != "bad,good1,good2" {
		t.Fatalf("전제가 깨졌다 — 시도 순서가 %v 다. 막힌 줄 뒤가 시도되지 않았다", tried)
	}

	if res.Sent != 2 || res.Rejected != 1 || res.Remaining != 0 {
		t.Errorf("보냄=%d 격리=%d 남음=%d — 2/1/0 이어야 한다.\n"+
			"영구 거절 하나가 뒤엣것을 인질로 잡으면 이 값이 0/0/3 이 된다.\n%s",
			res.Sent, res.Rejected, res.Remaining, res.Detail)
	}

	// 큐는 비었고 **기록은 남아 있어야 한다** — 큐를 비우는 것과 없애는 것은 다르다.
	pend, err := ob.List()
	if err != nil {
		t.Fatalf("대기열 조회 실패: %v", err)
	}
	if len(pend) != 0 {
		t.Errorf("대기열에 %d건 남았다 — 뚫렸어야 한다", len(pend))
	}
	rej, err := ob.Rejected()
	if err != nil {
		t.Fatalf("격리 조회 실패: %v", err)
	}
	if len(rej) != 1 || rej[0].Entry.Key != "bad" {
		t.Fatalf("격리에 bad 1건이 있어야 한다: %+v", rej)
	}
	if strings.TrimSpace(rej[0].Reason) == "" {
		t.Error("격리 사유가 비었다 — 왜 뺐는지 없으면 되돌릴 근거도 없다")
	}
	if !strings.Contains(rej[0].Reason, "400") {
		t.Errorf("격리 사유가 상태코드를 안 말한다: %q", rej[0].Reason)
	}
}

// 일시 실패는 **그대로 멈춘다** — 순서 보증이 유지돼야 한다.
// 여기서는 **서버에 닿았는데** 실패한 경우(500)를 쓴다. 미도달은 횟수에 안 세므로
// 그것으로는 이 축을 못 본다(TestLongOfflineNeverQuarantines 가 그쪽을 본다).
func TestReachableTransientFailureStopsAndCountsTheTry(t *testing.T) {
	ob := mkOutbox(t)
	for _, k := range []string{"a", "b"} {
		if err := ob.Append(entry(k)); err != nil {
			t.Fatalf("적재 실패: %v", err)
		}
	}
	res, err := ob.Replay(context.Background(), func(_ context.Context, e OutboxEntry) error {
		return &APIError{Status: http.StatusInternalServerError, Message: "내부 오류"}
	})
	if err != nil {
		t.Fatalf("재생이 오류로 끝났다: %v", err)
	}
	if res.Sent != 0 || res.Rejected != 0 || res.Remaining != 2 {
		t.Fatalf("보냄=%d 격리=%d 남음=%d — 0/0/2 여야 한다: %s",
			res.Sent, res.Rejected, res.Remaining, res.Detail)
	}
	pend, _ := ob.List()
	if len(pend) != 2 || pend[0].Tries != 1 || pend[1].Tries != 0 {
		t.Errorf("시도 횟수가 %v 다 — 실패한 첫 줄만 1 이어야 한다",
			[]int{pend[0].Tries, pend[1].Tries})
	}
}

// 계속 실패하면 결국 격리된다 — 실측된 그 줄이 풀리는 경로다.
func TestAlwaysFailingEntryEventuallyLeavesTheQueue(t *testing.T) {
	ob := mkOutbox(t)
	if err := ob.Append(entry("forever")); err != nil {
		t.Fatalf("적재 실패: %v", err)
	}
	// 서버가 500 으로만 답한다 — 상태코드만 보면 영원히 재시도할 실패다.
	send := func(_ context.Context, e OutboxEntry) error {
		return &APIError{Status: http.StatusInternalServerError, Message: "내부 오류"}
	}
	for i := 0; i < maxReplayTries; i++ {
		if _, err := ob.Replay(context.Background(), send); err != nil {
			t.Fatalf("%d회째 재생이 오류로 끝났다: %v", i+1, err)
		}
	}
	pend, _ := ob.List()
	if len(pend) != 0 {
		t.Fatalf("%d번 실패했는데 대기열에 %d건 남았다(시도=%d) — 큐가 영원히 막힌다",
			maxReplayTries, len(pend), pend[0].Tries)
	}
	rej, _ := ob.Rejected()
	if len(rej) != 1 {
		t.Fatalf("격리에 1건이 있어야 한다: %+v", rej)
	}
	// 사유가 **몇 번 실패해서인지**를 말해야 한다 — 문구가 아니라 그 사실을 단정한다.
	if !strings.Contains(rej[0].Reason, fmt.Sprintf("%d번", maxReplayTries)) {
		t.Errorf("격리 사유가 시도 횟수를 안 말한다: %q", rej[0].Reason)
	}
}

// 옛 파일(tries 필드가 없는 줄)도 그대로 읽혀야 한다.
func TestOldEntriesWithoutTriesStillLoad(t *testing.T) {
	ob := mkOutbox(t)
	if err := ob.Append(entry("old")); err != nil {
		t.Fatalf("적재 실패: %v", err)
	}
	pend, err := ob.List()
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if len(pend) != 1 || pend[0].Tries != 0 {
		t.Errorf("tries 없는 줄이 %+v 로 읽혔다 — 0 이어야 한다", pend)
	}
}

// ★ 오프라인이 아무리 길어도 **격리하지 않는다.**
//
// 앞선 판은 시도 횟수를 미도달보다 **먼저** 봤고, 그래서 서버가 꺼진 채 명령을 다섯 번
// 돌리면 멀쩡한 판단이 격리됐다. 훅의 L1 시험이 그것을 잡았다 — 여기에 그 축을 직접 박는다.
// 못 보낸 것은 그 줄이 나쁘다는 증거가 아니다.
func TestLongOfflineNeverQuarantines(t *testing.T) {
	ob := mkOutbox(t)
	if err := ob.Append(entry("offline")); err != nil {
		t.Fatalf("적재 실패: %v", err)
	}
	send := func(_ context.Context, e OutboxEntry) error { return ErrUnreachable }
	for i := 0; i < maxReplayTries*3; i++ {
		if _, err := ob.Replay(context.Background(), send); err != nil {
			t.Fatalf("%d회째 재생이 오류로 끝났다: %v", i+1, err)
		}
	}
	pend, _ := ob.List()
	if len(pend) != 1 {
		t.Fatalf("오프라인 %d회 뒤 대기열이 %d건이다 — 판단이 사라졌다", maxReplayTries*3, len(pend))
	}
	if pend[0].Tries != 0 {
		t.Errorf("미도달을 %d회 셌다 — 미도달은 횟수에 안 세야 한다", pend[0].Tries)
	}
	if rej, _ := ob.Rejected(); len(rej) != 0 {
		t.Errorf("오프라인인데 %d건을 격리했다: %+v", len(rej), rej)
	}
}
