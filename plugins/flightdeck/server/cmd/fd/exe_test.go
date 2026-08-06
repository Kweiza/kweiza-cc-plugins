package main

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 지금 계산되는 바이너리 캐시 자리 — 시험 안에서 '자리 안/밖'의 기준점이다.
const testBinDir = "/home/u/.cache/flightdeck/bin"

func TestExeLinesPlainPath(t *testing.T) {
	got := ExeLines(testBinDir+"/fd-%2fhome%2fu%2fsrc", nil, testBinDir)
	if len(got) != 1 {
		t.Fatalf("교체도 안 됐고 자리도 맞는데 줄이 %d개다: %v", len(got), got)
	}
	if !strings.HasPrefix(got[0], "실행 파일 "+testBinDir+"/fd-") {
		t.Fatalf("경로를 잘못 냈다: %q", got[0])
	}
	if strings.Contains(got[0], "deleted") {
		t.Fatalf("표식이 새어 나왔다: %q", got[0])
	}
}

// ★ 이 갈래는 **방어**다 — 지금 실물에서는 안 뜬다.
//
// 예전 이 자리에는 "이 갈래가 이 함수의 존재 이유다"라고 적혀 있었는데, 그것이 거짓이라
// 낮춘다. 실제 프로세스로 못 만드는 이유가 "그런 프로세스가 없어서"가 아니라 **os.Executable 이
// 표식을 떼고 주기 때문**이다(exe.go 의 deletedSuffix ★, 2026-08-07 실측). 그래서 이 시험이
// 잠그는 것은 "표식이 들어오면 경로에서 떼고 뜻을 낸다"이지 "실물 doctor 에 이 줄이 뜬다"가
// 아니다 — 후자를 재는 시험은 이 파일 끝의 doctor 배선 시험이고, 그쪽은 자리 축만 본다.
func TestExeLinesReplacedBinaryIsAnnounced(t *testing.T) {
	got := ExeLines(testBinDir+"/fd-%2fhome%2fu%2fsrc"+deletedSuffix, nil, testBinDir)
	if len(got) != 2 {
		t.Fatalf("교체된 자리인데 줄이 %d개다 — 사실만 찍고 뜻을 안 낸 것이다: %v", len(got), got)
	}
	if strings.Contains(got[0], "deleted") {
		t.Fatalf("경로 줄에 커널 표식이 남았다: %q", got[0])
	}
	if !strings.HasPrefix(got[0], "실행 파일 "+testBinDir+"/fd-") {
		t.Fatalf("경로에서 표식만 떼야 한다: %q", got[0])
	}
	// 사람이 다음에 할 일이 그 줄에 있어야 한다. 없으면 빌드를 다시 하러 간다.
	if !strings.Contains(got[1], "재기동") {
		t.Fatalf("무엇을 해야 하는지가 없다: %q", got[1])
	}
}

// ★ 링크 아래에서 거짓 경보를 안 낸다 — **자리가 둘인 것**과 **이름이 둘인 것**은 다르다.
//
// 실측 재현: 가짜 홈의 `~/.cache` 를 다른 디스크로 링크해 두고 런처로 doctor 를 돌리면,
// 방금 지어 exec 한 바로 그 파일에 "자리 밖"이 붙고 재기동해도 안 사라졌다. 리눅스의
// os.Executable 은 푼 경로(`/proc/self/exe`)를, wantDir 은 HOME 을 이어 붙인 안 푼 경로를
// 주기 때문이다. `~/.cache` 를 큰 디스크로 옮긴 구성·`/home -> /var/home` 인 배포판·NFS 홈이
// 전부 그 모양이라, 이 시험이 없으면 다음 사람이 문자열 비교로 되돌린다.
//
// 이 시험만 진짜 디렉토리를 만든다 — 나머지 갈래는 문자열로 다 준다(inode 는 못 꾸민다).
func TestExeLinesSeesThroughSymlinkedHome(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "bigdisk", "cache", "flightdeck", "bin")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "cachelink")
	if err := os.Symlink(filepath.Join(root, "bigdisk", "cache"), link); err != nil {
		t.Skipf("이 판에서 심볼릭 링크를 못 만든다: %v", err)
	}
	want := filepath.Join(link, "flightdeck", "bin") // 런처가 계산하는 **안 푼** 자리
	exe := filepath.Join(real, "fd-%2fhome%2fu%2fsrc")

	cases := []struct {
		name  string
		exe   string
		lines int
	}{
		{"한 자리를 두 이름으로 부른 것이면 침묵한다", exe, 1},
		// 파일이 교체돼도 **부모**는 살아 있으므로 좌표계 맞추기가 계속 선다.
		// (예전 주석은 "`(deleted)` 갈래에서는 어차피 실패한다"고 적었는데, 푸는 대상이
		//  파일이 아니라 부모 디렉토리라 그 말이 안 맞는다.)
		{"교체 갈래에서도 좌표계가 맞는다", exe + deletedSuffix, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExeLines(tc.exe, nil, want)
			if len(got) != tc.lines {
				t.Fatalf("줄이 %d개다(기대 %d): %v", len(got), tc.lines, got)
			}
			for _, l := range got {
				if strings.Contains(l, "자리 밖") {
					t.Fatalf("같은 디렉토리인데 거짓 경보를 냈다: %q", l)
				}
			}
		})
	}

	// ★ 참 양성이 안 죽는지 — 진짜 다른 자리는 링크를 풀어도 그대로 걸려야 한다.
	// 이 대조가 없으면 위 셋은 "축을 껐다"로도 통과한다.
	old := filepath.Join(root, "state", "flightdeck", "bin")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	got := ExeLines(filepath.Join(old, "fd-%2fhome%2fu%2fsrc"), nil, want)
	if len(got) != 2 || !strings.Contains(got[1], "자리 밖") {
		t.Fatalf("진짜 옛 자리인데 침묵했다: %v", got)
	}
}

