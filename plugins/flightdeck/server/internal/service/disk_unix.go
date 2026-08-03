//go:build unix

package service

import (
	"fmt"
	"syscall"
)

// diskFreePct 는 그 경로가 든 파일시스템의 여유 비율(0~100)이다.
//
// **디스크 고갈은 이 서버가 조용히 죽는 경로다**(설계 §7). 그래서 임계 감시는 세션이 아니라
// 자원에 붙이고, 그 값이 여기서 나온다.
// 못 재면 0이 아니라 **오류**를 낸다 — 0%는 "가득 찼다"는 값이고, 그 둘을 뭉개면
// 측정이 깨진 날 대시보드에 빨간 배너가 상시 점등돼 판별력이 0이 된다.
func diskFreePct(path string) (float64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("statfs 실패(path=%q): %w", path, err)
	}
	total := uint64(st.Blocks)
	if total == 0 {
		return 0, fmt.Errorf("statfs 가 전체 블록 0을 냈다(path=%q) — 값으로 쓸 수 없다", path)
	}
	avail := uint64(st.Bavail)
	return float64(avail) / float64(total) * 100, nil
}
