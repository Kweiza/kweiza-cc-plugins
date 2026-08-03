package main

import (
	"strings"
	"testing"
)

// 리뷰가 찾은 결함: REST 전환이 note 를 **내용 해시 멱등 키** 뒤로 옮겼는데
// 재생 사실이 응답에 하나도 안 실렸다. 도구가 "저장했다"고 말하는데 원장은 그대로였고,
// 응답 문구도 판단 id 도 첫 호출과 글자 그대로 같아 판별할 방법이 없었다.
//
// 판단은 파생 불가한 유일한 자산이고 추가 전용이라, 삼켜진 노트는 다른 세션에게 영영 안 보인다.
//
// 그리고 이 축은 **변이 주입으로도 안 잡힌다** — 기존 열화 표의 모든 케이스가 서로 다른 본문을
// 쓰므로 "같은 본문 두 번"이라는 입력을 원리적으로 안 만든다. 입력 공간을 넓혀야만 보인다.
func TestSameNoteTwiceSaysItWasReplayed(t *testing.T) {
	h := newHarness(t)
	rig := newMCPRig(t, h, "cc-replay")

	args := map[string]any{"kind": "now", "body": "계속 진행"}
	first, isErr := mcpText(t, lastFrame(t, mcpServe(t, rig, mcpCall("note", args))))
	if isErr {
		t.Fatalf("전제가 깨졌다 — 첫 호출이 실패했다:\n%s", first)
	}
	if strings.Contains(first, "이미 있었다") {
		t.Fatalf("전제가 깨졌다 — 첫 호출이 이미 재생으로 나왔다:\n%s", first)
	}
	if n := len(h.judgments("now")); n != 1 {
		t.Fatalf("전제가 깨졌다 — 첫 호출 뒤 판단이 %d건이다(1건이어야 한다)", n)
	}

	// 같은 본문 = 같은 내용 해시 = 같은 멱등 키
	second, secondErr := mcpText(t, lastFrame(t, mcpServe(t, rig, mcpCall("note", args))))
	if secondErr {
		t.Errorf("재생은 실패가 아니다 — isError 가 참이면 에이전트가 다시 시도한다:\n%s", second)
	}

	if n := len(h.judgments("now")); n != 1 {
		t.Fatalf("두 번째 호출이 판단을 새로 만들었다(%d건) — 멱등이 안 걸렸다", n)
	}
	if !strings.Contains(second, "이미 있었다") && !strings.Contains(second, "replay") {
		t.Errorf("두 번째 호출이 재생 사실을 말하지 않는다 — 원장은 그대로인데 \"저장했다\"로 읽힌다:\n%s", second)
	}
}

// lastFrame 은 응답 프레임 중 마지막(= tools/call 결과)을 낸다.
func lastFrame(t *testing.T, fs []mcpFrame) mcpFrame {
	t.Helper()
	if len(fs) == 0 {
		t.Fatal("응답 프레임이 하나도 없다")
	}
	return fs[len(fs)-1]
}
