package mcpsrv

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

var t0 = time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)

func TestFormatAge(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"초", 42 * time.Second, "42초"},
		{"분", 12 * time.Minute, "12분"},
		{"시간", 3*time.Hour + 7*time.Minute, "3시간 7분"},
		{"일", 50 * time.Hour, "2일 2시간"},

		// ── 표 밖 케이스 ──
		{"0", 0, "0초"},
		{"음수(시계 역전)", -5 * time.Second, "0초"},
		{"딱 1분", time.Minute, "1분"},
		{"딱 1시간", time.Hour, "1시간 0분"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FormatAge(c.d); got != c.want {
				t.Fatalf("FormatAge(%v) = %q, 기대 %q", c.d, got, c.want)
			}
		})
	}
}

// TestFormatSignalsKeepsFourAxesApart 는 신호를 합치지 않는다는 §4 규율을 문자열로 단정한다.
func TestFormatSignalsKeepsFourAxesApart(t *testing.T) {
	sig := map[model.SignalKind]time.Time{
		model.SignalPrompt: t0.Add(-2 * time.Minute),
		model.SignalTool:   t0.Add(-40 * time.Second),
	}
	got := FormatSignals(sig, t0)
	if !strings.Contains(got, "prompt 2분") || !strings.Contains(got, "tool 40초") {
		t.Fatalf("신호 두 축이 나란히 안 나온다: %q", got)
	}
	// 안 온 종류는 0값으로 채우지 않는다 — 부재와 0을 가른다.
	if strings.Contains(got, "commit") {
		t.Fatalf("한 번도 안 온 신호가 찍혔다: %q", got)
	}
	if got := FormatSignals(nil, t0); got != "신호 없음" {
		t.Fatalf("신호 0건에 %q — '신호 없음'이어야 한다", got)
	}
}

// TestRenderTailSeparatesZeroFromUnobserved 는 이 제품의 뿌리 원인 하나를 문자열로 막는다:
// "겹침 없음"과 "이 축을 안 본다"가 같은 화면이 되면 도구가 자기가 무엇을 안 보는지 모른다.
func TestRenderTailSeparatesZeroFromUnobserved(t *testing.T) {
	zero := RenderTail(TailInput{Now: t0, NotesObserved: true, OverlapsObserved: true})
	unobs := RenderTail(TailInput{Now: t0, NotesObserved: true, OverlapsObserved: false})

	if !strings.Contains(zero, "겹침: 없음") {
		t.Fatalf("겹침 0건이 '없음'으로 안 나온다:\n%s", zero)
	}
	if strings.Contains(unobs, "겹침: 없음") {
		t.Fatalf("안 읽은 축이 '없음'으로 나왔다 — 이 둘이 같으면 축이 조용히 죽는다:\n%s", unobs)
	}
	if !strings.Contains(unobs, "읽지 않았다") {
		t.Fatalf("안 읽었다는 사실이 꼬리에 없다:\n%s", unobs)
	}

	withOverlap := RenderTail(TailInput{
		Now: t0, NotesObserved: true, OverlapsObserved: true,
		Overlaps: []judge.Overlap{{SessionID: "01ABCDEFGH", Label: "트랙2",
			Pairs: [][2]string{{"pipeline/", "pipeline/x.py"}}}},
	})
	if !strings.Contains(withOverlap, "거르지 않고 알린다") ||
		!strings.Contains(withOverlap, "pipeline/↔pipeline/x.py") {
		t.Fatalf("겹침이 무엇끼리인지 안 보인다:\n%s", withOverlap)
	}

	// 알림 축도 같은 규율이다.
	notes := RenderTail(TailInput{
		Now: t0, NotesObserved: true, OverlapsObserved: true,
		Notes: []model.Judgment{{Kind: model.JudgmentAsk, SessionID: "01ZZZZZZZZ",
			At: t0.Add(-9 * time.Minute), Title: "contracts/ 는 건드리지 마라"}},
	})
	if !strings.Contains(notes, "contracts/ 는 건드리지 마라") || !strings.Contains(notes, "9분 전") {
		t.Fatalf("알림 본문·나이가 꼬리에 없다:\n%s", notes)
	}
	if !strings.Contains(notes, "확인 원장이 없다") {
		t.Fatalf("'미확인'을 '최근'으로 근사했다는 사실이 안 적혀 있다:\n%s", notes)
	}
}

