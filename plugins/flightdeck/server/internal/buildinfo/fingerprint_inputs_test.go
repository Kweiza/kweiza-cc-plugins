package buildinfo

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// 지문을 만드는 두 자리는 같은 파일 집합을 해시해야 한다
// ─────────────────────────────────────────────────────────────────────────────
//
// `Resolve` 가 go 스탬프보다 우선하는 `injectedFingerprint` 는 **두 곳에서** 만들어진다:
//
//   - `plugins/flightdeck/bin/fd`      — 사용자 전원의 클라이언트(훅·MCP)
//   - `plugins/flightdeck/server/Dockerfile` — 공유 서버
//
// 이 축이 성립하는 근거는 단 하나, **두 자리가 해시하는 범위가 정확히 같다**는 것이다
// (양쪽 다 `server/` 트리를 상대경로로 훑는다). 그래서 sha 없이도 "이 클라이언트와 이
// 서버가 같은 소스인가"에 답할 수 있다 — DESIGN §13 ④ 가 그 실측을 적는다.
//
// ★ **그 전제가 깨지면 아무것도 안 보인다.** 한쪽 목록에만 `*.json` 이나 `*.tmpl` 이
// 추가되는 것을 상상해 보면 된다: 빌드는 양쪽 다 성공하고, 지문은 양쪽 다 나오고,
// **같은 소스인데 값이 영영 달라진다.** 그러면 배너가 항상 뜨고, 항상 뜨는 경고는
// 배경이 되어 안 읽힌다 — 이 축이 고치려던 실패(무의미한 신호)로 정확히 되돌아간다.
// 게다가 이번에는 **틀렸다는 사실조차 안 보인다**: 지문은 원래 사람이 읽고 판단할 수
// 있는 값이 아니다.
//
// 종전 방어는 양쪽 주석에 "같아야 한다"고 적어 둔 것이 전부였다. 이 저장소는 그런
// 자리를 주석이 아니라 시험으로 막는다 — 선례: `store/signal_is_not_history_test.go` ·
// `store/item_body_immutable_test.go` · `store/schema_table_count_test.go`.
//
// 비교 축이 **집합**인 이유: 양쪽 다 `-o`(OR)로 이어진 술어이고 결과를 `LC_ALL=C sort`
// 로 정렬해 해시하므로, 순서가 달라도 같은 파일 집합이면 같은 지문이 나온다.
// 단정은 주장과 같은 좌표여야 한다 — 주장은 "같은 집합을 해시한다"이지 "같은 순서로
// 적혀 있다"가 아니다.

// fpSrcInputsRe 는 런처의 배열 정의를 집는다: `src_inputs=( … )`.
var fpSrcInputsRe = regexp.MustCompile(`(?s)src_inputs=\(([^)]*)\)`)

// fpDockerFindRe 는 Dockerfile 의 지문 계산 술어를 집는다: `find . -type f \( … \)`.
//
// 셸 이스케이프가 그대로 파일에 있으므로 `\(` 를 리터럴로 찾는다(정규식 `\\\(`).
// 비탐욕이라 첫 `\)` 에서 끊긴다.
var fpDockerFindRe = regexp.MustCompile(`(?s)find\s+\.\s+-type\s+f\s+\\\((.*?)\\\)`)

// fpLauncherFindRe 는 런처의 **지문 계산** find 를 집는다(재빌드 판정 쪽이 아니다).
// 그쪽은 `-newer` 로 끝나고 `-type f` 가 없다 — 두 질문이 다르기 때문이다.
var fpLauncherFindRe = regexp.MustCompile(`(?s)find\s+\.\s+-type\s+f\s+\\\(\s*"\$\{src_inputs\[@\]\}"\s*\\\)`)

// fpNameRe 는 `-name '<패턴>'` 에서 패턴만 뽑는다.
var fpNameRe = regexp.MustCompile(`-name\s+'([^']*)'`)

// fpRepoRoot 는 이 시험이 읽는 레포 루트다(buildinfo 에서 다섯 단계 위).
//
// 좌표가 밀리면 이 시험은 아무것도 안 보면서 초록이 된다. 못박아 둔다.
func fpRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("레포 루트를 못 찾았다: %v", err)
	}
	for _, must := range []string{
		"plugins/flightdeck/bin/fd",
		"plugins/flightdeck/server/Dockerfile",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(must))); err != nil {
			t.Fatalf("레포 루트(%s)에 %s 가 없다 — 이 시험의 좌표가 틀렸다: %v", root, must, err)
		}
	}
	return root
}

