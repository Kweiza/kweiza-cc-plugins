package service

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// 보드 — "지금 누가 무엇을 만지고 있나".
//
// ★ 이 파일은 "죽었다"를 만들지 않는다. 신호 넷의 시각을 그대로 담고 나이 계산은 표시 계층 몫이다.
// 불리언을 만드는 순간 그것이 회수·회피·탈락 셋의 상류가 되고, 그 판정은 실측에서 두 번 틀렸다 —
// 죽었다고 판정한 세션이 그 뒤 6커밋을 랜딩했고, 419분 무갱신으로 표시된 세션이 실제로는 17초 전이었다.

// BoardOptions 는 보드 조회의 선택 인자다.
type BoardOptions struct {
	// Window 는 "이 구간 안에 신호가 있었나"로 자르는 지점이다. 0 이면 DefaultLiveWindow.
	// **생존 판정이 아니다** — 결과에는 각 신호의 시각이 그대로 실린다.
	Window time.Duration
	// Self 는 요청한 세션 id 다. 표시 전용이고 **어떤 배제 판정에도 안 쓴다**.
	Self string
	// IncludeQueue 는 열린 항목을 함께 낸다.
	IncludeQueue bool
	// IncludeNotes 는 막힘·요청 판단을 함께 낸다.
	IncludeNotes bool
	// NoteLimit 는 IncludeNotes 일 때 종류별로 가져올 판단 수다. 0 이면 20.
	NoteLimit int
}

// SessionCard 는 세션 하나의 보드 표시분이다.
//
// BranchKnown·AheadKnown 이 있는 이유: 둘 다 0값이 유효한 값이라
// **"못 읽었다"와 "0이다"가 구분되지 않기 때문이다.** 그 구분이 없으면
// git 이 죽은 화면과 브랜치가 main 과 같은 화면이 똑같이 보인다.
type SessionCard struct {
	View        model.SessionView `json:"view"`
	BranchKnown bool              `json:"branch_known"`
	AheadKnown  bool              `json:"ahead_known"`
	DeriveError string            `json:"derive_error,omitempty"`
	IsSelf      bool              `json:"is_self"`
}

// splitCardsOf 는 카드에서 갈림 탐지 입력을 뽑는다.
//
// judge 가 SessionCard 를 직접 받지 않는 이유: 그러면 판정 계층이 표시 계층의
// 구조체에 묶이고, SessionCard 에 필드를 더할 때마다 judge 가 다시 컴파일된다.
func splitCardsOf(cards []SessionCard) []judge.SplitCard {
	out := make([]judge.SplitCard, 0, len(cards))
	for _, c := range cards {
		out = append(out, judge.SplitCard{
			SessionID:   c.View.Session.ID,
			MachineID:   c.View.Session.MachineID,
			Worktree:    c.View.Session.Worktree,
			CCSessionID: c.View.Session.CCSessionID,
		})
	}
	return out
}