// TestRenderPickCarriesBranchAndWorktree 는 설계 §6 의
// "pick 꼬리에 브랜치·워크트리 명령이 온다"를 응답 문자열로 단정한다.
func TestRenderPickCarriesBranchAndWorktree(t *testing.T) {
	item := model.Item{
		Project: "proj", ID: "t5-iam", Title: "IAM 컬럼 상한", Body: "본문이다",
		Paths: []string{"services/console-api/"}, State: model.ItemOpen, CreatedAt: t0,
	}
	res := service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다", Scope: "후보 = 열린 항목 3건",
		Item: &item, Branch: item.ID,
		Setup: service.SetupCommands("/home/a/proj", "main", item.ID),
		Rejected: []model.Rejection{
			{Item: "t4-x", Reason: judge.RejectClaimed, Detail: "세션 01AB 가 선점했다"},
			{Item: "t6-y", Reason: judge.AfterUnmetItem, Detail: "선행 t3-z 가 안 끝났다"},
		},
	}
	got := RenderPick(res, t0)

	if !strings.Contains(got, "브랜치: t5-iam") {
		t.Fatalf("브랜치 이름이 없다:\n%s", got)
	}
	if !strings.Contains(got, "git worktree add '.flightdeck/worktrees/t5-iam' -b t5-iam 'main'") {
		t.Fatalf("워크트리 준비 명령이 없다:\n%s", got)
	}
	// 기계가 세는 값(사유 코드)은 사람 말로 풀지 않고 그대로 보인다.
	for _, code := range []string{judge.RejectClaimed, judge.AfterUnmetItem} {
		if !strings.Contains(got, code) {
			t.Fatalf("탈락 사유 코드 %q 가 응답에 없다:\n%s", code, got)
		}
	}
	if !strings.Contains(got, "아직 선점하지 않았다") {
		t.Fatalf("추천이 선점으로 오해될 수 있다:\n%s", got)
	}

	// id 가 안전하지 않아 명령을 못 만든 경우 — 침묵하지 않는다.
	bad := res
	bad.Branch = "--evil"
	bad.Setup = service.SetupCommands("/home/a/proj", "main", "--evil")
	if out := RenderPick(bad, t0); !strings.Contains(out, "워크트리 준비 명령을 만들지 않았다") {
		t.Fatalf("명령을 못 만든 사실이 응답에 없다:\n%s", out)
	}
}

// TestRenderPickCarriesQueueSizeInEveryMode 는 네 모드 어느 쪽으로 들어와도
// 같은 이름의 같은 줄을 본다는 것이다 — 세션이 모드를 보고 어디를 읽을지 고르지 않아도 된다.
func TestRenderPickCarriesQueueSizeInEveryMode(t *testing.T) {
	n := 5
	for _, mode := range []service.PickMode{
		service.PickRecommended, service.PickClaimed, service.PickResumed, service.PickNone,
	} {
		got := RenderPick(service.PickResult{Mode: mode, Reason: "사유다", QueueOpen: &n}, t0)
		if !strings.Contains(got, "큐 열림 5건") {
			t.Fatalf("%s 모드 응답에 큐 열림 수가 없다:\n%s", mode, got)
		}
	}
}

// TestRenderPickNeverCallsAnAbsentQueueSizeZero 가 이 설계에서 가장 중요한 시험이다.
//
// nil 이 되는 경로는 구버전 서버(SkewBanner 가 안 잡는다) · 필드가 생기기 전의 캐시 ·
// 조회 실패 셋이다. 그것을 "큐 열림 0건" 으로 찍으면 신선한 온라인 응답이 거짓을 단정하고,
// none 모드에는 그 모순을 드러낼 항목조차 없어 에이전트가 "큐가 비었다" 로 읽고 세션을 접는다.
// 스큐 구간에서만 나타나는 실패라 사람이 재현하기 어렵다 — 이 시험이 유일한 방벽이다.
func TestRenderPickNeverCallsAnAbsentQueueSizeZero(t *testing.T) {
	got := RenderPick(service.PickResult{Mode: service.PickNone, Reason: "적격 0건이다"}, t0)
	if strings.Contains(got, "큐 열림 0건") {
		t.Fatalf("부재를 0건으로 단정했다:\n%s", got)
	}
	if !strings.Contains(got, "이 응답에 없다") {
		t.Fatalf("부재를 침묵으로 접었다 — 안 본 것을 침묵하지 않는다:\n%s", got)
	}
}

// TestRenderPickPrintsAGenuineZero 는 부재의 반대편을 못박는다.
//
// nil 이 "0건" 이 되면 안 된다는 것은 다른 시험이 지킨다. 이 시험은 그 반대 —
// **진짜 0건이 부재로 접히면 안 된다**. 둘 중 하나만 지키면 `*QueueOpen > 0` 같은
// '정리'가 시험을 전부 통과하면서 빈 큐를 "서버가 안 냈다" 로 바꿔 놓는다.
func TestRenderPickPrintsAGenuineZero(t *testing.T) {
	zero := 0
	got := RenderPick(service.PickResult{Mode: service.PickNone, Reason: "적격 0건이다", QueueOpen: &zero}, t0)
	if !strings.Contains(got, "큐 열림 0건") {
		t.Fatalf("진짜 0건이 숫자로 안 나왔다:\n%s", got)
	}
	if strings.Contains(got, "이 응답에 없다") {
		t.Fatalf("진짜 0건을 부재로 접었다:\n%s", got)
	}
}

