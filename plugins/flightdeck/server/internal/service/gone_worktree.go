package service

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 워크트리가 사라진 카드를 서버가 닫는다 — 설계 §4 「세션은 어떻게 닫히나」의 셋째 경로.
//
// ★ 판정은 judge/gone_worktree.go 에 있다. 이 파일은 관측(git 목록 · lstat · 원장)을 모아
// 넘기고, 닫기로 났을 때 쓰기 한 번을 한다.
//
// ★★ **왜 여기(카드 파생 안)서 닫나 — 후보 셋 중에서 골랐다.**
//
//	⒜ 파생 중 관측하고 그 자리에서 닫는다          ← 이것. 다만 판정(순수 함수)과 쓰기
//	                                              (카드당 트랜잭션 하나)를 가른다
//	⒝ 파생은 관측만 모으고 끝난 뒤 따로 닫는다
//	⒞ 서버 주기 작업(ledgerbackup 같은 티커)
//
//	⒝ 를 안 고른 이유: 파생이 끝난 뒤에 닫으면 방금 닫을 카드가 이미 git 파생을 다 탔다 —
//	그 카드의 미커밋 실패 두 축이 같은 응답에 남거나, 남기지 않으려면 파생 기록에서 되빼는
//	갈래가 하나 더 생긴다. 앞에서 닫으면 그 카드는 애초에 파생을 안 탄다.
//
//	⒞ 를 안 고른 이유: 이 판정의 입력인 `git worktree list` 를 카드 파생이 **이미** 돌린다
//	(sessionCardsAndRoots → worktreeIndex). 티커는 같은 목록을 프로젝트마다 따로 또 읽어야
//	하고, 지운 순간부터 다음 틱까지 유령이 그대로 보드와 겹침에 뜬다 — 이 항목이 없애려는
//	바로 그 화면이다. 그리고 그 잡은 로그 말고 관측될 자리가 없다(ledgerbackup.go 머리말이
//	같은 구멍을 스스로 적었다).
//
//	읽기 경로가 쓰기를 하는 비용은 이렇게 갚는다: ① 이 파일의 쓰기는 **조건이 다 선 카드에만**
//	돈다 — 평시 보드는 쓰기가 0이고, 추가 비용은 카드당 lstat·stat 두 번이다(git 호출은 0).
//	② 쓰기는 카드 한 장당 짧은 트랜잭션 하나이고, 판정의 안전 조건(active · 선점 0)을 UPDATE
//	한 문장 안에서 다시 건다(store.Tx.CloseSessionIfActiveUnclaimed). ③ 실패해도 조회를 안
//	죽인다 — 그 카드는 지금까지처럼 파생되어 나가고(파생 실패도 그대로 뜬다) WARN 이 남는다.
//	같은 파일이 이미 읽기 중에 관측을 쓴다(board.go 의 rememberRef·rememberChangeSet) —
//	다른 점은 이것이 상태 전이라는 것이고, 그래서 판정을 순수 함수로 떼고 원장에 남긴다.
//
//	**파생 앞에서** 닫는다. 닫은 카드는 그 조회의 카드 목록에서 빠지고 git 호출(미커밋 경로·
//	규모)을 아예 안 탄다 — 방금 닫힌 카드가 같은 응답에 `uncommitted:<세션>` 실패로 뜨면
//	그 응답은 스스로 모순된다.
//
//	카드 파생 한 자리에 두는 이유: 보드뿐 아니라 pick·note(수신자)·amend 가 같은 함수를
//	지난다(board.go 의 sessionCards·liveOverlapSessions). 유령은 보드 카드로도 뜨지만
//	**겹침 표에도** 뜨고, 겹침은 pick 응답 꼬리가 낸다 — 보드에서만 닫으면 그 표면이 남는다.

