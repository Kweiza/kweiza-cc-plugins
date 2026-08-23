package store

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 012 는 인자 삼킴으로 죽은 판단↔항목 링크 21행을 되살린다.
//
// ★ 이 시험의 무게는 "21행이 들어갔다"가 아니라 **"안 건드릴 것을 안 건드렸다"** 에 있다
// (005·010 시험이 세운 그 규율). 그리고 여기서 안 건드릴 것은 같은 표의 다른 행이 아니라
// **item.close_reason** 이다 — 같은 사고로 judgment.title 20건이 비었는데 그 제목의
// **유일한 사본**이 close_reason 꼬리이고, judgment 는 추가 전용이라(judgment_no_update)
// 그것을 "마크업 정리"로 자르면 복구 경로가 0이 된다. 그 금지의 이행이 이 시험이다.
//
// ★ 그리고 **읽는 경로까지 본다.** judgment_link 에 행이 있는 것과 그 판단이 항목을 집는
// 세션에게 딸려오는 것은 다른 축이다 — JudgmentsForItem 은
// COALESCE(target_project, judgment.project) 로 프로젝트를 판정하므로, 행을 넣고도
// 그 판정에서 갈리면 되살린 것이 다시 안 읽힌다. 링크 수만 세면 그 갈래가 안 보인다.
func TestMigration012RecoversSwallowedLinksAndTouchesNothingElse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	// ★ 리터럴 11 이다. SchemaVersion-1 이 아니다 — 재현하려는 옛 상태는 "012 직전",
	//   즉 11 이고 그 사실은 012 파일에 고정돼 있다(005·010 시험이 못박아 둔 함정).
	//   11판 DB 는 Open 이 거절하므로(적용이 기동에서 분리된 뒤의 규약) openRaw 로 연다.
	const prev = 11
	mustMigrateTo(t, path, prev)
	s, err := openRaw(path, log)
	if err != nil {
		t.Fatalf("열기 실패: %v", err)
	}
	seed(t, s, "context-platform")
	sess := mustSession(t, s, "context-platform", "cc-swallow")

	for _, id := range []string{
		"owner-audit-worm-blocks-e2e-reset", // 증분이 되살리는 대상 중 open 인 것
		"e2e-assert-no-firing-alerts",       // followups 삼킴이 가리키는 셋 중 하나
		"e2e-sa-owner-last-effective-guard", // 〃
		"e2e-sa-owners-roundtrip",           // 이미 정상 링크를 가진 대조군의 대상
	} {
		mustItem(t, s, "context-platform", id)
	}

	// ── 사고를 재현한다: 링크가 **없는** 판단 둘 ──
	//
	// id 는 증분이 못박은 그 값이다. 증분은 이 id 목록으로만 도므로, 여기서 다른 id 를 쓰면
	// 시험은 초록인데 증분은 실제 원장에서 0행을 넣는 상태가 조용히 성립한다.
	//
	// ★ 갈래 **둘 다** 심는다. 삼킨 인자가 item_id 인 것(1판단→1항목)과 followups 인 것
	//   (1판단→항목 셋)은 증분 안에서 같은 VALUES 목록에 있지만 사고의 모양이 다르다 —
	//   후자는 후속 등록 자체가 0건이 돼 세션이 add 로 다시 만든 갈래다.
	const jItemID = "01KZQ8MAWKBFZR63WHW49Q14RF"    // item_id 가 삼켜졌다
	const jFollowups = "01KZQ8HEWK5ZQBB0JRTG7BZVW1" // followups 가 삼켜졌다
	for _, id := range []string{jItemID, jFollowups} {
		if _, err := s.AddJudgment(ctx, model.Judgment{
			ID: id, Project: "context-platform", SessionID: sess.ID,
			Kind: model.JudgmentHandoff,
			Body: "삼킨 인자가 값 안으로 들어온 판단이다. 링크가 하나도 안 걸렸다.",
			// Links 가 비어 있는 것이 이 사고의 실물이다.
		}); err != nil {
			t.Fatalf("삼킴 판단 심기 실패(%s): %v", id, err)
		}
	}

	// ── 안 건드려야 할 것 ①: 이미 정상 링크를 가진 판단 ──
	if _, err := s.AddJudgment(ctx, model.Judgment{
		Project: "context-platform", SessionID: sess.ID, Kind: model.JudgmentDecision,
		Body:  "정상 링크를 가진 대조군이다.",
		Links: []model.JudgmentLink{{TargetKind: "item", TargetID: "e2e-sa-owners-roundtrip"}},
	}); err != nil {
		t.Fatalf("대조군 판단 심기 실패: %v", err)
	}

	// ── 안 건드려야 할 것 ②: 삼킨 꼬리가 든 close_reason ──
	//
	// ★ 이 문자열이 **잃은 판단 제목의 유일한 사본**이다. 증분이 여기 한 글자라도 쓰면
	//   그 제목은 영구 소실된다 — judgment 는 추가 전용이라 되살릴 자리가 없다.
	const pollutedClose = "처분했다.</close_reason>\n" +
		`<parameter name="title">` + "처분 — 밟을 화면이 전건 없어졌다"
	if err := s.SetItemState(ctx, "context-platform", "e2e-sa-owners-roundtrip",
		model.ItemDone, pollutedClose); err != nil {
		t.Fatalf("오염된 close_reason 심기 실패: %v", err)
	}

	// ── 전제를 결과보다 **먼저** 단정한다 ──
	// 옛 상태가 실제로 안 만들어졌으면 아래 단정은 아무것도 안 지킨다.
	for _, id := range []string{jItemID, jFollowups} {
		if n := countLinks(t, s, id); n != 0 {
			t.Fatalf("전제가 깨졌다 — 삼킴 판단 %s 이 링크를 %d개 갖고 있다. 0이어야 이 시험이 본다", id, n)
		}
	}
	beforeLinks := countAllLinks(t, s)
	if err := s.Close(); err != nil {
		t.Fatalf("닫기 실패: %v", err)
	}

	// ── 증분을 태운다 ──
	mustMigrateTo(t, path, prev+1)
	s2, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("적용 뒤 열기 실패: %v", err)
	}
	t.Cleanup(func() { s2.Close() })

	// ── ① 되살아난 링크가 **읽는 경로**에 닿는다 ──
	//
	// 수가 아니라 **어느 판단이** 닿았는지를 본다. 수만 세면 엉뚱한 판단이 걸려도 통과한다.
	want := map[string][]string{
		"owner-audit-worm-blocks-e2e-reset": {jItemID, jFollowups}, // 둘 다 이 항목을 가리킨다
		"e2e-assert-no-firing-alerts":       {jFollowups},
		"e2e-sa-owner-last-effective-guard": {jFollowups},
	}
	for itemID, wantIDs := range want {
		js, err := s2.JudgmentsForItem(ctx, "context-platform", itemID)
		if err != nil {
			t.Fatalf("항목의 판단 조회 실패(%s): %v", itemID, err)
		}
		got := map[string]bool{}
		for _, j := range js {
			got[j.ID] = true
		}
		for _, id := range wantIDs {
			if !got[id] {
				t.Errorf("되살아난 링크가 읽는 경로에 안 닿았다 — 항목 %s 에 판단 %s 이 없다 (조회 %d건)",
					itemID, id, len(js))
			}
		}
	}

	// ── ② close_reason 은 한 글자도 안 변했다 ★ ──
	var gotClose string
	if err := s2.db.QueryRowContext(ctx,
		`SELECT close_reason FROM item WHERE project='context-platform' AND id='e2e-sa-owners-roundtrip'`,
	).Scan(&gotClose); err != nil {
		t.Fatalf("close_reason 확인 실패: %v", err)
	}
	if gotClose != pollutedClose {
		t.Errorf("close_reason 이 변했다 — 이것이 잃은 판단 제목의 유일한 사본이다.\n"+
			"  전: %q\n  후: %q", pollutedClose, gotClose)
	}

	// ── ③ 판단 본문은 안 변했다(judgment 는 추가 전용이다) ──
	var body string
	if err := s2.db.QueryRowContext(ctx, `SELECT body FROM judgment WHERE id=?`, jItemID).Scan(&body); err != nil {
		t.Fatalf("판단 본문 확인 실패: %v", err)
	}
	if body != "삼킨 인자가 값 안으로 들어온 판단이다. 링크가 하나도 안 걸렸다." {
		t.Errorf("판단 본문이 변했다 — 이 증분은 judgment 를 읽기만 해야 한다: %q", body)
	}

	// ── ④ 늘어난 링크는 정확히 심은 만큼이다 ──
	//
	// 증분은 21쌍으로 돌지만 이 DB 에는 그중 4쌍의 판단·항목만 있다
	// (jItemID→1 · jFollowups→3). 나머지는 조인이 0행이라 안 들어간다 — 그 부분성이
	// 신규 설치 DB 에서 이 증분이 no-op 인 것과 **같은 조건**이다.
	if got, want := countAllLinks(t, s2)-beforeLinks, 4; got != want {
		t.Errorf("늘어난 링크가 %d행이다. %d행이어야 한다 — 이 DB 에 있는 판단·항목 쌍만큼", got, want)
	}

	// ── ⑤ 되살린 링크는 나머지와 **같은 모양**이다(target_project 가 NULL) ──
	var nonNull int
	if err := s2.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM judgment_link WHERE judgment_id IN (?, ?) AND target_project IS NOT NULL`,
		jItemID, jFollowups).Scan(&nonNull); err != nil {
		t.Fatalf("target_project 확인 실패: %v", err)
	}
	if nonNull != 0 {
		t.Errorf("되살린 링크 %d행에 target_project 가 들어 있다 — 판단과 대상이 같은 프로젝트라 "+
			"코드가 만들었으면 NULL 이 들어갔을 자리다(model.JudgmentLink.TargetProject 의 규약)", nonNull)
	}

	// ── ⑥ 두 번 돌아도 같다 ──
	//
	// ★ 증분은 schema_version 으로 정확히 한 번만 도므로 멱등은 **필수가 아니라 머리말이
	//   한 주장**이다(007 자신의 주석이 "멱등이 아니어도 족하다"를 적어 뒀다). 그래서 이
	//   단정이 없으면 다음 사람이 NOT EXISTS 를 군더더기로 읽고 뗀다 — 실제로 그 변이를
	//   넣어 봤더니 위 ①~⑤ 어느 것도 안 잡았다(2026-08-23). 그리고 그때 깨지는 자리는
	//   `fd migrate --rollback` 뒤의 재적용이라, 깨진 것을 보는 사람은 이미 사고 중이다.
	before2 := countAllLinks(t, s2)
	if _, err := s2.db.ExecContext(ctx, migrationRecoverSwallowedLinks); err != nil {
		t.Fatalf("증분을 두 번째로 태우는 데 실패했다 — 멱등이 아니다: %v", err)
	}
	if got := countAllLinks(t, s2); got != before2 {
		t.Errorf("두 번째 적용에서 링크가 %d행 → %d행이 됐다. 멱등이어야 한다", before2, got)
	}
}

// 012 는 그 판단이 없는 DB 에서 아무것도 안 한다.
//
// ★ 이것이 조건부로 쓴 이유의 시험이다. judgment_link.judgment_id 에는
// REFERENCES judgment(id) 가 걸려 있어, 조건 없이 VALUES 로 밀어 넣으면 신규 설치에서
// FK 위반으로 **적용 자체가 실패한다** — 즉 이 시험이 빨개지는 방식은 "링크가 생긴다"가
// 아니라 보통 "새 DB 를 못 연다"다.
func TestMigration012IsNoOpOnAFreshDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	mustMigrate(t, path)

	raw, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("raw 열기 실패: %v", err)
	}
	defer raw.Close()

	var links, judgments int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM judgment_link`).Scan(&links); err != nil {
		t.Fatalf("링크 수 확인 실패: %v", err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM judgment`).Scan(&judgments); err != nil {
		t.Fatalf("판단 수 확인 실패: %v", err)
	}
	if judgments != 0 {
		t.Fatalf("전제가 깨졌다 — 새 DB 에 판단이 %d건 있다", judgments)
	}
	if links != 0 {
		t.Errorf("새 DB 에 링크가 %d행 생겼다 — 012 는 판단이 없으면 아무것도 안 넣어야 한다", links)
	}
}

func countLinks(t *testing.T, s *Store, judgmentID string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM judgment_link WHERE judgment_id = ?`, judgmentID).Scan(&n); err != nil {
		t.Fatalf("링크 수 확인 실패(%s): %v", judgmentID, err)
	}
	return n
}

func countAllLinks(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM judgment_link`).Scan(&n); err != nil {
		t.Fatalf("전체 링크 수 확인 실패: %v", err)
	}
	return n
}
