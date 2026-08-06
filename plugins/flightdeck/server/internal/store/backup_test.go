package store

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 원장 읽기는 DB 전량이다 — 프로젝트로 안 거른다.
// project 가 NULL 인 판단이 스키마상 가능하고, WHERE project = ? 는 그런 행을 절대 못 잡는다.
func TestReadLedgerCoversAllProjectsAndNullProject(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p1")
	seed(t, s, "p2")

	linkJudgment(t, s, "p1", model.JudgmentDecision, "i1")
	linkJudgment(t, s, "p2", model.JudgmentAsk, "i2")
	// project 를 비우면 nullStr 이 NULL 로 넣는다. FK 를 아예 안 탄다.
	if _, err := s.AddJudgment(ctx, model.Judgment{Kind: model.JudgmentNow, Body: "프로젝트 없는 판단"}); err != nil {
		t.Fatalf("project 없는 판단 저장 실패: %v", err)
	}

	d, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}
	if len(d.Judgments) != 3 {
		t.Fatalf("판단이 %d건이다 — 3건을 기대한다", len(d.Judgments))
	}
	var nullProject int
	for _, j := range d.Judgments {
		if j.Project == nil {
			nullProject++
		}
	}
	if nullProject != 1 {
		t.Errorf("project=NULL 판단이 %d건 — 1건을 기대한다. 포인터가 아니면 NULL 과 \"\" 가 안 갈린다", nullProject)
	}
}

// 판단 정렬은 id 순이다. ULID 라 생성순이고, 같은 DB 면 같은 바이트가 나와야 한다.
func TestReadLedgerIsDeterministicallyOrdered(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")
	for i := 0; i < 5; i++ {
		linkJudgment(t, s, "p", model.JudgmentDecision, "i1", "i2")
	}

	first, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}
	second, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 재실행 실패: %v", err)
	}
	for i := range first.Judgments {
		if first.Judgments[i].ID != second.Judgments[i].ID {
			t.Fatalf("두 번 읽었더니 순서가 달라졌다: %d번째 %q vs %q",
				i, first.Judgments[i].ID, second.Judgments[i].ID)
		}
		if i > 0 && first.Judgments[i-1].ID >= first.Judgments[i].ID {
			t.Fatalf("id 오름차순이 아니다: %q >= %q", first.Judgments[i-1].ID, first.Judgments[i].ID)
		}
	}
	for i := 1; i < len(first.Links); i++ {
		p, c := first.Links[i-1], first.Links[i]
		if p.JudgmentID > c.JudgmentID {
			t.Fatalf("링크가 judgment_id 순이 아니다: %q > %q", p.JudgmentID, c.JudgmentID)
		}
	}
}

// 시각은 DB 원문 문자열 그대로다. time.Time 으로 접으면 마셜이 후행 0을 지워
// 폭이 흔들리고, 그러면 사전순 정렬이 시간순과 어긋난다(store.go 의 timeLayout 주석).
func TestReadLedgerKeepsRawTimestampString(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")
	linkJudgment(t, s, "p", model.JudgmentDecision, "i1")

	d, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}
	at := d.Judgments[0].At
	if len(at) != len("2006-01-02T15:04:05.000000Z") {
		t.Fatalf("at 이 폭 고정이 아니다(%q, %d자) — DB 원문을 그대로 실어야 한다", at, len(at))
	}
}

// snapshotByKey 는 d.Snapshots 에서 key 로 하나를 찾는다. 없으면 시험을 즉시 실패시킨다.
func snapshotByKey(t *testing.T, d LedgerDump, key string) LedgerSnapshot {
	t.Helper()
	for _, sn := range d.Snapshots {
		if sn.Key == key {
			return sn
		}
	}
	t.Fatalf("스냅숏 %q 를 원장에서 못 찾았다(%d건 중)", key, len(d.Snapshots))
	return LedgerSnapshot{}
}

// readLedgerSnapshots 는 이전 시험 셋 어디에서도 실행되지 않는다 — snapshot 행을
// 하나도 안 쓰기 때문에 rows.Next() 가 0행에서 바로 false 를 낸다. 이 아래 넷은
// 실제 행을 넣어 Scan 이 진짜로 도는 경로를 잠근다.

