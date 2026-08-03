package legacy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// `fd export --to-legacy` — 옛 형식으로 되쓴다.
//
// **되돌릴 수 없는 이관은 1인 운영에서 착수 자체가 안 된다.** 실패하면 그날 전 세션이
// 멈추고 복구할 사람이 하나뿐이다. 그래서 옛 도구를 지우지 않고, 이 명령이 옛 도구가
// 읽을 수 있는 트리를 다시 만든다.
//
// # 왕복이 완전하지 않다 — 무엇이 안 돌아오는지
//
// 아래는 **일부러** 안 돌아오는 것이고, [RoundTripLosses] 가 같은 목록을 문자열로 낸다
// (시험이 그 함수를 부른다 — 문서와 코드가 갈라지지 않게).
//
//	세션 카드 `branch`·`head`  파생이라 fd 에 칸이 없다. ref_state 가 git 에서 직접 읽는 축이고,
//	                          손으로 베낀 스냅숏은 원본이 움직이는 순간 조용히 거짓이 된다.
//	세션 카드 `pid`            칸이 없다. pid 死를 근거로 살아 있는 세션을 죽었다고 판정한 사고가
//	                          실재해서 스키마가 그 칸을 아예 안 만든다.
//	`slides/status.html`      되쓰지 않는다. 부분 재생성한 DATA 블록을 그 파일에 끼우면 인라인
//	                          스크립트가 통째로 깨질 수 있고, 그 사고가 실제로 나서 12커밋 동안
//	                          페이지가 백지였다. 원본은 읽기만 했으므로 그 파일이 그대로 정본이다.
//	`gone` 으로 판정된 포인터   가리키는 대상이 없어 FK 로 못 옮긴 것이라 되쓸 값이 없다.
//	거절된 항목·카드           애초에 안 들어갔으므로 안 나온다. 원본이 그대로 정본이다.

// ExportResult 는 되쓴 결과다.
type ExportResult struct {
	Sessions int
	Items    int
	Handoffs int
	Files    []string // 만든 파일(출력 디렉토리 기준)
	Losses   []string
}

// RoundTripLosses 는 `import → export` 왕복에서 **돌아오지 않는 것** 전량이다.
//
// 순수 함수로 두는 이유: 시험이 이 목록을 직접 부르고, 되쓰기 산출물이 실제로 그
// 목록대로만 잃는지 대조한다. 산문에만 적어 두면 코드가 더 잃기 시작해도 아무도 모른다.
func RoundTripLosses() []string {
	return []string{
		"세션 카드 `branch` — 파생(ref_state)이라 fd 에 칸이 없다. 빈 값으로 나간다",
		"세션 카드 `head` — 같은 이유로 빈 값으로 나간다",
		"세션 카드 `pid` — 스키마에 칸이 없다(pid 死로 살아 있는 세션을 죽었다고 판정한 사고 때문에 일부러 없앴다). 빈 값으로 나간다",
		"`slides/status.html` — 되쓰지 않는다. 부분 DATA 블록을 끼우면 페이지가 통째로 안 그려질 수 있다(실사고 있음)",
		"`gone` 으로 판정된 `handoff:`·`after:` 포인터 — 대상이 없어 FK 가 안 만들어졌으므로 되쓸 값이 없다",
		"Fatal 로 거절된 파일 전부 — DB 에 안 들어갔으므로 안 나온다. 원본이 그대로 정본이다",
		"선택 필드 `blocks_on` 의 frontmatter 위치 — 규약 순서(`handoff` 다음)로 고정해 내보낸다. " +
			"원본 12건 중 1건이 `handoff` 앞에 적어 그 파일만 줄 순서가 바뀐다(값은 같다)",
		"본문이 비어 있던 절 — 판단 표가 빈 본문을 안 받아 그 자리에 " +
			"\"(원본에서 이 절의 본문이 비어 있었다)\" 가 들어간다. 절 이름은 그대로다(원본 36장 중 1장)",
	}
}

