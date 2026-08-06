package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 플러그인 배선 시험 — **파일을 실제로 파싱해 단정한다.**
//
// 여기 있는 것들은 Go 코드가 아니라 JSON·셸이라 컴파일러가 안 봐 준다.
// 그리고 틀리면 조용히 죽는다: type 이 없는 MCP 항목은 서버가 통째로 스킵되고,
// 훅 경로가 틀리면 세션이 그냥 아무것도 안 한다. 그래서 여기가 유일한 검사다.

func pluginRoot(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("플러그인 루트를 못 찾았다: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatalf("여기가 플러그인 루트가 아니다(%s): %v", p, err)
	}
	return p
}

type hooksFile struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Async   bool   `json:"async"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func TestHooksJSONIsWiredAsDesigned(t *testing.T) {
	root := pluginRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("hooks.json 을 못 읽었다: %v", err)
	}
	var hf hooksFile
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&hf); err != nil {
		t.Fatalf("hooks.json 이 유효한 JSON 이 아니거나 모르는 키가 있다: %v", err)
	}

	want := map[string]struct {
		matcher string
		async   bool
	}{
		"SessionStart":     {"startup|resume|clear|compact", false},
		"UserPromptSubmit": {"", false},
		"PostToolUse":      {"Edit|Write", true},
		"PreCompact":       {"", true},
		// ★ Stop 은 async 면 안 된다 — 이 훅의 출력이 곧 처방 배달이고, async 는
		//   그 출력의 운명을 안 정해 준다(설계 §6).
		"Stop": {"", false},
		// ★ SessionEnd 는 **clear 만** 받는다. 아래 단정이 그 폭을 붙들고 있다.
		"SessionEnd": {"clear", true},
	}
	if len(hf.Hooks) != len(want) {
		t.Fatalf("훅 이벤트가 %d개다 — %d개여야 한다: %v", len(hf.Hooks), len(want), keysOf(hf.Hooks))
	}

	// ★★ SessionEnd 의 폭을 여기서 못박는다. **이 단정이 이 파일에서 가장 중요하다.**
	//
	// 앞선 판은 SessionEnd 를 통째로 금지했고 그 사유는 "세션 종료를 신뢰성 있게 감지할
	// 수단이 없다"였다. **그 전제는 지금도 참이다** — 설치본 2.1.221·2.1.222 를 뜯어 보면
	// executeSessionEndHooks 를 부르는 자리가 `o3t("clear", …)` 와 `o3t("resume", …)` 둘뿐이고,
	// logout·prompt_input_exit·other·bypass_permissions_disabled 는 zod 열거값에만 있고
	// 아무도 안 쏜다. 훅 이벤트 31종에도 프로세스 종료를 알리는 것이 없다.
	//
	// 바뀐 것은 **쓰임**이다. 이 훅은 죽음을 감지하지 않는다. /clear 로 떠나는 대화의 카드를
	// 닫을 뿐이고, 그 판정은 되돌릴 수 있다(Tx.OpenSession 이 닫힌 카드를 되살린다).
	//
	// 그래서 matcher 를 넓히면 안 된다:
	//   · logout·prompt_input_exit·other — 아무도 안 쏜다. 넣으면 "잡고 있다"는 착각만 생긴다
	//     (prompt_input_exit 옆에는 "Session keeps running. Use /stop to end it." 이 박혀 있다)
	//   · resume — **/fork 도 같은 사유로 온다.** fork 에서 원본 카드를 닫는 것이 옳은지는
	//     별도 판단이고 지금 그 근거가 없다
	se := hf.Hooks["SessionEnd"]
	if len(se) != 1 {
		t.Fatalf("SessionEnd 그룹이 %d개다 — 하나여야 한다", len(se))
	}
	if se[0].Matcher != "clear" {
		t.Fatalf("SessionEnd 의 matcher 가 %q 다 — clear 하나여야 한다. "+
			"이 훅은 프로세스 종료를 못 잡는다(사유 넷은 아무도 안 쏘고, resume 은 /fork 와 공유한다)",
			se[0].Matcher)
	}

	for ev, w := range want {
		groups, ok := hf.Hooks[ev]
		if !ok || len(groups) == 0 {
			t.Fatalf("%s 훅이 없다", ev)
		}
		g := groups[0]
		if g.Matcher != w.matcher {
			t.Fatalf("%s 의 matcher 가 %q 다, %q 를 기대했다", ev, g.Matcher, w.matcher)
		}
		if len(g.Hooks) != 1 {
			t.Fatalf("%s 에 훅이 %d개다", ev, len(g.Hooks))
		}
		h := g.Hooks[0]
		if h.Type != "command" {
			t.Fatalf("%s 의 type 이 %q 다", ev, h.Type)
		}
		// ★ 절대경로여야 한다. 훅 실행 환경이 Bash 도구와 같다는 보장이 없다(설계 §13).
		if !strings.Contains(h.Command, "${CLAUDE_PLUGIN_ROOT}/bin/fd") {
			t.Fatalf("%s 의 명령이 ${CLAUDE_PLUGIN_ROOT}/bin/fd 절대경로가 아니다: %q", ev, h.Command)
		}
		if h.Async != w.async {
			t.Fatalf("%s 의 async 가 %v 다, %v 를 기대했다", ev, h.Async, w.async)
		}
		if h.Timeout <= 0 {
			t.Fatalf("%s 에 타임아웃이 없다 — 훅이 끊기지 않으면 세션이 멈춘다", ev)
		}
	}

	// 훅 이름이 fd 가 아는 것과 **같아야** 한다. 여기가 어긋나면 fd 는 fail-open 이라
	// 조용히 아무것도 안 하고 0 을 낸다 — 그것이 이 단정을 두는 이유다.
	known := map[string]bool{
		"session-start": true, "user-prompt": true, "post-tool": true, "pre-compact": true,
		"stop": true, "session-end": true,
	}
	for ev, groups := range hf.Hooks {
		cmd := groups[0].Hooks[0].Command
		fields := strings.Fields(cmd)
		name := fields[len(fields)-1]
		if !known[name] {
			t.Fatalf("%s 가 fd 가 모르는 훅 이름 %q 를 부른다", ev, name)
		}
	}
}

func keysOf[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestMCPJSONHasTypeStdio(t *testing.T) {
	root := pluginRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatalf(".mcp.json 을 못 읽었다: %v", err)
	}
	var f struct {
		MCPServers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf(".mcp.json 이 유효한 JSON 이 아니다: %v", err)
	}
	srv, ok := f.MCPServers["fd"]
	if !ok {
		t.Fatalf("mcpServers 에 fd 가 없다: %v", keysOf(f.MCPServers))
	}
	// ★ type 이 없으면 **서버가 통째로 스킵된다.** 조용히 사라지므로 여기서 막는다.
	if srv.Type != "stdio" {
		t.Fatalf("type 이 %q 다 — stdio 여야 한다(없으면 서버가 통째로 스킵된다)", srv.Type)
	}
	if srv.Command != "${CLAUDE_PLUGIN_ROOT}/bin/fd" {
		t.Fatalf("command 가 %q 다", srv.Command)
	}
	if len(srv.Args) != 1 || srv.Args[0] != "mcp" {
		t.Fatalf("args 가 %v 다 — [\"mcp\"] 여야 한다", srv.Args)
	}
}

// 런처는 **Go 가 없어도 종료코드 0** 이다. 훅이 세션을 막으면 안 된다.
func TestLauncherIsFailOpenWithoutGo(t *testing.T) {
	root := pluginRoot(t)
	script := filepath.Join(root, "bin", "fd")
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("bin/fd 가 없다: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("bin/fd 에 실행 권한이 없다: %v", info.Mode())
	}

	// PATH 를 비워 Go 를 없앤다. 스크립트 자체는 절대경로 bash 로 띄운다
	// (shebang 의 /usr/bin/env 도 PATH 를 타므로).
	state := t.TempDir()
	cmd := exec.Command("/bin/bash", script, "status")
	cmd.Env = []string{"PATH=", "HOME=" + t.TempDir(), "FD_STATE_DIR=" + state}
	out, err := cmd.CombinedOutput()

	// 대조 전제: 정말 Go 없이 돌았나. 캐시된 바이너리가 있으면 이 시험은 아무것도 안 본다.
	if _, serr := os.Stat(filepath.Join(state, "bin", "fd")); serr == nil {
		t.Fatal("대조 전제가 깨졌다 — Go 없이 돌렸는데 바이너리가 만들어졌다")
	}
	if err != nil {
		t.Fatalf("런처가 실패했다(종료코드 0 이어야 한다): %v\n%s", err, out)
	}
	got := string(out)
	mustContain(t, "런처 안내", got,
		"Go 툴체인이 없어",
		"조정 기능 없이 그대로 진행된다",
	)
}

// 정상 환경에서는 실제로 빌드해 exec 한다.
func TestLauncherBuildsAndRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("빌드가 걸린다")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("이 머신에 Go 가 없다")
	}
	root := pluginRoot(t)
	state := t.TempDir()
	cmd := exec.Command(filepath.Join(root, "bin", "fd"), "version")
	cmd.Env = append(os.Environ(), "FD_STATE_DIR="+state)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("런처가 빌드·실행에 실패했다: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "fd api=") {
		t.Fatalf("빌드된 바이너리가 안 돌았다:\n%s", out)
	}
	if _, serr := os.Stat(filepath.Join(state, "bin", "fd")); serr != nil {
		t.Fatalf("바이너리가 캐시되지 않았다 — 매 훅마다 다시 빌드하게 된다: %v", serr)
	}
}

// 스킬 본문의 줄 수 상한은 **호출 빈도로 갈린다.**
//
// ★ 앞선 판의 주석은 "스킬 목록은 항목당 잘리므로"를 60줄의 근거로 댔는데, 그 문장이
// 맞다면 **본문을 아무리 길게 써도 목록은 안 밀린다** — 근거와 결론이 안 이어진다.
// 그래서 상한을 "부른 턴의 예산"으로 다시 세우고 호출 빈도로 갈랐다. 그 비용은 부르는
// 만큼 반복해서 들기 때문이다.
//
// ★ **다만 그 재정의가 기대는 플랫폼 동작은 아직 안 쟀다 — 측정 전 잠정이다.**
// "목록에는 frontmatter 의 `description`(항목당 1,536자)만 실리고 본문은 스킬을 부른
// 뒤에 실린다"는 이 레포 어디에도 측정 기록이 없다(§13 「아직 아님」에 올려 뒀다).
// 앞선 판이 두 축을 섞은 자리를 **또 다른 미측정값**으로 메우지 않기 위해 여기 적어 둔다 —
// §13 의 첫 줄이 "추측을 사실로 적지 않는다"다. 80 을 지탱하는 실질 근거는 아래 회귀선이고,
// 그쪽은 이 측정과 무관하게 선다.
//
// ★ 머신 스킬의 80은 계산이 아니라 **회귀선**이다. `fd-update` 가 지금 72줄이고, 그 산문은
// 갱신 판정을 코드로 안 뺐기 때문에 있다(DESIGN §1 의 2026-08-06 개정). 여기서 더 늘면
// 줄을 깎을 것이 아니라 `fd update` 를 만들지를 다시 판정해야 한다 — 그 판정을 미루는 대신
// 상한을 올리는 것이 이 항목이 고발한 표류다.
var skillLineCaps = map[string]int{
	"fd-pickup":  60, // 매 세션 부른다
	"fd-handoff": 60, // 매 세션 부른다
	"fd-setup":   80, // 머신당 1회
	"fd-update":  80, // 머신당 1회
}

func TestSkillsStayWithinTheContextBudget(t *testing.T) {
	root := pluginRoot(t)

	// ★ 스킬 **전수**를 표에 물린다. 이 항목의 뿌리가 "넷째(`fd-update`)가 이 표에서 빠진
	// 채로 DESIGN 도 셋이라 적고 아무도 안 세었다"이다. 표를 손으로만 유지하면 다섯째에
	// 같은 일이 그대로 난다 — 빠진 스킬은 줄 수도 frontmatter 도 검사받지 않는다.
	ents, err := os.ReadDir(filepath.Join(root, "skills"))
	if err != nil {
		t.Fatalf("skills 디렉토리를 못 읽었다: %v", err)
	}
	onDisk := map[string]bool{}
	for _, e := range ents {
		if e.IsDir() {
			onDisk[e.Name()] = true
		}
	}
	for name := range onDisk {
		if _, ok := skillLineCaps[name]; !ok {
			t.Fatalf("스킬 %s 가 상한 표에 없다 — 표에 없는 스킬은 줄 수도 frontmatter 도\n"+
				"검사받지 않는다. 수를 늘리기 전에 DESIGN §1 이 그것을 정당화하는지부터 정해라", name)
		}
	}
	for name := range skillLineCaps {
		if !onDisk[name] {
			t.Fatalf("상한 표의 %s 가 skills/ 에 없다 — 죽은 이름이 표에 남으면\n"+
				"그 표를 근거로 센 수가 전부 틀린다", name)
		}
	}

	for name, limit := range skillLineCaps {
		path := filepath.Join(root, "skills", name, "SKILL.md")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s 를 못 읽었다: %v", name, err)
		}
		lines := strings.Count(strings.TrimRight(string(raw), "\n"), "\n") + 1
		if lines >= limit {
			t.Fatalf("%s 가 %d줄이다 — %d줄 미만이어야 한다", name, lines, limit)
		}
		// frontmatter 의 name·description 이 없으면 스킬이 목록에 안 뜬다.
		head := string(raw)
		if !strings.HasPrefix(head, "---\n") {
			t.Fatalf("%s 에 frontmatter 가 없다", name)
		}
		mustContain(t, name+" frontmatter", head, "name: "+name, "description:")
	}
}

// 문서가 세는 스킬 수와 실재하는 수가 어긋나면 여기서 걸린다.
//
// ★ 이 시험이 없어서 난 일이 이 항목이다: `cdce59d` 가 넷째를 만들었고 README 는 넷으로
// 고쳤는데 DESIGN §1 만 셋으로 남았다. 그 문장 바로 아래 문단이 **"셋인 근거"**를 대고
// 있었으므로, 수만 고치면 그 문단이 통째로 거짓이 되는 자리였다 — 즉 이 어긋남은
// 오탈자가 아니라 **설계 판정이 밀린 흔적**이고, 그래서 조용히 오래 남았다.
//
// ★ 잠그는 것은 **수**뿐이다. 근거 산문까지 잠그면 개정할 때마다 시험이 깨져서
// 근거를 안 고치고 수만 고치는 쪽으로 사람을 민다.
//
// ★ **"어딘가에 그 수가 있다"로는 부족하다 — 모든 출현을 본다.** 앞선 판은
// `strings.Contains(파일 전체, "스킬은 4개")` 였는데, 그러면 문서 끝에 "스킬은 3개"를
// 덧붙여도 초록이다(격리 사본에서 실증). 이 저장소는 옛 문단을 안 지우고 개정 블록을
// 얹는 습관이 있어서(§1 이 그 예다) 낡은 수가 남기 쉽고, 그게 정확히 이 항목의 뿌리였다.
// 그래서 정규식으로 **출현 전부**를 뽑아 하나라도 다르면 실패시킨다. 0건도 실패다.
// §1 이 보존한 옛 문단의 "셋"·"넷"은 한글이라 이 정규식에 안 걸린다 — 사료 서술은
// 살아남고 현재형 수만 잠긴다.
func TestDocsCountTheSkillsThatActuallyExist(t *testing.T) {
	root := pluginRoot(t)
	// ★ 수를 상한 표에서 뽑는다. 표는 위 시험이 skills/ 전수에 물려 뒀으므로, 다섯째가
	// 생기면 연쇄가 정확히 돈다: 전수 검사가 "표에 없다"로 먼저 걸리고 → 표에 넣으면
	// 이 시험이 문서의 수를 요구한다. 어느 한 자리만 고치고 지나가는 길이 없다.
	want := len(skillLineCaps)
	for _, doc := range []struct{ file, pattern string }{
		{"DESIGN.md", `스킬은 (\d+)개`},
		{"README.md", `스킬 (\d+)개`},
	} {
		raw, err := os.ReadFile(filepath.Join(root, doc.file))
		if err != nil {
			t.Fatalf("%s 를 못 읽었다: %v", doc.file, err)
		}
		hits := regexp.MustCompile(doc.pattern).FindAllStringSubmatch(string(raw), -1)
		if len(hits) == 0 {
			t.Fatalf("%s 가 스킬 수를 아예 안 말한다(정규식 %q) — 실재하는 스킬은 %d개다(skills/).\n"+
				"수를 고칠 때는 그 수의 **근거**를 대는 문단이 같이 거짓이 되는지 보고,\n"+
				"거짓이 되면 근거부터 다시 써라(DESIGN §1 의 2026-08-06 개정이 그 예다)",
				doc.file, doc.pattern, want)
		}
		for _, h := range hits {
			if h[1] != fmt.Sprintf("%d", want) {
				t.Fatalf("%s 가 %q 라고 말한다 — 실재하는 스킬은 %d개다(skills/).\n"+
					"출현 %d건 중 하나라도 어긋나면 실패다. 개정 블록을 얹을 때 **낡은 수를 현재형으로**\n"+
					"남기지 마라 — 이 항목의 뿌리가 바로 그것이다(사료로 남길 것은 한글로 적어라)",
					doc.file, h[0], want, len(hits))
			}
		}
	}
}

// compose 와 Dockerfile 이 설계가 못박은 좌표를 지키는가.
func TestContainerFilesKeepTheDesignedCoordinates(t *testing.T) {
	root := pluginRoot(t)
	df, err := os.ReadFile(filepath.Join(root, "server", "Dockerfile"))
	if err != nil {
		t.Fatalf("Dockerfile 이 없다: %v", err)
	}
	mustContain(t, "Dockerfile", string(df),
		"CGO_ENABLED=0",
		"distroless",
		"HEALTHCHECK",
		"EXPOSE 7420",
	)
	cf, err := os.ReadFile(filepath.Join(root, "compose.yaml"))
	if err != nil {
		t.Fatalf("compose.yaml 이 없다: %v", err)
	}
	mustContain(t, "compose.yaml", string(cf),
		"7420:7420",
		"~/.flightdeck:/data",
		"restart: unless-stopped",
		"healthcheck:",
	)
}
