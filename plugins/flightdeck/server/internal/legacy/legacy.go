// Package legacy 는 옛 조정 도구의 산출물을 flightdeck 의 DB 로 옮기고(`fd import`)
// 다시 옛 형식으로 되쓴다(`fd export --to-legacy`).
//
// 옮기는 대상은 넷이다.
//
//	.claude/sessions/*.md            세션 카드 — 머리 8필드 + `---` + 마크다운 절
//	.claude/queue/{items,claims,done,dropped}/*.md   큐 항목 — frontmatter + 본문
//	.claude/handoffs/*.md            핸드오프 산문 — **파싱하지 않는다**
//	slides/status.html 의 DATA 블록   대시보드 — JS 객체 리터럴
//
// # 이 패키지를 지배하는 규율 넷
//
//	① **조용히 버리는 것이 하나도 없다.** 이 레포는 이미 두 번 원문을 영구 소실했다
//	   (2026-07-28·07-29, 저장소가 gitignore 라 복구 불가). 해석하지 못한 것은 전부
//	   Reject 로, 끊긴 포인터는 전부 Gone 으로 나오고 dry-run 이 그 전량을 찍는다.
//	   "몇 건 실패"라는 요약만 내면 그 순간 이 도구가 손 기재와 같은 것이 된다.
//
//	② **형식 위반은 거절한다. 고쳐 주지 않는다.** 예: 큐의 `paths` 가 쉼표로 구분돼
//	   들어온 항목(원본에 실재한다)을 쪼개 주지 않는다. 조용히 고치면 그것이 또 하나의
//	   손 기재이고, 원본과 DB 가 다른 값을 갖게 된 사실이 어디에도 안 남는다.
//
//	③ **비규약 절을 버리지 않는다.** 규율은 세션 카드에 4절을 정하는데 실무는 그것을
//	   넘었다(`## 실측 기록`·`## 범위에서 뺀 것`·`## 지금`). 규율이 실무보다 좁았다는
//	   증거이지 오류가 아니다. 절 이름을 **원문 그대로** judgment.title 에 싣고,
//	   분류하지 못했다는 사실을 dry-run 이 따로 나열한다.
//
//	④ **핸드오프는 통째로 blob 이다.** 구조화하면 세션들이 스스로 발명한 절이 갈 곳을 잃는다.
//
// # 판정과 실행을 가른다
//
// 무엇을 넣고 무엇을 거절하는지는 [PlanImport] 가 정하고 그것은 순수 함수다.
// 실제 쓰기([Apply])는 그 계획을 집행할 뿐 스스로 판정하지 않는다.
// 판정이 실행 본문에 흩어지면 시험이 그 로직의 **사본**을 단정하게 되고 변이가 조용히 샌다.
package legacy

import (
	"sort"
	"strings"
	"time"
)

// KST 는 레거시 산출물의 시간대다.
//
// 옛 도구는 시각을 전부 `+09:00` 으로 적었고 대시보드 DATA 블록은 아예 오프셋 없이
// `'YYYY-MM-DD HH:MM'` 만 적으며 "KST 기준"이라고 파일 안 주석에 못박았다.
// 그 값을 UTC 로 읽으면 아홉 시간이 통째로 밀리는데, 그 어긋남은 어느 화면에도 안 뜬다.
var KST = time.FixedZone("KST", 9*3600)

// Reject 는 이관이 받아들이지 않은 것 하나다.
//
// Fatal 이면 그 단위(파일 하나 · 레코드 하나)를 통째로 안 넣는다. Fatal 이 아니면
// 그 필드만 못 옮긴 것이고 나머지는 들어간다 — **둘을 뭉개지 않는다.** 뭉개면
// "이 파일이 통째로 빠졌다"와 "이 파일의 한 필드를 못 읽었다"가 같은 줄로 보인다.
type Reject struct {
	Source string // session | queue | handoff | dashboard
	Path   string // 원본 파일 경로(원본 루트 기준). 대시보드는 레코드 좌표
	Field  string // 걸린 필드 이름. 파일 전체가 문제면 빈 문자열
	Code   string // 기계 판정용 사유 코드
	Detail string // 사람이 읽는 사유 전문
	Fatal  bool
}

// Gone 은 원본에서 경로·id 문자열로 걸려 있던 포인터인데 가리키는 대상이 없는 것이다.
//
// 이관의 진짜 비용이 여기 있다. 옛 구조는 포인터가 **문자열**이라 대상이 사라져도
// 아무 일도 일어나지 않았고(다른 머신에서 온 항목이 특히 그렇다 — `.claude/handoffs/` 는
// gitignore 라 따라오지 않는다), 그래서 끊긴 포인터가 몇 개인지 세는 축 자체가 없었다.
// FK 로 옮기면 그 침묵이 사라지는 대신 **옮기지 못하는 것이 생긴다.** 그 전량이 이 목록이다.
type Gone struct {
	Kind   string // item.handoff | item.after | blocker.qid
	From   string // 가리킨 쪽의 좌표
	Target string // 가리킨 값(원문 그대로)
	Detail string
}

// SectionRef 는 분류하지 못한 절 하나의 좌표다. 보존은 되지만 kind 를 못 정한 것이다.
type SectionRef struct {
	File string
	Name string
}

// sortRejects 는 보고 순서를 고정한다. map 순회가 섞이면 dry-run 이 실행마다 달라 보이고,
// 그러면 "무엇이 달라졌나"를 diff 로 볼 수 없다.
func sortRejects(rs []Reject) {
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		switch {
		case a.Source != b.Source:
			return a.Source < b.Source
		case a.Path != b.Path:
			return a.Path < b.Path
		case a.Field != b.Field:
			return a.Field < b.Field
		default:
			return a.Code < b.Code
		}
	})
}

func sortGone(gs []Gone) {
	sort.SliceStable(gs, func(i, j int) bool {
		a, b := gs[i], gs[j]
		switch {
		case a.Kind != b.Kind:
			return a.Kind < b.Kind
		case a.From != b.From:
			return a.From < b.From
		default:
			return a.Target < b.Target
		}
	})
}

// clip 은 로그·보고에 실을 외부 문자열을 자르고 제어문자를 걷어낸다.
// 원본은 사람이 손으로 쓴 마크다운이라 무엇이든 들어 있을 수 있다.
func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}

// firstLine 은 여러 줄 본문에서 제목으로 쓸 첫 줄을 고른다.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
