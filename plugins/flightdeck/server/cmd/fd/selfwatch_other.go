//go:build !unix

package main

import (
	"errors"
	"fmt"
)

// ★ 비유닉스는 **오류를 낸다.** 빈 값을 돌려주면 호출부가 "쟀는데 안 바뀌었다"와
// "못 쟀다"를 구분 못 하고, 그러면 감시기가 조용히 아무것도 안 하는 상태로 산다
// (internal/window/proc_other.go 와 같은 규율).
//
// syscall.Exec 이 없는 플랫폼이라 애초에 자기 재기동을 할 수 없다.
var errSelfWatchUnsupported = errors.New("이 플랫폼은 자기 재기동을 지원하지 않는다(syscall.Exec 부재)")

// exeIDOfPath 는 경로 하나를 잰다.
func exeIDOfPath(path string) (ExeID, error) {
	return ExeID{}, fmt.Errorf("%w (path=%q)", errSelfWatchUnsupported, path)
}

// selfWatchSupported 는 이 플랫폼에서 자기 재기동이 가능한가다.
func selfWatchSupported() bool { return false }
