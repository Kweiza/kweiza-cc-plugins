package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 쓰기는 다섯뿐이고 전부 사유가 필수다(설계 §6). 그중 **셋**이 Tier A 에 있다 —
// 선점 회수 · 항목 폐기 · 레인 회수. 남은 하나(잡 우회 기록)는 여전히 Tier B 다.
//
// ★ 파생물에 손대는 폼을 하나라도 만들면 대시보드가 다시 손 기재 저장소가 되고,
// 그 순간 이 제품이 없애려던 병목 1위가 그대로 돌아온다. 그래서 여기 있는 쓰기는
// **회수 둘과 폐기 하나뿐**이고, 셋 다 사유를 원장(judgment)에 추가 전용으로 남긴다.

// ActionKind 는 대시보드가 허용하는 쓰기 종류다.
type ActionKind string

const (
	ActionReclaim ActionKind = "reclaim" // 선점 회수
	ActionDrop    ActionKind = "drop"    // 항목 폐기
	// ActionLaneRelease 는 랜딩 줄 행의 회수다.
	//
	// ★ 이름이 lane·lane-stop·lane-resume 이면 **안 된다.** 아래 JudgeAction 이 그 셋을
	// 이름으로 알고 "Tier B 라 거절"하는데 그 거절은 **여전히 참이다** — 정지/재개는
	// 러너의 일이고 이 서버에 러너가 없다. 열린 것은 회수 하나뿐이라 이름도 하나뿐이다.
	ActionLaneRelease ActionKind = "lane-release"
)

// reasonMin 은 사유의 최소 길이다. 한 글자짜리 사유는 사유가 아니다 —
// 나중에 되짚을 수 없는 기록은 원장의 부피만 늘린다.
const reasonMin = 4

// reasonMax 는 사유의 상한이다. 서사는 note 의 몫이라 여기서 잘라 두면
// 원장의 이 자리가 무엇을 담는 자리인지 흐려지지 않는다.
const reasonMax = 2000

// ActionInput 은 폼에서 온 것 전부다.
type ActionInput struct {
	Kind    ActionKind
	Project string
	Item    string
	Reason  string
}

// ActionVerdict 는 쓰기 요청의 판정이다. **불리언이 아니라 사유를 담는다** —
// 사유가 없으면 "사유를 안 적었다"와 "그런 항목이 없다"와 "이 축을 안 본다"가 같은 실패로 접힌다.
type ActionVerdict struct {
	OK     bool
	Reason string // 항상 채운다
}

// JudgeAction 은 대시보드 쓰기 요청이 성립하는지 판정한다. 순수 함수다.
//
// Tier B 버튼(레인 정지/재개 · 잡 우회 기록)의 이름을 **알고 거절한다**.
// 모르는 것으로 뭉개면 "아직 없다"와 "그런 것은 없다"가 구분되지 않는다.
func JudgeAction(in ActionInput) ActionVerdict {
	switch in.Kind {
	case ActionReclaim, ActionDrop, ActionLaneRelease:
		// 통과. 아래에서 인자를 본다
	case "lane", "lane-stop", "lane-resume":
		// ★ 이 거절은 **여전히 참이다.** 열린 것은 회수 하나뿐이라, 문구를 갈라
		// 무엇이 열렸고 무엇이 안 열렸는지를 한 문장에서 다 말한다. 뭉개면
		// 사람이 "레인은 화면에서 못 만진다"로 읽고 열린 길을 안 쓴다.
		return ActionVerdict{false, "레인 정지/재개는 여전히 Tier B(러너)다 — 지금 서버에 러너가 없다. " +
			"줄 행 회수(" + string(ActionLaneRelease) + ")는 열려 있다"}
	case "bypass":
		return ActionVerdict{false, "잡 우회 기록은 Tier B(러너)다 — 지금 서버에 잡이 없다"}
	default:
		return ActionVerdict{false, fmt.Sprintf("모르는 쓰기 종류다: %q", Clip(string(in.Kind), 64))}
	}
	if strings.TrimSpace(in.Project) == "" {
		return ActionVerdict{false, "프로젝트가 비었다"}
	}
	if strings.TrimSpace(in.Item) == "" {
		return ActionVerdict{false, "대상 항목이 비었다"}
	}
	if len([]rune(in.Item)) > 200 {
		return ActionVerdict{false, "항목 id 가 너무 길다(최대 200자)"}
	}
	// ★ 레인 회수의 대상은 항목 id 가 아니라 **줄 행 번호**다. 여기서 안 보면
	// 문자열이 그대로 서비스까지 가서 0 으로 파싱되고, "줄 행 0" 거절 문구가
	// 사람이 실제로 친 것과 무관한 말을 한다.
	if in.Kind == ActionLaneRelease {
		if v := JudgeLaneRowID(in.Item); !v.OK {
			return v
		}
	}
	reason := strings.TrimSpace(in.Reason)
	switch {
	case reason == "":
		return ActionVerdict{false, "사유가 비었다 — 사유 없는 회수·폐기는 나중에 되짚을 수 없다"}
	case len([]rune(reason)) < reasonMin:
		return ActionVerdict{false, fmt.Sprintf("사유가 너무 짧다(최소 %d자) — 무엇을 왜 했는지가 이 기록의 존재 이유다", reasonMin)}
	case len([]rune(reason)) > reasonMax:
		return ActionVerdict{false, fmt.Sprintf("사유가 너무 길다(최대 %d자) — 서사는 note 로 남겨라", reasonMax)}
	}
	return ActionVerdict{true, "인자가 성립한다(대상·사유가 있다)"}
}

