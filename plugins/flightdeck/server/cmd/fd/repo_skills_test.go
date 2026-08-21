package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// 스킬 관문을 **저장소 전체**로 넓힌다 — 옆 플러그인의 스킬은 아무도 안 보고 있었다.
//
// `plugin_test.go` 의 관문은 넓지만 그 `pluginRoot` 는 `plugins/flightdeck` 이다.
// `plugins/session-handoff/skills/` 는 그 시야 밖이고 그 플러그인 안에는 시험 파일이
// 없다. 그래서 이것들이 전부 조용히 지나갔다 — frontmatter 가 깨져 스킬이 목록에서
// 통째로 사라져도, `description` 이 비어도, 한국어 트리거가 옆 플러그인과 겹쳐도.
//
// ★ 이 항목의 뿌리는 `session-handoff-skill-has-no-korean-triggers` 다 — 스킬 둘에
// 한국어 트리거가 **하나도 없는 채로 5일**을 갔고, 그동안 한국어 발화에 아무것도 안
// 걸렸다. 기능이 죽은 것과 같았는데 붉힐 관문이 없었다. 그래서 이 파일이 무는 것 중
// 첫째가 **"스킬마다 한국어 트리거가 최소 하나"** 다. 나머지 둘(적재 가능성·겹침)은
// 그때 손으로 잰 것을 굳힌 것이다.
//
// ★ **줄 수 상한은 여기로 안 가져왔다.** `skillLineCaps` 의 근거는 "부른 턴의 예산 ×
// 호출 빈도"이고(plugin_test.go 의 주석), 그 빈도는 flightdeck 스킬의 것이다.
// `session-handoff` 는 세션당 많아야 한 번 부르고 그 산문이 인수인계 문서의 형식을
// 나르므로 같은 회귀선이 안 선다. 빼먹은 것이 아니라 **근거가 안 따라오는 것**이다 —
// 밖에도 상한을 걸려면 그 근거부터 새로 세워라.

// repoRootFromCmdFd 는 이 시험이 훑는 레포 루트다(`cmd/fd` 에서 다섯 단계 위).
//
// 좌표가 밀리면 이 시험은 아무것도 안 보면서 초록이 된다. 못박아 둔다
// (`internal/store/signal_is_not_history_test.go` 와 같은 규율).
func repoRootFromCmdFd(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("레포 루트를 못 찾았다: %v", err)
	}
	for _, must := range []string{
		".claude-plugin/marketplace.json",
		"plugins/flightdeck/server/go.mod",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(must))); err != nil {
			t.Fatalf("레포 루트(%s)에 %s 가 없다 — 이 시험의 좌표가 틀렸다: %v", root, must, err)
		}
	}
	return root
}

// repoSkillCounts 는 **플러그인별 스킬 수**다. 표와 디스크를 양방향으로 문다.
//
// ★ 표가 필요한 이유: 훑기가 glob 뿐이면 "아무것도 못 찾았다"와 "전부 봤다"가 화면에서
// 같다. 플러그인이 하나 더 생기거나 스킬 디렉토리가 사라지면 이 표가 먼저 빨개져서,
// **검사받지 않은 채 들어오는 길**이 없다 — `skillLineCaps` 가 flightdeck 안에서 하는
// 일을 플러그인 축으로 옮긴 것이다.
//
// 0 도 적는다. `grafik-bar` 는 훅과 스크립트만 있는 플러그인이고, "스킬이 없다"는 사실
// 자체가 표에 있어야 스킬이 생긴 날 이 관문이 답한다.
var repoSkillCounts = map[string]int{
	"flightdeck":      4,
	"grafik-bar":      0,
	"session-handoff": 2,
}

