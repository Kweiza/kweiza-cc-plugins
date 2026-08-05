package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 랜딩 레인 — 순서(landing_queue)와 배타(resource_hold)를 잇는 전이 한 곳.
//
// 두 표가 무엇을 나눠 갖는지가 이 파일의 전제다:
//
//	landing_queue  : 누가 언제 줄에 섰나. id 가 곧 순번이다. 점유 여부는 안 담는다
//	resource_hold  : 지금 누가 레인을 쥐었나. 부분 유니크 인덱스가 배타의 정본이다
//
// ★ **살아 있는 랜딩 점유에는 반드시 대응하는 살아 있는 줄 행이 있다.** 이 불변식이
// 깨지면 ListLandingQueue 는 아무도 안 보여 주는데 레인은 영영 잡혀 있고, 그 프로젝트의
// 랜딩이 전원 정지한다(복구 수단이 sqlite3 직접 UPDATE 뿐이 된다).
// 그래서 줄 행을 닫는 모든 경로가 같은 트랜잭션에서 점유도 함께 본다 —
// LandReport 는 반납하고, LandLeave 는 자기 점유면 반납하고, ReleaseLaneRow 는 강제 반납한다.
// TestLiveLandingHoldAlwaysHasALiveQueueRow 가 이 축을 동작으로 잠근다.
//
// ★ **ok 는 "랜딩됐다"가 아니다.** 세션이 ok 로 보고하고 레인을 놓았다는 뜻뿐이다.
// item.LandedRef 도 랜딩 이력도 이 값으로 채우지 않는다(model.LandingLeftOK 주석 참조).

// LaneResource 는 랜딩 레인의 자원 이름이다. **이 상수는 이 패키지 하나에 있다** —
// 사본을 다른 패키지에 두면 한쪽만 고쳐진 날 배타가 조용히 두 벌이 되고, 그때 두 세션이
// 서로 다른 이름의 레인을 각자 쥔 채 같은 브랜치에 랜딩한다.
const LaneResource = "landing"

// LandInput 은 줄에 서거나 내 자리를 다시 묻는 인자다.
type LandInput struct{ Project, SessionID string }

// LandReportInput 은 레인을 쓰고 난 뒤의 보고다.
type LandReportInput struct {
	Project, SessionID string
	Kind               model.LandingLeftKind // ok | fail
	Detail             string
}

// LandLeaveInput 은 줄에서 스스로 빠지는 인자다.
type LandLeaveInput struct{ Project, SessionID, Detail string }

// LandResult 는 land 세 갈래가 공유하는 응답이다.
//
// ★ respond.go 가 이 타입을 그대로 직렬화하므로 json 태그가 REST 계약이자
// CLI 파싱 대상이다. 태그가 어긋나면 CLI 가 오류 없이 0값을 찍는다.
type LandResult struct {
	State    string      `json:"state"` // turn | waiting | released | left | reclaimed
	RowID    int64       `json:"row_id"`
	Position int         `json:"position"`         // 1이면 맨 앞. waiting 일 때만 의미 있다
	Reason   string      `json:"reason,omitempty"` // reclaimed 일 때 회수 사유
	Holder   *LaneHolder `json:"holder,omitempty"` // waiting 일 때 앞사람
}

// LaneHolder 는 지금 레인을 쥔 세션이다.
type LaneHolder struct {
	SessionID    string     `json:"session_id"`
	AcquiredAt   time.Time  `json:"acquired_at"`
	LastSignalAt *time.Time `json:"last_signal_at,omitempty"` // nil = 신호가 하나도 없다
}

// LaneView 는 보드·화면이 읽는 레인 전체다.
//
// ★ BoardView.Lane 은 *LaneView 다 — nil = 안 읽었다, Entries 빈 슬라이스 = 0건.
// 둘을 한 값으로 접으면 "질의가 안 돌았다"와 "아무도 안 섰다"가 화면에서 같아진다.
type LaneView struct {
	Holder  *LaneHolder `json:"holder,omitempty"`
	Entries []LaneEntry `json:"entries"`
}

// LaneEntry 는 줄의 한 자리다.
type LaneEntry struct {
	RowID        int64      `json:"row_id"`
	SessionID    string     `json:"session_id"`
	EnqueuedAt   time.Time  `json:"enqueued_at"`
	LastSignalAt *time.Time `json:"last_signal_at,omitempty"`
}

// LaneReleaseResult 는 사람이 한 회수의 결과다.
type LaneReleaseResult struct {
	RowID       int64  `json:"row_id"`
	SessionID   string `json:"session_id"`
	HeldRelease bool   `json:"held_release"` // 점유까지 회수했나(대기 중이면 false)
	JudgmentID  string `json:"judgment_id"`
}

// ─────────────────────────────────────────────────────────────────────────────
// land — 서기 · 보고 · 이탈
// ─────────────────────────────────────────────────────────────────────────────