// AckReach 는 처방 확인율이 지금 무엇을 재고 있는지다.
// Emitted 와 Reachable 이 크게 다르면 그 격차의 사유는 둘이다 — **판단을 아예 안 남긴
// 대화**, 그리고 **판단을 쓸 이유가 없던 대화**(land 만 하고 떠난 경우. 아래 ★).
// 카드 갈림은 더는 사유가 아니다 — 2026-08-08 부터 대화 단위로 세어 접힌다.
//
// ★ **세 값 다 대화 단위다 — 카드도 키도 안 본다.** 한 대화(machine + cc_session_id)가
// 카드 여럿으로 갈려도 한 번 센다. 카드로 세던 판이 **갈림**을 규율로 착각하게 만들었고,
// 그 갈림은 워크트리 이동이 만드는 것이라 이 레포의 규율을 지킬수록 심해졌다
// (store/prescribe_reach.go 의 ★ 참조. 카드 3중키는 설계이고 안 바뀐다).
// **키별 확인율은 여전히 여기서 안 나온다** — 설계 §10 이 인용하는 "overlap 0/31" 같은
// 수치는 사람이 따로 잰 값이고 이 구조체가 낸 것이 아니다. 그 구분을 놓치면 §10 의 수치를
// 이 필드로 재현하려다 다른 값을 얻는다.
// 그리고 `Reachable` 에는 조건이 하나 더 붙는다 — 그 대화의 **어느 카드든 `judgment` 행이
// 있어야 한다**(store/prescribe_reach.go 의 `judged`). 셋을 같은 모양으로 읽으면 안 된다.
//
// ★ **여기 있던 축의 뒤집힘은 2026-08-09 에 고쳐졌다.** 원래 이 자리는 "ack 을 남기는
// 경로는 판단을 쓰는 쪽(note·finish)뿐이고 `land` 는 그 통로를 안 지난다"였고, 그래서
// 행동이 `land()` 인 처방을 **정확히 따르고** 판단을 안 남긴 대화가 `Emitted` 에만 들어가
// **`Reachable` 에서 통째로 빠졌다**(관측에서 사라졌다). 지금은 land 가 자기가 응답한
// `lane-turn:<행>` 을 닫는다(service/prescribe.go 의 ackLaneTurn).
//
// ★ **그래도 `Reachable` 의 조건은 안 바뀌었다.** 저쪽은 여전히 그 대화의 어느 카드든
// `judgment` 행이 있어야 분모에 든다(store/prescribe_reach.go 의 `judged`) — land 만 하고
// 판단을 하나도 안 남긴 대화는 **ack 은 남기면서 분모에는 안 든다.** 위 격차의 둘째 사유는
// 이제 "통로가 없다"가 아니라 "분모의 조건이 판단이다"이고, 둘은 다른 것이다.
//
// ★ 그리고 **구간이 둘이다.** 전 역사만 내면 Emitted 가 단조 증가해, 갈림의 원인을
// 고쳐도 이미 갈린 옛 카드가 분모에 영영 남는다 — 두 수의 차이가 회복되지 않으니
// "지금 규율이 나아졌나"를 물을 수 없다(§10 이 요구한 재측정이 그 물음이다).
// 최근 벌만 내면 반대로 표본이 얇아 노이즈가 된다. 그래서 둘을 나란히 낸다.
type AckReach struct {
	// AllTime 은 프로젝트 전 역사다. 추세로만 읽어라 — 분모가 단조 증가한다.
	AllTime AckCounts `json:"all_time"`
	// Recent 는 Window 안의 벌이다. "지금 규율"을 묻는 쪽은 이것이다.
	Recent AckCounts `json:"recent"`
	// Window 는 Recent 가 자른 폭이다. **값으로 낸다** — 표면이 어느 구간의 수인지
	// 문장에 적어야 하고, 여기 없으면 렌더러가 24시간을 문자열로 박게 된다.
	Window time.Duration `json:"recent_window"`
}

// AckCounts 는 **한 구간**의 세 수다.
//
// ★ JSON 모양이 바뀐 자리다. 옛 `ack_reach` 는 세 수를 최상위에 평탄하게 냈고
// (`ack_reach.emitted`), 지금은 구간 아래로 들어간다(`ack_reach.all_time.emitted`).
// 평탄한 채로 최근 벌만 덧붙이는 길도 있었지만 그러면 **구간 라벨이 없는 수**가 그대로
// 남는다 — 그 이름 없음이 이 항목이 고치려던 결함 자체다.
//
// ★★ **이 모양 변경은 확인율을 통째로 침묵시키는 조건을 하나 만들었다 — 실물로 났다.**
// 앞선 판은 이 자리에 "이 저장소 안에 그 키를 읽는 비-Go 소비자는 없다(표면은
// dashboard.json 하나이고 렌더러를 같이 고쳤다)"고 적어 그 위험을 기각했다. **그 반증이
// 틀렸다.** 위험은 비-Go 소비자가 아니라 **Go 클라이언트의 버전 스큐**다 — 옛 CLI 가 새
// 서버의 JSON 을 읽으면 최상위 `emitted` 가 없어 값 타입이 영값으로 채워지고, 렌더러
// 게이트(mcpsrv/render.go 의 `r.AllTime.Emitted > 0`)가 확인율 두 줄을 통째로 접는다.
// 값 타입에서는 **부재와 영값이 같기 때문**이다.
//
//	즉 **`fd status` 에 확인율 두 줄이 안 보이는 것은 "발화 0" 이 아닐 수 있다.**
//	클라이언트가 서버보다 옛 판이라는 뜻일 수 있고, 그 둘은 화면에서 구별되지 않는다.
//	해소는 Claude Code 재시작이다 — 런처가 CLI 를 새 판으로 다시 만든다.
//	실측(2026-08-08): 13:05 컨테이너 0.12.0 기동 직후 침묵 시작 → 13:13 재시작 →
//	두 줄이 곧바로 복구. **스큐 창 8분.**
//
// 그 창을 더 좁히는 길 둘 — 필드를 `*AckCounts` 로 바꿔 부재를 nil 로 받기, 렌더러가 세 수
// 전부 0이면 "발화 0"이 아니라 "못 읽었다"를 말하기 — 은 **일부러 안 갔다.** 창이 짧고
// 해소가 확정적이며, 버전 스큐 자체는 이미 표면에 있다(cmd/fd/offline.go 의 `SkewBanner`).
// 같은 모양의 필드가 늘 때마다 nil 갈래를 하나씩 더하는 대신 그 배너 한 자리에서 다루는
// 쪽을 골랐다. **되돌리는 조건**: 이 침묵이 배너로 안 잡히는 실물 사례가 나오면 그때
// 포인터 갈래가 값을 얻는다.
type AckCounts struct {
	Emitted   int `json:"emitted"`
	Reachable int `json:"reachable"`
	Acked     int `json:"acked"`
}

