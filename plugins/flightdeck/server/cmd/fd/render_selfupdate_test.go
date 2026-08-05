package main

import (
	"strings"
	"testing"
)

func TestRenderHealthShowsRefusal(t *testing.T) {
	var h healthzResponse
	h.OK, h.APIVersion, h.DBOK = true, "1", true
	h.SelfUpdate.Watching = true
	h.SelfUpdate.LastAt = "2026-08-05T00:31:02Z"
	h.SelfUpdate.From, h.SelfUpdate.To = "07e5df4", "1d044b2"
	h.SelfUpdate.Outcome = "refused"
	h.SelfUpdate.Detail = "selfcheck exit 1 — 증분 계획이 거절된다"

	got := RenderHealth(h, true, "http://x:7420")
	for _, want := range []string{"자동 갱신", "거절", "07e5df4", "1d044b2", "selfcheck"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 화면에 없다:\n%s", want, got)
		}
	}
}

// ★ 안 보고 있는 서버는 그 사실을 말해야 한다. 침묵하면 '따라오고 있다'로 읽힌다.
func TestRenderHealthSaysNotWatching(t *testing.T) {
	var h healthzResponse
	h.OK, h.APIVersion, h.DBOK = true, "1", true
	h.SelfUpdate.Watching = false
	h.SelfUpdate.Reason = "이 서버는 컨테이너다 — 자기 이미지를 다시 만들 수 없다"

	got := RenderHealth(h, true, "http://x:7420")
	if !strings.Contains(got, "자동 갱신") || !strings.Contains(got, "컨테이너") {
		t.Fatalf("안 보고 있다는 사실이 화면에 없다:\n%s", got)
	}
}

// 정상일 때는 한 줄만 — 배경이 된 경고는 안 읽힌다.
func TestRenderHealthIsQuietWhenWatchingAndNothingHappened(t *testing.T) {
	var h healthzResponse
	h.OK, h.APIVersion, h.DBOK = true, "1", true
	h.SelfUpdate.Watching = true

	got := RenderHealth(h, true, "http://x:7420")
	if strings.Contains(got, "거절") || strings.Contains(got, "실패") {
		t.Fatalf("아무 일도 없었는데 경고가 있다:\n%s", got)
	}
	if !strings.Contains(got, "자동 갱신  보는 중") {
		t.Fatalf("감시 중이라는 사실이 없다:\n%s", got)
	}
}

// ★ 거절 경로에서 To 가 빈 채로 오는 것이 알려진 한계다(Task 4 지연 항목).
// 그때 "07e5df4 → " 처럼 화살표를 매달아 두면 부재가 빈칸으로 묻힌다 —
// 빈 쪽은 "(미상)"으로 말하고, 끝을 침묵으로 남기지 않는다.
func TestRenderHealthNeverEndsWithDanglingArrow(t *testing.T) {
	var h healthzResponse
	h.OK, h.APIVersion, h.DBOK = true, "1", true
	h.SelfUpdate.Watching = true
	h.SelfUpdate.From = "07e5df4"
	h.SelfUpdate.To = "" // ★ 알려진 한계: 거절 경로가 To 를 못 채우는 경우가 있다
	h.SelfUpdate.Outcome = "refused"

	got := RenderHealth(h, true, "http://x:7420")
	for _, line := range strings.Split(got, "\n") {
		trimmed := strings.TrimRight(line, " ")
		if strings.HasSuffix(trimmed, "→") {
			t.Fatalf("화살표가 매달려 있다(빈칸으로 끝난다):\n%s", got)
		}
	}
	if !strings.Contains(got, "07e5df4 → (미상)") {
		t.Fatalf("빈 쪽이 (미상) 으로 채워지지 않았다:\n%s", got)
	}
}