// JudgeLaneRowID 는 폼이 보낸 회수 대상이 줄 행 번호인지 본다. 순수 함수다.
func JudgeLaneRowID(s string) ActionVerdict {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return ActionVerdict{false, fmt.Sprintf(
			"회수 대상이 줄 행 번호가 아니다: %q — 레인 회수의 대상은 레인도 세션도 아닌 줄 행 하나다",
			Clip(s, 64))}
	}
	if n <= 0 {
		return ActionVerdict{false, fmt.Sprintf("줄 행 번호가 %d 다 — 번호는 1부터다", n)}
	}
	return ActionVerdict{true, fmt.Sprintf("줄 행 %d 를 대상으로 읽었다", n)}
}

// JudgeDropTarget 은 그 항목을 지금 폐기해도 되는지 본다. 순수 함수다.
//
// 이미 끝난 항목을 다시 폐기하면 close_reason 이 덮이고 closed_at 이 오늘로 밀린다 —
// 즉 **이력이 조용히 거짓이 된다.** 그래서 상태를 보고 거절한다.
func JudgeDropTarget(state model.ItemState) ActionVerdict {
	switch state {
	case model.ItemOpen, model.ItemClaimed:
		return ActionVerdict{true, fmt.Sprintf("아직 열려 있다(state=%s)", state)}
	case model.ItemDone, model.ItemDropped:
		return ActionVerdict{false, fmt.Sprintf("이미 종료된 항목이다(state=%s) — 종료는 되돌리지 않는다", state)}
	default:
		return ActionVerdict{false, fmt.Sprintf("모르는 항목 상태다(state=%q) — 스키마 CHECK 와 코드가 어긋났다는 뜻이다",
			Clip(string(state), 64))}
	}
}

// NoticeText 는 리다이렉트 뒤에 띄울 알림 문장을 만든다. 순수 함수다.
//
// **코드 목록이 고정이다.** 질의 문자열의 문장을 그대로 찍으면 링크 하나로 아무 말이나
// 대시보드에 띄울 수 있게 된다(이스케이프와 별개의 축이다 — 이스케이프는 태그를 막지 문장을 안 막는다).
func NoticeText(code, item string) string {
	it := Clip(item, 120)
	switch code {
	case string(ActionReclaim):
		return fmt.Sprintf("선점을 회수했다: %s. 회수 사유는 판단(decision)으로 원장에 남았다.", it)
	case string(ActionDrop):
		return fmt.Sprintf("항목을 폐기했다: %s. 사유는 close_reason 과 판단(decision) 양쪽에 남았다.", it)
	case string(ActionLaneRelease):
		return fmt.Sprintf("랜딩 줄 행 %s 를 회수했다. 사유와 함께 **서버가 관측한 것**"+
			"(획득 경과 · 마지막 신호 나이 · 그때 줄에 있던 사람)이 판단(decision)으로 남았다.", it)
	default:
		return ""
	}
}

