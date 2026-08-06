package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/api"
)

// drainProbe 는 api.Handler 를 만족하는 시험용 표면이다.
//
// ★ 이 타입이 필요한 것 자체가 이 축의 계약이다 — api.Serve 는 종료를 통지받을 수 있는
// 핸들러만 받는다. 맨 *http.ServeMux 를 넘기던 앞선 판은 통지 경로가 빠진 채로 돌았고,
// 컴파일러가 그것을 못 잡았다.
type drainProbe struct {
	http.Handler
	drained chan struct{}
	once    sync.Once
}

func newDrainProbe(h http.Handler) *drainProbe {
	if h == nil {
		h = http.NewServeMux()
	}
	return &drainProbe{Handler: h, drained: make(chan struct{})}
}

func (d *drainProbe) Drain() { d.once.Do(func() { close(d.drained) }) }

var _ api.Handler = (*drainProbe)(nil)

// TestServeWithWatcherJoinsWatcherBeforeReturning 은 Critical 리뷰 회귀 시험이다.
//
// ★ 실제 api.Serve 를 127.0.0.1:0 에 띄우고(다른 세션의 :7420 과 안 부딪힌다),
// 실제 goroutine 스케줄로 "감시기의 exec 시도가 기록되기 전에는 serveWithWatcher 가
// 반환하지 않는다"를 단언한다. drain 악수(served 채널)만 확인한 단위 시험(selfwatch_test.go)은
// 주입한 drain 클로저가 즉시·항상 반환해 이 위험(goroutine 을 안 join 하면 exec 전에
// 프로세스가 죽을 수 있다)을 못 본다 — 이 시험이 그 축을 덮는다.
func TestServeWithWatcherJoinsWatcherBeforeReturning(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	w := newSelfWatcher(log, "/tmp/does-not-matter.db", "")
	w.watching = true
	w.reason = ""
	w.exePath = "/fake/fd"
	w.start = id(10, 1000)
	w.interval = 10 * time.Millisecond // 시험이 30초를 기다리지 않게

	w.stat = func(string) (ExeID, error) { return id(11, 2000), nil } // 항상 "교체됨"
	w.verify = func(context.Context, string, string) (string, error) {
		return "1d044b2 · test", nil
	}
	execStarted := make(chan struct{})
	w.execSelf = func(string, []string, []string) error {
		close(execStarted)
		time.Sleep(50 * time.Millisecond) // exec 가 "느리게 기록되는" 상황을 흉내낸다
		return nil
	}

	done := make(chan int, 1)
	go func() {
		done <- serveWithWatcher(context.Background(), "127.0.0.1:0", newDrainProbe(nil), log, w)
	}()

	select {
	case <-execStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("exec 시도가 안 왔다 — 감시기가 아예 안 돌았다")
	}

	// exec 가 아직 안 끝났는데(50ms sleep 중) 반환하면 join 이 안 된 것이다.
	select {
	case <-done:
		t.Fatal("exec 가 끝나기 전에 serveWithWatcher 가 반환했다 — 감시기 goroutine 을 join 안 한다")
	case <-time.After(15 * time.Millisecond):
	}

	select {
	case got := <-done:
		if got != 0 {
			t.Fatalf("종료코드 %d 다", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exec 이후에도 반환하지 않았다 — 어딘가 매달렸다")
	}
}

// TestServeWithWatcherReturnsFailWhenExecFails 는 Critical (b) 회귀 시험이다.
//
// ★ execSelf 가 실패하면 Status().Outcome=="failed" 가 남는다. join 없이 반환하면
// 이 검사(serve.go 의 su.Outcome=="failed")가 그 값이 채워지기 전에 읽혀 종료코드 0을
// 낸다 — "재기동이 필요하다"는 신호가 정확히 그 실패 경우에 안 나온다.
func TestServeWithWatcherReturnsFailWhenExecFails(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	w := newSelfWatcher(log, "/tmp/does-not-matter.db", "")
	w.watching = true
	w.reason = ""
	w.exePath = "/fake/fd"
	w.start = id(10, 1000)
	w.interval = 10 * time.Millisecond

	w.stat = func(string) (ExeID, error) { return id(11, 2000), nil }
	w.verify = func(context.Context, string, string) (string, error) {
		return "1d044b2 · test", nil
	}
	w.execSelf = func(string, []string, []string) error {
		time.Sleep(30 * time.Millisecond)
		return errors.New("가짜 exec 실패")
	}

	got := serveWithWatcher(context.Background(), "127.0.0.1:0", newDrainProbe(nil), log, w)
	if got != 1 {
		t.Fatalf("exec 가 실패했는데 종료코드가 %d 다", got)
	}
}

// TestServeDrainsHandlerBeforeExec 는 조합 축(감시기 ↔ api.Serve ↔ 핸들러)을 붙든다.
//
// ★ **exec 시점에 종료 통지가 이미 가 있어야 한다.** 안 그러면 수명이 정해지지 않은
// 응답이 매달린 채로 프로세스가 갈아치워지고, 그 사이 셧다운 유예가 통째로 쓰인다.
// 이 순서는 api.Serve 안에 있어(Drain → Shutdown → 반환 → close(served) → exec)
// 여기서는 인과로만 확인한다 — sleep 도 시계 단정도 없다.
//
// 이 시험이 `serve.go` 의 옛 "우아한 마무리가 아니다" 주석을 실제로 뒤집는 자리다.
func TestServeDrainsHandlerBeforeExec(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	w := newSelfWatcher(log, "/tmp/does-not-matter.db", "")
	w.watching = true
	w.reason = ""
	w.exePath = "/fake/fd"
	w.start = id(10, 1000)
	w.interval = 10 * time.Millisecond

	w.stat = func(string) (ExeID, error) { return id(11, 2000), nil }
	w.verify = func(context.Context, string, string) (string, error) {
		return "1d044b2 · test", nil
	}

	probe := newDrainProbe(nil)
	var drainedAtExec atomic.Bool
	w.execSelf = func(string, []string, []string) error {
		select {
		case <-probe.drained:
			drainedAtExec.Store(true)
		default:
		}
		return nil
	}

	if got := serveWithWatcher(context.Background(), "127.0.0.1:0", probe, log, w); got != 0 {
		t.Fatalf("종료코드 %d 다", got)
	}
	if !drainedAtExec.Load() {
		t.Fatal("exec 시점에 종료 통지가 아직 안 갔다 — 스트림이 매달린 채로 프로세스가 갈아치워진다")
	}
}
