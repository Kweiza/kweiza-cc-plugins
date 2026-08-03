package legacy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 시험은 **실물 픽스처**로 돈다. testdata/legacy/ 는 원본에서 그대로 복사한 파일이다
// (세션 카드 3 · 큐 항목 6 · 핸드오프 2 · status.html 은 실물 줄 범위를 이어 붙인 것).
// 손으로 지어낸 문자열만 쓰면 "내가 만든 걸 내가 파싱한다"가 되고, 그러면 시험이
// 원본의 실제 모양(비규약 절 · 쉼표 paths · 규약 밖 파일명)을 원리적으로 못 본다.

const (
	fxRoot = "testdata/legacy"
	fxCode = fxRoot + "/code"
	fxDocs = fxRoot + "/docs"
)

func readFixture(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fxRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("픽스처를 읽지 못했다(%s): %v", rel, err)
	}
	return b
}

// ─────────────────────────────────────────────────────────────────────────────
// 비규약 절 — 이름과 본문 둘 다 보존되는가
// ─────────────────────────────────────────────────────────────────────────────

func TestParseSessionCardKeepsNonCanonicalSections(t *testing.T) {
	cases := []struct {
		file    string
		section string
	}{
		{"7.md", "실측 기록"},
		{"7.md", "범위에서 뺀 것"},
		{"dash.md", "지금"}, // 규약은 `## 지금 하는 것` 인데 실무가 줄여 썼다
	}
	for _, c := range cases {
		raw := readFixture(t, "code/.claude/sessions/"+c.file)

		// ★ 대조가 성립했는지 **먼저** 단정한다. 픽스처에 그 절이 실제로 없으면
		//   아래 단정은 "보존됐다"가 아니라 "찾을 것이 없었다"를 통과시킨다.
		if !strings.Contains(string(raw), "\n## "+c.section+"\n") {
			t.Fatalf("전제가 깨졌다 — 픽스처 %s 에 `## %s` 절이 없다. "+
				"이 시험은 그 절이 보존되는지를 보는 것이라 전제가 없으면 아무것도 못 본다", c.file, c.section)
		}

		card, rs := ParseSessionCard(c.file, raw)
		for _, r := range rs {
			if r.Fatal {
				t.Fatalf("%s: 통째로 거절됐다 — [%s] %s", c.file, r.Code, r.Detail)
			}
		}
		body := card.SectionBody(c.section)
		if body == "" {
			var got []string
			for _, s := range card.Sections {
				got = append(got, s.Name)
			}
			t.Fatalf("%s: `## %s` 절이 사라졌다. 파싱된 절: %v", c.file, c.section, got)
		}
		// 본문도 원문 그대로여야 한다 — 이름만 남기고 본문을 버리면 같은 소실이다.
		if !strings.Contains(string(raw), body) {
			t.Errorf("%s: `## %s` 의 본문이 원문과 다르다(앞 80자: %q)",
				c.file, c.section, clip(body, 80))
		}
		// 그리고 이 절들은 **분류되지 않는다**. 그 사실이 값으로 나와야 한다.
		if _, canonical := SectionKind(c.section); canonical && c.section != "지금" {
			t.Errorf("`%s` 가 규약 절로 분류됐다 — 규율의 4절이 아니다", c.section)
		}
	}
}

func TestSectionKindClassifiesCanonicalFour(t *testing.T) {
	for _, name := range []string{"지금 하는 것", "다음", "막힘", "다른 트랙에 요청"} {
		if _, ok := SectionKind(name); !ok {
			t.Errorf("규약 절 %q 가 분류되지 않았다", name)
		}
	}
	if _, ok := SectionKind("실측 기록"); ok {
		t.Error("`실측 기록` 은 규약 밖인데 분류됐다고 나왔다 — 그러면 dry-run 이 그것을 나열하지 않는다")
	}
}