// ★ **자리 비교를 감싸는 세 조각이 한 자리에 있는가.**
//
// 판정(os.SameFile)은 앞 라운드가 sameDir 한 자리로 모았는데, 그것을 감싸는 셋 — Clean ·
// 문자열 지름길 · 그다음의 sameDir — 은 exe.go(부정 방향)와 selfwatch.go(긍정 방향)에 각각
// 있었다. 그 셋이 갈리면 **심볼릭 링크 홈에서만** 두 화면이 다른 답을 내므로, 링크가 없는
// 머신에서는 갈린 채로도 전 시험이 초록이다. 그래서 헬퍼 자체에 표를 붙인다.
//
// 표의 자리 중 셋(끝 슬래시·`.` 성분·**없는 자리**)이 Clean 을 잰다. 특히 마지막 짝은
// **Clean 과 지름길이 stat 앞에 있다**는 계약을 잰다 — 순서가 뒤집히면 없는 자리는 양쪽 stat 이
// 다 실패해 false 가 된다. (호출 횟수는 안 잰다. sameDir 에 이음매를 내야 하는데, 그것을
// 재려고 제품에 주입 자리를 파는 것이 이 후속이 없애려는 모양이다 — 안 잰 축은 안 잰다.)
func TestSameDirAsWrapsThePlaceComparison(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "bigdisk", "cache")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	sameAlias := filepath.Join(root, "samelink")
	otherAlias := filepath.Join(root, "otherlink")
	if err := os.Symlink(real, sameAlias); err != nil {
		t.Skipf("이 판에서 심볼릭 링크를 못 만든다: %v", err)
	}
	if err := os.Symlink(other, otherAlias); err != nil {
		t.Skipf("이 판에서 심볼릭 링크를 못 만든다: %v", err)
	}
	// 만들지 않는다 — 런처가 한 번도 안 돈 머신의 binDir 이 이 모양이다.
	missing := filepath.Join(root, "never-built")

	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"같은 문자열이면 같다", real, real, true},
		{"끝 슬래시는 표기 차이다", real + "/", real, true},
		{"중복 슬래시와 `.` 성분도 표기 차이다", root + "//bigdisk/./cache", real, true},
		{
			// ★ 이 짝이 "Clean·지름길이 stat 앞"이라는 계약을 잰다. 뒤로 밀면 여기가 빨간불이다.
			name: "없는 자리라도 표기만 다르면 같은 자리다 — 여기서 stat 은 양쪽 다 실패한다",
			a:    missing + "/", b: missing, want: true,
		},
		{"한 자리를 두 이름으로 부른 것 — inode 로 알아본다", sameAlias, real, true},
		// ★ 위 갈래의 대조. inode 비교가 "링크면 무조건 같다"로 무너지면 여기가 빨간불이다.
		{"링크라도 가리키는 곳이 다르면 다른 자리다", otherAlias, real, false},
		{"실재하는 서로 다른 자리", other, real, false},
		{"둘 다 없고 문자열도 다르면 — 모르는 것은 같은 것이 아니다", missing, missing + "-2", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameDirAs(tc.a, tc.b); got != tc.want {
				t.Fatalf("sameDirAs(%q, %q) = %v, 원한 것 %v", tc.a, tc.b, got, tc.want)
			}
			// 대칭이 아니면 부정 방향(ExeLines)과 긍정 방향(newSelfWatcher)이 같은 입력에
			// 다른 답을 낼 수 있다 — 이 함수가 존재하는 이유가 정확히 그 갈림이다.
			if got := sameDirAs(tc.b, tc.a); got != tc.want {
				t.Fatalf("인자 순서를 바꾸니 답이 달라졌다: sameDirAs(%q, %q) = %v, 원한 것 %v",
					tc.b, tc.a, got, tc.want)
			}
		})
	}
}

