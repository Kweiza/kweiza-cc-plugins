package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 캐시 — 마지막 **성공** 응답. 서버가 죽었을 때 읽기가 낼 유일한 값이다.
//
// ★ 실패 응답은 절대 캐시하지 않는다. 넣는 순간 그 오류가 "마지막으로 알려진 사실"이 되어
// 영원히 재생된다.

// CacheEntry 는 캐시 한 칸이다. 언제 받은 것인지를 값과 **같은 파일에** 둔다 —
// 파일 mtime 에 기대면 백업·복사·rsync 가 시각을 바꿔 낡음 판정이 조용히 거짓이 된다.
type CacheEntry struct {
	At   time.Time       `json:"at"`
	Path string          `json:"path"` // 어느 요청의 응답인가
	Body json.RawMessage `json:"body"`
}

// CacheKey 는 요청 하나의 캐시 파일 이름이다. 순수 함수다.
//
// 경로와 질의를 통째로 해시한다 — 사람이 읽을 수 있게 접두를 남기되,
// 그 접두만으로 파일 이름을 만들면 project 가 다른 두 보드가 서로를 덮는다.
func CacheKey(path string) string {
	sum := sha256.Sum256([]byte(path))
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, strings.TrimPrefix(path, "/"))
	safe = strings.Trim(safe, "-")
	if len(safe) > 40 {
		safe = safe[:40]
	}
	if safe == "" {
		safe = "req"
	}
	return safe + "-" + hex.EncodeToString(sum[:6]) + ".json"
}

// Cache 는 상태 디렉토리 아래의 캐시 하나다.
type Cache struct{ dir string }

func newCache(sd StateDir) *Cache { return &Cache{dir: sd.sub("cache")} }

// Put 은 성공 응답을 보관한다. 실패해도 상위 동작을 죽이지 않고 사유를 돌려준다 —
// 캐시 쓰기 실패는 지금 이 요청의 실패가 아니다. 다만 **삼키지도 않는다.**
//
// ★ 임시 파일 이름은 **호출마다 다르다**(tmpPath). 고정 이름이던 시절에는 같은 path 로
// 둘이 동시에 들어오면 같은 tmp 파일을 O_TRUNC 로 열고 각자 offset 0 부터 써서,
// 교체는 원자인데 **교체되는 내용이 두 응답의 이어붙임**이 됐다. 길이가 1바이트만
// 달라도 JSON 이 깨진다. rename 이 막아 주는 것은 부분 기록이지 동시 기록이 아니다 —
// 원자여야 했던 것은 rename 한 단계가 아니라 "임시 파일에 쓰고 옮긴다"는 두 단계다.
func (c *Cache) Put(path string, body []byte, at time.Time) error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return fmt.Errorf("캐시 디렉토리 생성 실패: %w", err)
	}
	e := CacheEntry{At: at.UTC(), Path: path, Body: json.RawMessage(body)}
	buf, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("캐시 직렬화 실패: %w", err)
	}
	// 원자적으로 바꾼다. 부분 기록된 캐시는 다음 읽기에서 "깨진 JSON"이 되고,
	// 그러면 서버가 죽은 바로 그 순간에 마지막 스냅숏까지 잃는다.
	tmp := c.tmpPath(path)
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return fmt.Errorf("캐시 기록 실패: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(c.dir, CacheKey(path))); err != nil {
		os.Remove(tmp) // 이름이 유일해졌으니 실패한 tmp 를 다음 Put 이 안 덮는다
		return fmt.Errorf("캐시 교체 실패: %w", err)
	}
	return nil
}

// tmpPath 는 이번 Put 만 쓰는 임시 파일 자리다. **호출마다 달라야 한다.**
//
// ★ pid 만으로는 부족하다 — 한 프로세스 안 두 고루틴도 같은 키를 다툰다(훅은
// PostToolUse·PreCompact 가 async 라 동기 훅과 겹쳐 돈다). pid-only 판은 프레임 루프를
// 병렬화하는 날 조용히 무력해진다.
func (c *Cache) tmpPath(path string) string {
	return filepath.Join(c.dir, CacheKey(path)+"."+tmpNonce()+".tmp")
}

// Get 은 보관된 응답을 낸다. 없으면 오류다(빈 값을 내면 "없음"과 "빈 결과"가 뭉개진다).
func (c *Cache) Get(path string) (CacheEntry, error) {
	var e CacheEntry
	b, err := os.ReadFile(filepath.Join(c.dir, CacheKey(path)))
	if err != nil {
		return e, fmt.Errorf("캐시 없음(%s): %w", clip(path, 120), err)
	}
	if err := json.Unmarshal(b, &e); err != nil {
		return e, fmt.Errorf("캐시가 깨졌다(%s): %w", clip(path, 120), err)
	}
	return e, nil
}

// LastContact 는 아무 캐시든 가장 최근 성공 시각이다. 배너의 "마지막 접속"이 이 값이다.
// 하나도 없으면 0값 — 호출부가 "캐시 없음"으로 표시한다.
func (c *Cache) LastContact() time.Time {
	ents, err := os.ReadDir(c.dir)
	if err != nil {
		return time.Time{} // 디렉토리 자체가 없다 = 한 번도 성공한 적이 없다
	}
	var last time.Time
	for _, de := range ents {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(c.dir, de.Name()))
		if err != nil {
			continue // 이 칸만 못 읽는다. 다른 칸으로 계속한다
		}
		var e CacheEntry
		if json.Unmarshal(b, &e) != nil {
			continue // 깨진 칸은 시각의 근거가 못 된다
		}
		if e.At.After(last) {
			last = e.At
		}
	}
	return last
}
