package legacy

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 대시보드 `slides/status.html` 의 DATA 블록을 읽는다.
//
// 이 파일은 **빌드가 없다.** 값과 그리기 코드가 한 파일 안에 있고 저장하면 곧바로
// 배포된다. 그래서 경계를 잘못 잡으면 값이 통째로 사라지거나 그리기 코드가 값으로
// 읽히는데, 그 두 사고가 이 파일에서 실제로 났다.

var (
	// ★ 줄 전체 정규식이다. 부분 문자열로 찾으면 **그 문구를 인용한 산문이 경계를 옮긴다.**
	//   그 사고가 실재하고(시험 23건이 한꺼번에 깨지고 경계가 DATA 한복판으로 올라갔다),
	//   **지금도 그 조건이 살아 있다** — 실물 status.html 에서 "DATA 블록 끝"은 2회 나오고
	//   그중 하나는 세션 카드 본문이 그 사고를 서술하며 인용한 것이다. 줄 전체로 보면 1회다.
	dataStartRe = regexp.MustCompile(`^const DATA = \{$`)
	dataEndRe   = regexp.MustCompile(`^/\* ═+ DATA 블록 끝 .*\*/$`)
)

// ExtractDataBlock 은 status.html 에서 DATA 객체 리터럴의 원문을 잘라 낸다.
//
// ★ 경계 마커는 **줄 전체 정규식 + 매치 정확히 1개 단정**으로 찾는다.
// 매치가 0개면 원본 형식이 바뀐 것이고 2개 이상이면 어느 쪽이 진짜인지 알 수 없다 —
// 둘 다 "적당히 첫 번째를 쓴다"로 넘기면 조용히 틀린 범위를 읽는다.
func ExtractDataBlock(src string) (string, error) {
	lines := strings.Split(src, "\n")
	var starts, ends []int
	for i, line := range lines {
		l := strings.TrimRight(line, "\r")
		if dataStartRe.MatchString(l) {
			starts = append(starts, i)
		}
		if dataEndRe.MatchString(l) {
			ends = append(ends, i)
		}
	}
	if len(starts) != 1 {
		return "", fmt.Errorf("DATA 시작 마커(`const DATA = {` 줄 전체)가 %d개다 — 정확히 1개여야 한다 "+
			"(찾은 줄: %v)", len(starts), plusOne(starts))
	}
	if len(ends) != 1 {
		return "", fmt.Errorf("DATA 끝 마커(`/* ═… DATA 블록 끝 … */` 줄 전체)가 %d개다 — 정확히 1개여야 한다 "+
			"(찾은 줄: %v)", len(ends), plusOne(ends))
	}
	if ends[0] <= starts[0] {
		return "", fmt.Errorf("DATA 끝 마커(%d행)가 시작 마커(%d행)보다 앞이다", ends[0]+1, starts[0]+1)
	}

	block := strings.Join(lines[starts[0]:ends[0]], "\n")
	block = strings.TrimPrefix(block, "const DATA = ")
	block = strings.TrimRight(block, " \t\r\n")
	if !strings.HasSuffix(block, "};") {
		return "", fmt.Errorf("DATA 블록이 `};` 로 끝나지 않는다 — 끝 %q "+
			"(두 마커 사이에 그리기 코드가 섞여 들어왔을 수 있다)", clip(tailOf(block, 40), 60))
	}
	return strings.TrimSuffix(block, ";"), nil
}

func plusOne(xs []int) []int {
	out := make([]int, len(xs))
	for i, x := range xs {
		out[i] = x + 1
	}
	return out
}

func tailOf(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[len(rs)-n:])
}

// ─────────────────────────────────────────────────────────────────────────────
// DATA 의 네 축 — landings · parts · issues · blockers
// ─────────────────────────────────────────────────────────────────────────────

