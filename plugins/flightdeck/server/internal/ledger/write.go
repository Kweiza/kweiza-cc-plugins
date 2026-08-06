package ledger

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Write 는 인코딩된 파일들을 dir 에 원자적으로 쓴다. 만든 파일 이름을 정렬해 낸다.
//
// ★ tmp 에 쓰고 rename 한다. os.WriteFile 직접 쓰기는 중간 실패 시 반쪽 파일을 남긴다 —
// 판단 자산에는 맞지 않는다(cmd/fd/outbox.go 의 Outbox.keep 이 같은 규율의 선례다).
func Write(files map[string][]byte, dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("원장 디렉토리 생성 실패(%q): %w", clip(dir, 200), err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	// ★ map 순회는 순서가 흔들린다. 정렬해야 반환 목록과 출력이 실행마다 같다.
	sort.Strings(names)

	for _, name := range names {
		if err := writeAtomic(filepath.Join(dir, name), files[name]); err != nil {
			return nil, err
		}
	}
	return names, nil
}

// writeAtomic 은 tmp+rename 으로 쓴다.
func writeAtomic(path string, body []byte) error {
	// ★ tmp 이름은 프로세스마다 다르다. 고정 이름이면 떨어진 갈래 둘이 같은 tmp 에
	//   O_TRUNC 로 쓰고, 그러면 서로의 바이트가 섞인 채 rename 된다.
	tmp := fmt.Sprintf("%s.%s.tmp", path, tmpNonce())
	// 0600 — 판단 본문이라 남이 못 읽게 한다.
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		// ★ 실패해도 치운다. os.WriteFile 은 O_CREATE|O_TRUNC 로 먼저 만들고 쓰므로
		//   ENOSPC 로 죽어도 파일은 남고, 이름이 유일해진 뒤로는 아무도 안 치운다.
		os.Remove(tmp)
		return fmt.Errorf("원장 기록 실패(%q): %w", clip(path, 200), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("원장 교체 실패(%q): %w", clip(path, 200), err)
	}
	return nil
}

// tmpNonce 는 임시 파일 이름에 붙일 조각이다. pid 만으로는 부족하다 —
// 한 프로세스 안 두 고루틴도 같은 tmp 를 다툴 수 있다.
// (cmd/fd/outbox.go 에 같은 함수가 있지만 그것은 package main 이라 여기서 못 쓴다.)
func tmpNonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d-%x", os.Getpid(), time.Now().UnixNano())
	}
	return fmt.Sprintf("%d-%s", os.Getpid(), hex.EncodeToString(b[:]))
}

// clip 은 오류에 실을 외부 문자열을 자른다.
// (store·legacy 에 같은 함수가 있으나 둘 다 unexported 다.)
func clip(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}
