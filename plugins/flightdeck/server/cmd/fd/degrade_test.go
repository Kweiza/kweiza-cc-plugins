package main

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 열화 경로 통합 시험 — 설계 §7 이 요구하는 자리다.
// "열화 경로는 안 돌리면 썩고, 그러면 정확히 필요한 순간에 없다."
//
// 단정의 좌표계는 **서버가 실제로 갖게 된 판단**과 **세션이 읽는 stdout** 둘이다.

// 오프라인 note → 아웃박스 → 재연결 → **중복 없이** 재생.
func TestOfflineNoteQueuesAndReplaysExactlyOnce(t *testing.T) {
	h := newHarness(t)
	// 먼저 온라인으로 세션을 연다(오프라인 캐시의 전제).
	if code, out := h.run("", "open"); code != 0 {
		t.Fatalf("세션 열기 실패(%d): %s", code, out)
	}
	h.down()

	// ① 오프라인 판단 둘. 둘 다 종료코드 0 이어야 한다 — 판단은 잃으면 끝이다.
	if code, out := h.run("", "note", "--kind", "decision", "--body", "첫째 판단"); code != 0 {
		t.Fatalf("오프라인 note 가 실패했다(%d): %s", code, out)
	} else {
		mustContain(t, "오프라인 note stdout", out, "아웃박스에 쌓았다")
	}
	if code, _ := h.run("", "note", "--kind", "decision", "--body", "둘째 판단"); code != 0 {
		t.Fatalf("두 번째 오프라인 note 가 실패했다")
	}
	// ② 같은 본문을 다시 — 훅 재시도의 모양이다. 아웃박스가 늘면 안 된다.
	if code, _ := h.run("", "note", "--kind", "decision", "--body", "첫째 판단"); code != 0 {
		t.Fatalf("재시도 note 가 실패했다")
	}

	ob := newOutbox(ResolveStateDir(envOf(h.env), ""))
	pend, err := ob.List()
	if err != nil {
		t.Fatalf("아웃박스 조회 실패: %v", err)
	}
	if len(pend) != 2 {
		t.Fatalf("아웃박스에 %d건이다 — 같은 본문의 재시도가 별건으로 쌓였다", len(pend))
	}
	// 대조 전제: 이 시점의 서버에는 판단이 하나도 없어야 한다.
	if got := len(h.judgments(model.JudgmentDecision)); got != 0 {
		t.Fatalf("대조 전제가 깨졌다 — 서버가 죽은 동안 판단 %d건이 들어갔다", got)
	}

	// ③ 재연결. 아무 명령이나 돌면 Flush 가 먼저 돈다.
	sent := append([]OutboxEntry(nil), pend...) // ④에서 **같은 키로 다시 쌓기** 위해 보관한다
	h.up()
	if code, out := h.run("", "status"); code != 0 {
		t.Fatalf("재연결 후 status 실패(%d): %s", code, out)
	}
	js := h.judgments(model.JudgmentDecision)
	if len(js) != 2 {
		t.Fatalf("재생 후 판단이 %d건이다 — 2건을 기대했다", len(js))
	}
	bodies := js[0].Body + "|" + js[1].Body
	mustContain(t, "재생된 판단", bodies, "첫째 판단", "둘째 판단")

	pend, err = ob.List()
	if err != nil || len(pend) != 0 {
		t.Fatalf("재생 뒤에도 아웃박스에 %d건 남았다(err=%v)", len(pend), err)
	}

	// ④ **서버를 재기동한 뒤 같은 키로 실제로 다시 보낸다.**
	//
	// ★ 앞 판의 이 자리는 거짓 초록이었다: 아웃박스가 비어 있어 재전송이 **아예 안 일어났고**,
	//   그래서 "판단이 2건 그대로"는 멱등의 근거가 아니라 "아무 일도 안 일어남"의 근거였다.
	//   그 상태에서는 서버가 재기동으로 멱등 기억을 통째로 잃어도 시험이 초록이다.
	//
	//   그리고 이 조합이 실제로 나는 상황이다 — 서버가 죽어 아웃박스가 쌓이고, 살아나서
	//   재생이 돈다. 그때 서버는 방금 재기동해 메모리 기억이 비어 있다.
	for _, e := range sent {
		if err := ob.Append(e); err != nil {
			t.Fatalf("아웃박스 재주입 실패: %v", err)
		}
	}
	// ── 대조 전제 ①: 정말 다시 쌓였는가(같은 키로) ──
	again, err := ob.List()
	if err != nil {
		t.Fatalf("아웃박스 조회 실패: %v", err)
	}
	if len(again) != 2 {
		t.Fatalf("전제가 깨졌다 — 재주입 뒤 아웃박스가 %d건이다. 이 상태로는 아래 단정이 무의미하다", len(again))
	}
	for i := range again {
		if again[i].Key != sent[i].Key {
			t.Fatalf("전제가 깨졌다 — 재주입한 키가 다르다(%q vs %q): 키가 다르면 멱등이 아니라 새 요청이다",
				again[i].Key, sent[i].Key)
		}
	}

	// 서버 재기동. 멱등 기억이 프로세스 메모리뿐이면 여기서 사라진다.
	h.down()
	h.up()

	if code, out := h.run("", "status"); code != 0 {
		t.Fatalf("재기동 후 status 실패(%d): %s", code, out)
	}
	// ── 본 판정: 판단은 추가 전용이라 중복이 들어가면 되돌릴 수 없다 ──
	if got := len(h.judgments(model.JudgmentDecision)); got != 2 {
		t.Fatalf("서버를 재기동한 뒤 같은 키로 다시 보냈더니 판단이 %d건이 됐다 — "+
			"멱등 기억이 재기동을 못 넘겼다(판단은 추가 전용이라 되돌릴 수 없다)", got)
	}
	pend, err = ob.List()
	if err != nil || len(pend) != 0 {
		t.Fatalf("재기동 뒤 재생에서 아웃박스에 %d건 남았다(err=%v)", len(pend), err)
	}
}

