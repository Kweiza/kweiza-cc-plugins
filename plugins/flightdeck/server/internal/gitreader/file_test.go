package gitreader

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 커밋된 내용을 읽는다 — **작업 트리가 아니라**. 그 차이가 이 함수의 존재 이유다.
func TestFileAtReadsTheCommittedContentNotTheWorkingTree(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, ".flightdeck.yaml", "workspace:\n  members:\n    - path: a\n")
	commit(t, repo, "명부를 커밋한다")

	// 커밋 뒤 트리를 고친다 — 편집기에 열어 둔 반쯤 쓴 명부에 해당한다.
	write(t, repo, ".flightdeck.yaml", "workspace:\n  members:\n    - path: 아직-쓰는-중\n")

	got, err := New(repo).FileAt(ctxT(t), "HEAD", ".flightdeck.yaml")
	if err != nil {
		t.Fatalf("읽기 실패: %v", err)
	}
	if strings.Contains(string(got), "아직-쓰는-중") {
		t.Fatalf("작업 트리를 읽었다 — 커밋된 내용이어야 한다:\n%s", got)
	}
	if !strings.Contains(string(got), "- path: a") {
		t.Fatalf("커밋된 내용이 안 나왔다:\n%s", got)
	}
}

// 「그 ref 에 그 경로가 없다」와 「git 을 못 읽었다」를 **가른다**.
//
// 하나로 접으면 마운트가 빠진 컨테이너에서 명부가 조용히 빈 것이 되고,
// 그 침묵은 자원 배타가 안 서는 것으로만 나타난다.
func TestFileAtSeparatesMissingPathFromRealFailure(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "other.txt", "x\n")
	commit(t, repo, "명부 없이 커밋한다")
	r := New(repo)

	// ① 커밋에 그 경로가 없다 → ErrFileNotInRef
	_, err := r.FileAt(ctxT(t), "HEAD", ".flightdeck.yaml")
	if !errors.Is(err, ErrFileNotInRef) {
		t.Fatalf("경로 부재를 ErrFileNotInRef 로 안 냈다: %v", err)
	}

	// ② 트리에는 있는데 커밋에는 없다 → 이것도 「선언이 없다」다
	write(t, repo, ".flightdeck.yaml", "workspace:\n")
	_, err = r.FileAt(ctxT(t), "HEAD", ".flightdeck.yaml")
	if !errors.Is(err, ErrFileNotInRef) {
		t.Fatalf("미커밋 파일을 ErrFileNotInRef 로 안 냈다: %v", err)
	}

	// ③ ref 자체가 없다 → **진짜 오류다.** 이것을 ②와 접으면 「브랜치를 못 읽었다」가
	//    「명부가 없다」로 둔갑한다.
	_, err = r.FileAt(ctxT(t), "없는-브랜치", ".flightdeck.yaml")
	if err == nil {
		t.Fatal("없는 ref 인데 성공했다")
	}
	if errors.Is(err, ErrFileNotInRef) {
		t.Fatalf("ref 부재를 경로 부재로 접었다: %v", err)
	}

	// ④ 저장소가 아니다 → 진짜 오류다
	base, err2 := filepath.EvalSymlinks(t.TempDir())
	if err2 != nil {
		t.Fatalf("임시 경로 해석 실패: %v", err2)
	}
	_, err = New(base).FileAt(ctxT(t), "HEAD", ".flightdeck.yaml")
	if err == nil || errors.Is(err, ErrFileNotInRef) {
		t.Fatalf("저장소 아닌 경로를 경로 부재로 접었다: %v", err)
	}
}

// 상한을 넘으면 **자르지 않고 거절한다** — 잘린 YAML 은 문법이 맞을 수 있어서,
// 자르면 명부의 뒤쪽 절반이 조용히 사라진 채로 파싱을 통과한다.
func TestFileAtRefusesOversizeInsteadOfTruncating(t *testing.T) {
	repo := newRepo(t)
	big := "# " + strings.Repeat("가", maxFileBytes) + "\n"
	write(t, repo, ".flightdeck.yaml", big)
	commit(t, repo, "큰 파일")

	_, err := New(repo).FileAt(ctxT(t), "HEAD", ".flightdeck.yaml")
	if err == nil {
		t.Fatal("상한을 넘었는데 통과했다")
	}
	if !strings.Contains(err.Error(), "상한") {
		t.Fatalf("사유가 상한을 안 말한다: %v", err)
	}
}

// 경로 가드 — 절대경로·제어문자·빈 값은 git 에 안 넘긴다.
func TestFileAtRefusesHostilePaths(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "a.txt", "a\n")
	commit(t, repo, "c")
	r := New(repo)
	for _, p := range []string{"", "   ", "/etc/passwd", "a\nb", "a\x00b"} {
		if _, err := r.FileAt(ctxT(t), "HEAD", p); err == nil {
			t.Errorf("경로 %q 를 거절해야 한다", p)
		}
	}
	// ref 쪽 가드는 validateRev 가 이미 갖고 있다 — 여기서 그것이 **걸리는지**만 본다.
	if _, err := r.FileAt(ctxT(t), "--help", "a.txt"); err == nil {
		t.Error("ref 가 - 로 시작하는데 통과했다 — git 이 옵션으로 읽는다")
	}
}

// `./` 접두를 벗긴다 — git 이 그것을 "현재 디렉토리 기준"으로 읽어 -C 자리에 따라 답이 달라진다.
func TestFileAtStripsDotSlashPrefix(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "sub/x.yaml", "v: 1\n")
	commit(t, repo, "c")
	got, err := New(repo).FileAt(ctxT(t), "HEAD", "./sub/x.yaml")
	if err != nil {
		t.Fatalf("읽기 실패: %v", err)
	}
	if strings.TrimSpace(string(got)) != "v: 1" {
		t.Fatalf("내용=%q", got)
	}
}

// 브랜치 이름으로도 읽는다 — 명부는 default_branch 에서 읽히므로 이 갈래가 실사용 경로다.
func TestFileAtReadsByBranchName(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, ".flightdeck.yaml", "workspace:\n  members:\n    - path: m\n")
	commit(t, repo, "c")
	// 트리를 지워도 오브젝트 DB 에서 답한다 — 컨테이너가 읽기 전용 마운트인 이유다.
	if err := os.Remove(filepath.Join(repo, ".flightdeck.yaml")); err != nil {
		t.Fatalf("삭제 실패: %v", err)
	}
	got, err := New(repo).FileAt(ctxT(t), "main", ".flightdeck.yaml")
	if err != nil {
		t.Fatalf("브랜치로 읽기 실패: %v", err)
	}
	if !strings.Contains(string(got), "- path: m") {
		t.Fatalf("내용=%q", got)
	}
}
