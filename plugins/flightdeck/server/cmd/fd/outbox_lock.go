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
// 그래서 잠그는 것은 **파일 조작 구간뿐**이고, 실측 점유는 잠금 한 번에 5.5µs 다.

// queueLockName 은 잠금 파일이다. 큐 파일 옆에 둔다.
//
// ★ **지우지 않는다.** 다 쓴 뒤 지우면 "지우는 순간과 여는 순간" 사이에 새 경합이
// 생겨서, 잠금 기구가 스스로 잠금이 필요한 대상이 된다. 빈 파일 하나가 남는 값이 싸다.
const queueLockName = ".lock"

// queueLockBudget 은 잠금을 기다려 보는 시간이다.
//
// 실측 점유가 5.5µs 라 이 예산은 두 자릿수 여유다. 가장 빡빡한 훅 예산(UserPromptSubmit
// 2s)의 12.5% 이기도 하다. 이보다 크게 잡을 이유가 없다 — 못 잡으면 어차피 무잠금으로
// 떨어지고, 그 갈래는 오늘과 같은 상태다.
const queueLockBudget = 250 * time.Millisecond

// queueLockRetry 는 재시도 간격이다. 점유가 µs 라 이 간격이면 사실상 첫 재시도에 잡힌다.
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
