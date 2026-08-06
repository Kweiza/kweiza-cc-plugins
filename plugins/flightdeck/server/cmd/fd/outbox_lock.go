package main

import (
	"os"
	"path/filepath"
	"time"
)

// 큐 하나에 대한 **프로세스 간** 배타 잠금이다.
//
// ★ 왜 프로세스 간이어야 하나. 아웃박스 자리는 채널 환경과 무관하게 머신당 하나로
// 일부러 고정돼 있다(env.go 의 OutboxPath, 설계 §7). 그래서 이 파일을 다투는 것은
// 한 프로세스의 고루틴 둘이 아니라 **서로 다른 fd 프로세스들**이다 — 세션마다 뜨는
// `fd mcp`, 세션 시작마다 뜨는 훅, 사람이나 에이전트가 치는 셸 명령.
// **프로세스 내 sync.Mutex 로는 이 축을 하나도 못 막는다.** 다른 프로세스는 그 뮤텍스가
// 있는 줄도 모른다. 그 사실이 이 파일이 존재하는 이유 전부다.
//
// ★ 왜 논블로킹인가. 훅은 예산이 2s·3s·10s 이고 종료코드가 항상 0 이어야 한다
// (hook.go 의 runHook). 잠금을 기다리다 예산을 넘기면 훅이 세션을 막는다 —
// 그것은 이 잠금이 막으려는 판단 유실보다 더 나쁘다. 그래서 **못 잡으면 포기하고
// 오늘과 같은 무잠금 경로로 떨어진다**(fail-open). 나빠지지는 않는다.
//
// ★ 왜 잠금을 짧게 쥐는가. 전송(HTTP 왕복)을 감싸면 늦게 온 쪽이 남의 망 왕복을
// 통째로 기다린다 — 실측으로 진짜 프로세스 둘 시험이 1초에서 30초 타임아웃으로 갔다.
// 그래서 잠그는 것은 **파일 조작 구간뿐**이다.
//
// ★ 여기에 "실측 점유는 잠금 한 번에 5.5µs" 라고 적혀 있었다. **거짓이었다.**
// 그 값은 코드의 잠금 구간으로 **존재하지 않는** 갈래(keep(nil) 단독)의 것이고,
// 실제 점유는 O(큐 크기)다 — 정확한 수치는 아래 queueLockBudget 주석에 있다.
// 짧게 쥔다는 판단 자체는 맞지만 그 근거로 이 수를 인용하면 안 된다.

// queueLockName 은 잠금 파일이다. 큐 파일 옆에 둔다.
//
// ★ **지우지 않는다.** 다 쓴 뒤 지우면 "지우는 순간과 여는 순간" 사이에 새 경합이
// 생겨서, 잠금 기구가 스스로 잠금이 필요한 대상이 된다. 빈 파일 하나가 남는 값이 싸다.
const queueLockName = ".lock"