// ★ **거동이 아니라 모양을 잰다 — 이 후속이 고친 것이 모양이기 때문이다.**
//
// 위 표와 두 소비부의 링크 시험은 "지금 답이 맞는가"를 잠근다. 하지만 누군가 호출부에서
// 세 조각을 **다시 인라인**하면(복사·붙여넣기 한 번이면 된다) 그 시험들은 전부 초록인 채로
// 후속이 없앤 모양이 돌아온다 — 갈림이 링크 홈에서만 드러나기 때문이다. 그래서 여기서만
// 파서로 구조를 단정한다: `sameDir` 를 부르는 자리는 `sameDirAs` 하나뿐이다.
//
// 주석은 안 센다(parser 가 버린다) — `sameDir` 를 **말하는** 주석은 두 파일에 여럿이다.
func TestSameDirIsCalledOnlyByItsWrapper(t *testing.T) {
	for _, file := range []string{"exe.go", "selfwatch.go"} {
		t.Run(file, func(t *testing.T) {
			f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
			if err != nil {
				t.Fatalf("%s 를 못 읽었다: %v", file, err)
			}
			var wrapped int
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					id, ok := call.Fun.(*ast.Ident)
					if !ok {
						return true
					}
					switch id.Name {
					case "sameDirAs":
						wrapped++
					case "sameDir":
						if fn.Name.Name != "sameDirAs" {
							t.Errorf("%s 의 %s 가 sameDir 를 직접 부른다 — 감싸는 세 조각"+
								"(Clean·문자열 지름길·inode 비교)이 다시 두 벌이 된다. "+
								"그 갈림은 심볼릭 링크 홈에서만 드러나 시험이 놓친다",
								file, fn.Name.Name)
						}
					}
					return true
				})
			}
			// ★ 대조가 없으면 이 시험은 "축을 통째로 껐다"로도 통과한다.
			if wrapped == 0 {
				t.Errorf("%s 에서 sameDirAs 를 부르는 자리가 사라졌다 — 이 파일의 자리 축이 꺼졌다", file)
			}
		})
	}
}

func TestExeLinesErrorIsNotSilent(t *testing.T) {
	got := ExeLines("", errors.New("readlink /proc/self/exe: permission denied"), testBinDir)
	if len(got) != 1 || !strings.Contains(got[0], "permission denied") {
		t.Fatalf("오류를 삼켰다: %v", got)
	}
	// ★ 자리를 못 읽었는데 '자리 밖'이라 답하면 안 잰 축을 잰 척하는 것이다.
	if strings.Contains(got[0], "자리 밖") {
		t.Fatalf("경로를 모르는데 자리를 판정했다: %v", got)
	}
}

// 경로 안에 우연히 같은 글자가 있어도 **끝에 붙은 것만** 표식이다.
func TestExeLinesOnlyTrimsTheSuffix(t *testing.T) {
	got := ExeLines("/opt/my (deleted) dir/fd", nil, "")
	if len(got) != 1 {
		t.Fatalf("경로 중간의 문자열을 표식으로 읽었다: %v", got)
	}
}