// 오프라인 pick 은 **거절된다.** 배타는 서버만 보장할 수 있다.
func TestOfflinePickIsRefusedWithReason(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.run("", "open"); code != 0 {
		t.Fatal("세션 열기 실패")
	}
	h.down()

	code, out := h.run("", "pick", "some-item")
	if code == 0 {
		t.Fatalf("오프라인 선점이 성공으로 끝났다 — 배타가 거짓이 된다:\n%s", out)
	}
	mustContain(t, "오프라인 pick stdout", out,
		"선점은 오프라인에서 안 된다",
		"배타는 서버만 보장할 수 있",
	)
	// 아웃박스에 새면 안 된다 — 나중에 재생되면 그때 배타가 깨진다.
	ob := newOutbox(ResolveStateDir(envOf(h.env), ""))
	pend, err := ob.List()
	if err != nil {
		t.Fatalf("아웃박스 조회 실패: %v", err)
	}
	for _, e := range pend {
		if strings.Contains(e.Path, "/claim") {
			t.Fatalf("선점이 아웃박스에 쌓였다: %s", e.Path)
		}
	}
}

// 오프라인 읽기는 **캐시 + 배너**다. 침묵하지 않는다.
func TestOfflineReadServesCacheWithBanner(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "status"); code != 0 {
		t.Fatalf("온라인 status 실패(%d): %s", code, out)
	}
	h.down()

	code, out := h.run("", "status")
	if code != 0 {
		t.Fatalf("오프라인 status 가 실패했다(%d):\n%s", code, out)
	}
	mustContain(t, "오프라인 status stdout", out,
		"⚠ 조정 서버 미도달",
		"안 되는 것: 새 항목 선점",
		"보드", // 캐시된 보드가 실제로 나온다
	)
}

// 캐시조차 없으면 **그 사실을 말한다.** 빈 화면은 "아무도 없다"로 읽힌다.
func TestOfflineReadWithoutCacheSaysSo(t *testing.T) {
	h := newHarness(t)
	h.down() // 한 번도 성공한 적 없이 죽인다

	code, out := h.run("", "status")
	if code == 0 {
		t.Fatalf("캐시도 없는데 성공으로 끝났다:\n%s", out)
	}
	mustContain(t, "캐시 없는 status stdout", out, "캐시 없음", "알 방법이 지금 없다")
}

// 발번은 **고정 키를 쓰지 않는다.** 고정하면 두 호출이 같은 번호를 받는다.
func TestAllocNeverReturnsTheSameNumberTwice(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.run("", "open"); code != 0 {
		t.Fatal("세션 열기 실패")
	}
	code1, out1 := h.run("", "alloc", "contract_revision")
	code2, out2 := h.run("", "alloc", "contract_revision")
	if code1 != 0 || code2 != 0 {
		t.Fatalf("발번 실패: %d/%d — %s %s", code1, code2, out1, out2)
	}
	if strings.TrimSpace(out1) == strings.TrimSpace(out2) {
		t.Fatalf("두 번 발번했는데 같은 번호가 나왔다(%q) — 멱등 키가 고정됐다", strings.TrimSpace(out1))
	}
	if strings.TrimSpace(out1) != "1" || strings.TrimSpace(out2) != "2" {
		t.Fatalf("발번이 1,2 가 아니다: %q %q", out1, out2)
	}
}

func TestIdempotencyStableTable(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"note", true}, {"finish", true}, {"add", true},
		{"alloc", false}, {"open", false}, {"beat", false},
		{"pick", false}, {"claim", false},
		{"모르는명령", false}, // ★ 표 밖: 기본값은 고정하지 않는 쪽이다
		{"", false},
	}
	for _, c := range cases {
		got, reason := IdempotencyStable(c.cmd)
		if got != c.want {
			t.Fatalf("%q: 고정=%v, %v 를 기대했다(사유 %q)", c.cmd, got, c.want, reason)
		}
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("%q: 사유가 비었다", c.cmd)
		}
	}
	// 같은 세션·같은 명령이라도 고정이 아니면 키가 달라야 한다.
	if FreshKey("s") == FreshKey("s") {
		t.Fatal("FreshKey 가 같은 값을 두 번 냈다 — 재사용되지 않아야 한다")
	}
}