// skillDescRuneCap 은 `description` 의 상한이다.
//
// ★ **이 수는 이 레포가 물려받은 미측정값이다** — DESIGN §13 「아직 아님」 6 이 "항목당
// 1,536자 절단"에 측정 기록이 없다고 적어 뒀다. 그래서 이것은 플랫폼 한계의 재현이
// 아니라 **폭주 방지선**이다: 지금 최대가 295자라 평상시엔 아무것도 안 문다. 여기에
// 걸리는 날 할 일은 상한을 올리는 것이 아니라 **§13 의 그 줄을 재는 것**이다.
//
// 자(rune)로 센다 — DESIGN 이 "1,536자"라 적었고, 그 수가 바이트라는 근거도 없기
// 때문이다. 실패 메시지에는 바이트 수도 함께 낸다(둘 중 무엇이 진짜인지 재는 사람이
// 두 수를 다 갖고 시작하도록).
const skillDescRuneCap = 1536

// skillTriggerRe 는 `description` 에서 `"…"` 인용구를 뽑는다.
var skillTriggerRe = regexp.MustCompile(`"([^"]*)"`)

// hangulRe 는 한글이 한 자라도 들었는지 본다(음절·자모·호환자모).
var hangulRe = regexp.MustCompile(`[\x{AC00}-\x{D7A3}\x{1100}-\x{11FF}\x{3130}-\x{318F}]`)

// skillRef 는 훑어 낸 스킬 하나다.
type skillRef struct{ plugin, dir, path string }

func (s skillRef) id() string { return s.plugin + "/" + s.dir }

// skillRefs 는 `plugins/*/skills/*/SKILL.md` 를 전수로 낸다.
//
// 표본을 자르지 않는다 — 이 레포에는 `head` 파이프가 목록을 말없이 잘라 "전수"가 거짓이
// 된 선례가 있다(`94fc82e`). 하나도 못 찾으면 초록으로 지나가지 않고 그 자리에서 죽는다.
func skillRefs(t *testing.T, root string) []skillRef {
	t.Helper()
	pents, err := os.ReadDir(filepath.Join(root, "plugins"))
	if err != nil {
		t.Fatalf("plugins/ 를 못 읽었다: %v", err)
	}
	var out []skillRef
	for _, p := range pents {
		if !p.IsDir() {
			continue
		}
		sdir := filepath.Join(root, "plugins", p.Name(), "skills")
		ents, err := os.ReadDir(sdir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("%s/skills 를 못 읽었다: %v", p.Name(), err)
		}
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			out = append(out, skillRef{p.Name(), e.Name(), filepath.Join(sdir, e.Name(), "SKILL.md")})
		}
	}
	if len(out) == 0 {
		t.Fatalf("스킬을 하나도 못 찾았다(레포 루트 %s) — 훑기가 눈이 먼 것이지 통과가 아니다", root)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id() < out[j].id() })
	return out
}

