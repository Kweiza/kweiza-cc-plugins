package main

import (
	"strings"
	"testing"
)

// `fd setup` 의 **이음매** — 저장한 값이 실제로 다음 실행에 실리는가.
//
// ★ 순수 판정기 시험(setup_test.go)은 판정이 맞다는 것만 말하고 "저장이 실제로 먹히나"는
// 말하지 못한다. 여기서는 하네스로 진짜 명령을 두 번 돌려 **두 번째 실행이 첫 번째가 쓴 값을
// 읽는지**를 본다 — 설정이 안 살아남는 것이 이 항목의 본체이므로 그 축을 직접 눌러야 한다.

func TestSetupSavesAndTheNextRunReadsIt(t *testing.T) {
	h := newHarness(t)
	// 축을 푼 환경을 쓴다 — 하네스 기본 env 는 FD_URL 을 고정하고, 환경변수는 파일을 이기므로
	// 그대로 두면 이 시험이 "파일에서 읽었다"를 원리적으로 못 본다.
	env := h.unpinnedEnv(nil)
	delete(env, "FD_URL")

	// ── 대조가 성립했는지 먼저 단정한다 ─────────────────────────────────────
	code, out := h.runEnv(env, "", "setup")
	if code != 0 {
		t.Fatalf("setup 이 %d 로 끝났다:\n%s", code, out)
	}
	if !strings.Contains(out, "기본값") {
		t.Fatalf("전제가 깨졌다 — 저장 전인데 출처가 기본값이 아니다:\n%s", out)
	}

	code, out = h.runEnv(env, "", "setup", "--url", "http://10.0.0.5:7420", "--token", "s3cret")
	if code != 0 {
		t.Fatalf("setup --url 이 %d 로 끝났다:\n%s", code, out)
	}
	// ★ 저장 직후 **"다음 세션부터"** 를 반드시 말해야 한다. MCP 는 기동 시 환경을 한 번
	//   읽고 끝이라, 이 말이 없으면 사용자는 "설정했는데 안 된다"를 겪는다.
	if !strings.Contains(out, "다시 시작") {
		t.Errorf("저장했는데 '재시작해야 반영된다'를 안 말한다 — 지금 도는 MCP 는 옛 값을 든다:\n%s", out)
	}

	// 두 번째 실행이 그 값을 읽는가.
	code, out = h.runEnv(env, "", "setup")
	if code != 0 {
		t.Fatalf("두 번째 setup 이 %d 로 끝났다:\n%s", code, out)
	}
	if !strings.Contains(out, "10.0.0.5") {
		t.Errorf("저장한 주소를 다음 실행이 못 읽었다:\n%s", out)
	}
	if !strings.Contains(out, "config.json") {
		t.Errorf("출처가 config.json 이라고 안 말한다 — '왜 저 주소인가'에 답할 자리가 없다:\n%s", out)
	}
	if !strings.Contains(out, "클라이언트") {
		t.Errorf("원격 주소인데 역할을 클라이언트로 안 읽었다:\n%s", out)
	}
}

// doctor 도 같은 값을 **같은 출처와 함께** 낸다 — 두 화면이 다른 말을 하면 안 된다.
func TestDoctorReportsWhereTheAddressCameFrom(t *testing.T) {
	h := newHarness(t)
	env := h.unpinnedEnv(nil)
	delete(env, "FD_URL")

	if code, out := h.runEnv(env, "", "setup", "--url", "http://10.0.0.5:7420"); code != 0 {
		t.Fatalf("저장 실패:\n%s", out)
	}
	// ★ 종료코드를 단정하지 않는다. 일부러 없는 주소를 가리켰으니 doctor 가 미도달로
	// 0이 아닌 코드를 내는 것이 **옳은 동작**이다 — 여기서 보는 것은 출처 줄이다.
	_, out := h.runEnv(env, "", "doctor")
	for _, want := range []string{"10.0.0.5", "config.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor 출력에 %q 가 없다:\n%s", want, out)
		}
	}
}

// 환경변수가 파일을 이긴다 — 그리고 doctor 가 **어느 쪽에서 읽었는지** 말한다.
func TestEnvWinsAndDoctorSaysSo(t *testing.T) {
	h := newHarness(t)
	env := h.unpinnedEnv(nil)
	delete(env, "FD_URL")

	if code, out := h.runEnv(env, "", "setup", "--url", "http://from-file:7420"); code != 0 {
		t.Fatalf("저장 실패:\n%s", out)
	}
	env["FD_URL"] = "http://from-env:7420"
	_, out := h.runEnv(env, "", "doctor") // 미도달이라 코드는 0이 아니다 — 볼 것은 출처다
	if !strings.Contains(out, "from-env") {
		t.Errorf("환경변수가 파일을 못 이겼다:\n%s", out)
	}
	if !strings.Contains(out, "FD_URL") {
		t.Errorf("출처가 FD_URL 이라고 안 말한다 — '저장했는데 왜 안 바뀌나'에 답할 자리다:\n%s", out)
	}
}

// 토큰을 지울 수 있어야 한다 — 서버를 무인증으로 되돌리는 경로가 없으면 막힌다.
func TestSetupCanClearTheToken(t *testing.T) {
	h := newHarness(t)
	env := h.unpinnedEnv(nil)
	delete(env, "FD_URL")
	delete(env, "FD_TOKEN")

	if code, out := h.runEnv(env, "", "setup", "--url", "http://x:7420", "--token", "s3cret"); code != 0 {
		t.Fatalf("저장 실패:\n%s", out)
	}
	if code, out := h.runEnv(env, "", "setup"); code != 0 || !strings.Contains(out, "config.json") {
		t.Fatalf("전제가 깨졌다 — 토큰이 파일에서 안 읽힌다(%d):\n%s", code, out)
	}
	if code, out := h.runEnv(env, "", "setup", "--clear-token"); code != 0 {
		t.Fatalf("토큰 지우기 실패:\n%s", out)
	}
	_, out := h.runEnv(env, "", "doctor") // 미도달이라 코드는 0이 아니다 — 볼 것은 토큰 줄이다
	if !strings.Contains(out, "서버 토큰 없음") {
		t.Errorf("토큰을 지웠는데 doctor 가 아직 설정됐다고 한다:\n%s", out)
	}
}
