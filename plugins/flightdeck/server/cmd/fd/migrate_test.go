package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/ledger"
	"github.com/kweiza/flightdeck/internal/store"
)

// 이관 CLI 의 소비자 좌표계는 **stdout 과 파일시스템**이다.
// "DB 를 안 건드린다"는 주장은 mtime·크기로만 확인할 수 있다 —
// 코드를 읽어 "여기서 안 열잖아"라고 단정하면 그 단정은 다음 판올림에서 조용히 거짓이 된다.

// fixtureRoot 는 internal/legacy 의 실물 픽스처다.
// 이 시험이 그것을 다시 쓰는 이유: CLI 는 **경로 두 개를 받아 도는 것**이 계약이고,
// 손으로 만든 임시 트리를 쓰면 실제 원본의 모양(쉼표 paths·비규약 절)을 못 본다.
const fixtureRoot = "../../internal/legacy/testdata/legacy"

func fixtureArgs() (code, docs string) {
	return filepath.Join(fixtureRoot, "code"), filepath.Join(fixtureRoot, "docs")
}

// ★ 예행은 DB 를 **한 바이트도** 안 건드린다.
func TestImportDryRunDoesNotTouchDB(t *testing.T) {
	h := newHarness(t)
	code, docs := fixtureArgs()

	// 대조 전제 ①: DB 파일이 실제로 있고 크기가 0이 아니어야 한다.
	// 파일이 없으면 "안 건드렸다"는 단정이 "만들지도 않았다"와 구분되지 않는다.
	before, err := os.Stat(h.db)
	if err != nil {
		t.Fatalf("전제가 깨졌다 — DB 파일이 없다: %v", err)
	}
	if before.Size() == 0 {
		t.Fatal("전제가 깨졌다 — DB 파일 크기가 0이다")
	}

	rc, out := h.run("", "import", "--from-code", code, "--from-docs", docs,
		"--project", h.project, "--db", h.db)
	if rc != 0 {
		t.Fatalf("예행이 실패했다(rc=%d):\n%s", rc, out)
	}
	mustContain(t, "예행 출력", out,
		"DB 를 한 바이트도 건드리지 않았다", "건수 대조", "--apply")

	after, err := os.Stat(h.db)
	if err != nil {
		t.Fatalf("예행 뒤 DB 파일이 사라졌다: %v", err)
	}
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("예행이 DB 를 건드렸다 — 크기 %d→%d · mtime %s→%s",
			before.Size(), after.Size(), before.ModTime(), after.ModTime())
	}

	// 대조 전제 ②: 이 실행이 정말 무언가 읽긴 했는가.
	// 아무것도 못 읽고 0건으로 끝났다면 "안 건드렸다"는 공허하다.
	if !strings.Contains(out, "queue/items/dash-real-data-render.md") {
		t.Errorf("예행이 원본을 읽지 못한 것으로 보인다 — 거절 목록이 비었다:\n%s", out)
	}
}

// DB 파일이 아예 없을 때도 예행은 만들지 않는다(WAL·마이그레이션·백업 전부).
func TestImportDryRunDoesNotCreateDB(t *testing.T) {
	h := newHarness(t)
	code, docs := fixtureArgs()
	fresh := filepath.Join(t.TempDir(), "nested", "new.db")

	rc, out := h.run("", "import", "--from-code", code, "--from-docs", docs,
		"--project", h.project, "--db", fresh)
	if rc != 0 {
		t.Fatalf("예행이 실패했다(rc=%d):\n%s", rc, out)
	}
	for _, p := range []string{fresh, fresh + "-wal", fresh + "-shm", filepath.Dir(fresh)} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("예행이 %s 를 만들었다 — 예행은 아무것도 안 남겨야 한다", p)
		}
	}
}

