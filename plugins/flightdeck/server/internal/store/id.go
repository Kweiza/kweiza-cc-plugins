package store

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

// ULID 를 직접 만든다. 외부 의존을 하나도 안 쓴다 —
// 26자 문자열 하나를 위해 모듈을 늘리지 않는다(의존성 최소주의).
//
// 구성은 표준 ULID 와 같다: 상위 48비트 = 밀리초 유닉스 시각, 하위 80비트 = 난수.
// Crockford base32 로 26자. 128비트를 5비트씩 26조각(=130비트)으로 끊으므로
// 맨 앞 2비트는 0으로 채운다.
//
// **왜 정렬 가능해야 하나**: session.id 가 이 값이고, 시각 컬럼과 별개로
// id 순 정렬이 곧 생성순이어야 "언제 것인지"를 조인 없이 안다.

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ" // I·L·O·U 를 뺀 32자

var idGen struct {
	mu     sync.Mutex
	lastMS int64
	last   [10]byte // 마지막으로 발급한 80비트 난수부
}

// NewID 는 새 ULID 를 만든다.
//
// ★ 같은 밀리초 안에서도 **엄격히 증가**한다. 난수만 쓰면 같은 ms 에 발급한 둘의
// 순서가 뒤집히고, 그러면 "id 순 = 생성순"이라는 이 함수의 유일한 계약이 깨진다.
// 같은 ms 면 앞 값의 난수부를 1 증가시킨다(80비트 빅엔디언 캐리).
// 80비트가 다 차는 극단(같은 ms 에 2^80회 발급)에서는 다음 ms 로 넘어간다.
func NewID() string {
	ms := time.Now().UTC().UnixMilli()

	idGen.mu.Lock()
	var ent [10]byte
	switch {
	case ms > idGen.lastMS:
		fillRandom(ent[:])
		idGen.lastMS, idGen.last = ms, ent
	default:
		// 같은 ms 이거나(정상) 시계가 뒤로 갔을 때(NTP 보정). 어느 쪽이든
		// 앞 값보다 커야 하므로 마지막 값에서 증가시킨다. 시계 역행에도 순서가 안 깨진다.
		ent = idGen.last
		if !inc80(&ent) {
			// 80비트 전부 소진. 다음 ms 로 밀고 새 난수를 뽑는다.
			idGen.lastMS++
			ms = idGen.lastMS
			fillRandom(ent[:])
		} else {
			ms = idGen.lastMS
		}
		idGen.last = ent
	}
	idGen.mu.Unlock()

	var b [16]byte
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	copy(b[6:], ent[:])
	return encodeCrockford(b)
}

// fillRandom 은 crypto/rand 로 채운다.
// rand.Read 는 Go 1.24+ 에서 결코 실패하지 않지만(실패하면 프로세스가 죽는다),
// 오류를 `_` 로 버리지 않기 위해 명시적으로 본다.
func fillRandom(p []byte) {
	if _, err := rand.Read(p); err != nil {
		// 여기 도달하면 엔트로피 원천이 죽은 것이다. id 를 못 만들면 세션도 판단도
		// 저장할 수 없으므로 조용히 약한 값으로 대체하지 않고 패닉한다.
		panic(fmt.Sprintf("crypto/rand 실패 — ULID 를 만들 수 없다: %v", err))
	}
}

// inc80 은 80비트 빅엔디언 정수를 1 증가시킨다. 넘치면 false.
func inc80(b *[10]byte) bool {
	for i := 9; i >= 0; i-- {
		b[i]++
		if b[i] != 0 {
			return true
		}
	}
	return false
}

// encodeCrockford 는 128비트를 26자로 만든다.
//
// 비트 단위로 읽는다 — 바이트 경계에 맞춘 손전개는 자리 하나만 틀려도
// "그럴듯하지만 정렬이 안 되는" 값을 내고, 그 오류는 눈으로 안 보인다.
func encodeCrockford(b [16]byte) string {
	out := make([]byte, 26)
	for i := 0; i < 26; i++ {
		var v byte
		for j := 0; j < 5; j++ {
			// 앞 2비트는 존재하지 않는 자리(130 - 128)라 0으로 읽는다.
			bit := i*5 + j - 2
			v <<= 1
			if bit >= 0 && b[bit/8]&(1<<(7-uint(bit%8))) != 0 {
				v |= 1
			}
		}
		out[i] = crockford[v]
	}
	return string(out)
}
