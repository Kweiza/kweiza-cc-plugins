package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ── codex 판 스킬의 표류 관문 ───────────────────────────────────────────────
//
// ★ 이 파일이 있는 이유: 스킬이 **두 벌**이 됐기 때문이다. 같은 판정이 두 자리에 살면
// 반드시 표류하고, 여기서 표류는 조용하다 — codex 창이 없는 문법을 따라 하면 **실패조차
// 안 난다.** 도구가 없으니 오류도 없고, 모델은 자기가 뭘 못 했는지 모른다.
// 그것이 이 항목이 처음 열린 이유이고(스킬 넷 중 둘이 없는 도구를 가르쳤다),
// 관문 없이 변종만 만들면 같은 병을 자리만 옮겨 재발시킨다.

// codexSkillVariants 는 **codex 판이 있어야 하는 스킬**이다. 표와 디스크를 양방향으로 문다.
//
// ★ 넷 중 둘만이다. `fd-setup`·`fd-update` 는 터미널 명령만 가르치므로 두 하네스에서
// 문장이 같다 — 변종을 만들면 같은 산문이 두 벌이 되고 그 자체가 표류원이 된다.
// **필요 없는 변종을 안 만드는 것도 이 표가 지키는 것이다.**
var codexSkillVariants = map[string]bool{
	"fd-pickup": true, "fd-handoff": true,
}

// mcpCallSyntax 는 **codex 창에 없는 것**을 부르는 문법이다.
//
// 변종에 이것이 남아 있으면 이 항목이 고치려던 병이 그대로다. 괄호까지 문다 —
// 산문에서 도구 이름을 언급하는 것("Claude 판은 finish(...) 를 가르친다")과
// 호출을 가르치는 것을 갈라야 하므로, **코드블록 안에서만** 센다.
var mcpCallSyntax = []string{
	"pick(", "note(", "finish(", "board(", "land(", "add(", "label(", "alloc(",
}

var (
	fenceRe   = regexp.MustCompile("(?s)```(.*?)```")
	fdCallRe  = regexp.MustCompile(`\bfd ([a-z][a-z-]*)`)
	caseStrRe = regexp.MustCompile(`case "([a-z][a-z-]*)"`)
)

// dispatchedSubcommands 는 `main.go` 가 **실제로 받는** 서브명령이다.
//
// ★ 표를 손으로 적지 않는다. 손 표는 낡고, 낡은 표는 "없는 명령을 있다고" 통과시킨다 —
// 이 관문이 막으려는 것과 정확히 같은 실패다. 소스를 읽으면 표류가 원리적으로 없다.
func dispatchedSubcommands(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("main.go 를 못 읽었다: %v", err)
	}
	out := map[string]bool{}
	for _, m := range caseStrRe.FindAllStringSubmatch(string(b), -1) {
		out[m[1]] = true
	}
	// 두 단계 명령의 하위(`fd lane wait`·`fd claim release`…)는 각자 파일에서 갈리므로
	// 여기서는 첫 낱말만 본다. 그 첫 낱말이 없으면 명령 자체가 없는 것이다.
	if len(out) < 10 {
		t.Fatalf("서브명령을 %d개만 찾았다 — 훑기가 눈이 먼 것이지 통과가 아니다", len(out))
	}
	return out
}

func readCodexSkill(t *testing.T, name string) string {
	t.Helper()
	b, err := codexSkillsFS.ReadFile("assets/codex-skills/" + name + "/SKILL.md")
	if err != nil {
		t.Fatalf("codex 판 %s 를 embed 에서 못 읽었다: %v", name, err)
	}
	return string(b)
}

// TestCodexSkillVariantsMatchTheTable 은 표와 embed 를 양방향으로 문다.
func TestCodexSkillVariantsMatchTheTable(t *testing.T) {
	ents, err := codexSkillsFS.ReadDir("assets/codex-skills")
	if err != nil {
		t.Fatalf("codex 스킬 자산을 못 읽었다: %v", err)
	}
	got := map[string]bool{}
	for _, e := range ents {
		if e.IsDir() {
			got[e.Name()] = true
		}
	}
	for name := range codexSkillVariants {
		if !got[name] {
			t.Errorf("표는 codex 판 %s 가 있다는데 embed 에 없다", name)
		}
	}
	for name := range got {
		if !codexSkillVariants[name] {
			t.Errorf("embed 에 표에 없는 codex 판 %s 가 있다 — 표를 먼저 고쳐라", name)
		}
	}
	if len(got) == 0 {
		t.Fatal("codex 판 스킬이 0건이다 — 훑기가 눈이 먼 것이지 통과가 아니다")
	}
}