// Land 는 랜딩 줄에 서거나, 이미 서 있으면 내 자리를 다시 낸다.
//
// ★ 전부 한 트랜잭션 안에서 한다. DSN 이 _txlock=immediate 라 land 끼리 직렬화된다
// (store.go 의 Tx 주석). 그래서 "맨 앞인가" 판정과 취득 사이에 남이 끼어들 수 없다.
//
// ★ **ResourceHeldError 는 오류가 아니라 정상 결과다.** 삼켜서 "너는 N번째"로 바꾸고
// 트랜잭션은 커밋한다. 여기서 롤백하면 줄 행과 순번이 함께 사라져 큐에 영원히
// 한 명만 남고 "순서 큐"라는 이름이 거짓이 된다.
//
// ★ 순서 집행 지점은 front.ID == mine.ID 비교 **하나**다. 이 비교가 없으면 순번은
// 표시용이 되고 아무것도 집행하지 않는다.
//
// 차례를 미는 주체는 **다음 호출**이다(지연 부여). 서버가 남의 이름으로 자원을 잡지 않는다.
func (s *Service) Land(ctx context.Context, in LandInput) (LandResult, error) {
	if strings.TrimSpace(in.Project) == "" || strings.TrimSpace(in.SessionID) == "" {
		return LandResult{}, &RefusedError{What: "land", Reason: "프로젝트나 세션 좌표가 비었다"}
	}
	var out LandResult
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		// 시도를 **먼저** 예약한다 — 롤백돼도 남는다. 끝에 두면 성공한 것만 세게 되고,
		// 그러면 §10 의 "세션당 쓰기 호출 수"가 대기 폴링의 비용을 못 본다.
		t.LogEvent("lane.land", in.Project, in.SessionID, map[string]any{"mode": "acquire"})

		mine, err := t.EnqueueLanding(in.Project, in.SessionID)
		if err != nil {
			return err
		}
		out = LandResult{State: "waiting", RowID: mine.ID}

		front, err := t.FrontLandingRow(in.Project)
		if err != nil {
			return err // 방금 넣었으므로 ErrNotFound 는 불가능하다
		}
		if front.ID == mine.ID {
			_, aerr := t.AcquireResource(in.Project, LaneResource, store.Holder{SessionID: in.SessionID})
			if aerr == nil {
				out.State = "turn"
				out.Position = 1
				t.LogEvent("lane.grant", in.Project, in.SessionID,
					map[string]any{"row": mine.ID})
				return nil
			}
			var held *store.ResourceHeldError
			if !errors.As(aerr, &held) {
				return aerr
			}
			if held.Holder.SessionID == in.SessionID {
				// 이미 내가 쥐고 있다 = 재진입이다. **저장층 둘의 재진입 성질이 반대라
				// 그것을 잇는 자리가 여기밖에 없다**: EnqueueLanding 은 재진입 안전이라
				// 기존 행을 그대로 내주는데(store/landing.go), AcquireResource 는 같은
				// 점유자여도 무조건 INSERT 하고 부분 유니크 위반을 ResourceHeldError 로
				// 바꾼다(store/resource.go). 안 이으면 "이미 서 있으면 내 자리를 다시 낸다"는
				// 이 함수의 계약이 점유자에게 {waiting, position:1, holder:자기 자신} 을
				// 답하고, 그 세션은 **자기 자신을 기다리며** report·leave 를 안 불러
				// 레인이 교착한다. 표시 오류가 아니라 교착이다.
				//
				// grant 이벤트는 다시 안 남긴다 — 부여가 아니라 재확인이고, 여기서 세면
				// 대기 폴링 횟수가 부여 횟수로 둔갑한다.
				out.State = "turn"
				out.Position = 1
				return nil
			}
			// 맨 앞인데 **남이** 쥐고 있다 = 두 표가 어긋난 상태다. 오류로 올리지 않고
			// 점유자를 그대로 실어 보낸다 — 그 상태를 푸는 것은 사람의 회수다.
		}
		pos, holder, err := s.lanePosition(t, in.Project, mine.ID)
		if err != nil {
			return err
		}
		out.Position, out.Holder = pos, holder
		return nil
	})
	// ★ 앞사람의 마지막 신호는 **커밋 뒤에** 읽는다. 트랜잭션 안에서 다른 커넥션으로 읽으면
	//   쓰기 잠금을 쥔 채 커넥션 풀(상한 8)을 기다리는 자리가 생기고, 그 대기 동안 다른 land
	//   전부가 busy_timeout 만큼 선다. 이 값은 사람이 나이를 재는 표시용이라 커밋 직후 시점으로 충분하다.
	if err == nil && out.Holder != nil {
		out.Holder.LastSignalAt, _ = s.lastSignal(ctx, out.Holder.SessionID)
	}
	if err != nil {
		s.logFail(ctx, "lane.land", in.Project, in.SessionID, err)
		s.log.ErrorContext(ctx, "랜딩 줄 서기 실패",
			"project", clip(in.Project, 64), "session_id", clip(in.SessionID, 64),
			"error", err.Error())
		return LandResult{}, err
	}
	s.log.InfoContext(ctx, "랜딩 줄",
		"project", in.Project, "session_id", in.SessionID,
		"mode", out.State, "count", out.Position)
	return out, nil
}