// ★ 이 갈래가 이행 창의 유일한 증상이다.
//
// 자리를 옮겨도 이미 exec 된 프로세스는 옛 inode 를 끝까지 문다. 그 창에서 `(deleted)` 축은
// **언제나** 침묵한다 — 옛 런처를 가진 세션이 옛 자리를 계속 갱신하면 커널이 표식조차 안 붙이고,
// 붙이더라도 doctor 가 부르는 os.Executable 이 떼고 준다(exe.go 의 deletedSuffix ★). 즉 이 줄이
// 없으면 옛 자리를 도는 프로세스가 자기가 옛 자리에 있다는 것을 **어느 축으로도 못 말한다**.
func TestExeLinesAnnouncesStaleLocation(t *testing.T) {
	const old = "/home/u/.local/state/flightdeck/bin/fd"
	cases := []struct {
		name    string
		exe     string
		wantDir string
		lines   int
		stale   bool
		deleted bool
	}{
		{
			name:    "옛 자리를 돌면 말한다",
			exe:     old,
			wantDir: testBinDir,
			lines:   2,
			stale:   true,
		},
		{
			name:    "지금 자리면 침묵한다 — 이름은 안 본다(키 규칙의 주인은 런처다)",
			exe:     testBinDir + "/fd-%2fhome%2fu%2fsrc",
			wantDir: testBinDir,
			lines:   1,
		},
		{
			name:    "자리를 못 계산했으면(HOME 없음) 침묵한다 — 안 잰 축을 잰 척하지 않는다",
			exe:     old,
			wantDir: "",
			lines:   1,
		},
		{
			name:    "공백만 온 것도 못 계산한 것이다",
			exe:     old,
			wantDir: "   ",
			lines:   1,
		},
		{
			name:    "끝 슬래시·중복 슬래시는 같은 자리다 — 표기 차이로 거짓 경보를 내면 안 된다",
			exe:     testBinDir + "/fd-%2fhome%2fu%2fsrc",
			wantDir: "/home/u/.cache//flightdeck/bin/",
			lines:   1,
		},
		{
			name:    "하위 디렉토리는 그 자리가 아니다",
			exe:     testBinDir + "/old/fd-%2fhome%2fu%2fsrc",
			wantDir: testBinDir,
			lines:   2,
			stale:   true,
		},
		{
			// 옛 자리에서 도는 프로세스가 실제로 이 모양이다 — 둘 중 하나로 접으면 답의 절반이 사라진다.
			name:    "교체와 옛 자리는 동시에 참이고 둘 다 나온다",
			exe:     old + deletedSuffix,
			wantDir: testBinDir,
			lines:   3,
			stale:   true,
			deleted: true,
		},
		{
			// 컨테이너 HEALTHCHECK 가 30초마다 이 배치로 doctor 를 돌린다(실측 HOME=/ 라
			// wantDir 이 /.cache/flightdeck/bin 으로 계산되고 그 자리는 영영 안 생긴다).
			// 재기동해도 영원히 같은 파일이라 '옮겨졌다·아무도 갱신 안 한다'로 단정하면 거짓이다 —
			// 줄은 그대로 내되 아래 단정 검사가 문장을 잠근다.
			name:    "컨테이너 이미지 자리 — 사실은 말하되 처방을 단정하지 않는다",
			exe:     "/usr/local/bin/fd",
			wantDir: "/.cache/flightdeck/bin",
			lines:   2,
			stale:   true,
		},
		{
			// README 가 안내하는 개발 경로. go-build 캐시의 일회용 파일에 대고 "옮겨졌다"고
			// 말하면 안 된다.
			name:    "go run 의 임시 자리도 같은 배치다",
			exe:     "/tmp/go-build3721623929/b001/exe/fd",
			wantDir: testBinDir,
			lines:   2,
			stale:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExeLines(tc.exe, nil, tc.wantDir)
			if len(got) != tc.lines {
				t.Fatalf("줄이 %d개다(기대 %d): %v", len(got), tc.lines, got)
			}
			var stale, deleted string
			for _, l := range got {
				if strings.Contains(l, "옛 자리") {
					stale = l
				}
				if strings.Contains(l, "교체됐다") {
					deleted = l
				}
			}
			if tc.stale && stale == "" {
				t.Fatalf("자리 밖인데 아무도 그 말을 안 한다: %v", got)
			}
			if !tc.stale && stale != "" {
				t.Fatalf("자리가 맞는데(혹은 못 쟀는데) 옛 자리라 답했다: %q", stale)
			}
			if tc.deleted && deleted == "" {
				t.Fatalf("교체 축이 자리 축에 먹혔다: %v", got)
			}
			if stale != "" {
				// 사람이 다음에 볼 자리가 그 줄에 있어야 한다. 없으면 "어디로 갔나"를 또 찾는다.
				if want := filepath.Clean(strings.TrimSpace(tc.wantDir)); !strings.Contains(stale, want) {
					t.Fatalf("새 자리를 안 알려 준다: %q", stale)
				}
				if !strings.Contains(stale, "재기동") {
					t.Fatalf("무엇을 해야 하는지가 없다: %q", stale)
				}
				// ★ 런처를 안 거친 배치(컨테이너 ENTRYPOINT·go run·손빌드)에도 **참인 문장만**
				// 단정해야 한다. 그쪽에서 그 자리는 이미지·빌드가 갱신하는 정본이고, 재기동해도
				// 영원히 같은 파일이다.
				if strings.Contains(stale, "아무도 갱신하지 않는") {
					t.Fatalf("전제가 거짓인 배치에까지 단정한다: %q", stale)
				}
				if !strings.Contains(stale, "런처") {
					t.Fatalf("처방에 조건이 없다 — 어느 경우에 재기동이 답인지가 없다: %q", stale)
				}
			}
		})
	}
}

