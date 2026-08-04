//go:build linux

package window

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Alive 는 pid 가 지금 살아 있는지다. Prune 이 이 판정을 인자로 받는 자리의 실물이다.
//
// ★ **ESRCH 만 "없다"다.** signal 0 은 아무것도 안 보내고 존재와 권한만 검사하는데,
// EPERM 은 "그 pid 는 있는데 남의 것"이라는 뜻이라 살아 있는 쪽이다. 둘을 뭉개
// "오류면 죽었다"로 접으면 다른 사용자로 뜬 창의 비콘을 지우고, 그 창은 다음 /clear 에서
// 표류를 못 고친다 — 지우는 쪽의 실수만 되돌릴 수 없다.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// PPidOf 는 /proc/<pid>/stat 에서 부모 pid 를 읽는다.
//
// ★ 필드를 공백으로 자르면 안 된다. 2번 필드가 실행파일 이름이고 괄호로 싸여 있는데
// 그 안에 공백이 들어갈 수 있다("(fd mcp)"). 그래서 **마지막 ')' 뒤부터** 자른다.
func PPidOf(pid int) (int, error) {
	f, err := statFields(pid)
	if err != nil {
		return 0, err
	}
	// f[0] 은 3번 필드(state)다. PPid 는 4번 필드이므로 f[1].
	if len(f) < 2 {
		return 0, fmt.Errorf("/proc/%d/stat 이 짧다(필드 %d개)", pid, len(f))
	}
	pp, err := strconv.Atoi(f[1])
	if err != nil {
		return 0, fmt.Errorf("/proc/%d/stat 의 PPid 가 수가 아니다(%q): %w", pid, f[1], err)
	}
	return pp, nil
}

// StartedOf 는 부팅 뒤 경과 틱(22번 필드)을 문자열 그대로 낸다.
//
// ★ 파싱하지 않는다. 쓰는 쪽과 읽는 쪽이 같은 헬퍼를 쓰므로 **일관성만 있으면 되고**
// 이식 가능한 의미는 필요 없다. 수로 바꾸면 오버플로·단위 해석이라는 틀릴 거리만 는다.
func StartedOf(pid int) (string, error) {
	f, err := statFields(pid)
	if err != nil {
		return "", err
	}
	// 22번 필드는 3번 필드부터 세어 20번째 → f[19].
	if len(f) < 20 {
		return "", fmt.Errorf("/proc/%d/stat 이 짧다(필드 %d개, 20개 이상이어야 한다)", pid, len(f))
	}
	if strings.TrimSpace(f[19]) == "" {
		return "", fmt.Errorf("/proc/%d/stat 의 시작 틱이 비었다", pid)
	}
	return f[19], nil
}

// statFields 는 /proc/<pid>/stat 을 3번 필드부터 자른 조각으로 낸다.
func statFields(pid int) ([]string, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return nil, fmt.Errorf("/proc/%d/stat 을 못 읽었다: %w", pid, err)
	}
	s := string(raw)
	i := strings.LastIndex(s, ")")
	if i < 0 || i+2 > len(s) {
		return nil, fmt.Errorf("/proc/%d/stat 의 꼴이 예상과 다르다", pid)
	}
	return strings.Fields(s[i+2:]), nil
}