// LandReport 는 레인을 쓰고 난 결과를 보고하고 레인을 놓는다.
//
// ★ **먼저 내가 아직 점유자인지 본다.** 아니면 "회수됐다"로 답한다 — 여기서 줄 행을
// ok 로 닫으면 "성공적으로 랜딩했다"는 거짓 기록이 원장에 남고, 회수 사유는 덮여 사라진다.
func (s *Service) LandReport(ctx context.Context, in LandReportInput) (LandResult, error) {
	if strings.TrimSpace(in.Project) == "" || strings.TrimSpace(in.SessionID) == "" {
		return LandResult{}, &RefusedError{What: "land report", Reason: "프로젝트나 세션 좌표가 비었다"}
	}
	if in.Kind != model.LandingLeftOK && in.Kind != model.LandingLeftFail {
		return LandResult{}, &RefusedError{What: "land report",
			Reason: fmt.Sprintf("보고 종류는 ok 또는 fail 이어야 한다(받은 값 %q)", clip(string(in.Kind), 32)),
			Guidance: "줄 서 놓고 그만두는 것은 leave 다. finish 는 마무리가 함께 닫고, " +
				"force 는 사람이 회수할 때만 붙는다."}
	}
	// 사유 필수 판정을 여기서 먼저 한다 — 스키마 CHECK 는 최종 방어이지 1차 방어가 아니다.
	if err := store.ValidateLandingLeave(in.Kind, in.Detail); err != nil {
		return LandResult{}, &RefusedError{What: "land report", Reason: err.Error(),
			Guidance: "다음 사람이 \"다시 서면 통과할 종류인가\"에 답할 수 있어야 한다 — 한 줄이면 된다."}
	}

	var out LandResult
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		t.LogEvent("lane.report", in.Project, in.SessionID, map[string]any{
			"mode": string(in.Kind), "bytes": len(in.Detail),
		})

		held, herr := t.HeldBy(in.Project, LaneResource)
		if herr != nil && !errors.Is(herr, store.ErrNotFound) {
			return herr
		}
		if herr != nil || held.SessionID != in.SessionID {
			// 내 레인이 아니다. 줄 행을 **건드리지 않고** 사실만 답한다.
			return s.laneNotMine(t, in.Project, in.SessionID, &out)
		}

		// 여기부터는 내가 점유자다. 줄 행 번호를 응답에 싣기 위해 먼저 읽는다.
		row, rerr := t.LiveLandingRow(in.Project, in.SessionID)
		switch {
		case rerr == nil:
			out.RowID = row.ID
		case errors.Is(rerr, store.ErrNotFound):
			// 점유는 있는데 줄 행이 없다 = 두 표가 어긋난 상태다. 그래도 **반납은 한다** —
			// 여기서 멈추면 아무도 못 잡는 레인이 그대로 남는다. 사실은 원장에 남긴다.
			t.LogEvent("lane.divergent", in.Project, in.SessionID,
				map[string]any{"mode": "report", "state": "hold-without-row"})
		default:
			return rerr
		}

		if err := t.ReleaseResource(in.Project, LaneResource, store.Holder{SessionID: in.SessionID}); err != nil {
			return err
		}
		// 살아 있는 행이 없으면 무동작으로 통과한다(CloseLandingRowBySession 의 규율).
		if err := t.CloseLandingRowBySession(in.Project, in.SessionID, in.Kind, in.Detail); err != nil {
			return err
		}
		out.State = "released"
		return nil
	})
	if err != nil {
		s.logFail(ctx, "lane.report", in.Project, in.SessionID, err)
		s.log.ErrorContext(ctx, "랜딩 보고 실패",
			"project", clip(in.Project, 64), "session_id", clip(in.SessionID, 64),
			"mode", string(in.Kind), "error", err.Error())
		return LandResult{}, err
	}
	s.log.InfoContext(ctx, "랜딩 보고",
		"project", in.Project, "session_id", in.SessionID,
		"mode", string(in.Kind), "state", out.State)
	return out, nil
}