// synthBoard 는 세션 n개짜리 보드를 짓는다(순수 함수 시험용).
func synthBoard(n int) service.BoardView {
	v := service.BoardView{
		Project: model.Project{ID: "sample-platform", Path: "/home/a/p", DefaultBranch: "main"},
		At:      t0, Window: 8 * time.Hour,
		Derived: service.Derived{Freshness: model.Freshness{Source: "git", ObservedAt: t0}},
	}
	for i := 0; i < n; i++ {
		v.Sessions = append(v.Sessions, service.SessionCard{
			View: model.SessionView{
				Session: model.Session{
					ID:    fmt.Sprintf("01SESSION%04d", i),
					Label: fmt.Sprintf("트랙 %d — 파이프라인 색인 경로 정리와 계약 개정 반영", i),
					State: model.SessionActive,
				},
				Signals: map[model.SignalKind]time.Time{
					model.SignalPrompt: t0.Add(-time.Duration(i) * time.Minute),
					model.SignalTool:   t0.Add(-time.Duration(i) * time.Second),
				},
				Paths: []string{
					"pipeline/indexer/", "contracts/search-index/", "services/data-api/",
					"deploy/k3s/base/", "tools/staging-load-images.sh", "Makefile",
				},
				HasFootprint: true,
				Claims:       []string{fmt.Sprintf("t%d-item", i)},
				Branch:       fmt.Sprintf("t%d-item", i),
				AheadMain:    i,
			},
			BranchKnown: true, AheadKnown: true,
		})
	}
	for i := 0; i < 5; i++ {
		v.OpenItems = append(v.OpenItems, model.Item{
			ID: fmt.Sprintf("q-%d", i), Title: "열린 항목 제목", State: model.ItemOpen})
	}
	return v
}

// TestRenderBoardBudget 은 설계 §6 의 "board 기본 1,200토큰"을 단정한다.
//
// ★ 대조를 **먼저** 단정한다: detail 출력이 예산을 넘지 않으면 이 시험은
// 자르는 코드를 아예 통과하지 않은 채 초록을 낸다(빈 표에 UPDATE 를 걸어 놓고
// 트리거를 시험했다고 믿은 실패와 같은 모양이다).
func TestRenderBoardBudget(t *testing.T) {
	v := synthBoard(30)
	tail := RenderTail(TailInput{Now: t0, NotesObserved: true, OverlapsObserved: true})

	detail := RenderBoard(v, BoardRenderOptions{Detail: true, Now: t0, Tail: tail})
	if got := EstimateTokens(detail); got <= BoardTokenBudget {
		t.Fatalf("대조가 성립하지 않았다: detail 출력이 %d토큰이라 예산 %d 를 안 넘는다 — "+
			"이 입력으로는 자르는 경로가 안 돈다", got, BoardTokenBudget)
	}

	brief := RenderBoard(v, BoardRenderOptions{Now: t0, Tail: tail})
	if got := EstimateTokens(brief); got > BoardTokenBudget {
		t.Fatalf("기본 출력이 %d토큰이다 — 상한 %d\n%s", got, BoardTokenBudget, brief)
	}
	// 조용히 자르지 않는다.
	if !strings.Contains(brief, "접었다") {
		t.Fatalf("잘랐는데 잘랐다는 사실이 없다:\n%s", brief)
	}
	if !strings.Contains(brief, "detail=true") {
		t.Fatalf("전부 보는 방법이 안 적혀 있다:\n%s", brief)
	}
	// 꼬리는 예산 안에 함께 든다 — 예산 밖에 두면 이 단정이 실제 응답을 안 보게 된다.
	if !strings.Contains(brief, "── 꼬리 ──") {
		t.Fatalf("꼬리가 보드 출력에 없다:\n%s", brief)
	}

	// 세션이 적으면 자르지 않는다(상한이 상시 발동하면 판별력이 0이 된다).
	small := RenderBoard(synthBoard(2), BoardRenderOptions{Now: t0, Tail: tail})
	if strings.Contains(small, "접었다") {
		t.Fatalf("세션 2건인데 잘랐다:\n%s", small)
	}
}

// heavyTail 은 **실제로 관측된 모양의** 무거운 고정분을 만든다 — 겹침 다수 + 표류 배너.
//
// ★ TestRenderBoardBudget 이 쓰는 꼬리는 알림 0·겹침 0·배너 없음이라 두 줄짜리다.
// 그래서 그 시험은 "고정분이 크면 어떻게 되나"를 **원리적으로 못 본다.** 카드가 0장이 된
// 2026-08-05 의 실제 응답이 초록으로 지나간 이유가 그것이다. 이 헬퍼가 그 구멍을 메운다.
// countCards 는 렌더된 보드에 실제로 남은 카드 수다.
// synthBoard 의 id 는 ShortID 로 "01SESSIO…" 까지만 찍히므로 그 접두로 센다.
func countCards(rendered string) int { return strings.Count(rendered, "01SESSIO") }