// --apply 가 있어야 실제로 쓴다. 그리고 원본은 그대로다.
func TestImportApplyWritesAndLeavesSourceUntouched(t *testing.T) {
	h := newHarness(t)
	code, docs := fixtureArgs()

	// 원본 트리의 지문을 먼저 뜬다. 이관이 원본에 쓰면 되돌리기 근거가 통째로 무너진다.
	beforeSrc := treeFingerprint(t, fixtureRoot)

	h.closeStore() // 같은 파일을 두 프로세스가 여는 상황을 만들지 않는다
	rc, out := h.run("", "import", "--from-code", code, "--from-docs", docs,
		"--project", h.project, "--db", h.db, "--apply")
	if rc != 0 {
		t.Fatalf("적용이 실패했다(rc=%d):\n%s", rc, out)
	}
	mustContain(t, "적용 출력", out, "아래대로 DB 에 넣었다", "넣음: 세션")

	if got := treeFingerprint(t, fixtureRoot); got != beforeSrc {
		t.Error("이관이 원본 트리를 건드렸다 — 되돌리기(DB 삭제 + 재실행)가 성립하지 않게 된다")
	}

	h.openStore()
	items, err := h.st.ListItems(t.Context(), h.project)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("--apply 인데 항목이 하나도 안 들어갔다")
	}
	for _, it := range items {
		if it.ID == "dash-real-data-render" {
			t.Error("쉼표 paths 로 거절한 항목이 DB 에 들어갔다")
		}
	}
}

// --apply 와 --dry-run 을 함께 주면 아무것도 하지 않는다.
// 둘 중 하나를 골라 진행하면 그 선택이 어디에도 안 남는다.
func TestImportRefusesContradictoryFlags(t *testing.T) {
	h := newHarness(t)
	code, _ := fixtureArgs()
	rc, out := h.run("", "import", "--from-code", code, "--apply", "--dry-run",
		"--project", h.project, "--db", h.db)
	if rc == 0 {
		t.Fatalf("모순된 인자를 받았다:\n%s", out)
	}
	mustContain(t, "거절 사유", out, "어느 쪽인지 알 수 없으므로")
}

// 되쓰기는 --out 없이는 안 돈다 — 기본값이 원본이면 언젠가 원본 위에 쓴다.
func TestExportRequiresExplicitOutDir(t *testing.T) {
	h := newHarness(t)
	rc, out := h.run("", "export", "--to-legacy", "--project", h.project, "--db", h.db)
	if rc == 0 {
		t.Fatalf("--out 없이 돌았다:\n%s", out)
	}
	mustContain(t, "거절 사유", out, "원본 위에 쓰지 않기 위해서다")
}

// 되쓰기 출력은 **무엇이 안 돌아오는지**를 반드시 낸다.
func TestExportPrintsRoundTripLosses(t *testing.T) {
	h := newHarness(t)
	code, docs := fixtureArgs()
	h.closeStore()
	if rc, out := h.run("", "import", "--from-code", code, "--from-docs", docs,
		"--project", h.project, "--db", h.db, "--apply"); rc != 0 {
		t.Fatalf("이관 실패(rc=%d):\n%s", rc, out)
	}
	outDir := filepath.Join(t.TempDir(), "legacy-out")
	rc, out := h.run("", "export", "--to-legacy", "--out", outDir,
		"--project", h.project, "--db", h.db)
	if rc != 0 {
		t.Fatalf("되쓰기 실패(rc=%d):\n%s", rc, out)
	}
	mustContain(t, "되쓰기 출력", out,
		"왕복에서 돌아오지 않는 것", "branch", "head", "pid", "status.html")

	if _, err := os.Stat(filepath.Join(outDir, ".claude", "sessions", "7.md")); err != nil {
		t.Errorf("세션 카드가 안 나왔다: %v", err)
	}
	h.openStore()
}

// treeFingerprint 는 트리의 경로·크기·내용 해시를 하나로 접는다.
// mtime 은 넣지 않는다 — 읽기만 해도 atime 정책에 따라 흔들려 거짓 양성이 난다.
func treeFingerprint(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		b.WriteString(p)
		b.WriteByte('\x00')
		b.WriteString(hashOf(data))
		b.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatalf("원본 지문을 못 떴다: %v", err)
	}
	return b.String()
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// 형식을 하나도 안 고르면 거절한다. 둘을 함께 골라도 거절한다 —
// 어느 쪽인지 알 수 없으므로 아무것도 하지 않는다.
func TestExportRequiresExactlyOneFormat(t *testing.T) {
	h := newHarness(t)
	h.closeStore()
	defer h.openStore()

	rc, out := h.run("", "export", "--out", filepath.Join(t.TempDir(), "o"), "--db", h.db)
	if rc == 0 {
		t.Fatalf("형식 없이 통과했다: %s", out)
	}
	mustContain(t, "형식 없음 거절", out, "--to-legacy", "--judgments")

	rc, out = h.run("", "export", "--to-legacy", "--judgments",
		"--out", filepath.Join(t.TempDir(), "o"), "--db", h.db)
	if rc == 0 {
		t.Fatalf("둘을 함께 줬는데 통과했다: %s", out)
	}
	mustContain(t, "형식 둘 거절", out, "함께")
}