// fpRead 는 레포 상대경로 파일을 읽는다.
func fpRead(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s 를 못 읽었다 — 이 시험의 좌표가 틀렸다: %v", rel, err)
	}
	return string(b)
}

// fpNamePatterns 는 술어 조각에서 `-name` 패턴 집합을 뽑아 정렬해 낸다.
func fpNamePatterns(clause string) []string {
	var out []string
	for _, m := range fpNameRe.FindAllStringSubmatch(clause, -1) {
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

// fpCapture 는 정규식 첫 그룹을 뽑는다. 못 찾으면 빈 문자열이다.
func fpCapture(re *regexp.Regexp, src string) string {
	m := re.FindStringSubmatch(src)
	if m == nil {
		return ""
	}
	return m[1]
}

// TestFingerprintInputsAgreeAcrossBuildSites 는 런처와 Dockerfile 이 **같은 파일 집합**을
// 해시하는지 본다. 이 시험이 빨개지면 두 빌드가 같은 소스에서도 다른 지문을 낸다.
func TestFingerprintInputsAgreeAcrossBuildSites(t *testing.T) {
	root := fpRepoRoot(t)

	launcher := fpRead(t, root, "plugins/flightdeck/bin/fd")
	docker := fpRead(t, root, "plugins/flightdeck/server/Dockerfile")

	launcherClause := fpCapture(fpSrcInputsRe, launcher)
	dockerClause := fpCapture(fpDockerFindRe, docker)

	// 눈을 뜨고 있는지 본다. 한쪽이라도 못 읽으면 "같다"가 아니라 "안 봤다"인데,
	// 둘은 화면에서 구분되지 않는다.
	if launcherClause == "" {
		t.Fatalf("bin/fd 에서 `src_inputs=( … )` 를 못 찾았다 — 그물이나 좌표가 밀렸다. " +
			"목록을 다른 모양으로 옮겼다면 이 시험의 정규식을 함께 고쳐라")
	}
	if dockerClause == "" {
		t.Fatalf("Dockerfile 에서 `find . -type f \\( … \\)` 를 못 찾았다 — 그물이나 좌표가 밀렸다. " +
			"지문 계산을 다른 모양으로 옮겼다면 이 시험의 정규식을 함께 고쳐라")
	}

	launcherNames := fpNamePatterns(launcherClause)
	dockerNames := fpNamePatterns(dockerClause)

	if len(launcherNames) == 0 || len(dockerNames) == 0 {
		t.Fatalf("`-name` 패턴을 한쪽에서 0개 뽑았다(런처 %d · Docker %d) — 그물이 죽었다",
			len(launcherNames), len(dockerNames))
	}

	if strings.Join(launcherNames, " ") == strings.Join(dockerNames, " ") {
		return
	}
	t.Errorf("지문 입력 목록이 두 빌드 자리에서 갈렸다:\n"+
		"  bin/fd     : %s\n"+
		"  Dockerfile : %s\n\n"+
		"이 둘이 같은 파일 집합을 해시한다는 것이 소스 지문 축의 **유일한 전제**다"+
		"(DESIGN §13 ④). 갈리면 같은 소스인데 지문이 영영 달라지고, 배너가 항상 뜬다 —"+
		" 항상 뜨는 경고는 배경이 되어 안 읽힌다.\n"+
		"한쪽에 확장자를 더했다면 **다른 쪽도 같이** 더해라.",
		strings.Join(launcherNames, " "), strings.Join(dockerNames, " "))
}

// TestFingerprintBothSitesHashFilesOnly 는 두 지문 명령이 **둘 다** `-type f` 로 거르는지
// 본다.
//
// 목록이 같아도 이 술어가 한쪽에만 있으면 해시 대상이 갈린다 — `find` 는 기본적으로
// 디렉토리도 낸다. 목록 일치만 보는 시험은 그 갈래에 대해 조용하다.
func TestFingerprintBothSitesHashFilesOnly(t *testing.T) {
	root := fpRepoRoot(t)

	if !fpLauncherFindRe.MatchString(fpRead(t, root, "plugins/flightdeck/bin/fd")) {
		t.Errorf("bin/fd 의 지문 계산이 `find . -type f \\( \"${src_inputs[@]}\" \\)` 모양이 아니다 — " +
			"`-type f` 가 빠지면 디렉토리가 해시 입력에 섞여 Dockerfile 쪽과 갈린다")
	}
	if !fpDockerFindRe.MatchString(fpRead(t, root, "plugins/flightdeck/server/Dockerfile")) {
		t.Errorf("Dockerfile 의 지문 계산이 `find . -type f \\( … \\)` 모양이 아니다 — " +
			"`-type f` 가 빠지면 디렉토리가 해시 입력에 섞여 런처 쪽과 갈린다")
	}
}

// TestLauncherKeepsTheInputListInOnePlace 는 런처가 목록을 **한 자리에만** 적는지 본다.
//
// bin/fd 안에서도 소비처가 둘이다 — 재빌드 판정(mtime)과 지문 계산. 그 둘이 배열을 안
// 쓰고 각자 패턴을 적기 시작하면, 이 파일 **안에서** 같은 표류가 재현된다. 그때는
// "안 바뀌었다"고 판정한 쪽과 "이 지문이다"라고 말한 쪽이 서로 다른 파일 집합을 본다.
// 배열 정의 자신의 주석이 그것을 못박고 있으니, 그 문장에 관문을 물린다.
func TestLauncherKeepsTheInputListInOnePlace(t *testing.T) {
	src := fpRead(t, fpRepoRoot(t), "plugins/flightdeck/bin/fd")

	if got := len(fpSrcInputsRe.FindAllString(src, -1)); got != 1 {
		t.Errorf("bin/fd 의 `src_inputs=( … )` 정의가 %d 개다 — 목록은 한 자리에만 산다", got)
	}
	// 정의 1 + 소비 2(재빌드 판정 · 지문) = 3. 소비처가 배열을 안 쓰고 패턴을 직접
	// 적으면 이 수가 준다.
	if got := strings.Count(src, "src_inputs"); got < 3 {
		t.Errorf("bin/fd 에서 `src_inputs` 참조가 %d 회뿐이다(정의 1 + 소비 2 이상이어야 한다) — "+
			"소비처 하나가 목록을 직접 적기 시작하면 이 파일 안에서 표류가 재현된다", got)
	}
}

// TestFingerprintInputParserActuallyCatches 는 그물이 무엇을 잡고 무엇을 통과시키는지
// 못박는다. 두 목록이 같으면 위 시험은 초록이라, 파서가 죽어도 그것만으로는 안 보인다.
func TestFingerprintInputParserActuallyCatches(t *testing.T) {
	const launcherLike = `src_inputs=( -name '*.go' -o -name 'go.mod' -o -name '*.sql' )`
	const dockerLike = `fp="$(find . -type f \( -name '*.sql' -o -name '*.go' -o -name 'go.mod' \) \`

	got := fpNamePatterns(fpCapture(fpSrcInputsRe, launcherLike))
	want := fpNamePatterns(fpCapture(fpDockerFindRe, dockerLike))
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("순서만 다른 같은 집합을 다르다고 봤다(런처 %v · Docker %v) — "+
			"두 자리는 결과를 정렬해 해시하므로 순서는 지문에 안 들어간다", got, want)
	}
	if len(got) != 3 {
		t.Errorf("런처 쪽에서 패턴 3개를 뽑아야 하는데 %d 개다: %v", len(got), got)
	}

	// 한쪽에만 확장자가 더해진 것 — 이 관문이 존재하는 이유 그 자체다.
	const dockerDrifted = `fp="$(find . -type f \( -name '*.go' -o -name 'go.mod' -o -name '*.sql' -o -name '*.json' \) \`
	if drifted := fpNamePatterns(fpCapture(fpDockerFindRe, dockerDrifted)); strings.Join(got, " ") == strings.Join(drifted, " ") {
		t.Errorf("한쪽에 '*.json' 이 더해졌는데 같다고 봤다 — 그물이 죽었다: %v", drifted)
	}

	// 못 찾는 경우가 빈 문자열로 와야 Fatal 갈래가 선다.
	if fpCapture(fpSrcInputsRe, "여기에는 배열이 없다") != "" {
		t.Error("없는 배열을 찾았다고 했다")
	}
	if fpCapture(fpDockerFindRe, "여기에는 find 가 없다") != "" {
		t.Error("없는 find 를 찾았다고 했다")
	}
}
