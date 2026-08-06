package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 이 파일이 지키는 것은 하나다: **아무도 안 도는 옛 바이너리 자리를 doctor 가 말한다.**
//
// ★ 왜 순수 함수 시험(env_test.go 의 LegacyBinDirs 표)으로 부족한가. 그 표는 "어느 자리를
// 후보로 삼는가"만 잰다. 이 항목의 결함은 후보를 못 골라서 난 것이 아니라 **그 목록을 화면에
// 잇는 줄이 아예 없어서** 났다 — §7 이 "그 사실은 doctor 가 말로 찍는다"고 적었는데 거짓이었고,
// ExeLines 의 "자리 밖" 줄은 지금 도는 프로세스만 재서 아무도 안 도는 자리의 44.2MB 는 어느
// 화면에도 안 떴다. 앞 브랜치가 같은 모양의 결함(순수 판정은 다 잠겼는데 배선만 안 잠겼다)에
// 뮤테이션 여덟 개를 뚫렸다. 그래서 배선을 좌표계로 삼는 시험이 여기 따로 있다.
//
// ★ **지우는 시험은 없다.** doctor 는 말만 한다 — 이관도 삭제도 안 한다는 판정이 그대로다
// (env.go 의 LegacyBinDirs ★). 아래 시험들이 "파일이 그대로 남았는가"를 함께 단정하는 것은
// 그 판정을 지키는 자리다.

// seedLegacyBin 은 옛 자리 하나를 만들고 주어진 크기의 산출물을 놓는다.
//
// 이름이 `fd` 인 것이 요점이다 — 옛 런처가 짓던 이름에는 접두가 없다(개정 전 bin/fd 의
// `bin="$state/bin/fd"`). 여기서 `fd-…` 로 씨를 뿌리면 legacyBinPrefix 를 bincache.go 의
// `fd-` 로 좁히는 뮤테이션이 초록으로 지나간다 — 정작 실물 잔존 두 벌이 안 세어지는데.
func seedLegacyBin(t *testing.T, dir, name string, size int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("옛 자리를 못 만들었다(%s): %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o755); err != nil {
		t.Fatalf("옛 산출물을 못 놨다(%s/%s): %v", dir, name, err)
	}
}

// legacyBinHome 은 하네스의 가짜 홈 아래 **진짜** 옛 자리 후보다.
//
// 리터럴로 적지 않고 LegacyBinDirs 에서 받아 온다 — 후보 계산의 주인이 하나라는 것이
// 이 축의 규율이고, 여기 사본을 두면 목록이 바뀌는 날 시험만 옛 자리를 겨눈다.
func legacyBinHome(t *testing.T, h *harness) string {
	t.Helper()
	binDir, _ := BinCacheDir(envOf(h.env), h.home)
	want := filepath.Join(h.home, ".local", "state", "flightdeck", "bin")
	for _, d := range LegacyBinDirs(envOf(h.env), h.home, binDir) {
		if d == want {
			return d
		}
	}
	t.Fatalf("대조 전제가 깨졌다 — 하네스 환경에서 %s 가 후보에 없다", want)
	return ""
}