// BoardView 는 보드 한 장이다.
type BoardView struct {
	Project  model.Project `json:"project"`
	At       time.Time     `json:"at"`
	Window   time.Duration `json:"window"`
	Sessions []SessionCard `json:"sessions"`
	// Splits 는 워크트리 정규화가 안 돈 흔적이다. **비어 있는 것이 정상**이고,
	// 하나라도 있으면 그 카드를 연 클라이언트가 4de4b21 이전 판이라는 뜻이다.
	//
	// ★ git 을 못 읽어도 이 축이 **완전히 죽지는 않는다** — judge 가 카드 경로에서
	//   관례 루트(.flightdeck/worktrees/<이름>)를 되읽기 때문이다. 다만 근거가 그것뿐이라
	//   판정 범위가 좁아지고, 그 사실은 Failures 의 `split-detect` 축에 남는다.
	//   침묵과 "갈림 없음"을 구분해야 한다.
	Splits []judge.SplitReport `json:"splits,omitempty"`
	// AckReach 는 detail 꼬리 전용이다. nil 이면 이 조회가 안 돌았다는 뜻이다.
	AckReach  *AckReach            `json:"ack_reach,omitempty"`
	OpenItems []model.Item         `json:"open_items,omitempty"`
	Blocked   []model.Judgment     `json:"blocked,omitempty"`
	Asks      []model.Judgment     `json:"asks,omitempty"`
	Held      []model.ResourceHold `json:"held,omitempty"`
	// Lane 은 랜딩 줄이다. **nil 과 빈 값을 구분한다** — nil 은 이 조회가 레인을 안 읽었다는
	// 뜻이고, Entries 가 빈 슬라이스인 것은 질의는 돌았는데 아무도 안 섰다는 뜻이다(LaneView 주석).
	Lane *LaneView `json:"lane,omitempty"`
	// OutOfWindow 는 창 밖이라 카드가 안 나간 세션 수다. **화면이 반드시 말한다** —
	// 침묵하면 "그런 세션이 없다"와 "안 보여 준다"가 구분되지 않는다.
	OutOfWindow int `json:"out_of_window,omitempty"`
	// OldestOutside 는 창 밖 세션 중 가장 오래된 마지막 신호 시각이다.
	OldestOutside time.Time `json:"oldest_outside,omitempty"`
	// OutsideClaims 는 **창 밖인데 선점을 든 세션**이다. 화면 ①이 선점을 필터로 쓰면서
	// 창은 안 걸기 때문에 필요하다 — 창을 함께 걸면 회수가 가장 필요한 카드(오래 조용한데
	// 항목을 쥔 세션)가 먼저 사라진다. 실측: 마지막 활동 709분 전인 세션이 항목 하나를
	// 12시간째 쥐고 있었다.
	//
	// ★ **아무것도 안 거른다.** Sessions·OutOfWindow 는 그대로다. 이미 도는 순회
	// (OldestOutside)에 조건 하나를 얹은 것뿐이라 새 질의도 새 git 호출도 없다.
	//
	// ★ 카드가 아니라 **원시 뷰**다. git 파생(브랜치·ahead·미커밋)이 안 붙어 있다 —
	// 파생은 카드당 git 호출 2~5회고(미커밋 규모가 조건 없이 붙어 하한도 올랐다) 캐시가
	// 없어서, 창 밖까지 파생하면 세션 수만큼 터진다.
	// 표시 계층이 이 사실을 말해야 한다: 이 줄은 "무엇이 잠겼나"만 답하고 파생 축은 모른다.
	OutsideClaims []model.SessionView `json:"outside_claims,omitempty"`
	Derived
}

