package judge

import (
	"strings"
	"testing"
)

// 시험 표의 앞 세 줄은 기존 도구에서 **실행해 확인한 실측 반례**다.
// 그 셋이 여기서 초록이 되는 것이 이 함수를 다시 쓴 이유 전부다.
func TestPathsOverlap(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		// ── 기존 도구의 실측 결함 (전부 "겹침 없음"을 냈다) ──
		{"파일 토큰이 자기 자신과", []string{"Makefile"}, []string{"Makefile"}, true},
		{"파일 토큰이 목록 안의 자기 자신과",
			[]string{".gitleaks.toml"}, []string{".gitleaks.toml", "tools/x.sh"}, true},
		{"디렉토리 목록 vs 파일",
			[]string{"slides/", "tools/"}, []string{"tools/a.sh"}, true},

		// ── 기존 도구가 맞게 판정하던 것 (회귀 방지) ──
		{"디렉토리가 그 아래 파일을", []string{"tools/"}, []string{"tools/work-queue.sh"}, true},

		// ── 성분 단위여야만 맞는 것 (문자열 접두면 틀린다) ──
		{"tool 은 tools 를 안 덮는다", []string{"tool/"}, []string{"tools/"}, false},
		{"tools 는 toolsmith 를 안 덮는다", []string{"tools/"}, []string{"toolsmith/a.go"}, false},
		{"접두가 같아도 성분이 다르면", []string{"services/data-api"}, []string{"services/data-api-v2"}, false},

		// ── 조상 관계는 양방향 ──
		{"깊은 쪽이 먼저 와도", []string{"a/b/c/d.go"}, []string{"a/b"}, true},
		{"얕은 쪽이 먼저 와도", []string{"a/b"}, []string{"a/b/c/d.go"}, true},

		// ── 무관 ──
		{"완전히 다른 가지", []string{"services/"}, []string{"pipeline/"}, false},
		{"같은 뿌리 다른 가지", []string{"a/b/c"}, []string{"a/b/d"}, false},

		// ── 표기 흔들림을 흡수한다 ──
		{"뒤 슬래시 유무", []string{"tools"}, []string{"tools/"}, true},
		{"앞 슬래시", []string{"/tools/a.go"}, []string{"tools/a.go"}, true},
		{"중복 슬래시", []string{"tools//a.go"}, []string{"tools/a.go"}, true},
		{"점 성분", []string{"./tools/a.go"}, []string{"tools/a.go"}, true},

		// ── 빈 입력은 겹치지 않는다 (모두와 겹치는 것으로 접히면 큐가 통째로 막힌다) ──
		{"빈 집합", nil, []string{"tools/"}, false},
		{"빈 문자열 토큰", []string{""}, []string{"tools/"}, false},
		{"공백만", []string{"   "}, []string{"tools/"}, false},
		{"양쪽 다 빔", nil, nil, false},

		// ── 쉼표 구분은 **한 토큰**이다. 조용히 쪼개지 않는다 ──
		//   기존 큐에 쉼표로 넣은 항목 2건이 무검증으로 들어와 조용히 죽어 있었다.
		//   여기서 쪼개 주면 그 입력 오류가 영영 안 보인다 — 입력 검증이 거절해야 할 몫이다.
		{"쉼표 토큰은 안 쪼갠다", []string{"slides/,tools/"}, []string{"tools/a.sh"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PathsOverlap(c.a, c.b); got != c.want {
				t.Errorf("PathsOverlap(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
			}
			// 대칭이어야 한다. 비대칭은 결함의 신호다.
			if got := PathsOverlap(c.b, c.a); got != c.want {
				t.Errorf("대칭 위반: PathsOverlap(%q, %q) = %v, want %v", c.b, c.a, got, c.want)
			}
		})
	}
}

func TestOverlapPairsNamesWhatOverlapped(t *testing.T) {
	// 불리언만으로는 "무엇이 겹쳤나"를 말할 수 없다. 거르지 않고 알리는 것이 규율이므로
	// 알릴 내용이 반드시 있어야 한다.
	got := OverlapPairs(
		[]string{"tools/", "docs/x.md"},
		[]string{"tools/work-queue.sh", "pipeline/"},
	)
	if len(got) != 1 {
		t.Fatalf("쌍 1개를 기대했는데 %d개: %v", len(got), got)
	}
	if got[0] != [2]string{"tools/", "tools/work-queue.sh"} {
		t.Errorf("겹친 쌍이 틀렸다: %v", got[0])
	}
}

func TestOverlapPairsDeduplicates(t *testing.T) {
	got := OverlapPairs([]string{"a/", "a/"}, []string{"a/b.go"})
	if len(got) != 1 {
		t.Errorf("중복 쌍을 접어야 한다: %v", got)
	}
}

