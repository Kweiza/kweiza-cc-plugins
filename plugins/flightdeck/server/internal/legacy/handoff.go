package legacy

import (
	"regexp"
	"strconv"
	"time"
)

// Handoff 는 `.claude/handoffs/*.md` 한 장이다.
//
// ★ **파싱하지 않는다.** 본문은 통째로 blob 이고 `judgment(kind='handoff')` 하나가 된다.
// 구조를 강제하면 세션들이 스스로 발명한 절이 갈 곳을 잃는다 — 그 절들이야말로
// 다음 세션이 실제로 막히는 자리("왜 그렇게 하기로 했나")를 나르는 것이다.
type Handoff struct {
	File   string // 파일 이름 원문. judgment.title 이 되고 되쓰기의 파일 이름이 된다
	Rel    string // 레포 루트 기준 경로. 큐의 `handoff:` 문자열과 대조하는 키
	Body   string
	At     time.Time
	AtFrom string // "filename" | "mtime" — 어느 축에서 시각이 나왔는지
}

// handoffNameRe 는 규약 파일명 `YYYY-MM-DD-HHMM-<주제>.md` 다.
//
// ★ 줄 전체(^…$)로 잡는다. 부분 일치로 잡으면 `_wip-2026-08-03-…` 처럼 접두가 붙은
// 이탈 파일이 규약을 지킨 것으로 읽혀 시각이 조용히 엉뚱해진다 — 실물에 그런 이름이 있다.
var handoffNameRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})-(\d{2})(\d{2})-.+\.md$`)

// HandoffTime 은 핸드오프 한 장의 시각을 정한다. 순수 함수다.
//
// 규약 파일명이면 거기서 읽고(KST — 옛 도구가 그 시간대로 이름을 지었다),
// 아니면 **mtime 으로 폴백한다.** 실물 89장 중 3장이 규약을 벗어나 있다
// (`_wip-…` · `…-PLAN-…` · `…-CONTRACT-HANDOFF-…`). 이름이 규약을 어겼다는 이유로
// 통째로 거절하면 그 3장의 산문이 사라지는데, 그것이 이 이관이 지키려는 바로 그 자산이다.
//
// 두 번째 반환값이 어느 축에서 나왔는지다. 뭉개면 "이름에서 읽은 정확한 시각"과
// "파일을 복사한 시각"이 같은 값으로 보인다 — mtime 은 레포를 옮기면 통째로 바뀐다.
func HandoffTime(name string, mtime time.Time) (time.Time, string) {
	m := handoffNameRe.FindStringSubmatch(name)
	if m == nil {
		return mtime.UTC(), "mtime"
	}
	n := func(s string) int { v, _ := strconv.Atoi(s); return v }
	t := time.Date(n(m[1]), time.Month(n(m[2])), n(m[3]), n(m[4]), n(m[5]), 0, 0, KST)
	return t.UTC(), "filename"
}
