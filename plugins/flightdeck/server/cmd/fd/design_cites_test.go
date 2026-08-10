package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 코드가 인용하는 DESIGN 절이 **실재하는가.**
//
// ★★ 왜 이 관문이 있나. 이 저장소는 주석이 **근거를 대는** 방식으로 서 있고, 그 근거의
// 상당수가 DESIGN 절 번호다(실측 2026-08-11: `plugins/flightdeck/server/` 의 Go 파일에서
// **369건**). 그런데 그 인용이 유효한지 아무도 안 셌다. 빈 인용은 낡은 문서보다 나쁘다 —
// 낡은 것은 읽으면 틀렸다는 것을 알 수 있지만, **빈 인용은 독자가 자기가 못 찾은 것으로 읽는다.**
//
// ★ **이 시험이 무엇을 못 잡는지 먼저 적는다.** 이것은 **존재만** 잰다. "그 절이 정말
// 그 말을 하는가"는 기계가 못 한다 — 그리고 실제 사고는 그쪽에서 났다(2026-08-10:
// `outbox.go`·`cmds.go` 가 NFS 사각을 "설계 §13" 으로 가리켰는데 §13 에 그 축이 없었다.
// §13 은 존재했으므로 이 시험은 그것을 **원리적으로 못 잡는다**).
//
// ★ 그래도 두는 이유는 **한 축이 실제로 걸렸기 때문**이다. 표본 검사(2026-08-11)에서
// 절 번호 인용은 369건 중 무효가 **0건**이었지만, 하위번호(`§N.M`) 인용은 **4건 중 4건이
// 무효**였다 — DESIGN 에는 하위번호 구조가 아예 없는데 `judge/bundle.go` 계열이 `§0.1`·
// `§0.2` 를 근거로 달고 있었다. 그 넷은 이 커밋에서 고쳤고, 이 시험이 다시 생기는 것을 막는다.
//
// ★ 내용 축의 표본 결과도 함께 남긴다(기계가 못 재므로 사람이 다시 재야 하는 수치다):
// 절별 2건씩 12건을 손으로 대조해 **5건이 어긋났다(42%)** — 하위번호 둘 · §3 이 "이름
// 붙였다"는 문구가 §0 에 있던 것 · §4 로 인용된 파생 정의가 §5 에 있던 것 · §2 의
// "둘째 층" 규칙이 **DESIGN 어디에도 없던 것**. 마지막 것은 그 규칙을 §4 에 적어서 닫았다.
func TestDesignSectionCitationsResolve(t *testing.T) {
	root := pluginRoot(t)
	design, err := os.ReadFile(filepath.Join(root, "DESIGN.md"))
	if err != nil {
		t.Fatalf("DESIGN.md 를 못 읽었다: %v", err)
	}

	// ── 실재하는 절 번호 ──
	haveTop := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^## (\d+)\.`).FindAllStringSubmatch(string(design), -1) {
		haveTop[m[1]] = true
	}
	if len(haveTop) == 0 {
		t.Fatal("DESIGN.md 에서 절을 하나도 못 찾았다 — 이 시험의 전제가 깨졌다")
	}
	// ── 실재하는 하위번호(`### 7.2` 나 본문의 `§7.2` 형태) ──
	haveSub := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^#{3,6} (\d+\.\d+)`).FindAllStringSubmatch(string(design), -1) {
		haveSub[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`§(\d+\.\d+)`).FindAllStringSubmatch(string(design), -1) {
		haveSub[m[1]] = true
	}

	cite := regexp.MustCompile(`(?:설계|DESIGN) §(\d+(?:\.\d+)?)`)
	var dangling []string
	total := 0
	err = filepath.WalkDir(filepath.Join(root, "server"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, p)
		for i, line := range strings.Split(string(b), "\n") {
			for _, m := range cite.FindAllStringSubmatch(line, -1) {
				total++
				n := m[1]
				ok := haveTop[n]
				if strings.Contains(n, ".") {
					// ★ 하위번호는 **하위번호로만** 만족된다. 상위 절이 있다고 통과시키면
					//   `§0.1` 이 `§0` 으로 통과해 이 시험이 아무것도 안 잡는다 —
					//   그것이 정확히 이 관문을 만든 사고다.
					ok = haveSub[n]
				}
				if !ok {
					dangling = append(dangling, fmt.Sprintf("%s:%d  §%s  %s",
						rel, i+1, n, strings.TrimSpace(clip(line, 100))))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("소스를 훑다 걸렸다: %v", err)
	}
	if total == 0 {
		t.Fatal("DESIGN 인용을 하나도 못 찾았다 — 정규식이 죽었으면 이 시험은 항상 초록이다")
	}
	if len(dangling) > 0 {
		sort.Strings(dangling)
		t.Errorf("DESIGN 에 없는 절을 가리키는 인용 %d건(전체 %d건):\n  %s\n\n"+
			"빈 인용은 낡은 문서보다 나쁘다 — 독자는 자기가 못 찾은 것으로 읽는다. "+
			"절을 옮겼으면 인용을 옮기고, 그 말이 DESIGN 에 없으면 **적거나 인용을 빼라**.",
			len(dangling), total, strings.Join(dangling, "\n  "))
	}
	t.Logf("DESIGN 인용 %d건 · 무효 0건 (절 %d개 · 하위번호 %d개)", total, len(haveTop), len(haveSub))
}
