package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// 이 파일이 지키는 것: **같은 키에 동시에 들어온 두 Put 이 서로의 바이트를 안 섞는다.**
//
// ★ 왜 rename 이 이것을 못 막나. `os.Rename` 은 원자가 맞지만, 원자여야 했던 것은
// rename 한 단계가 아니라 "**임시 파일에 쓰고** 옮긴다"는 두 단계다. tmp 이름이 키마다
// 하나로 고정돼 있으면 둘이 같은 파일을 O_TRUNC 로 열고 각자 offset 0 부터 쓴다.
// 짧은 쪽이 앞을 덮고 긴 쪽의 꼬리가 남아, 교체는 원자인데 내용이 두 응답의 이어붙임이
// 된다. **길이가 1바이트만 달라도** JSON 이 깨진다.
//
// 피해는 서버가 죽은 그 순간에만 드러난다 — 캐시가 유일한 값이 되는 정확히 그때다.
// 그때 Client.Read 는 "캐시도 없다"고 말한다. 캐시는 **있는데** 못 읽는 것을 없다고 한다.
//
// ★ **여기에 동시 Put 의 뒤섞임을 직접 재는 시험은 없다. 일부러 없다.**
// 재현은 됐지만(60,000 쌍 중 11회 = 0.018%) 확률이 그 자리에 있다. 깨지려면
// A.open → B.open(O_TRUNC) → A.write(긴 것) → B.write(짧은 것) 순이어야 하는데 그 창이
// µs 미만이다. 그 확률을 시험으로 붙들려면 수만 쌍을 돌려야 하고(재현자 실측 421초),
// 그러고도 깜빡인다. **깜빡이는 관문은 이 저장소에 해롭다** — 빨강을 무시하는 습관을
// 만들고, 그 습관이 진짜 회귀를 통과시킨다. 그래서 확률을 재는 대신 **그 확률을 0으로
// 만드는 계약**(tmp 이름이 호출마다 다르다)을 결정적으로 재고, 못 재는 축은 이렇게 적어 둔다.
// -race 도 이 축을 원리적으로 못 본다 — 공유 상태가 Go 메모리가 아니라 파일이다.

// 임시 파일 이름은 호출마다 다르다. 이것이 뒤섞임을 0 으로 만드는 계약이다.
func TestCacheTempPathIsUniquePerCall(t *testing.T) {
	c := &Cache{dir: t.TempDir()}
	const path = "/api/v1/board?project=p"

	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		tmp := c.tmpPath(path)
		if seen[tmp] {
			t.Fatalf("%d번째 호출이 이미 나온 임시 자리를 또 냈다: %s\n"+
				"같은 tmp 를 둘이 O_TRUNC 로 열면 최종 캐시가 두 응답의 이어붙임이 된다", i, tmp)
		}
		seen[tmp] = true
	}
	// 그리고 그 이름은 여전히 이 키의 것이어야 한다 — 유일해지느라 키를 잃으면
	// 정리·진단이 어느 키의 잔해인지 못 말한다.
	if !strings.HasPrefix(c.tmpPath(path), c.dir) ||
		!strings.Contains(c.tmpPath(path), strings.TrimSuffix(CacheKey(path), ".json")) {
		t.Errorf("임시 자리가 키를 안 담고 있다: %s", c.tmpPath(path))
	}
}

// 실패한 임시 파일이 캐시 디렉토리에 쌓이지 않는다.
//
// ★ 유일화와 정리는 **짝이다.** 고정 이름일 때는 다음 Put 이 같은 tmp 를 덮어써서
// 청소가 공짜로 됐다. 유일해지는 순간 그 청소가 사라지므로, 정리를 함께 넣지 않으면
// 이 축을 고치면서 "캐시에 쓰레기가 무한히 쌓인다"는 새 갈래를 연다.
func TestCachePutLeavesNoTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	c := &Cache{dir: dir}
	for i := 0; i < 20; i++ {
		body := json.RawMessage(fmt.Sprintf(`{"n":%d}`, i))
		if err := c.Put(fmt.Sprintf("/api/v1/thing/%d", i), body, time.Now()); err != nil {
			t.Fatalf("Put 실패: %v", err)
		}
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("캐시 디렉토리를 못 읽었다: %v", err)
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("임시 파일이 남았다: %s — 유일한 이름은 다음 Put 이 안 치운다", e.Name())
		}
	}
}
