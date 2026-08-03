package legacy

import (
	"fmt"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// QueueItem 은 `.claude/queue/<bucket>/<id>.md` 한 장이다.
type QueueItem struct {
	Bucket   string // items | claims | done | dropped
	File     string
	ID       string
	Title    string
	Repo     string
	Paths    []string
	Track    string
	Needs    string
	After    []string // 원문 그대로(항목 id 또는 `<sha>@landed`)
	Handoff  string   // 레포 루트 기준 경로 문자열. FK 로 옮길 대상
	BlocksOn string
	Created  time.Time

	// Body 는 `---` 뒤의 **전문**이다. 꼬리 필드(landed_sha·closed·dropped_reason)까지
	// 그대로 담는다 — 아래 필드로 따로 뽑아 두지만 본문에서 빼지는 않는다.
	// 빼면 되쓰기가 그 줄을 다시 만들어야 하고, 만들면 그 순간 원문과 다른 값이 나갈 수 있다.
	Body string

	LandedSHA     string
	DroppedReason string
	Closed        time.Time
}

// queueKeys 는 frontmatter 에 허용되는 필드다. 순서도 이대로 되쓴다.
// `blocks_on` 은 전체 260건 중 12건에만 있는 선택 필드다.
var queueKeys = []string{"id", "title", "repo", "paths", "track", "needs", "after", "handoff", "created", "blocks_on"}

// bucketState 는 디렉토리 이름을 항목 상태로 옮긴다. 순수 함수다.
//
// 옛 구조는 **상태를 필드가 아니라 위치로** 표현했다(items/·claims/·done/·dropped/).
// 그 선택 자체는 옳았고(필드로 두면 원자적 선점과 상태 표기가 갈라져 표류한다),
// fd 의 item.state 가 같은 값을 담는다.
func bucketState(bucket string) (model.ItemState, bool) {
	switch bucket {
	case "items":
		return model.ItemOpen, true
	case "claims":
		return model.ItemClaimed, true
	case "done":
		return model.ItemDone, true
	case "dropped":
		return model.ItemDropped, true
	default:
		return "", false
	}
}

// ParseQueueItem 은 큐 항목 한 장을 읽는다. 순수 함수다.
//
// ★ `paths` 에 쉼표가 있으면 **쪼개 주지 않고 거절한다.** 옛 도구의 규약은 공백 구분인데
// 쉼표로 적힌 항목이 원본에 실재한다(2건). 여기서 쉼표를 구분자로 받아 주면 그것이
// 또 하나의 손 기재가 되고, 원본과 DB 가 다른 값을 갖게 된 사실이 어디에도 안 남는다.
// 그리고 그 관대함은 되돌릴 수 없다 — 다음 세션은 DB 만 보므로 원본이 규약을 어겼다는
// 사실 자체에 도달할 길이 사라진다.
func ParseQueueItem(bucket, file string, data []byte) (QueueItem, []Reject) {
	it := QueueItem{Bucket: bucket, File: file}
	path := "queue/" + bucket + "/" + file
	var rs []Reject
	add := func(field, code, detail string, fatal bool) {
		rs = append(rs, Reject{Source: "queue", Path: path, Field: field, Code: code, Detail: detail, Fatal: fatal})
	}

	if _, ok := bucketState(bucket); !ok {
		add("", "unknown_bucket",
			fmt.Sprintf("모르는 디렉토리 %q — items|claims|done|dropped 만 옮긴다", clip(bucket, 40)), true)
		return it, rs
	}

	head, body, ok := splitFrontMatter(string(data))
	if !ok {
		add("", "no_delimiter", "frontmatter 와 본문을 가르는 `---` 줄이 없다", true)
		return it, rs
	}
	it.Body = body

	got := map[string]string{}
	for i, line := range strings.Split(head, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, v, found := strings.Cut(line, ":")
		if !found {
			add("", "bad_header_line",
				fmt.Sprintf("frontmatter %d번째 줄이 `key: value` 가 아니다: %q", i+1, clip(line, 80)), true)
			continue
		}
		k = strings.TrimSpace(k)
		if _, dup := got[k]; dup {
			add(k, "dup_header",
				fmt.Sprintf("frontmatter 에 %q 가 두 번 있다 — 뒤가 앞을 덮으므로 거절한다", clip(k, 40)), true)
			continue
		}
		got[k] = strings.TrimSpace(v)
	}
	for k, v := range got {
		if !contains(queueKeys, k) {
			add(k, "unknown_key",
				fmt.Sprintf("frontmatter 에 규약 밖 필드 %q 가 있다(값 %q) — 옮길 자리가 없다",
					clip(k, 40), clip(v, 80)), false)
		}
	}

	it.ID, it.Title = got["id"], got["title"]
	it.Repo, it.Track, it.Needs = got["repo"], got["track"], got["needs"]
	it.Handoff, it.BlocksOn = got["handoff"], got["blocks_on"]

	if it.ID == "" {
		add("id", "missing_id", "`id` 가 없다 — 항목의 PK 이자 브랜치 이름이라 없으면 넣을 수 없다", true)
	} else if stem := strings.TrimSuffix(file, ".md"); stem != it.ID {
		add("id", "id_file_mismatch",
			fmt.Sprintf("파일 이름의 stem(%q)과 id(%q)가 다르다 — 되쓰기가 파일 이름을 id 로 만들므로 왕복이 깨진다",
				stem, clip(it.ID, 60)), true)
	}
	if it.Title == "" {
		add("title", "missing_title", "`title` 이 없다", true)
	}

	// ── paths — 여기가 이 함수의 무게중심이다
	raw := got["paths"]
	switch {
	case strings.Contains(raw, ","):
		add("paths", "paths_comma",
			fmt.Sprintf("`paths` 가 쉼표로 구분돼 있다(%q) — 옛 도구의 규약은 공백 구분이다. "+
				"쪼개 주지 않는다: 조용히 고치면 원본과 DB 가 다른 값을 갖게 되고 그 사실이 어디에도 안 남는다",
				clip(raw, 120)), true)
	case strings.TrimSpace(raw) == "":
		it.Paths = nil
	default:
		it.Paths = strings.Fields(raw)
	}

	if a := strings.TrimSpace(got["after"]); a != "" {
		it.After = strings.Fields(a)
	}

	if c := got["created"]; c == "" {
		add("created", "missing_created", "`created` 가 없다 — 시각을 지어내지 않는다", true)
	} else if t, err := time.Parse(time.RFC3339, c); err != nil {
		add("created", "bad_time",
			fmt.Sprintf("`created` 를 RFC3339 로 읽지 못했다(%q): %v", clip(c, 60), err), true)
	} else {
		it.Created = t
	}

	// ── 꼬리 필드
	tail := tailFields(body)
	it.LandedSHA = tail["landed_sha"]
	it.DroppedReason = tail["dropped_reason"]
	if c := tail["closed"]; c != "" {
		if t, err := time.Parse(time.RFC3339, c); err != nil {
			add("closed", "bad_time",
				fmt.Sprintf("`closed` 를 RFC3339 로 읽지 못했다(%q): %v", clip(c, 60), err), false)
		} else {
			it.Closed = t
		}
	}

	state, _ := bucketState(bucket)
	if state == model.ItemDropped && strings.TrimSpace(it.DroppedReason) == "" {
		// 스키마 CHECK 와 같은 규율이다 — 사유 없는 폐기는 나중에 되짚을 수 없다.
		add("dropped_reason", "dropped_no_reason",
			"dropped/ 에 있는데 `dropped_reason:` 이 없다 — 사유 없는 폐기는 되짚을 수 없어 거절한다", true)
	}
	if (state == model.ItemDone || state == model.ItemDropped) && it.Closed.IsZero() && tail["closed"] == "" {
		add("closed", "missing_closed",
			"종료된 항목인데 `closed:` 가 없다 — 종료 시각을 지어내지 않고 created 로 넣는다", false)
	}
	return it, rs
}

// tailFieldKeys 는 옛 도구가 본문 **뒤에** 덧붙이는 필드다.
var tailFieldKeys = []string{"landed_sha", "closed", "dropped_reason"}

// tailFields 는 본문 끝에 붙은 `key: value` 줄들을 읽는다.
//
// ★ 본문 전체를 훑지 않고 **끝에서 위로** 훑다가 규약 밖 줄을 만나면 멈춘다.
// 전체를 훑으면 산문 한복판의 `closed: …` 같은 줄이 값으로 잡힌다 — 원본의 본문은
// 사람이 쓴 마크다운이라 무엇이든 들어 있다.
func tailFields(body string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(body, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimRight(lines[i], "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, v, found := strings.Cut(line, ":")
		if !found || !contains(tailFieldKeys, strings.TrimSpace(k)) {
			return out
		}
		k = strings.TrimSpace(k)
		if _, dup := out[k]; !dup {
			out[k] = strings.TrimSpace(v)
		}
	}
	return out
}

// Labels 는 fd 의 item.labels 로 갈 값이다.
//
// ★ **표시 전용이다. 어떤 배제 판정에도 안 쓴다**(설계 §5). `track` 은 자유 문자열이라
// 절반 이상이 디렉토리 경계가 아니고, 그것을 필터 축으로 쓴 것이 옛 큐가 남의 트랙을
// 집게 만든 원인 중 하나였다. 경로 겹침 판정은 paths ∪ footprint 가 한다.
//
// `repo`·`needs`·`blocks_on` 도 여기 같이 싣는다 — fd 에 대응하는 칸이 없는데
// 버리면 그 값이 이관에서 사라지고, 되쓰기가 frontmatter 를 복원하지 못한다.
func (it QueueItem) Labels() []string {
	var out []string
	for _, kv := range [][2]string{
		{"track", it.Track}, {"repo", it.Repo}, {"needs", it.Needs}, {"blocks_on", it.BlocksOn},
	} {
		if strings.TrimSpace(kv[1]) != "" {
			out = append(out, kv[0]+":"+kv[1])
		}
	}
	return out
}

// LabelValue 는 Labels 가 만든 문자열에서 값을 되찾는다. 되쓰기가 쓴다.
func LabelValue(labels []string, key string) string {
	for _, l := range labels {
		if v, ok := strings.CutPrefix(l, key+":"); ok {
			return v
		}
	}
	return ""
}
