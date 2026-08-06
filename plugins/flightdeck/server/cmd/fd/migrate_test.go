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
//   구분하지 못한다. exported_at 축을 고른 이유: exportJudgments 가 실행마다
//   nowStampString() 으로 새로 찍고 그 값이 manifest.json 에 실린다. 두 h.run
//   호출 사이에는 DB 를 열고 ReadLedger 로 여섯 표를 읽고 파일 일곱 개를
//   tmp→rename 으로 쓰는 실 I/O 가 끼어 있어(같은 프로세스 안에서 순차 실행되지만
//   이 I/O 만으로 마이크로초 여러 개가 흐른다), nowStampString() 의 마이크로초
//   해상도에서 두 값이 우연히 같을 실무적 여지가 없다.
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