// eventSessionWorktreeGone 은 이 경로가 원장에 남기는 kind 다.
//
// ★ `session.state`(fd close·SessionEnd 훅이 PATCH 로 남기는 것)와 **다른 kind** 다.
// 사람이 나중에 "왜 이 카드가 닫혔지"를 물을 때 사람이 닫은 것과 서버가 닫은 것이 한 kind 로
// 섞이면 payload 를 뒤져야 갈린다. 상수로 두는 이유는 발화부와 한도 조회(priorAutoClose)가
// 같은 문자열을 봐야 해서다 — 리터럴 둘이면 한쪽만 고칠 때 카드당 한 번 한도가 조용히 풀린다.
const eventSessionWorktreeGone = "session.close.worktree_gone"

// GoneWorktreeClosure 는 **이 조회가** 닫은 카드 한 장이다.
type GoneWorktreeClosure struct {
	SessionID string `json:"session_id"`
	Label     string `json:"label,omitempty"`
	Worktree  string `json:"worktree"`
}

// worktreeList 는 `git worktree list` 한 번의 결과다.
//
// ★ ok 를 따로 나른다. 경로 목록이 비었는지로 대신하면 "못 읽었다"와 "목록에 없다"가 한 값이
// 되고, 그 구분이 이 경로의 첫째 안전 조건이다(judge.GoneWorktreeFacts.ListOK).
type worktreeList struct {
	paths []string
	ok    bool
}

// closeIfWorktreeGone 은 카드 한 장의 좌표가 사라졌으면 닫는다. **실제로 닫았을 때만** true.
//
// proj 는 카드의 프로젝트이고 wl 은 **그 프로젝트 저장소(proj.Path)** 에서 읽은 목록이다 —
// 카드는 ListLive(proj.ID) 에서 왔으므로 둘이 같은 저장소다. 형제 프로젝트의 카드
// (siblingLive)는 이 함수를 안 지난다: 그 카드들은 자기 프로젝트의 파생이 돌 때 자기 저장소의
// 목록과 대조된다. 남의 저장소 목록과 대조해 "없다"고 보는 자리가 원리적으로 없게 한 것이다.
func (s *Service) closeIfWorktreeGone(ctx context.Context, proj model.Project, v model.SessionView,
	self string, wl worktreeList) bool {

	tree, home := judge.GoneWorktreeCoords(v.Session.Worktree, wl.paths)
	gone := judge.WorktreeGone(judge.GoneWorktreeFacts{
		Worktree:     v.Session.Worktree,
		ListOK:       wl.ok,
		Listed:       wl.paths,
		TreePresence: lstatPresence(tree),
		HomePresence: dirPresence(home),
	})
	if !gone.Gone {
		return false
	}
	// ★ 원장 조회는 좌표가 사라진 카드에만 돈다 — 평시 카드는 이 줄에 안 온다.
	ok, why := judge.MayCloseGoneCard(judge.GoneCardGuards{
		State:  v.Session.State,
		Claims: len(v.Claims),
		IsSelf: v.Session.ID == self,
		Prior:  s.priorAutoClose(ctx, v.Session.ID),
	})
	if !ok {
		// Debug 인 이유: 조건이 풀리지 않는 한 **조회마다** 같은 줄이 나온다(선점을 든 유령 등).
		// 그 카드는 보드에 그대로 뜨고 파생 실패도 그대로 뜬다 — 화면이 이미 말하고 있다.
		s.log.DebugContext(ctx, "워크트리가 사라진 카드를 안 닫는다",
			"session_id", clip(v.Session.ID, 64), "worktree", clip(v.Session.Worktree, 200),
			"reason", why)
		return false
	}

	var closed bool
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		var e error
		if closed, e = t.CloseSessionIfActiveUnclaimed(v.Session.ID); e != nil || !closed {
			// 안 닫혔으면(그 사이 blocked·선점·닫기) 원장에 아무것도 안 남긴다 — 닫지 않은
			// 닫기를 남기면 한도 조회가 그것을 "이미 닫았다"로 센다.
			return e
		}
		t.LogEvent(eventSessionWorktreeGone, proj.ID, v.Session.ID, map[string]any{
			"worktree": clip(v.Session.Worktree, 200),
			"tree":     clip(gone.Tree, 200),
			"home":     clip(gone.Home, 200),
			"repo":     clip(proj.Path, 200),
			"machine":  clip(v.Session.MachineID, 64),
			"reason":   clip(gone.Reason, 400),
		})
		return nil
	})
	if err != nil {
		s.log.WarnContext(ctx, "워크트리가 사라진 카드를 닫으려다 실패했다 — 카드는 그대로 파생한다",
			"session_id", clip(v.Session.ID, 64), "worktree", clip(v.Session.Worktree, 200),
			"error", err.Error())
		return false
	}
	if closed {
		s.log.InfoContext(ctx, "워크트리가 git 목록에서 사라진 카드를 닫았다",
			"project", proj.ID, "session_id", v.Session.ID,
			"worktree", clip(v.Session.Worktree, 200), "home", clip(gone.Home, 200))
	}
	return closed
}

