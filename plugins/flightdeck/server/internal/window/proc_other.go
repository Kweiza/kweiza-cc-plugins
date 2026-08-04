//go:build !linux

package window

// ★ 지원 안 되는 플랫폼은 **오류를 낸다.** 빈 값을 돌려주면 호출부가 "읽었는데 비어 있다"와
// "못 읽었다"를 구분 못 하고, 그러면 시작 시각 대조가 언제나 통과해 방어가 사라진다
// (internal/service/disk_other.go 와 같은 규율).
func PPidOf(pid int) (int, error) { return 0, ErrUnsupported }

func StartedOf(pid int) (string, error) { return "", ErrUnsupported }

// Alive 는 여기서 **살아 있다고 본다.** 위 둘과 달리 오류를 낼 자리가 없다 —
// Prune 의 계약이 bool 이고, 그것이 곧 "지울까"의 판정이기 때문이다.
//
// ★ 그래서 모를 때는 **안 지우는 쪽**으로 붙인다. false 를 내면 이 플랫폼의 Prune 이
// 멀쩡한 창의 비콘을 전부 지워 표류 수리가 조용히 죽는다. true 의 손해는 파일이 쌓이는
// 것뿐이고, 애초에 이 플랫폼은 StartedOf 가 없어 비콘이 심기지도 않는다.
func Alive(pid int) bool { return true }
