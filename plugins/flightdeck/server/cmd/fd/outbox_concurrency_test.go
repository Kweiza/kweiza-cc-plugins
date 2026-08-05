package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// 이 파일이 지키는 것은 하나다: **같은 큐를 동시에 만지는 프로세스가 판단을 잃지 않는다.**
//
// ★ 왜 -race 로는 이 축을 못 지키는가. 공유 상태가 Go 메모리가 아니라 **파일**이다.
// 다섯 축 전부에서 동시 진입이 실제로 **있는데도** -race 는 0건을 냈다. 그러니 이 축의
// 관문은 -race 가 아니라 **큐 내용에 대한 바이트 단정**이어야 한다. 아래가 그것이다.
//
// ★ 고루틴으로 재는 것이 프로세스를 대신할 수 있는 이유: 이 축이 다투는 자원은 전부
// 파일시스템이고(open·write·rename·remove), 그 연산들은 프로세스 경계를 안 본다.
// 실제로 진짜 OS 프로세스 둘로도 같은 결과가 나오는 것을 재현 단계에서 확인했다.
// 고루틴 판을 쓰는 이유는 값이 같으면서 빠르고 결정적이기 때문이다.

// concurrentRounds 는 한 시험이 도는 시도 횟수다.
//
// ★ 이 수를 줄이지 마라. 잠금이 없을 때의 유실률은 라운드당 10% 안팎이라 몇 회로는
// 조용히 초록이 난다 — 그 초록이 정확히 이 결함이 지금까지 살아남은 방식이다.
const concurrentRounds = 300

// roundDir 은 라운드마다 새 큐 자리를 낸다. t.TempDir 을 라운드마다 부르면 정리가 느리다.
func roundDir(t *testing.T, base string, i int) string {
	t.Helper()
	dir := filepath.Join(base, fmt.Sprintf("q%03d", i))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("큐 자리를 못 만들었다: %v", err)
	}
	return dir
}

// bothAtOnce 는 두 함수를 배리어로 **동시에** 출발시킨다. 수면도 채널 강제도 없다 —
// 강제해야만 나는 결함과 그냥 나는 결함을 섞지 않기 위해서다.
func bothAtOnce(a, b func()) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); <-start; a() }()
	go func() { defer wg.Done(); <-start; b() }()
	close(start)
	wg.Wait()
}

// holdsKey 는 그 키가 큐에 있거나 격리에 있는지다. **둘 중 하나면 잃지 않은 것이다** —
// 격리는 큐에서 빼되 기록은 남기는 자리라 손실이 아니다(설계 §7).
func holdsKey(t *testing.T, o *Outbox, key string) bool {
	t.Helper()
	es, err := o.List()
	if err != nil {
		t.Fatalf("큐를 못 읽었다: %v", err)
	}
	for _, e := range es {
		if e.Key == key {
			return true
		}
	}
	rs, err := o.Rejected()
	if err != nil {
		t.Fatalf("격리를 못 읽었다: %v", err)
	}
	for _, r := range rs {
		if r.Entry.Key == key {
			return true
		}
	}
	return false
}

// 재생이 도는 동안 들어온 새 판단을 재생이 먹지 않는다.
//
// ★ 이것이 이 축에서 가장 무거운 결함이다. Replay 는 List 로 뜬 **스냅숏**을 기준으로
// keep() 이 파일을 통째로 되쓰는데(전량 재기록 또는 os.Remove), 그 사이 다른 프로세스가
// Append 한 줄은 스냅숏에 없다. 그래서 **한 번도 안 보낸 판단이 격리에도 안 남고 사라진다.**
// 그때 Append 를 부른 쪽은 err=nil 을 받고 "아웃박스에 쌓았다"를 찍는다 — 조용한 손실이
// 아니라 거짓말이다. 운영 바이너리와 진짜 프로세스 둘로 200건 중 15건 소실을 재현했다.
func TestReplayNeverEatsAConcurrentAppend(t *testing.T) {
	base := t.TempDir()
	lost := 0
	for i := 0; i < concurrentRounds; i++ {
		dir := roundDir(t, base, i)
		seedQueue(t, dir, "already-queued")
		replayer, appender := newOutboxAt(dir), newOutboxAt(dir)

		// ★ **전송된 것도 세야 한다.** Replay 의 첫 List 는 잠금 밖이라(전송을 잠그면
		// 늦게 온 쪽이 남의 망 왕복을 통째로 기다린다) 방금 들어온 줄을 함께 읽어
		// **그 자리에서 보내 버리는** 갈래가 있다. 그건 손실이 아니라 정상 배달이다.
		// 큐/격리만 보는 오라클은 그 갈래를 손실로 잘못 세고, 그러면 시험이 고칠 수
		// 없는 것을 요구하게 된다 — 실제로 이 시험을 처음 썼을 때 그렇게 틀렸다.
		var mu sync.Mutex
		deliveredNew := false
		bothAtOnce(
			func() {
				_, _ = replayer.Replay(context.Background(),
					func(_ context.Context, e OutboxEntry) error {
						mu.Lock()
						if e.Key == "brand-new" {
							deliveredNew = true
						}
						mu.Unlock()
						return nil // 전송 성공
					})
			},
			func() { _ = appender.Append(entry("brand-new")) },
		)

		mu.Lock()
		sentIt := deliveredNew
		mu.Unlock()
		if !sentIt && !holdsKey(t, appender, "brand-new") {
			lost++
		}
	}
	if lost > 0 {
		t.Errorf("재생이 새 판단을 %d/%d 회 먹었다 — 큐에도 격리에도 없다. "+
			"Append 를 부른 쪽은 err=nil 을 받았다", lost, concurrentRounds)
	}
}