// ExportLegacy 는 DB 를 옛 디렉토리 구조로 되쓴다.
func ExportLegacy(ctx context.Context, st *store.Store, project, outDir string) (ExportResult, error) {
	var res ExportResult
	res.Losses = RoundTripLosses()

	sessions, err := st.ListSessions(ctx, project)
	if err != nil {
		return res, err
	}
	items, err := st.ListItems(ctx, project)
	if err != nil {
		return res, err
	}
	// 핸드오프는 최신순 상한이 있는 조회라 상한을 항목 수보다 넉넉히 준다.
	// 상한에 걸려 조용히 잘리면 되쓴 트리가 원본보다 적어지고, 그 차이는 세어 보기 전에는 안 보인다.
	handoffs, err := st.ListJudgmentsByKind(ctx, project, model.JudgmentHandoff, 100000)
	if err != nil {
		return res, err
	}

	// 항목 id → 그 항목을 가리키는 핸드오프 파일 이름. 큐 frontmatter 의 `handoff:` 를 복원한다.
	handoffOf := map[string]string{}
	for _, h := range handoffs {
		for _, l := range h.Links {
			if l.TargetKind == "item" {
				handoffOf[l.TargetID] = h.Title
			}
		}
	}

	write := func(rel, body string) error {
		p := filepath.Join(outDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fmt.Errorf("되쓰기 디렉토리 생성 실패(%q): %w", clip(filepath.Dir(p), 200), err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return fmt.Errorf("되쓰기 실패(%q): %w", clip(rel, 200), err)
		}
		res.Files = append(res.Files, rel)
		return nil
	}

	for _, s := range sessions {
		file, ok := strings.CutPrefix(s.CCSessionID, "legacy:")
		if !ok {
			// 이관이 만들지 않은 세션이다. 옛 형식에는 대응하는 파일 이름이 없다 —
			// 이름을 지어내면 다음 이관이 그것을 원본으로 읽는다.
			continue
		}
		body, err := renderSessionCard(ctx, st, s, file)
		if err != nil {
			return res, err
		}
		if err := write(".claude/sessions/"+file, body); err != nil {
			return res, err
		}
		res.Sessions++
	}

	for _, it := range items {
		bucket, ok := legacyBucket(it.State)
		if !ok {
			continue
		}
		if strings.HasPrefix(it.ID, "legacy-issue-") || strings.HasPrefix(it.ID, "legacy-blocker-") {
			// 대시보드에서 온 것은 큐 파일이 원본이 아니다. 되쓰면 다음 이관이
			// 같은 내용을 큐 항목으로 한 번 더 들여와 원본에 없던 항목이 늘어난다.
			continue
		}
		if err := write(".claude/queue/"+bucket+"/"+it.ID+".md",
			renderQueueItem(it, handoffOf[it.ID])); err != nil {
			return res, err
		}
		res.Items++
	}

	for _, h := range handoffs {
		if strings.TrimSpace(h.Title) == "" || strings.Contains(h.Title, "/") {
			// 랜딩 서사(대시보드에서 온 판단)는 파일 이름이 아니라 제목이라 되쓸 자리가 없다.
			continue
		}
		if !strings.HasSuffix(h.Title, ".md") {
			continue
		}
		if err := write(".claude/handoffs/"+h.Title, h.Body); err != nil {
			return res, err
		}
		res.Handoffs++
	}
	sort.Strings(res.Files)
	return res, nil
}

func legacyBucket(s model.ItemState) (string, bool) {
	switch s {
	case model.ItemOpen:
		return "items", true
	case model.ItemClaimed:
		return "claims", true
	case model.ItemDone:
		return "done", true
	case model.ItemDropped:
		return "dropped", true
	default:
		return "", false
	}
}

// renderSessionCard 는 세션 카드 한 장을 옛 형식으로 되쓴다.
//
// 머리 8필드의 **순서를 원본과 같게** 낸다. 옛 도구가 순서를 따지지는 않지만,
// 순서가 흔들리면 원본과 되쓴 것을 diff 로 대조할 수 없고 그 대조가 이 명령의 존재 이유다.
func renderSessionCard(ctx context.Context, st *store.Store, s model.Session, file string) (string, error) {
	js, err := st.ListJudgmentsBySession(ctx, s.ID)
	if err != nil {
		return "", err
	}
	track := strings.TrimSuffix(file, ".md")

	// `updated` 는 **카드를 마지막으로 쓴 시각**이다. session.opened_at 은 그 축이 아니고
	// (이관 시점의 지금이 들어간다) 세션에 그 값을 담을 칸도 없다. 대신 절 판단들이
	// 카드의 `updated` 를 그대로 물고 있으므로 그중 가장 늦은 것을 쓴다 —
	// 파생 가능한 것에 칸을 만들지 않는다는 원칙 ①이 여기에도 그대로 걸린다.
	updated := s.OpenedAt // 절이 하나도 없을 때의 폴백. 그때는 이관 시각이 나간다
	for i, j := range js {
		if i == 0 || j.At.After(updated) {
			updated = j.At
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "track: %s\n", track)
	fmt.Fprintf(&b, "desc: %s\n", s.Label)
	fmt.Fprintf(&b, "state: %s\n", s.State)
	// branch·head·pid 는 파생이거나 칸이 없다. **빈 값으로 낸다** —
	// 지어내면 옛 도구가 그것을 사실로 읽고, 그 거짓이 어디에도 안 뜬다.
	fmt.Fprintf(&b, "branch: \n")
	fmt.Fprintf(&b, "worktree: %s\n", s.Worktree)
	fmt.Fprintf(&b, "head: \n")
	fmt.Fprintf(&b, "pid: \n")
	fmt.Fprintf(&b, "updated: %s\n", kstStamp(updated))
	b.WriteString("---\n")
	for _, j := range js {
		if strings.TrimSpace(j.Title) == "" {
			b.WriteString(j.Body + "\n\n")
			continue
		}
		// ★ 절 이름은 judgment.title 에 원문 그대로 있다. 비규약 절이 여기서 그대로 돌아온다.
		fmt.Fprintf(&b, "## %s\n%s\n\n", j.Title, j.Body)
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

// renderQueueItem 은 큐 항목 하나를 옛 형식으로 되쓴다.
//
// 본문은 **원문 그대로**다(꼬리 필드 landed_sha·closed·dropped_reason 까지 본문에 들어 있다).
// 그 값들을 컬럼에서 다시 만들지 않는 이유: 만드는 순간 원본과 다른 문자열이 나갈 수 있고,
// 그 차이는 되쓴 트리를 원본과 대조하기 전에는 안 보인다.
func renderQueueItem(it model.Item, handoff string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "id: %s\n", it.ID)
	fmt.Fprintf(&b, "title: %s\n", it.Title)
	fmt.Fprintf(&b, "repo: %s\n", LabelValue(it.Labels, "repo"))
	fmt.Fprintf(&b, "paths: %s\n", strings.Join(it.Paths, " "))
	fmt.Fprintf(&b, "track: %s\n", LabelValue(it.Labels, "track"))
	fmt.Fprintf(&b, "needs: %s\n", LabelValue(it.Labels, "needs"))
	fmt.Fprintf(&b, "after: %s\n", strings.Join(afterStrings(it.After), " "))
	if h := handoff; h != "" {
		fmt.Fprintf(&b, "handoff: .claude/handoffs/%s\n", h)
	} else {
		b.WriteString("handoff: \n")
	}
	if v := LabelValue(it.Labels, "blocks_on"); v != "" {
		fmt.Fprintf(&b, "blocks_on: %s\n", v)
	}
	fmt.Fprintf(&b, "created: %s\n", kstStamp(it.CreatedAt))
	b.WriteString("---\n")
	b.WriteString(it.Body)
	return b.String()
}

// afterStrings 는 선행 조건을 옛 표기로 되돌린다.
//
// dep_sha 는 `<sha>@landed` 다 — 옛 규약이 랜딩된 것을 그렇게 적었다.
// ★ 정렬하지 않는다. 저장 계층이 삽입 순서(=원본의 줄 순서)로 내주므로 그대로 쓴다.
//
//	여기서 사전순으로 다시 정렬하면 값은 같은데 줄이 달라져 원본과 diff 가 안 된다.
func afterStrings(as []model.After) []string {
	var out []string
	for _, a := range as {
		switch {
		case a.Item != "":
			out = append(out, a.Item)
		case a.SHA != "":
			out = append(out, a.SHA+"@landed")
		case a.Job != "":
			// 잡은 Tier B 개념이라 옛 형식에 자리가 없다. 지어내지 않고 건너뛴다.
		}
	}
	return out
}

// kstStamp 는 옛 도구가 쓰던 표기다.
//
// 옛 산출물은 시각을 전부 `+09:00` 으로 적었다. UTC 로 내면 같은 순간이지만 문자열이
// 달라서 원본과 되쓴 것을 diff 로 대조할 수 없다 — 대조가 이 명령의 존재 이유다.
func kstStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(KST).Format(time.RFC3339)
}
