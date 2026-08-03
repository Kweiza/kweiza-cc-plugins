package judge

import "testing"

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