func TestSessionStateOfKeepsReasonForLanding(t *testing.T) {
	st, why, ok := SessionStateOf("landing")
	if !ok || why == "" {
		t.Fatalf("landing 이 사유 없이 옮겨졌다(state=%q why=%q ok=%v) — "+
			"바꿔 넣었다는 사실이 안 남으면 되쓰기가 무엇을 잃는지 말할 수 없다", st, why, ok)
	}
	if _, why, ok := SessionStateOf("zzz"); ok || why == "" {
		t.Errorf("모르는 상태가 사유 없이 통과했다(why=%q ok=%v)", why, ok)
	}
	if _, why, ok := SessionStateOf("done"); !ok || why != "" {
		t.Errorf("done 은 그대로 옮겨져야 한다(why=%q ok=%v)", why, ok)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 쉼표 paths — 거절되는가, 그리고 쪼개지지 않는가
// ─────────────────────────────────────────────────────────────────────────────

func TestParseQueueItemRejectsCommaPathsAndDoesNotSplit(t *testing.T) {
	for _, file := range []string{"dash-real-data-render.md", "t5-issuer-aud-literal-drift.md"} {
		raw := readFixture(t, "code/.claude/queue/items/"+file)

		// 전제: 픽스처의 paths 가 실제로 쉼표 구분인가.
		var pathsLine string
		for _, l := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(l, "paths:") {
				pathsLine = l
				break
			}
		}
		if !strings.Contains(pathsLine, ",") {
			t.Fatalf("전제가 깨졌다 — 픽스처 %s 의 paths 에 쉼표가 없다(%q)", file, pathsLine)
		}

		it, rs := ParseQueueItem("items", file, raw)
		var found *Reject
		for i := range rs {
			if rs[i].Code == "paths_comma" {
				found = &rs[i]
			}
		}
		if found == nil {
			t.Fatalf("%s: 쉼표 paths 가 거절되지 않았다(거절 %d건)", file, len(rs))
		}
		if !found.Fatal {
			t.Errorf("%s: 쉼표 paths 거절이 Fatal 이 아니다 — 그러면 항목이 반쯤 들어간다", file)
		}
		if len(it.Paths) != 0 {
			t.Errorf("%s: paths 를 쪼개 줬다(%v) — 조용히 고치면 그것이 또 하나의 손 기재다", file, it.Paths)
		}
	}
}

func TestParseQueueItemReadsRealItem(t *testing.T) {
	raw := readFixture(t, "code/.claude/queue/items/contracts-batch8.md")
	it, rs := ParseQueueItem("items", "contracts-batch8.md", raw)
	for _, r := range rs {
		if r.Fatal {
			t.Fatalf("실물 항목이 거절됐다 — [%s] %s", r.Code, r.Detail)
		}
	}
	if it.ID != "contracts-batch8" || it.Repo != "code" || it.Track != "contracts" {
		t.Errorf("frontmatter 를 잘못 읽었다: id=%q repo=%q track=%q", it.ID, it.Repo, it.Track)
	}
	if len(it.Paths) != 1 || it.Paths[0] != "contracts/" {
		t.Errorf("paths=%v (기대 [contracts/])", it.Paths)
	}
	if it.Handoff != ".claude/handoffs/2026-07-30-0923-contracts-batch7-landed.md" {
		t.Errorf("handoff=%q", it.Handoff)
	}
	// 본문은 꼬리 필드까지 **원문 그대로**여야 한다 — 되쓰기가 그것을 그대로 낸다.
	if !strings.HasSuffix(string(raw), it.Body) {
		t.Error("본문이 원문 꼬리와 다르다 — 되쓰기가 원본과 다른 파일을 낸다")
	}
	// 그리고 비규약 절(항목 본문의 `## …`)도 본문 안에 그대로 있어야 한다.
	if !strings.Contains(it.Body, "## 2026-08-03 정합성 감사") {
		t.Error("항목 본문의 절이 사라졌다")
	}
}

func TestParseQueueItemReadsTailFields(t *testing.T) {
	raw := readFixture(t, "code/.claude/queue/done/batch7-design-doc.md")
	it, rs := ParseQueueItem("done", "batch7-design-doc.md", raw)
	for _, r := range rs {
		if r.Fatal {
			t.Fatalf("거절됐다 — [%s] %s", r.Code, r.Detail)
		}
	}
	if it.LandedSHA != "71f52b4c8d1e097a3b6f24e5d8c0a917be34f6d2" {
		t.Errorf("landed_sha=%q", it.LandedSHA)
	}
	if it.Closed.IsZero() {
		t.Error("closed 를 못 읽었다")
	}

	// 폐기 항목은 사유가 없으면 못 들어간다(스키마 CHECK 와 같은 규율).
	draw := readFixture(t, "code/.claude/queue/dropped/t1-cover-fallback-removal.md")
	dit, _ := ParseQueueItem("dropped", "t1-cover-fallback-removal.md", draw)
	if !strings.Contains(dit.DroppedReason, "after 를 잘못 걸었다") {
		t.Errorf("dropped_reason 을 못 읽었다: %q", clip(dit.DroppedReason, 80))
	}
}

// tailFields 는 본문 한복판의 같은 모양 줄을 값으로 잡으면 안 된다.
func TestTailFieldsStopsAtProse(t *testing.T) {
	body := "본문이다.\nclosed: 이건 산문 한복판이라 꼬리 필드가 아니다\n더 있는 산문\n\nlanded_sha: abc\nclosed: 2026-01-01T00:00:00+09:00\n"
	got := tailFields(body)
	if got["landed_sha"] != "abc" || got["closed"] != "2026-01-01T00:00:00+09:00" {
		t.Fatalf("꼬리 필드를 못 읽었다: %v", got)
	}
	if strings.Contains(got["closed"], "산문") {
		t.Error("산문 한복판의 줄을 값으로 잡았다")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 핸드오프 — 규약 밖 파일명은 mtime 폴백
// ─────────────────────────────────────────────────────────────────────────────

func TestHandoffTimeFilenameThenMtime(t *testing.T) {
	mtime := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

	at, from := HandoffTime("2026-07-30-0923-contracts-batch7-landed.md", mtime)
	if from != "filename" {
		t.Fatalf("규약 파일명인데 축이 %q 다", from)
	}
	if want := time.Date(2026, 7, 30, 9, 23, 0, 0, KST).UTC(); !at.Equal(want) {
		t.Errorf("시각 %s (기대 %s) — KST 로 안 읽으면 아홉 시간이 밀린다", at, want)
	}

	for _, name := range []string{
		"2026-07-31-CONTRACT-HANDOFF-raw-replay.md",
		"_wip-2026-08-03-t4-flags-rulings.md",
	} {
		at, from := HandoffTime(name, mtime)
		if from != "mtime" {
			t.Errorf("%s: 규약 밖인데 축이 %q 다 — 부분 일치로 잡으면 시각이 조용히 엉뚱해진다", name, from)
		}
		if !at.Equal(mtime) {
			t.Errorf("%s: mtime 폴백이 %s 다(기대 %s)", name, at, mtime)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 대시보드 경계 — 줄 전체 정규식 + 매치 정확히 1개
// ─────────────────────────────────────────────────────────────────────────────

func TestExtractDataBlockUsesFullLineBoundary(t *testing.T) {
	raw := string(readFixture(t, "docs/slides/status.html"))

	// ★ 전제: 이 픽스처에 "DATA 블록 끝" 이 **2회 이상** 있어야 한다.
	//   1회뿐이면 부분 문자열로 찾아도 통과하므로 이 시험이 자기가 막는다는 축을 못 본다.
	if n := strings.Count(raw, "DATA 블록 끝"); n < 2 {
		t.Fatalf("전제가 깨졌다 — 픽스처에 \"DATA 블록 끝\"이 %d회뿐이다. "+
			"그 문구를 인용한 산문이 있어야 경계 판정을 실제로 시험할 수 있다", n)
	}

	block, err := ExtractDataBlock(raw)
	if err != nil {
		t.Fatalf("경계를 못 잡았다: %v", err)
	}
	if !strings.HasPrefix(block, "{") || !strings.HasSuffix(strings.TrimSpace(block), "}") {
		t.Fatalf("블록이 객체가 아니다(앞 40자 %q · 뒤 40자 %q)",
			clip(block, 40), clip(tailOf(block, 40), 40))
	}
	// 부분 문자열로 찾았다면 카드 본문(495행 근처)에서 잘려 issues 까지 못 왔을 것이다.
	if !strings.Contains(block, "issues: [") {
		t.Error("블록이 issues 앞에서 잘렸다 — 인용된 문구가 경계를 앞으로 옮겼다")
	}
	// 반대로 그리기 코드가 딸려 들어오면 안 된다.
	if strings.Contains(block, "document.getElementById") {
		t.Error("블록에 그리기 코드가 섞였다 — 경계가 뒤로 밀렸다")
	}
}

func TestExtractDataBlockRefusesAmbiguousBoundary(t *testing.T) {
	raw := string(readFixture(t, "docs/slides/status.html"))

	// 실물 끝 마커를 그대로 한 줄 더 넣는다 = 매치 2개.
	marker := "/* ═══════════════ DATA 블록 끝 — 아래는 그리기 코드입니다 ═══════════════ */"
	if !strings.Contains(raw, "\n"+marker+"\n") {
		t.Fatalf("전제가 깨졌다 — 픽스처에 끝 마커 줄이 그대로 없다")
	}
	doubled := strings.Replace(raw, "\n"+marker+"\n", "\n"+marker+"\n"+marker+"\n", 1)
	if _, err := ExtractDataBlock(doubled); err == nil {
		t.Fatal("끝 마커가 2개인데 통과했다 — 어느 쪽이 진짜인지 알 수 없으므로 거절해야 한다")
	} else if !strings.Contains(err.Error(), "정확히 1개") {
		t.Errorf("사유가 매치 개수를 말하지 않는다: %v", err)
	}

	if _, err := ExtractDataBlock(strings.Replace(raw, "const DATA = {", "const DATA2 = {", 1)); err == nil {
		t.Fatal("시작 마커가 0개인데 통과했다")
	}
}

func TestParseDashboardReadsFourAxes(t *testing.T) {
	block, err := ExtractDataBlock(string(readFixture(t, "docs/slides/status.html")))
	if err != nil {
		t.Fatalf("경계: %v", err)
	}
	d, rs := ParseDashboard(block)
	for _, r := range rs {
		if r.Fatal {
			t.Fatalf("실물 DATA 가 통째로 거절됐다 — [%s] %s %s", r.Code, r.Path, r.Detail)
		}
	}
	if len(d.Landings) != 5 || len(d.Parts) != 3 || len(d.Issues) != 6 || len(d.Blockers) != 3 {
		t.Fatalf("축 개수가 다르다: landings=%d parts=%d issues=%d blockers=%d",
			len(d.Landings), len(d.Parts), len(d.Issues), len(d.Blockers))
	}
	if d.Judged.SHA != "5e83926" || d.Judged.At != "2026-08-03" {
		t.Errorf("judged=%+v", d.Judged)
	}
	if d.Parts[0].Pct != 75 || d.Parts[0].Name != "계약 — 스키마 · API 스펙" {
		t.Errorf("parts[0]=%+v", d.Parts[0])
	}
	if d.Landings[0].Commit != "8206c5a" {
		t.Errorf("landings[0].commit=%q", d.Landings[0].Commit)
	}
	// ★ `body` 와 `note` 는 같은 자리의 두 이름이다. 실물 70건 중 41건이 `note` 만 갖고 있어서
	//   `body` 를 필수로 두면 그 41건이 통째로 거절된다 — 형식 위반이 아니라 그냥 옛 이름이다.
	//   픽스처의 세 번째 랜딩이 그 모양이다.
	if d.Landings[2].Body != "" {
		t.Fatalf("전제가 깨졌다 — 픽스처의 세 번째 랜딩에 body 가 있다(note 전용이어야 한다)")
	}
	if strings.TrimSpace(d.Landings[2].Note) == "" {
		t.Error("note 만 있는 랜딩의 서사가 사라졌다")
	}
	// `sessions` 는 일부러 안 담는다. 그 사실이 값으로 나와야 한다.
	var sawSessions bool
	for _, s := range d.Skipped {
		if strings.HasPrefix(s, "sessions ") {
			sawSessions = true
		}
	}
	if !sawSessions {
		t.Errorf("`sessions` 를 안 담는다는 사실이 Skipped 에 없다 — 침묵하면 '판단해서 뺐다'와 '못 봤다'가 같아진다(%v)", d.Skipped)
	}
	// evidence 는 지어내지 않고 judged 에서 만든다.
	ev := PartEvidence(d.Judged, d.Parts[0])
	if !strings.Contains(ev, "5e83926") || !strings.Contains(ev, "2026-08-03") {
		t.Errorf("근거 문자열에 전수 판정 좌표가 없다: %q", ev)
	}
}

func TestParseDashAtIsKST(t *testing.T) {
	got, err := ParseDashAt("2026-08-03 18:15")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 3, 18, 15, 0, 0, KST).UTC()
	if !got.Equal(want) {
		t.Errorf("%s (기대 %s) — UTC 로 읽으면 아홉 시간이 통째로 밀린다", got, want)
	}
	if _, err := ParseDashAt("2026-08-03T18:15:00Z"); err == nil {
		t.Error("다른 표기를 조용히 받았다")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// JS 리터럴 파서 — 조용히 넘기는 자리가 없는가
// ─────────────────────────────────────────────────────────────────────────────

func TestParseJSObjectSubset(t *testing.T) {
	m, err := ParseJSObject(`{
		// 줄 주석
		a: 'x\x3cy', /* 블록 주석 */
		b: [1, -2.5, null, true,],
		c: { d: "겹따옴표도 받는다" },
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if m["a"] != "x<y" {
		t.Errorf("\\x3c 이스케이프를 못 풀었다: %q", m["a"])
	}
	if len(m["b"].([]any)) != 4 {
		t.Errorf("배열=%v", m["b"])
	}
}

func TestParseJSObjectRefusesSilentLoss(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"모르는 이스케이프", `{a: 'x\q'}`, "모르는 이스케이프"},
		{"중복 키", `{a: 1, a: 2}`, "두 번"},
		{"문자열 안 줄바꿈", "{a: 'x\ny'}", "줄이 바뀌었다"},
		{"객체 뒤 잔여물", `{a: 1} 뒤에 뭔가`, "더 있다"},
	}
	for _, c := range cases {
		_, err := ParseJSObject(c.src)
		if err == nil {
			t.Errorf("%s: 통과했다 — 조용히 넘기면 그 값이 흔적 없이 사라진다", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: 사유가 %q 를 말하지 않는다 — %v", c.name, c.want, err)
		}
	}
}
