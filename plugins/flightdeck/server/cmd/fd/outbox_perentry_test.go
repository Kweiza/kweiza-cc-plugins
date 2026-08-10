package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 항목당 파일 큐 — **이 항목이 만든 것과 그것이 지켜야 하는 것.**
//
// ★ 옛 판은 큐가 `pending.jsonl` 하나였고, 잠금 구간의 첫 줄이 파일 전량 읽기 · 끝이
// 파일 전량 쓰기라 **점유가 O(큐 크기)** 였다. 그래서 큐가 깊고 세션이 몰리면 예산 250ms 를
// 못 채우는 프로세스가 나오고 무잠금으로 떨어져 판단이 사라졌다(실측: 큐 1000건·세션 30 에서
// 유실 10/36). 예산 조정으로는 못 닫는다 — 30s 면 유실이 0 이 되지만 훅 예산을 통째로 넘긴다.
//
// ★ **이 파일이 지키는 것은 그 전환이 무엇을 얻고 무엇을 잃었는가다.** 동시성 축 자체는
// outbox_concurrency_test.go 가 두 형식 모두에 대해 잰다.

// ── 형식 ─────────────────────────────────────────────────────────────────────

// 새 쓰기는 **항목당 파일로 간다. JSONL 은 안 만든다.**
func TestAppendWritesOneFilePerEntryAndNeverTheLegacyJSONL(t *testing.T) {
	o := mkOutbox(t)
	for _, k := range []string{"a", "b", "c"} {
		if err := o.Append(entry(k)); err != nil {
			t.Fatalf("적재 실패(%s): %v", k, err)
		}
	}
	if _, err := os.Stat(o.pendingPath()); !os.IsNotExist(err) {
		t.Errorf("옛 JSONL(%s)이 생겼다(err=%v) — 새 쓰기는 그 파일을 안 만든다", o.pendingPath(), err)
	}
	names := queueFileNames(t, o.pendingDir())
	if len(names) != 3 {
		t.Fatalf("항목 파일이 %d개다(%v) — 3개여야 한다", len(names), names)
	}
	// ★ 이름이 키만으로 정해진다는 것을 **구현과 독립으로** 단정한다. 이름에 시각이 섞이면
	//   같은 키가 두 파일을 얻고 아래 "같은 키는 한 번만"이 원리적으로 못 성립한다.
	for _, k := range []string{"a", "b", "c"} {
		if _, err := os.Stat(o.entryPath(k)); err != nil {
			t.Errorf("키 %q 의 자리가 이름으로 안 잡힌다: %v", k, err)
		}
	}
}

// 빈 키는 **거절한다.** 조용히 쌓지 않는다.
//
// ★ 옛 판은 빈 키도 쌓았고, `List` 의 키 매칭이 빈 키끼리 별칭이 되어 **둘째 빈 키가
// 조용히 안 쌓였다** — 서로 다른 판단 하나를 소리 없이 버리는 동작이었다(설계 §9).
func TestAppendRefusesAnEmptyKeyInsteadOfAliasingIt(t *testing.T) {
	o := mkOutbox(t)
	e := entry("")
	e.Key = ""
	err := o.Append(e)
	if err == nil {
		t.Fatal("빈 키를 쌓았다 — 빈 키끼리 별칭이 되어 한쪽이 다른 쪽을 지운다")
	}
	if !strings.Contains(err.Error(), "별칭") {
		t.Errorf("거절 사유가 왜인지를 안 말한다: %v", err)
	}
	if names := queueFileNames(t, o.pendingDir()); len(names) != 0 {
		t.Errorf("거절했는데 파일이 %d개 생겼다: %v", len(names), names)
	}
}

// 같은 키를 **동시에** 여럿이 쌓아도 한 번만 쌓인다.
//
// ★ 옛 판의 중복 검사는 `List→검사→O_APPEND` 라 그 사이에 남이 끼면 서로를 못 봤고,
// 그래서 프로세스 간 잠금이 필요했다. 여기서는 판정이 **파일 이름의 존재**이고 커널이
// 그것을 원자적으로 한다 — 이 시험이 그 원자성을 직접 잰다.
func TestConcurrentAppendsOfTheSameKeyLandExactlyOnce(t *testing.T) {
	for i := 0; i < 100; i++ {
		o := newOutboxAt(t.TempDir())
		const writers = 8
		var wg sync.WaitGroup
		wg.Add(writers)
		errs := make([]error, writers)
		start := make(chan struct{})
		for w := 0; w < writers; w++ {
			go func(w int) {
				defer wg.Done()
				<-start
				errs[w] = o.Append(entry("same"))
			}(w)
		}
		close(start)
		wg.Wait()
		for w, err := range errs {
			if err != nil {
				t.Fatalf("%d회차 쓰기 %d 가 실패했다: %v", i, w, err)
			}
		}
		es, err := o.List()
		if err != nil {
			t.Fatalf("%d회차 조회 실패: %v", i, err)
		}
		if len(es) != 1 {
			t.Fatalf("%d회차: 같은 키 %d 건이 쌓였다 — 멱등 키가 이름이므로 1 이어야 한다",
				i, len(es))
		}
	}
}

