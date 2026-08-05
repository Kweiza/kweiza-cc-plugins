package legacy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// ImportPlan 은 **무엇을 넣고 무엇을 거절하는지의 판정**이다. 실행이 아니다.
//
// [Apply] 는 이 계획을 집행할 뿐 스스로 아무것도 판정하지 않는다.
// 판정이 실행 본문에 흩어지면 시험이 그 로직의 사본을 단정하게 되고 변이가 조용히 샌다.
type ImportPlan struct {
	Project string

	Counts   []CountRow
	Sessions []PlannedSession
	Items    []PlannedItem
	Handoffs []Handoff
	Landings []Landing
	Parts    []PlannedPart
	Issues   []PlannedItem
	Blockers []PlannedItem

	// Rejects 는 **전량**이다. 요약하지 않는다.
	Rejects []Reject
	// Gone 은 FK 로 못 옮긴 포인터 **전량**이다.
	Gone []Gone
	// Unclassified 는 분류하지 못했으나 보존되는 절 전량이다.
	Unclassified []SectionRef
	// Notes 는 "넣긴 넣는데 원본과 다르게 넣는다"를 말하는 자리다.
	Notes []string
	// SkippedAxes 는 일부러 안 옮기는 축이다(파생이라 손 기재를 되들이지 않는 것).
	SkippedAxes []string
}

// CountRow 는 대조표 한 줄이다. 발견 · 넣음 · 거절이 나란히 있어야
// "해석 성공 N건"이 몇 건 중 N건인지가 보인다.
type CountRow struct {
	Source string
	Found  int
	Bytes  int64
	Accept int
	Reject int
}

// PlannedSession 은 넣을 세션 하나와 그 절들이다.
type PlannedSession struct {
	Card        SessionCard
	CCSessionID string // `legacy:<파일이름>` — 되쓰기가 여기서 파일 이름과 track 을 복원한다
	State       model.SessionState
	BlockedWhy  string
	Sections    []PlannedSection
}

// PlannedSection 은 절 하나다. Name 은 **원문 그대로**이고 그것이 judgment.title 이 된다.
type PlannedSection struct {
	Name      string
	Body      string
	Kind      model.JudgmentKind
	Canonical bool
}

// PlannedItem 은 넣을 큐 항목 하나다.
type PlannedItem struct {
	Item       model.Item
	HandoffRel string // 연결할 핸드오프(빈 문자열이면 없음)
}

// PlannedPart 는 넣을 진척 스냅숏 하나다.
type PlannedPart struct {
	Key      string
	Value    string // JSON
	Evidence string
	// Digest 는 **판정 당시의 입력**이다(= judged 블록의 코드 sha).
	// 이것이 비면 UI 의 낡음 대조가 "대조할 축이 없다"로 죽는다 —
	// 설계 §3 이 "input_digest 가 현재 트리와 다르면 UI 가 자동으로 낡음을 붙인다"고
	// 약속한 자리라, 근거 문자열에 이미 있는 sha 를 여기 함께 넣어 그 약속을 살린다.
	Digest string
}

// PlanOptions 는 판정에 필요한 바깥 값이다.
type PlanOptions struct {
	Project string
}