func heavyTail(overlaps, twins int) string {
	var ol []judge.Overlap
	for i := 0; i < overlaps; i++ {
		ol = append(ol, judge.Overlap{
			SessionID: fmt.Sprintf("01OTHER%04d", i),
			Pairs: [][2]string{
				{".superpowers/sdd/2026-08-05-x/progress.md", ".superpowers/sdd/2026-08-05-x/progress.md"},
				{"plugins/flightdeck/server/internal/service/board.go", "plugins/flightdeck/server/internal/service/board.go"},
				{"plugins/flightdeck/server/internal/mcpsrv/render.go", "plugins/flightdeck/server/internal/mcpsrv/render.go"},
			},
		})
	}
	var tw []CoordinateTwin
	for i := 0; i < twins; i++ {
		tw = append(tw, CoordinateTwin{
			SessionID:   fmt.Sprintf("01KZ7CARD%013d", i),
			CCSessionID: fmt.Sprintf("%08d-6ca4-4321-9912-f713e791f3fe", i),
		})
	}
	return RenderTail(TailInput{
		Now: t0, NotesObserved: true, OverlapsObserved: true, Overlaps: ol,
		Banner: RenderDrift(tw, "ce5c2e79-767f-4e85-8893-52a0219f6d9a", ""),
	})
}

// TestRenderBoardKeepsCardFloorWhenFixedPartIsHuge 는 **카드 0장인 보드를 금지한다.**
//
// 2026-08-05 01:12 UTC 실측: 살아 있는 세션 34건인데 카드 0장, "34건을 접었다" 한 줄만.
// 고정분(머리·발·꼬리·배너)이 예산 1200 을 156% 먹어서 예산 루프가 첫 블록에서 즉시
// break 했다. 그 출력은 예산도 못 지키고(고정분이 이미 넘었다) 본체도 100% 잃는다.
func TestRenderBoardKeepsCardFloorWhenFixedPartIsHuge(t *testing.T) {
	// ── ① 실측된 그 모양 ── 2026-08-05 01:12 의 응답(세션 34건·겹침 16건·쌍둥이 10건)이
	// 이제 카드를 낸다. **예산 안이라고는 단정하지 않는다** — 고정분이 그만큼 크면 예산을
	// 넘기는 것이 설계된 거동이고(바닥이 예산을 이긴다), 그때 넘겼다고 말하는지를 대신 본다.
	observed := RenderBoard(synthBoard(34), BoardRenderOptions{Now: t0, Tail: heavyTail(16, 10)})
	if cards := countCards(observed); cards < 1 {
		t.Fatalf("실측된 그 입력에서 카드가 0장이다 — 카드 0장인 보드는 보드가 아니다:\n%s", observed)
	}
	// 상한 둘이 실제로 걸렸는지 — 이 두 단정이 drift·꼬리 상한의 회귀 가드다.
	if !strings.Contains(observed, "11건 더") {
		t.Fatalf("겹침 16건인데 꼬리 상한이 안 걸렸다:\n%s", observed)
	}
	if !strings.Contains(observed, "7건 더") {
		t.Fatalf("쌍둥이 10건인데 배너 상한이 안 걸렸다:\n%s", observed)
	}

	// ── ② 바닥 자체 ── 고정분이 예산을 넘기는 갈래를 직접 지난다.
	// 상한 둘이 붙은 뒤로는 현실적인 꼬리로 예산을 못 넘기므로(그것이 상한의 목적이다)
	// 예산을 좁혀서 그 갈래를 만든다 — 바닥은 예산 값과 무관한 성질이어야 한다.
	tail := heavyTail(16, 10)
	const tight = 400

	// 대조 먼저: 고정분만으로 이 예산을 넘겨야 바닥이 발동하는 갈래를 실제로 지난다.
	empty := RenderBoard(service.BoardView{
		Project: model.Project{ID: "sample-platform", DefaultBranch: "main"},
		At:      t0, Window: 8 * time.Hour,
	}, BoardRenderOptions{Now: t0, Tail: tail, Budget: tight})
	if got := EstimateTokens(empty); got <= tight {
		t.Fatalf("대조가 성립하지 않았다: 카드 0장인 고정분이 %d토큰이라 예산 %d 를 안 넘는다 — "+
			"이 입력으로는 바닥이 발동하는 갈래가 안 돈다", got, tight)
	}

	got := RenderBoard(synthBoard(34), BoardRenderOptions{Now: t0, Tail: tail, Budget: tight})

	// ★ **깨질 수 없는 계약을 리터럴로 단정한다.** 여기를 `cards < boardCardFloor` 로 쓰면
	// 시험이 자기가 지켜야 할 상수를 자기 기준으로 재게 되어, 바닥을 0 으로 만드는 변이가
	// `0 < 0`(거짓)으로 **초록을 낸다** — 실제로 그렇게 썼다가 변이 시험에서 잡혔다.
	// 계약은 "0장이면 안 된다"이고 3은 조율값이다. 둘을 따로 단정한다.
	cards := countCards(got)
	if cards < 1 {
		t.Fatalf("카드가 0장이다 — 카드 0장인 보드는 보드가 아니다:\n%s", got)
	}
	if cards < boardCardFloor {
		t.Fatalf("카드가 %d장이다 — 바닥 %d장을 못 지켰다:\n%s", cards, boardCardFloor, got)
	}
	// 예산을 넘겼다는 사실과 **넘긴 주체**를 말한다. 조용히 넘기면 계약이 거짓이 된다.
	if !strings.Contains(got, "고정분") {
		t.Fatalf("예산을 넘겼는데 무엇이 넘겼는지 안 말한다:\n%s", got)
	}
	// 접은 사실은 여전히 따로 말한다 — 원인이 둘이라 뭉치면 손댈 자리를 못 찾는다.
	if !strings.Contains(got, "접었다") {
		t.Fatalf("접었는데 접었다는 사실이 없다:\n%s", got)
	}

	// ── ③ 바닥은 예산이 넉넉할 때 **아무것도 안 바꾼다.** 상시 발동하면 판별력이 0이 된다.
	light := RenderBoard(synthBoard(34), BoardRenderOptions{Now: t0,
		Tail: RenderTail(TailInput{Now: t0, NotesObserved: true, OverlapsObserved: true})})
	if strings.Contains(light, "고정분") {
		t.Fatalf("가벼운 꼬리인데 예산 초과를 말한다 — 바닥이 상시 발동한다:\n%s", light)
	}
	if got := EstimateTokens(light); got > BoardTokenBudget {
		t.Fatalf("가벼운 꼬리인데 기본 출력이 %d토큰이다 — 상한 %d", got, BoardTokenBudget)
	}
}

