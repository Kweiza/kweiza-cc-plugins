package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// 루프백 축은 **설정과 관측 둘 다** 화면에 나와야 한다.
//
// ★ 하나만 내면 두 상황이 화면에서 같아진다 — "면제를 껐다"(의도한 상태)와
// "면제는 켰는데 아무도 못 받는다"(배선 결함). 처방이 정반대인데 글자가 같다.
// 앞선 판은 `루프백 개방 %v` 한 값만 냈고, 그 값이 설정이었다.
func TestRenderHealthSeparatesLoopbackConfigFromReach(t *testing.T) {
	var h healthzResponse
	h.OK, h.APIVersion, h.DBOK = true, "1", true
	h.Auth.TokenSet = true
	h.Auth.LoopbackConfigured = true
	h.Auth.LoopbackOpen = false // 설정은 열렸는데 도달이 없다
	// ★ Notice 를 **일부러 비운다.** 채워 두면 이 시험은 그 문장을 타고 통과하고,
	// 라벨이 한 값만 내는 상태를 전혀 안 잠근다. 그리고 이 축을 아직 모르는 옛 서버는
	// 실제로 notice 가 비어서 오므로, 그때도 라벨만으로 두 축이 갈려야 한다.

	got := RenderHealth(h, true, "http://x:7420")
	for _, want := range []string{"설정", "도달"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 축이 라벨에 없다 — notice 없이는 설정과 관측을 못 가른다:\n%s", want, got)
		}
	}
}

// 사유 줄은 라벨과 **함께** 나와야 한다. 요약만 내면 "왜"가 사라진다.
// (이 갈래는 앞선 판에도 있던 동작이다 — 라벨을 고치면서 흘리지 않았는지를 잠근다.)
func TestRenderHealthKeepsTheReasonLineBesideTheLabel(t *testing.T) {
	var h healthzResponse
	h.OK, h.APIVersion, h.DBOK = true, "1", true
	h.Auth.TokenSet, h.Auth.LoopbackConfigured = true, true
	h.Auth.Notice = "루프백 면제는 설정상 열려 있으나 도달한 요청이 없다 — 컨테이너라서다"

	got := RenderHealth(h, true, "http://x:7420")
	if !strings.Contains(got, "컨테이너") {
		t.Fatalf("안 닿는 사유가 화면에서 잘렸다:\n%s", got)
	}
}

// 클라이언트 미러가 새 축을 **읽어야** 화면이 그 값을 낼 수 있다.
//
// ★ 미러 타입은 조용히 낡는다 — 서버가 필드를 더해도 클라이언트 구조체에 없으면
// json 이 그냥 버리고, 화면은 제로값을 정상값으로 찍는다. 그 침묵을 여기서 깬다.
func TestHealthzResponseReadsLoopbackConfigured(t *testing.T) {
	raw := `{"ok":true,"api_version":"1","db_ok":true,
	         "auth":{"token_set":true,"loopback_open":false,"loopback_configured":true,"notice":"x"}}`
	var h healthzResponse
	if err := json.Unmarshal([]byte(raw), &h); err != nil {
		t.Fatalf("역직렬화 실패: %v", err)
	}
	if !h.Auth.LoopbackConfigured {
		t.Fatal("서버가 보낸 loopback_configured 를 클라이언트가 버린다 — 화면이 설정과 관측을 못 가른다")
	}
	if h.Auth.LoopbackOpen {
		t.Fatal("관측값이 뒤집혔다")
	}
}

// 컨테이너 판정이 **api 표면까지 실제로 가는가.**
//
// ★ 이 시험이 이 항목의 핵심 가드다. 이 항목이 고치는 결함 자체가
// "배선과 광고가 어긋났는데 전 스위트가 초록이었다"였다. 조립이 serve 본문
// 안에만 있으면 축 하나가 빠져도 아무 시험이 안 잡는다 — 그래서 순수 함수로 뽑는다.
func TestServeAPIOptionsCarriesTheContainerVerdict(t *testing.T) {
	in := serveAPIOptions("tok", 60, quietLogger(), true, nil, nil, false)
	if !in.InContainer {
		t.Fatal("컨테이너 판정이 api 옵션까지 안 간다 — /healthz 와 401 처방이 '왜 면제가 안 닿는가'를 말할 근거를 잃는다")
	}
	out := serveAPIOptions("tok", 60, quietLogger(), false, nil, nil, false)
	if out.InContainer {
		t.Fatal("컨테이너가 아닌데 컨테이너라고 넘긴다 — 사유가 틀리면 안 말하느니만 못하다")
	}
	// 같은 조립이 나르던 다른 축들이 사라지지 않았는가. 뽑아내면서 흘리는 것이 흔하다.
	if in.Token != "tok" || in.RatePerMinute != 60 {
		t.Fatalf("기존 축이 조립에서 빠졌다: token=%q rate=%d", in.Token, in.RatePerMinute)
	}
}

// TestServeAPIOptionsCarriesLoopbackSwitch 는 스위치가 실제로 옵션에 실리는지 본다.
//
// ★ 이 축이 안 잠기면 스위치가 **조용히 죽는다.** 운영자가 -require-token-on-loopback 을
// 켰는데 아무 일도 안 일어나고, 그 사실이 증상으로 안 드러난다 — 면제는 원래 눈에 안
// 보이고, 안 걸리는 것과 안 열린 것이 화면에서 같기 때문이다.
func TestServeAPIOptionsCarriesLoopbackSwitch(t *testing.T) {
	if serveAPIOptions("tok", 60, quietLogger(), false, nil, nil, false).RequireTokenOnLoopback {
		t.Error("기본값이 참이다 — 로컬 루프백으로 토큰 없이 붙던 세션이 전부 깨진다")
	}
	if !serveAPIOptions("tok", 60, quietLogger(), false, nil, nil, true).RequireTokenOnLoopback {
		t.Error("스위치를 켰는데 옵션에 안 실렸다 — 플래그가 조용히 죽는다")
	}
}
