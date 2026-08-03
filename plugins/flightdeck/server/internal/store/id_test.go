package store

import (
	"sort"
	"strings"
	"testing"
)

// ULID 의 계약은 하나다: **id 순 정렬 = 생성순**.
// 그 계약이 깨지는 자리는 둘뿐이라 둘 다 시험한다 —
// ① 인코딩이 비트 자리를 틀리면 사전순이 값 순서와 어긋난다
// ② 같은 밀리초 안에서 난수만 쓰면 순서가 뒤집힌다

func TestNewIDShape(t *testing.T) {
	id := NewID()
	if len(id) != 26 {
		t.Fatalf("길이 26자를 기대했는데 %d자: %q", len(id), id)
	}
	for i, r := range id {
		if !strings.ContainsRune(crockford, r) {
			t.Errorf("%d번째 문자 %q 가 Crockford base32 밖이다 (I·L·O·U 는 없어야 한다): %q", i, r, id)
		}
	}
	// Crockford 는 헷갈리는 네 글자를 뺀다. 알파벳에 그것이 들어 있으면 인코딩이 표준이 아니다.
	for _, bad := range []string{"I", "L", "O", "U"} {
		if strings.Contains(crockford, bad) {
			t.Errorf("알파벳에 %q 가 있다 — Crockford base32 가 아니다", bad)
		}
	}
}

// 같은 밀리초 안에서도 엄격히 증가해야 한다.
// 루프를 아주 빠르게 돌려 대부분이 같은 ms 에 떨어지게 만든다.
func TestNewIDStrictlyIncreasing(t *testing.T) {
	const n = 20000
	ids := make([]string, n)
	for i := range ids {
		ids[i] = NewID()
	}
	for i := 1; i < n; i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("%d번째가 앞 것보다 크지 않다: %q <= %q", i, ids[i], ids[i-1])
		}
	}
	// 정렬해도 순서가 그대로여야 한다 = 사전순이 곧 생성순이다.
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	for i := range ids {
		if ids[i] != sorted[i] {
			t.Fatalf("사전순 정렬이 생성순과 어긋났다(%d번째): %q vs %q", i, ids[i], sorted[i])
		}
	}
}

func TestNewIDConcurrentUnique(t *testing.T) {
	const workers, each = 16, 500
	out := make(chan string, workers*each)
	done := make(chan struct{})
	for w := 0; w < workers; w++ {
		go func() {
			for i := 0; i < each; i++ {
				out <- NewID()
			}
			done <- struct{}{}
		}()
	}
	for w := 0; w < workers; w++ {
		<-done
	}
	close(out)
	seen := map[string]bool{}
	for id := range out {
		if seen[id] {
			t.Fatalf("중복 id: %q", id)
		}
		seen[id] = true
	}
	if len(seen) != workers*each {
		t.Fatalf("%d개를 기대했는데 %d개", workers*each, len(seen))
	}
}

// 인코딩은 손전개하면 자리 하나가 조용히 틀린다. 알려진 값으로 못박는다.
func TestEncodeCrockfordVectors(t *testing.T) {
	var zero [16]byte
	var ones [16]byte
	for i := range ones {
		ones[i] = 0xFF
	}
	var topBit [16]byte
	topBit[0] = 0x80
	var lowBit [16]byte
	lowBit[15] = 0x01

	cases := []struct {
		name string
		in   [16]byte
		want string
	}{
		{"전부 0", zero, "00000000000000000000000000"},
		// 128비트를 26×5=130비트로 읽으므로 첫 글자는 실질 3비트뿐이다 → 최대 7.
		{"전부 1", ones, "7ZZZZZZZZZZZZZZZZZZZZZZZZZ"},
		{"최상위 비트만", topBit, "40000000000000000000000000"},
		{"최하위 비트만", lowBit, "00000000000000000000000001"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := encodeCrockford(c.in); got != c.want {
				t.Errorf("encodeCrockford(%x) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// 표 밖 케이스: 값이 1 커지면 문자열도 반드시 커져야 한다(단조성).
// 표만 보면 "네 벡터가 맞다"까지고, 그 사이가 뒤집혀도 안 보인다.
func TestEncodeCrockfordMonotone(t *testing.T) {
	var prev string
	for i := 0; i < 4096; i++ {
		var b [16]byte
		b[14] = byte(i >> 8)
		b[15] = byte(i)
		got := encodeCrockford(b)
		if i > 0 && got <= prev {
			t.Fatalf("값 %d 에서 단조성이 깨졌다: %q <= %q", i, got, prev)
		}
		prev = got
	}
}

func TestInc80Carry(t *testing.T) {
	// 캐리가 바이트 경계를 넘어가는지 — 이 자리를 틀리면 같은 ms 에서 순서가 뒤집힌다.
	b := [10]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0xFF}
	if !inc80(&b) {
		t.Fatal("넘칠 리 없는데 false 가 왔다")
	}
	want := [10]byte{0, 0, 0, 0, 0, 0, 0, 0, 1, 0}
	if b != want {
		t.Errorf("캐리 결과가 틀렸다: %v, want %v", b, want)
	}

	// 표 밖 케이스: 80비트 전부 1이면 넘친다(false).
	var full [10]byte
	for i := range full {
		full[i] = 0xFF
	}
	if inc80(&full) {
		t.Error("80비트가 전부 1인데 넘침을 보고하지 않았다")
	}
	var zero [10]byte
	if full != zero {
		t.Errorf("넘친 뒤 값이 0이어야 한다: %v", full)
	}
}