// PlanImport 는 훑은 결과를 계획으로 옮긴다. **순수 함수다** — 파일도 DB 도 안 만진다.
//
// 규율 넷이 여기서 집행된다.
//
//	① Fatal 거절이 하나라도 붙은 파일은 통째로 안 넣는다. 반쯤 넣으면 원본과 DB 중
//	   어느 쪽이 참인지 아무도 모르는 상태가 되고, 그 상태에서는 롤백도 판단 불가가 된다.
//	② 문자열 포인터를 FK 로 옮기다 실패한 것은 전부 Gone 이다. 하나도 안 접는다.
//	③ `track` 은 라벨로만 간다. 어떤 배제 판정에도 안 쓴다.
//	④ parts 는 evidence 없이 못 들어간다. 근거를 지어내지 않고 통째로 거절한다.
func PlanImport(sc Scan, opt PlanOptions) ImportPlan {
	p := ImportPlan{Project: opt.Project, Rejects: append([]Reject(nil), sc.Rejects...)}

	fatal := map[string]bool{}
	for _, r := range p.Rejects {
		if r.Fatal {
			fatal[r.Source+"\x00"+r.Path] = true
		}
	}
	isFatal := func(source, path string) bool { return fatal[source+"\x00"+path] }
	reject := func(source, path, field, code, detail string, f bool) {
		p.Rejects = append(p.Rejects, Reject{
			Source: source, Path: path, Field: field, Code: code, Detail: detail, Fatal: f})
	}

	// ── 핸드오프: 파싱하지 않는다. 통째로 blob 이다
	handoffByRel := map[string]bool{}
	mtimeFallback := 0
	for _, h := range sc.Handoffs {
		if isFatal("handoff", h.Rel) {
			continue
		}
		handoffByRel[h.Rel] = true
		if h.AtFrom == "mtime" {
			mtimeFallback++
		}
		p.Handoffs = append(p.Handoffs, h)
	}
	if mtimeFallback > 0 {
		p.Notes = append(p.Notes, fmt.Sprintf(
			"핸드오프 %d건은 파일명이 `YYYY-MM-DD-HHMM-…` 규약을 벗어나 **mtime 으로 시각을 정했다** — "+
				"mtime 은 파일을 복사하면 통째로 바뀌므로 이 값은 '언제 썼나'가 아니라 '언제 이 트리에 놓였나'다",
			mtimeFallback))
	}

	// ── 세션 카드
	for _, c := range sc.Sessions {
		if isFatal("session", c.File) {
			continue
		}
		state, why, ok := SessionStateOf(c.State)
		if !ok {
			reject("session", c.File, "state", "bad_state", why, true)
			continue
		}
		if why != "" {
			p.Notes = append(p.Notes, fmt.Sprintf("세션 %s: %s", c.File, why))
		}
		ps := PlannedSession{Card: c, CCSessionID: "legacy:" + c.File, State: state}
		if state == model.SessionBlocked {
			ps.BlockedWhy = c.BlockedWhy()
			if ps.BlockedWhy == "" {
				reject("session", c.File, "state", "blocked_no_why",
					"state=blocked 인데 `## 막힘` 절이 비었거나 `(없음)` 이다 — "+
						"사유 없는 막힘은 공허한 단정이라 스키마가 받지 않는다", true)
				continue
			}
		}
		for _, s := range c.Sections {
			if strings.TrimSpace(s.Body) == "" && strings.TrimSpace(s.Name) == "" {
				continue
			}
			kind, canonical := SectionKind(s.Name)
			body := s.Body
			if strings.TrimSpace(body) == "" {
				// 판단 표는 빈 본문을 받지 않는다(스키마 CHECK). 절 이름을 잃지 않으려면
				// 무언가를 실어야 하는데, 지어내지 않고 **비어 있었다는 사실**을 싣는다.
				body = "(원본에서 이 절의 본문이 비어 있었다)"
				reject("session", c.File, s.Name, "empty_section",
					fmt.Sprintf("절 %q 의 본문이 비어 있다 — 절 이름은 보존하되 본문 자리에 그 사실을 적는다",
						clip(s.Name, 60)), false)
			}
			if !canonical {
				p.Unclassified = append(p.Unclassified, SectionRef{File: c.File, Name: s.Name})
			}
			ps.Sections = append(ps.Sections, PlannedSection{
				Name: s.Name, Body: body, Kind: kind, Canonical: canonical})
		}
		p.Sessions = append(p.Sessions, ps)
	}
	if len(p.Unclassified) > 0 {
		p.Notes = append(p.Notes, fmt.Sprintf(
			"세션 카드의 비규약 절 %d개는 **절 이름 그대로 보존**하되 판단 종류를 정하지 못해 `now` 로 넣는다 — "+
				"규율이 실무보다 좁았다는 증거이지 오류가 아니다(전량은 아래 목록)", len(p.Unclassified)))
	}

	// ── 큐 항목
	itemIDs := map[string]bool{}
	for _, it := range sc.Items {
		if isFatal("queue", "queue/"+it.Bucket+"/"+it.File) || it.ID == "" {
			continue
		}
		itemIDs[it.ID] = true
	}
	landedSHA := 0
	for _, it := range sc.Items {
		path := "queue/" + it.Bucket + "/" + it.File
		if isFatal("queue", path) {
			continue
		}
		state, _ := bucketState(it.Bucket)
		closeReason := ""
		if state == model.ItemDropped {
			closeReason = it.DroppedReason
		}
		closed := it.Closed
		if (state == model.ItemDone || state == model.ItemDropped) && closed.IsZero() {
			closed = it.Created
		}
		// ★ item.paths 좌표계 관문 — 이관은 이 컬럼으로 가는 **세 번째 문**이다.
		//
		// 앞의 둘은 add(service.AddItem)와 finish(followup.paths)이고 둘 다
		// judgeItemPathsCoordinate 로 **거절**한다(스펙 §4.2 "사람이 넣으면 거절").
		// 여기는 거절하지 않고 **그 경로만 버리고 남긴다.** 다른 규율을 쓰는 이유는
		// 고칠 수 있는 사람이 있느냐다 — add·finish 는 지금 그 자리에 있는 사람이
		// 사유를 읽고 즉시 고칠 수 있지만, 이관은 과거의 원본을 옮기는 것이라 그
		// 사람이 없다. 고칠 수 없는 것을 거절하면 카드 하나가 이관 전체를 멈춘다.
		// 발자국 쪽 규율(service.Beat — 버리되 원장에 남긴다)과 같은 자리다.
		//
		// Fatal 이 아닌 이유: 좌표계가 틀린 경로는 그 항목의 **겹침 축만** 죽인다.
		// 제목·본문·상태·선행은 멀쩡하므로 항목을 통째로 버릴 근거가 못 된다
		// (규율 ①의 Fatal 은 "파일을 읽다 실패한 것"에 대한 것이다).
		//
		// 판정을 Apply 가 아니라 여기서 하는 것은 ImportPlan 주석의 계약이다 —
		// 판정이 실행 본문에 흩어지면 시험이 그 사본을 단정하게 되고 변이가 조용히 샌다.
		keptPaths, badPaths := judge.FilterPathCoordinate(it.Paths)
		for _, bp := range badPaths {
			reject("queue", path, "paths", "bad_path_coordinate", bp.Reason, false)
		}

		// ★ 포함 축("이 경로가 어느 트리 안인가")은 **여기서 판정할 수 없다.**
		//
		// 좌표계 축과 갈리는 자리다. 좌표계는 문자열 형태만 보므로 이관에서도 판정된다
		// (바로 위). 포함 축은 기준 트리를 알아야 하는데 PlanOptions 에 그것이 없다 —
		// 그리고 없는 것이 옳다. 레거시 카드의 경로는 다른 머신·다른 디렉토리에서 온
		// 것일 수 있고, 이 순수 함수에는 그것을 알 방법이 없다.
		//
		// 그래서 **버리지 않는다.** 판정할 수 없는 것을 버리면 못 읽음이 값이 되고,
		// 그 경로가 정말 밖이었는지 아무도 다시 못 안다(service.RelPathWithin 이
		// root 를 모를 때 within=true 로 두는 것과 같은 규율 — fail-open).
		//
		// 대신 말한다. 관문이 어느 표면에 없는지가 코드 어디에도 안 적혀 있으면 다음
		// 사람이 네 표면을 다시 전수해야 그 표를 만든다 — 항목
		// fd-containment-gate-only-on-one-of-three-doors 가 그 비용을 적었다.
		var absPaths []string
		for _, p := range keptPaths {
			if strings.HasPrefix(p, "/") {
				absPaths = append(absPaths, p)
			}
		}
		if len(absPaths) > 0 {
			p.Notes = append(p.Notes, fmt.Sprintf(
				"큐 항목 `%s` 의 경로 %d개가 절대경로다 — **포함 축을 판정하지 않고 그대로 넣는다.** "+
					"어느 트리 안인지는 기준 트리를 알아야 하는데 이관에는 그것이 없다"+
					"(레거시 카드의 경로는 다른 머신에서 온 것일 수 있다). "+
					"살아 있는 두 문(service.Beat·service.Pick)은 이 축을 태우므로, "+
					"이 경로들은 겹침 축에서 아무와도 안 맞을 수 있다: %s",
				it.ID, len(absPaths), strings.Join(absPaths, " · ")))
		}

		m := model.Item{
			Project: opt.Project, ID: it.ID, Title: it.Title, Body: it.Body,
			Paths: keptPaths, Labels: it.Labels(), State: state,
			CloseReason: closeReason, CreatedAt: it.Created,
		}
		if state == model.ItemDone || state == model.ItemDropped {
			c := closed
			m.ClosedAt = &c
		}
		// ★ landed_sha 를 landed_ref 에 넣지 않는다.
		//   그 칸에는 **러너가 실제로 fast-forward 한 sha 만** 들어간다(설계 §3 Q 계층).
		//   옛 도구는 "메인 트리의 지금 HEAD"를 적어 남의 커밋이 박혔고 그 관측이 3회 있다.
		//   레거시 값은 본문 꼬리에 원문 그대로 남으므로 잃지 않는다.
		if strings.TrimSpace(it.LandedSHA) != "" {
			landedSHA++
		}

		pi := PlannedItem{Item: m}
		if h := strings.TrimSpace(it.Handoff); h != "" {
			if handoffByRel[h] {
				pi.HandoffRel = h
			} else {
				p.Gone = append(p.Gone, Gone{
					Kind: "item.handoff", From: it.ID, Target: h,
					Detail: "가리키는 핸드오프 파일이 이 트리에 없다 — `.claude/handoffs/` 는 gitignore 라 " +
						"다른 머신에서 온 항목이 이 모양이 된다. FK 로 옮길 대상이 없으므로 연결을 만들지 않는다",
				})
			}
		}
		for _, a := range it.After {
			switch {
			case strings.HasSuffix(a, "@landed"):
				sha := strings.TrimSuffix(a, "@landed")
				if sha == "" {
					reject("queue", path, "after", "bad_after",
						fmt.Sprintf("선행 조건 %q 에 sha 가 없다", clip(a, 60)), false)
					continue
				}
				// 랜딩된 선행은 sha 축으로 간다. **브랜치 이름을 담을 칸이 없다**(설계 §3) —
				// 랜딩이 끝나면 규율대로 브랜치가 지워져 조건이 충족되는 바로 그 순간
				// merge-base 가 해석 불가를 내던 결함이 여기서 소멸한다.
				m.After = append(m.After, model.After{SHA: sha})
			case itemIDs[a]:
				m.After = append(m.After, model.After{Item: a})
			default:
				p.Gone = append(p.Gone, Gone{
					Kind: "item.after", From: it.ID, Target: a,
					Detail: "선행으로 걸린 항목 id 가 큐에 없다 — 브랜치 이름을 적었거나 다른 머신의 항목이다. " +
						"item_after 에는 브랜치를 담을 칸이 없으므로(설계 §3) 이 선행은 옮기지 않는다",
				})
			}
		}
		pi.Item = m
		p.Items = append(p.Items, pi)
	}
	if landedSHA > 0 {
		p.Notes = append(p.Notes, fmt.Sprintf(
			"큐 항목 %d건의 `landed_sha:` 를 **item.landed_ref 에 넣지 않는다** — 그 칸에는 러너가 실제로 "+
				"fast-forward 한 sha 만 들어간다. 옛 도구는 '메인 트리의 지금 HEAD'를 적어 **남의 커밋이 "+
				"랜딩 sha 로 박힌 관측이 3회** 있다. 원문은 항목 본문 꼬리에 그대로 남는다", landedSHA))
	}

	// ── 대시보드
	if sc.DashSeen {
		p.SkippedAxes = append(p.SkippedAxes, sc.Dash.Skipped...)
		emptyNarrative := 0
		for i, l := range sc.Dash.Landings {
			path := fmt.Sprintf("DATA.landings[%d]", i)
			if isFatal("dashboard", path) {
				continue
			}
			if _, err := ParseDashAt(l.At); err != nil {
				reject("dashboard", path, "at", "bad_time", err.Error(), true)
				continue
			}
			// ★ 서사가 통째로 빈 랜딩이 실물에 5건 있다(`note: ''`). 판단 표는 빈 본문을
			//   받지 않으므로(스키마 CHECK) 그대로는 못 들어간다. 그렇다고 거절하면
			//   **제목과 커밋 sha 까지 함께 사라진다** — 그 둘은 실재하는 기록이다.
			//   지어내지 않고 **제목을 본문 자리에 옮긴다**(새 문장을 만들지 않는다).
			if strings.TrimSpace(l.Body) == "" && strings.TrimSpace(l.Note) == "" {
				if strings.TrimSpace(l.Title) == "" {
					reject("dashboard", path, "body", "empty_record",
						"`body`·`note`·`title` 이 전부 비었다 — 넣을 것이 없다", true)
					continue
				}
				l.Body = l.Title
				emptyNarrative++
				reject("dashboard", path, "body", "empty_narrative",
					fmt.Sprintf("서사(`body`·`note`)가 비어 제목을 본문 자리에 넣는다 — 제목 %q",
						clip(l.Title, 80)), false)
			}
			p.Landings = append(p.Landings, l)
		}
		if emptyNarrative > 0 {
			p.Notes = append(p.Notes, fmt.Sprintf(
				"랜딩 %d건은 서사가 빈 문자열이라 **제목을 본문 자리에 넣었다** — 판단 표는 빈 본문을 "+
					"받지 않는데(스키마 CHECK) 거절하면 제목과 커밋 sha 까지 함께 사라진다. 새 문장은 만들지 않았다",
				emptyNarrative))
		}

		judged := sc.Dash.Judged
		switch {
		case len(sc.Dash.Parts) == 0:
			// 파트가 없으면 판정할 것이 없다. 사유 없이 침묵하지는 않는다.
		case strings.TrimSpace(judged.At) == "" || strings.TrimSpace(judged.SHA) == "":
			reject("dashboard", "DATA.parts", "", "no_evidence",
				fmt.Sprintf("`judged` 에 at·sha 가 없어 진척 %d건의 근거 문자열을 만들 수 없다 — "+
					"snapshot(method='manual') 은 근거 없이는 못 들어간다(스키마 CHECK). "+
					"근거를 지어내지 않고 parts 를 통째로 거절한다", len(sc.Dash.Parts)), true)
		default:
			for i, pt := range sc.Dash.Parts {
				if isFatal("dashboard", fmt.Sprintf("DATA.parts[%d]", i)) {
					continue
				}
				p.Parts = append(p.Parts, PlannedPart{
					Key:      "part:" + pt.Name,
					Value:    partValueJSON(pt),
					Evidence: PartEvidence(judged, pt),
					Digest:   judged.SHA,
				})
			}
			p.Notes = append(p.Notes, "진척 `parts` 는 snapshot(method='manual')로 들어간다 — "+
				"근거는 `judged` 블록에서 만든다. **손으로 올릴 칸이 아니다**: 그 숫자는 "+
				"12파트 전수 판정의 결과이고 재계산이 20분 넘게 걸린다")
		}

		for i, s := range sc.Dash.Issues {
			id := fmt.Sprintf("legacy-issue-%02d", i+1)
			p.Issues = append(p.Issues, PlannedItem{Item: model.Item{
				Project: opt.Project, ID: id,
				Title:  clip(firstLine(s), 120),
				Body:   s,
				Labels: []string{"출처:대시보드 issues"},
				State:  model.ItemOpen,
			}})
		}
		for i, b := range sc.Dash.Blockers {
			id := fmt.Sprintf("legacy-blocker-%02d", i+1)
			m := model.Item{
				Project: opt.Project, ID: id,
				Title:  clip(firstLine(b.T), 120),
				Body:   blockerBody(b),
				Labels: []string{"출처:대시보드 blockers", "kind:" + b.Kind},
				State:  model.ItemOpen,
			}
			for _, q := range b.QIDs {
				if itemIDs[q] {
					m.After = append(m.After, model.After{Item: q})
					continue
				}
				p.Gone = append(p.Gone, Gone{
					Kind: "blocker.qid", From: id, Target: q,
					Detail: "막힘이 가리키는 큐 항목이 없다 — 그 항목이 닫히면서 포인터만 남은 것이다. " +
						"대시보드가 스스로 이 경고를 띄우던 축이고, FK 로 옮기면 그 침묵이 원리적으로 사라진다",
				})
			}
			p.Blockers = append(p.Blockers, PlannedItem{Item: m})
		}
	}

	// ── 대조표
	p.Counts = buildCounts(sc, p)
	sortRejects(p.Rejects)
	sortGone(p.Gone)
	sort.SliceStable(p.Unclassified, func(i, j int) bool {
		if p.Unclassified[i].File != p.Unclassified[j].File {
			return p.Unclassified[i].File < p.Unclassified[j].File
		}
		return p.Unclassified[i].Name < p.Unclassified[j].Name
	})
	return p
}

