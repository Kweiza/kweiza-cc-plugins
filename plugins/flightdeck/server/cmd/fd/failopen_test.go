package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fail-open 계수 — **잠금을 못 잡고 지나간 횟수를 물어볼 자리를 만든다.**
//
// ★ 왜 파일이어야 하나. 오늘 이 사건은 `o.warn` 으로만 흐르고 그 로거는 stderr 로 간다.
// `fd doctor` 는 **파일만 읽는다** — 로그를 읽는 줄이 하나도 없다. 그리고 이 경로의 주
// 사용자인 훅은 설계가 "정의상 조용히 죽는다"고 적어 둔 것이다. 그래서 빈도를 물을 자리가
// **구조적으로** 없었고, 그 상태로 두면 "잠금을 넣었다"가 거짓 안심이 된다(설계 §9).
//
// ★ 왜 잠금 없이 O_APPEND 로 쓰나. 격리 파일과 **성질이 반대다**: 격리는 겹친 재생 둘이
// 같은 사건을 두 번 적는 것이 문제였지만, fail-open 은 **겹칠수록 사건이 진짜로 더 난다.**
// 두 프로세스가 각자 못 잡았으면 그것은 두 사건이다. 그러니 여기서 중복 제거를 하면
// 세려던 것을 지운다. 그리고 애초에 이 경로는 **잠금을 못 잡아서 온 자리**라 잠글 수도 없다.

// 잠금 기구가 없는 플랫폼에서는 **한 줄도 안 남긴다.**
//
// ★ 그 플랫폼에서는 모든 호출이 fail-open 이다(withQueueLock 이 곧장 (false, nil) 을 낸다).
// 사건으로 세면 `fd` 호출마다 한 줄씩 파일이 자라고, 그 수는 "얼마나 자주 경합하나"를
// 하나도 안 말한다 — 상수를 사건으로 적는 것이다. 그 사실은 **doctor 가 한 번 말할 것**이지
// 계수기가 셀 것이 아니다.
func TestFailOpenReasonIsEmptyWhenThePlatformHasNoLock(t *testing.T) {
	if got := FailOpenReason(false, nil); got != "" {
		t.Errorf("기구 없는 플랫폼의 사유를 %q 로 냈다 — 빈 문자열이어야 기록을 안 한다", got)
	}
	// 기구가 없으면 오류가 함께 와도 마찬가지다. 그 오류는 기구의 것이 아니다.
	if got := FailOpenReason(false, errors.New("무언가")); got != "" {
		t.Errorf("기구 없는 플랫폼에서 오류가 있다고 %q 로 기록하려 한다", got)
	}
}

// 예산 초과와 기구 실패는 **서로 다른 사건이다.**
//
// ★ 뭉치면 처방이 갈린다. 예산 초과는 "경합이 세다"이고 처방은 큐를 쪼개는 것이다.
// 기구 실패는 "디렉토리를 못 열었다" 같은 것이고 처방은 자리·권한이다. 한 수로 접으면
// 어느 쪽을 고쳐야 하는지 화면이 말을 못 한다.
func TestFailOpenReasonSeparatesBudgetFromMechanismFailure(t *testing.T) {
	budget := FailOpenReason(true, nil)
	if budget == "" {
		t.Fatal("예산 안에 못 잡은 것을 기록 안 한다고 판정했다 — 이것이 세려던 바로 그 사건이다")
	}
	if !strings.Contains(budget, "예산") {
		t.Errorf("예산 초과 사유가 %q 다 — 어느 축인지 안 보인다", budget)
	}
	mech := FailOpenReason(true, errors.New("디렉토리를 못 열었다"))
	if mech == budget {
		t.Errorf("기구 실패와 예산 초과를 같은 사유(%q)로 접었다 — 처방이 다른 두 사건이다", mech)
	}
	if !strings.Contains(mech, "디렉토리를 못 열었다") {
		t.Errorf("기구 실패 사유가 원인을 안 싣는다: %q", mech)
	}
}