// --judgments 는 DB 전량이다. --project 를 명시하면 거절한다 —
// 조용히 무시하면 백업이 반쪽인 걸 아무도 모른다.
func TestExportJudgmentsRejectsExplicitProject(t *testing.T) {
	h := newHarness(t)
	h.closeStore()
	defer h.openStore()

	rc, out := h.run("", "export", "--judgments", "--project", "p",
		"--out", filepath.Join(t.TempDir(), "o"), "--db", h.db)
	if rc == 0 {
		t.Fatalf("--project 를 줬는데 통과했다: %s", out)
	}
	mustContain(t, "--project 거절", out, "전량")
}

// 실제로 내보낸다. FD_PROJECT 는 환경에 있지만 거절 사유가 아니다.
func TestExportJudgmentsWritesFilesAndPrintsLosses(t *testing.T) {
	h := newHarness(t)

	// 판단을 REST 로 넣는다 — 실물 경로다. closeStore 뒤에는 REST 를 못 쓴다.
	rc, out := h.run("", "note", "--kind", "decision", "--body", "왕복 대상 판단")
	if rc != 0 {
		t.Fatalf("판단 등록 실패(rc=%d): %s", rc, out)
	}

	h.closeStore()
	defer h.openStore()

	outDir := filepath.Join(t.TempDir(), "ledger-out")
	rc, out = h.run("", "export", "--judgments", "--out", outDir, "--db", h.db)
	if rc != 0 {
		t.Fatalf("내보내기 실패(rc=%d): %s", rc, out)
	}
	mustContain(t, "내보내기 출력", out,
		"fd export --judgments", "DB 전량", "이 백업이 안 덮는 것", "아웃박스")

	for _, name := range []string{
		"judgments.jsonl", "judgment_links.jsonl", "snapshots.jsonl",
		"machines.jsonl", "projects.jsonl", "sessions.jsonl", "manifest.json",
	} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Errorf("%s 가 안 났다: %v", name, err)
		}
	}
}

// 같은 자리에 두 번 내보내도 --force 가 필요 없다 — 자기 산출물을 알아본다.
//
// ★ rc==0 만 보면 "아무 일도 안 하고 그냥 통과시켰다"와 "실제로 다시 썼다"를
//
//	구분하지 못한다. exported_at 축을 고른 이유: exportJudgments 가 실행마다
//	nowStampString() 으로 새로 찍고 그 값이 manifest.json 에 실린다. 두 h.run
//	호출 사이에는 DB 를 열고 ReadLedger 로 여섯 표를 읽고 파일 일곱 개를
//	tmp→rename 으로 쓰는 실 I/O 가 끼어 있어(같은 프로세스 안에서 순차 실행되지만
//	이 I/O 만으로 마이크로초 여러 개가 흐른다), nowStampString() 의 마이크로초
//	해상도에서 두 값이 우연히 같을 실무적 여지가 없다.
func TestExportJudgmentsRerunNeedsNoForce(t *testing.T) {
	h := newHarness(t)
	rc, out := h.run("", "note", "--kind", "decision", "--body", "판단")
	if rc != 0 {
		t.Fatalf("판단 등록 실패: %s", out)
	}
	h.closeStore()
	defer h.openStore()

	outDir := filepath.Join(t.TempDir(), "ledger-out")
	if rc, out := h.run("", "export", "--judgments", "--out", outDir, "--db", h.db); rc != 0 {
		t.Fatalf("첫 내보내기 실패: %s", out)
	}
	first := readManifestExportedAt(t, outDir)

	rc, out = h.run("", "export", "--judgments", "--out", outDir, "--db", h.db)
	if rc != 0 {
		t.Fatalf("두 번째 내보내기가 거절됐다 — 자기 산출물을 알아봐야 한다(rc=%d): %s", rc, out)
	}
	second := readManifestExportedAt(t, outDir)
	if second == first {
		t.Fatalf("두 번째 실행이 산출물을 실제로 갱신하지 않았다 — manifest.json 의 exported_at 이 그대로다: %s", first)
	}
}