// fd doctor 는 **바이너리 캐시 자리**를 찍고, 그 자리를 ExeLines 에 **실제로 먹인다**.
//
// ★ **위 갈래들은 ExeLines 를 직접 부른다.** 그 함수가 무엇을 내는지는 잠겨 있었는데
// **doctor 가 그 함수에 무엇을 넘기는가**는 아무도 안 쟀다. 2026-08-07 변이 실측: cmds.go 의
// 셋째 인자를 `""` 로 바꿔도, 「바이너리 캐시」 분기를 통째로 지워도 전 패키지가 초록이었다.
// 순수 판정을 아무리 잠가도 그것을 제품에 잇는 **스위치**가 안 잠기면, 이 브랜치의 헤드라인
// 수정이 제품에서 조용히 꺼진 채로 관문이 열린다. 두 줄의 존재 이유가 이행 창에서 **옛 자리를
// 도는 프로세스가 스스로 그것을 말하게 하는 것**이라, 배선이 끊긴 것을 관문이 봐야 한다.
//
// ★ **자리 밖 갈래는 여기서 항상 열린다.** 시험 바이너리는 go-build 캐시(`/tmp/go-buildNNN`)
// 에서 돌고 하네스의 `<state>/bin` 은 만들어지지도 않아 sameDir 이 false 다. 그래서 이 시험은
// 자리 축을 **켜 둔 채로** 배선만 흔든다 — 셋째 인자가 끊기면 ExeLines 가 그 갈래를 안 내는
// 것이 계약이라 줄이 통째로 사라진다.
//
// ★ **「자리 없음」 갈래는 여기서 안 잰다.** env 맵에서 HOME 을 빼면 homeDir(app.go)이
// os.UserHomeDir() 로 떨어지는데 그것은 **프로세스 환경**이라 시험이 못 바꾼다 — 즉 시험이
// 사용자의 진짜 홈을 겨눈다(harness_test.go 의 unpinnedEnv ★ 가 적어 둔 바로 그 사고다).
// 그 갈래의 주인은 순수 함수 층인 env_test.go 의 HOME 부재 시험이다.
func TestDoctorReportsBinCacheDirAndFeedsItToExeLines(t *testing.T) {
	h := newHarness(t)
	code, out := h.run("", "doctor")
	if code != 0 {
		t.Fatalf("doctor 종료코드 %d:\n%s", code, out)
	}
	want, src := BinCacheDir(envOf(h.env), h.home)
	if want == "" {
		t.Fatalf("대조 전제가 깨졌다 — 하네스 환경에서 자리가 안 나왔다(%s)", src)
	}
	// ★ 값과 사유를 **한 줄로** 단정한다(머신 축과 같은 규율 — 따로 찾으면 이웃 줄이 같은
	// 낱말을 갖고 있어 통째로 없어도 통과하는 단정이 된다). 자리 문자열까지 박는 것도 같은
	// 이유다: 아무 자리나 넘겨도 통과하면 "넘긴다"만 재고 "무엇을"은 안 재는 것이다.
	cases := []struct {
		name string
		line string
		why  string
	}{
		{
			name: "자리를 사유와 함께 찍는다",
			line: "  바이너리 캐시 " + want + " (" + src + ")",
			why:  "이 줄이 없으면 응답 캐시와 갈린 사실 자체가 화면에서 사라진다(cmds.go 의 ★)",
		},
		{
			name: "그 자리를 ExeLines 에 먹인다",
			line: "**런처가 짓는 자리 밖**이다(" + want + ")",
			why:  `셋째 인자가 "" 로 끊기면 자리 축이 제품에서 조용히 꺼진다`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(out, tc.line) {
				t.Errorf("doctor 출력에 %q 가 없다 — %s:\n%s", tc.line, tc.why, out)
			}
		})
	}
}