// 좌표계 관문 — 잘못된 좌표계의 경로는 '겹침 없음'이 아니라 **사유**를 받아야 한다.
// 사유가 비면 이 판정의 존재 이유가 사라지므로 사유 조각도 함께 단정한다.
func TestJudgePathCoordinate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
		want string // 사유에 반드시 들어 있어야 하는 조각
	}{
		{"드라이브 절대경로(백슬래시)", `C:\repo\x.go`, false, "드라이브 절대경로"},
		{"드라이브 절대경로(슬래시)", `C:/repo/x.go`, false, "드라이브 절대경로"},
		{"소문자 드라이브", `d:\a`, false, "드라이브 절대경로"},
		{"UNC", `\\server\share\x.go`, false, "UNC"},
		{"상대 백슬래시", `internal\api\x.go`, false, "백슬래시"},
		{"백슬래시 하나만", `a\b`, false, "백슬래시"},

		{"정상 상대경로", "internal/api/x.go", true, "슬래시 좌표계"},
		{"정상 절대경로", "/home/a/repo/x.go", true, "슬래시 좌표계"},
		{"디렉토리 토큰", "tools/", true, "슬래시 좌표계"},
		{"파일형 토큰", "Makefile", true, "슬래시 좌표계"},
		{"빈 문자열", "", true, "빈 경로"},
		{"공백만", "   ", true, "빈 경로"},

		// ── 이 축이 아닌 것 (다른 판정의 몫이다) ──
		{".. 는 좌표계 축이 아니다", "../a/b.go", true, "슬래시 좌표계"},
		{"콜론이 있어도 드라이브가 아니면", "a:b", true, "슬래시 좌표계"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := JudgePathCoordinate(c.in)
			if got.OK != c.ok {
				t.Fatalf("JudgePathCoordinate(%q).OK = %v, want %v (사유: %s)",
					c.in, got.OK, c.ok, got.Reason)
			}
			if got.Reason == "" {
				t.Fatal("사유가 비었다 — 사유 없는 판정은 이 패키지에서 금지다")
			}
			if !strings.Contains(got.Reason, c.want) {
				t.Fatalf("사유 %q 에 %q 가 없다", got.Reason, c.want)
			}
		})
	}
}

// 거절 사유는 무엇을 해야 하는지 말해야 한다 — "안 된다"만으로는 사람이 못 고친다.
func TestJudgePathCoordinateReasonCarriesGuidance(t *testing.T) {
	v := JudgePathCoordinate(`C:\repo\x.go`)
	for _, want := range []string{"POSIX", "WSL"} {
		if !strings.Contains(v.Reason, want) {
			t.Errorf("사유에 %q 가 없다: %s", want, v.Reason)
		}
	}
}

// 백슬래시 거절은 POSIX 합법 파일명 하나를 실제로 막는다. 그 사실을 사유가 숨기지 않는지 본다.
func TestJudgePathCoordinateAdmitsItRejectsALegalPOSIXName(t *testing.T) {
	v := JudgePathCoordinate(`a\b`)
	if !strings.Contains(v.Reason, "지원하지 않는다") {
		t.Errorf("사유가 '지원하지 않는다'를 말하지 않는다 — 침묵의 반대는 명시다: %s", v.Reason)
	}
}

func TestFilterPathCoordinate(t *testing.T) {
	kept, rejected := FilterPathCoordinate([]string{
		"internal/api/x.go",
		`C:\repo\y.go`,
		"Makefile",
		`z\w.go`,
	})
	wantKept := []string{"internal/api/x.go", "Makefile"}
	if len(kept) != len(wantKept) {
		t.Fatalf("kept = %q, want %q", kept, wantKept)
	}
	for i := range wantKept {
		if kept[i] != wantKept[i] {
			t.Fatalf("kept = %q, want %q", kept, wantKept)
		}
	}
	if len(rejected) != 2 {
		t.Fatalf("rejected %d건, want 2건: %+v", len(rejected), rejected)
	}
	if rejected[0].Path != `C:\repo\y.go` || rejected[0].Reason == "" {
		t.Errorf("첫 거절이 원본 경로와 사유를 함께 날라야 한다: %+v", rejected[0])
	}
}

func TestFilterPathCoordinateEmptyInput(t *testing.T) {
	kept, rejected := FilterPathCoordinate(nil)
	if len(kept) != 0 || len(rejected) != 0 {
		t.Fatalf("빈 입력에 kept=%q rejected=%+v", kept, rejected)
	}
}

// ★ 이 시험은 결함을 고발하지 않는다. **결정을 지킨다.**
//
// components 는 백슬래시를 성분 구분자로 보지 않는다. 그것이 옳은 이유:
// POSIX 에서 백슬래시는 파일명에 쓸 수 있는 합법 문자라 `a\b` 는 성분 하나짜리 정상
// 파일명이다. 구분자에 넣으면 그 파일이 `a/b` 와 겹친다고 **오탐**한다 —
// 침묵 하나를 오탐 하나와 바꾸는 거래이고, 이 도구에서 오탐은 침묵만큼 나쁘다.
//
// Windows 경로가 여기 도달하지 않게 막는 자리는 이 함수가 아니라 입구의
// JudgePathCoordinate 다(스펙 §3.2 · §4.2).
//
// **이 시험이 깨졌다면 그 결정을 되돌리는 변경을 하고 있는 것이다. 스펙을 먼저 읽어라.**
func TestComponentsDeliberatelyDoesNotSplitBackslash(t *testing.T) {
	got := components(`a\b`)
	if len(got) != 1 || got[0] != `a\b` {
		t.Fatalf(`components("a\b") = %q — 성분 1개 [a\b] 여야 한다. 위 주석을 읽어라`, got)
	}
	if PathsOverlap([]string{`a\b`}, []string{"a/b"}) {
		t.Fatal(`a\b 와 a/b 가 겹친다고 나왔다 — POSIX 합법 파일명을 오탐하고 있다. 위 주석을 읽어라`)
	}
}