// 쓰는 도중에 남이 읽어도 **반쪽 JSON 을 안 본다.**
//
// ★ 항목 본문은 `O_CREATE|O_EXCL` 직접 쓰기를 제안했는데, 그것은 *이름*만 원자적이고
// **내용은 아니다**: 자리를 잡은 뒤 Write 까지의 사이에 남의 List 가 0바이트를 읽는다.
// 그 자리에서 해석 실패를 오류로 올리면 정상 동시성이 매번 빨간 줄을 내고, 조용히
// 건너뛰면 재생성 불가한 자산을 버린다 — 둘 다 못 쓴다. 그래서 tmp + Link 다.
func TestReadersNeverSeeAHalfWrittenEntry(t *testing.T) {
	o := newOutboxAt(t.TempDir())
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	var readErr error
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := o.List(); err != nil {
				readErr = err
				return
			}
		}
	}()
	for i := 0; i < 400; i++ {
		if err := o.Append(entry(fmtKey(i))); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("%d번째 적재 실패: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()
	if readErr != nil {
		t.Errorf("쓰는 동안 읽기가 깨졌다: %v — 항목은 완성된 뒤에 보여야 한다", readErr)
	}
}

// ── 순서 ─────────────────────────────────────────────────────────────────────

// 순서는 **파일 이름이 아니라 `At`** 이 정한다.
//
// ★ 옛 판의 순서는 append 도착 순(= 잠금 획득 순)이었지 `At` 순이 아니었다. 서버가 판단
// 시각을 자기 수신 시각으로 채우므로(`store/judgment.go`) **큐 순서가 곧 원장 순서**이고,
// 판단이 실제로 만들어진 순서에 가까운 쪽은 `At` 이다. 이름순(키 해시)에 기대면 그 순서는
// 아무 뜻이 없다 — 이 시험이 그것을 가른다.
func TestReplayOrderFollowsAtNotTheFileName(t *testing.T) {
	o := mkOutbox(t)
	base := time.Unix(0, 0).UTC()
	// **키 이름의 순서와 At 순서를 일부러 어긋나게 둔다.** 이름(알파벳도 해시도)으로는
	// z→m→a 가 안 나오므로, 아래 단정이 맞으면 순서를 정한 것은 이름이 아니라 At 이다.
	for i, k := range []string{"z-first", "m-mid", "a-last"} {
		e := entry(k)
		e.At = base.Add(time.Duration(i) * time.Second) // z=+0s, m=+1s, a=+2s
		if err := o.Append(e); err != nil {
			t.Fatalf("적재 실패(%s): %v", k, err)
		}
	}
	var got []string
	if _, err := o.Replay(context.Background(), func(_ context.Context, e OutboxEntry) error {
		got = append(got, e.Key)
		return nil
	}); err != nil {
		t.Fatalf("재생 실패: %v", err)
	}
	if strings.Join(got, ",") != "z-first,m-mid,a-last" {
		t.Errorf("재생 순서가 %v 다 — At 오름차순(z-first,m-mid,a-last)이어야 한다. "+
			"이름순이면 키 해시가 순서를 정하고 그 순서에는 뜻이 없다", got)
	}
}

// ── 두 형식의 공존 ───────────────────────────────────────────────────────────

// 옛 JSONL 과 항목 파일이 **함께 읽히고**, 재생이 옛 파일을 비우면 그 파일이 사라진다.
//
// ★ 이 자리가 형식 전환에 마이그레이션 코드를 안 짓는 근거다. 옮기지 않는다는 설계
// 판정(§7)도 그대로다 — 읽는 자리가 둘일 뿐 판단은 제자리에서 전송으로 나간다.
func TestBothFormatsAreReadAndTheLegacyFileDisappearsWhenDrained(t *testing.T) {
	o := mkOutbox(t)
	seedQueue(t, o.Dir(), "old1", "old2")
	if err := o.Append(entry("new1")); err != nil {
		t.Fatalf("적재 실패: %v", err)
	}
	es, err := o.List()
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if len(es) != 3 {
		t.Fatalf("두 형식을 합쳐 %d건이다 — 3건이어야 한다: %+v", len(es), es)
	}
	res, err := o.Replay(context.Background(), func(context.Context, OutboxEntry) error { return nil })
	if err != nil {
		t.Fatalf("재생 실패: %v", err)
	}
	if res.Sent != 3 || res.Remaining != 0 {
		t.Fatalf("보냄=%d 남음=%d — 3/0 이어야 한다: %s", res.Sent, res.Remaining, res.Detail)
	}
	if _, err := os.Stat(o.pendingPath()); !os.IsNotExist(err) {
		t.Errorf("비운 뒤에도 옛 JSONL 이 남아 있다(err=%v) — 재생이 그 파일을 없앤다", err)
	}
	if names := queueFileNames(t, o.pendingDir()); len(names) != 0 {
		t.Errorf("항목 파일이 %d개 남았다: %v", len(names), names)
	}
}

// 옛 형식 병합이 **항목 파일을 JSONL 로 되쓰지 않는다.**
//
// ★ 이것을 안 막으면 같은 판단이 두 자리에 생긴다 — 항목 파일 하나와 JSONL 한 줄. 그 뒤
// 항목 파일이 지워져도 JSONL 쪽 사본이 남아 **영원히 재생된다.** 멱등 키가 살아 있는 동안은
// 헛 POST 로 끝나지만 멱등 표는 TTL 로 청소되므로, 그 뒤에는 원장에 판단이 두 줄 들어간다.
// 병합이 `o.List()` 를 부르면 정확히 그렇게 된다(두 형식을 합쳐 읽고 JSONL 로 되쓰므로).
func TestLegacyMergeNeverCopiesPerEntryFilesIntoTheJSONL(t *testing.T) {
	o := mkOutbox(t)
	seedQueue(t, o.Dir(), "old-stuck")
	if err := o.Append(entry("new-safe")); err != nil {
		t.Fatalf("적재 실패: %v", err)
	}
	// 옛 줄은 안 나가고(미도달) 새 줄만 나가게 한다 — 그래야 병합이 실제로 되쓰기를 한다.
	if _, err := o.Replay(context.Background(), func(_ context.Context, e OutboxEntry) error {
		if e.Key == "old-stuck" {
			return ErrUnreachable
		}
		return nil
	}); err != nil {
		t.Fatalf("재생 실패: %v", err)
	}
	raw, err := os.ReadFile(o.pendingPath())
	if err != nil {
		t.Fatalf("옛 JSONL 을 못 읽었다: %v", err)
	}
	if strings.Contains(string(raw), "new-safe") {
		t.Errorf("항목 파일의 판단이 옛 JSONL 에 복사됐다 — 같은 판단이 두 자리에 산다:\n%s", raw)
	}
}

// ── 이 항목의 존재 이유 ──────────────────────────────────────────────────────

// 깊은 큐에서도 **쌓기가 큐 크기에 안 매인다.**
//
// ★★ 이것이 이 항목의 존재 이유다. 옛 판은 `Append` 가 잠금 안에서 파일을 전량 읽고
// 전량 썼다 — 실측 ext4 로 빈 큐 14.6µs vs **1000건 10.0ms**. 그 O(큐 크기) 점유가
// 예산 250ms 를 먹어 세션이 몰릴 때 무잠금으로 떨어뜨렸고, 거기서 판단이 사라졌다.
//
// ★ **절대 시간이 아니라 비(比)로 잰다.** 붐비는 머신·느린 디스크에서 절대값은 흔들리지만
// "1000건일 때가 빈 큐일 때보다 몇 배인가"는 자료구조의 성질이다. 상한을 크게 잡은 이유도
// 같다 — 이 관문이 재려는 것은 "빨라졌다"가 아니라 **"큐 크기에 안 매인다"** 이고,
// 옛 판은 이 비가 수백 배였다(14.6µs → 10.0ms).
func TestAppendCostDoesNotGrowWithQueueDepth(t *testing.T) {
	if testing.Short() {
		t.Skip("시간을 재는 시험이다")
	}
	measure := func(o *Outbox, key string) time.Duration {
		start := time.Now()
		if err := o.Append(entry(key)); err != nil {
			t.Fatalf("적재 실패(%s): %v", key, err)
		}
		return time.Since(start)
	}
	// 빈 큐 — 여러 번 재고 중앙값에 가까운 값을 쓰기 위해 최소값을 취한다
	// (최소값은 잡음이 가장 적게 낀 표본이다).
	empty := time.Hour
	for i := 0; i < 20; i++ {
		o := newOutboxAt(t.TempDir())
		if d := measure(o, "probe"); d < empty {
			empty = d
		}
	}
	// 1000건 깊이
	deep := newOutboxAt(t.TempDir())
	for i := 0; i < 1000; i++ {
		if err := deep.Append(entry(fmtKey(i))); err != nil {
			t.Fatalf("%d번째 적재 실패: %v", i, err)
		}
	}
	loaded := time.Hour
	for i := 0; i < 20; i++ {
		if d := measure(deep, fmtKey(10000+i)); d < loaded {
			loaded = d
		}
	}
	// 20배는 아주 헐거운 상한이다. 옛 판은 같은 자리에서 **수백 배**였다.
	if loaded > 20*empty {
		t.Errorf("빈 큐 %s → 1000건 %s (%.1f배) — 쌓기가 큐 크기에 매여 있다. "+
			"그 매임이 예산을 먹고 판단을 잃게 한 원인이었다",
			empty, loaded, float64(loaded)/float64(empty))
	}
	t.Logf("쌓기 비용: 빈 큐 %s · 1000건 %s (%.2f배)", empty, loaded, float64(loaded)/float64(empty))
}

// ── 하네스 ───────────────────────────────────────────────────────────────────

// queueFileNames 는 항목당 파일 큐 자리의 `.json` 이름 전부다.
//
// ★ tmp 잔해(`.tmp-…`)를 함께 세지 않는다. 다만 **거르는 것이 아니라 이름으로 가른다** —
// tmp 가 남아 있으면 그것은 그것대로 결함이고, 아래 시험이 그 축을 따로 본다.
func queueFileNames(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("큐 자리를 못 읽었다(%s): %v", dir, err)
	}
	var out []string
	for _, de := range des {
		if strings.HasSuffix(de.Name(), ".json") {
			out = append(out, de.Name())
		}
	}
	return out
}