// Board 는 살아 있는 세션과 그들이 만지는 경로를 낸다.
//
// git 파생이 통째로 실패해도 **응답은 낸다**. 조정(누가 살아 있나·누가 무엇을 선점했나)은
// DB 만으로 완결되고, 그것이 이 도구의 존재 이유이기 때문이다.
// 다만 침묵하지 않는다 — Freshness.Stale 과 Failures 가 "이 값은 못 읽었다"를 표면에 낸다.
func (s *Service) Board(ctx context.Context, project string, opt BoardOptions) (BoardView, error) {
	now := s.now()
	d := &derive{}

	proj, err := s.st.GetProject(ctx, project)
	if err != nil {
		// 프로젝트 미등록은 파생 실패가 아니라 설정 오류다. 접지 않고 그대로 올린다.
		return BoardView{}, err
	}

	window := opt.Window
	if window <= 0 {
		window = s.window
	}
	cards, roots, err := s.sessionCardsAndRoots(ctx, proj, s.cut(now, window), opt.Self, d)
	if err != nil {
		return BoardView{}, err
	}

	view := BoardView{Project: proj, At: now, Window: window, Sessions: cards}
	// ★ 침묵하지 않는다. 루트를 못 읽었거나 어느 트리에도 못 붙인 카드가 있으면
	//   그 사실을 파생 기록에 남긴다 — 안 남기면 "갈림 없음"과 "판정을 못 했다"가
	//   화면에서 같아진다.
	if len(roots) == 0 {
		d.note("split-detect", "워크트리 루트를 못 읽었다 — 갈림 탐지의 근거가 관례 복원뿐이다")
	}
	var unattributed int
	view.Splits, unattributed = judge.DetectUnnormalizedSplit(splitCardsOf(cards), roots)
	if unattributed > 0 {
		d.note("split-detect", fmt.Sprintf(
			"카드 %d장은 어느 워크트리에도 못 붙여 갈림 판정에서 빠졌다", unattributed))
	}

	// ★ 실패해도 보드를 죽이지 않는다 — 파생이 통째로 실패해도 응답을 내는 것이
	//   이 도구의 존재 이유다. 다만 침묵하지 않고 파생 실패로 남긴다.
	//
	// ★ 절단은 주입 시계를 탄다(now = s.now()). time.Now() 를 직접 부르면 시험이 창을
	//   못 움직여 이 축이 통째로 안 잠긴다.
	if all, recent, aerr := s.st.AckReach(ctx, project, now.Add(-AckWindow)); aerr != nil {
		d.fail("ack-reach", aerr)
	} else {
		view.AckReach = &AckReach{
			AllTime: AckCounts(all), Recent: AckCounts(recent), Window: AckWindow,
		}
	}

	// 창 밖 건수 — 카드를 안 만든다. 세는 것만 한다(파생 비용을 안 늘린다).
	listAll := s.outOfWindowLister
	if listAll == nil {
		listAll = s.st.ListLive
	}
	if all, aerr := listAll(ctx, proj.ID, time.Time{}); aerr != nil {
		d.fail("out-of-window", aerr) // 못 세면 침묵하지 않고 파생 실패로 남긴다
	} else {
		view.OutOfWindow = len(all) - len(cards)

		// ★ OldestOutside 는 **숨은 세션**만의 값이어야 한다. cards 에 이미 나온 세션의
		// 신호 하나하나를 다시 훑으면, 그 세션이 최근 신호로 창 안에 있어도 다른 종류의
		// 옛 신호(예: 6시간 전 commit) 때문에 "창 밖"으로 잘못 집힌다 — 보이는 세션의
		// 신호로 숨은 세션 지표를 오염시키는 것이다. 숨은지 여부는 cards(= 실제로 사용한
		// ListLive(cut) 결과)와 **같은 판정**을 다시 쓴다 — 여기서 opened_at·신호 조건을
		// 새로 판단하면 그 판정이 SQL 쪽과 갈라질 수 있다(같은 판정을 두 자리에 두면
		// 한쪽만 고치는 순간 조용히 어긋난다는 것을 이 파일이 이미 한 번 겪었다).
		shown := make(map[string]bool, len(cards))
		for _, c := range cards {
			shown[c.View.Session.ID] = true
		}
		for _, v := range all {
			if shown[v.Session.ID] {
				continue
			}
			// ★ 창 밖인데 항목을 쥔 세션. 화면 ①이 선점을 필터로 쓰면서 창은 안 걸기
			// 때문에 이 줄이 필요하다 — 이 조건이 없으면 **회수가 가장 필요한 카드**가
			// 정확히 창 때문에 화면에서 사라진다. 여기서 모으는 이유는 이 순회가 이미
			// 돌고 있어서다: 새 질의도 새 git 파생도 안 는다(all 은 DB 전용이고
			// store.ListLive 가 Claims 를 이미 채워 준다).
			if len(v.Claims) > 0 {
				view.OutsideClaims = append(view.OutsideClaims, v)
			}
			// 숨은 세션의 "마지막으로 언제 봤나" — 그 세션 신호들의 최댓값이다.
			var lastSeen time.Time
			for _, at := range v.Signals {
				if at.After(lastSeen) {
					lastSeen = at
				}
			}
			if lastSeen.IsZero() {
				continue // 신호가 하나도 없다 — 언제 봤는지 답할 재료가 없다
			}
			if view.OldestOutside.IsZero() || lastSeen.Before(view.OldestOutside) {
				view.OldestOutside = lastSeen
			}
		}
	}

	if opt.IncludeQueue {
		items, err := s.st.ListOpen(ctx, project)
		if err != nil {
			return BoardView{}, err
		}
		view.OpenItems = items
	}
	if opt.IncludeNotes {
		limit := opt.NoteLimit
		if limit <= 0 {
			limit = 20
		}
		// ★ 여기서도 **같은 함수**를 부른다. 앞선 판은 이 자리가 직접 질의해서
		//   RecentNotes 만 고쳤을 때 훅으로 나가는 경로가 그대로 넘쳤다 —
		//   판정을 두 자리에 두면 한쪽만 고치는 순간 조용히 어긋난다는 것을
		//   이 파일이 스스로 실증한 셈이다.
		if view.Blocked, err = s.liveNotesOfKind(ctx, project, model.JudgmentBlocked, limit); err != nil {
			return BoardView{}, err
		}
		if view.Asks, err = s.liveNotesOfKind(ctx, project, model.JudgmentAsk, limit); err != nil {
			return BoardView{}, err
		}
	}
	if view.Held, err = s.st.ListHeld(ctx, project); err != nil {
		return BoardView{}, err
	}
	// 레인 — 항상 채운다. LandingLane 은 지금까지 비시험 호출자가 0건이었고(TestLandingQueueHasAProductionReader
	// 가 그 축을 잠근다), 이 한 줄이 그 표를 "저장만 하고 아무도 안 읽는 표"에서 꺼낸다.
	if lane, err := s.LandingLane(ctx, project); err != nil {
		return BoardView{}, err
	} else {
		view.Lane = &lane
	}

	view.Derived = d.result(now)
	s.log.InfoContext(ctx, "보드 조회",
		"project", project, "count", len(cards), "stale", view.Freshness.Stale,
		"skipped", len(view.Failures))
	return view, nil
}

