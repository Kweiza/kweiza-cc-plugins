package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
//
// ★ **GOMAXPROCS 를 최소 2 로 올린다. 이 줄이 없으면 관문이 환경 하나로 사라진다.**
// 실측: `-test.cpu=1` 이나 GOMAXPROCS=1 이면 잠금을 **통째로 꺼도** 아래 세 시험이
// 3/3 통과한다. 고루틴 둘이 진짜로 겹치지 않으니 볼 경합이 없어서다. 그 상태의 초록은
// "안전하다"가 아니라 "아무것도 안 쟀다"인데 화면에는 똑같이 ok 로 보인다 —
// 이 저장소가 반복해서 경계한 '전 시험 초록 상태로 사는 결함' 그 모양이다.
func bothAtOnce(a, b func()) {
	if runtime.GOMAXPROCS(0) < 2 {
		defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(2))
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); <-start; a() }()
	go func() { defer wg.Done(); <-start; b() }()
	close(start)
	wg.Wait()
}

// queueFormat 은 이 파일의 시험들이 도는 **큐 형식** 하나다.
//
// ★★ **이 파라미터가 없으면 이 파일 전체가 거짓 초록이 된다.** 아래 시험 셋은 큐를
// `seedQueue` 로 심었고 그것은 옛 `pending.jsonl` 만 만든다. 큐가 항목당 파일로 바뀐 뒤에도
// 그 시험들은 **통째로 초록이었다** — 새 자료구조의 동시성 축을 하나도 안 재면서. 즉 화면은
// "동시성 관문 셋 통과"인데 실제로 관문이 걸린 곳은 **전환이 끝나면 사라질 옛 경로**뿐이었다.
// 두 형식을 다 태우는 이유가 그것이고, 옛 형식을 안 지우는 이유는 그 경로가 아직 실물
// 머신에 남아 있기 때문이다(재생이 비울 때까지).
type queueFormat struct {
	name string
	seed func(t *testing.T, dir string, keys ...string)
}

// queueFormats 는 동시성 시험이 도는 형식 전부다.
var queueFormats = []queueFormat{
	{"항목당 파일", seedQueueDir},
	{"옛 JSONL", seedQueue},
}

// seedQueueDir 는 **항목당 파일** 형식으로 큐를 심는다.
//
// ★ `Append` 를 거친다 — 파일을 손으로 쓰면 이름 규칙(키 해시)을 시험이 두 번째로 알게 되고,
// 그러면 규칙이 바뀔 때 구현과 하네스가 따로 논다. `entry()` 가 주는 단조 `At` 이 순서다.
func seedQueueDir(t *testing.T, dir string, keys ...string) {
	t.Helper()
	o := newOutboxAt(dir)
	for _, k := range keys {
		if err := o.Append(entry(k)); err != nil {
			t.Fatalf("항목당 파일 큐를 못 심었다(%s): %v", k, err)
		}
	}
}

