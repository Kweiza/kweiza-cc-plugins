//go:build windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const queueLockSupported = true

// tryQueueLock 은 windows 판이다. 의미론은 unix 판과 같아야 한다 —
// **논블로킹이고, 프로세스가 죽으면 커널이 푼다.**
//
// LOCKFILE_FAIL_IMMEDIATELY 가 LOCK_NB 에 해당한다. 남이 쥐고 있으면
// ERROR_LOCK_VIOLATION 이 오는데 그것은 오류가 아니라 정상 결과다.
func tryQueueLock(f *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, new(windows.Overlapped))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, err
}

func unlockQueue(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