// evidence·input_digest 가 NULL 이면 nil 포인터로, 값이 있으면 그 값을 가리키는
// 포인터로 나와야 한다. str() 을 썼다면 NULL 과 "" 가 똑같이 non-nil 빈 문자열
// 포인터가 되어 이 시험이 못 잡는다 — ptrOf() 를 쓴 이유가 정확히 이것이다.
func TestReadLedgerSnapshotsDistinguishNullFromValue(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")

	if err := s.PutSnapshot(ctx, model.Snapshot{
		Project: "p", Key: "manual-key", Value: "42",
		Method: model.SnapshotManual, Evidence: "근거 문서", InputDigest: "deadbeef",
	}); err != nil {
		t.Fatalf("manual 스냅숏 저장 실패: %v", err)
	}
	if err := s.PutSnapshot(ctx, model.Snapshot{
		Project: "p", Key: "command-key", Value: "7",
		Method: model.SnapshotCommand,
	}); err != nil {
		t.Fatalf("command 스냅숏 저장 실패: %v", err)
	}

	d, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}

	manual := snapshotByKey(t, d, "manual-key")
	if manual.Evidence == nil || *manual.Evidence != "근거 문서" {
		t.Errorf("manual 스냅숏의 evidence 가 %v — \"근거 문서\" 를 가리키는 포인터를 기대한다", manual.Evidence)
	}
	if manual.InputDigest == nil || *manual.InputDigest != "deadbeef" {
		t.Errorf("manual 스냅숏의 input_digest 가 %v — \"deadbeef\" 를 가리키는 포인터를 기대한다", manual.InputDigest)
	}

	cmd := snapshotByKey(t, d, "command-key")
	if cmd.Evidence != nil {
		t.Errorf("command 스냅숏의 evidence 가 nil 이어야 하는데 %q 를 가리킨다", *cmd.Evidence)
	}
	if cmd.InputDigest != nil {
		t.Errorf("command 스냅숏의 input_digest 가 nil 이어야 하는데 %q 를 가리킨다", *cmd.InputDigest)
	}
}

// 일곱 컬럼을 전부 서로 다른 값으로 채워 SELECT 목록과 Scan 인자 순서가
// 어긋나면 반드시 걸리게 한다 — 예컨대 value 와 key 에 같은 문자열을 주면
// 그 둘이 뒤바뀌어도 시험이 통과하므로, 값을 겹치지 않게 고른다.
func TestReadLedgerSnapshotsPreserveColumnOrder(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "ordp")

	if err := s.PutSnapshot(ctx, model.Snapshot{
		Project: "ordp", Key: "ordkey", Value: "ordvalue",
		Method: model.SnapshotManual, Evidence: "ordevidence", InputDigest: "orddigest",
	}); err != nil {
		t.Fatalf("스냅숏 저장 실패: %v", err)
	}

	d, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}
	sn := snapshotByKey(t, d, "ordkey")

	switch {
	case sn.Project != "ordp":
		t.Errorf("project = %q, 기대 %q", sn.Project, "ordp")
	case sn.Key != "ordkey":
		t.Errorf("key = %q, 기대 %q", sn.Key, "ordkey")
	case sn.Value != "ordvalue":
		t.Errorf("value = %q, 기대 %q", sn.Value, "ordvalue")
	case sn.Method != "manual":
		t.Errorf("method = %q, 기대 %q", sn.Method, "manual")
	case sn.Evidence == nil || *sn.Evidence != "ordevidence":
		t.Errorf("evidence = %v, 기대 \"ordevidence\" 를 가리키는 포인터", sn.Evidence)
	case sn.InputDigest == nil || *sn.InputDigest != "orddigest":
		t.Errorf("input_digest = %v, 기대 \"orddigest\" 를 가리키는 포인터", sn.InputDigest)
	}
}

