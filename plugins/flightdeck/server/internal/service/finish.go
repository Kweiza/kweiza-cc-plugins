package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 마무리 — 판단 저장 + 후속 등록 + 항목 종료 + 자원 반납을 **한 호출, 한 트랜잭션**으로.
//
// 기존 규율은 이 순서를 산문으로 강제했고(문서 → done → add → unregister),
// 순서를 어긴 세션이 실제로 있었다. 원자화하면 **검산할 순서 자체가 사라진다**.

// HandoffGuidance 는 body 없이 finish 를 부른 세션에게 그 자리에서 내는 문구다.
//
// 규율 산문을 도구 설명이나 스킬에 넣지 않는다(컨텍스트 예산 — 설계 §6).
// **필요할 때, 그 자리에서** 응답에 싣는다. 넷의 문구는 설계 §5 "남는 손 기재 넷"의
// judgment(kind=handoff) 줄 그대로다.
const HandoffGuidance = `무엇을 적어야 하는가 — 넷이다:
  ① 왜 그렇게 했나
  ② 무엇을 기각했나
  ③ 일부러 안 한 것
  ④ 확인했으나 못 한 것
안 남기면 다음 세션이 같은 조사를 처음부터 다시 하거나,
더 나쁘게는 **의도적으로 남긴 자리를 결함으로 보고 고치러 간다.**
후속이 있으면 followups 로 같은 호출에 넣어라 — 그러면 판단과 후속이 FK 로 이어진다.`

// FollowupInput 은 마무리와 같은 호출에 넣는 후속 항목이다.
type FollowupInput struct {
	ID     string
	Title  string
	Body   string
	Paths  []string
	Labels []string
	After  []model.After
}

// FinishInput 은 마무리 한 번의 인자다.
type FinishInput struct {
	Project     string
	SessionID   string
	ItemID      string
	Outcome     model.ItemState // done | dropped
	Title       string
	Body        string // 핸드오프 본문. **비면 거절한다**
	CloseReason string // dropped 면 필수
	Followups   []FollowupInput
	Links       []model.JudgmentLink // 커밋 등 추가 링크
}

// FinishResult 는 마무리 결과다.
type FinishResult struct {
	Item      model.Item     `json:"item"`
	Judgment  model.Judgment `json:"judgment"`
	Followups []model.Item   `json:"followups,omitempty"`
	Released  []string       `json:"released,omitempty"` // 함께 반납한 자원
	Derived
}

// FinishVerdict 는 마무리 요청의 판정이다. Reason 은 항상 채운다.
type FinishVerdict struct {
	OK       bool
	Reason   string
	Guidance string // 거절일 때 무엇을 하면 되는지. 없을 수 있다
}

// JudgeFinish 는 마무리 인자가 성립하는지 판정한다. 순수 함수다.
//
// 불리언이 아니라 **사유와 처방**을 돌려준다. body 누락은 이 도구가 지키려는 것의 핵심이라
// 사유만으로 끝내지 않고 "무엇을 적어야 하는지"까지 낸다 —
// 판단은 원리적으로 파생 불가한 유일한 자산이고, 안 남으면 다음 세션이
// **의도적으로 남긴 자리를 결함으로 보고 고치러 간다.**
func JudgeFinish(outcome model.ItemState, itemID, body, closeReason string) FinishVerdict {
	switch {
	case strings.TrimSpace(itemID) == "":
		return FinishVerdict{Reason: "item_id 가 비었다 — 무엇을 끝내는지 없이는 종료도 판단 링크도 좌표가 없다"}

	case outcome != model.ItemDone && outcome != model.ItemDropped:
		return FinishVerdict{Reason: fmt.Sprintf(
			"outcome 은 done 또는 dropped 여야 한다(받은 값 %q)", clip(string(outcome), 32))}

	case strings.TrimSpace(body) == "":
		return FinishVerdict{
			Reason:   "판단 본문(body)이 비어 있어 끝낼 수 없다",
			Guidance: HandoffGuidance,
		}

	case outcome == model.ItemDropped && strings.TrimSpace(closeReason) == "":
		return FinishVerdict{
			Reason:   "outcome=dropped 에는 폐기 사유(close_reason)가 필수다",
			Guidance: "사유 없는 폐기는 나중에 왜 버렸는지 되짚을 수 없다 — 한 줄이면 된다.",
		}

	default:
		return FinishVerdict{OK: true, Reason: "항목 좌표·종료 상태·판단 본문이 전부 있다"}
	}
}

