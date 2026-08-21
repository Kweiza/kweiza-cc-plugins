package judge

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// 정렬 축을 더하면 문서가 먼저 빨개진다
// ─────────────────────────────────────────────────────────────────────────────
//
// DESIGN §3 의 정렬 키 문단은 **두 번 낡았다.** 기아(StarvationAge)가 상수를 0에서
// 1로 만들었고, 종료 선언이 키를 하나 더 더했다. 두 번 다 시험이 안 빨개졌다 —
// 그 줄에 수를 세는 관문이 하나도 안 물려 있었다. 스킬 수(cmd/fd/plugin_test.go)는
// 같은 부류의 실패를 겪고 이미 정규식 관문을 얻었는데, 이쪽은 못 얻었다.
//
// 관문의 방향은 **코드 → 문서**다. 코드에 축의 근거(상수 선언·구조체 필드)가 있으면
// 문서의 그 문단이 그 축을 이름으로 불러야 한다. 반대 방향(문서에 있으면 코드에도
// 있어야 한다)은 **일부러 안 건다** — DESIGN 은 "여기 있는 것은 이대로 만든다"라서
// 구현보다 앞설 수 있는 문서이고(§0 머리말), 그 방향까지 걸면 설계 커밋이 못 선다.

// designDoc 은 설계 정본이다.
// internal/judge → internal → server → plugins/flightdeck 세 단계 위다.
func designDoc(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "..", "..", "DESIGN.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("DESIGN.md 를 못 읽었다(%s) — 이 시험의 좌표가 틀렸다: %v", p, err)
	}
	return string(b)
}

// judgeSource 는 이 패키지의 소스 한 벌을 텍스트로 읽는다. 시험은 패키지
// 디렉토리에서 도므로 파일 이름만 준다.
func judgeSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("%s 를 못 읽었다: %v", name, err)
	}
	return string(b)
}

// sortKeyParagraph 는 DESIGN §3 의 "정렬 키는 …" 문단을 낸다(빈 줄 앞까지).
// 문서 전체를 보면 다른 절의 낱말이 이 단정을 통과시킨다.
func sortKeyParagraph(t *testing.T, design string) string {
	t.Helper()
	i := strings.Index(design, "정렬 키는")
	if i < 0 {
		t.Fatalf("DESIGN.md 에서 정렬 키 문단을 못 찾았다 — 이 시험의 정본이 사라졌다")
	}
	rest := design[i:]
	if j := strings.Index(rest, "\n\n"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// zeroTuningClaimRe 는 조정 상수가 **하나도** 없다고 무조건 주장하는 문장이다.
// 한정한 형태("이 함수 안에는" · "하나뿐이다")는 안 잡는다 — 그것이 지금의 사실이다.
var zeroTuningClaimRe = regexp.MustCompile(`조정할 상수가 0개|조정할 상수가 하나도 없다`)

func TestNoOneClaimsTheSortHasZeroTuningConstants(t *testing.T) {
	bundleSrc := judgeSource(t, "bundle.go")
	// 전제를 먼저 밟는다. 상수가 사라지면 이 관문은 판정할 것이 없다.
	if !strings.Contains(bundleSrc, "const StarvationAge") {
		t.Skip("StarvationAge 가 사라졌다 — 이 관문의 전제가 없어졌으므로 판정하지 않는다")
	}

	targets := []struct{ name, body string }{
		{"DESIGN.md", designDoc(t)},
		{"judge/bundle.go", bundleSrc},
		{"judge/eligible.go", judgeSource(t, "eligible.go")},
	}
	for _, tgt := range targets {
		for i, ln := range strings.Split(tgt.body, "\n") {
			if !zeroTuningClaimRe.MatchString(ln) {
				continue
			}
			t.Errorf("%s:%d 가 조정 상수가 하나도 없다고 주장한다 — StarvationAge(24h)는 "+
				"실측으로 선 조정 상수다:\n  %s", tgt.name, i+1, strings.TrimSpace(ln))
		}
	}
}

func TestDesignSortKeyParagraphNamesEveryLiveAxis(t *testing.T) {
	para := sortKeyParagraph(t, designDoc(t))
	bundleSrc := judgeSource(t, "bundle.go")

	// ★ 이 표는 **손으로 유지한다** — 축을 더하고 여기 안 넣으면 이 시험은 안 빨개진다.
	// 그것이 이 파일이 고발한 실패 모양 그대로다(위 머리말). 축을 더할 때 여기 한 줄을
	// 같이 넣어라. 전수 자동화(구조체 필드 파싱)를 안 하는 이유는 Bundle 의 필드가 전부
	// 정렬 축은 아니기 때문이다(Reason·Links·Members 는 축이 아니다) — 그 판정은 사람 몫이다.
	axes := []struct{ evidence, name string }{
		{"const StarvationAge", "기아"},
		{"CloseDeclared ", "종료 선언"},
		{"AfterCleared ", "선행"},
	}
	for _, a := range axes {
		if !strings.Contains(bundleSrc, a.evidence) {
			continue // 그 축이 아직 코드에 없다 — 문서를 강요하지 않는다
		}
		if strings.Contains(para, a.name) {
			continue
		}
		t.Errorf("bundle.go 에 %q 가 있는데 DESIGN 의 정렬 키 문단이 %q 를 안 부른다:\n%s",
			a.evidence, a.name, para)
	}
}