// 겹친 재생 둘이 각자 한 번씩 시도했으면 Tries 는 **둘 다 세야 한다.**
//
// ★ 안 그러면 격리가 늦어진다. maxReplayTries 는 "이 줄이 나쁘다"를 판정하는 유일한
// 둘째 축인데(상태코드만으로는 못 가른다), 시도가 안 세이면 영구히 실패할 줄이 큐를
// 그만큼 오래 막는다 — 그 필드가 애초에 생긴 이유가 바로 그 실측이다.
//
// 스냅숏 기준으로 +1 하면 둘 다 같은 값에서 출발해 같은 값을 쓴다. 그래서 **잠금 안에서
// 읽은 지금 파일값**에 더해야 실제 시도 횟수와 맞는다.
func TestConcurrentReplaysCountEveryTry(t *testing.T) {
	base := t.TempDir()
	under := 0
	for i := 0; i < concurrentRounds; i++ {
		dir := roundDir(t, base, i)
		seedQueue(t, dir, "keeps-failing")
		a, b := newOutboxAt(dir), newOutboxAt(dir)

		// 500 은 "일시 장애일 수 있다"라 격리가 아니라 Tries 만 올린다.
		fail := func(context.Context, OutboxEntry) error {
			return &APIError{Status: 500, Message: "서버가 아프다"}
		}
		bothAtOnce(
			func() { _, _ = a.Replay(context.Background(), fail) },
			func() { _, _ = b.Replay(context.Background(), fail) },
		)

		es, err := a.List()
		if err != nil || len(es) != 1 {
			t.Fatalf("전제가 깨졌다 — 큐가 %d건이다(err=%v)", len(es), err)
		}
		if es[0].Tries < 2 {
			under++
		}
	}
	if under > 0 {
		t.Errorf("시도 둘이 겹쳤는데 Tries 가 2 미만인 라운드가 %d/%d 였다 — "+
			"서버에 닿아 실패한 횟수를 놓쳤고, 그만큼 격리가 늦어진다", under, concurrentRounds)
	}
}

// 이미 보낸 줄이 큐로 되살아나지 않는다.
//
// ★ 되살아나면 다음 재생이 같은 판단을 다시 POST 한다. 멱등 키가 살아 있는 동안은
// 헛 POST 로 끝나지만, 멱등 표는 TTL·개수로 청소되므로 그 뒤에 재생되면 **원장에 판단이
// 두 줄 들어간다.** 판단은 추가 전용이라(트리거가 UPDATE·DELETE 를 막는다) 회수가 안 된다.
func TestConcurrentReplaysNeverResurrectASentEntry(t *testing.T) {
	base := t.TempDir()
	resurrected := 0
	for i := 0; i < concurrentRounds; i++ {
		dir := roundDir(t, base, i)
		seedQueue(t, dir, "j1", "j2", "j3")
		a, b := newOutboxAt(dir), newOutboxAt(dir)

		// ★ **재생 도중 서버가 죽는 것**을 모형화한다. 이 대조가 없으면 이 시험은
		// 아무것도 못 잰다: 두 재생이 똑같이 전량 성공하면 둘 다 keep(nil) 이라
		// 결과가 같아서 스냅숏이 갈릴 자리가 없다. 앞선 셋만 나가고 그 뒤가 미도달이면
		// 한쪽은 "전부 보냈다"로, 다른 쪽은 "하나도 못 보냈다"로 끝나 스냅숏이 갈린다.
		var mu sync.Mutex
		delivered := map[string]bool{}
		send := func(_ context.Context, e OutboxEntry) error {
			mu.Lock()
			defer mu.Unlock()
			if len(delivered) >= 3 {
				return ErrUnreachable
			}
			delivered[e.Key] = true
			return nil
		}
		bothAtOnce(
			func() { _, _ = a.Replay(context.Background(), send) },
			func() { _, _ = b.Replay(context.Background(), send) },
		)

		es, err := a.List()
		if err != nil {
			t.Fatalf("큐를 못 읽었다: %v", err)
		}
		mu.Lock()
		for _, e := range es {
			if delivered[e.Key] {
				resurrected++
				break
			}
		}
		mu.Unlock()
	}
	if resurrected > 0 {
		t.Errorf("서버가 이미 받은 줄이 %d/%d 회 큐로 되살아났다 — 다음 재생이 다시 보낸다",
			resurrected, concurrentRounds)
	}
}
