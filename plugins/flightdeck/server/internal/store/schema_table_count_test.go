package store

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// declaredTableRe 는 **사람이 선언한** 표를 찾는다.
//
// ★ `IF NOT EXISTS` 를 반드시 건너뛴다. `schema_version` 하나가 그 형태인데,
// `^CREATE TABLE [a-z_]` 같은 순진한 패턴은 그 한 줄을 놓쳐 21 을 20 으로 센다.
// 이 함정으로 세 세션이 각자 다른 수(21·23·28)를 보고했다 — 패턴에 못박아 둔다.
var declaredTableRe = regexp.MustCompile(
	`(?im)^\s*CREATE\s+(VIRTUAL\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z_][A-Za-z0-9_]*)`)

// declaredTables 는 schema.sql 과 증분 전부가 선언하는 표 이름을 정렬해 낸다.
//
// 여기서 세는 것은 **정의가 이 레포에 있는 표**뿐이다. 살아 있는 DB 의 `sqlite_master`
// 는 이보다 큰 수를 내는데(아래 시험의 주석을 보라) 그 차이는 엔진이 만든 것이라
// 우리가 세는 대상이 아니다.
func declaredTables(t *testing.T) []string {
	t.Helper()

	sources := map[string]string{"schema.sql": schemaSQL}
	for _, m := range migrations {
		sources[m.Name] = m.SQL
	}

	seen := make(map[string]string, 32)
	var names []string
	for src, sql := range sources {
		if strings.TrimSpace(sql) == "" {
			t.Fatalf("%s 의 SQL 이 비었다 — //go:embed 가 안 걸렸을 수 있다", src)
		}
		for _, m := range declaredTableRe.FindAllStringSubmatch(sql, -1) {
			name := m[2]
			// ★ 같은 표를 두 자리에서 선언하면 신규 설치와 업그레이드가 다른 모양의
			// DB 를 갖는다(BaseSchemaVersion 주석의 그 경로다). 수를 세기 전에 막는다.
			if prev, dup := seen[name]; dup {
				t.Fatalf("표 %q 가 %s 와 %s 두 자리에서 선언됐다 — 정의는 한 자리에만 둔다", name, prev, src)
			}
			seen[name] = src
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// TestDeclaredTablesMatchDesign 은 DESIGN §3 이 못박은 표 수를 스키마 쪽에서 지킨다.
//
// ★ 왜 문서가 아니라 여기서 지키나 — `TestToolTableIsSix`(mcpsrv/protocol_test.go) 와 같은
// 규율이다. 표를 **더하는 사람**의 빨간불이 여기서 나야 그 사람이 DESIGN 을 같이 고친다.
// 문서를 읽는 시험은 표를 안 더한 사람까지 세우고, 문서 편집이 도는 동안 계속 빨갛다.
//
// 이름 목록을 통째로 못박는다(수만 세지 않는다). 표 하나를 지우고 하나를 더하면
// 수는 그대로인데 데이터 모델은 달라지고, 그 변경이 리뷰에서 안 보이면 안 된다.
func TestDeclaredTablesMatchDesign(t *testing.T) {
	// DESIGN §3 — "SQLite 파일 하나, 테이블 N개, 계층 셋".
	//
	// 살아 있는 DB 의 `sqlite_master` 는 이보다 5 큰 수를 낸다:
	// FTS5 가 judgment_fts 뒤에 자동 생성하는 그림자 표 4개(_config·_data·_docsize·_idx)와,
	// AUTOINCREMENT 이 있으면 생기는 sqlite_sequence 1개다.
	// **그 5개는 우리가 정한 값이 아니다** — fts5 옵션이나 SQLite 판이 바뀌면 조용히 달라진다.
	// 데이터 모델은 사람이 선언한 것이므로 여기서 세는 것은 아래 목록뿐이다.
	want := []string{
		"change_set",
		"claim",
		"counter",
		"event",
		"footprint",
		"idempotency", // 증분 002
		"item",
		"item_after",
		"item_dependents",
		"job",
		"judgment",
		"judgment_fts", // CREATE VIRTUAL TABLE
		"judgment_link",
		"landing_queue", // 증분 003 — 랜딩 순서 큐
		"machine",
		"pick_eval",
		"project",
		"ref_state",
		"resource_hold",
		"schema_version", // CREATE TABLE IF NOT EXISTS — 순진한 패턴이 놓치는 그 한 줄
		"session",
		"session_workspace",
		"signal",
		"snapshot",
	}

	got := declaredTables(t)

	if len(got) != len(want) {
		t.Fatalf("선언된 표가 %d개다 — 이 시험은 %d개를 안다.\n"+
			"표를 더했거나 지웠으면 **DESIGN §3 의 '테이블 N개' 도 같이 고쳐라**"+
			"(그 숫자가 곧 이 레포의 계약이고, 틀리면 아무도 계약을 검산할 수 없다).\n"+
			"실측: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("정렬 %d번 표가 %q다 — 기대 %q.\n"+
				"수는 같은데 목록이 달라졌다(하나를 지우고 하나를 더했다).\n"+
				"실측: %v", i, got[i], want[i], got)
		}
	}
}

// TestSchemaVersionTableIsCounted 는 위 목록이 `IF NOT EXISTS` 한 줄을 계속 세는지 본다.
//
// 이것만 따로 세우는 이유: 표 수가 틀렸던 세 번의 보고가 **전부** 이 한 줄에서 갈렸다.
// 목록 시험이 어떤 이유로 느슨해져도 이 갈래만은 남는다.
func TestSchemaVersionTableIsCounted(t *testing.T) {
	if !strings.Contains(schemaSQL, "CREATE TABLE IF NOT EXISTS schema_version") {
		t.Skip("schema_version 의 선언 형태가 바뀌었다 — 이 시험이 지키던 함정이 사라졌다")
	}
	for _, name := range declaredTables(t) {
		if name == "schema_version" {
			return
		}
	}
	t.Fatal("schema_version 이 안 세졌다 — `IF NOT EXISTS` 를 건너뛰는 패턴이 깨졌다. " +
		"이 상태로 센 수는 실제보다 1 작다")
}