// skillFront 는 SKILL.md 의 frontmatter 를 키 → 값으로 읽는다.
//
// ★ 이것은 **YAML 파서가 아니다.** 이 모듈에 YAML 의존성이 없고, 관문 하나를 위해
// 의존성을 새로 들이는 것은 이 파일이 버는 것보다 비싸다. 대신 **YAML 이 확실히
// 거절하거나 값을 조용히 바꿔 버리는 모양**만 골라 막는다:
//
//	여닫는 `---` 부재 · 탭 · 한 겹이 아닌 줄 · `key: value` 아닌 줄 · 같은 키 두 번
//	(뒤가 앞을 덮는다) · 안 닫힌 인용부호 · 따옴표 없는 값 안의 `: ` 와 ` #`
//
// 그래서 **통과가 "YAML 로 파싱된다"를 증명하지는 않는다** — 이 관문이 무는 것은 그
// 부분집합이다. 전수 파싱을 물고 싶어지면 의존성 판정을 먼저 하고 이 주석을 고쳐라.
func skillFront(raw string) (map[string]string, error) {
	if !strings.HasPrefix(raw, "---\n") {
		return nil, fmt.Errorf("여는 `---` 줄이 없다 — frontmatter 가 없으면 스킬이 목록에 아예 안 뜬다")
	}
	rest := raw[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 && strings.HasSuffix(rest, "\n---") {
		end = len(rest) - len("\n---")
	}
	if end < 0 {
		return nil, fmt.Errorf("닫는 `---` 줄이 없다")
	}
	block := rest[:end]
	if strings.Contains(block, "\t") {
		return nil, fmt.Errorf("frontmatter 에 탭이 있다 — YAML 은 들여쓰기 자리의 탭을 거절한다")
	}
	front := map[string]string{}
	for i, line := range strings.Split(block, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line != strings.TrimLeft(line, " ") {
			return nil, fmt.Errorf("%d번째 줄이 들여쓰여 있다 — 이 관문은 한 겹 `key: value` 만 읽는다: %q", i+1, line)
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("%d번째 줄이 `key: value` 가 아니다: %q", i+1, line)
		}
		if !skillKeyRe.MatchString(k) {
			return nil, fmt.Errorf("%d번째 줄의 키 %q 가 키 모양이 아니다", i+1, k)
		}
		if v != "" && !strings.HasPrefix(v, " ") {
			return nil, fmt.Errorf("%d번째 줄의 `:` 뒤에 공백이 없다 — YAML 은 `key:value` 를 키의 일부로 읽는다: %q", i+1, line)
		}
		if _, dup := front[k]; dup {
			return nil, fmt.Errorf("frontmatter 에 %q 가 두 번 있다 — 뒤가 앞을 조용히 덮는다", k)
		}
		val := strings.TrimSpace(v)
		if err := skillScalarHazard(val); err != nil {
			return nil, fmt.Errorf("키 %q 의 값: %w", k, err)
		}
		front[k] = val
	}
	return front, nil
}

var skillKeyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// skillScalarHazard 는 스칼라 값 하나가 YAML 에서 깨지거나 잘리는 모양인지 답한다.
func skillScalarHazard(v string) error {
	if v == "" {
		return nil // 빈 값 자체는 여기서 안 막는다 — 필수 키의 공백은 부르는 쪽이 문다
	}
	if q := v[0]; q == '"' || q == '\'' {
		if len(v) < 2 || v[len(v)-1] != q {
			return fmt.Errorf("%q 로 열고 안 닫았다", string(q))
		}
		return nil
	}
	if strings.Contains(v, ": ") || strings.HasSuffix(v, ":") {
		return fmt.Errorf("따옴표 없는 값 안에 `: ` 가 있다 — YAML 이 그 자리를 매핑으로 읽어 파싱이 깨진다")
	}
	if strings.Contains(v, " #") {
		return fmt.Errorf("따옴표 없는 값 안에 ` #` 이 있다 — 그 뒤가 주석으로 잘려 값이 **조용히** 짧아진다")
	}
	if strings.HasPrefix(v, "- ") || strings.HasPrefix(v, "? ") {
		return fmt.Errorf("따옴표 없는 값이 %q 로 시작한다 — YAML 지시 문자다", v[:2])
	}
	if r, _ := utf8.DecodeRuneInString(v); strings.ContainsRune("#&*!|>%@`{[", r) {
		return fmt.Errorf("따옴표 없는 값이 %q 로 시작한다 — YAML 지시 문자다", string(r))
	}
	return nil
}

// skillUnquote 는 인용된 스칼라의 바깥 따옴표를 벗긴다.
//
// 벗기지 않으면 트리거 정규식이 값 전체를 인용구 하나로 읽어 **눈이 먼다** — 겹침 0건이
// 측정이 아니라 사고가 되는 경로다.
func skillUnquote(v string) string {
	if len(v) >= 2 {
		if q := v[0]; (q == '"' || q == '\'') && v[len(v)-1] == q {
			inner := v[1 : len(v)-1]
			if q == '"' {
				return strings.ReplaceAll(inner, `\"`, `"`)
			}
			return strings.ReplaceAll(inner, "''", "'")
		}
	}
	return v
}

// 훑기의 **범위**가 표와 맞는가 — 표에 없는 플러그인도, 플러그인 없는 표 항목도 실패다.
func TestEverySkillInTheRepoIsInScope(t *testing.T) {
	root := repoRootFromCmdFd(t)

	// flightdeck 의 수는 두 표에 있다. 서로를 물려 두 벌이 갈라지는 길을 막는다.
	if got := repoSkillCounts["flightdeck"]; got != len(skillLineCaps) {
		t.Fatalf("이 표는 flightdeck 을 %d개라 하는데 skillLineCaps 는 %d개다 —\n"+
			"같은 수를 두 곳에 적었으면 둘이 서로를 물어야 한다", got, len(skillLineCaps))
	}

	pents, err := os.ReadDir(filepath.Join(root, "plugins"))
	if err != nil {
		t.Fatalf("plugins/ 를 못 읽었다: %v", err)
	}
	onDisk := map[string]bool{}
	for _, p := range pents {
		if p.IsDir() {
			onDisk[p.Name()] = true
		}
	}
	for name := range onDisk {
		if _, ok := repoSkillCounts[name]; !ok {
			t.Fatalf("플러그인 %s 가 스킬 수 표에 없다 — 표에 없는 플러그인의 스킬은\n"+
				"이 파일의 어느 관문도 세지 않는다. 수를 적기 전에 그 스킬들을 먼저 읽어라", name)
		}
	}
	for name := range repoSkillCounts {
		if !onDisk[name] {
			t.Fatalf("표의 %s 가 plugins/ 에 없다 — 죽은 이름이 표에 남으면\n"+
				"그 표를 근거로 센 수가 전부 틀린다", name)
		}
	}

	got := map[string]int{}
	for _, s := range skillRefs(t, root) {
		got[s.plugin]++
		if _, err := os.Stat(s.path); err != nil {
			t.Fatalf("%s 에 SKILL.md 가 없다 — 디렉토리만 있으면 스킬이 아니다: %v", s.id(), err)
		}
	}
	for name, want := range repoSkillCounts {
		if got[name] != want {
			t.Fatalf("플러그인 %s 의 스킬이 %d개인데 표는 %d개라 한다 —\n"+
				"수를 고치기 전에 늘어난 쪽이 이 파일의 관문 셋을 지나는지부터 봐라", name, got[name], want)
		}
	}
}

// frontmatter 가 깨지면 스킬이 **목록에서 통째로 사라지고** 관문은 초록이었다.
func TestEverySkillFrontmatterSurvivesLoading(t *testing.T) {
	root := repoRootFromCmdFd(t)
	for _, s := range skillRefs(t, root) {
		raw, err := os.ReadFile(s.path)
		if err != nil {
			t.Fatalf("%s 를 못 읽었다: %v", s.id(), err)
		}
		front, err := skillFront(string(raw))
		if err != nil {
			t.Fatalf("%s 의 frontmatter 를 못 읽었다: %v\n"+
				"깨진 frontmatter 는 스킬을 목록에서 통째로 지운다 — 화면에 오류가 안 뜨는 부류다", s.id(), err)
		}
		if front["name"] != s.dir {
			t.Fatalf("%s 의 frontmatter name 이 %q 다 — 디렉토리 이름 %q 와 같아야 한다",
				s.id(), front["name"], s.dir)
		}
		desc, ok := front["description"]
		if !ok {
			t.Fatalf("%s 에 description 키가 없다 — 목록에 실리는 것이 그 한 줄이라, 없으면\n"+
				"스킬이 떠 있어도 아무도 언제 부를지 모른다", s.id())
		}
		desc = strings.TrimSpace(skillUnquote(desc))
		if desc == "" {
			t.Fatalf("%s 의 description 이 비었다", s.id())
		}
		if n := utf8.RuneCountInString(desc); n > skillDescRuneCap {
			t.Fatalf("%s 의 description 이 %d자(%d바이트)다 — 상한 %d자다.\n"+
				"이 상한은 DESIGN §13 「아직 아님」 6 의 **미측정값**이다. 여기 걸렸으면 상한을\n"+
				"올리지 말고 그 줄을 재라(Claude Code 판 번호를 함께 적어라)",
				s.id(), n, len(desc), skillDescRuneCap)
		}
	}
}

// 한국어 트리거 — **스킬마다 최소 하나**, 그리고 스킬 사이에 **겹치지 않는다**.
//
// ★ 최소 하나가 이 항목의 뿌리다. `session-handoff`·`session-resume` 은 한국어 트리거가
// 0개인 채로 5일을 갔고, 이 저장소의 대화가 한글이라 그동안 그 둘은 없는 것과 같았다.
// 0개는 겹침도 0건이라 **겹침만 보는 관문은 그 상태에서 초록**이다 — 그래서 수를 먼저 문다.
//
// ★ **정확히 같은 문자열만 겹침이다 — 부분 문자열은 아니다.** `session-handoff` 가
// `"인수인계"` 를, `session-resume` 이 `"인수인계 불러와"` 를 갖는 것은 2026-08-19 판단이
// **일부러 그렇게 가른 것**이다(저장과 복원이 같은 낱말 뿌리를 쓰는 것이 자연스럽고,
// 발화 전체가 다르면 갈린다). 여기를 부분 문자열 일치로 "강화"하면 그 판단이 빨개진다 —
// 그때 고칠 것은 시험이 아니라 판단이니, 바꾸려면 그 판단부터 뒤집어라.
//
// ★ 겹치면 안 되는 이유: 트리거가 같으면 같은 발화에 스킬 둘이 걸리고, 큐 항목을 닫는
// 일과 대화 맥락을 파일로 남기는 일이 서로를 가로챈다.
func TestKoreanTriggersDoNotCollideAcrossPlugins(t *testing.T) {
	root := repoRootFromCmdFd(t)
	owners := map[string][]string{}
	total := 0
	refs := skillRefs(t, root)
	for _, s := range refs {
		raw, err := os.ReadFile(s.path)
		if err != nil {
			t.Fatalf("%s 를 못 읽었다: %v", s.id(), err)
		}
		front, err := skillFront(string(raw))
		if err != nil {
			t.Fatalf("%s 의 frontmatter 를 못 읽었다: %v", s.id(), err)
		}
		desc := skillUnquote(front["description"])
		seen := map[string]bool{}
		n := 0
		for _, m := range skillTriggerRe.FindAllStringSubmatch(desc, -1) {
			trig := strings.TrimSpace(m[1])
			if trig == "" || !hangulRe.MatchString(trig) {
				continue
			}
			if seen[trig] {
				t.Fatalf("%s 가 트리거 %q 를 두 번 적었다 — 목록에 실리는 한 줄에서 자리만 먹는다", s.id(), trig)
			}
			seen[trig] = true
			owners[trig] = append(owners[trig], s.id())
			n++
		}
		if n == 0 {
			t.Fatalf("%s 의 description 에 한국어 트리거가 하나도 없다 —\n"+
				"이 저장소의 대화는 한글이라, 한국어 발화에 안 걸리는 스킬은 없는 것과 같다.\n"+
				"`\"인수인계\"` 처럼 **큰따옴표로 감싼 한국어 발화**를 description 에 넣어라", s.id())
		}
		total += n
	}
	t.Logf("스킬 %d개 · 한국어 트리거 %d개 · 서로 다른 트리거 %d개", len(refs), total, len(owners))

	var bad []string
	for trig, own := range owners {
		if len(own) > 1 {
			bad = append(bad, fmt.Sprintf("%q ← %s", trig, strings.Join(own, ", ")))
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("한국어 트리거가 스킬 사이에서 겹친다 %d건:\n  %s\n"+
			"같은 발화에 스킬 둘이 걸리면 서로를 가로챈다 — 낱말 축을 갈라라",
			len(bad), strings.Join(bad, "\n  "))
	}
}
