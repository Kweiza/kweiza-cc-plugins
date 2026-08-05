package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// 경로 실재 축의 `unknown` 이 **왜** 못 읽었는지를 날라야 한다.
//
// ★ 이 시험의 존재 이유는 실측이다. fd-item-path-project-mismatch-hint 의 전수 리뷰(D2)가
// 원인 셋이 **바이트 단위로 같은 문장**을 낸다는 것을 쟀다:
//
//	· 프로젝트 루트가 통째로 없다   → 고칠 것은 프로젝트 등록이다
//	· 경로가 ".." 로 루트 밖이다    → 고칠 것은 항목의 경로다(입력 오류)
//	· 권한 거부 등 stat 오류        → 고칠 것은 파일 권한이다
//
// 셋은 고칠 사람도 고칠 자리도 다른데 화면이 같은 말을 했다. 관측 계층은 errno 를 손에
// 쥐고 있었지만 판정 계층으로 넘길 통로가 없어 그 자리에서 버렸다.
//
// 그래서 이 시험의 본 단정은 문구 일치가 아니라 **셋이 서로 다른가**다.
// 문구만 단정하면 다음 사람이 셋을 같은 문장으로 되돌려도 하나씩은 통과한다.
func TestUnknownCarriesItsCause(t *testing.T) {
	svc, _ := newSvc(t)

	// ── ① 루트가 통째로 없다.
	gone := model.Project{ID: "gone", Path: filepath.Join(t.TempDir(), "없는-루트")}
	vGone := svc.checkItemPaths(t.Context(), gone, []string{"a/b.go", "c/d.go"})

	// ── ② 경로가 ".." 로 루트 밖이다.
	live := model.Project{ID: "live", Path: t.TempDir()}
	vEscape := svc.checkItemPaths(t.Context(), live, []string{"../밖에.go"})

	// ── ③ stat 오류(권한 거부).
	locked := t.TempDir()
	inner := filepath.Join(locked, "잠긴")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatalf("디렉토리 생성 실패: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inner, "x.go"), []byte("x"), 0o644); err != nil {
		t.Fatalf("파일 생성 실패: %v", err)
	}
	if err := os.Chmod(inner, 0o000); err != nil {
		t.Fatalf("권한 변경 실패: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(inner, 0o755) }) // 안 되돌리면 TempDir 정리가 실패한다

	for _, v := range []*judge.ItemPathVerdict{vGone, vEscape} {
		if v.Kind != judge.KindUnknown {
			t.Fatalf("unknown 이어야 한다: kind=%s summary=%s", v.Kind, v.Summary)
		}
	}

	// ── 단정 ①: 루트가 없으면 **경로가 아니라 레포**를 지목해야 한다.
	//
	// 실패 시나리오가 이 모양이다 — 등록 경로가 옮겨지면 그 프로젝트의 모든 pick 이
	// 영구히 경로 목록만 낸다. 화면은 경로를 지목하는데 실제 고장은 레포라
	// 운영자가 엉뚱한 것을 고치러 간다.
	if !strings.Contains(vGone.Summary, "전부 같은 이유다") ||
		!strings.Contains(vGone.Summary, "프로젝트 루트를 열 수 없다") {
		t.Errorf("루트 결손이 레포를 안 지목한다:\n%s", vGone.Summary)
	}
	if strings.Contains(vGone.Summary, "a/b.go") {
		t.Errorf("원인이 하나인데 경로를 나열했다 — 화면이 엉뚱한 것을 지목한다:\n%s", vGone.Summary)
	}

	// ── 단정 ②: ".." 는 **입력 오류**라는 것이 드러나야 한다.
	if !strings.Contains(vEscape.Summary, "루트 밖") {
		t.Errorf("'..' 가 입력 오류로 안 읽힌다:\n%s", vEscape.Summary)
	}

	// ── 단정 ③: stat 오류는 errno 를 날라야 한다.
	// root 는 권한을 무시하므로 그 환경에서는 이 갈래를 만들 수 없다.
	if os.Geteuid() == 0 {
		t.Log("root 로 도는 중이라 권한 거부 갈래는 건너뛴다")
	} else {
		vPerm := svc.checkItemPaths(t.Context(), model.Project{ID: "locked", Path: locked},
			[]string{"잠긴/x.go"})
		if vPerm.Kind != judge.KindUnknown {
			t.Fatalf("권한 거부가 unknown 이 아니다: kind=%s summary=%s", vPerm.Kind, vPerm.Summary)
		}
		if !strings.Contains(vPerm.Summary, "permission denied") {
			t.Errorf("errno 가 문장에 없다:\n%s", vPerm.Summary)
		}
		// 루트 절대경로가 새면 안 된다 — *fs.PathError 를 그대로 쓰면 샌다.
		if strings.Contains(vPerm.Summary, locked) {
			t.Errorf("사유에 루트 절대경로가 샜다:\n%s", vPerm.Summary)
		}
		// ── 본 단정: 셋이 서로 다른 문장인가.
		all := []string{vGone.Summary, vEscape.Summary, vPerm.Summary}
		for i := range all {
			for j := i + 1; j < len(all); j++ {
				if all[i] == all[j] {
					t.Errorf("원인이 다른데 문장이 같다(%d==%d) — D2 가 고발한 그 상태다:\n%s", i, j, all[i])
				}
			}
		}
	}
}

// 원인이 갈리면 경로별로 내야 한다 — 그때는 경로가 진짜 판별자다.
func TestUnknownListsPerPathWhenCausesDiffer(t *testing.T) {
	in := judge.ItemPathInput{
		Project: "p",
		Paths:   []string{"안/있다.go", "../밖.go"},
		Here: map[string]judge.PathPresence{
			"안/있다.go": judge.PathUnknown,
			"../밖.go": judge.PathUnknown,
		},
		UnknownReason: map[string]string{
			"안/있다.go": "permission denied",
			"../밖.go": "'..' 로 프로젝트 루트 밖을 가리킨다 — 관측하지 않았다",
		},
	}
	v := judge.ClassifyItemPaths(in)
	if v.Kind != judge.KindUnknown {
		t.Fatalf("unknown 이어야 한다: %s", v.Kind)
	}
	if strings.Contains(v.Summary, "전부 같은 이유다") {
		t.Errorf("원인이 갈렸는데 하나로 접었다:\n%s", v.Summary)
	}
	for _, want := range []string{"안/있다.go (permission denied)", "../밖.go ("} {
		if !strings.Contains(v.Summary, want) {
			t.Errorf("경로별 사유에 %q 가 없다:\n%s", want, v.Summary)
		}
	}
}

// 사유를 하나도 못 받으면 옛 거동(경로 나열)으로 떨어진다 — 지어내지 않는다.
func TestUnknownWithoutReasonsFallsBackToPathList(t *testing.T) {
	in := judge.ItemPathInput{
		Project: "p",
		Paths:   []string{"a.go", "b.go"},
		Here: map[string]judge.PathPresence{
			"a.go": judge.PathUnknown,
			"b.go": judge.PathUnknown,
		},
	}
	v := judge.ClassifyItemPaths(in)
	if v.Kind != judge.KindUnknown {
		t.Fatalf("unknown 이어야 한다: %s", v.Kind)
	}
	if strings.Contains(v.Summary, "전부 같은 이유다") || strings.Contains(v.Summary, "(") {
		t.Errorf("사유가 없는데 지어냈다:\n%s", v.Summary)
	}
	for _, want := range []string{"a.go", "b.go"} {
		if !strings.Contains(v.Summary, want) {
			t.Errorf("옛 거동인 경로 나열이 깨졌다 — %q 가 없다:\n%s", want, v.Summary)
		}
	}
}