// laneRelease 는 랜딩 줄 행 하나를 회수한다.
//
// ★ **CLI 와 같은 함수를 부른다**(service.ReleaseLaneRow). 화면이 자기 경로를 따로
// 만들면 "회수했는데 판단이 안 남았다"거나 "CLI 로는 되는데 화면으로는 안 된다"가
// 생기고, 그 어긋남은 원장에서만 보인다 — 즉 아무도 안 볼 때 생긴다.
//
// ★ actor 를 **빈 문자열로 준다.** 그 값이 판단 본문의 "행위자: 대시보드(사람)"를
// 고르고, 그 문장은 정확히 이 경로에서만 참이다. 여기서 무언가를 채우면 불변으로
// 남는 기록에 "사람이 눌렀다"가 아닌 다른 것이 적힌다.
func (h *handler) laneRelease(w http.ResponseWriter, r *http.Request) {
	in, ok := h.formInput(w, r, ActionLaneRelease)
	if !ok {
		return
	}
	// JudgeAction 이 이미 번호로 읽었다(JudgeLaneRowID). 여기서 다시 실패할 수 없다.
	rowID, err := strconv.ParseInt(strings.TrimSpace(in.Item), 10, 64)
	if err != nil {
		h.refuse(w, in, JudgeLaneRowID(in.Item))
		return
	}

	ctx := r.Context()
	res, err := h.svc.ReleaseLaneRow(ctx, in.Project, rowID, "", in.Reason)
	if err != nil {
		h.fail(w, r, "web.lane.release", in, err)
		return
	}
	h.log.InfoContext(ctx, "랜딩 줄 행 회수", "route", "POST /actions/lane-release",
		"project", in.Project, "count", rowID, "session_id", Clip(res.SessionID, 64))
	h.back(w, r, in, ActionLaneRelease)
}

// reclaim 은 선점을 회수한다. 회수 행위 자체가 judgment(decision) 으로 남는다(설계 §4).
//
// 로직은 service.ReclaimClaim 하나다 — CLI 의 `fd claim release` 와 **같은 함수**를
// 부른다(레인 회수가 web·CLI 에서 ReleaseLaneRow 하나를 부르는 것과 같은 결선).
// 점유자·선점 나이·마지막 신호를 판단에 남기는 일도 그 함수가 한다.
func (h *handler) reclaim(w http.ResponseWriter, r *http.Request) {
	in, ok := h.formInput(w, r, ActionReclaim)
	if !ok {
		return
	}
	ctx := r.Context()
	// actor 는 빈 문자열로 준다 — 그 값이 판단 본문의 "행위자: 대시보드(사람)"를 고른다
	// (레인 회수와 같은 갈래, service 쪽 actorLine 주석 참고).
	if _, err := h.svc.ReclaimClaim(ctx, in.Project, in.Item, "", in.Reason); err != nil {
		h.fail(w, r, "web.claim.reclaim", in, err)
		return
	}
	h.log.InfoContext(ctx, "선점 회수", "route", "POST /actions/reclaim",
		"project", in.Project, "item", Clip(in.Item, 64))
	h.back(w, r, in, ActionReclaim)
}

// drop 은 항목을 폐기한다. 사유는 close_reason 과 판단 양쪽에 남는다.
func (h *handler) drop(w http.ResponseWriter, r *http.Request) {
	in, ok := h.formInput(w, r, ActionDrop)
	if !ok {
		return
	}
	st := h.svc.Store()
	ctx := r.Context()

	it, err := st.GetItem(ctx, in.Project, in.Item)
	if err != nil {
		h.fail(w, r, "web.item.drop", in, err)
		return
	}
	if v := JudgeDropTarget(it.State); !v.OK {
		h.refuse(w, in, v)
		return
	}

	body := fmt.Sprintf("대시보드에서 항목을 폐기했다.\n항목: %s (%s)\n사유: %s\n"+
		"행위자: 대시보드(사람). 세션이 아니라 사람이 누른 것이므로 session_id 는 비어 있다.",
		in.Item, Clip(it.Title, 200), in.Reason)

	err = st.Tx(ctx, func(t *store.Tx) error {
		t.LogEvent("web.item.drop", in.Project, "", map[string]any{
			"item": in.Item, "state": string(it.State),
		})
		if err := t.SetItemState(in.Project, in.Item, model.ItemDropped, in.Reason); err != nil {
			return err
		}
		_, err := t.AddJudgment(model.Judgment{
			Project: in.Project, Kind: model.JudgmentDecision,
			Title: "항목 폐기: " + in.Item, Body: body,
			Links: []model.JudgmentLink{{TargetKind: "item", TargetID: in.Item}},
		})
		return err
	})
	if err != nil {
		h.fail(w, r, "web.item.drop", in, err)
		return
	}
	h.log.InfoContext(ctx, "항목 폐기", "route", "POST /actions/drop",
		"project", in.Project, "item", Clip(in.Item, 64))
	h.back(w, r, in, ActionDrop)
}