// LandLeave 는 줄에서 스스로 빠진다. **레인 미보유여도 성립한다** —
// 줄 서 놓고 포기한 세션이 스스로 빠지는 유일한 길이다.
//
// ★ 쥐고 있으면 점유도 함께 놓는다. 줄 행만 닫으면 "대응하는 줄 행이 없는 살아 있는 점유"가
// 남아 그 프로젝트의 랜딩이 전원 정지한다(파일 위쪽의 불변식).
func (s *Service) LandLeave(ctx context.Context, in LandLeaveInput) (LandResult, error) {
	if strings.TrimSpace(in.Project) == "" || strings.TrimSpace(in.SessionID) == "" {
		return LandResult{}, &RefusedError{What: "land leave", Reason: "프로젝트나 세션 좌표가 비었다"}
	}
	if err := store.ValidateLandingLeave(model.LandingLeftLeave, in.Detail); err != nil {
		return LandResult{}, &RefusedError{What: "land leave", Reason: err.Error(),
			Guidance: "왜 줄에서 빠지는지 한 줄이면 된다 — 사유 없는 이탈은 나중에 되짚을 수 없다."}
	}

	var out LandResult
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		t.LogEvent("lane.leave", in.Project, in.SessionID, map[string]any{"bytes": len(in.Detail)})

		row, rerr := t.LiveLandingRow(in.Project, in.SessionID)
		switch {
		case rerr == nil:
			out.RowID = row.ID
		case errors.Is(rerr, store.ErrNotFound):
			// 줄에 안 서 있다. 오류로 올리지 않는다 — 이탈은 멱등해야 한다
			// (item.go 의 ReleaseClaim 과 같은 규율). 아래 점유 반납은 그대로 시도한다.
		default:
			return rerr
		}

		held, herr := t.HeldBy(in.Project, LaneResource)
		switch {
		case herr == nil && held.SessionID == in.SessionID:
			if err := t.ReleaseResource(in.Project, LaneResource, store.Holder{SessionID: in.SessionID}); err != nil {
				return err
			}
		case herr == nil, errors.Is(herr, store.ErrNotFound):
			// 남이 쥐었거나 아무도 안 쥐었다. 남의 점유는 건드리지 않는다.
		default:
			return herr
		}

		if err := t.CloseLandingRowBySession(in.Project, in.SessionID, model.LandingLeftLeave, in.Detail); err != nil {
			return err
		}
		out.State = "left"
		return nil
	})
	if err != nil {
		s.logFail(ctx, "lane.leave", in.Project, in.SessionID, err)
		s.log.ErrorContext(ctx, "랜딩 줄 이탈 실패",
			"project", clip(in.Project, 64), "session_id", clip(in.SessionID, 64),
			"error", err.Error())
		return LandResult{}, err
	}
	s.log.InfoContext(ctx, "랜딩 줄 이탈",
		"project", in.Project, "session_id", in.SessionID, "count", out.RowID)
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 레인 읽기 · 회수
// ─────────────────────────────────────────────────────────────────────────────

