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

// ── 플래그 축 ─────────────────────────────────────────────────────────────────
//
// ★ 위 다섯은 **서브명령**까지만 문다. 그 아래 한 칸, 플래그는 아무도 안 봤고
// 그 칸에서 정확히 이 항목이 났다: `fd finish --followups` 가 92e452b 로 들어온
// **1시간 24분 뒤**에 「이 창에는 followups 가 없다」고 적은 변종이 실려 나갔다.
// 코드와 산문이 **같은 커밋 안에서** 갈렸고, 관문 다섯 중 어느 것도 안 붉었다.
//
// 그 거짓이 무해하지 않은 이유: 변종을 믿은 codex 창은 후속을 `fd add` 로 따로
// 만들고, 그러면 판단과의 FK 연결을 영영 못 산다 — **이 항목이 막으려던 바로 그
// 피해가 문서를 통해 일어난다.** 없는 명령을 가르치면 codex 는 죽기라도 하는데,
// 있는 기능을 없다고 가르치면 **아무도 안 죽고 원장만 조용히 상한다.**

var (
	flagDefRe  = regexp.MustCompile(`fs\.(?:String|Bool|Int|Int64|Uint|Float64|Duration)\("([a-z][a-z0-9-]*)"`)
	flagVarRe  = regexp.MustCompile(`fs\.Var\(&[A-Za-z_][A-Za-z0-9_]*,\s*"([a-z][a-z0-9-]*)"`)
	backtickRe = regexp.MustCompile("`([^`]+)`")
)

// cliFlagNames 는 `cmd/fd` 가 **실제로 정의하는** 플래그 이름 전부다(모든 서브명령 합집합).
//
// ★ 손 표를 안 쓰는 이유는 관문 넷째와 같다 — 손 표는 낡고, 낡은 표는 이 관문이
// 막으려는 것을 그대로 통과시킨다. 소스를 읽으면 표류가 원리적으로 없다.
func cliFlagNames(t *testing.T) map[string]bool {
	t.Helper()
	srcs, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("소스를 못 훑었다: %v", err)
	}
	out := map[string]bool{}
	for _, p := range srcs {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s 를 못 읽었다: %v", p, err)
		}
		for _, re := range []*regexp.Regexp{flagDefRe, flagVarRe} {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				out[m[1]] = true
			}
		}
	}
	// 눈멂 방지. 이 패키지의 플래그는 수십 개다 — 한 자릿수면 정규식이 먼 것이지 통과가 아니다.
	if len(out) < 20 {
		t.Fatalf("플래그를 %d개만 찾았다 — 훑기가 눈이 먼 것이지 통과가 아니다", len(out))
	}
	if !out["followups"] || !out["body"] {
		t.Fatalf("아는 플래그(followups·body)가 훑기에 안 걸린다 — 정규식이 소스 형태와 갈렸다")
	}
	return out
}

// TestCodexSkillsDoNotDenyFlagsThatExist 는 변종의 **부정 주장**을 소스에 대조한다.
//
// 무는 것: 한 줄 안에 백틱 토큰과 「없다」가 함께 있고, 그 토큰이 실재하는 플래그일 때.
// 「없다」고 쓰려면 **정말 없어야 한다** — 이 관문은 그 규율 하나다.
//
// ★ **이 시험이 무엇을 못 잡는지 먼저 적는다.** 이름을 안 부르는 부정은 안 걸린다.
// 이 사건의 원문에도 그런 줄이 있었다 — 「**CLI 에는 그 인자가 없다.**」 는 백틱이
// 없어 이 관문을 그대로 지나간다. 걸린 것은 이름을 부른 두 줄(제목의 `followups`,
// 머리말의 `followups`)이었다. 즉 이 관문은 **거짓말 전체가 아니라 그 첫 문장**을
// 무는 것이고, 이름 없이 한 문단을 쓰면 여전히 샌다. 그 사각을 정규식으로 메우려면
// 산문 구조를 가정해야 하는데, 낡은 가정은 이 관문이 막으려는 것과 같은 실패다.
func TestCodexSkillsDoNotDenyFlagsThatExist(t *testing.T) {
	flags := cliFlagNames(t)
	for name := range codexSkillVariants {
		for i, line := range strings.Split(readCodexSkill(t, name), "\n") {
			if !strings.Contains(line, "없다") {
				continue
			}
			for _, m := range backtickRe.FindAllStringSubmatch(line, -1) {
				tok := strings.TrimPrefix(strings.TrimSpace(m[1]), "--")
				if !flags[tok] {
					continue
				}
				t.Errorf("codex 판 %s:%d 이 `%s` 가 없다고 가르치는데 **실재한다**:\n  %s\n"+
					"이 거짓은 실패를 안 낸다 — 변종을 믿은 창이 대체 경로로 흘러가고 원장이 조용히 상한다",
					name, i+1, tok, strings.TrimSpace(line))
			}
		}
	}
}

var (
	newFlagSetRe = regexp.MustCompile(`newFlagSet\("([a-z][a-z ]*)"\)`)
	heredocRe    = regexp.MustCompile(`<<-?'?([A-Za-z_][A-Za-z0-9_]*)'?`)
	contRe       = regexp.MustCompile(`\\\n`)
	wordRe       = regexp.MustCompile(`^[a-z][a-z-]*$`)
)

