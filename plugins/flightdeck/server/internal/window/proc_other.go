//go:build !linux

package window

// ★ 지원 안 되는 플랫폼은 **오류를 낸다.** 빈 값을 돌려주면 호출부가 "읽었는데 비어 있다"와
// "못 읽었다"를 구분 못 하고, 그러면 시작 시각 대조가 언제나 통과해 방어가 사라진다
// (internal/service/disk_other.go 와 같은 규율).
func PPidOf(pid int) (int, error) { return 0, ErrUnsupported }

func StartedOf(pid int) (string, error) { return "", ErrUnsupported }
