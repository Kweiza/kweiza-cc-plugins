package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/ledger"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// 산출물 자리는 DB 와 **다른 볼륨**이어야 한다(설계 §7).
func TestLedgerOutDir(t *testing.T) {
	env := func(m map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	cases := []struct {
		name      string
		vars      map[string]string
		home      string
		inDataDir bool
		want      string
	}{
		{"FD_LEDGER 가 이긴다", map[string]string{"FD_LEDGER": "/mnt/backup"}, "/home/u", true, "/mnt/backup"},
		{"컨테이너면 /ledger", nil, "/home/u", true, "/ledger"},
		{"호스트면 홈 아래", nil, "/home/u", false, "/home/u/.flightdeck-ledger"},
		{"공백은 없는 것으로 본다", map[string]string{"FD_LEDGER": "   "}, "/home/u", false, "/home/u/.flightdeck-ledger"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LedgerOutDir(env(c.vars), c.home, c.inDataDir); got != c.want {
				t.Errorf("자리가 %q 다 — %q 를 기대한다", got, c.want)
			}
		})
	}
	// ★ DB 자리와 같으면 분리가 통째로 무의미하다. 기본값끼리 겹치지 않는 것을 못박는다.
	db := DefaultDBPath(env(nil), "/home/u", true)
	out := LedgerOutDir(env(nil), "/home/u", true)
	if filepath.Dir(db) == out {
		t.Errorf("원장 자리가 DB 디렉토리와 같다(%s) — 볼륨 하나가 죽으면 둘 다 잃는다", out)
	}
}

// 안 바뀐 회차는 안 쓴다 — **매니페스트만 다른 것은 '바뀐 것'이 아니다.**
func TestLedgerDataUnchangedIgnoresManifest(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		"judgments.jsonl":   []byte("{\"id\":\"a\"}\n"),
		"machines.jsonl":    []byte("{\"id\":\"m\"}\n"),
		ledger.ManifestName: []byte(`{"exported_at":"1"}`),
	}
	if ledgerDataUnchanged(files, dir) {
		t.Error("빈 자리를 '이미 최신'으로 봤다 — 첫 회차가 통째로 안 돈다")
	}
	if _, err := ledger.Write(files, dir); err != nil {
		t.Fatalf("Write 실패: %v", err)
	}

	// 매니페스트만 다른 회차 — exported_at 은 회차마다 새로 찍힌다.
	next := map[string][]byte{}
	for k, v := range files {
		next[k] = v
	}
	next[ledger.ManifestName] = []byte(`{"exported_at":"2"}`)
	if !ledgerDataUnchanged(next, dir) {
		t.Error("매니페스트만 다른데 '바뀌었다'로 봤다 — 안 바뀐 원장을 매시간 다시 쓰게 된다")
	}

	// 데이터가 다르면 바뀐 것이다.
	next["judgments.jsonl"] = []byte("{\"id\":\"b\"}\n")
	if ledgerDataUnchanged(next, dir) {
		t.Error("데이터가 다른데 '안 바뀌었다'로 봤다 — 새 판단이 백업에 영영 안 들어간다")
	}

	// 파일 하나가 사라지면 바뀐 것이다(반쯤 덮인 자리가 그 모양이다).
	next["judgments.jsonl"] = files["judgments.jsonl"]
	if err := os.Remove(filepath.Join(dir, "machines.jsonl")); err != nil {
		t.Fatalf("파일 제거 실패: %v", err)
	}
	if ledgerDataUnchanged(next, dir) {
		t.Error("파일이 빠진 자리를 '이미 최신'으로 봤다")
	}
}