// 옛 자리에 산출물이 남아 있으면 doctor 가 **자리·개수·크기**를 찍는다.
func TestDoctorReportsAbandonedBinCacheDirs(t *testing.T) {
	h := newHarness(t)
	legacy := legacyBinHome(t, h)
	// 2026-08-07 이 머신 실측 잔존과 같은 자릿수로 둔다 — humanBytes 의 MB 갈래를 탄다.
	seedLegacyBin(t, legacy, "fd", 1_500_000)
	// ★ 접두를 가진 **디렉토리**를 하나 같이 놓는다. 세면 개수가 2가 되고 크기에 4KB 가
	// 얹힌다 — 사람이 지울 대상과 부피를 오판하고, 재귀로 훑는 쪽으로 번지는 첫 걸음이다.
	// (런처는 여기에 디렉토리를 안 짓는다. 그래서 이것은 사람이 둔 것이고 남의 것일 수 있다.)
	if err := os.MkdirAll(filepath.Join(legacy, "fd-내가만든디렉토리"), 0o755); err != nil {
		t.Fatalf("디렉토리를 못 만들었다: %v", err)
	}

	code, out := h.run("", "doctor")
	if code != 0 {
		t.Fatalf("doctor 종료코드 %d:\n%s", code, out)
	}

	// ★ 자리·개수·크기·처방을 **한 줄로** 단정한다. 따로 찾으면 공허해진다 — 경로는
	// 위 응답 캐시 줄에도 있고 "1개"는 아웃박스 줄에도 있어서, 이 줄이 통째로 없어도
	// 통과하는 단정이 된다(머신 축 시험이 같은 함정을 같은 이유로 피했다).
	line := "  ! 옛 바이너리 자리 " + legacy + " — fd 1개 · 1.5MB(아무도 안 쓴다. 지우려면 사람이 지운다)"
	if !strings.Contains(out, line) {
		t.Errorf("doctor 에 옛 바이너리 자리 줄(%q)이 없다 — 아무도 안 도는 자리의 파일은 "+
			"ExeLines 의 프로세스 축이 원리적으로 못 본다:\n%s", line, out)
	}

	// ★ **말만 한다.** 지우는 코드는 앞 브랜치가 명시로 기각했다(§11 — 잃어도 다시 만들면
	// 되는 것에 재생 기구를 안 만든다). 이 단정이 그 기각을 지키는 자리다.
	if _, err := os.Stat(filepath.Join(legacy, "fd")); err != nil {
		t.Errorf("doctor 가 옛 산출물을 건드렸다 — 이 명령은 읽기 전용이어야 한다: %v", err)
	}

	// ★ 이 줄의 정직성은 **바로 뒤 문장**이 떠받친다. 후보가 채널 환경에서 오므로 다른
	// 채널의 자리는 여기서 안 보이고, 그 한계를 화면이 말해야 "갈린 보고가 각자 옳다"가 선다.
	if !strings.Contains(out, "옛 자리 탐색(아웃박스 큐 · 바이너리 자리)은 이 채널이 계산할 수 있는") {
		t.Errorf("한계 문장이 바이너리 자리를 이름으로 안 댄다 — 침묵이 '깨끗하다'로 읽힌다:\n%s", out)
	}
}

// 말할 것이 없으면 **아무 줄도 안 낸다.** 잡음이 된 줄은 정작 참인 날 아무도 안 읽는다.
func TestDoctorIsSilentWhenNothingIsLeftInAbandonedBinDirs(t *testing.T) {
	cases := []struct {
		name string
		// seed 는 옛 자리를 어떤 상태로 만들 것인가. nil 이면 자리 자체를 안 만든다.
		seed func(t *testing.T, dir string)
		why  string
	}{
		{
			name: "자리가 아예 없다",
			seed: nil,
			why:  "이 판을 안 거친 머신에서 매 doctor 마다 세 줄이 뜬다",
		},
		{
			name: "자리는 있는데 비었다",
			seed: func(t *testing.T, dir string) {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("옛 자리를 못 만들었다: %v", err)
				}
			},
			why: "사람이 이미 지운 뒤에도 같은 줄이 계속 뜬다",
		},
		{
			name: "fd 가 아닌 파일만 있다",
			seed: func(t *testing.T, dir string) { seedLegacyBin(t, dir, "notes.txt", 10) },
			why:  "남의 파일을 '지워라'로 가리키게 된다 — 그 자리를 나눠 쓸 수 있다",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			legacy := legacyBinHome(t, h)
			if c.seed != nil {
				c.seed(t, legacy)
			}
			code, out := h.run("", "doctor")
			if code != 0 {
				t.Fatalf("doctor 종료코드 %d:\n%s", code, out)
			}
			// "! 옛 바이너리 자리 " 로 찾는다 — 바로 아래 한계 문장에도 "바이너리 자리" 가
			// 들어 있어서(그 문장은 **언제나** 뜬다) 짧게 찾으면 이 단정이 늘 참이 된다.
			if strings.Contains(out, "! 옛 바이너리 자리 ") {
				t.Errorf("말할 것이 없는데 줄이 떴다 — %s:\n%s", c.why, out)
			}
		})
	}
}