// Finish 는 판단 저장 + 후속 등록 + 항목 종료 + 자원 반납을 한 트랜잭션에서 한다.
//
// ★ 넷 중 하나라도 실패하면 **전부 롤백된다.** 판단만 남고 항목이 안 닫히거나,
// 항목은 닫혔는데 후속이 안 들어간 상태는 만들어지지 않는다 —
// 그 반쪽 상태가 기존 도구에서 "핸드오프는 했는데 후속이 유입되지 않은" 결함이었다.
func (s *Service) Finish(ctx context.Context, in FinishInput) (FinishResult, error) {
	if v := JudgeFinish(in.Outcome, in.ItemID, in.Body, in.CloseReason); !v.OK {
		s.log.WarnContext(ctx, "마무리 거절",
			"project", clip(in.Project, 64), "session_id", clip(in.SessionID, 64),
			"item", clip(in.ItemID, 64), "reason", v.Reason)
		return FinishResult{}, &RefusedError{What: "finish", Reason: v.Reason, Guidance: v.Guidance}
	}
	for i, f := range in.Followups {
		if err := ValidateItemID(f.ID); err != nil {
			return FinishResult{}, &RefusedError{What: "finish",
				Reason:   fmt.Sprintf("%d번째 후속: %v", i, err),
				Guidance: "후속 항목 id 도 브랜치 이름으로 그대로 쓰인다."}
		}
		if strings.TrimSpace(f.Title) == "" || strings.TrimSpace(f.Body) == "" {
			return FinishResult{}, &RefusedError{What: "finish",
				Reason: fmt.Sprintf("%d번째 후속(%s)에 제목이나 본문이 없다", i, clip(f.ID, 64)),
				Guidance: "후속은 다음 세션이 집을 항목이다 — 제목만 있으면 " +
					"그 세션이 무엇을 해야 하는지 다시 조사해야 한다."}
		}
	}

	now := s.now()

	// 자원 목록은 트랜잭션 밖에서 읽는다(읽기는 WAL 이라 쓰기 잠금과 안 부딪힌다).
	holds, err := s.st.ListHeld(ctx, in.Project)
	if err != nil {
		return FinishResult{}, err
	}

	var out FinishResult
	err = s.st.Tx(ctx, func(t *store.Tx) error {
		// ★ 시도를 **먼저** 예약한다. Tx.LogEvent 는 롤백된 뒤에도 흘러가므로
		//   "무엇을 시도했다 실패했나"가 원장에 남는다 — 끝에 두면 성공한 것만 세게 되고,
		//   그러면 §10 의 "세션당 쓰기 호출 수"가 실패를 못 본다.
		t.LogEvent("item.finish", in.Project, in.SessionID, map[string]any{
			"item": in.ItemID, "mode": string(in.Outcome), "count": len(in.Followups),
			"bytes": len(in.Body), // §10 "세션당 판단 바이트" — 0 에 수렴하면 위험 신호다
		})

		// ① 판단 — 가장 먼저 저장한다. 이것이 원리적으로 파생 불가한 유일한 자산이다.
		links := append([]model.JudgmentLink{{TargetKind: "item", TargetID: in.ItemID}}, in.Links...)
		for _, f := range in.Followups {
			links = append(links, model.JudgmentLink{TargetKind: "item", TargetID: f.ID})
		}
		j, err := t.AddJudgment(model.Judgment{
			Project: in.Project, SessionID: in.SessionID, At: now,
			Kind: model.JudgmentHandoff, Title: in.Title, Body: in.Body, Links: links,
		})
		if err != nil {
			return err
		}
		out.Judgment = j

		// ② 후속 등록. 여기서 실패하면 ①③④ 가 전부 롤백된다 —
		//    그것이 이 함수를 한 트랜잭션에 둔 이유다.
		for _, f := range in.Followups {
			it := model.Item{
				Project: in.Project, ID: f.ID, Title: f.Title, Body: f.Body,
				Paths: f.Paths, Labels: f.Labels, State: model.ItemOpen, After: f.After,
				CreatedAt: now,
			}
			if err := t.AddItem(it); err != nil {
				return fmt.Errorf("후속 항목 %s 등록 실패: %w", clip(f.ID, 64), err)
			}
			out.Followups = append(out.Followups, it)
		}

		// ③ 항목 종료(선점 반납을 포함한다).
		if err := t.FinishItem(in.Project, in.ItemID, in.SessionID, in.Outcome, in.CloseReason); err != nil {
			return err
		}

		// ④ 이 세션이 쥔 자원 반납. 규율 산문이 강제하던 마지막 단계다.
		for _, h := range holds {
			if h.SessionID != in.SessionID {
				continue
			}
			if err := t.ReleaseResource(in.Project, h.Resource, store.Holder{SessionID: in.SessionID}); err != nil {
				return fmt.Errorf("자원 %s 반납 실패: %w", clip(h.Resource, 64), err)
			}
			out.Released = append(out.Released, h.Resource)
		}

		return nil
	})
	if err != nil {
		s.logFail(ctx, "item.finish", in.Project, in.SessionID, err)
		s.log.ErrorContext(ctx, "마무리 실패",
			"project", clip(in.Project, 64), "session_id", clip(in.SessionID, 64),
			"item", clip(in.ItemID, 64), "error", err.Error())
		return FinishResult{}, err
	}

	// ★ 트랜잭션이 커밋된 **바로 다음**에 부른다. 이 자리 뒤의 GetItem 이 실패해도
	// handoff 판단은 이미 남았으므로 열린 처방은 닫혀야 한다 — Note(finish.go 아래)와
	// 같은 규율이다: 커밋됐으면 그 뒤 무엇이 실패하든 ack 은 반드시 시도한다.
	s.ackPrescriptions(ctx, in.Project, in.SessionID)

	item, err := s.st.GetItem(ctx, in.Project, in.ItemID)
	if err != nil {
		return FinishResult{}, err
	}
	out.Item = item
	out.Derived = (&derive{}).result(now) // 마무리에는 git 파생이 없다. 그 사실도 사유로 남는다
	s.log.InfoContext(ctx, "마무리",
		"project", in.Project, "session_id", in.SessionID, "item", in.ItemID,
		"mode", string(in.Outcome), "count", len(out.Followups), "released", len(out.Released))
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 판단 · 발번
// ─────────────────────────────────────────────────────────────────────────────

// NoteInput 은 판단 하나를 남기는 인자다.
type NoteInput struct {
	Project    string
	SessionID  string
	Kind       model.JudgmentKind
	Title      string
	Body       string
	ItemID     string // 있으면 항목에 링크한다
	Supersedes string // 정정은 새 행 + supersedes. **덮어쓰기는 없다**
	Links      []model.JudgmentLink
}

// NoteResult 는 저장 확인과 이 노트를 받을 세션이다.
type NoteResult struct {
	Judgment   model.Judgment `json:"judgment"`
	Recipients []string       `json:"recipients"` // 지금 살아 있는 다른 세션들
}

// ValidateNoteKind 는 판단 종류가 열거에 있는지 본다. 순수 함수다.
func ValidateNoteKind(k model.JudgmentKind) error {
	switch k {
	case model.JudgmentHandoff, model.JudgmentDecision, model.JudgmentBlocked,
		model.JudgmentAsk, model.JudgmentNow, model.JudgmentRejected,
		model.JudgmentNotDone, model.JudgmentVerified, model.JudgmentDraft:
		return nil
	case "":
		return fmt.Errorf("판단 종류가 비었다 — handoff|decision|blocked|ask|now|rejected|not-done|verified|draft 중 하나여야 한다")
	default:
		return fmt.Errorf("판단 종류 %q 가 열거에 없다 — "+
			"handoff|decision|blocked|ask|now|rejected|not-done|verified|draft 중 하나여야 한다",
			clip(string(k), 32))
	}
}

// Recipients 는 이 노트를 받을 세션들이다. 순수 함수다.
//
// 자기 자신은 뺀다 — 자기가 쓴 것을 자기가 받는다고 세면 그 숫자가 항상 1 이상이 되어
// "아무도 안 보고 있다"는 사실이 화면에서 사라진다.
func Recipients(cards []SessionCard, self string) []string {
	var out []string
	for _, c := range cards {
		if c.View.Session.ID == self {
			continue
		}
		out = append(out, c.View.Session.ID)
	}
	return out
}

// Note 는 판단 하나를 남긴다. **추가 전용이다** — 정정은 새 행 + Supersedes 다.
func (s *Service) Note(ctx context.Context, in NoteInput) (NoteResult, error) {
	if err := ValidateNoteKind(in.Kind); err != nil {
		return NoteResult{}, &RefusedError{What: "note", Reason: err.Error()}
	}
	if strings.TrimSpace(in.Body) == "" {
		return NoteResult{}, &RefusedError{What: "note",
			Reason:   "판단 본문이 비었다",
			Guidance: "무엇을 왜 그렇게 했는지가 이 표의 존재 이유다 — 한 줄이라도 남겨라."}
	}
	now := s.now()

	links := in.Links
	if strings.TrimSpace(in.ItemID) != "" {
		links = append(links, model.JudgmentLink{TargetKind: "item", TargetID: in.ItemID})
	}
	if strings.TrimSpace(in.SessionID) != "" {
		links = append(links, model.JudgmentLink{TargetKind: "session", TargetID: in.SessionID})
	}

	var j model.Judgment
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		t.LogEvent("judgment.note", in.Project, in.SessionID, map[string]any{
			"mode": string(in.Kind), "bytes": len(in.Body), "item": clip(in.ItemID, 64),
		})
		var err error
		j, err = t.AddJudgment(model.Judgment{
			Project: in.Project, SessionID: in.SessionID, At: now,
			Kind: in.Kind, Title: in.Title, Body: in.Body,
			Supersedes: in.Supersedes, Links: links,
		})
		if err != nil {
			return err
		}
		if in.SessionID != "" {
			if err := t.Beat(in.SessionID, model.SignalMCP, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.logFail(ctx, "judgment.note", in.Project, in.SessionID, err)
		s.log.ErrorContext(ctx, "판단 저장 실패",
			"project", clip(in.Project, 64), "session_id", clip(in.SessionID, 64),
			"mode", string(in.Kind), "error", err.Error())
		return NoteResult{}, err
	}

	// 처방을 받고 판단을 남겼다 — 열린 처방을 닫는다. 실패해도 판단은 이미 저장됐다.
	//
	// ★ **수신자 파생보다 먼저 부른다.** 아래 sessionCards 가 실패하면 이 함수는
	// 그 자리에서 바로 반환한다 — 그 반환 뒤에 ack 을 두면, 판단은 트랜잭션에서
	// 이미 커밋됐는데 파생 실패 하나 때문에 처방이 영영 안 닫히는 반쪽 상태가 생긴다.
	// 커밋 여부만이 ack 을 가르는 조건이어야 하고, 그 뒤의 다른 축 실패는 아니다.
	s.ackPrescriptions(ctx, in.Project, in.SessionID)

	// 받을 세션은 조정 정보다. 파생 실패로 접지 않고 그대로 올린다.
	d := &derive{}
	proj, perr := s.st.GetProject(ctx, in.Project)
	var recipients []string
	if perr == nil {
		cards, err := s.sessionCards(ctx, proj, s.cut(now, 0), in.SessionID, d)
		if err != nil {
			return NoteResult{}, err
		}
		recipients = Recipients(cards, in.SessionID)
	}
	s.log.InfoContext(ctx, "판단 저장",
		"project", in.Project, "session_id", in.SessionID, "mode", string(in.Kind),
		"count", len(recipients), "bytes", len(in.Body))

	return NoteResult{Judgment: j, Recipients: recipients}, nil
}

// Alloc 은 논리 카운터의 다음 번호를 발급한다.
//
// ★ 락이 원리적으로 못 지키는 자리다 — 파일 접근을 직렬화해도
// 두 세션이 각자 "지금 값"을 읽고 각자 +1 하면 둘 다 같은 번호를 쓴다.
// 그래서 이 계층은 값을 읽어 더하지 않고 store 의 원자 발번을 그대로 부른다.
func (s *Service) Alloc(ctx context.Context, project, counter string) (int64, error) {
	if strings.TrimSpace(project) == "" {
		return 0, &RefusedError{What: "alloc", Reason: "project 가 비었다"}
	}
	if strings.TrimSpace(counter) == "" {
		return 0, &RefusedError{What: "alloc", Reason: "카운터 이름이 비었다"}
	}
	n, err := s.st.NextCounter(ctx, project, counter)
	if err != nil {
		s.log.ErrorContext(ctx, "발번 실패",
			"project", clip(project, 64), "mode", clip(counter, 64), "error", err.Error())
		return 0, err
	}
	s.st.LogEvent(ctx, "counter.alloc", project, "", map[string]any{
		"mode": clip(counter, 64), "count": n,
	})
	s.log.InfoContext(ctx, "발번", "project", project, "mode", clip(counter, 64), "count", n)
	return n, nil
}
