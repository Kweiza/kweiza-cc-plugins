package judge

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// comparablePath 의 **존치 근거가 다른 패키지의 시험 둘에 기대고 있다.** 이 관문은 그
// 기댐이 이름으로 적혀 있고 그 이름이 실재하는지, 그리고 저쪽이 이쪽을 되가리키는지만 본다.
//
// 왜 필요한가. 이 가드가 서 있는 이유(comparablePath 주석 ②)는 **두 사실의 곱**이다:
//
//	ⓐ service.RelPathWithin 의 fail-open 이 살아 있다 — 그것이 절대경로가 들어올 문이다
//	ⓑ service.JudgeOpenSession 이 그 문을 닫고 있다   — 그래서 지금은 안 들어온다
//
// 둘 다 이미 시험이 잠그고 있다(2026-08-11 실측: ⓐ 를 없애는 변이를 넣으면
// internal/service 가 빨개진다). **그런데 그 시험들은 자기가 이 가드를 지탱하는지 몰랐다** —
// ⓐ 를 잠그는 케이스의 사유는 "못 읽음을 '밖'으로 세면 발자국이 사라진다"로 이 가드와
// 무관하고, ⓑ 를 잠그는 시험은 자기가 무엇의 근거인지 안 밝혔다. 그래서 그 시험을 완화하는
// 사람은 이 가드의 근거가 함께 사라지는 것을 모른다 — 가드는 그대로 서 있고 근거만 조용히
// 없어진다. 이 저장소가 반복해서 데이는 모양이고, 이 자리는 그 사유가 **이미 한 번 거짓으로
// 판명된** 자리다(앞 판이 legacy.PlanImport 를 근거로 들었고 리뷰가 반증했다).
//
// ★ 이 관문이 **일부러 안 하는 것**: ⓐⓑ 가 참인지 자체는 안 본다. 그건 저쪽 시험의 일이고
// 여기서 다시 하면 같은 단정이 두 벌이 된다(그 중복이 갈리는 날 어느 쪽이 정본인지 아무도
// 모른다). 이 관문이 보는 것은 **인용이 썩지 않았는가** 하나다 — 이름이 바뀌거나 시험이
// 지워지거나 저쪽 주석이 사라지면 여기가 빨개진다.
func TestComparablePathCitesTheTestsThatKeepItsGroundTrue(t *testing.T) {
	src := readSourceFile(t, filepath.Join("prescribe.go"))

	block := commentBlockBefore(t, src, "func comparablePath(")

	// 인용된 시험 이름을 뽑는다. 중복은 접는다 — 같은 이름을 두 번 적어도 인용은 하나다.
	//
	// ★ **이 패키지에 실재하는 이름은 뺀다.** 주석이 이 관문 자신을 언급하는 것은 정당하고
	// 유용한데(다음 사람이 관문의 존재를 안다), 그 이름을 internal/service 에서 찾으면
	// 당연히 없다. 자기 이름을 문자열로 박아 빼면 이름이 바뀌는 날 그 예외가 조용히
	// 낡으므로, **패키지 소속으로** 가른다.
	own := readOwnTests(t)
	seen := map[string]bool{}
	var names []string
	for _, n := range regexp.MustCompile(`Test[A-Za-z0-9_]+`).FindAllString(block, -1) {
		if seen[n] || strings.Contains(own, "func "+n+"(") {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	if len(names) < 2 {
		t.Fatalf("comparablePath 주석이 인용하는 시험이 %d개다(%v) — 존치 근거는 두 사실의 곱이고 "+
			"각각 **다른** 시험이 잠근다(fail-open 이 살아 있음 · 그 문이 닫혀 있음). "+
			"인용이 사라졌거나 애초에 안 적혔다", len(names), names)
	}

	// internal/service 의 시험 전량을 한 덩이로 읽는다. 어느 파일에 있는지는 이 관문의
	// 관심이 아니다 — 파일이 갈리거나 합쳐지는 것은 정당한 정리이고, 그때마다 이 관문이
	// 빨개지면 관문이 정리를 벌하게 된다.
	svc := readServiceTests(t)

	for _, n := range names {
		if !strings.Contains(svc, "func "+n+"(") {
			t.Errorf("comparablePath 주석이 인용한 %s 가 internal/service 의 시험에 없다 — "+
				"**인용이 썩었다.** 이름이 바뀌었으면 주석을 따라 고치고, 시험이 지워졌으면 "+
				"이 가드의 존치 근거가 사라진 것이므로 가드를 재판정해라", n)
			continue
		}
		// ★ 양방향이어야 한다. 이쪽만 가리키면 저쪽을 고치는 사람은 여전히 아무것도 모른다 —
		// 이 항목이 고치려던 상태가 정확히 그것이다.
		region := testRegion(t, svc, n)
		if !strings.Contains(region, "comparablePath") {
			t.Errorf("%s 가 comparablePath 를 되가리키지 않는다 — 그 시험을 완화하는 사람은 "+
				"자기가 무엇의 근거를 무너뜨리는지 모른다. 그 시험(또는 그 케이스)의 사유에 "+
				"judge.comparablePath 를 적어라", n)
		}
	}
}

// readSourceFile 은 이 패키지의 소스를 읽는다. 못 읽으면 죽는다 — 이 저장소의 관문은
// 전부 "무출력이면 통과" 형태라, 좌표가 틀려 아무것도 안 읽은 것과 통과가 같아 보이면
// 안 된다(DESIGN 인용 관문들이 같은 이유로 t.Fatalf 를 쓴다).
func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("%s 를 못 읽었다 — 이 시험의 좌표가 틀렸다: %v", name, err)
	}
	if len(b) == 0 {
		t.Fatalf("%s 가 비었다 — 빈 haystack 은 모든 단정을 통과시킨다", name)
	}
	return string(b)
}

