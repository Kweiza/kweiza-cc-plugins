package legacy

import (
	"fmt"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// SessionCard 는 `.claude/sessions/<track>.md` 한 장이다.
//
// 파일 이름의 stem 이 곧 `track` 이다(실물 36장 전수 확인 — 어긋난 것이 0건이다).
// 그래서 이관은 track 을 따로 저장하지 않고 cc_session_id 에 파일 이름을 실어
// 되쓰기에서 그대로 복원한다. 파생 가능한 것에 칸을 만들지 않는다는 원칙 ①이 여기에도 걸린다.
type SessionCard struct {
	File     string // 파일 이름(`2.md`). 정체이자 track 의 원천
	Track    string
	Desc     string
	State    string // 원문 그대로(active|paused|blocked|landing|done)
	Branch   string
	Worktree string
	Head     string
	PID      string
	Updated  time.Time
	Sections []Section // 원문 순서 그대로
}

// Section 은 카드 본문의 절 하나다.
//
// Name 은 `## ` 를 뗀 **원문 그대로**다. 규율의 4절 밖 이름도 그대로 싣는다 —
// 규율이 실무보다 좁았다는 증거이지 오류가 아니다.
type Section struct {
	Name      string
	Body      string
	Canonical bool // 규율이 정한 4절(+ `## 지금` 변형)인가
}

// sessionHeaderKeys 는 카드 머리에 있어야 하는 8필드다. 순서도 이대로 되쓴다.
var sessionHeaderKeys = []string{"track", "desc", "state", "branch", "worktree", "head", "pid", "updated"}

// SectionKind 는 절 이름을 판단 종류로 옮긴다. 순수 함수다.
//
// 두 번째 반환값이 false 면 **분류하지 못한 것**이고, 그 사실을 dry-run 이 따로 나열한다.
// 절 자체는 이름과 본문 그대로 보존된다 — 분류 실패가 폐기가 되면 이 이관이
// 지키려던 것(비규약 절)이 정확히 그 자리에서 사라진다.
//
// `## 다음` 과 `## 지금 하는 것` 이 같은 kind 로 가는 것은 손실이 아니다.
// 절 이름이 judgment.title 에 원문 그대로 실려 둘을 가른다.
func SectionKind(name string) (model.JudgmentKind, bool) {
	switch strings.TrimSpace(name) {
	case "지금 하는 것", "지금":
		return model.JudgmentNow, true
	case "다음":
		return model.JudgmentNow, true
	case "막힘":
		return model.JudgmentBlocked, true
	case "다른 트랙에 요청":
		return model.JudgmentAsk, true
	default:
		return model.JudgmentNow, false
	}
}

// SessionStateOf 는 레거시 상태 문자열을 fd 의 상태로 옮긴다. 순수 함수다.
//
// 두 번째 반환값은 **사유**다. 빈 문자열이면 정확히 옮겨진 것이고, 그렇지 않으면
// 무엇을 어떻게 바꿔 넣었는지가 그 문자열에 있다. 불리언으로 두면 "그대로 옮겼다"와
// "바꿔 넣었다"가 구분되지 않고, 그 구분이 없으면 되쓰기가 무엇을 잃는지 말할 수 없다.
// 세 번째 반환값이 false 면 옮길 수 없다는 뜻이다.
func SessionStateOf(legacyState string) (model.SessionState, string, bool) {
	switch strings.TrimSpace(legacyState) {
	case "active":
		return model.SessionActive, "", true
	case "paused":
		return model.SessionPaused, "", true
	case "blocked":
		return model.SessionBlocked, "", true
	case "done":
		return model.SessionDone, "", true
	case "landing":
		// fd 에는 landing 상태가 없다. 랜딩은 잡 레코드가 표현하고 세션 상태는
		// 신호에서 파생된다(설계 §4·§5). 바꿔 넣되 그 사실을 사유로 남긴다.
		return model.SessionActive, "레거시 `landing` 은 fd 에 없는 상태다 — " +
			"랜딩은 잡 레코드가 표현하므로 active 로 넣는다(되쓰기에서 active 로 나간다)", true
	default:
		return "", fmt.Sprintf("모르는 상태 %q — active|paused|blocked|landing|done 만 옮긴다",
			clip(legacyState, 40)), false
	}
}

// ParseSessionCard 는 카드 한 장을 읽는다. 순수 함수다(파일을 열지 않는다).
//
// 두 번째 반환값은 이 파일에서 나온 거절 전부다. Fatal 이 하나라도 있으면
// [PlanImport] 가 이 카드를 통째로 뺀다.
func ParseSessionCard(file string, data []byte) (SessionCard, []Reject) {
	card := SessionCard{File: file}
	var rs []Reject
	add := func(field, code, detail string, fatal bool) {
		rs = append(rs, Reject{Source: "session", Path: file, Field: field, Code: code, Detail: detail, Fatal: fatal})
	}

	head, body, ok := splitFrontMatter(string(data))
	if !ok {
		add("", "no_delimiter",
			"머리와 본문을 가르는 `---` 줄이 없다 — 세션 카드 형식이 아니다", true)
		return card, rs
	}

	got := map[string]string{}
	for i, line := range strings.Split(head, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, v, found := strings.Cut(line, ":")
		if !found {
			add("", "bad_header_line",
				fmt.Sprintf("머리 %d번째 줄이 `key: value` 가 아니다: %q", i+1, clip(line, 80)), true)
			continue
		}
		k = strings.TrimSpace(k)
		if _, dup := got[k]; dup {
			add(k, "dup_header",
				fmt.Sprintf("머리에 %q 가 두 번 있다 — 뒤가 앞을 덮으므로 거절한다", clip(k, 40)), true)
			continue
		}
		got[k] = strings.TrimSpace(v)
	}
	for k, v := range got {
		if !contains(sessionHeaderKeys, k) {
			add(k, "unknown_header",
				fmt.Sprintf("머리에 규약 밖 필드 %q 가 있다(값 %q) — 옮길 자리가 없다",
					clip(k, 40), clip(v, 80)), false)
		}
	}
	for _, k := range sessionHeaderKeys {
		if _, okk := got[k]; !okk {
			add(k, "missing_header", fmt.Sprintf("머리에 %q 가 없다", k), true)
		}
	}

	card.Track, card.Desc, card.State = got["track"], got["desc"], got["state"]
	card.Branch, card.Worktree, card.Head, card.PID = got["branch"], got["worktree"], got["head"], got["pid"]

	if u := got["updated"]; u != "" {
		t, err := time.Parse(time.RFC3339, u)
		if err != nil {
			add("updated", "bad_time",
				fmt.Sprintf("`updated` 를 RFC3339 로 읽지 못했다(%q): %v", clip(u, 60), err), true)
		} else {
			card.Updated = t
		}
	}

	if stem := strings.TrimSuffix(file, ".md"); card.Track != "" && stem != card.Track {
		// 되쓰기가 파일 이름에서 track 을 복원하므로 둘이 어긋나면 왕복이 깨진다.
		add("track", "track_file_mismatch",
			fmt.Sprintf("파일 이름의 stem(%q)과 track(%q)이 다르다 — "+
				"되쓰기는 파일 이름에서 track 을 복원하므로 왕복이 깨진다", stem, clip(card.Track, 40)), true)
	}

	card.Sections = parseSections(body)
	if len(card.Sections) == 0 {
		add("", "no_sections", "본문에 절이 하나도 없다", false)
	}
	return card, rs
}

// parseSections 는 본문을 `## ` 머리글 단위로 가른다.
//
// 첫 머리글 앞에 글이 있으면 이름 없는 절로 담는다 — 버리면 그것이 곧 원문 소실이다.
func parseSections(body string) []Section {
	lines := strings.Split(body, "\n")
	var out []Section
	cur := Section{Name: ""}
	var buf []string
	flush := func() {
		cur.Body = strings.Trim(strings.Join(buf, "\n"), "\n")
		if cur.Name != "" || strings.TrimSpace(cur.Body) != "" {
			_, cur.Canonical = SectionKind(cur.Name)
			out = append(out, cur)
		}
		buf = nil
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			flush()
			cur = Section{Name: strings.TrimSpace(strings.TrimPrefix(line, "## "))}
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return out
}

// SectionBody 는 이름으로 절 본문을 찾는다. 없으면 빈 문자열이다.
func (c SessionCard) SectionBody(name string) string {
	for _, s := range c.Sections {
		if s.Name == name {
			return s.Body
		}
	}
	return ""
}

// BlockedWhy 는 `blocked` 상태에 필요한 사유다.
//
// 스키마가 공허한 단정을 막는다(`state='blocked'` 면 사유 필수). 레거시의
// `(없음)` 은 사유가 아니라 자리 표시라 사유로 치지 않는다.
func (c SessionCard) BlockedWhy() string {
	b := strings.TrimSpace(c.SectionBody("막힘"))
	if b == "" || b == "(없음)" || b == "없음" {
		return ""
	}
	return b
}

// splitFrontMatter 는 `---` 한 줄을 경계로 머리와 본문을 가른다.
//
// ★ 경계는 **줄 전체 일치**로 찾는다. 본문에 수평선이나 `---` 로 시작하는 줄이
// 있을 수 있고, 부분 문자열로 찾으면 그런 줄이 경계를 옮긴다.
func splitFrontMatter(s string) (head, body string, ok bool) {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if strings.TrimRight(line, "\r") == "---" {
			return strings.Join(lines[:i], "\n"), strings.Join(lines[i+1:], "\n"), true
		}
	}
	return "", "", false
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