// TestCardCapsItsOwnNoteLines 는 **카드 바닥의 비용**에 상한이 있는지 본다.
//
// boardCardFloor 는 예산을 이기고 카드를 남긴다. 그 카드 한 장의 크기에 상한이 없으면
// 바닥이 예산을 얼마나 넘길지도 상한이 없다 — 실측(2026-08-05): 남은 카드 3장에
// 사건 줄이 8개 붙어 예산을 531토큰 넘겼고 그 줄들이 초과분의 대부분이었다.
func TestCardCapsItsOwnNoteLines(t *testing.T) {
	const n = 6
	var asks []model.Judgment
	for i := 0; i < n; i++ {
		asks = append(asks, model.Judgment{
			Kind: model.JudgmentAsk, SessionID: "01SESSION0000", At: t0.Add(-time.Duration(i) * time.Minute),
			Title: fmt.Sprintf("요청 %d — 만질 자리 전부를 낸다", i)})
	}
	got := noteLines("01SESSION0000", asks, nil, t0)

	shown := 0
	for _, l := range got {
		if strings.Contains(l, "[ask ") {
			shown++
		}
	}
	if shown > cardNoteLimit {
		t.Fatalf("사건 줄이 %d개다 — 상한 %d\n%v", shown, cardNoteLimit, got)
	}
	if !strings.Contains(strings.Join(got, "\n"), fmt.Sprintf("%d건 더", n-cardNoteLimit)) {
		t.Fatalf("잘랐는데 몇 건을 잘랐는지 안 말한다:\n%v", got)
	}

	// 대조 — 상한 이하면 전부 나오고 "더" 줄이 안 붙는다.
	few := noteLines("01SESSION0000", asks[:1], nil, t0)
	if len(few) != 1 {
		t.Fatalf("사건 1건인데 줄이 %d개다:\n%v", len(few), few)
	}
	// 남의 사건은 애초에 안 센다 — 상한이 그 판정을 바꾸면 안 된다.
	if other := noteLines("01OTHER", asks, nil, t0); len(other) != 0 {
		t.Fatalf("남의 사건이 내 카드에 실렸다:\n%v", other)
	}
}

