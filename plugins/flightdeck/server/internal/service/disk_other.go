//go:build !unix

package service

import (
	"fmt"
	"runtime"
)

// diskFreePct 는 이 플랫폼에서 재는 방법이 없다.
//
// 0을 돌려주지 않는다 — 0%는 "가득 찼다"는 값이라 "못 쟀다"와 뭉개면
// 그 플랫폼에서 상시 빨간불이 된다. 부재를 기본값으로 접지 않는 것이 설계 §13 의 요구다.
func diskFreePct(path string) (float64, error) {
	return 0, fmt.Errorf("%s 에서는 디스크 여유를 재는 경로가 없다(path=%q)", runtime.GOOS, path)
}
