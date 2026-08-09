package judge

import (
	"regexp"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// 근거 있는 상수는 **날짜 있는** 실측을 달고 있어야 한다
// ─────────────────────────────────────────────────────────────────────────────
//
// StarvationAge 는 "근거 없는 상수를 없앤다"의 산물이다. 그런데 근거로 적힌 리드타임
// 분포에 **날짜가 없었다.** 그래서 나흘 만에 p90 이 16.3h → 33.8h 로 올라 결론
// ("24h 는 p90 바깥이라 정상 작업이 안 걸린다")이 거짓이 됐는데도 아무도 못 봤다 —
// 그 문장 옆에서 열린 30건 중 26건이 굶은 상태가 유지됐다(2026-08-09 실측).
//
// 이 관문이 지키는 것은 값이 아니라 **실측의 유효기간 표기**다. 값을 바꿀 때도,
// 안 바꿀 때도, 언제 잰 것인지가 주석에 있어야 다음 사람이 다시 잴지를 판단한다.
//
// ★ 잔량(열린 큐의 나이)을 함께 요구하는 이유. 리드타임만 보면 임계를 p90 에
// 자동 추종시키게 되는데, 그러면 큐가 나빠질수록 경고가 사라진다 — 설계 §4 가
// 고발한 상시 점등의 거울상(상시 소등)이다.

var isoDateRe = regexp.MustCompile(`20\d{2}-\d{2}-\d{2}`)

// starvationDoc 은 StarvationAge 선언 바로 위의 주석 블록이다.
func starvationDoc(t *testing.T) string {
	t.Helper()
	src := judgeSource(t, "bundle.go")
	i := strings.Index(src, "// StarvationAge 는")
	if i < 0 {
		t.Fatalf("StarvationAge 의 주석 블록을 못 찾았다 — 이 시험의 좌표가 틀렸다")
	}
	j := strings.Index(src[i:], "\nconst StarvationAge")
	if j < 0 {
		t.Fatalf("StarvationAge 선언을 못 찾았다 — 이 시험의 좌표가 틀렸다")
	}
	return src[i : i+j]
}

func TestStarvationRationaleCarriesADatedMeasurement(t *testing.T) {
	doc := starvationDoc(t)

	if dates := isoDateRe.FindAllString(doc, -1); len(dates) < 2 {
		t.Errorf("StarvationAge 의 근거에 날짜가 %d개다 — 최초 측정과 재측 **둘**을 나란히 적어라. "+
			"날짜 없는 실측은 언제 거짓이 됐는지 아무도 못 본다.\n%s", len(dates), doc)
	}
	if strings.Contains(doc, "정상 작업이 안 걸린다") {
		t.Errorf("StarvationAge 가 아직 '정상 작업이 안 걸린다'고 말한다 — 2026-08-09 실측으로 "+
			"리드타임 p90 이 33.8h 이고 열린 30건 중 26건이 24h 를 넘겼다.\n%s", doc)
	}
	if !strings.Contains(doc, "열린") {
		t.Errorf("StarvationAge 의 근거가 리드타임만 말하고 잔량(열린 큐의 나이)을 안 말한다 — "+
			"리드타임만으로 이 값을 다시 정하면 큐가 나빠질수록 경고가 사라진다.\n%s", doc)
	}
	if StarvationAge.Hours() != 24 {
		t.Errorf("StarvationAge 가 %v 다 — 값을 바꿨으면 위 실측 줄에 그 판정을 함께 적어라", StarvationAge)
	}
}