// Append 는 **잠금을 아예 안 기다린다** — 남이 예산 내내 쥐고 있어도 그대로 쌓고,
// fail-open 을 **한 건도 안 적는다.**
//
// ★ 이 시험은 앞선 판을 뒤집은 것이다. 그때는 "잠금을 못 잡으면 그 사실이 파일에 남는다"를
// 단정했고 그것이 그 시점의 계약이었다. 큐가 항목당 파일이 되면서 이 경로에 잠금이
// **필요 없어졌다** — 중복 검사가 파일 이름의 존재이고 그 판정을 커널이 원자적으로 한다.
//
// ★ **뒤집힌 단정을 지우지 않고 남기는 이유**: 이 자리에 잠금을 다시 들이는 변경이 오면
// 그것은 조용히 예산 문제를 되살린다(점유가 O(큐 크기)였고 그래서 유실 10/36 이 났다).
// "안 적혔다"를 관문으로 두면 그 회귀가 여기서 빨간불이 된다. fail-open 이 0 이라는 것은
// 계수기가 죽었다는 뜻이 아니다 — settle 쪽 시험이 그 계수기가 살아 있음을 따로 잡는다.
func TestAppendNeverWaitsForTheQueueLock(t *testing.T) {
	if !queueLockSupported {
		t.Skip("이 플랫폼에는 잠금 기구가 없다 — 잡을 잠금이 없으니 이 축을 못 잰다")
	}
	dir := t.TempDir()
	o := newOutboxAt(dir)

	// 잠금을 예산보다 오래 쥔다. flock 은 open file description 단위라 같은 프로세스의
	// 다른 fd 도 배제된다 — 그래서 고루틴 하나로 이 축을 재는 것이 가능하다.
	held, release := make(chan struct{}), make(chan struct{})
	go func() {
		_, _ = withQueueLock(dir, queueLockBudget, func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	// ★ **예산보다 빨리 끝나야 한다.** 이것이 "안 기다린다"의 실측 축이다 —
	//   기다렸다가 fail-open 으로 떨어지는 판과 안 기다리는 판은 결과가 같고 시간이 다르다.
	start := time.Now()
	err := o.Append(OutboxEntry{Key: "k1", Path: "/api/v1/judgments", Body: []byte(`{}`)})
	elapsed := time.Since(start)
	close(release)
	if err != nil {
		t.Fatalf("남이 잠금을 쥐었다고 못 쌓았다: %v", err)
	}
	if elapsed >= queueLockBudget {
		t.Errorf("쌓는 데 %s 걸렸다 — 예산(%s)을 기다렸다는 뜻이다. 이 경로는 잠금을 안 본다",
			elapsed.Round(time.Millisecond), queueLockBudget)
	}

	evs, rerr := o.FailOpens()
	if rerr != nil {
		t.Fatalf("계수 파일을 못 읽었다: %v", rerr)
	}
	if len(evs) != 0 {
		t.Fatalf("fail-open 을 %d 건 적었다 — 이 경로는 잠금을 안 잡으므로 0 이어야 한다. "+
			"0 이 아니면 쌓기가 다시 잠금에 매인 것이고, 그러면 O(큐 크기) 점유와 "+
			"그것이 만든 유실(10/36)이 함께 돌아온다: %+v", len(evs), evs)
	}
	// ★ 판단은 그대로 쌓여 있어야 한다.
	if pend, err := o.List(); err != nil || len(pend) != 1 {
		t.Errorf("판단이 안 쌓였다(%d건, %v)", len(pend), err)
	}
}

// 겹친 fail-open 은 **전부 센다.** 여기서 중복 제거를 하면 세려던 것을 지운다.
//
// ★ 격리 파일과 성질이 반대다: 거기서는 겹친 재생 둘이 **같은 사건**을 두 줄로 적는 것이
// 문제였다. 여기서는 프로세스 둘이 각자 못 잡았으면 **두 사건**이다. 그래서 잠금도
// 중복 검사도 안 붙인다 — O_APPEND 가 정확히 맞는 원시 연산이다.
func TestFailOpenRecordsEveryConcurrentEvent(t *testing.T) {
	dir := t.TempDir()
	o := newOutboxAt(dir)
	const writers = 20
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o.recordFailOpen("append", true, nil)
		}()
	}
	wg.Wait()

	evs, err := o.FailOpens()
	if err != nil {
		t.Fatalf("계수 파일을 못 읽었다: %v", err)
	}
	if len(evs) != writers {
		t.Errorf("동시 %d 건 중 %d 건만 남았다 — 겹친 fail-open 은 서로 다른 사건이라 "+
			"하나도 접으면 안 된다", writers, len(evs))
	}
}