// 쌓기가 **잔해를 안 남긴다.**
//
// ★ tmp + Link 는 링크가 성공해도 tmp 이름이 같은 inode 를 가리킨 채 남는다. 안 치우면
// 큐 자리에 파일이 호출마다 하나씩 쌓이고, 그것은 이 항목이 없애려던 O(큐 크기) 스캔을
// 다른 이름으로 되살린다.
func TestAppendLeavesNoTempFiles(t *testing.T) {
	o := mkOutbox(t)
	for i := 0; i < 50; i++ {
		if err := o.Append(entry(fmtKey(i))); err != nil {
			t.Fatalf("%d번째 적재 실패: %v", i, err)
		}
	}
	des, err := os.ReadDir(o.pendingDir())
	if err != nil {
		t.Fatalf("큐 자리를 못 읽었다: %v", err)
	}
	var strays []string
	for _, de := range des {
		if !strings.HasSuffix(de.Name(), ".json") {
			strays = append(strays, de.Name())
		}
	}
	if len(strays) > 0 {
		t.Errorf("큐 자리에 잔해가 %d개 남았다: %v", len(strays), strays)
	}
}

// 깨진 항목 파일은 **조용히 안 버린다.**
//
// ★ readEntries 와 같은 규율이다(설계 §9). 이 자리를 건너뛰게 만들면 재생성 불가한
// 자산이 화면에 아무 흔적 없이 사라진다.
func TestBrokenEntryFileIsReportedNotSkipped(t *testing.T) {
	o := mkOutbox(t)
	if err := o.Append(entry("good")); err != nil {
		t.Fatalf("적재 실패: %v", err)
	}
	// ★ 이름을 `ffff…` 로 둔다. ReadDir 이 이름순이므로 깨진 파일이 **뒤에** 와야
	//   "읽은 데까지 함께 온다"를 잴 수 있다 — 앞에 오면 읽은 것이 0건인 게 정상이다.
	broken := filepath.Join(o.pendingDir(), strings.Repeat("f", 32)+".json")
	if err := os.WriteFile(broken, []byte("{이건 JSON 이 아니다"), 0o600); err != nil {
		t.Fatalf("깨진 파일을 못 만들었다: %v", err)
	}
	es, err := o.List()
	if err == nil {
		t.Fatal("깨진 항목을 조용히 건너뛰었다 — 판단은 조용히 사라지면 안 된다")
	}
	if !strings.Contains(err.Error(), "해석") {
		t.Errorf("오류가 무슨 일인지를 안 말한다: %v", err)
	}
	// ★ **읽은 데까지는 함께 온다.** 하나가 깨졌다고 나머지를 못 보면 복구가 더 어려워진다.
	if len(es) != 1 || es[0].Key != "good" {
		t.Errorf("읽은 데까지를 안 냈다: %+v", es)
	}
}

// fmtKey 는 시험용 키를 만든다. 키가 파일 이름으로 해시되므로 값 자체에 제약은 없다.
func fmtKey(i int) string {
	b, _ := json.Marshal(i)
	return "k-" + string(b)
}
