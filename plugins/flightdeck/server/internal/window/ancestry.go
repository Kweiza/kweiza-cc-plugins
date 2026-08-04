package window

import "errors"

// ErrUnsupported 는 이 플랫폼에서 계보를 읽을 수 없다는 뜻이다.
//
// ★ 빈 값이 아니라 오류인 것이 계약이다(internal/service/disk_other.go 선례).
// StartedOf 가 빈 문자열을 내면 대조가 언제나 통과해 pid 재사용 방어가 조용히 사라진다.
var ErrUnsupported = errors.New("이 플랫폼에서는 프로세스 계보를 읽을 수 없다")

// Ancestors 는 pid 에서 위로 올라가며 만난 pid 를 순서대로 낸다(자기 자신을 포함한다).
// 순수 함수다 — 프로세스 표를 인자로 받으므로 시험이 진짜 /proc 을 안 건드린다.
//
// ★ 깊이 제한이 필수다. PPid 가 자기 자신을 가리키는 표(컨테이너 pid 네임스페이스에서
// 실제로 나온다)를 만나면 제한이 없을 때 영원히 돈다.
//
// ★ 중간에 못 읽어도 **읽은 데까지 낸다.** 조상 하나를 못 읽었다고 전부 버리면
// 그 아래에서 이미 찾은 비콘까지 못 쓰게 된다.
func Ancestors(pid int, ppidOf func(int) (int, error), max int) []int {
	if pid <= 0 || max <= 0 {
		return nil
	}
	out := make([]int, 0, max)
	seen := make(map[int]bool, max)
	cur := pid
	for len(out) < max {
		if cur <= 1 || seen[cur] {
			break
		}
		seen[cur] = true
		out = append(out, cur)
		pp, err := ppidOf(cur)
		if err != nil {
			break
		}
		cur = pp
	}
	return out
}