// 정렬은 (project, key) 오름차순이다. 두 프로젝트에 키를 사전순 역순으로 넣어
// 삽입 순서가 아니라 ORDER BY 가 결과를 정한다는 것을 확인한다.
func TestReadLedgerSnapshotsOrderedByProjectThenKey(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "q1")
	seed(t, s, "q2")

	// 삽입은 일부러 기대 순서의 역순으로 한다.
	for _, sn := range []model.Snapshot{
		{Project: "q2", Key: "k2", Value: "v-q2-k2", Method: model.SnapshotCommand},
		{Project: "q1", Key: "k2", Value: "v-q1-k2", Method: model.SnapshotCommand},
		{Project: "q2", Key: "k1", Value: "v-q2-k1", Method: model.SnapshotCommand},
		{Project: "q1", Key: "k1", Value: "v-q1-k1", Method: model.SnapshotCommand},
	} {
		if err := s.PutSnapshot(ctx, sn); err != nil {
			t.Fatalf("스냅숏 저장 실패(%s/%s): %v", sn.Project, sn.Key, err)
		}
	}

	d, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}

	want := [][2]string{{"q1", "k1"}, {"q1", "k2"}, {"q2", "k1"}, {"q2", "k2"}}
	if len(d.Snapshots) != len(want) {
		t.Fatalf("스냅숏이 %d건 — %d건을 기대한다", len(d.Snapshots), len(want))
	}
	for i, w := range want {
		got := d.Snapshots[i]
		if got.Project != w[0] || got.Key != w[1] {
			t.Fatalf("%d번째가 (%q,%q) — (%q,%q) 를 기대한다(정렬이 project,key 순이 아니다)",
				i, got.Project, got.Key, w[0], w[1])
		}
	}
}

// computed_at 도 판단의 at 과 같은 이유로 DB 원문 폭 고정 문자열이어야 한다
// (time.Time 으로 접으면 마셜이 후행 0을 지워 폭이 흔들린다).
func TestReadLedgerSnapshotsKeepRawTimestampString(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")
	if err := s.PutSnapshot(ctx, model.Snapshot{
		Project: "p", Key: "k", Value: "v", Method: model.SnapshotCommand,
	}); err != nil {
		t.Fatalf("스냅숏 저장 실패: %v", err)
	}

	d, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}
	sn := snapshotByKey(t, d, "k")
	if len(sn.ComputedAt) != len("2006-01-02T15:04:05.000000Z") {
		t.Fatalf("computed_at 이 폭 고정이 아니다(%q, %d자) — DB 원문을 그대로 실어야 한다",
			sn.ComputedAt, len(sn.ComputedAt))
	}
}

// journalHeaderOf 는 SQLite 파일 헤더의 18·19번째 바이트다 — 1 이면 롤백저널, 2 면 WAL.
//
// ★ size/ModTime 으로는 이 축이 안 보인다. journal_mode 전환은 파일 앞 20바이트만 고치므로
// 크기가 안 변하고, 같은 초 안에서 끝나면 ModTime 도 흔들리지 않는다.
// TestOpenLedgerDoesNotMigrateOrBackup 이 정확히 그 이유로 "대상을 안 바꾼다"를
// 증명하지 못했다(픽스처가 이미 WAL 이라 전환이 무연산이었다).
func journalHeaderOf(t *testing.T, path string) [2]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("헤더를 읽을 파일을 열지 못했다(%s): %v", path, err)
	}
	defer f.Close()
	var head [20]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		t.Fatalf("헤더 20바이트를 읽지 못했다(%s): %v", path, err)
	}
	return [2]byte{head[18], head[19]}
}

