//go:build !unix && !windows

package main

import "os"

// 이 플랫폼에는 프로세스 간 잠금 기구가 없다.
//
// ★ **잰 척하지 않는다**(설계 §13). `queueLockSupported = false` 면 withQueueLock 이
// 곧장 (false, nil) 을 내고 호출자는 무잠금 경로로 떨어진다 — 오늘과 정확히 같은 동작이다.
// 여기서 "항상 잠갔다"고 참을 돌려주는 것이 가장 나쁜 선택이다: 동작은 무잠금인데
// 코드와 로그는 잠갔다고 말하게 되고, 그러면 아무도 이 구멍을 못 본다.
const queueLockSupported = false

func tryQueueLock(*os.File) (bool, error) { return false, nil }
func unlockQueue(*os.File)                {}
