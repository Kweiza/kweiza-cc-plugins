package store

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// TestProjectRefCountsCountsRealRows 는 ProjectRefCounts 가 실제 행을 실제로 세는지 본다.
//
// ★ 도달하는 시험이 이미 둘 있다(service.TestListProjectSummariesCounts ·
// cmd/fd.TestProjectLsPrintsAxisAndCounts). 그런데 그 둘은 item 을 뺀 나머지 표가 늘
// 0건이라, `Sessions: counts["item"]` 처럼 카운트 배선이 뒤바뀌어도 안 걸린다 — 0과
// 0을 비교해서는 뒤바뀐 것을 못 잡는다. 이 시험은 표 셋(session·landing_queue·claim)에
// 정확히 1행씩 만들고 **그 표 이름으로 직접** 잰다. claim 을 고른 이유: 리뷰가 지적한
// 대로 landing_queue 보다 흔한 경로이고(항목을 한 번이라도 선점하면 생긴다),
// projectRefTables 에 새로 들어온 표라 검증이 아예 없었다.
func TestProjectRefCountsCountsRealRows(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	seed(t, st, "p") // project "p" + machine "m1" 을 넣는다(store_test.go)

	sess, _, err := st.OpenSession(ctx, "p", "m1", "/wt", "cc1", "", time.Time{})
	if err != nil {
		t.Fatalf("세션 열기 실패: %v", err)
	}
	if err := st.AddItem(ctx, model.Item{
		Project: "p", ID: "i1", Title: "t", Body: "b", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("항목 등록 실패: %v", err)
	}
	if _, err := st.ClaimItem(ctx, "p", "i1", sess.ID, time.Time{}); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	if err := st.Tx(ctx, func(tx *Tx) error {
		_, err := tx.EnqueueLanding("p", sess.ID, time.Now().UTC())
		return err
	}); err != nil {
		t.Fatalf("랜딩 줄 등록 실패: %v", err)
	}

	counts, err := st.ProjectRefCounts(ctx, "p")
	if err != nil {
		t.Fatalf("ProjectRefCounts 실패: %v", err)
	}
	for _, tbl := range []string{"session", "landing_queue", "claim"} {
		if counts[tbl] != 1 {
			t.Fatalf("%s 카운트 %d, 기대 1 — 전체: %v", tbl, counts[tbl], counts)
		}
	}
}

// TestProjectRefTablesOrdersSessionAfterItsNonCascadingReferers 는 projectRefTables 가
// 스스로 못박은 "삭제 순서이기도 하다 — 자식부터 부모 순이다"를 정적으로 잰다.
//
// ★ landing_queue · claim · resource_hold · job 넷은 session_id 로 session(id) 를
// CASCADE 없이 참조한다(schema.sql). 이 넷 중 하나라도 session 뒤에 남아 있으면, 다음
// 태스크(rm)가 이 목록 순서 그대로 DELETE 를 돌릴 때 session 삭제가 FK 위반으로 막힌다 —
// 이 리뷰 회차 전까지 정확히 그 상태였다(resource_hold·job 이 session 뒤 10·11번째였다).
// TestProjectRefCountsCountsRealRows 와 TestProjectRefTablesCoverEveryProjectColumn 은
// **집합**만 보고 **순서**는 안 본다 — 이 시험이 그 빈틈을 잠근다.
func TestProjectRefTablesOrdersSessionAfterItsNonCascadingReferers(t *testing.T) {
	idx := make(map[string]int, len(projectRefTables))
	for i, tbl := range projectRefTables {
		idx[tbl] = i
	}
	sessionIdx, ok := idx["session"]
	if !ok {
		t.Fatal("session 이 projectRefTables 에 없다")
	}
	for _, tbl := range []string{"landing_queue", "claim", "resource_hold", "job"} {
		i, ok := idx[tbl]
		if !ok {
			t.Fatalf("%s 가 projectRefTables 에 없다 — session_id 로 session(id) 를 "+
				"CASCADE 없이 참조하므로 목록에 있어야 한다", tbl)
		}
		if i >= sessionIdx {
			t.Fatalf("%s(순번 %d)가 session(순번 %d) 보다 뒤에 있다 — session 삭제 시점에 "+
				"아직 이 표에 그 세션을 가리키는 행이 남아 FK 위반이 난다", tbl, i, sessionIdx)
		}
	}
}

// knownProjectRefTables 는 project 컬럼을 가진 표 중 projectRefTables 밖에 있는 것과 그
// 이유다. TestProjectRefTablesCoverEveryProjectColumn 가 이 목록과 projectRefTables 의
// 합집합이 "project 컬럼을 가진 표 전부"와 같은 집합인지를 실제 스키마로 잰다.
//
// ("project" 자기 자신은 여기 없다 — project.id 를 project 컬럼처럼 세는 것 자체가
// 의미가 없어서 애초에 후보에서 뺀다. 아래 시험이 그 자리를 따로 처리한다.)
var knownProjectRefTables = map[string]string{
	"event": "ProjectRefCounts 가 별도 질의로 여전히 센다(project.go 의 그 자리) — " +
		"projectRefTables 밖이라고 안 세는 것이 아니다. FK 가 아니라 컬럼이라 이 목록 " +
		"(삭제 순서)에는 안 두지만, 셈에서는 안 빠진다.",
	"item_after": "(project, item_id) 로 item(project, id) 를 ON DELETE CASCADE 로 " +
		"참조한다 — item 삭제에 자동으로 딸려 사라진다. claim 과 달리 session 을 안 봐서 " +
		"session 삭제 순서에도 안 걸린다(claim 은 그래서 이번에 목록에 들어왔다).",
}

// TestProjectRefTablesCoverEveryProjectColumn 은 project 컬럼을 가진 표 전부가
// projectRefTables 아니면 knownProjectRefTables 어느 한쪽에는 있는지, **살아 있는 DB 의
// 스키마**로 대조한다.
//
// ★ schema.sql 을 정규식으로 읽지 않는다. landing_queue 가 이 목록에서 처음에 빠졌던
// 결함이 정확히 "파일을 눈으로/정규식으로 훑는" 방식의 산물이었다 — 그 표는 증분
// (internal/store/migrations/003_landing_queue.sql)에 있고 schema.sql 자체에는 없다.
// sqlite_master + PRAGMA table_info 는 마이그레이션을 전부 적용한 뒤의 실제 스키마를
// 보므로 증분에서 생긴 표도, 앞으로 생길 표도 자동으로 걸린다 — 사람이 목록을 손으로
// 갱신하는 것을 잊어도 이 시험이 대신 빨개진다.
func TestProjectRefTablesCoverEveryProjectColumn(t *testing.T) {
	ctx := context.Background()
	st := newStore(t) // OpenWithLogger 가 이미 schema.sql + 증분 전부를 적용한다

	rows, err := st.db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatalf("표 목록 조회 실패: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("표 이름 스캔 실패: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("표 목록 순회 실패: %v", err)
	}
	rows.Close()

	var withProject []string
	for _, tbl := range tables {
		// ★ PRAGMA 문은 자리표시자 바인딩을 지원하지 않는다 — 표 이름은 위에서 방금
		// sqlite_master 가 낸 실제 표 이름이라 외부 입력이 아니다(project.go 의
		// ProjectRefCounts 가 같은 근거로 문자열 결합을 쓰는 것과 같은 사정이다).
		cols, err := st.db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, tbl))
		if err != nil {
			t.Fatalf("%s 의 컬럼 조회 실패: %v", tbl, err)
		}
		has := false
		for cols.Next() {
			var cid, notnull, pk int
			var name, ctype string
			var dflt any
			if err := cols.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
				cols.Close()
				t.Fatalf("%s 의 컬럼 스캔 실패: %v", tbl, err)
			}
			if name == "project" {
				has = true
			}
		}
		if err := cols.Err(); err != nil {
			t.Fatalf("%s 의 컬럼 순회 실패: %v", tbl, err)
		}
		cols.Close()
		if has {
			withProject = append(withProject, tbl)
		}
	}
	sort.Strings(withProject)

	known := make(map[string]bool, len(projectRefTables)+len(knownProjectRefTables))
	for _, tbl := range projectRefTables {
		known[tbl] = true
	}
	for tbl := range knownProjectRefTables {
		known[tbl] = true
	}

	// project 자신은 project 컬럼이 없다(그것이 곧 id 다) — 후보에 안 나온다. 혹시
	// 스키마가 바뀌어 나오면 그 사실 자체가 놀랄 일이라 알려진 목록에 안 넣고 그대로
	// 잡히게 둔다.
	for _, tbl := range withProject {
		if known[tbl] {
			continue
		}
		t.Errorf("표 %q 가 project 컬럼을 가지는데 projectRefTables 에도 "+
			"knownProjectRefTables 에도 없다 — ProjectRefCounts 가 이 표를 안 세거나, "+
			"rm(다음 태스크)이 이 표에 남은 행을 못 보고 지운다. landing_queue 가 이 시험이 "+
			"없어서 처음에 놓쳤던 바로 그 실패가 다시 난 것이다. project.go 의 "+
			"projectRefTables 나 knownProjectRefTables 에 이유와 함께 추가하라.", tbl)
	}

	// 반대 방향 — 안다고 적은 표 이름이 실제로 project 컬럼을 가진 실재 표인지.
	// 표가 없어지거나 컬럼이 바뀌면(리네임) 목록이 유령을 세게 된다.
	inWithProject := make(map[string]bool, len(withProject))
	for _, tbl := range withProject {
		inWithProject[tbl] = true
	}
	for _, tbl := range projectRefTables {
		if !inWithProject[tbl] {
			t.Errorf("projectRefTables 의 %q 가 실제로는 project 컬럼이 없거나 존재하지 않는다 — "+
				"목록이 낡았다(표가 지워졌거나 컬럼 이름이 바뀌었다)", tbl)
		}
	}
	for tbl, reason := range knownProjectRefTables {
		if !inWithProject[tbl] {
			t.Errorf("knownProjectRefTables 의 %q(%s)가 실제로는 project 컬럼이 없거나 "+
				"존재하지 않는다 — 이유가 낡았다", tbl, reason)
		}
	}
}
