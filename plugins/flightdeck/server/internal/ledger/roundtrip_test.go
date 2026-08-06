package ledger_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kweiza/flightdeck/internal/ledger"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// ★ 이 시험이 "무손실"의 유일한 증명이다.
// DB → 원장 읽기 → JSONL → 파일 → 되읽기 → 빈 DB → 다시 원장 읽기 가 원본과 같아야 한다.
// 이것이 없으면 이 저장소가 만든 것은 "복원해 본 적 없는 백업"이다.
func TestLedgerSurvivesFullRoundTrip(t *testing.T) {
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	srcPath := filepath.Join(t.TempDir(), "src.db")
	src, err := store.OpenWithLogger(srcPath, quiet)
	if err != nil {
		t.Fatalf("원본 DB 열기 실패: %v", err)
	}
	seedLedgerFixture(t, src)

	want, err := src.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("원장 읽기 실패: %v", err)
	}
	if len(want.Judgments) < 3 || len(want.Links) < 2 || len(want.Snapshots) < 1 {
		t.Fatalf("픽스처가 빈약하다 — 이 시험이 아무것도 안 본다: %d/%d/%d",
			len(want.Judgments), len(want.Links), len(want.Snapshots))
	}
	if err := src.Close(); err != nil {
		t.Fatalf("원본 닫기 실패: %v", err)
	}

	// 내보낸다.
	files, m, err := ledger.Encode(want, store.SchemaVersion, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "out")
	if _, err := ledger.Write(files, dir); err != nil {
		t.Fatalf("Write 실패: %v", err)
	}

	// 되읽는다.
	got, gotM, err := ledger.Read(dir)
	if err != nil {
		t.Fatalf("Read 실패: %v", err)
	}
	if gotM.SchemaVersion != m.SchemaVersion {
		t.Errorf("스키마 버전이 달라졌다: %d → %d", m.SchemaVersion, gotM.SchemaVersion)
	}

	// ★ 완전히 빈 DB 에 되쓴다. seed 를 부르지 않는다 — Task 10 이 폐포를 닫았으므로
	//   machine·project·session 까지 원장이 다 갖고 와야 한다. 미리 심어 줘야 통과한다면
	//   그것은 폐포가 안 닫힌 것이고, 이 시험의 존재 이유가 바로 그것을 잡는 것이다.
	dstPath := filepath.Join(t.TempDir(), "dst.db")
	dst, err := store.OpenWithLogger(dstPath, quiet)
	if err != nil {
		t.Fatalf("복원 DB 열기 실패: %v", err)
	}
	defer dst.Close()

	if err := dst.WriteLedger(ctx, got); err != nil {
		t.Fatalf("빈 DB 되쓰기 실패 — 폐포가 안 닫혔다: %v", err)
	}

	final, err := dst.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("복원본 읽기 실패: %v", err)
	}
	if !reflect.DeepEqual(want, final) {
		t.Fatalf("왕복에서 원장이 달라졌다:\n원본 %+v\n복원 %+v", want, final)
	}
}

// 복원한 DB 에서 전문검색이 실제로 동작하는지 본다.
// judgment_fts 는 내보내지 않는데, 그것을 손실 0이라고 주장하는 근거가
// "AFTER INSERT 트리거가 다시 채운다" 하나뿐이다 — 그 주장을 여기서 실측한다.
func TestRestoredDBHasWorkingFullTextSearch(t *testing.T) {
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	src, err := store.OpenWithLogger(filepath.Join(t.TempDir(), "src.db"), quiet)
	if err != nil {
		t.Fatalf("원본 열기 실패: %v", err)
	}
	seedLedgerFixture(t, src)
	dump, err := src.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("원장 읽기 실패: %v", err)
	}
	src.Close()

	dst, err := store.OpenWithLogger(filepath.Join(t.TempDir(), "dst.db"), quiet)
	if err != nil {
		t.Fatalf("복원 DB 열기 실패: %v", err)
	}
	defer dst.Close()
	// 여기도 seed 를 안 부른다 — 폐포가 닫혔으므로 원장만으로 복원된다.
	if err := dst.WriteLedger(ctx, dump); err != nil {
		t.Fatalf("WriteLedger 실패: %v", err)
	}

	hits, err := dst.SearchJudgments(ctx, "p", "고유낱말", 10)
	if err != nil {
		t.Fatalf("복원본 검색 실패: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("복원 후 전문검색이 아무것도 못 찾는다 — judgment_fts 가 안 채워졌다")
	}
}

// seedLedgerRefs 는 원장 밖 표(project·session·machine)를 만든다.
func seedLedgerRefs(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	if err := s.UpsertProject(ctx, model.Project{ID: "p", Path: "/repo/p"}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m1", Hostname: "dev"}); err != nil {
		t.Fatalf("머신 등록 실패: %v", err)
	}
	if _, _, err := s.OpenSession(ctx, "p", "m1", "/w/cc1", "cc1", ""); err != nil {
		t.Fatalf("세션 등록 실패: %v", err)
	}
}

// seedLedgerFixture 는 왕복이 실제로 무언가를 보게 하는 데이터를 넣는다 —
// NULL 과 값, supersedes, 여러 target_kind, 전문검색용 고유 낱말.
func seedLedgerFixture(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	seedLedgerRefs(t, s)

	sess, _, err := s.OpenSession(ctx, "p", "m1", "/w/cc1", "cc1", "")
	if err != nil {
		t.Fatalf("세션 재개 실패: %v", err)
	}
	if err := s.AddItem(ctx, model.Item{
		Project: "p", ID: "i1", Title: "i1", Body: "본문", Paths: []string{"services/"},
	}); err != nil {
		t.Fatalf("항목 등록 실패: %v", err)
	}

	// ① 세션·제목이 있고 링크가 둘(종류가 다르다)
	first, err := s.AddJudgment(ctx, model.Judgment{
		Project: "p", SessionID: sess.ID, Kind: model.JudgmentDecision,
		Title: "결정", Body: "고유낱말 이 들어간 본문",
		Links: []model.JudgmentLink{
			{TargetKind: "item", TargetID: "i1"},
			{TargetKind: "commit", TargetID: "deadbeef"},
		},
	})
	if err != nil {
		t.Fatalf("판단① 저장 실패: %v", err)
	}

	// ② supersedes 가 걸린 정정
	if _, err := s.AddJudgment(ctx, model.Judgment{
		Project: "p", SessionID: sess.ID, Kind: model.JudgmentDecision,
		Title: "정정", Body: "앞 판단을 대체한다", Supersedes: first.ID,
	}); err != nil {
		t.Fatalf("판단② 저장 실패: %v", err)
	}

	// ③ project·session·title 이 전부 NULL 인 판단 — 포인터가 아니면 여기서 티가 난다
	if _, err := s.AddJudgment(ctx, model.Judgment{
		Kind: model.JudgmentDecision, Body: "좌표 없는 판단",
	}); err != nil {
		t.Fatalf("판단③ 저장 실패: %v", err)
	}

	// 스냅숏 둘 — evidence 가 있는 것과 없는 것
	if err := s.PutSnapshot(ctx, model.Snapshot{
		Project: "p", Key: "manual-key", Value: "12", Method: model.SnapshotManual,
		Evidence: "손으로 셌다", InputDigest: "abc",
	}); err != nil {
		t.Fatalf("스냅숏① 저장 실패: %v", err)
	}
	if err := s.PutSnapshot(ctx, model.Snapshot{
		Project: "p", Key: "cmd-key", Value: "7", Method: model.SnapshotCommand,
	}); err != nil {
		t.Fatalf("스냅숏② 저장 실패: %v", err)
	}
}