// TestCodexSkillVariantsShadowARealSkill 은 **고아 변종**을 막는다.
//
// Claude 쪽에 없는 이름의 변종이 생기면 그것은 아무것도 안 가리키는 산문이고,
// 반대로 Claude 쪽 스킬이 사라졌는데 변종만 남으면 없는 도구를 가르치는 상태가 된다.
func TestCodexSkillVariantsShadowARealSkill(t *testing.T) {
	root := repoRootFromCmdFd(t)
	for name := range codexSkillVariants {
		p := filepath.Join(root, "plugins", "flightdeck", "skills", name, "SKILL.md")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("codex 판 %s 에 대응하는 Claude 판이 없다(%s) — 고아 변종이다: %v", name, p, err)
		}
	}
}

// TestCodexSkillsTeachNoMCPSyntax 는 변종이 **없는 도구**를 부르라고 가르치지 않는지 문다.
//
// ★ 이 항목이 처음 열린 이유가 정확히 이것이다. 코드블록 안에서만 센다 — 산문에서
// "Claude 판은 finish(...) 를 가르친다"고 **대비를 설명하는 것**은 오히려 필요하다.
func TestCodexSkillsTeachNoMCPSyntax(t *testing.T) {
	for name := range codexSkillVariants {
		body := readCodexSkill(t, name)
		blocks := fenceRe.FindAllStringSubmatch(body, -1)
		if len(blocks) == 0 {
			t.Errorf("%s 에 코드블록이 하나도 없다 — 무엇을 치라는 것인지 화면에 없다", name)
		}
		for _, b := range blocks {
			for _, bad := range mcpCallSyntax {
				if strings.Contains(b[1], bad) {
					t.Errorf("codex 판 %s 의 코드블록이 MCP 문법 %q 를 가르친다 — "+
						"codex 창에는 그 도구가 없고, 불러도 **실패조차 안 난다**", name, bad)
				}
			}
		}
	}
}

// TestCodexSkillsOnlyTeachRealSubcommands 는 변종이 부르는 `fd <sub>` 가 **전부 실재하는지** 문다.
//
// ★★ 이것이 이 파일에서 제일 값이 나가는 관문이다. 변종은 손으로 쓴 산문이고, 없는
// 서브명령을 적어도 아무도 안 붉는다 — codex 가 그것을 치면 "모르는 명령"으로 죽고,
// 모델은 fd 가 고장난 줄 안다. `main.go` 의 dispatch 를 직접 읽어 대조한다.
func TestCodexSkillsOnlyTeachRealSubcommands(t *testing.T) {
	real := dispatchedSubcommands(t)
	seen := 0
	for name := range codexSkillVariants {
		body := readCodexSkill(t, name)
		var bad []string
		for _, m := range fdCallRe.FindAllStringSubmatch(body, -1) {
			sub := m[1]
			seen++
			if !real[sub] {
				bad = append(bad, sub)
			}
		}
		if len(bad) > 0 {
			sort.Strings(bad)
			t.Errorf("codex 판 %s 가 실재하지 않는 서브명령을 가르친다: %s\n"+
				"main.go 의 dispatch 에 없는 이름이다 — codex 가 이걸 치면 그냥 죽는다",
				name, strings.Join(bad, ", "))
		}
	}
	if seen == 0 {
		t.Fatal("변종에서 `fd <명령>` 을 하나도 못 찾았다 — 정규식이 눈이 먼 것이지 통과가 아니다")
	}
}

// TestCodexSkillsHaveFrontmatter 는 frontmatter 가 성한지 문다.
// 깨지면 codex 가 그 스킬을 **목록에서 통째로 빼고**, 그 사실은 어디에도 안 뜬다.
func TestCodexSkillsHaveFrontmatter(t *testing.T) {
	for name := range codexSkillVariants {
		body := readCodexSkill(t, name)
		if !strings.HasPrefix(body, "---\n") {
			t.Errorf("%s 가 frontmatter 로 시작하지 않는다", name)
			continue
		}
		head, _, ok := strings.Cut(body[4:], "\n---\n")
		if !ok {
			t.Errorf("%s 의 frontmatter 가 안 닫혔다", name)
			continue
		}
		if !strings.Contains(head, "name: "+name) {
			t.Errorf("%s 의 frontmatter name 이 디렉토리 이름과 다르다:\n%s", name, head)
		}
		if !strings.Contains(head, "description:") {
			t.Errorf("%s 에 description 이 없다 — 트리거가 없으면 아무 발화에도 안 걸린다", name)
		}
		// ★ 한국어 트리거 — Claude 쪽 `repo_skills_test.go` 가 무는 것과 같은 축이다.
		// 이 저장소의 발화는 한국어다.
		if !strings.ContainsAny(head, "가나다라마바사아자차카타파하작세뭐마핸넘") {
			t.Errorf("%s 의 description 에 한국어 트리거가 없다 — 한국어 발화에 아무것도 안 걸린다", name)
		}
	}
}