// Dashboard 는 DATA 블록에서 이관 대상인 것만 담는다.
//
// `sessions`·`locks`·`queue` 는 **일부러 안 담는다** — 셋 다 파생이고 fd 에서는
// 게시판·자원 표·큐가 정본이다. 손으로 베낀 스냅숏을 다시 손 기재로 들여오면
// 이 설계가 없애려던 것이 첫날부터 되살아난다. 그 사실은 dry-run 이 명시한다.
type Dashboard struct {
	AsOf     string
	Judged   Judged
	Landings []Landing
	Parts    []Part
	Issues   []string
	Blockers []BlockerItem
	Skipped  []string // 안 담은 최상위 키
}

// Judged 는 마지막 전수 판정의 좌표다. parts 의 숫자가 **어디서 나왔는지**가 여기 있고,
// 그것이 snapshot(method='manual') 의 evidence 가 된다.
type Judged struct {
	At, SHA, Deck                 string
	Items, Done, Partial, Nothing int
}

// Landing 은 핸드오프 단위 랜딩 이력 한 줄이다.
type Landing struct{ At, Title, Commit, Body, Note string }

// Part 는 12파트 진척 한 줄이다.
type Part struct {
	Name, Owner, State, Delta string
	Pct, D, Q, N              int
}

// BlockerItem 은 막힘 하나다. 그룹의 kind·hint 를 항목에 접어 넣는다 —
// 그룹은 화면 배치일 뿐이고 fd 에는 그 계층이 없다.
type BlockerItem struct {
	Kind, Hint, T, B string
	QIDs             []string
}

