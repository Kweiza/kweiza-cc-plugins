package mcpsrv

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 백엔드 이음매 — 이 서버가 조정 서버에서 필요로 하는 것의 **전부**.
//
// ★ 이 인터페이스가 생긴 이유는 `fd mcp` 를 REST 클라이언트로 돌리기 위해서다(설계 원칙 ③:
// "정합성 경로는 REST, MCP 는 그 위의 얇은 껍데기").
//
// 앞선 판은 여기에 *service.Service 를 직접 꽂아 **로컬 SQLite 에 직접 썼다.** 결과가 둘이었다:
//
//  1. SSE 허브는 internal/api 의 server 안에 있으므로 MCP 가 만든 변화는 **알림에 한 줄도 안 떴다.**
//     도구 전부가 에이전트의 유일한 쓰기 표면이라(설계 §6) 조정 트래픽의 대부분이 알림 축에서
//     통째로 사라졌고, 그러면 "아무 일도 없다"와 "이 경로는 안 낸다"가 구분되지 않는다.
//  2. 세션이 10개면 이 프로세스도 10개이고 전부 같은 파일에 `_txlock=immediate` 로 썼다.
//     쓰기 주체를 서버 하나로 모으면 그 경합이 사라진다.
//
// **넓히지 않는다.** 여기 있는 것은 도구 8개와 세션 귀속이 실제로 부르는 메서드뿐이다.

// Backend 는 조정 서버 한 대에 붙는 통로다.
//
// *service.Service 가 이것을 만족한다(같은 머신에서 DB 를 직접 여는 경로 — 시험이 쓴다).
// 운영 배선은 cmd/fd 의 REST 구현이다.
type Backend interface {
	OpenSession(ctx context.Context, in service.OpenSessionInput) (service.SessionResult, error)
	Beat(ctx context.Context, sessionID string, kind model.SignalKind, paths []string) error
	Board(ctx context.Context, project string, opt service.BoardOptions) (service.BoardView, error)
	Pick(ctx context.Context, in service.PickInput) (service.PickResult, error)
	Note(ctx context.Context, in service.NoteInput) (service.NoteResult, error)
	AddItem(ctx context.Context, in service.AddItemInput) (model.Item, error)
	Finish(ctx context.Context, in service.FinishInput) (service.FinishResult, error)
	Alloc(ctx context.Context, project, counter string) (int64, error)

	// SetLabels 는 이미 있는 항목의 꼬리표를 고친다. 고칠 수 있는 축은 그 하나뿐이다.
	SetLabels(ctx context.Context, in service.LabelInput) (service.LabelResult, error)

	// LeaveClaim 은 이 세션이 **자기** 선점을 놓는다(pick 의 leave 인자).
	// 회수(ReclaimClaim)는 여기 없다 — 그것은 세션의 도구가 아니라 사람의 표면이고,
	// pick 이 steal_reason 을 거절해 잠근 축이다.
	LeaveClaim(ctx context.Context, in service.LeaveInput) (service.ClaimLeaveResult, error)

	// 랜딩 레인 셋 — land 도구 하나가 인자에 따라 이 셋 중 하나를 부른다.
	// LandingLane 은 여기 없다: 보드가 레인 절을 낼 때 쓸 통로이지 land 도구가 직접 쓰지 않는다
	// (레인 전체를 낸다는 것과 "내 자리"를 묻는 것은 다른 조회다 — Task 8 이 그 자리를 잇는다).
	Land(ctx context.Context, in service.LandInput) (service.LandResult, error)
	LandReport(ctx context.Context, in service.LandReportInput) (service.LandResult, error)
	LandLeave(ctx context.Context, in service.LandLeaveInput) (service.LandResult, error)

	// RecentNotes 는 응답 꼬리에 실을 최근 ask·blocked 다. **거르지 않은 것**을 낸다 —
	// 자기 것을 빼는 판정은 표시 계층(FilterNotes)이 한다.
	RecentNotes(ctx context.Context, project string, limit int) ([]model.Judgment, error)
}

// ─────────────────────────────────────────────────────────────────────────────
// 없음(404) 의 처방
// ─────────────────────────────────────────────────────────────────────────────

