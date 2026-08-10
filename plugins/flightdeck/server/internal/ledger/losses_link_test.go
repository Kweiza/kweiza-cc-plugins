package ledger

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// 링크 대상의 운명은 **스키마가 정한다** — 손실 목록이 그것을 지어내면 안 된다
// ─────────────────────────────────────────────────────────────────────────────
//
// 손실 목록은 이 저장소가 "손실을 코드가 열거하고 시험이 그 목록대로만 잃는지 단정한다"는
// 규율로 만든 것이다. 실제보다 손실을 **크게** 말하는 것도 그 규율 위반이다 — 다음 사람이
// 복원 결과를 잘못 예상한다.
//
// 실제로 한 번 그랬다. 폐포가 닫히면서 session 이 원장에 들어왔는데(Task 10) 손실 목록은
// 여전히 "링크가 가리키는 항목은 복원된 DB 에 없다"를 통째로 말하고 있었다. 실측하면
// 링크 2,805건 중 session 축이 959건(34%)이고 **그 959건은 전부 복원된 DB 에서 자기
// 세션 행을 찾는다.**
//
// ★ 그래서 운명을 산문이 아니라 표로 두고, 그 표를 스키마와 댄다. 규칙은 하나다:
//
//	폐포에 그 이름의 표가 있다        → 복원된다
//	스키마에 있지만 폐포 밖이다        → 링크는 살고 대상은 없다(진짜 손실)
//	스키마에 그런 표가 아예 없다       → 애초에 DB 행이 아니었다(손실이 아니다)
//
// 이 규칙이 있으면 다섯째 target_kind 가 들어오거나 표가 폐포 안팎으로 옮겨갈 때
// 사람이 반드시 판단하게 된다.
func TestLinkTargetKindFatesFollowTheSchema(t *testing.T) {
	schema := readSchemaSQL(t)

	// ① 스키마가 허용하는 target_kind 전부.
	kinds := schemaLinkTargetKinds(t, schema)
	var have []string
	for k := range linkKindFate {
		have = append(have, k)
	}
	sort.Strings(have)
	if strings.Join(have, ",") != strings.Join(kinds, ",") {
		t.Fatalf("링크 대상 종류의 집합이 스키마와 다르다.\n  표: %v\n  스키마: %v\n"+
			"종류가 늘었으면 그 대상이 복원된 DB 에서 발견되는지 판단해 표에 더해라 — "+
			"안 더하면 손실 목록이 그 종류에 대해 아무 말도 안 한다.", have, kinds)
	}

	// ② 스키마의 표 전부와 원장 폐포.
	tables := schemaTableNames(t, schema)
	closure := map[string]bool{}
	for _, n := range store.LedgerTableNames() {
		closure[n] = true
	}
	if len(closure) == 0 {
		t.Fatal("원장 폐포가 비었다 — 이 관문이 아무것도 안 보면서 초록이 된다")
	}

	for _, k := range kinds {
		var want string
		switch {
		case closure[k]:
			want = linkFateRestored
		case tables[k]:
			want = linkFateDangling
		default:
			want = linkFateNotARow
		}
		if got := linkKindFate[k]; got != want {
			t.Errorf("target_kind %q 의 운명이 %q 로 적혀 있는데 스키마상 %q 다.\n"+
				"  폐포 안: %v · 스키마의 표: %v", k, got, want, closure[k], tables[k])
		}
	}
}

// ③ 손실 목록의 문장이 그 표를 실제로 쓰는지 — 표만 고치고 문구가 옛말이면 소용없다.
func TestLossesTextNamesEveryLinkKind(t *testing.T) {
	joined := strings.Join(Losses(), "\n")
	for k := range linkKindFate {
		if !strings.Contains(joined, "`"+k+"`") {
			t.Errorf("손실 목록이 target_kind %q 를 한 번도 안 부른다 — "+
				"사용자는 그 종류의 링크가 복원 후 어떻게 되는지 못 읽는다:\n%s", k, joined)
		}
	}
	// 복원되는 종류를 손실로 부르면 안 된다. 그것이 이 항목이 고치러 온 과대주장이다.
	for k, f := range linkKindFate {
		if f != linkFateRestored {
			continue
		}
		if strings.Contains(joined, "`"+k+"`") && !strings.Contains(joined, linkFateRestored) {
			t.Errorf("복원되는 종류 %q 를 부르면서 %q 라는 말이 목록 어디에도 없다 — "+
				"과대주장이 남아 있다", k, linkFateRestored)
		}
	}
}

func readSchemaSQL(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "store", "schema.sql")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("schema.sql 을 못 읽었다(%s): %v", p, err)
	}
	return string(b)
}

// schemaLinkTargetKinds 는 judgment_link.target_kind 의 CHECK 가 허용하는 값들이다.
func schemaLinkTargetKinds(t *testing.T, schema string) []string {
	t.Helper()
	re := regexp.MustCompile(`target_kind\s+TEXT\s+NOT NULL\s+CHECK\s*\(target_kind IN \(([^)]*)\)\)`)
	m := re.FindStringSubmatch(schema)
	if m == nil {
		t.Fatal("judgment_link.target_kind 의 CHECK 를 못 찾았다 — 이 관문의 좌표가 틀렸다")
	}
	var out []string
	for _, s := range strings.Split(m[1], ",") {
		if s = strings.Trim(strings.TrimSpace(s), "'"); s != "" {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// schemaTableNames 는 schema.sql 이 만드는 표 이름 전부다.
func schemaTableNames(t *testing.T, schema string) map[string]bool {
	t.Helper()
	re := regexp.MustCompile(`(?m)^CREATE TABLE (?:IF NOT EXISTS )?(\w+)`)
	out := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(schema, -1) {
		out[m[1]] = true
	}
	if len(out) == 0 {
		t.Fatal("schema.sql 에서 표를 하나도 못 뽑았다 — 이 관문이 눈이 멀었다")
	}
	return out
}