// forEachQueueFormat 은 시험 하나를 **모든 큐 형식**에 태운다.
//
// ★ 형식마다 t.Run 을 쓰므로 어느 형식이 깨졌는지가 이름으로 나온다. 한 형식만 도는
// 시험은 다른 형식에 대해 **아무것도 안 재면서 초록**이고, 화면에서 그 둘이 똑같다.
func forEachQueueFormat(t *testing.T, body func(t *testing.T, seed func(*testing.T, string, ...string))) {
	t.Helper()
	for _, f := range queueFormats {
		t.Run(f.name, func(t *testing.T) { body(t, f.seed) })
	}
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
	forEachQueueFormat(t, func(t *testing.T, seed func(*testing.T, string, ...string)) {
		base := t.TempDir()
		lost := 0
		for i := 0; i < concurrentRounds; i++ {
			dir := roundDir(t, base, i)
			seed(t, dir, "already-queued")
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
	})
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
	forEachQueueFormat(t, func(t *testing.T, seed func(*testing.T, string, ...string)) {
		base := t.TempDir()
		under := 0
		for i := 0; i < concurrentRounds; i++ {
			dir := roundDir(t, base, i)
			seed(t, dir, "keeps-failing")
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
	})
}

// 이미 보낸 줄이 큐로 되살아나지 않는다.
//
// ★ 되살아나면 다음 재생이 같은 판단을 다시 POST 한다. 멱등 키가 살아 있는 동안은
// 헛 POST 로 끝나지만, 멱등 표는 TTL·개수로 청소되므로 그 뒤에 재생되면 **원장에 판단이
// 두 줄 들어간다.** 판단은 추가 전용이라(트리거가 UPDATE·DELETE 를 막는다) 회수가 안 된다.
func TestConcurrentReplaysNeverResurrectASentEntry(t *testing.T) {
	forEachQueueFormat(t, func(t *testing.T, seed func(*testing.T, string, ...string)) {
		base := t.TempDir()
		resurrected := 0
		for i := 0; i < concurrentRounds; i++ {
			dir := roundDir(t, base, i)
			seed(t, dir, "j1", "j2", "j3")
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
	})
}

// 재생 결과의 "남았다"가 **병합 뒤 파일 기준**이다 — 스냅숏 기준이 아니다.
//
// ★ 이 단정이 없으면 settle 의 `remaining = len(out)` 를 지워도 전 시험이 초록이다
// (리뷰의 변이 실험이 잡았다). 위의 세 시험은 큐 내용만 보고 ReplayResult 는 안 본다.
//
// 재생 도중 새 판단이 쌓이면 남은 건수는 **1** 이다. 스냅숏 기준으로 세면 0 이 나오고,
// 그러면 도구가 "다 보냈다"고 말하는데 큐에는 판단이 남아 있다.
func TestReplayReportsRemainingFromTheFileNotTheSnapshot(t *testing.T) {
	dir := t.TempDir()
	seedQueue(t, dir, "first")
	replayer, appender := newOutboxAt(dir), newOutboxAt(dir)

	// 전송 중에 남이 쌓는다 — 재생의 스냅숏에는 없는 줄이다.
	res, err := replayer.Replay(context.Background(),
		func(context.Context, OutboxEntry) error {
			if aerr := appender.Append(entry("arrived-mid-flight")); aerr != nil {
				t.Fatalf("끼어드는 Append 가 실패했다: %v", aerr)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("Replay 가 오류를 냈다: %v", err)
	}
	if res.Remaining != 1 {
		t.Errorf("Remaining 이 %d 다 — 1 이어야 한다. 파일에는 판단이 남아 있는데 "+
			"결과가 스냅숏 기준이면 도구가 '다 보냈다'고 말한다", res.Remaining)
	}
	// ★ 그리고 **사유가 비면 안 된다.** ReplayResult 는 건수와 왜 남았는지를 함께 내겠다고
	// 스스로 적어 둔 타입인데, 이 갈래는 내가 멈춘 것이 아니라 남이 쌓은 것이라
	// stopReason 이 비어 있다 — 그대로 두면 "…남았다 — " 로 대시만 남는다.
	if strings.HasSuffix(strings.TrimSpace(res.Detail), "—") || !strings.Contains(res.Detail, "새로 쌓였다") {
		t.Errorf("Detail 이 사유 없이 끝났다: %q", res.Detail)
	}
}

// 임시 파일을 만들어 놓고 실패한 자리도 치운다.
//
// ★ tmp 이름이 유일해진 대가다. 고정 이름일 때는 다음 호출이 같은 이름을 O_TRUNC 로
// 재사용해 청소가 공짜였는데, 유일해지는 순간 아무도 그것을 안 치운다. rename 실패에만
// 정리를 붙였다가 리뷰에서 잡혔다 — `os.WriteFile` 은 O_CREATE|O_TRUNC 로 **먼저 만들고**
// 쓰므로 ENOSPC·EDQUOT 로 실패해도 파일이 남는다.
func TestKeepRemovesTheTempWhenWritingFails(t *testing.T) {
	dir := t.TempDir()
	o := newOutboxAt(dir)
	// pending.jsonl 자리를 디렉토리로 막아 rename 을 실패시킨다(쓰기는 성공한다).
	if err := os.MkdirAll(o.pendingPath(), 0o755); err != nil {
		t.Fatalf("자리를 못 막았다: %v", err)
	}
	if err := o.keep([]OutboxEntry{entry("k1")}); err == nil {
		t.Fatal("전제가 깨졌다 — keep 이 성공했다")
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("디렉토리를 못 읽었다: %v", err)
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("실패한 임시 파일이 남았다: %s — 유일한 이름은 다음 호출이 안 덮는다", e.Name())
		}
	}
}