// TestRenderTailCapsOverlapLines 는 꼬리의 **바깥 차원**에 상한이 있는지 본다.
//
// 안쪽 차원(겹침 한 건 안의 경로쌍)은 원래 4개로 잘렸는데 겹침 **건수**는 안 잘렸다.
// 꼬리는 모든 응답에 붙고 board 에서는 고정분이라, 그 축이 세션 수에 O(N) 으로 자랐다.
func TestRenderTailCapsOverlapLines(t *testing.T) {
	const n = 16
	got := heavyTail(n, 0)

	lines := strings.Count(got, "  · 01OTHER")
	if lines > tailOverlapLimit {
		t.Fatalf("겹침 줄이 %d개다 — 상한 %d\n%s", lines, tailOverlapLimit, got)
	}
	// 자른 것을 조용히 하지 않는다. 그리고 **건수는 참값**이 나와야 한다 —
	// 상한을 건수에도 적용하면 화면이 "겹침 5건"이라 거짓말을 한다.
	if !strings.Contains(got, fmt.Sprintf("겹침 %d건", n)) {
		t.Fatalf("머리줄이 참 건수 %d 를 안 낸다:\n%s", n, got)
	}
	if !strings.Contains(got, fmt.Sprintf("%d건 더", n-tailOverlapLimit)) {
		t.Fatalf("잘랐는데 몇 건을 잘랐는지 안 말한다:\n%s", got)
	}

	// 대조 — 상한 이하면 전부 낸다.
	few := heavyTail(2, 0)
	if strings.Contains(few, "건 더") {
		t.Fatalf("겹침 2건인데 잘랐다:\n%s", few)
	}
	if c := strings.Count(few, "  · 01OTHER"); c != 2 {
		t.Fatalf("겹침 2건인데 줄이 %d개다:\n%s", c, few)
	}
}

// TestRenderBoardKeepsUnknownApartFromZero 는 0값과 "못 읽었다"를 화면에서 가른다.
func TestRenderBoardKeepsUnknownApartFromZero(t *testing.T) {
	v := synthBoard(1)
	v.Sessions[0].BranchKnown = false
	v.Sessions[0].AheadKnown = false
	v.Sessions[0].View.Paths = nil
	v.Sessions[0].View.HasFootprint = false

	got := RenderBoard(v, BoardRenderOptions{Now: t0})
	if !strings.Contains(got, "브랜치 ?(못 읽음)") {
		t.Fatalf("브랜치를 못 읽은 사실이 안 보인다:\n%s", got)
	}
	if !strings.Contains(got, "발자국 없음") || !strings.Contains(got, "아무도 안 막는다") {
		t.Fatalf("발자국 없는 세션이 아무도 안 막는다는 사실이 화면에 없다:\n%s", got)
	}

	// 대조: 읽은 경우에는 그 문구가 없어야 한다.
	ok := RenderBoard(synthBoard(1), BoardRenderOptions{Now: t0})
	if strings.Contains(ok, "못 읽음") {
		t.Fatalf("읽은 브랜치에 '못 읽음'이 붙었다:\n%s", ok)
	}
}

// TestBoardCardCarriesItsOwnAsk 는 사건이 그것을 남긴 세션의 카드에 붙는다는 것을 단정한다.
// 전역 꼬리만으로는 누가 남겼는지가 안 이어진다.
func TestBoardCardCarriesItsOwnAsk(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	v := service.BoardView{
		Sessions: []service.SessionCard{
			{View: model.SessionView{Session: model.Session{ID: "01AAA"}}},
			{View: model.SessionView{Session: model.Session{ID: "01BBB"}}},
		},
		Asks: []model.Judgment{
			{ID: "j1", SessionID: "01AAA", At: now.Add(-12 * time.Minute),
				Title: "mcpbackend.go 를 잡는다"},
		},
	}
	got := RenderBoard(v, BoardRenderOptions{Now: now, Detail: true})

	lines := strings.Split(got, "\n")
	var aaaIdx, askIdx, bbbIdx int = -1, -1, -1
	for i, l := range lines {
		switch {
		case strings.Contains(l, "01AAA"):
			aaaIdx = i
		case strings.Contains(l, "mcpbackend.go 를 잡는다"):
			if askIdx < 0 {
				askIdx = i
			}
		case strings.Contains(l, "01BBB"):
			bbbIdx = i
		}
	}
	if askIdx < 0 {
		t.Fatalf("사건이 어디에도 없다:\n%s", got)
	}
	if !(aaaIdx < askIdx && askIdx < bbbIdx) {
		t.Fatalf("사건이 01AAA 카드 안에 없다 (aaa=%d ask=%d bbb=%d):\n%s", aaaIdx, askIdx, bbbIdx, got)
	}
	if !strings.Contains(lines[askIdx], "12분") {
		t.Fatalf("사건의 나이가 없다: %q", lines[askIdx])
	}
}