// readOwnTests 는 **이 패키지**의 시험 파일 전량을 이어 붙인다. 인용에서 자기 패키지
// 시험을 가려내는 데만 쓴다.
func readOwnTests(t *testing.T) string {
	t.Helper()
	return concatTests(t, ".")
}

// readServiceTests 는 internal/service 의 시험 파일 전량을 이어 붙인다.
//
// import 가 아니라 **파일 읽기**다. judge 는 service 를 임포트할 수 없다(service 가 judge 를
// 쓴다 — 순환이 된다). 그래서 이 관문은 텍스트 층에서 본다. 이 저장소에 같은 형식의
// 선례가 있다(pick_axis_wiring_test.go 가 pick.go 를 텍스트로 훑는다).
func readServiceTests(t *testing.T) string {
	t.Helper()
	return concatTests(t, filepath.Join("..", "service"))
}

// concatTests 는 한 디렉토리의 *_test.go 를 전부 이어 붙인다. 파일이 하나도 없으면
// 죽는다 — 빈 haystack 은 이 관문의 모든 Contains 단정을 뒤집는다.
func concatTests(t *testing.T, dir string) string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("%s 를 못 읽었다 — 이 시험의 좌표가 틀렸다: %v", dir, err)
	}
	var sb strings.Builder
	n := 0
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("%s 를 못 읽었다: %v", e.Name(), err)
		}
		sb.Write(b)
		sb.WriteString("\n")
		n++
	}
	if n == 0 {
		t.Fatalf("%s 에 시험 파일이 없다 — 좌표가 틀렸다(빈 haystack)", dir)
	}
	return sb.String()
}

// commentBlockBefore 는 decl 바로 앞에 붙은 // 주석 덩이를 낸다.
//
// 빈 문자열을 돌려주지 않는다 — 빈 조각은 Contains 검사를 전부 실패시키고 len 검사를
// 오해하게 만든다. 못 찾으면 죽는다.
func commentBlockBefore(t *testing.T, src, decl string) string {
	t.Helper()
	i := strings.Index(src, decl)
	if i < 0 {
		t.Fatalf("%q 를 소스에서 못 찾았다 — 이름이 바뀌었으면 이 관문의 좌표를 따라 고쳐라", decl)
	}
	lines := strings.Split(src[:i], "\n")
	var block []string
	for j := len(lines) - 2; j >= 0; j-- { // -2: decl 바로 앞 줄부터
		s := strings.TrimSpace(lines[j])
		if !strings.HasPrefix(s, "//") {
			break
		}
		block = append([]string{s}, block...)
	}
	if len(block) == 0 {
		t.Fatalf("%q 앞에 주석 덩이가 없다 — 존치 근거가 적히는 자리 자체가 사라졌다", decl)
	}
	return strings.Join(block, "\n")
}