// ParseDashboard 는 DATA 블록을 읽는다. 순수 함수다.
//
// 모르는 키는 **조용히 넘기지 않는다.** 넘기면 대시보드에 새 축이 생겨도 이관이
// 아무 말 없이 그것을 버리고, 버렸다는 사실이 어디에도 안 남는다.
func ParseDashboard(block string) (Dashboard, []Reject) {
	var d Dashboard
	var rs []Reject
	add := func(path, field, code, detail string, fatal bool) {
		rs = append(rs, Reject{Source: "dashboard", Path: path, Field: field, Code: code, Detail: detail, Fatal: fatal})
	}

	root, err := ParseJSObject(block)
	if err != nil {
		add("DATA", "", "parse", err.Error(), true)
		return d, rs
	}

	// known 은 **이 파서가 실제로 읽는** 키다. 여기에 이름만 올려 두고 안 읽으면
	// 그 축은 deliberate 갈래에 도달하기 전에 continue 로 빠져 **어느 목록에도 안 나온다** —
	// "판단해서 뺐다"도 "못 봤다"도 아닌 제3의 상태(**못 봤는데 봤다고 처리된 것**)가 된다.
	// `code` 가 정확히 그 상태였다: known 에 있는데 root["code"] 를 읽는 코드가 없어
	// 건너뛴 축 목록에서도 사라졌다. 아래 절 자신이 못박은 불변식을 그 자리에서 어긴 것이다.
	known := map[string]bool{
		"asOf": true, "judged": true,
		"landings": true, "parts": true, "issues": true, "blockers": true,
	}
	// 담지 않기로 한 것과 모르는 것을 가른다. 뭉개면 "판단해서 뺐다"와 "못 봤다"가 같아진다.
	deliberate := map[string]string{
		"sessions": "게시판 스냅숏 — fd 에서는 session·signal 이 정본이라 손 기재를 되들이지 않는다",
		"locks":    "락 스냅숏 — fd 에서는 resource_hold 의 부분 유니크 인덱스가 정본이다",
		"queue":    "큐 스냅숏 — `.claude/queue/` 원본을 직접 읽으므로 사본을 또 넣지 않는다",
		"code":     "코드 레포 HEAD 스냅숏 — ref_state 가 git 에서 직접 읽는 자리다",
	}
	var keys []string
	for k := range root {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if known[k] {
			continue
		}
		if why, ok := deliberate[k]; ok {
			d.Skipped = append(d.Skipped, k+" — "+why)
			continue
		}
		add("DATA."+k, k, "unknown_key",
			fmt.Sprintf("DATA 최상위에 모르는 키 %q 가 있다 — 옮길 자리가 없다", clip(k, 40)), false)
	}

	d.AsOf, _ = root["asOf"].(string)

	if jm, ok := root["judged"].(map[string]any); ok {
		f := newFields("DATA.judged", jm, add)
		d.Judged = Judged{
			At: f.str("at", true), SHA: f.str("sha", true), Deck: f.str("deck", false),
			Items: f.num("items", false), Done: f.num("done", false),
			Partial: f.num("partial", false), Nothing: f.num("none", false),
		}
		f.rest("judged")
	} else {
		add("DATA.judged", "judged", "missing",
			"`judged` 가 없다 — parts 의 숫자가 어디서 나왔는지를 담은 유일한 축이라 "+
				"이것이 없으면 snapshot 의 evidence 를 지어내야 한다. 지어내지 않고 parts 를 통째로 거절한다", true)
	}

	for i, raw := range arrayOf(root, "landings") {
		m, ok := raw.(map[string]any)
		if !ok {
			add(fmt.Sprintf("DATA.landings[%d]", i), "", "not_object", "랜딩 원소가 객체가 아니다", true)
			continue
		}
		p := fmt.Sprintf("DATA.landings[%d]", i)
		f := newFields(p, m, add)
		l := Landing{
			At: f.str("at", true), Title: f.str("title", true), Commit: f.str("commit", true),
			// ★ `body` 와 `note` 는 **같은 자리의 두 이름**이다. 실물 70건 중 41건이
			//   `note` 만 갖고 있고(옛 이름이다) 5건은 그 `note` 마저 빈 문자열이다.
			//   `body` 를 필수로 두면 그 41건이 통째로 거절되는데, 그것들은 형식 위반이
			//   아니라 그냥 옛 이름이다. 둘 다 선택으로 두고, 서사가 통째로 비었을 때
			//   무엇을 할지는 **판정 계층**([PlanImport])이 정한다 — 이 함수는 구조만 읽는다.
			Body: f.str("body", false), Note: f.str("note", false),
		}
		f.rest("landings")
		if f.fatal {
			continue
		}
		d.Landings = append(d.Landings, l)
	}

	for i, raw := range arrayOf(root, "parts") {
		m, ok := raw.(map[string]any)
		if !ok {
			add(fmt.Sprintf("DATA.parts[%d]", i), "", "not_object", "파트 원소가 객체가 아니다", true)
			continue
		}
		p := fmt.Sprintf("DATA.parts[%d]", i)
		f := newFields(p, m, add)
		pt := Part{
			Name: f.str("name", true), Owner: f.str("owner", false), State: f.str("state", false),
			Delta: f.str("delta", false),
			Pct:   f.num("pct", true), D: f.num("d", false), Q: f.num("q", false), N: f.num("n", false),
		}
		f.rest("parts")
		if f.fatal {
			continue
		}
		d.Parts = append(d.Parts, pt)
	}

	for i, raw := range arrayOf(root, "issues") {
		s, ok := raw.(string)
		if !ok {
			add(fmt.Sprintf("DATA.issues[%d]", i), "", "not_string",
				fmt.Sprintf("이슈 원소가 문자열이 아니다(%T)", raw), true)
			continue
		}
		d.Issues = append(d.Issues, s)
	}

	for gi, raw := range arrayOf(root, "blockers") {
		g, ok := raw.(map[string]any)
		if !ok {
			add(fmt.Sprintf("DATA.blockers[%d]", gi), "", "not_object", "막힘 그룹이 객체가 아니다", true)
			continue
		}
		gp := fmt.Sprintf("DATA.blockers[%d]", gi)
		gf := newFields(gp, g, add)
		kind, hint := gf.str("kind", true), gf.str("hint", false)
		items, _ := g["items"].([]any)
		delete(gf.left, "items")
		gf.rest("blockers")
		if len(items) == 0 {
			add(gp, "items", "missing", "막힘 그룹에 `items` 배열이 없다", true)
			continue
		}
		for ii, iraw := range items {
			im, ok := iraw.(map[string]any)
			if !ok {
				add(fmt.Sprintf("%s.items[%d]", gp, ii), "", "not_object", "막힘 원소가 객체가 아니다", true)
				continue
			}
			ip := fmt.Sprintf("%s.items[%d]", gp, ii)
			f := newFields(ip, im, add)
			b := BlockerItem{Kind: kind, Hint: hint, T: f.str("t", true), B: f.str("b", true)}
			for qi, q := range arrayOfVal(im["qid"]) {
				s, ok := q.(string)
				if !ok {
					add(fmt.Sprintf("%s.qid[%d]", ip, qi), "qid", "not_string",
						fmt.Sprintf("큐 포인터가 문자열이 아니다(%T)", q), false)
					continue
				}
				b.QIDs = append(b.QIDs, s)
			}
			delete(f.left, "qid")
			f.rest("blockers.items")
			if f.fatal {
				continue
			}
			d.Blockers = append(d.Blockers, b)
		}
	}
	return d, rs
}

