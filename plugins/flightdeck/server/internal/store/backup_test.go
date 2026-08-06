package store

import (
	"context"
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

// 원장 열기는 스키마를 바꾸지 않는다. store.Open 은 반드시 migrate 를 돌고
// 그 앞에서 VACUUM INTO 를 뜨는데, 백업이 그 계기가 되면 안 된다.
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

	// 빈 DB 에 되쓴다. project·session·machine 은 원장 밖이라 미리 만든다.
	dst := newStore(t)
	seed(t, dst, "p")
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

// 같은 id 를 두 번 넣으면 거절한다. judgment 는 트리거로 UPDATE·DELETE 가 금지돼 있어
// 잘못 넣은 행을 고치거나 지울 수 없다 — 조용히 넘어가면 되돌릴 방법이 없다.
func TestWriteLedgerRejectsDuplicateID(t *testing.T) {
	src := newStore(t)
	ctx := context.Background()
	seed(t, src, "p")
	linkJudgment(t, src, "p", model.JudgmentDecision, "i1")
	d, err := src.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("원본 읽기 실패: %v", err)
	}

	dst := newStore(t)
	seed(t, dst, "p")
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