// makeRollbackJournalDB 는 **롤백저널 모드**의 v4 DB 를 만든다.
//
// ★ 이것이 실제로 건져야 할 대상의 모양이다. migrate 직전 VACUUM INTO 로 뜨는
// <db>.bak-* 산출물이 정확히 이 모드로 나온다(실측: 이 머신의 fd.db.bak-* 헤더가 1/1).
func makeRollbackJournalDB(t *testing.T, path string, downgradeTo int) {
	t.Helper()
	s, err := OpenWithLogger(path, testLogger())
	if err != nil {
		t.Fatalf("픽스처 초기 Open 실패: %v", err)
	}
	seed(t, s, "p")
	linkJudgment(t, s, "p", model.JudgmentDecision, "i1")
	if downgradeTo > 0 {
		if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version > ?`, downgradeTo); err != nil {
			t.Fatalf("픽스처 버전 되돌리기 실패: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("픽스처 닫기 실패: %v", err)
	}

	// WAL 을 걷어 롤백저널로 되돌린다. journal_mode 를 안 거는 DSN 으로 열어야
	// 이 전환이 곧바로 되감기지 않는다.
	raw, err := sql.Open("sqlite", ledgerDSN(path))
	if err != nil {
		t.Fatalf("픽스처 raw 열기 실패: %v", err)
	}
	var mode string
	if err := raw.QueryRow(`PRAGMA journal_mode=delete`).Scan(&mode); err != nil {
		t.Fatalf("픽스처 journal_mode 전환 실패: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("픽스처 raw 닫기 실패: %v", err)
	}

	// ── 전제 확인 ── 픽스처가 정말 롤백저널이어야 아래 단정이 의미를 갖는다.
	if got := journalHeaderOf(t, path); got != [2]byte{1, 1} {
		t.Fatalf("픽스처가 롤백저널이 아니다(헤더 %d/%d) — 이 시험이 아무것도 안 본다", got[0], got[1])
	}
}

// 원장 열기는 **대상 파일의 저널 모드를 바꾸지 않는다** — 거절할 때도, 성공할 때도.
//
// ★ 이 시험이 겨냥하는 결함: OpenLedger 의 첫 줄인 ProbeMigration 이 기본 dsn() 을 써서
// `journal_mode(WAL)` 을 걸었다. ledgerDSN 이 일부러 지운 유일한 쓰기 pragma 를 두 줄 위에서
// 되살리는 것이라, "거절한다"고 인쇄하는 실행이 아카이브를 WAL 로 영구 변환했다.
// 이 명령이 존재하는 이유가 "망가진 DB 에서 판단을 건진다"인데, 그 대상이 마지막 남은
// 백업 하나뿐인 상황에서 오타 한 번이 그것을 고쳐 버린다.
func TestOpenLedgerDoesNotRewriteJournalMode(t *testing.T) {
	t.Run("거절 경로", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "fd.db")
		makeRollbackJournalDB(t, path, BaseSchemaVersion)
		before := journalHeaderOf(t, path)

		s, err := OpenLedger(context.Background(), path, testLogger())
		if err == nil {
			s.Close()
			t.Fatal("이행이 필요한 DB 를 열었다 — 거절해야 한다")
		}
		if !strings.Contains(err.Error(), "거절") {
			t.Errorf("거절 문구가 아니다: %v", err)
		}
		if after := journalHeaderOf(t, path); after != before {
			t.Errorf("거절하면서 대상을 바꿨다: 헤더 %d/%d → %d/%d",
				before[0], before[1], after[0], after[1])
		}
	})

	t.Run("성공 경로", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "fd.db")
		makeRollbackJournalDB(t, path, 0)
		before := journalHeaderOf(t, path)

		s, err := OpenLedger(context.Background(), path, testLogger())
		if err != nil {
			t.Fatalf("롤백저널 DB 를 열지 못했다 — VACUUM INTO 산출물이 이 모드다: %v", err)
		}
		d, err := s.ReadLedger(context.Background())
		if err != nil {
			t.Fatalf("ReadLedger 실패: %v", err)
		}
		if len(d.Judgments) != 1 {
			t.Fatalf("판단이 %d건 — 1건을 기대한다", len(d.Judgments))
		}
		if err := s.Close(); err != nil {
			t.Fatalf("닫기 실패: %v", err)
		}
		if after := journalHeaderOf(t, path); after != before {
			t.Errorf("성공 경로가 대상을 바꿨다: 헤더 %d/%d → %d/%d",
				before[0], before[1], after[0], after[1])
		}
	})
}

// 원장 열기는 스키마를 바꾸지 않는다. store.Open 은 반드시 migrate 를 돌고
// 그 앞에서 VACUUM INTO 를 뜨는데, 백업이 그 계기가 되면 안 된다.
//
// ★ 이 시험은 "대상을 안 바꾼다"의 **일부만** 본다. 픽스처를 OpenWithLogger 로 만들어
// 이미 WAL 인 파일을 재므로 journal_mode 전환이 무연산이 되고, 그래서 size/ModTime
// 단정이 저절로 참이 된다 — 대상이 실제로 바뀌는 유일한 방식에 눈이 멀어 있다.
// 그 축은 TestOpenLedgerDoesNotRewriteJournalMode 가 롤백저널 픽스처로 본다.
// 여기서 픽스처 모드를 바꾸지 않는 이유: 이 시험의 대상은 "증분·백업이 안 도는가"이고
// 그것은 WAL 인 정상 DB 에서 재는 것이 맞다.
func TestOpenLedgerDoesNotMigrateOrBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fd.db")

	// 먼저 정상 Open 으로 스키마를 올린다.
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := OpenWithLogger(path, quiet)
	if err != nil {
		t.Fatalf("초기 Open 실패: %v", err)
	}
	seed(t, s, "p")
	linkJudgment(t, s, "p", model.JudgmentDecision, "i1")
	if err := s.Close(); err != nil {
		t.Fatalf("닫기 실패: %v", err)
	}

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat 실패: %v", err)
	}

	ls, err := OpenLedger(context.Background(), path, quiet)
	if err != nil {
		t.Fatalf("OpenLedger 실패: %v", err)
	}
	d, err := ls.ReadLedger(context.Background())
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}
	if len(d.Judgments) != 1 {
		t.Fatalf("판단이 %d건 — 1건을 기대한다", len(d.Judgments))
	}
	if err := ls.Close(); err != nil {
		t.Fatalf("원장 핸들 닫기 실패: %v", err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat 실패: %v", err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("DB 파일이 바뀌었다: %d/%v → %d/%v",
			before.Size(), before.ModTime(), after.Size(), after.ModTime())
	}
	// 새 .bak 이 뜨지 않았는지 본다.
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("디렉토리 훑기 실패: %v", err)
	}
	for _, e := range ents {
		if strings.Contains(e.Name(), ".bak-") {
			t.Errorf("원장 열기가 백업 파일을 만들었다: %s", e.Name())
		}
	}
}

// 없는 파일은 만들지 않고 거절한다. sql.Open 은 파일을 만들기 때문에,
// 부재를 확인 안 하면 백업이 빈 DB 를 하나 만들어 놓고 "0건 백업했다"고 말한다.
func TestOpenLedgerRejectsMissingFile(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	path := filepath.Join(t.TempDir(), "없다.db")
	if _, err := OpenLedger(context.Background(), path, quiet); err == nil {
		t.Fatal("없는 파일을 열었다 — 부재는 오류여야 한다")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("없는 파일을 만들어 버렸다")
	}
}

// 되쓰기는 원문을 그대로 되살린다 — id·at·NULL 까지.
// AddJudgment 를 안 쓰는 이유가 이것이다(그것은 빈 ID/At 을 자기가 채운다).
func TestWriteLedgerRestoresRowsVerbatim(t *testing.T) {
	src := newStore(t)
	ctx := context.Background()
	seed(t, src, "p")
	first := linkJudgment(t, src, "p", model.JudgmentDecision, "i1", "i2")
	// supersedes 가 실제로 걸린 행을 만든다 — 자기참조 FK 라 삽입 순서 제약이 여기서 드러난다.
	//
	// ★ id 를 일부러 first 보다 사전순으로 앞세운다. ULID 는 생성 시각순이라, 자연 생성
	// 그대로 두면(id 를 비워 NewID 가 채우게 하면) supersedes 행은 참조 대상보다 **나중에**
	// 만들어지므로 항상 더 큰 id 를 받는다 — 그러면 ReadLedger 의 id 오름차순 정렬에서
	// 참조 대상이 항상 먼저 나와, WriteLedger 가 defer_foreign_keys 없이도 우연히 통과한다.
	// 삽입 순서 제약을 실제로 시험하려면 참조하는 쪽이 먼저 나와야 하므로, crockford 알파벳의
	// 최소 문자('0')로만 된 26자 id 를 손으로 박는다 — 어떤 실제 ULID 보다도 사전순으로 작다.
	if _, err := src.AddJudgment(ctx, model.Judgment{
		ID:      "00000000000000000000000000",
		Project: "p", Kind: model.JudgmentDecision, Title: "정정", Body: "앞 판단을 대체한다",
		Supersedes: first,
	}); err != nil {
		t.Fatalf("supersedes 판단 저장 실패: %v", err)
	}
	if err := src.PutSnapshot(ctx, model.Snapshot{
		Project: "p", Key: "k", Value: "1", Method: model.SnapshotCommand,
	}); err != nil {
		t.Fatalf("스냅숏 저장 실패: %v", err)
	}

	want, err := src.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("원본 읽기 실패: %v", err)
	}

	// 빈 DB 에 되쓴다. project·machine 도 원장에 실려 있으므로 미리 만들지 않는다
	// (미리 만들면 WriteLedger 의 되쓰기와 id 가 겹쳐 UNIQUE 위반이 난다).
	dst := newStore(t)
	if err := dst.WriteLedger(ctx, want); err != nil {
		t.Fatalf("WriteLedger 실패: %v", err)
	}

	got, err := dst.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("복원본 읽기 실패: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("원본과 복원본이 다르다:\n원본 %+v\n복원 %+v", want, got)
	}
}

// 같은 원장을 통째로 두 번 되쓰면 거절한다.
//
// ★ 이 시험이 실제로 잡는 지점: 되쓰기는 machine → project → session → judgment 순으로
// 도는데, 두 번째 호출은 machine 부터 이미 dst 에 있는 id 라 **machine 표의 UNIQUE 에서
// 가장 먼저 걸린다** — judgment.id 까지 내려가지 않는다. project·machine 도 원장에 실려
// 있으므로 미리 만들지 않는다(TestWriteLedgerRestoresRowsVerbatim 참고).
// "판단 자체의 중복만" 격리해서 보려면 TestWriteLedgerRejectsDuplicateJudgmentEvenWhenClosureExists 를 봐라 —
// 이 시험은 그보다 넓은 축("중복 되쓰기는 전부 거절되고 부분 적용이 없다")을 지킨다.
func TestWriteLedgerRejectsDuplicateID(t *testing.T) {
	src := newStore(t)
	ctx := context.Background()
	seed(t, src, "p")
	linkJudgment(t, src, "p", model.JudgmentDecision, "i1")
	d, err := src.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("원본 읽기 실패: %v", err)
	}

	// project·machine 도 원장에 실려 있으므로 미리 만들지 않는다(TestWriteLedgerRestoresRowsVerbatim 참고).
	dst := newStore(t)
	if err := dst.WriteLedger(ctx, d); err != nil {
		t.Fatalf("첫 되쓰기 실패: %v", err)
	}
	if err := dst.WriteLedger(ctx, d); err == nil {
		t.Fatal("같은 원장을 두 번 되썼는데 통과했다 — 판단은 추가 전용이라 되돌릴 수 없다")
	}

	// 실패한 되쓰기가 부분 적용으로 남지 않는지 본다.
	after, err := dst.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("재확인 실패: %v", err)
	}
	if len(after.Judgments) != len(d.Judgments) {
		t.Errorf("판단이 %d건으로 늘었다 — 한 트랜잭션이라 그대로여야 한다", len(after.Judgments))
	}
}

// judgment 표 자체의 중복 거절을 격리해서 본다.
//
// TestWriteLedgerRejectsDuplicateID 는 원장 전체를 두 번 되쓰므로 machine 표(가장 먼저
// 도는 표)의 UNIQUE 에서 먼저 걸려 judgment.id 까지 내려가지 않는다. 여기서는 폐포(machine·
// project·session)를 dst 에 먼저 정상 되쓴 뒤, **판단만 담은** LedgerDump 를 다시 넘긴다 —
// 그러면 FK 는 이미 만족돼 있으므로 이번에는 judgment.id 의 PK 위반에서만 걸린다.
func TestWriteLedgerRejectsDuplicateJudgmentEvenWhenClosureExists(t *testing.T) {
	src := newStore(t)
	ctx := context.Background()
	seed(t, src, "p")
	linkJudgment(t, src, "p", model.JudgmentDecision, "i1")
	d, err := src.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("원본 읽기 실패: %v", err)
	}

	dst := newStore(t)
	if err := dst.WriteLedger(ctx, d); err != nil {
		t.Fatalf("첫 되쓰기 실패: %v", err)
	}

	// 폐포는 이미 dst 에 있다 — 판단만 다시 넘겨 FK 를 건드리지 않고 judgment.id 만 겹치게 한다.
	judgmentsOnly := LedgerDump{Judgments: d.Judgments}
	err = dst.WriteLedger(ctx, judgmentsOnly)
	if err == nil {
		t.Fatal("같은 판단 id 를 두 번 되썼는데 통과했다 — judgment 는 UPDATE·DELETE 트리거로 금지돼 있어 되돌릴 수 없다")
	}
	if !strings.Contains(err.Error(), "judgment") {
		t.Errorf("오류가 judgment 를 가리키지 않는다(격리가 안 됐다는 뜻) — %v", err)
	}

	after, err := dst.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("재확인 실패: %v", err)
	}
	if len(after.Judgments) != len(d.Judgments) {
		t.Errorf("판단이 %d건으로 늘었다 — 한 트랜잭션이라 그대로여야 한다", len(after.Judgments))
	}
}

// 원장은 FK 폐포를 통째로 담는다. session.id 는 서버 발급 ULID 라 새 DB 에서 재현할 수 없고,
// 그래서 세션을 안 담으면 세션 걸린 판단(실측 85%)이 FK 위반으로 전부 롤백된다.
func TestReadLedgerCoversTheFullFKClosure(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p1")
	sess := mustSession(t, s, "p1", "cc-closure")

	if _, err := s.AddJudgment(ctx, model.Judgment{
		Project: "p1", SessionID: sess.ID, Kind: model.JudgmentDecision, Body: "세션 걸린 판단",
	}); err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}

	d, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}
	if len(d.Machines) == 0 {
		t.Error("machine 이 원장에 없다 — session.machine_id 가 가리킬 대상이 사라진다")
	}
	if len(d.Projects) == 0 {
		t.Error("project 가 원장에 없다")
	}
	if len(d.Sessions) == 0 {
		t.Fatal("session 이 원장에 없다 — 판단의 85%가 이것을 가리킨다")
	}
	var found bool
	for _, x := range d.Sessions {
		if x.ID == sess.ID {
			found = true
			if x.Project != "p1" || x.MachineID != "m1" {
				t.Errorf("세션 필드가 틀리다: %+v", x)
			}
		}
	}
	if !found {
		t.Errorf("판단이 가리키는 세션 %q 가 원장에 없다", sess.ID)
	}
}

// 빈 DB 에 되쓰면 폐포가 통째로 복원된다 — 미리 심어 둘 것이 하나도 없어야 한다.
// 이것이 "무손실" 등급의 실제 의미다.
func TestWriteLedgerRestoresIntoATrulyEmptyDB(t *testing.T) {
	src := newStore(t)
	ctx := context.Background()
	seed(t, src, "p1")
	sess := mustSession(t, src, "p1", "cc-restore")

	if _, err := src.AddJudgment(ctx, model.Judgment{
		Project: "p1", SessionID: sess.ID, Kind: model.JudgmentAsk,
		Title: "제목", Body: "세션 걸린 판단",
	}); err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}
	if err := src.PutSnapshot(ctx, model.Snapshot{
		Project: "p1", Key: "k", Value: "1", Method: model.SnapshotCommand,
	}); err != nil {
		t.Fatalf("스냅숏 저장 실패: %v", err)
	}

	want, err := src.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("원본 읽기 실패: %v", err)
	}

	// ★ seed 를 부르지 않는다. 원장이 project·machine·session 을 다 갖고 와야 한다.
	dst := newStore(t)
	if err := dst.WriteLedger(ctx, want); err != nil {
		t.Fatalf("빈 DB 되쓰기 실패 — 폐포가 안 닫혔다: %v", err)
	}
	got, err := dst.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("복원본 읽기 실패: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("왕복에서 원장이 달라졌다:\n원본 %+v\n복원 %+v", want, got)
	}
}

// state='blocked' 세션은 CHECK(state<>'blocked' OR blocked_why 가 비지 않음) 를 통과해야 한다.
// 지금 실 DB 에 blocked 세션이 0건이라 이 축은 시험이 만들어야만 검증된다.
func TestWriteLedgerRestoresBlockedSession(t *testing.T) {
	src := newStore(t)
	ctx := context.Background()
	seed(t, src, "p1")
	sess := mustSession(t, src, "p1", "cc-blocked")
	if err := src.SetSessionState(ctx, sess.ID, model.SessionBlocked, "왜 막혔는지"); err != nil {
		t.Fatalf("세션을 막힘으로 못 바꿨다: %v", err)
	}

	want, err := src.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("원본 읽기 실패: %v", err)
	}
	var blocked bool
	for _, x := range want.Sessions {
		if x.State == "blocked" {
			blocked = true
			if x.BlockedWhy == nil || *x.BlockedWhy == "" {
				t.Fatalf("blocked 세션인데 사유가 비었다: %+v", x)
			}
		}
	}
	if !blocked {
		t.Fatal("전제가 깨졌다 — blocked 세션이 원장에 없다")
	}

	dst := newStore(t)
	if err := dst.WriteLedger(ctx, want); err != nil {
		t.Fatalf("blocked 세션 되쓰기 실패(CHECK 위반?): %v", err)
	}
}