// 한 회차가 실제로 원장을 낳고, 안 바뀌면 건너뛰고, 판단이 늘면 다시 쓴다.
func TestLedgerBackupOnceWritesThenSkipsThenWrites(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "fd.db")
	st, err := store.OpenWithLogger(dbPath, quietLog())
	if err != nil {
		t.Fatalf("DB 열기 실패: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProject(ctx, model.Project{ID: "p", Path: "/repo/p"}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	if _, err := st.AddJudgment(ctx, model.Judgment{
		Project: "p", Kind: model.JudgmentDecision, Body: "첫 판단",
	}); err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}

	out := filepath.Join(t.TempDir(), "ledger")

	wrote, err := ledgerBackupOnce(ctx, st, out, "2026-08-10T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("첫 회차 실패: %v", err)
	}
	if !wrote {
		t.Fatal("첫 회차가 아무것도 안 썼다 — 빈 자리인데 '이미 최신'으로 봤다")
	}
	if !ledger.IsOurOutput(out) {
		t.Fatal("산출물이 우리 원장으로 안 보인다")
	}

	// 같은 DB · 다른 시각 — 데이터가 그대로이므로 건너뛴다.
	wrote, err = ledgerBackupOnce(ctx, st, out, "2026-08-10T01:00:00.000000Z")
	if err != nil {
		t.Fatalf("둘째 회차 실패: %v", err)
	}
	if wrote {
		t.Error("안 바뀐 회차가 다시 썼다 — exported_at 만 흔들려도 매시간 새 세대가 난다")
	}

	// 판단이 늘면 다시 쓴다.
	if _, err := st.AddJudgment(ctx, model.Judgment{
		Project: "p", Kind: model.JudgmentDecision, Body: "둘째 판단",
	}); err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}
	wrote, err = ledgerBackupOnce(ctx, st, out, "2026-08-10T02:00:00.000000Z")
	if err != nil {
		t.Fatalf("셋째 회차 실패: %v", err)
	}
	if !wrote {
		t.Fatal("판단이 늘었는데 안 썼다 — 새 판단이 백업에 영영 안 들어간다")
	}
	d, _, err := ledger.Read(out)
	if err != nil {
		t.Fatalf("산출물 되읽기 실패: %v", err)
	}
	if len(d.Judgments) != 2 {
		t.Errorf("산출물의 판단이 %d건 — 2건이어야 한다", len(d.Judgments))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 잡의 관측이 /healthz 까지 실제로 도착한다
// ─────────────────────────────────────────────────────────────────────────────
//
// ★ 이 시험이 무는 것은 **조립·선 넘기기·화면을 잇는 변환**이다. 앞선 물결이 자동 갱신
// 축에서 정확히 이 자리를 잃었다(2026-08-07 실측): 셋은 각자 잠겨 있었는데 그것들을 잇는
// 변환만 아무 시험에도 안 걸려서, 판정은 살아 있고 값만 안 도착하는 상태가 조용히 났다.

// LastAt 은 "회차 없음"과 "1970년"을 안 접는다.
func TestLedgerBackupStatusOfDoesNotFoldNeverRanIntoEpoch(t *testing.T) {
	got := ledgerBackupStatusOf(ledgerBackupState{route: "/ledger"})
	if !got.Running {
		t.Error("잡이 있는데 Running=false 다 — '배선 안 됨'과 '아직 회차 없음'이 접힌다")
	}
	if got.LastAt != nil {
		t.Errorf("한 회차도 안 돌았는데 시각이 실렸다(%v) — 1970년에 백업한 서버로 보인다", *got.LastAt)
	}
	if got.Route != "/ledger" {
		t.Errorf("산출물 자리가 안 실렸다 — '돌긴 도는데 어디에 쌓이는지 모른다'가 된다: %+v", got)
	}

	at := time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)
	got = ledgerBackupStatusOf(ledgerBackupState{lastAt: at, outcome: "failed", detail: "디스크가 찼다", route: "/ledger"})
	if got.LastAt == nil || !got.LastAt.Equal(at) {
		t.Errorf("회차 시각이 안 실렸다: %+v", got)
	}
	if got.Outcome != "failed" || got.Detail != "디스크가 찼다" {
		t.Errorf("결과와 사유가 안 실렸다: %+v", got)
	}
}

// 회차가 돌면 잡이 그것을 기억하고, 그 값이 API 모양까지 온다.
func TestLedgerBackupJobRemembersItsLastTick(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "fd.db")
	st, err := store.OpenWithLogger(dbPath, quietLog())
	if err != nil {
		t.Fatalf("DB 열기 실패: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProject(ctx, model.Project{ID: "p", Path: "/repo/p"}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}

	out := filepath.Join(t.TempDir(), "ledger")
	j := newLedgerBackupJob(quietLog(), st, out, time.Hour)

	// 아직 안 돌았다.
	if s := ledgerBackupStatusOf(j.State()); s.LastAt != nil || s.Outcome != "" {
		t.Fatalf("한 회차도 안 돌았는데 상태가 차 있다: %+v", s)
	}

	at := time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)
	j.tick(ctx, at)
	s := ledgerBackupStatusOf(j.State())
	if s.Outcome != "wrote" {
		t.Errorf("첫 회차가 %q 다 — 빈 자리라 wrote 여야 한다: %+v", s.Outcome, s)
	}
	if s.LastAt == nil || !s.LastAt.Equal(at) {
		t.Errorf("회차 시각이 안 남았다: %+v", s)
	}

	// 안 바뀐 회차 — unchanged 이고, 그것은 실패가 아니다.
	j.tick(ctx, at.Add(time.Hour))
	if s := ledgerBackupStatusOf(j.State()); s.Outcome != "unchanged" {
		t.Errorf("안 바뀐 회차가 %q 다: %+v", s.Outcome, s)
	}

	// 실패하는 회차 — 자리를 파일로 막으면 MkdirAll 이 죽는다.
	bad := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(bad, []byte("x"), 0o600); err != nil {
		t.Fatalf("픽스처 실패: %v", err)
	}
	j2 := newLedgerBackupJob(quietLog(), st, bad, time.Hour)
	j2.tick(ctx, at)
	s2 := ledgerBackupStatusOf(j2.State())
	if s2.Outcome != "failed" {
		t.Errorf("쓰기가 실패했는데 %q 로 남았다 — 조용히 실패하는 자리가 그대로다: %+v", s2.Outcome, s2)
	}
	if strings.TrimSpace(s2.Detail) == "" {
		t.Error("실패인데 사유가 비었다 — 화면이 '무엇이 잘못됐나'를 못 말한다")
	}
}

// 배선이 실제로 걸린다 — 잡이 nil 이면 콜백을 안 달고, 있으면 그 값이 온다.
func TestServeAPIOptionsWiresLedgerBackup(t *testing.T) {
	if serveAPIOptions("tok", 60, quietLog(), false, nil, nil).LedgerBackup != nil {
		t.Error("잡이 없는데 콜백이 달렸다 — api 가 '배선 안 됨'을 못 말하게 된다")
	}
	j := newLedgerBackupJob(quietLog(), nil, "/ledger", time.Hour)
	opt := serveAPIOptions("tok", 60, quietLog(), false, nil, j)
	if opt.LedgerBackup == nil {
		t.Fatal("잡이 있는데 콜백이 안 달렸다 — /healthz 가 이 축을 영영 못 낸다")
	}
	if got := opt.LedgerBackup(); !got.Running || got.Route != "/ledger" {
		t.Errorf("콜백이 잡의 상태를 안 나른다: %+v", got)
	}
}