// priorAutoClose 는 이 카드가 앞서 **커밋된** 자동 닫기를 겪었는지 원장에서 읽는다.
//
// ★ 롤백된 행은 안 센다 — 예약 이벤트는 롤백 갈래에서도 흘러간다(store 의 TxOutcomeKey).
// ★ 못 읽거나 payload 를 못 풀면 Unknown 이다. alreadyLoggedProjectMismatch(session.go)는
// 같은 실패를 "억제 없이 남긴다"로 접는데 여기는 반대다 — 그쪽은 잘못 접어도 이벤트가 한 줄
// 더 남을 뿐이고, 여기는 잘못 접으면 **카드를 닫는다.**
func (s *Service) priorAutoClose(ctx context.Context, sessionID string) judge.PriorAutoClose {
	evs, err := s.st.ListSessionEvents(ctx, sessionID, eventSessionWorktreeGone, time.Time{})
	if err != nil {
		s.log.WarnContext(ctx, "앞선 자동 닫기 조회 실패 — 안 닫는다",
			"session_id", clip(sessionID, 64), "error", err.Error())
		return judge.PriorAutoCloseUnknown
	}
	for _, e := range evs {
		var p struct {
			Tx string `json:"tx"`
		}
		if json.Unmarshal([]byte(e.Payload), &p) != nil {
			s.log.WarnContext(ctx, "자동 닫기 이벤트 payload 해석 실패 — 안 닫는다",
				"session_id", clip(sessionID, 64), "payload", clip(e.Payload, 200))
			return judge.PriorAutoCloseUnknown
		}
		if p.Tx == store.TxCommitted {
			return judge.PriorAutoCloseSeen
		}
	}
	return judge.PriorAutoCloseNone
}

// lstatPresence 는 경로 하나를 서버에서 lstat 한다.
//
// ★ lstat 인 이유: 그 이름에 무엇이든(끊어진 심볼릭 링크라도) 있으면 "있다"다 — 닫는 쪽으로
// 해석할 여지를 안 남긴다. ErrNotExist 만 Absent 다. 권한·ENOTDIR·I/O 는 Unknown 이다
// (observeOne 과 같은 규율 — "못 쟀다"는 절대 "없다"가 아니다).
func lstatPresence(p string) judge.PathPresence {
	if strings.TrimSpace(p) == "" {
		return judge.PathUnknown
	}
	if _, err := os.Lstat(p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return judge.PathAbsent
		}
		return judge.PathUnknown
	}
	return judge.PathPresent
}

// dirPresence 는 경로가 서버에서 **디렉토리로** 보이는지 stat 한다.
// 디렉토리가 아니면 Unknown 이다 — 담는 워크트리가 파일로 보이는 것은 관측이 이상하다는 뜻이다.
func dirPresence(p string) judge.PathPresence {
	if strings.TrimSpace(p) == "" {
		return judge.PathUnknown
	}
	st, err := os.Stat(p)
	switch {
	case err == nil && st.IsDir():
		return judge.PathPresent
	case err != nil && errors.Is(err, fs.ErrNotExist):
		return judge.PathAbsent
	default:
		return judge.PathUnknown
	}
}