// cliFlagsBySubcommand 는 서브명령마다 **그 flagset 이 실제로 정의한** 플래그다.
//
// 구간은 `newFlagSet("<이름>")` 부터 다음 `newFlagSet(` 까지로 자른다 — 이 패키지는
// 서브명령 하나가 flagset 하나를 열고 곧바로 플래그를 다는 형태라 그 경계가 함수 경계와 같다.
func cliFlagsBySubcommand(t *testing.T) map[string]map[string]bool {
	t.Helper()
	srcs, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("소스를 못 훑었다: %v", err)
	}
	out := map[string]map[string]bool{}
	for _, p := range srcs {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s 를 못 읽었다: %v", p, err)
		}
		src := string(b)
		locs := newFlagSetRe.FindAllStringSubmatchIndex(src, -1)
		for i, loc := range locs {
			name := src[loc[2]:loc[3]]
			end := len(src)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			seg := src[loc[1]:end]
			if out[name] == nil {
				out[name] = map[string]bool{}
			}
			for _, re := range []*regexp.Regexp{flagDefRe, flagVarRe} {
				for _, m := range re.FindAllStringSubmatch(seg, -1) {
					out[name][m[1]] = true
				}
			}
		}
	}
	// 눈멂 방지 — 아는 조합이 안 걸리면 구간 자르기가 소스 형태와 갈린 것이다.
	if !out["finish"]["followups"] || !out["pick"]["leave"] || !out["land"]["resource"] {
		t.Fatalf("아는 조합(finish/followups · pick/leave · land/resource)이 훑기에 안 걸린다: finish=%v", out["finish"])
	}
	return out
}

// skillCommandLines 는 변종의 **코드블록**에서 `fd …` 한 줄씩을 낸다.
// heredoc 본문과 `#` 주석은 명령이 아니므로 걷어낸다. `\` 줄이음은 한 줄로 잇는다.
func skillCommandLines(body string) []string {
	var out []string
	for _, blk := range fenceRe.FindAllStringSubmatch(body, -1) {
		// 줄이음이 먼저다 — `\` 로 끊긴 한 명령이 여러 줄로 보이면 플래그가 명령을 잃는다.
		s := contRe.ReplaceAllString(blk[1], " ")
		here := "" // 여는 heredoc 의 종료 표시. RE2 에는 역참조가 없어 상태로 든다
		for _, line := range strings.Split(s, "\n") {
			if here != "" {
				if strings.TrimSpace(line) == here {
					here = ""
				}
				continue // heredoc 본문은 명령이 아니다 — 판단 본문이 그 안에 산다
			}
			if m := heredocRe.FindStringSubmatchIndex(line); m != nil {
				here = line[m[2]:m[3]]
				line = line[:m[0]] // 여는 줄에는 `--body -` 가 남아 있다
			}
			if i := strings.Index(line, "#"); i >= 0 {
				line = line[:i]
			}
			if strings.Contains(line, "fd ") {
				out = append(out, line)
			}
		}
	}
	return out
}

// TestCodexSkillsOnlyTeachRealFlags 는 변종이 가르치는 `fd <명령> --<플래그>` 를
// **그 명령의 flagset** 에 대조한다.
//
// ★ 관문 넷째의 한 칸 아래다. 저쪽은 명령이 실재하는지만 보고, 플래그는 안 봤다 —
// codex 가 없는 플래그를 치면 `flag provided but not defined` 로 죽고, 모델은 fd 가
// 고장난 줄 안다. 그리고 그 죽음은 **이 저장소가 아니라 남의 터미널에서** 일어난다.
func TestCodexSkillsOnlyTeachRealFlags(t *testing.T) {
	bySub := cliFlagsBySubcommand(t)
	seen := 0
	for name := range codexSkillVariants {
		for _, line := range skillCommandLines(readCodexSkill(t, name)) {
			toks := strings.Fields(line)
			for i := 0; i < len(toks); i++ {
				if toks[i] != "fd" {
					continue
				}
				// 서브명령: `fd` 뒤로 이어지는 소문자 낱말 최대 둘. 긴 것부터 맞춘다.
				var words []string
				for j := i + 1; j < len(toks) && len(words) < 2 && wordRe.MatchString(toks[j]); j++ {
					words = append(words, toks[j])
				}
				sub := ""
				for n := len(words); n > 0; n-- {
					if cand := strings.Join(words[:n], " "); bySub[cand] != nil {
						sub = cand
						break
					}
				}
				if sub == "" {
					continue // flagset 이 없는 명령 — 이 축은 판정할 것이 없다
				}
				for j := i + 1; j < len(toks); j++ {
					if toks[j] == "fd" {
						break
					}
					flag, ok := strings.CutPrefix(toks[j], "--")
					if !ok || flag == "help" { // --help 는 flag 패키지가 늘 준다
						continue
					}
					flag, _, _ = strings.Cut(flag, "=")
					seen++
					if !bySub[sub][flag] {
						t.Errorf("codex 판 %s 가 `fd %s --%s` 를 가르치는데 그 명령에 그 플래그가 없다:\n  %s\n"+
							"codex 가 이걸 치면 `flag provided but not defined` 로 죽는다",
							name, sub, flag, strings.TrimSpace(line))
					}
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("변종의 코드블록에서 `fd <명령> --<플래그>` 를 하나도 못 찾았다 — 훑기가 눈이 먼 것이지 통과가 아니다")
	}
	t.Logf("대조한 플래그 사용 %d건", seen)
}