// RecentNotes 는 프로젝트의 최근 ask·blocked 판단이다(종류별 limit 건).
//
// Board 를 통째로 부르지 않고 이 축만 여는 이유: 이것을 부르는 자리(MCP 응답 꼬리)는
// 세션 카드도 큐도 안 쓰는데, Board 는 그것을 내려고 git 을 여러 번 읽는다.
// 꼬리 하나 때문에 매 도구 호출이 저장소 전체를 훑게 두면 첫 명령이 그만큼 느려진다.
//
// **누가 남겼는지로 거르지 않는다.** "내가 쓴 것은 알림이 아니다"는 표시 계층의 판정이고,
// 그 축을 여기서 접으면 같은 목록을 다른 '나'로 다시 볼 수 없다.
// liveNotesOfKind 는 **지금 살아 있는 세션이 남긴** 그 종류의 판단이다.
//
// ★ 알림이 답하는 질문은 "지금 누가 나에게 무엇을 요청했나"이지 "무슨 일이 있었나"가 아니다.
// 생존 범위가 없으면 이관 직후 옛 판단 수십 건이 전부 "미확인"으로 잡혀 매 프롬프트에 실린다 —
// 실제로 그렇게 났다(ask 36 + blocked 36, 제목이 옛 절 이름이라 **전부 같은 문구**였다).
// 그러면 이 채널은 첫날부터 노이즈가 되고, 노이즈가 된 채널은 아무도 안 읽는다.
// 지난 일은 사라지지 않는다 — 판단 검색(설계 §6 ⑥)이 그 자리다.
func (s *Service) liveNotesOfKind(ctx context.Context, project string,
	kind model.JudgmentKind, limit int) ([]model.Judgment, error) {
	if limit <= 0 {
		limit = 20
	}
	live, err := s.st.ListLive(ctx, project, s.now().Add(-s.window))
	if err != nil {
		return nil, err
	}
	alive := make(map[string]bool, len(live))
	for _, sess := range live {
		alive[sess.Session.ID] = true
	}
	// 살아 있는 것만 남기므로 넉넉히 읽고 거른다.
	js, err := s.st.ListJudgmentsByKind(ctx, project, kind, limit*4)
	if err != nil {
		return nil, err
	}
	out := make([]model.Judgment, 0, limit)
	for _, j := range js {
		if !alive[j.SessionID] {
			continue
		}
		if out = append(out, j); len(out) >= limit {
			break
		}
	}
	return out, nil
}

// RecentNotes 는 프로젝트의 최근 ask·blocked 판단이다(종류별 limit 건).
func (s *Service) RecentNotes(ctx context.Context, project string, limit int) ([]model.Judgment, error) {
	if strings.TrimSpace(project) == "" {
		return nil, nil
	}
	var out []model.Judgment
	for _, k := range []model.JudgmentKind{model.JudgmentAsk, model.JudgmentBlocked} {
		js, err := s.liveNotesOfKind(ctx, project, k, limit)
		if err != nil {
			return nil, err
		}
		out = append(out, js...)
	}
	return out, nil
}

