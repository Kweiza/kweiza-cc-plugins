//go:build unix

package main

import (
	"errors"
	"os"
	"syscall"
)

// ★ 갈리는 것은 **이 두 함수뿐**이다. 구조체도 상수도 빌드 태그로 복제하지 마라 —
// selfwatch 가 그 자리에서 한 번 데였다(태그 없는 시험 파일이 다른 플랫폼에서만
// 컴파일에 실패했다). 공통 로직은 전부 outbox_lock.go 에 있다.

const queueLockSupported = true

// tryQueueLock 은 **논블로킹** 배타 잠금을 시도한다. 남이 쥐고 있으면 (false, nil) 이다.
//
// ★ flock 을 고른 이유는 하나다 — **커널이 프로세스가 죽을 때 자동으로 푼다.**
// 이 저장소에서 프로세스가 죽는 것은 예외가 아니라 규칙이다: Claude Code 가 훅을
// 2s·3s·10s 예산으로 끊는다. O_EXCL 락파일이었다면 한 번 끊긴 훅 하나가 머신의 모든
// fd 재생을 영구히 막았을 것이고, 그러면 이 저장소가 이미 실측해 둔 "영구히 막힌 줄이
// 큐를 영구히 막았다"가 잠금 층에서 그대로 재현된다.
//
// ★ **NFS 에서는 신뢰할 수 없다.** flock 은 권고 락이고 NFS 클라이언트 간 보장이 없다.
// ~/.flightdeck 가 네트워크 홈이면 이 잠금은 조용히 안 걸린다. 그 경우 동작은
// **오늘과 정확히 같다**(무잠금) — 나빠지지는 않지만 좋아지지도 않는다. 안 잰 축이다.
func tryQueueLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	// 남이 쥐고 있다 — 오류가 아니라 정상 결과다.
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return false, err
}

func unlockQueue(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