// testRegion 은 시험 함수 하나의 **선행 주석 + 본문**을 낸다. 이 관문이 "저쪽이 이쪽을
// 되가리키는가"를 볼 때의 haystack 이다.
//
// 파일 전체를 haystack 으로 쓰면 같은 파일의 **다른** 시험이 적은 낱말이 이 단정을
// 만족시킨다 — 이 저장소가 화면 단정에서 실측한 거짓 초록의 모양이 정확히 그것이다.
// 못 찾거나 빈 구간이면 죽는다(빈 조각이 통과시키는 방향의 실패를 막는다).
func testRegion(t *testing.T, all, name string) string {
	t.Helper()
	i := strings.Index(all, "func "+name+"(")
	if i < 0 {
		t.Fatalf("%s 의 구간을 못 잘랐다 — 호출부가 실재를 먼저 확인해야 한다", name)
	}
	from := precededByComments(all, lineStartAt(all, i))
	end := funcEnd(all, i)
	if end <= from {
		t.Fatalf("%s 의 구간 경계가 뒤집혔다(from=%d end=%d) — 이 헬퍼가 헛돌고 있다", name, from, end)
	}
	region := all[from:end]
	if strings.TrimSpace(region) == "" {
		t.Fatalf("%s 의 구간이 비었다 — 빈 조각은 모든 Contains 를 실패시켜 판별력이 없다", name)
	}
	if !strings.Contains(region, "func "+name+"(") {
		t.Fatalf("%s 의 구간에 그 함수 자신이 없다 — 오프셋 계산이 틀렸다", name)
	}
	return region
}

// lineStartAt 은 off 가 속한 줄의 시작 오프셋이다.
func lineStartAt(s string, off int) int {
	return strings.LastIndexByte(s[:off], '\n') + 1
}

// precededByComments 는 start 앞에 붙은 연속 // 주석 줄까지 시작을 끌어올린다.
func precededByComments(s string, start int) int {
	for start > 0 {
		prevEnd := start - 1 // 앞 줄의 '\n'
		prevStart := lineStartAt(s, prevEnd)
		if !strings.HasPrefix(strings.TrimSpace(s[prevStart:prevEnd]), "//") {
			break
		}
		start = prevStart
	}
	return start
}

// funcEnd 는 최상위 함수 하나의 **닫는 중괄호 직후**다. gofmt 가 최상위 선언의 닫는
// 중괄호를 열 0 에 두는 것에 기댄다(이 저장소에는 gofmt 관문이 있어 그 전제가 지켜진다.
// 같은 기댐을 pick_axis_wiring_test.go 의 지배 판정이 이미 쓴다).
//
// ★ **경계를 `\nfunc` 으로 잡으면 거짓 초록이 난다** — 실측으로 두 번 확인했다(2026-08-11).
// 첫 판은 다음 선언까지 잘랐고, 그러면 두 함수 **사이**에 있는 주석이 앞 함수의 구간에
// 들어온다. 다음 함수의 doc 주석은 precededByComments 로 뺄 수 있지만, **빈 줄로 분리된
// 떠 있는 주석은 Go 규칙상 다음 함수의 doc 이 아니라서** 그 방법으로 안 빠진다. 그 상태에서
// 관문이 통과했다 — 대상 함수에서 낱말을 지우고 그 떠 있는 자리에 심었더니 초록이었다.
// 닫는 중괄호로 끊으면 두 경우 다 구간 밖이 된다.
func funcEnd(s string, i int) int {
	k := strings.Index(s[i:], "\n}")
	if k < 0 {
		return len(s)
	}
	return i + k + 2
}