// sessionCardsAndRoots 는 sessionCards 에 **git 이 아는 워크트리 루트 목록**을 더해 낸다.
//
// 갈림 탐지가 그것 없이는 못 돌고(judge.DetectUnnormalizedSplit), 여기서 이미
// `git worktree list` 를 한 번 돌렸으므로 호출부가 다시 부르면 이 서버에서 가장 비싼
// 일이 두 배가 된다. git 을 못 읽었으면 nil 이다 — 빈 것과 못 읽은 것을 호출부가
// 가를 수 있어야 한다.
//
// 붙이는 것은 셋이다 — 브랜치·HEAD(워크트리 목록) · ahead(기본 브랜치 대비) ·
// 경로(footprint ∪ change_set ∪ 미커밋). 셋 다 실패해도 세션 행은 남는다.
func (s *Service) sessionCardsAndRoots(ctx context.Context, proj model.Project, cut time.Time, self string, d *derive) ([]SessionCard, []string, error) {
	// ★ 이 함수가 이 서버에서 가장 비싼 일이다 — `git worktree list` 한 번 + 살아 있는
	//   세션마다 ChangedPaths·UncommittedPaths·UncommittedDelta. 그 비용을 세는 자리를 여기 둔다.
	//   호출부에 두면 호출부가 늘 때마다 계측이 조용히 빠진다(실제로 그 모양으로
	//   MCP 꼬리가 도구 호출마다 이 파생을 한 번씩 더 돌리고 있었고, 아무 화면에도 안 떴다).
	start := time.Now()
	defer func() {
		s.derives.Add(1)
		s.deriveMicros.Add(uint64(time.Since(start).Microseconds()))
	}()

	live, err := s.st.ListLive(ctx, proj.ID, cut)
	if err != nil {
		return nil, nil, err
	}
	s.deriveCards.Add(uint64(len(live)))

	var g GitReader
	var wts map[string]string // 워크트리 경로 → 브랜치
	var heads map[string]string
	var roots []string
	if strings.TrimSpace(proj.Path) == "" {
		d.note("project-path", "프로젝트 경로가 비어 있다 — git 파생을 아예 시도하지 않았다")
	} else {
		g = s.git(proj.Path)
		wts, heads = s.worktreeIndex(ctx, g, d)
		roots = make([]string, 0, len(wts))
		for wt := range wts {
			roots = append(roots, wt)
		}
		sort.Strings(roots) // 파생 결과가 맵 순회 순서에 안 새게
		// 기본 브랜치의 tip 은 신선도·ref_state 용으로만 읽는다. 변경집합의 base 로는 안 쓴다 —
		// 그 자리는 갈래 지점이다(아래 MergeBase 참조).
		if r, err := g.Ref(ctx, proj.DefaultBranch); err != nil {
			d.fail("ref:"+proj.DefaultBranch, err)
		} else {
			d.ok()
			s.rememberRef(ctx, proj.ID, r)
		}
	}

	cards := make([]SessionCard, 0, len(live))
	for _, v := range live {
		card := SessionCard{View: v, IsSelf: v.Session.ID == self}
		var fails []string

		if g != nil {
			wt := filepath.Clean(v.Session.Worktree)
			if br, ok := wts[wt]; ok {
				card.View.Branch, card.BranchKnown = br, true
				card.View.BranchSHA = heads[wt]
			}
			// 변경집합 — 착수 직후 구간은 브랜치 diff 가 정의상 비어 있어 footprint 가 덮는다.
			if card.BranchKnown && card.View.Branch != "" && card.View.Branch != proj.DefaultBranch {
				// ★ base 는 기본 브랜치의 tip 이 아니라 **갈래 지점**이다.
				//
				//   tip 을 넘기면 두 점 diff 가 되어 두 끝점을 비교한다 — main 만 바꾼 파일이
				//   브랜치의 변경으로 들어온다. 그러면 main 에 커밋이 하나 랜딩할 때마다 그 커밋이
				//   건드린 파일이 **살아 있는 모든 브랜치**의 발자국에 더해져, 브랜치가 오래 살수록
				//   오탐이 단조로 는다(실측: 겹침 6건 중 3건이 이 원인). §5 가 겹침을 거르지 않고
				//   알리므로 그 오탐은 곧바로 화면에 나가고, 거짓 겹침이 늘면 진짜 겹침도 같이 죽는다.
				//
				//   갈래 지점을 못 구하면 이 축을 **비운 채** 못 읽었다고 말한다. 두 점으로
				//   되돌아가지 않는다 — 그것이 없애려는 바로 그 오탐이다. 발자국·미커밋이 덮는다.
				if forkSHA, err := g.MergeBase(ctx, proj.DefaultBranch, card.View.Branch); err != nil {
					d.fail("merge-base:"+clip(card.View.Branch, 120), err)
					fails = append(fails, "갈래 지점을 못 읽었다")
				} else if paths, delta, err := g.ChangedPaths(ctx, forkSHA, card.View.Branch); err != nil {
					d.fail("changed-paths:"+clip(card.View.Branch, 120), err)
					fails = append(fails, "변경 경로를 못 읽었다")
				} else {
					d.ok()
					card.View.Paths = UnionPaths(card.View.Paths, paths)
					card.View.PathDelta = MergeDelta(card.View.PathDelta, delta)
					// 보관되는 뜻이 정확해진다 — 갈래 기준 diff 는 forkSHA 로부터의 두 점 diff 와 같다.
					s.rememberChangeSet(ctx, proj.ID, forkSHA, card.View.BranchSHA, paths)
				}
				if ahead, _, err := g.AheadBehind(ctx, card.View.Branch, proj.DefaultBranch); err != nil {
					d.fail("ahead-behind:"+clip(card.View.Branch, 120), err)
					fails = append(fails, "ahead 를 못 읽었다")
				} else {
					d.ok()
					card.View.AheadMain, card.AheadKnown = ahead, true
				}
			}
			// 미커밋 — 커밋 전 의도를 나르는 유일한 축이라 조용히 짧아지면 안 된다.
			if unc, err := g.UncommittedPaths(ctx, v.Session.Worktree); err != nil {
				d.fail("uncommitted:"+clip(v.Session.ID, 64), err)
				fails = append(fails, "미커밋 경로를 못 읽었다")
			} else {
				d.ok()
				card.View.Paths = UnionPaths(card.View.Paths, unc)
			}
			// 미커밋 규모 — **위 경로 축과 갈라 둔다.** 이것이 실패해도 위가 살아야 하고
			// (커밋 전 의도를 나르는 유일한 축이다), 그것이 두 git 호출을 안 합친 이유다.
			// 이 호출 하나가 이 축에서 새로 드는 비용의 전부다(세션당 4→5).
			//
			// ★ **두 축 이름의 접미가 같은 세션 id 인 것에 화면이 기댄다.** 워크트리가 지워지면
			// 둘이 **함께** 실패하고(같은 `Session.Worktree` 를 본다) 원인도 하나인데, 화면이
			// 같은 말을 두 번 하면 꼬리 예산이 배로 든다. `mcpsrv.foldTwinFailures` 가
			// `uncommitted:<세션>` 과 `uncommitted-delta:<세션>` 을 그 이름 꼴로 짝지어 한 줄로
			// 접는다 — **접기는 화면에서만 하고 여기서 내는 축은 그대로다**(둘을 그대로 받는
			// 소비자는 웹 패널과 MCP 렌더러다. 원장과 `/metrics` 는 이 축을 애초에 안 본다 —
			// 2026-08-12 실측: event.kind 전수(33종)에 파생 축이 없고 /metrics 는 runs·cards·seconds
			// 뿐이다. 원장 밖에 남는 것은 아래 "보드 조회" 로그 줄의 skipped 수가 전부다).
			// 이름 꼴을 바꾸면 접기가 조용히 안 걸린다(줄이 둘로 돌아갈 뿐이라
			// 시험 없이는 안 보인다) — `TestDeadWorktreeFoldsIntoOneLineButKeepsTheAxisCount` 가 문다.
			if ud, err := g.UncommittedDelta(ctx, v.Session.Worktree); err != nil {
				d.fail("uncommitted-delta:"+clip(v.Session.ID, 64), err)
				fails = append(fails, "미커밋 규모를 못 읽었다")
			} else {
				d.ok()
				card.View.PathDelta = MergeDelta(card.View.PathDelta, ud)
			}
		} else {
			fails = append(fails, "git 파생을 시도하지 않았다(프로젝트 경로 없음)")
		}

		// ★ 발자국이 없다는 사실을 침묵하지 않는다. false 면 그 세션은 경로 축에서
		//   아무도 안 막고, **안 막는다는 사실이 화면에 있어야** 한다(설계 §5).
		card.View.HasFootprint = len(card.View.Paths) > 0

		if note, err := s.lastNote(ctx, v.Session.ID); err != nil {
			return nil, nil, err
		} else if note != nil {
			card.View.LastNote = note
		}

		card.DeriveError = strings.Join(fails, " · ")
		cards = append(cards, card)
	}
	return cards, roots, nil
}