// PartEvidence 는 진척 숫자의 근거 문자열이다. 순수 함수다.
//
// snapshot 은 `CHECK (method <> 'manual' OR evidence IS NOT NULL)` 이다 —
// "손으로 올리지 마라"는 규율이 여기서 제약이 된다. 그 근거는 지어내는 것이 아니라
// 대시보드가 이미 갖고 있는 `judged` 블록(전수 판정의 날짜·코드 sha·덱)에서 온다.
func PartEvidence(j Judged, p Part) string {
	b := fmt.Sprintf("전수 판정 %s · 코드 %s", j.At, j.SHA)
	if j.Deck != "" {
		b += " · 덱 " + j.Deck
	}
	if j.Items > 0 {
		b += fmt.Sprintf(" (항목 %d: 완료 %d · 부분 %d · 없음 %d)", j.Items, j.Done, j.Partial, j.Nothing)
	}
	b += fmt.Sprintf(" · 이 파트 d=%d q=%d n=%d", p.D, p.Q, p.N)
	if strings.TrimSpace(p.Delta) != "" {
		b += " · 그 뒤 랜딩: " + clip(p.Delta, 400)
	}
	return b
}

func partValueJSON(p Part) string {
	// 손으로 만든다 — 필드가 일곱이고 전부 값을 우리가 통제하므로 인코더를 부를 이유가 없다.
	// 문자열은 jsonString 이 이스케이프한다.
	return fmt.Sprintf(`{"pct":%d,"d":%d,"q":%d,"n":%d,"state":%s,"owner":%s,"delta":%s}`,
		p.Pct, p.D, p.Q, p.N, jsonString(p.State), jsonString(p.Owner), jsonString(p.Delta))
}