// NotFoundCarrier 는 없음의 **처방까지** 실어 보내는 오류다.
//
// 이 이음매가 있는 이유: 종류별 처방표(항목 오타 · 프로젝트 미등록 · 이미 반납된 선점 …)는
// 정본 표면인 internal/api 에 있다. 그 문구를 여기에 한 벌 더 두면 두 벌이 되고,
// 두 벌은 반드시 표류한다 — 그때 어느 쪽이 참인지 말해 주는 자리가 없다.
// 그래서 REST 클라이언트가 서버 응답의 guidance 를 이 면으로 실어 올린다.
type NotFoundCarrier interface{ NotFoundGuidance() string }

// NotFoundGuidance 는 없음 하나의 처방을 고른다. 순수 함수다.
//
// 하류가 실어 보낸 것이 있으면 그것이 이긴다(종류별로 다르다). 없으면 고정 문구로 접는데,
// 그 경로는 **백엔드가 DB 를 직접 여는 배선**(시험)뿐이다 — 운영 배선은 REST 라 항상 실려 온다.
// 접었다는 사실을 문구가 스스로 말하지는 않는다: 좌설명을 덧붙이면 정상 경로의 응답까지
// 길어지는데, 이 자리는 세션이 읽는 화면이고 예산이 토큰이다.
func NotFoundGuidance(err error, project string) string {
	var c NotFoundCarrier
	if errors.As(err, &c) {
		if g := strings.TrimSpace(c.NotFoundGuidance()); g != "" {
			return clip(g, 600)
		}
	}
	return fmt.Sprintf("프로젝트 %q 가 아직 등록되지 않았다면 세션이 한 번도 안 열린 것이다 — "+
		"board 를 먼저 부르거나 훅이 도는지 확인해라.", clip(project, 64))
}

// ─────────────────────────────────────────────────────────────────────────────
// 열화(L1) — 서버 미도달일 때 백엔드가 **명시적으로 반환**하는 결과
// ─────────────────────────────────────────────────────────────────────────────

// DegradedMode 는 서버 미도달일 때 그 호출이 **실제로 무엇을 했는가**다.
//
// 넷을 가른다. 뭉개면 "캐시를 냈다"와 "쌓아 뒀다"와 "그냥 버렸다"와 "안 했다"가
// 구분되지 않아 세션이 안 된 일을 된 줄 안다(설계 §7 L1 · cmd/fd 의 OfflineMode 와 같은 축).
type DegradedMode string

const (
	// DegradedCache — 캐시된 마지막 성공 응답을 냈다. **값은 쓸 수 있고, 낡았다.**
	DegradedCache DegradedMode = "cache"
	// DegradedOutbox — 로컬에 쌓았다. 재연결 시 멱등 재생한다. 아직 서버에는 없다.
	DegradedOutbox DegradedMode = "outbox"
	// DegradedDrop — 버렸다. 다시 만들면 되는 사실이다.
	DegradedDrop DegradedMode = "drop"
	// DegradedRefuse — 하지 않았다. 배타는 서버만 보장할 수 있다.
	DegradedRefuse DegradedMode = "refuse"
	// DegradedReplay — 서버가 **이미 갖고 있던 요청**이라 첫 응답을 그대로 돌려줬다.
	//
	// 미도달이 아니다. 서버는 멀쩡했고 호출도 성공했는데 **새로 만들어진 것이 없다.**
	// 이 축을 안 가르면 도구가 "저장했다"고 말하는데 원장은 그대로다 —
	// 쓰기 중 일부가 내용 해시로 멱등 키를 만들기 때문에(같은 세션이 같은 본문을 두 번),
	// 짧은 판단(`kind=now body="계속 진행"`)에서 드물지 않게 난다.
	// 응답 문구도 판단 id 도 첫 호출과 글자 그대로 같아서, 이 표식이 없으면 판별할 길이 아예 없다.
	DegradedReplay DegradedMode = "replay"
)

// Degraded 는 조정 서버에 못 닿아 **온전히 수행되지 않은** 호출이다.
//
// ★ 오류로도 성공으로도 접지 않는다. 오류로 접으면 아웃박스에 쌓인 판단이 "사라졌다"로
// 읽히고, 성공으로 접으면 안 된 일이 된 줄로 읽힌다 — 그 둘 다 이 도구가 없애려는 사고다.
// 설계 §2: "원격 장애는 **도구가 열화 결과를 명시적으로 반환**하는 형태로만 나타난다."
//
// **Mode=cache 인 경우에만 값과 함께 온다**(값은 쓸 수 있고 낡았다). 나머지 셋은 값이 없다.
type Degraded struct {
	What   string       // 무엇을 하려 했나 — 도구/명령 이름
	Mode   DegradedMode // 무엇을 했나
	Reason string       // 왜 그 처방인가. **항상 채운다**
	Banner string       // 지금 무엇이 참인가(L1 배너). 없을 수 있다
	Cause  error        // 원인 전문. 삼키지 않는다
}