// sessionCards 는 루트가 필요 없는 호출부를 위한 껍데기다(finish.go · pick.go).
//
// ★ 그 둘의 시그니처를 안 바꾸려고 이 껍데기를 둔다 — 이 브랜치는 그 파일들을 안 연다
//
//	(다른 세션이 미랜딩으로 잡고 있다). 로직은 sessionCardsAndRoots 하나뿐이다.
func (s *Service) sessionCards(ctx context.Context, proj model.Project, cut time.Time, self string, d *derive) ([]SessionCard, error) {
	cards, _, err := s.sessionCardsAndRoots(ctx, proj, cut, self, d)
	return cards, err
}

// worktreeIndex 는 워크트리 경로 → 브랜치·HEAD 두 색인을 만든다.
func (s *Service) worktreeIndex(ctx context.Context, g GitReader, d *derive) (branches, heads map[string]string) {
	branches, heads = map[string]string{}, map[string]string{}
	wts, err := g.Worktrees(ctx)
	if err != nil {
		d.fail("worktrees", err)
		return branches, heads
	}
	d.ok()
	for _, w := range wts {
		p := filepath.Clean(w.Path)
		branches[p] = w.ShortBranch()
		heads[p] = w.HEAD
	}
	return branches, heads
}

// lastNote 는 세션이 마지막으로 남긴 판단이다. 없으면 nil.
func (s *Service) lastNote(ctx context.Context, sessionID string) (*model.Judgment, error) {
	js, err := s.st.ListJudgmentsBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(js) == 0 {
		return nil, nil
	}
	last := js[len(js)-1] // 시간순이라 마지막이 최신이다
	return &last, nil
}