// TestFoldKeepsEventCardsOverSilentOnes 는 예산이 자를 때 **사건이 붙은 카드가 조용한 카드보다
// 먼저 남는다**는 것을 단정한다. 이것이 없으면 사건을 카드에 붙여도 예산이 그걸 먼저 버린다.
func TestFoldKeepsEventCardsOverSilentOnes(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	var sessions []service.SessionCard
	for i := 0; i < 20; i++ {
		sessions = append(sessions, service.SessionCard{
			View: model.SessionView{
				Session: model.Session{ID: fmt.Sprintf("01S%02d", i)},
				Paths:   []string{"some/long/path/that/costs/tokens.go"},
			},
		})
	}
	v := service.BoardView{
		Sessions: sessions,
		Asks: []model.Judgment{
			{ID: "j1", SessionID: "01S19", At: now, Title: "마지막 세션이 남긴 요청"},
		},
	}
	got := RenderBoard(v, BoardRenderOptions{Now: now, Budget: 300})

	if !strings.Contains(got, "01S19") {
		t.Fatalf("사건이 붙은 카드가 접혔다:\n%s", got)
	}
	if !strings.Contains(got, "접었다") {
		t.Fatalf("예산 300 인데 아무것도 안 접혔다:\n%s", got)
	}
}

// TestFoldAlwaysKeepsSelfFirst: 나는 언제나 첫 카드다.
func TestFoldAlwaysKeepsSelfFirst(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	var sessions []service.SessionCard
	for i := 0; i < 20; i++ {
		sessions = append(sessions, service.SessionCard{
			View:   model.SessionView{Session: model.Session{ID: fmt.Sprintf("01S%02d", i)}},
			IsSelf: i == 19,
		})
	}
	got := RenderBoard(service.BoardView{Sessions: sessions},
		BoardRenderOptions{Now: now, Self: "01S19", Budget: 300})
	if !strings.Contains(got, "01S19") {
		t.Fatalf("내 카드가 접혔다:\n%s", got)
	}
}

func TestRenderFinishAndNoteAndAdd(t *testing.T) {
	fin := RenderFinish(service.FinishResult{
		Item:      model.Item{ID: "t5-iam", State: model.ItemDone},
		Judgment:  model.Judgment{ID: "01J", Kind: model.JudgmentHandoff, Body: "본문"},
		Followups: []model.Item{{ID: "t6-next"}},
		Released:  []string{"staging"},
	})
	for _, want := range []string{"t5-iam", "done", "t6-next", "staging", "한 트랜잭션"} {
		if !strings.Contains(fin, want) {
			t.Fatalf("finish 응답에 %q 가 없다:\n%s", want, fin)
		}
	}

	note := RenderNote(service.NoteResult{
		Judgment:   model.Judgment{ID: "01K", Kind: model.JudgmentAsk, Body: "가나다"},
		Recipients: []string{"01AAAAAAAAAA", "01BBBBBBBBBB"},
	})
	if !strings.Contains(note, "2건이 읽는다") {
		t.Fatalf("이 노트를 받을 세션 수가 없다:\n%s", note)
	}
	// 0건과 "안 봤다"를 가른다.
	if got := RenderNote(service.NoteResult{Judgment: model.Judgment{ID: "x", Kind: model.JudgmentAsk}}); !strings.Contains(got, "읽을 다른 세션이 없다") {
		t.Fatalf("받을 세션 0건이 명시되지 않는다:\n%s", got)
	}

	add := RenderAdd(model.Item{ID: "t7-x", Title: "제목", State: model.ItemOpen})
	if !strings.Contains(add, "브랜치 이름이 된다: t7-x") {
		t.Fatalf("add 응답이 브랜치 이름을 안 알린다:\n%s", add)
	}
	if !strings.Contains(add, "경로 0") {
		t.Fatalf("경로 0건이 겹침 축에 안 잡힌다는 사실이 없다:\n%s", add)
	}
}

// TestBoardSaysWhatTheWindowCutOff 는 창 밖으로 잘린 것을 **침묵시키지 않는다.**
// 창은 표시 구간이지 생존 판정이 아니다(설계 §4).
//
// ★ 창 값은 3시간으로 고른다 — 지금 기본값(2h, service.DefaultLiveWindow)도
// 옛 하드코딩 값(8h, 0113b35 이전 기본값)도 아닌 제3의 값이라, 문구가 하드코딩된
// 숫자를 그대로 찍으면 **반드시** 어긋난다. v.Window 를 실제로 안 읽으면 이 시험이 잡는다.
func TestBoardSaysWhatTheWindowCutOff(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	window := 3 * time.Hour
	got := RenderBoard(service.BoardView{
		Sessions:      []service.SessionCard{{View: model.SessionView{Session: model.Session{ID: "01AAA"}}}},
		Window:        window,
		OutOfWindow:   9,
		OldestOutside: now.Add(-7 * time.Hour),
	}, BoardRenderOptions{Now: now})

	if !strings.Contains(got, "창 밖 9건") {
		t.Fatalf("창 밖 건수를 안 말한다:\n%s", got)
	}
	// ★ 어떻게 보는지는 v.Window 에서 파생돼야 한다 — 하드코딩된 숫자가 아니라.
	if !strings.Contains(got, FormatAge(window)) {
		t.Fatalf("창 밖 문구가 실제 창(%s)을 안 말한다 — 하드코딩된 값을 찍고 있을 수 있다:\n%s",
			FormatAge(window), got)
	}
	if strings.Contains(got, "8h") || strings.Contains(got, "8시간") {
		t.Fatalf("창 밖 문구에 옛 하드코딩 값(8h, 0113b35 이전 기본값)이 남아 있다:\n%s", got)
	}
	// ★ MCP board 도구는 window 인자를 받지 않는다(tools.go) — 없는 손잡이를
	//   돌리라고 하면 그 문구 자체가 결함이다(설계가 도구 수를 6개로 눌러 잡는다).
	if strings.Contains(got, "window=") {
		t.Fatalf("존재하지 않는 window 인자를 돌리라고 한다:\n%s", got)
	}
	if strings.Contains(got, "죽") {
		t.Fatalf("생존 판정 낱말이 들어갔다 — 설계 §4 위반:\n%s", got)
	}
}

