package web

import (
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 활동 배지 — 순수 함수다. 이 패키지의 계약대로 **사유까지 돌려준다**.
//
// ★ mcp 를 활동에서 뺀 것이 이 함수의 핵심이다. mcp 는 도구 호출이면 무엇이든 찍어서
// 읽기 전용 board 하나로도, PreCompact 훅의 자동 초안 하나로도 켜진다(근거 전문은
// format.go 의 activityKinds 주석). 포함하면 배지가 상시 점등돼 판별력이 0이 된다 —
// 화면 ①이 선점만 내기로 한 이상 이 배지가 "쥐고만 있고 안 하는 세션"을 가리키는
// 유일한 축이다.
func TestActivityOfCountsPromptToolCommitAndNotMCP(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }

	cases := []struct {
		name    string
		sig     map[model.SignalKind]time.Time
		wantHas bool
		wantIn  string
	}{
		{
			name:    "prompt 하나면 활동이다",
			sig:     map[model.SignalKind]time.Time{model.SignalPrompt: ago(3 * time.Minute)},
			wantHas: true, wantIn: "3분",
		},
		{
			name:    "tool 만 있어도 활동이다 — 사람이 자리를 비워도 에이전트는 일한다",
			sig:     map[model.SignalKind]time.Time{model.SignalTool: ago(20 * time.Minute)},
			wantHas: true, wantIn: "20분",
		},
		{
			name:    "commit 은 서버가 직접 관측한 신호라 활동이다",
			sig:     map[model.SignalKind]time.Time{model.SignalCommit: ago(2 * time.Hour)},
			wantHas: true, wantIn: "2시간",
		},
		{
			name:    "가장 최근 것을 낸다",
			sig:     map[model.SignalKind]time.Time{model.SignalPrompt: ago(9 * time.Hour), model.SignalTool: ago(5 * time.Minute)},
			wantHas: true, wantIn: "5분",
		},
		{
			// ★ 이 갈래가 이 함수의 존재 이유다.
			name:    "mcp 뿐이면 활동이 아니다 — 조회 도구와 훅의 자동 초안이 그 신호를 찍는다",
			sig:     map[model.SignalKind]time.Time{model.SignalMCP: ago(1 * time.Minute)},
			wantHas: false, wantIn: "없음",
		},
		{
			name:    "push 만으로는 활동으로 안 친다 — 랜딩 뒤 떠난 세션이 일하는 것처럼 보인다",
			sig:     map[model.SignalKind]time.Time{model.SignalPush: ago(1 * time.Minute)},
			wantHas: false, wantIn: "없음",
		},
		{
			name:    "신호가 아예 없으면 활동이 아니다",
			sig:     nil,
			wantHas: false, wantIn: "없음",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			has, why := ActivityOf(now, c.sig)
			if has != c.wantHas {
				t.Fatalf("활동 판정 = %v, 기대 %v (사유: %q)", has, c.wantHas, why)
			}
			if !contains(why, c.wantIn) {
				t.Fatalf("사유 %q 에 %q 가 없다 — 불리언만 내면 사람이 무엇을 근거로 회수할지 못 정한다", why, c.wantIn)
			}
		})
	}
}

// 사유는 **비지 않는다.** 빈 문자열이면 화면이 배지 자리를 침묵으로 채우고,
// 그러면 "활동 없음"과 "이 축을 안 읽었다"가 구분되지 않는다.
func TestActivityOfAlwaysGivesAReason(t *testing.T) {
	now := time.Now().UTC()
	for _, sig := range []map[model.SignalKind]time.Time{
		nil,
		{},
		{model.SignalMCP: now},
		{model.SignalPrompt: now},
	} {
		if _, why := ActivityOf(now, sig); why == "" {
			t.Fatalf("사유가 비었다(sig=%v)", sig)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