// rememberRef 는 관측한 ref 를 보관한다. **실패해도 조회를 죽이지 않는다** —
// 이 값은 서버가 죽었을 때의 마지막 스냅숏(설계 §7 의 L1)이지 조회 결과가 아니다.
func (s *Service) rememberRef(ctx context.Context, project string, r model.RefState) {
	r.Project = project
	if err := s.st.UpsertRefState(ctx, r); err != nil {
		s.log.WarnContext(ctx, "ref 관측 보관 실패",
			"project", project, "ref", clip(r.Ref, 120), "error", err.Error())
	}
}

// rememberChangeSet 은 변경집합을 불변으로 보관한다(브랜치가 지워져도 남는다).
// sha 를 모르면 보관하지 않는다 — 키가 빈 행은 나중에 무엇의 변경인지 말하지 못한다.
func (s *Service) rememberChangeSet(ctx context.Context, project, baseSHA, headSHA string, paths []string) {
	if baseSHA == "" || headSHA == "" {
		return
	}
	err := s.st.UpsertChangeSet(ctx, model.ChangeSet{
		Project: project, BaseSHA: baseSHA, HeadSHA: headSHA, Paths: paths,
	})
	if err != nil {
		s.log.WarnContext(ctx, "변경집합 보관 실패",
			"project", project, "error", err.Error())
	}
}

// liveFor 는 겹침 판정에 쓸 살아 있는 세션 목록이다.
// judge 의 좌표계(LiveSession)로 옮긴다 — 판정 함수가 보드 타입을 알 필요가 없다.
func liveFor(cards []SessionCard) []judge.LiveSession {
	out := make([]judge.LiveSession, 0, len(cards))
	for _, c := range cards {
		out = append(out, judge.LiveSession{
			ID: c.View.Session.ID, Label: c.View.Session.Label, Paths: c.View.Paths,
			// 규모도 함께 넘긴다. 이 변환이 두 자리(여기(pick) · mcpsrv.liveOf(board))에
			// 있는데, 한쪽만 고치면 board 와 pick 중 한쪽에서만 규모가 뜬다.
			// 안 넘기면 꼬리 겹침이 규모를 원리적으로 못 낸다.
			Delta: c.View.PathDelta,
			// ★ 대화 id 를 함께 넘긴다. 카드 id 만으로는 형제 카드(같은 대화, 다른 카드)를
			// 남으로 보고 **자기 자신과 겹친다**고 알린다. prescribe 쪽이 먼저 같은 사고를
			// 겪고 같은 한 줄로 고쳤다(service/prescribe.go 의 Others 조립부).
			CCSessionID: c.View.Session.CCSessionID,
		})
	}
	return out
}

// selfCCOf 는 카드 목록에서 **내 카드의 대화 id** 를 찾는다.
//
// 못 찾으면 빈 문자열이고, 그러면 형제 판정이 안 돈다 — 겹침이 더 나오는 쪽이다.
// 반대로 접었다가는 관측이 깨진 순간 진짜 겹침이 조용히 사라진다.
func selfCCOf(cards []SessionCard, self string) string {
	for _, c := range cards {
		if c.View.Session.ID == self {
			return c.View.Session.CCSessionID
		}
	}
	return ""
}