// pickWith 는 경로 실재 판정 하나를 실은 pick 결과를 만든다.
func pickWith(v *judge.ItemPathVerdict, paths []string) service.PickResult {
	item := model.Item{
		Project: "proj", ID: "t-path", Title: "제목", Body: "본문",
		Paths: paths, State: model.ItemOpen, CreatedAt: t0,
	}
	return service.PickResult{
		Mode: service.PickClaimed, Reason: "선점했다", Scope: "지정된 항목 1건",
		Item: &item, Branch: item.ID, PathCheck: v,
	}
}

func TestRenderPickNamesMisregistrationAndTheWayBack(t *testing.T) {
	got := RenderPick(pickWith(&judge.ItemPathVerdict{
		Kind: judge.KindMisregistered, Suggest: "kweiza-cc-plugins",
		Summary: "1개 전부 이 프로젝트(context-platform)에 없다 — kweiza-cc-plugins 에는 있다. 오등록일 수 있다.",
	}, []string{"plugins/x.go"}), t0)

	for _, want := range []string{
		"경로 실재:",
		"오등록일 수 있다",
		"fd move t-path --project kweiza-cc-plugins",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 없다:\n%s", want, got)
		}
	}
}

// ★ V1(유일 지목 조건이 없는 규칙)의 헛발화 5건이 여기서 죽는다.
// 여럿이 지목될 때 되돌리는 명령을 내면 그것이 곧 오등록 단정이다.
func TestRenderPickDoesNotPrescribeMoveWhenAmbiguous(t *testing.T) {
	got := RenderPick(pickWith(&judge.ItemPathVerdict{
		Kind: judge.KindAmbiguous, Candidates: []string{"a", "b", "c"},
		Summary: "1개 전부 이 프로젝트에 없다. 등록된 다른 3개 프로젝트(a, b, c)에도 같은 이름이 있어 어느 하나를 지목하지 못한다 — 근거로 쓰지 않는다.",
	}, []string{"docs/"}), t0)

	if !strings.Contains(got, "지목하지 못한다") {
		t.Fatalf("모호하다는 사실이 없다:\n%s", got)
	}
	if strings.Contains(got, "fd move") {
		t.Fatalf("여럿이 지목됐는데 되돌리는 명령을 냈다 — 그것이 곧 오등록 단정이다:\n%s", got)
	}
}

func TestRenderPickStatesThePathAxisEvenWhenClean(t *testing.T) {
	got := RenderPick(pickWith(&judge.ItemPathVerdict{
		Kind: judge.KindOK, Summary: "2개 중 2개가 이 프로젝트(proj)에 있다.",
	}, []string{"a.go", "b.go"}), t0)

	if !strings.Contains(got, "경로 실재:") {
		t.Fatalf("이상이 없어도 경로 축 줄은 있어야 한다 — 침묵하면 '이상 없다'와 '안 봤다'가 같은 화면이 된다:\n%s", got)
	}
}

// nil 은 침묵이 아니다. 낡은 캐시가 "이상 없다"처럼 보이면 안 된다.
func TestRenderPickSaysTheAxisWasNotReadWhenVerdictIsNil(t *testing.T) {
	got := RenderPick(pickWith(nil, []string{"a.go"}), t0)

	if !strings.Contains(got, "읽지 않았다") {
		t.Fatalf("판정이 nil 인데 그 사실을 말하지 않는다:\n%s", got)
	}
}

// 적격 0건에는 항목이 없으므로 이 줄도 없다.
func TestRenderPickOmitsPathAxisWhenThereIsNoItem(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickNone, Reason: "적격 항목이 0건이다", Scope: "후보 = 열린 항목 0건",
	}, t0)

	if strings.Contains(got, "경로 실재:") {
		t.Fatalf("항목이 없는데 경로 축 줄이 나왔다:\n%s", got)
	}
}