func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func blockerBody(b BlockerItem) string {
	var sb strings.Builder
	sb.WriteString(b.T)
	sb.WriteString("\n\n")
	sb.WriteString(b.B)
	if len(b.QIDs) > 0 {
		sb.WriteString("\n\nqid: " + strings.Join(b.QIDs, " "))
	}
	return sb.String()
}

func buildCounts(sc Scan, p ImportPlan) []CountRow {
	rejCount := map[string]int{}
	for _, r := range p.Rejects {
		if r.Fatal {
			rejCount[r.Source]++
		}
	}
	dashAccept := len(p.Landings) + len(p.Parts) + len(p.Issues) + len(p.Blockers)
	dashFound := len(sc.Dash.Landings) + len(sc.Dash.Parts) + len(sc.Dash.Issues) + len(sc.Dash.Blockers)
	return []CountRow{
		{"세션 카드", sc.Found["sessions"].Files, sc.Found["sessions"].Bytes, len(p.Sessions), rejCount["session"]},
		{"큐 항목", sc.Found["queue"].Files, sc.Found["queue"].Bytes, len(p.Items), rejCount["queue"]},
		{"핸드오프", sc.Found["handoffs"].Files, sc.Found["handoffs"].Bytes, len(p.Handoffs), rejCount["handoff"]},
		{"대시보드 레코드", dashFound, sc.Found["dashboard"].Bytes, dashAccept, rejCount["dashboard"]},
	}
}