// doctor 가 그 수를 **화면에 낸다** — 물어볼 자리가 여기다.
func TestDoctorReportsFailOpenCount(t *testing.T) {
	h := newHarness(t)
	dir, _ := OutboxPath(envOf(h.env), homeDir(envOf(h.env)))
	o := newOutboxAt(dir)
	o.recordFailOpen("append", true, nil)
	o.recordFailOpen("settle", true, nil)

	code, out := h.run("", "doctor")
	if code != 0 {
		t.Fatalf("doctor 가 %d 로 끝났다:\n%s", code, out)
	}
	if !strings.Contains(out, "잠금 없이 지나간 2회") {
		t.Errorf("doctor 가 fail-open 횟수를 안 찍었다 — 이 항목이 만들려던 자리가 그것이다:\n%s", out)
	}
	// ★ 0 회를 '깨끗하다'로 읽히게 두지 않는다. 이 계수기는 **이 자리의 것만** 세고,
	//   NFS 처럼 flock 이 조용히 안 걸리는 축은 원리적으로 못 잰다(설계 §13).
	if !strings.Contains(out, "NFS") {
		t.Errorf("doctor 가 이 수가 못 보는 범위를 안 말한다 — 0회가 '안전하다'로 읽힌다:\n%s", out)
	}
}

// 진단은 **읽기만 한다.** doctor 가 계수 파일을 비우면 다음 사람이 같은 것을 두 번 못 본다.
func TestDoctorDoesNotClearTheFailOpenRecord(t *testing.T) {
	h := newHarness(t)
	dir, _ := OutboxPath(envOf(h.env), homeDir(envOf(h.env)))
	o := newOutboxAt(dir)
	o.recordFailOpen("append", true, nil)

	if code, out := h.run("", "doctor"); code != 0 {
		t.Fatalf("doctor 가 %d 로 끝났다:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, failOpenName)); err != nil {
		t.Fatalf("doctor 가 계수 파일을 건드렸다: %v", err)
	}
	if evs, err := o.FailOpens(); err != nil || len(evs) != 1 {
		t.Errorf("doctor 뒤에 계수가 %d 건이다(%v) — 진단은 부작용을 가지면 안 된다", len(evs), err)
	}
}

// 재생 쪽 fail-open 도 같은 자리에 남는다 — 갈래 이름이 다르다.
func TestSettleRecordsAFailOpenWhenTheLockIsHeld(t *testing.T) {
	if !queueLockSupported {
		t.Skip("이 플랫폼에는 잠금 기구가 없다")
	}
	dir := t.TempDir()
	o := newOutboxAt(dir)
	if err := o.keep([]OutboxEntry{{Key: "k1", Path: "/api/v1/judgments"}}); err != nil {
		t.Fatalf("큐를 못 심었다: %v", err)
	}

	held, release := make(chan struct{}), make(chan struct{})
	go func() {
		_, _ = withQueueLock(dir, 2*time.Second, func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	_, _ = o.Replay(context.Background(), func(context.Context, OutboxEntry) error { return nil })
	close(release)

	evs, err := o.FailOpens()
	if err != nil {
		t.Fatalf("계수 파일을 못 읽었다: %v", err)
	}
	var settles int
	for _, e := range evs {
		if e.Op == "settle" {
			settles++
		}
	}
	if settles != 1 {
		t.Errorf("재생 쪽 fail-open 을 %d 건으로 적었다 — 1 이어야 한다(사건 전부: %+v)", settles, evs)
	}
}