// ParseDashAt 은 대시보드가 쓰는 `'YYYY-MM-DD HH:MM'`(KST)을 읽는다. 순수 함수다.
//
// 이 표기에는 시간대가 없고 파일 안 주석이 "전부 KST"라고 못박는다. 그 주석을 안 보고
// UTC 로 읽으면 아홉 시간이 통째로 밀리는데, 랜딩 이력의 순서는 그래도 유지되므로
// 어긋남이 어느 화면에도 안 뜬다.
func ParseDashAt(s string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02 15:04", strings.TrimSpace(s), KST)
	if err != nil {
		return time.Time{}, fmt.Errorf("대시보드 시각을 읽지 못했다(%q, 기대 'YYYY-MM-DD HH:MM' KST): %w",
			clip(s, 40), err)
	}
	return t.UTC(), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 필드 뽑기 — 남은 키를 반드시 보고한다
// ─────────────────────────────────────────────────────────────────────────────

type fields struct {
	path  string
	left  map[string]any
	add   func(path, field, code, detail string, fatal bool)
	fatal bool
}

func newFields(path string, m map[string]any, add func(string, string, string, string, bool)) *fields {
	left := make(map[string]any, len(m))
	for k, v := range m {
		left[k] = v
	}
	return &fields{path: path, left: left, add: add}
}

func (f *fields) str(key string, required bool) string {
	v, ok := f.left[key]
	delete(f.left, key)
	if !ok || v == nil {
		if required {
			f.add(f.path, key, "missing", fmt.Sprintf("`%s` 가 없다", key), true)
			f.fatal = true
		}
		return ""
	}
	s, ok := v.(string)
	if !ok {
		f.add(f.path, key, "bad_type", fmt.Sprintf("`%s` 가 문자열이 아니다(%T)", key, v), required)
		f.fatal = f.fatal || required
		return ""
	}
	return s
}

func (f *fields) num(key string, required bool) int {
	v, ok := f.left[key]
	delete(f.left, key)
	if !ok || v == nil {
		if required {
			f.add(f.path, key, "missing", fmt.Sprintf("`%s` 가 없다", key), true)
			f.fatal = true
		}
		return 0
	}
	n, ok := v.(float64)
	if !ok {
		f.add(f.path, key, "bad_type", fmt.Sprintf("`%s` 가 수가 아니다(%T)", key, v), required)
		f.fatal = f.fatal || required
		return 0
	}
	return int(n)
}

// rest 는 안 읽고 남은 키를 전부 보고한다.
// 이것이 없으면 원본에 새 축이 생겨도 이관이 아무 말 없이 그것을 버린다.
func (f *fields) rest(what string) {
	var keys []string
	for k := range f.left {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f.add(f.path, k, "unknown_key",
			fmt.Sprintf("%s 레코드에 모르는 키 %q 가 있다 — 옮길 자리가 없다", what, clip(k, 40)), false)
	}
}

func arrayOf(m map[string]any, key string) []any { return arrayOfVal(m[key]) }

func arrayOfVal(v any) []any {
	a, _ := v.([]any)
	return a
}
