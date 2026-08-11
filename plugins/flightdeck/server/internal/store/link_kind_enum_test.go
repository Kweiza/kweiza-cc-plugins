package store

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// Go 의 열거와 스키마의 CHECK 열거가 **갈리면 판단이 죽는다.**
//
// 갈림의 방향 둘 다 나쁘다:
//
//	· Go 가 넓고 CHECK 가 좁다 → 전단 관문이 통과시킨 값이 **트랜잭션 안**에서 CHECK 로 죽고,
//	  Finish 의 tx 는 판단·후속·종료·반납을 묶으므로 **파생 불가한 판단이 함께 롤백된다.**
//	  이 항목(fd-finish-rolls-back-judgment-on-bad-after-or-link)이 고친 결함이 정확히
//	  그 모양이었다 — 그러니 그 결함을 열거 갈림으로 되살리지 않는다.
//	· Go 가 좁고 CHECK 가 넓다 → DB 는 받는데 코드가 거절한다. 조용하지는 않지만
//	  ValidateLink 를 안 타는 경로에서만 쓸 수 있는 값이 생겨 계약이 두 벌이 된다.
//
// 그래서 사람이 두 자리를 함께 고치는 규율에 기대지 않고 기계가 본다.
func TestLinkTargetKindsMatchSchemaCheck(t *testing.T) {
	p := filepath.Join("schema.sql")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%s 를 못 읽었다 — 이 시험의 좌표가 틀렸다: %v", p, err)
	}
	src := string(b)

	// judgment_link 의 target_kind CHECK 절만 집는다. 다른 표의 CHECK 를 잡으면
	// 이 시험이 엉뚱한 열거를 지키게 된다.
	re := regexp.MustCompile(`target_kind\s+TEXT\s+NOT\s+NULL\s+CHECK\s*\(\s*target_kind\s+IN\s*\(([^)]*)\)`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("schema.sql 에서 judgment_link.target_kind 의 CHECK 열거를 못 찾았다 — " +
			"스키마가 그 모양을 바꿨으면 이 시험의 정규식을 따라 고쳐라. " +
			"찾지 못한 것을 통과로 접으면 이 관문이 침묵한다")
	}

	var fromSchema []string
	for _, raw := range strings.Split(m[1], ",") {
		s := strings.TrimSpace(raw)
		s = strings.Trim(s, "'\"")
		if s != "" {
			fromSchema = append(fromSchema, s)
		}
	}
	if len(fromSchema) == 0 {
		t.Fatalf("CHECK 열거를 파싱했는데 값이 0개다(원문 %q) — 빈 집합은 어떤 대조도 통과시킨다", m[1])
	}

	got := append([]string(nil), LinkTargetKinds...)
	sort.Strings(got)
	sort.Strings(fromSchema)
	if strings.Join(got, ",") != strings.Join(fromSchema, ",") {
		t.Errorf("열거가 갈렸다.\n  Go(LinkTargetKinds): %v\n  schema.sql CHECK   : %v\n"+
			"넓은 쪽이 Go 면 전단 관문이 통과시킨 값이 tx 안에서 죽어 **판단이 롤백된다.**",
			got, fromSchema)
	}
}

// ValidateLink 자체의 판별력 — 열거 안·밖·빈 값 셋.
//
// 이 시험이 없으면 ValidateLink 가 늘 nil 을 돌려주도록 망가져도 위 시험은 초록이다
// (저쪽은 열거 **목록**만 대조하고 함수의 행동은 안 본다).
func TestValidateLinkSeparatesKnownFromUnknown(t *testing.T) {
	for _, k := range LinkTargetKinds {
		if err := ValidateLink(model.JudgmentLink{TargetKind: k, TargetID: "x"}); err != nil {
			t.Errorf("열거 안의 %q 를 거절했다: %v", k, err)
		}
	}
	bad := []struct {
		name, kind, id string
	}{
		{"열거 밖", "pull-request", "42"},
		{"대문자는 다른 값이다", "Item", "x"},
		{"빈 kind", "", "x"},
		{"공백만 든 kind", "   ", "x"},
		{"빈 id", "item", ""},
		{"공백만 든 id", "item", "  "},
	}
	for _, c := range bad {
		if err := ValidateLink(model.JudgmentLink{TargetKind: c.kind, TargetID: c.id}); err == nil {
			t.Errorf("%s(kind=%q id=%q)를 통과시켰다 — 이 값은 CHECK 에서 죽고, "+
				"그 자리가 트랜잭션 안이면 판단이 함께 사라진다", c.name, c.kind, c.id)
		}
	}
}