// queueLockBudget 은 잠금을 기다려 보는 시간이다.
//
// ★ **점유는 상수가 아니라 O(큐 크기)다.** 잠금 구간의 첫 줄이 List(파일 전량 읽기)이고
// 끝이 keep(파일 전량 쓰기)이라 그렇다. 실측(ext4):
//
//	빈 큐 append        14.6µs      1000건 append   10.0ms
//	1건 병합            22.3µs      1000건 병합     17.9ms (p95 35.4ms)
//
// 그러니 "점유가 µs 라 여유가 두 자릿수"라고 말하면 안 된다 — 큐가 깊으면 한 자릿수다.
// 그 문장을 한 번 적었다가 리뷰에서 반증됐다. 근거로 삼았던 5.5µs 는 keep(nil) 단독값인데,
// **그 조합은 코드의 잠금 구간으로 존재하지도 않는다.**
//
// ★ 그래서 남는 한계를 정확히 적는다(안 잰 축이 아니라 **잰** 축이다):
// 큐가 300건을 넘고 세션 20~30 이 수백 ms 안에 몰리면 예산을 못 채우는 프로세스가 나오고,
// 그 프로세스는 무잠금 병합으로 떨어진다. 실측: 큐 1000건·세션 30 에서 경고 132회,
// 새 판단 유실 10/36. 예산을 30s 로 키우면 유실이 0 이 되지만 그건 훅 예산(2s·3s·10s)을
// 통째로 넘기므로 **못 쓴다** — 훅이 세션을 막는 것이 더 나쁘다.
// 이 한계의 진짜 해법은 예산 조정이 아니라 큐를 항목당 파일로 쪼개 잠금 구간을 O(1) 로
// 만드는 것이다(후속).
//
// ★ 그리고 **이 한계가 실제로 얼마나 밟히는지는 이제 셀 수 있다.** 예전에는 이 자리가
// "숨기지 않는 데까지 한다"였는데, 그것은 **주석이 말하고 도구는 침묵하는** 상태였다:
// fail-open 은 o.warn 으로만 흘렀고 그 로거는 stderr 로 가는데 fd doctor 는 파일만 읽는다.
// 지금은 recordFailOpen 이 큐 옆 failopen.jsonl 에 사건을 남기고 doctor 가 그 수를 낸다.
// 위 수치들은 합성 부하의 것이다 — **이 머신에서 실제로 몇 회인지는 그 화면이 답한다.**
//
// 250ms 인 이유: 가장 빡빡한 훅 예산(UserPromptSubmit 2s)의 12.5% 다. 그리고 상한은
// **호출당**이 아니라 **큐 수 × 이 값**이다 — flushAll 이 고정 자리 + 옛 자리들을 차례로
// 돌기 때문이다(실측: 큐 넷이 전부 막힌 상태에서 session-start 훅 1.03초).
const queueLockBudget = 250 * time.Millisecond

// queueLockRetry 는 재시도 간격이다.
//
// ★ 여기에 "점유가 µs 라 이 간격이면 사실상 첫 재시도에 잡힌다"라고 적혀 있었다.
// 반증된 5.5µs 위에 선 문장이다. 점유는 O(큐 크기)라 **빈 큐에서만** 그렇고, 1000건이면
// 10~18ms 라 재시도가 5~9회 필요하다. 2ms 를 유지하는 근거는 "첫 재시도에 잡힌다"가
// 아니라 **예산 250ms 안에 125회를 시도할 수 있다**는 것이다.
const queueLockRetry = 2 * time.Millisecond

// withQueueLock 은 잠금을 잡고 fn 을 돌린다.
//
// 반환의 셋을 갈라 읽어라 — 뭉개면 fail-open 이 오류 삼킴과 구분되지 않는다:
//   - (true, err)   잠갔고 fn 이 돌았다. err 은 **fn 의** 결과다.
//   - (false, nil)  예산 안에 못 잡았다. fn 은 **안 돌았다.** 호출자가 무잠금으로 처리해라.
//   - (false, err)  잠금 기구 자체가 실패했다(디렉토리·파일을 못 열었다). fn 은 안 돌았다.
func withQueueLock(dir string, budget time.Duration, fn func() error) (bool, error) {
	if !queueLockSupported {
		return false, nil // 이 플랫폼에는 기구가 없다. 잰 척하지 않는다(설계 §13)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(filepath.Join(dir, queueLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	defer f.Close()

	deadline := time.Now().Add(budget)
	for {
		got, err := tryQueueLock(f)
		if err != nil {
			return false, err
		}
		if got {
			// ★ 해제는 defer 가 아니라 여기서 명시적으로 한다 — f.Close() 도 잠금을
			// 풀지만, 그것에 기대면 "왜 풀렸는지"가 코드에서 안 보인다.
			defer unlockQueue(f)
			return true, fn()
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		time.Sleep(queueLockRetry)
	}
}