func (d *Degraded) Error() string {
	return fmt.Sprintf("%s: 조정 서버 미도달(%s) — %s", d.What, d.Mode, d.Reason)
}

func (d *Degraded) Unwrap() error { return d.Cause }

// AsDegraded 는 오류가 열화 결과인지 본다.
func AsDegraded(err error) (*Degraded, bool) {
	var d *Degraded
	if errors.As(err, &d) {
		return d, true
	}
	return nil, false
}

// DegradedUsable 은 이 처방과 함께 온 값을 써도 되는지다. 순수 함수다.
//
// cache 와 replay 가 참이다(replay 의 값은 첫 호출의 응답 그대로라 쓸 수 있다). 모르는 값은 **거짓**이다 — 새 처방이 생겼을 때 기본값을
// "써도 된다"로 두면 아무도 정하지 않은 정책이 조용히 값을 통과시킨다.
func DegradedUsable(m DegradedMode) bool { return m == DegradedCache || m == DegradedReplay }

// DegradedIsError 는 이 열화가 도구 실패(isError)인지 판정한다. 순수 함수다.
//
// 셋(cache·outbox·drop)은 실패가 아니다 — 무언가는 됐고, 무엇이 됐는지는 본문이 말한다.
// refuse 와 **모르는 처방**은 실패다: 모르는 것을 성공으로 접으면 그 순간 조용해진다.
func DegradedIsError(m DegradedMode) bool {
	switch m {
	case DegradedCache, DegradedOutbox, DegradedDrop, DegradedReplay:
		return false
	default:
		return true
	}
}

// DegradedDid 는 그 처방이 실제로 무엇을 했는지 한 마디로 낸다. 순수 함수다.
func DegradedDid(m DegradedMode) string {
	switch m {
	case DegradedCache:
		return "캐시된 마지막 응답을 냈다(**지금 사실이 아니다**)"
	case DegradedOutbox:
		return "아웃박스에 쌓았다 — 재연결 시 멱등 재생한다. **아직 서버에 없어 다른 세션은 못 본다**"
	case DegradedDrop:
		return "버렸다 — 다음 신호가 다시 만든다"
	case DegradedRefuse:
		return "**하지 않았다**"
	default:
		return fmt.Sprintf("열화 처방 %q 를 이 계층이 모른다 — 무엇을 했는지 말할 수 없다", clip(string(m), 40))
	}
}

// RenderDegraded 는 열화 결과 하나를 사람이 읽는 텍스트로 만든다. 순수 함수다.
//
// 이 문자열이 **소비자 좌표계 그 자체**다 — 서버가 죽은 날 에이전트가 읽는 것은 이것뿐이고,
// 시험은 이 함수의 출력을 단정한다.
func RenderDegraded(d *Degraded) string {
	if d == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s · 조정 서버 미도달 — %s\n", d.What, DegradedDid(d.Mode))
	fmt.Fprintf(&b, "사유: %s\n", clip(d.Reason, 600))
	if strings.TrimSpace(d.Banner) != "" {
		b.WriteString(d.Banner)
	}
	return strings.TrimRight(b.String(), "\n")
}

// ─────────────────────────────────────────────────────────────────────────────
// 꼬리에 실을 알림 고르기
// ─────────────────────────────────────────────────────────────────────────────

// FilterNotes 는 꼬리에 실을 알림을 고른다. 순수 함수다.
//
// 셋을 한다: **자기가 쓴 것을 뺀다**(자기 노트는 알림이 아니다) · 최신순으로 세운다 ·
// 상한까지 자른다. 판정을 본문에 흩어 두면 시험이 그 사본을 단정하게 되고,
// 그러면 "자기 것이 섞여 들어오는" 회귀가 조용히 샌다.
//
// self 가 비면 아무것도 빼지 않는다 — 그때는 '나'라는 좌표가 아예 없으므로
// 빼는 판정 자체가 성립하지 않는다.
func FilterNotes(all []model.Judgment, self string, limit int) []model.Judgment {
	out := make([]model.Judgment, 0, len(all))
	for _, j := range all {
		if self != "" && j.SessionID == self {
			continue
		}
		out = append(out, j)
	}
	out = SortJudgmentsNewest(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
