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

// ★ 보고는 있는데 못 재는 서버를 "보는 중"으로 찍으면 정반대의 안심을 준다 —
// 그 서버는 교체가 와도 못 보고 옛 코드로 영원히 산다.
func TestRenderHealthSaysTheWatcherIsStalled(t *testing.T) {
	var h healthzResponse
	h.OK, h.APIVersion, h.DBOK = true, "1", true
	h.SelfUpdate.Watching = true
	h.SelfUpdate.Stalled = "실행 파일을 못 쟀다: stat /usr/local/bin/fd: no such file or directory"

	got := RenderHealth(h, true, "http://x:7420")
	if !strings.Contains(got, "자동 갱신  **막혔다** — 실행 파일을 못 쟀다") {
		t.Fatalf("막혔다는 사실이 화면에 없다:\n%s", got)
	}
	if strings.Contains(got, "보는 중") {
		t.Fatalf("못 재고 있는데 '보는 중'이라 찍었다:\n%s", got)
	}
}

// ★ 보고는 있는데 **구조적으로 못 덮는** 갈래를 "보는 중"으로 접으면, 플러그인 버전이
// 올라도 안 바뀌는 서버가 화면에서는 따라오는 것으로 보인다 — 침묵보다 나쁜 틀린 안심이다.
// (2026-08-06 A/B 실측: 지문 이름에서는 75초 뒤에도 watching=true 뿐이었다.)
func TestRenderHealthSaysTheWatcherCannotCoverVersionBumps(t *testing.T) {
	var h healthzResponse
	h.OK, h.APIVersion, h.DBOK = true, "1", true
	h.SelfUpdate.Watching = true
	h.SelfUpdate.Uncovered = "이 실행 파일 이름에는 소스 트리가 박혀 있다(런처 bin/fd) — " +
		"플러그인 **버전이 오르면 다른 이름**이 지어져 이 자리는 아무도 안 덮는다"

	got := RenderHealth(h, true, "http://x:7420")
	for _, want := range []string{"자동 갱신  **한 갈래를 못 덮는다**", "버전이 오르면"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 화면에 없다:\n%s", want, got)
		}
	}
	if strings.Contains(got, "보는 중 — 아직 교체를 못 봤다") {
		t.Fatalf("못 덮는 갈래를 '보는 중'으로 접었다:\n%s", got)
	}
	// ★ **막혔다와 같은 문구를 쓰면 안 된다.** 처방이 다르다 — 저쪽은 못 재는 원인을
	// 고치는 것이고, 이쪽은 고칠 것이 없고 사람이 재기동한다.
	if strings.Contains(got, "막혔다") {
		t.Fatalf("못 덮는 갈래를 '막혔다'(일시 고장)로 접었다:\n%s", got)
	}
}

// 막힌 사실과 지난 거절은 **둘 다** 참일 수 있다. 하나가 다른 하나를 지우면 안 된다.
func TestRenderHealthShowsStallAndPastRefusalTogether(t *testing.T) {
	var h healthzResponse
	h.OK, h.APIVersion, h.DBOK = true, "1", true
	h.SelfUpdate.Watching = true
	h.SelfUpdate.Stalled = "실행 파일을 못 쟀다: permission denied"
	h.SelfUpdate.Outcome = "refused"
	h.SelfUpdate.From, h.SelfUpdate.To = "07e5df4", "1d044b2"
	h.SelfUpdate.Detail = "selfcheck exit 1"

	got := RenderHealth(h, true, "http://x:7420")
	for _, want := range []string{"막혔다", "permission denied", "거절", "07e5df4 → 1d044b2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 화면에 없다:\n%s", want, got)
		}
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

// ─────────────────────────────────────────────────────────────────────────────
// 원장 백업 축은 **어느 갈래에서도 침묵하지 않는다**
// ─────────────────────────────────────────────────────────────────────────────
func TestLedgerBackupLinesNeverGoSilent(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(h *healthzResponse)
		want    []string
		notWant []string
	}{
		{"배선 안 됨", func(h *healthzResponse) {
			h.LedgerBackup.Running = false
			h.LedgerBackup.Reason = "이 서버는 판단 원장 백업 축을 배선하지 않았다"
		}, []string{"안 돈다", "배선하지 않았다"}, nil},
		{"옛 서버라 축이 통째로 없다", func(h *healthzResponse) {}, // 전부 제로값
			[]string{"안 돈다", "이 축을 알리기 전 판일 수 있다"}, nil},
		{"켜졌지만 첫 회차 전", func(h *healthzResponse) {
			h.LedgerBackup.Running = true
		}, []string{"켜져 있다", "첫 회차가 안 끝났다"}, []string{"실패"}},
		{"떴다", func(h *healthzResponse) {
			h.LedgerBackup.Running = true
			h.LedgerBackup.LastAt = "2026-08-10T04:00:00.000000Z"
			h.LedgerBackup.Outcome = "wrote"
			h.LedgerBackup.Route = "/ledger"
		}, []string{"떴다", "2026-08-10", "/ledger"}, []string{"실패"}},
		{"안 바뀌어 건너뛰었다 — 실패가 아니다", func(h *healthzResponse) {
			h.LedgerBackup.Running = true
			h.LedgerBackup.LastAt = "2026-08-10T05:00:00.000000Z"
			h.LedgerBackup.Outcome = "unchanged"
		}, []string{"건너뛰었다"}, []string{"실패", "안 돈다"}},
		{"실패", func(h *healthzResponse) {
			h.LedgerBackup.Running = true
			h.LedgerBackup.LastAt = "2026-08-10T06:00:00.000000Z"
			h.LedgerBackup.Outcome = "failed"
			h.LedgerBackup.Detail = "디스크가 찼다"
		}, []string{"실패", "디스크가 찼다"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var h healthzResponse
			c.setup(&h)
			got := strings.Join(ledgerBackupLines(h), "\n")
			if strings.TrimSpace(got) == "" {
				t.Fatal("아무 줄도 안 냈다 — 읽는 쪽은 백업이 돌고 있다고 믿는다(설계 §13)")
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("%q 가 없다:\n%s", w, got)
				}
			}
			for _, nw := range c.notWant {
				if strings.Contains(got, nw) {
					t.Errorf("%q 가 있으면 안 된다:\n%s", nw, got)
				}
			}
		})
	}
}

// RenderHealth 가 그 줄을 실제로 낸다 — 순수 함수만 시험하면 배선이 빠져도 초록이다.
func TestRenderHealthCarriesLedgerBackup(t *testing.T) {
	var h healthzResponse
	h.OK, h.APIVersion, h.DBOK = true, "1", true
	h.LedgerBackup.Running = true
	h.LedgerBackup.LastAt = "2026-08-10T04:00:00.000000Z"
	h.LedgerBackup.Outcome = "failed"
	h.LedgerBackup.Detail = "디스크가 찼다"
	out := RenderHealth(h, true, "http://x")
	for _, w := range []string{"원장 백업", "실패", "디스크가 찼다"} {
		if !strings.Contains(out, w) {
			t.Errorf("화면에 %q 가 없다 — 잡이 죽어도 사람이 못 본다:\n%s", w, out)
		}
	}
}