// readManifestExportedAt 은 dir/manifest.json 을 읽어 exported_at 을 낸다.
// TestExportJudgmentsRerunNeedsNoForce 가 "재실행이 rc==0 만 내고 실제로는
// 아무것도 다시 쓰지 않는다" 회귀를 잡는 유일한 좌표계다.
func readManifestExportedAt(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("manifest.json 을 못 읽었다: %v", err)
	}
	var m ledger.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("manifest.json 파싱 실패: %v", err)
	}
	return m.ExportedAt
}

// 내보내기는 DB 를 안 건드린다.
func TestExportJudgmentsLeavesDBUntouched(t *testing.T) {
	h := newHarness(t)
	rc, out := h.run("", "note", "--kind", "decision", "--body", "판단")
	if rc != 0 {
		t.Fatalf("판단 등록 실패: %s", out)
	}
	h.closeStore()
	defer h.openStore()

	before, err := os.Stat(h.db)
	if err != nil {
		t.Fatalf("stat 실패: %v", err)
	}
	if rc, out := h.run("", "export", "--judgments",
		"--out", filepath.Join(t.TempDir(), "o"), "--db", h.db); rc != 0 {
		t.Fatalf("내보내기 실패: %s", out)
	}
	after, err := os.Stat(h.db)
	if err != nil {
		t.Fatalf("stat 실패: %v", err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("DB 가 바뀌었다: %d/%v → %d/%v",
			before.Size(), before.ModTime(), after.Size(), after.ModTime())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ② `ours` 우회의 **음성** 시험 — 되돌릴 수 없는 덮어쓰기를 막는 가드다
// ─────────────────────────────────────────────────────────────────────────────
//
// migrate.go 의 우회는 셋을 곱한다:
//
//	ours := *toJudgments && v.Code == "not-empty" && ledger.IsOurOutput(*outDir)
//
// 그런데 이 셋 중 뒤 둘을 지우고 `ours := *toJudgments` 로 낮춰도 `go test ./cmd/fd/...`
// 가 초록이었다(리뷰가 변이로 재현). 즉 **덮어쓰기를 막는 가드에 시험이 없었다** —
// 자기 산출물을 알아보는 쪽(TestExportJudgmentsRerunNeedsNoForce)만 있었다.
//
// 아래 둘이 그 곱을 각각 잠근다.

// 남의 파일이 있는 자리는 --judgments 라도 거절한다. 자기 산출물이 아니기 때문이다.
func TestExportJudgmentsRefusesSomeoneElsesDirectory(t *testing.T) {
	h := newHarness(t)
	h.closeStore()
	defer h.openStore()

	outDir := filepath.Join(t.TempDir(), "남의자리")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("자리 만들기 실패: %v", err)
	}
	// 우리 매니페스트가 아니다 — IsOurOutput 이 거짓이어야 한다.
	if err := os.WriteFile(filepath.Join(outDir, "누군가의-메모.txt"), []byte("건드리지 마라"), 0o600); err != nil {
		t.Fatalf("남의 파일 쓰기 실패: %v", err)
	}

	rc, out := h.run("", "export", "--judgments", "--out", outDir, "--db", h.db)
	if rc == 0 {
		t.Fatalf("남의 파일이 있는 자리에 --force 없이 내보냈다 — 되돌릴 수 없는 덮어쓰기다:\n%s", out)
	}
	mustContain(t, "거절 문구", out, "되쓰기 거절", "not-empty")
	// 남의 파일이 그대로 있어야 한다.
	if _, err := os.Stat(filepath.Join(outDir, "누군가의-메모.txt")); err != nil {
		t.Errorf("거절했다면서 남의 파일을 건드렸다: %v", err)
	}
}

// git 작업 트리는 --judgments 에 --force 를 줘도 안 뚫린다(ForceAllows 가 안 허용한다).
func TestExportJudgmentsRefusesGitWorktreeEvenWithForce(t *testing.T) {
	h := newHarness(t)
	h.closeStore()
	defer h.openStore()

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("레포 흉내 실패: %v", err)
	}
	outDir := filepath.Join(repo, "ledger-out")

	for _, force := range []bool{false, true} {
		args := []string{"export", "--judgments", "--out", outDir, "--db", h.db}
		if force {
			args = append(args, "--force")
		}
		rc, out := h.run("", args...)
		if rc == 0 {
			t.Fatalf("git 작업 트리 안에 내보냈다(force=%v) — 판단 본문 몇 MB 가 "+
				"레포에 섞여 들어간다:\n%s", force, out)
		}
		mustContain(t, "거절 문구", out, "되쓰기 거절", "git-worktree")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// `fd import --judgments` — 복원 명령이 없어서 오늘 이 산출물로 복원하는 방법은
// Go 시험을 쓰는 것뿐이었다
// ─────────────────────────────────────────────────────────────────────────────

// readManifestCounts 는 dir/manifest.json 의 건수다.
func readManifestCounts(t *testing.T, dir string) ledger.Counts {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("manifest.json 을 못 읽었다: %v", err)
	}
	var m ledger.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("manifest.json 파싱 실패: %v", err)
	}
	return m.Counts
}

// 내보낸 원장이 **빈 DB 에** 그대로 들어간다. 이것이 "무손실"의 실물 증명이다.
func TestImportJudgmentsRestoresIntoEmptyDB(t *testing.T) {
	h := newHarness(t)
	if rc, out := h.run("", "note", "--kind", "decision", "--body", "복원 대상 판단"); rc != 0 {
		t.Fatalf("판단 등록 실패: %s", out)
	}
	h.closeStore()
	defer h.openStore()

	outDir := filepath.Join(t.TempDir(), "ledger-out")
	if rc, out := h.run("", "export", "--judgments", "--out", outDir, "--db", h.db); rc != 0 {
		t.Fatalf("내보내기 실패: %s", out)
	}
	want := readManifestCounts(t, outDir)
	if want.Judgments == 0 || want.Sessions == 0 {
		t.Fatalf("픽스처가 빈약하다 — 이 시험이 아무것도 안 본다: %+v", want)
	}

	// 빈 DB 에 넣는다. 미리 심어 둘 것이 하나도 없어야 한다.
	fresh := filepath.Join(t.TempDir(), "restored.db")
	rc, out := h.run("", "import", "--judgments", "--from", outDir, "--db", fresh, "--apply")
	if rc != 0 {
		t.Fatalf("되쓰기 실패(rc=%d): %s", rc, out)
	}
	mustContain(t, "되쓰기 출력", out, "fd import --judgments", "넣음")

	// 복원본에서 다시 뜬 원장이 원본과 같은 건수여야 한다.
	backDir := filepath.Join(t.TempDir(), "ledger-back")
	if rc, out := h.run("", "export", "--judgments", "--out", backDir, "--db", fresh); rc != 0 {
		t.Fatalf("복원본 내보내기 실패: %s", out)
	}
	if got := readManifestCounts(t, backDir); got != want {
		t.Errorf("복원본의 건수가 원본과 다르다:\n  원본: %+v\n  복원: %+v", want, got)
	}
}

// 예행은 **DB 를 열지도 않는다**. 여는 것만으로 파일이 생기면 "안 건드린다"가 거짓이다.
func TestImportJudgmentsDryRunDoesNotCreateDB(t *testing.T) {
	h := newHarness(t)
	if rc, out := h.run("", "note", "--kind", "decision", "--body", "판단"); rc != 0 {
		t.Fatalf("판단 등록 실패: %s", out)
	}
	h.closeStore()
	defer h.openStore()

	outDir := filepath.Join(t.TempDir(), "ledger-out")
	if rc, out := h.run("", "export", "--judgments", "--out", outDir, "--db", h.db); rc != 0 {
		t.Fatalf("내보내기 실패: %s", out)
	}

	never := filepath.Join(t.TempDir(), "안생겨야한다.db")
	rc, out := h.run("", "import", "--judgments", "--from", outDir, "--db", never)
	if rc != 0 {
		t.Fatalf("예행이 실패했다(rc=%d): %s", rc, out)
	}
	mustContain(t, "예행 출력", out, "예행이다", "--apply")
	if _, err := os.Stat(never); !os.IsNotExist(err) {
		t.Errorf("예행이 DB 파일을 만들었다 — 한 바이트도 안 건드린다는 계약이 거짓이 된다: %v", err)
	}
}

// 세대가 다른 원장은 거절한다 — 지금까지 manifest.schema_version 을 보는 자리가 없었다.
func TestImportJudgmentsRefusesSchemaVersionMismatch(t *testing.T) {
	h := newHarness(t)
	if rc, out := h.run("", "note", "--kind", "decision", "--body", "판단"); rc != 0 {
		t.Fatalf("판단 등록 실패: %s", out)
	}
	h.closeStore()
	defer h.openStore()

	outDir := filepath.Join(t.TempDir(), "ledger-out")
	if rc, out := h.run("", "export", "--judgments", "--out", outDir, "--db", h.db); rc != 0 {
		t.Fatalf("내보내기 실패: %s", out)
	}
	// 매니페스트의 스키마 판만 바꾼다 — 다른 값은 그대로라 건수 대조는 통과한다.
	mp := filepath.Join(outDir, "manifest.json")
	body, err := os.ReadFile(mp)
	if err != nil {
		t.Fatalf("manifest 읽기 실패: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("manifest 파싱 실패: %v", err)
	}
	raw["schema_version"] = store.SchemaVersion + 1
	fixed, _ := json.Marshal(raw)
	if err := os.WriteFile(mp, fixed, 0o600); err != nil {
		t.Fatalf("manifest 쓰기 실패: %v", err)
	}

	fresh := filepath.Join(t.TempDir(), "restored.db")
	rc, out := h.run("", "import", "--judgments", "--from", outDir, "--db", fresh, "--apply")
	if rc == 0 {
		t.Fatalf("세대가 다른 원장을 그대로 되썼다 — 컬럼이 조용히 어긋난다:\n%s", out)
	}
	mustContain(t, "거절 문구", out, "되쓰기 거절", "스키마")
}

// 비어 있지 않은 DB 에는 안 넣는다 — 병합 복원은 다른 연산이다.
func TestImportJudgmentsRefusesNonEmptyDB(t *testing.T) {
	h := newHarness(t)
	if rc, out := h.run("", "note", "--kind", "decision", "--body", "판단"); rc != 0 {
		t.Fatalf("판단 등록 실패: %s", out)
	}
	h.closeStore()
	defer h.openStore()

	outDir := filepath.Join(t.TempDir(), "ledger-out")
	if rc, out := h.run("", "export", "--judgments", "--out", outDir, "--db", h.db); rc != 0 {
		t.Fatalf("내보내기 실패: %s", out)
	}
	// 대상이 원본 자신이다 — 폐포가 비어 있지 않다.
	rc, out := h.run("", "import", "--judgments", "--from", outDir, "--db", h.db, "--apply")
	if rc == 0 {
		t.Fatalf("비어 있지 않은 DB 에 부어넣었다 — 어느 행이 어디서 왔는지 못 가른다:\n%s", out)
	}
	mustContain(t, "거절 문구", out, "되쓰기 거절", "비어 있지 않다", "병합")
}

// 두 이관은 원본도 대상도 다르다 — 섞어 주면 어느 쪽인지 알 수 없다.
func TestImportJudgmentsRefusesContradictoryFlags(t *testing.T) {
	h := newHarness(t)
	code := t.TempDir()

	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"--judgments 인데 --from 이 없다",
			[]string{"import", "--judgments", "--apply"}, "--from 이 비었다"},
		{"--judgments 와 레거시 원본을 함께 줬다",
			[]string{"import", "--judgments", "--from", code, "--from-code", code}, "함께 못 준다"},
		{"--from 만 주고 --judgments 를 안 줬다",
			[]string{"import", "--from", code, "--from-code", code}, "--judgments 와 함께만"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rc, out := h.run("", c.args...)
			if rc == 0 {
				t.Fatalf("모순된 플래그를 받아들였다:\n%s", out)
			}
			mustContain(t, "거절 문구", out, c.want)
		})
	}
}