// formInput 은 폼을 읽어 판정까지 끝낸다. 거절이면 그 자리에서 응답하고 false 를 돌려준다.
func (h *handler) formInput(w http.ResponseWriter, r *http.Request, kind ActionKind) (ActionInput, bool) {
	if err := r.ParseForm(); err != nil {
		// 4xx 라 로그 의무는 없지만 사용자에게는 사유를 준다.
		http.Error(w, "폼을 읽지 못했다: "+Clip(err.Error(), 200), http.StatusBadRequest)
		return ActionInput{}, false
	}
	in := ActionInput{
		Kind:    kind,
		Project: strings.TrimSpace(r.PostFormValue("project")),
		Item:    strings.TrimSpace(r.PostFormValue("item")),
		Reason:  strings.TrimSpace(r.PostFormValue("reason")),
	}
	if v := JudgeAction(in); !v.OK {
		h.refuse(w, in, v)
		return ActionInput{}, false
	}
	return in, true
}

// refuse 는 판정 거절을 사유째로 돌려준다.
func (h *handler) refuse(w http.ResponseWriter, in ActionInput, v ActionVerdict) {
	http.Error(w, fmt.Sprintf("%s 거절: %s\n대상: %s/%s\n뒤로 가서 고쳐 다시 보내라.",
		in.Kind, v.Reason, Clip(in.Project, 64), Clip(in.Item, 200)), http.StatusBadRequest)
}

// fail 은 쓰기 실패를 원인 전문째로 남기고 사용자에게는 고칠 거리만 준다.
//
// 응답 본문에 내부 이름(SQL·드라이버 원문)을 넣지 않는다 — 원인 전문은 로그로,
// 고칠 거리는 응답으로.
func (h *handler) fail(w http.ResponseWriter, r *http.Request, kind string, in ActionInput, err error) {
	h.log.ErrorContext(r.Context(), "대시보드 쓰기 실패",
		"route", "POST /actions/"+string(in.Kind),
		"project", in.Project, "item", Clip(in.Item, 64), "error", err.Error())
	h.svc.Store().LogEvent(r.Context(), kind+".fail", in.Project, "", map[string]any{
		"item": in.Item, "error": Clip(err.Error(), 400),
	})
	// "없는 대상"과 "서버 결함"은 처방이 다르다. 뭉개면 사용자가 서버를 의심한다.
	status := http.StatusInternalServerError
	msg := "쓰기에 실패했다. 서버 로그의 원인 전문을 보라."
	if errors.Is(err, store.ErrNotFound) {
		status = http.StatusNotFound
		msg = "대상이 없다: " + Clip(err.Error(), 300)
	}
	http.Error(w, msg, status)
}

// back 은 화면으로 되돌린다(POST-리다이렉트-GET).
//
// 상대 경로로 보낸다 — 이 핸들러가 하위 경로에 마운트돼도 그대로 성립한다.
// 알림은 **코드**로 넘기고 문장은 서버가 만든다(NoticeText).
func (h *handler) back(w http.ResponseWriter, r *http.Request, in ActionInput, code ActionKind) {
	q := url.Values{}
	q.Set("project", in.Project)
	q.Set("notice", string(code))
	q.Set("item", in.Item)
	http.Redirect(w, r, "../?"+q.Encode(), http.StatusSeeOther)
}