// LandingLane 은 지금 줄과 점유자를 낸다. 읽기 전용이라 트랜잭션을 안 거친다(WAL).
//
// ★ Entries 는 **절대 nil 이 아니다.** 0건은 빈 슬라이스여야 하고, "안 읽었다"는
// 호출부가 *LaneView 를 nil 로 두어 표현한다. 여기서 접으면 화면이 둘을 구분 못 한다.
//
// ★ 두 질의(줄·점유자)는 별개 스냅숏이라 그 사이의 land 커밋이 **거짓 정합 어긋남**을
// 만들 수 있다. 어긋나 보일 때만 줄을 한 번 더 읽어 재확인한다 — 아래 본문 주석에
// 왜 트랜잭션이 아니라 재확인인지가 있다.
//
// ★ 줄 길이만큼 LastSignal 을 부른다. 점유자 몫은 **따로 안 부른다** — 점유자가 줄에
// 있으면(정상 경로) 루프에서 읽은 값을 재사용하고, 줄에 없을 때(정합 어긋남)만 한 번 더
// 부른다. 줄 길이는 그 프로젝트의 동시 세션 수라 실무상 한 자릿수이므로 지금은 이대로
// 둔다 — 여기가 느려지면 신호 MAX 를 세션 목록으로 한 번에 읽는 질의를 store 에 더하면
// 된다. 나중에 "왜 느리지"를 묻는 사람이 이 문장을 보고 바로 답에 닿아야 해서 적어 둔다.
//
// ★ 이 문단은 **계약이 아니라 지금 코드의 사실이다.** 질의 수를 세는 이음매가 없어서
// 잠그는 시험도 없다 — 여기를 고치는 사람은 아래 두 자리(루프의 캡처, 점유자 채움)를
// 눈으로 같이 봐야 하고, 그러기 싫으면 세는 이음매를 먼저 만들어야 한다.
func (s *Service) LandingLane(ctx context.Context, project string) (LaneView, error) {
	if strings.TrimSpace(project) == "" {
		return LaneView{}, &RefusedError{What: "lane", Reason: "project 가 비었다"}
	}
	rows, err := s.st.ListLandingQueue(ctx, project)
	if err != nil {
		return LaneView{}, err
	}

	var holder *LaneHolder
	hold, herr := s.st.HeldBy(ctx, project, LaneResource)
	switch {
	case herr == nil:
		holder = &LaneHolder{SessionID: hold.SessionID, AcquiredAt: hold.AcquiredAt}
	case errors.Is(herr, store.ErrNotFound):
		// 아무도 안 쥐었다. Holder 는 nil 이고 그것이 곧 "레인이 비었다"다.
	default:
		return LaneView{}, herr
	}

	// ★ **어긋나 보일 때만 줄을 한 번 더 읽는다.** 위 두 질의는 트랜잭션 밖의 별개 질의라
	//   각각 자기 읽기 스냅숏을 가진다(WAL + database/sql 커넥션 풀). 사이에 세션 하나가
	//   land(줄 서기+취득을 한 트랜잭션으로 한다)를 커밋하면 줄은 **비어 있게**, 점유자는
	//   **있게** 읽혀서 화면이 "점유자는 있는데 줄 행이 없다"는 정합 어긋남 경고
	//   (mcpsrv/render.go)를 거짓으로 낸다. 그 경고는 이 화면에서 가장 시끄러운 문장이고,
	//   흔해지면 설계 §4 가 말한 "판별력 0"이 여기서 시작된다.
	//
	//   **트랜잭션으로 묶지 않는다** — DSN 이 _txlock=immediate 라 읽기 하나에 쓰기 잠금을
	//   잡고, 그러면 보드를 볼 때마다 그 프로젝트의 land 전부가 선다. 이 함수 머리의
	//   "읽기 전용이라 트랜잭션을 안 거친다"는 판정과 정합하는 쪽이 재확인이다.
	//
	//   줄을 **점유자 조회 뒤에** 다시 읽으므로 두 번째 스냅숏은 점유자 조회보다 새것이고,
	//   끼어든 land 의 줄 행이 반드시 보인다. 정상 경로 비용은 0이다(어긋나 보일 때만 돈다).
	//   남는 창은 반대 방향 하나다 — 점유자가 그 사이에 반납한 경우. 그때는 다음 조회에서
	//   점유도 함께 사라져 "비어 있음"으로 스스로 아문다. 진짜 어긋남은 조회를 다시 해도
	//   그대로 남는다는 점이 둘을 가른다.
	if holder != nil && !rowsHaveSession(rows, holder.SessionID) {
		fresh, ferr := s.st.ListLandingQueue(ctx, project)
		if ferr != nil {
			return LaneView{}, ferr
		}
		rows = fresh
	}

	// ★ 점유자가 줄에 있으면 아래 점유자 채움이 **같은 세션을 또 묻게 된다** — 정상 경로에서
	//   늘 그렇다. 그래서 루프를 돌면서 그 한 값을 붙잡아 둔다. 두 키(줄 행의 session_id 와
	//   점유의 session_id)는 같은 Land 호출의 한 값에서 갈라진 것이고 어느 쪽에도 정규화가
	//   없으므로 문자열이 같다 — 이 전제는 새로 만드는 것이 아니라 바로 아래 rowsHaveSession
	//   과 표시 계층 둘이 이미 == 로 의존하고 있는 것이다.
	//   맵을 만들지 않는다: 재사용할 대상이 최대 하나(점유는 배타다)라 값 두 개면 족하다.
	var holderSignal *time.Time
	holderSeen := false

	out := LaneView{Entries: make([]LaneEntry, 0, len(rows)), Holder: holder}
	for _, r := range rows {
		// 관측 실패와 "신호 없음"은 화면에서 둘 다 빈칸이다 — 사람이 다시 물으면 되고,
		// 실패 사유는 WARN 에 남는다. 이 축을 반드시 봐야 하는 곳은 불변으로 남는
		// 판단 본문이고, 그쪽(ReleaseLaneRow)은 두 경우를 다른 문장으로 적는다.
		at, _ := s.lastSignal(ctx, r.SessionID)
		if holder != nil && r.SessionID == holder.SessionID {
			holderSignal, holderSeen = at, true
		}
		out.Entries = append(out.Entries, LaneEntry{
			RowID: r.ID, SessionID: r.SessionID, EnqueuedAt: r.EnqueuedAt,
			LastSignalAt: at,
		})
	}
	if out.Holder != nil {
		if holderSeen {
			out.Holder.LastSignalAt = holderSignal
			// 위 재사용은 정합에 영향이 0이다: 키가 같으면 같은 질의라 결과가 같고,
			// 시점이 몇 ms 이른 것은 이 값이 표시용이라는 판정(lastSignal 주석)과 정합한다.
		} else {
			// ★ **여기 질의는 지운 것이 아니라 남긴 것이다.** 점유자가 줄에 없는 정합 어긋남
			//   갈래에서는 루프가 그 세션을 한 번도 안 읽었다. 그리고 이 값에는 실제 독자가
			//   있다 — 줄이 0건일 때 mcpsrv/render.go 의 어긋남 문장과 web/page.go 의 Missing
			//   행이 회수 판정용 "신호 나이"로 이 필드를 읽는 유일한 자리다. 안 채우면 그 두
			//   화면이 조용히 "신호 없음"이 되어, 사람이 회수를 판정할 두 숫자 중 하나가
			//   사라진다. 즉 이 갈래의 +1 질의는 낭비가 아니라 화면 계약이다.
			out.Holder.LastSignalAt, _ = s.lastSignal(ctx, out.Holder.SessionID)
		}
	}
	return out, nil
}

