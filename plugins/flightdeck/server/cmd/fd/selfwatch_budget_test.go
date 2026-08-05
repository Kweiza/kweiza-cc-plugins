package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/api"
)

// 이 파일은 **만료 조건을 빨간불로 바꾼다.** 산문에만 있는 조건은 아무도 안 본다
// (`TestBundledMigrationsAreAdditive` 가 이 레포에 그 선례를 만들었다).

// TestSelfUpdateBudgetFitsSkewCeiling 은 자기 갱신 반응 시간이 스큐 창 상한 아래인지 본다.
//
// 예산은 파생이라 항 하나가 늘면 자동으로 늘어난다 — api.ShutdownGrace 를 SSE 때문에
// 올리거나 selfVerifyTimeout 을 콜드 DB 때문에 올리면 여기서 걸린다.
// **그 결합을 말하는 자리가 이 시험 말고 없다.**
func TestSelfUpdateBudgetFitsSkewCeiling(t *testing.T) {
	if selfUpdateReactionBudget > selfUpdateSkewCeiling {
		t.Fatalf("자기 갱신 반응 예산이 %s 라 스큐 창 상한 %s 를 넘는다.\n"+
			"항: 탐지 %s + 검증 %s + 드레인 %s.\n"+
			"상한을 넘기면 새 클라이언트가 옛 서버를 부르는 창이 길어져 버전 스큐 배너가 "+
			"상시 점등되고 판별력을 잃는다(설계 §10 이 이름 붙인 실패 모양이다).\n"+
			"항 중 하나를 줄이거나, 도착 분포를 다시 재서 상한 자체를 옮겨라 — "+
			"상한만 올리는 것은 근거 없이 숫자를 미는 것이다.",
			selfUpdateReactionBudget, selfUpdateSkewCeiling,
			defaultSelfWatchInterval, selfVerifyTimeout, api.ShutdownGrace)
	}
}

// TestSelfUpdateBudgetTermsAreDeclaredLimits 는 예산이 **선언된 상한의 합**이라는 사실을
// 붙든다. 항이 하나 빠지거나 근거 없는 상수가 하나 끼어들면 여기서 걸린다.
//
// 이 시험이 없으면 예산은 "그럴듯한 숫자"로 표류한다 — 이 항목이 없애려던 것 그대로다.
func TestSelfUpdateBudgetTermsAreDeclaredLimits(t *testing.T) {
	want := defaultSelfWatchInterval + selfVerifyTimeout + api.ShutdownGrace
	if selfUpdateReactionBudget != want {
		t.Fatalf("예산 %s 가 항의 합 %s 와 다르다 — 파생이 아니라 손으로 적은 값이 됐다",
			selfUpdateReactionBudget, want)
	}
}

// TestComposeStopGraceExceedsShutdownGrace 는 **교차 파일 불변식**을 붙든다.
//
// compose 의 stop_grace_period 가 api.ShutdownGrace 보다 크지 않으면, 유예를 다 쓴 종료가
// SIGKILL 과 같은 순간에 잘려 "유예 안에 마무리를 못 했다" ERROR 조차 안 남는다 —
// 정확히 그 줄이 필요한 상황에서 사라진다. 두 값이 다른 파일에 있으니 주석으로는 못 막는다.
func TestComposeStopGraceExceedsShutdownGrace(t *testing.T) {
	root := pluginRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "compose.yaml"))
	if err != nil {
		t.Fatalf("compose.yaml 을 못 읽었다: %v", err)
	}
	m := regexp.MustCompile(`(?m)^\s*stop_grace_period:\s*(\S+)\s*$`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("compose.yaml 에 stop_grace_period 가 없다.\n"+
			"docker 기본값은 10초이고 api.ShutdownGrace 도 %s 라 부등식이 깨진다 — "+
			"유예를 다 쓴 종료가 그 사실을 말하기 전에 SIGKILL 로 잘린다.", api.ShutdownGrace)
	}
	got, err := time.ParseDuration(strings.TrimSpace(m[1]))
	if err != nil {
		t.Fatalf("stop_grace_period %q 를 못 읽었다: %v", m[1], err)
	}
	if got <= api.ShutdownGrace {
		t.Fatalf("stop_grace_period %s 가 api.ShutdownGrace %s 보다 크지 않다 — "+
			"유예 초과 ERROR 가 남을 창이 없다", got, api.ShutdownGrace)
	}
}

// TestDetectLagRefusesToFoldUnmeasurable 은 못 잰 것을 0 으로 접지 않는지 본다.
//
// 접으면 "즉시 봤다"와 "잴 수 없다"가 같은 값이 되고, 그 둘은 뜻이 반대다.
func TestDetectLagRefusesToFoldUnmeasurable(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	if _, ok := DetectLag(now, ExeID{OK: false, MtimeNano: now.UnixNano()}); ok {
		t.Fatal("관측 안 된 ExeID 에서 값을 냈다")
	}
	// mtime 이 미래다(시계 어긋남·NFS).
	if _, ok := DetectLag(now, ExeID{OK: true, MtimeNano: now.Add(time.Minute).UnixNano()}); ok {
		t.Fatal("미래 mtime 을 0 으로 접었다")
	}
	got, ok := DetectLag(now, ExeID{OK: true, MtimeNano: now.Add(-3 * time.Second).UnixNano()})
	if !ok || got != 3*time.Second {
		t.Fatalf("정상 경우가 (%s, %v) 다", got, ok)
	}
}