// **지금 쓰는 자리를 옛 자리로 찍지 않는다.**
//
// ★ 이 시험이 잠그는 것은 판정이 아니라 **배선의 셋째 인자**다. 순수 함수 표에도 "목표와 같은
// 자리는 뺀다" 갈래가 있지만, 그 목표를 doctor 가 실제로 넘기는지는 거기서 원리적으로 못 본다 —
// `a.binDir` 을 `""` 로 바꾸는 뮤테이션이 표 시험에는 안 걸린다. 앞 브랜치가 정확히 이 모양의
// 구멍(순수 판정은 다 잠겼는데 제품에 잇는 한 줄이 안 잠겼다)에 여덟 번 뚫렸다.
//
// ★ 하네스 기본 env 로는 이 갈래가 안 열린다. FD_STATE_DIR 가 고정돼 있어 목표가
// `<state>/bin` 인데 그 자리는 애초에 후보 목록에 없다. 축을 푸는 정식 갈래(unpinnedEnv)를
// 써야 목표가 고정 자리(`~/.cache/…`)와 같아져 **뺄 것이 생긴다.**
func TestDoctorNeverCallsTheCurrentBinPlaceAbandoned(t *testing.T) {
	h := newHarness(t)
	env := h.unpinnedEnv(nil)
	current, src := BinCacheDir(envOf(env), h.home)
	if current == "" {
		t.Fatalf("대조 전제가 깨졌다 — 축을 푼 환경에서 자리가 안 나왔다(%s)", src)
	}
	if !strings.HasPrefix(current, h.home) {
		t.Fatalf("대조 전제가 깨졌다 — 자리(%s)가 가짜 홈(%s) 밖이다", current, h.home)
	}
	legacy := filepath.Join(h.home, ".local", "state", "flightdeck", "bin")
	seedLegacyBin(t, current, "fd", 1_500_000) // 지금 자리 — 말하면 안 된다
	seedLegacyBin(t, legacy, "fd", 1_500_000)  // 옛 자리 — 말해야 한다

	code, out := h.runEnv(env, "", "doctor")
	if code != 0 {
		t.Fatalf("doctor 종료코드 %d:\n%s", code, out)
	}
	// ★ 대조부터 — 옛 자리는 실제로 떴는가. 이것 없이는 아래 단정이
	// "이 갈래가 통째로 꺼져 있다"로도 통과한다.
	if !strings.Contains(out, "! 옛 바이너리 자리 "+legacy+" — ") {
		t.Fatalf("대조가 깨졌다 — 옛 자리(%s)조차 안 떴다:\n%s", legacy, out)
	}
	if strings.Contains(out, "! 옛 바이너리 자리 "+current+" — ") {
		t.Errorf("지금 쓰는 자리(%s)를 옛 자리로 찍었다 — 그 줄은 그 자리에서 곧바로 거짓이고, "+
			"사람에게 지금 쓰는 바이너리를 지우라고 말한다:\n%s", current, out)
	}
}

// 셀 수 없으면 **그 사실을 말한다.** 침묵과 0개를 가른다.
//
// ★ 권한 0000 으로 만드는 방법은 안 쓴다 — root 로 도는 CI 에서 조용히 통과한다
// (hook_banner_legacy_test.go 가 같은 이유로 같은 선택을 했다). 실물에서 이 갈래를 여는 것은
// 권한이고, 여기서는 **같은 분기**(ReadDir 가 '없다'가 아닌 오류를 낸다)를 자리에 디렉토리
// 대신 파일을 놓아 이식성 있게 연다.
func TestDoctorSaysWhenAnAbandonedBinDirCannotBeCounted(t *testing.T) {
	h := newHarness(t)
	legacy := legacyBinHome(t, h)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatalf("부모를 못 만들었다: %v", err)
	}
	if err := os.WriteFile(legacy, []byte("디렉토리가 아니다\n"), 0o600); err != nil {
		t.Fatalf("자리를 파일로 못 만들었다: %v", err)
	}
	// 대조 전제: 정말 '없다'가 아니라 '못 읽는다'인가. 이것 없이는 아래 단정이
	// "그냥 조용하다"와 구별되지 않는다.
	if _, err := os.ReadDir(legacy); err == nil || os.IsNotExist(err) {
		t.Fatalf("전제가 깨졌다 — 읽기가 안 걸렸다(err=%v)", err)
	}

	code, out := h.run("", "doctor")
	if code != 0 {
		t.Fatalf("doctor 종료코드 %d:\n%s", code, out)
	}
	if !strings.Contains(out, "  ! 옛 바이너리 자리 "+legacy+" — 세다 걸렸다: ") {
		t.Errorf("셀 수 없는 자리(%s)를 doctor 가 안 말한다 — 침묵은 '0개'로, "+
			"0개는 '깨끗하다'로 읽힌다:\n%s", legacy, out)
	}
}