// rowsHaveSession 은 줄 목록에 그 세션의 행이 있는지다. 순수 함수다.
func rowsHaveSession(rows []model.LandingRow, sessionID string) bool {
	for _, r := range rows {
		if r.SessionID == sessionID {
			return true
		}
	}
	return false
}

// ReleaseLaneRow 는 사람이 줄 행 하나를 회수한다. **대상은 레인이 아니라 줄 행이다** —
// 점유 중이든 대기 중이든 같은 문법으로 빠져야 죽은 대기자도 큐에서 나온다.
//
// 한 트랜잭션에서 셋을 한다: (점유가 그 세션의 것이면) 강제 반납 → 줄 행을 force 로 닫기 →
// 판단(decision) 남기기. actor 는 누가 회수했나다 — CLI 는 세션 id 나 사용자, 웹은 빈 문자열이다.
//
// ★ 판단은 left_detail 의 **사본이 아니다.** left_detail 은 사람이 친 한 줄이고, 판단은
// 거기에 서버가 관측한 것(점유 경과·마지막 신호 나이·그때 줄에 있던 사람)을 더한 넓은
// 기록이다. 담는 것이 다르므로 "어느 쪽이 정본인가"가 생기지 않는다.
func (s *Service) ReleaseLaneRow(ctx context.Context, project string, rowID int64, actor, reason string) (LaneReleaseResult, error) {
	if strings.TrimSpace(project) == "" {
		return LaneReleaseResult{}, &RefusedError{What: "lane release", Reason: "project 가 비었다"}
	}
	if rowID <= 0 {
		return LaneReleaseResult{}, &RefusedError{What: "lane release",
			Reason:   fmt.Sprintf("줄 행 번호가 %d 다 — 회수 대상은 줄 행 하나다", rowID),
			Guidance: "번호는 보드의 레인 절과 fd lane 목록이 낸다."}
	}
	if strings.TrimSpace(reason) == "" {
		return LaneReleaseResult{}, &RefusedError{What: "lane release",
			Reason:   "회수 사유가 비었다",
			Guidance: "사유가 원장에 안 남는 회수는 나중에 \"왜 남의 실행이 깨졌나\"에 답할 수 없다."}
	}
	now := s.now()

	// ★ 마지막 신호는 트랜잭션 **밖에서** 먼저 읽는다. 신호 표를 트랜잭션 안에서 읽으려면
	//   다른 커넥션이 필요한데, 쓰기 잠금을 쥔 채 커넥션 풀(상한 8)을 기다리면 그 대기가
	//   다른 쓰기 전부를 busy_timeout 만큼 세운다. 이 값은 판단 본문에 적을 **관측**이고,
	//   "이 행이 지금도 살아 있나"라는 권위 있는 판정은 아래 트랜잭션이 다시 한다.
	pre, err := s.st.ListLandingQueue(ctx, project)
	if err != nil {
		return LaneReleaseResult{}, err
	}
	// ★ 셋을 **다른 문장으로** 적는다. 판단은 불변으로 남는 기록이라, 못 읽은 것을
	//   "없다"로 적으면 그 자리에 거짓 사실이 영구히 박힌다(이 기능이 금지하는 부류다).
	signalLine := "마지막 신호: 관측하지 못했다(회수 직전 줄 목록에 이 행이 없었다)"
	for _, r := range pre {
		if r.ID != rowID {
			continue
		}
		at, observed := s.lastSignal(ctx, r.SessionID)
		switch {
		case !observed:
			signalLine = "마지막 신호: 읽지 못했다(신호 조회가 실패했다 — 원인은 서버 로그의 WARN 에 있다). " +
				"**이 회수는 신호 나이를 보지 않고 한 것이다.**"
		case at == nil:
			signalLine = "마지막 신호: 없음(이 세션은 신호를 한 번도 안 남겼다)"
		default:
			signalLine = fmt.Sprintf("마지막 신호: %s (나이 %s)",
				at.Format(time.RFC3339), now.Sub(*at).Round(time.Second))
		}
		break
	}

	var out LaneReleaseResult
	err = s.st.Tx(ctx, func(t *store.Tx) error {
		// ★ 사람은 **"actor"** 에 싣는다. "mode" 는 형제 이벤트(lane.land)에서
		// acquire|report|leave 를 뜻하는 자리라, 거기에 사람 이름을 넣으면 같은 키가
		// 이벤트마다 다른 것을 뜻하게 되고 소비자가 조용히 엉뚱한 값을 읽는다.
		// event 는 추가 전용이라 잘못 쌓인 행은 영구히 남는다 — 미루는 것 자체가 비용이다.
		t.LogEvent("lane.release", project, "", map[string]any{
			"row": rowID, "actor": clip(actor, 64), "bytes": len(reason),
		})

		// 줄 전체를 **닫기 전에** 읽는다. 판단에 "그때 줄에 있던 사람"을 적어야 하고,
		// 대상 행의 세션도 여기서 나온다.
		queue, err := t.ListLandingQueue(project)
		if err != nil {
			return err
		}
		var target model.LandingRow
		for _, r := range queue {
			if r.ID == rowID {
				target = r
				break
			}
		}
		if target.ID == 0 {
			return &RefusedError{What: "lane release",
				Reason: fmt.Sprintf("줄 행 %d 는 프로젝트 %s 의 줄에 살아 있지 않다",
					rowID, clip(project, 64)),
				Guidance: "이미 빠졌거나 다른 프로젝트의 번호다 — 지금 번호는 보드의 레인 절이 낸다."}
		}
		out.RowID, out.SessionID = target.ID, target.SessionID

		held, herr := t.HeldBy(project, LaneResource)
		if herr != nil && !errors.Is(herr, store.ErrNotFound) {
			return herr
		}
		holdLine := "점유: 없음(대기 중인 줄 행이라 반납할 것이 없다)"
		switch {
		case herr == nil && held.SessionID == target.SessionID:
			if err := t.ForceReleaseResource(project, LaneResource, reason); err != nil {
				return err
			}
			out.HeldRelease = true
			holdLine = fmt.Sprintf("점유: 회수함(획득 %s · 경과 %s)",
				held.AcquiredAt.Format(time.RFC3339), now.Sub(held.AcquiredAt).Round(time.Second))
		case herr == nil:
			holdLine = fmt.Sprintf("점유: 다른 세션 %s 가 쥐고 있어 건드리지 않았다", held.SessionID)
		}

		if err := t.CloseLandingRow(project, rowID, model.LandingLeftForce, reason); err != nil {
			return err
		}

		j, err := t.AddJudgment(model.Judgment{
			Project: project, At: now, Kind: model.JudgmentDecision,
			Title: fmt.Sprintf("랜딩 줄 행 회수: %s", clip(target.SessionID, 64)),
			Body:  laneReleaseBody(now, target, queue, holdLine, signalLine, actor, reason),
			// 세션에 건다 — 항목이 아니라 세션이 이 회수의 좌표다.
			Links: []model.JudgmentLink{{TargetKind: "session", TargetID: target.SessionID}},
		})
		if err != nil {
			return err
		}
		out.JudgmentID = j.ID
		return nil
	})
	if err != nil {
		s.logFail(ctx, "lane.release", project, "", err)
		s.log.ErrorContext(ctx, "랜딩 줄 행 회수 실패",
			"project", clip(project, 64), "count", rowID, "error", err.Error())
		return LaneReleaseResult{}, err
	}
	s.log.InfoContext(ctx, "랜딩 줄 행 회수",
		"project", project, "session_id", out.SessionID, "count", out.RowID,
		"mode", clip(actor, 64), "state", out.HeldRelease)
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 헬퍼
// ─────────────────────────────────────────────────────────────────────────────

// lanePosition 은 줄에서 내 자리(1-based)와 지금 점유자를 낸다.
//
// ★ 트랜잭션 안에서 센다. 밖에서 읽으면 방금 넣은 내 행이 아직 커밋 전이라 안 보이고,
// 그러면 자기 자신이 빠진 줄에서 순번을 세게 된다.
//
// 내 행을 못 찾으면 0을 낸다 — 없는 자리를 1로 채우면 "맨 앞"이라는 거짓이 된다.
//
// ★ 점유자의 LastSignalAt 은 **여기서 안 채운다.** 신호 표는 이 트랜잭션 밖에 있어
// 다른 커넥션으로 읽어야 하는데, 쓰기 잠금을 쥔 채 커넥션을 기다리면 그 대기가
// 다른 land 전부를 세운다. 호출부가 커밋 뒤에 채운다.
func (s *Service) lanePosition(t *store.Tx, project string, rowID int64) (int, *LaneHolder, error) {
	rows, err := t.ListLandingQueue(project)
	if err != nil {
		return 0, nil, err
	}
	pos := 0
	for i, r := range rows {
		if r.ID == rowID {
			pos = i + 1
			break
		}
	}

	hold, herr := t.HeldBy(project, LaneResource)
	switch {
	case herr == nil:
		return pos, &LaneHolder{SessionID: hold.SessionID, AcquiredAt: hold.AcquiredAt}, nil
	case errors.Is(herr, store.ErrNotFound):
		// 아무도 안 쥐었는데 내 차례도 아니다 = 앞사람이 아직 land 를 안 불렀다.
		// 점유자를 지어내지 않는다.
		return pos, nil, nil
	default:
		return 0, nil, herr
	}
}

// laneNotMine 은 "레인이 내 것이 아니다"를 응답으로 옮긴다. 줄 행은 건드리지 않는다.
//
// 회수된 세션에게 **왜** 레인을 잃었는지 그대로 답하는 것이 이 함수의 목적이다.
// 사유는 닫힌 줄 행의 left_detail 에 있다.
func (s *Service) laneNotMine(t *store.Tx, project, sessionID string, out *LandResult) error {
	*out = LandResult{State: "reclaimed"}
	row, err := t.LastLandingRow(project, sessionID)
	switch {
	case err == nil:
		out.RowID = row.ID
		out.Reason = laneLeftReason(row)
	case errors.Is(err, store.ErrNotFound):
		out.Reason = "이 프로젝트 줄에 선 기록이 없다 — 먼저 land 로 줄을 서라"
	default:
		return err
	}
	return nil
}

// laneLeftReason 은 줄 행 하나가 "왜 내 것이 아닌가"를 한 줄로 만든다. 순수 함수다.
//
// ★ 절대 빈 문자열을 내지 않는다. 비면 CLI 가 "회수됐다"만 찍고 이유를 못 말하는데,
// 그러면 세션은 같은 호출을 반복하는 것 말고 할 수 있는 것이 없다.
func laneLeftReason(row model.LandingRow) string {
	if row.LeftAt == nil {
		return "레인을 쥔 적이 없다 — 줄 행은 아직 살아 있으니 land 로 차례를 확인해라"
	}
	if strings.TrimSpace(row.LeftDetail) != "" {
		return row.LeftDetail
	}
	// ok·finish 는 사유가 면제라 여기로 온다. 종류 자체가 "왜"다.
	return fmt.Sprintf("줄 행이 %s 로 이미 닫혀 있다", row.LeftKind)
}

// laneReleaseBody 는 회수 판단의 본문을 만든다. 순수 함수다.
//
// 서버가 관측한 것을 전부 적는다 — 이 판단이 left_detail 한 줄보다 넓어야 하는 이유가
// 여기 있다. 나중에 "왜 남의 레인이 끊겼나"에 답할 유일한 기록이다.
func laneReleaseBody(now time.Time, target model.LandingRow, queue []model.LandingRow,
	holdLine, signalLine, actor, reason string) string {
	var b strings.Builder
	b.WriteString("랜딩 줄 행을 회수했다.\n")
	fmt.Fprintf(&b, "프로젝트: %s\n", target.Project)
	fmt.Fprintf(&b, "줄 행: %d · 세션 %s (대기 시작 %s · 경과 %s)\n",
		target.ID, target.SessionID, target.EnqueuedAt.Format(time.RFC3339),
		now.Sub(target.EnqueuedAt).Round(time.Second))
	fmt.Fprintf(&b, "사유: %s\n", reason)
	b.WriteString(holdLine + "\n")
	b.WriteString(signalLine + "\n")

	b.WriteString("그때 줄에 있던 사람:")
	for i, r := range queue {
		fmt.Fprintf(&b, " %d.%s(행 %d)", i+1, r.SessionID, r.ID)
	}
	b.WriteString("\n")

	if strings.TrimSpace(actor) == "" {
		b.WriteString("행위자: 대시보드(사람). 세션이 아니라 사람이 누른 것이므로 session_id 는 비어 있다.\n")
	} else {
		fmt.Fprintf(&b, "행위자: %s. 세션 id 인지 사람 이름인지 서버는 구분하지 않으므로 "+
			"judgment.session_id 는 비운다(FK 대상이라 없는 id 면 회수 자체가 실패한다).\n", actor)
	}
	b.WriteString("★ 회수는 자동 만료가 아니다 — 사람이 위 관측을 보고 판정한 것이다.")
	return b.String()
}

// lastSignal 은 세션의 마지막 신호 시각이다. 두 번째 값은 **관측에 성공했나**다.
//
// ★ 읽기 실패를 오류로 올리지 않는다. 이 값은 사람이 나이를 재는 표시용인데,
// 그것 하나 못 읽었다고 Land 의 트랜잭션을 되돌리면 줄 행과 순번이 함께 사라진다 —
// 이 기능이 가장 조심하는 바로 그 사고다. 대신 삼키지도 않는다: WARN 으로 원인을 남기고,
// **"못 읽었다"와 "신호가 하나도 없다"를 두 번째 값으로 가른다**(pick.go 의 규율 —
// 못 읽은 축은 값으로 채우지 않고 그 사실을 따로 남긴다).
//
// 표시용 응답(LaneHolder.LastSignalAt)은 둘 다 nil 로 접어도 사람이 화면에서 다시 물으면
// 되지만, **판단 본문처럼 불변으로 남는 기록은 반드시 이 축을 봐야 한다** —
// 거기에 "신호가 없었다"고 적히면 그 거짓은 영구히 남고 되짚을 방법이 없다.
func (s *Service) lastSignal(ctx context.Context, sessionID string) (*time.Time, bool) {
	at, ok, err := s.st.LastSignal(ctx, sessionID)
	if err != nil {
		s.log.WarnContext(ctx, "마지막 신호 조회 실패(레인 응답은 계속한다)",
			"session_id", clip(sessionID, 64), "error", err.Error())
		return nil, false
	}
	if !ok {
		return nil, true // 관측은 됐다. 신호가 없는 것이다
	}
	return &at, true
}
