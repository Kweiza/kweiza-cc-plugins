package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/gitreader"
	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 워크스페이스 — 루트 프로젝트 하나가 하위 레포 여럿을 관장하는 배치의 조합 계층.
//
// 세 계층이 이 축을 나눠 진다:
//   - judge  — 커밋된 파일에서 명부를 뜯고(ParseWorkspace), 그 명부가 자원 스코프·경로
//     귀속·인자 검증을 어떻게 바꾸는지 판정한다(Roster). 전부 순수 함수다.
//   - store  — 명부를 캐시한다(project_member). 정본이 아니라 캐시라 **통째 교체**만 있다.
//   - 여기   — 파일을 언제 다시 읽을지 정하고, 루트 상대 경로를 절대경로로 푼다.
//
// ★ **이 파일이 «언제»를 진다.** 명부 자체는 파일이 정본인데, 그 파일을 매 요청마다
// 읽으면 세션 열기(훅이 프롬프트마다 문다)에 외부 프로세스가 하나 는다. 그래서 신선도
// 축을 하나 두고(project.config_from_sha) 기본 브랜치의 sha 가 그대로면 안 읽는다.

// WorkspaceConfigFile 은 명부가 사는 파일이다. 레포 루트 상대다.
//
// 설계 §8 이 정한 이름 그대로다 — 새 파일을 만들지 않는다. 이미 labels·verify·recipes 가
// 사는 자리이고, 두 번째 설정 파일이 생기면 "어느 것이 정본인가"가 또 하나의 규율이 된다.
const WorkspaceConfigFile = ".flightdeck.yaml"

// Roster 는 이 프로젝트가 속한 워크스페이스의 명부를 낸다.
//
// **두 방향을 다 본다.** 루트는 명부의 주인이라 project_member 의 행으로는 안 나오고,
// 멤버는 주인이 아니라 자기 명부를 못 갖는다:
//
//	① 내가 루트인가 — WorkspaceMembers(나) 가 비지 않았나
//	② 내가 멤버인가 — WorkspaceRootOf(나) 가 답하나
//
// 둘 다 아니면 제로값(Active()==false)이다. 그 부재가 곧 "단일 레포 프로젝트"라는 답이고,
// 그때 이 축을 읽는 모든 자리가 지금까지와 **바이트 동등**하게 돈다.
//
// ★ 캐시하지 않는다. 조회 두 번이고(둘 다 인덱스를 탄다) 캐시를 두는 순간 "언제
// 무효화하나"가 새 질문이 된다 — 명부는 커밋으로 바뀌므로 그 시점을 이 프로세스가 모른다.
func (s *Service) Roster(ctx context.Context, project string) (judge.Roster, error) {
	if strings.TrimSpace(project) == "" {
		return judge.Roster{}, nil
	}
	// ① 내가 루트인가
	mine, err := s.st.WorkspaceMembers(ctx, project)
	if err != nil {
		return judge.Roster{}, err
	}
	if len(mine) > 0 {
		return rosterOf(project, mine), nil
	}
	// ② 내가 멤버인가
	root, err := s.st.WorkspaceRootOf(ctx, project)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return judge.Roster{}, nil
		}
		return judge.Roster{}, err
	}
	siblings, err := s.st.WorkspaceMembers(ctx, root)
	if err != nil {
		return judge.Roster{}, err
	}
	return rosterOf(root, siblings), nil
}

// rosterOf 는 저장 표현을 판정 표현으로 옮긴다.
func rosterOf(root string, members []model.WorkspaceMember) judge.Roster {
	r := judge.Roster{Root: root, Members: make(map[string]string, len(members))}
	for _, m := range members {
		r.Members[m.Project] = m.Path
	}
	return r
}

// MemberRepoPath 는 멤버 프로젝트의 **절대경로**다.
//
// ★ 합치는 자리를 하나로 둔다. 명부는 루트 상대를 저장하므로(증분 014 의 근거) 절대
// 경로가 필요한 자리마다 루트 경로와 합쳐야 하는데, 그 합침이 여러 자리에 흩어지면
// 한 곳이 `filepath.Join` 을 빠뜨려도 그 사실이 「브랜치 ?(못 읽음)」 하나로만 보인다.
//
// ★ **루트 자신을 물으면 루트 경로다.** Roster.Root 는 명부의 행이 아니라 주인이라
// Members 에 없다 — 그 갈래를 여기서 접지 않으면 호출부마다 다시 판정하게 된다.
//
// ★ 멤버 프로젝트가 원장에 **자기 path 를 갖고 있어도 그것을 안 쓴다.** 그 값은 그
// 레포에서 세션이 열렸을 때 그 머신의 cwd 로 등록된 것이라, 서버가 컨테이너면 마운트
// 지점과 다를 수 있다. 명부의 상대 경로는 루트를 기준으로 하므로 **서버가 루트를 볼 수
// 있으면 멤버도 본다** — 그 함의가 doctor 의 판정 근거다.
func (s *Service) MemberRepoPath(ctx context.Context, r judge.Roster, member string) (string, error) {
	if !r.Knows(member) {
		return "", fmt.Errorf("프로젝트 %q 는 이 워크스페이스의 것이 아니다", clip(member, 64))
	}
	rootProj, err := s.st.GetProject(ctx, r.Root)
	if err != nil {
		return "", err
	}
	if member == r.Root {
		return rootProj.Path, nil
	}
	rel := r.Members[member]
	return filepath.Join(rootProj.Path, filepath.FromSlash(rel)), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 명부 갱신 — 커밋된 파일이 정본이다
// ─────────────────────────────────────────────────────────────────────────────

// refreshWorkspace 는 루트 레포의 명부를 읽어 캐시를 맞춘다.
//
// **언제 도나**: 기본 브랜치의 sha 가 마지막으로 읽은 것과 다를 때만. 같으면 파일도
// 같으므로 아무것도 안 한다 — 세션 열기가 프롬프트마다 도는 자리라는 사실이 이 조건의
// 전부다.
//
// **무엇을 안 하나**:
//   - 파일이 그 커밋에 없으면 **아무것도 안 바꾼다.** 단일 레포 프로젝트 전건이 이
//     갈래로 오고, 여기서 캐시를 비우면 «파일이 잠깐 안 읽힌 것»이 «명부를 지운 것»이 된다.
//   - `workspace:` 블록이 없어도 같다(judge.Workspace.Declared 가 그 축이다).
//   - 파싱이 실패하면 **캐시를 안 건드리고 사유를 낸다.** 반쯤 읽은 명부로 덮으면
//     자원 배타가 조용히 좁아진다.
//
// 반환은 (바꿨나, 사유)다. 사유는 언제나 채운다 — 안 바꾼 이유가 화면에 있어야
// 「등록했는데 안 붙는다」를 사람이 스스로 짚는다.
func (s *Service) refreshWorkspace(ctx context.Context, proj model.Project, g GitReader,
	headSHA string) (changed bool, detail string) {

	if proj.ID == "" || strings.TrimSpace(proj.Path) == "" {
		return false, "프로젝트 경로를 몰라 명부를 안 읽었다"
	}
	ref := strings.TrimSpace(proj.DefaultBranch)
	if ref == "" {
		ref = "main"
	}
	// ★ sha 를 모르면(브랜치를 못 읽었다) **읽는다.** 모르는 것을 "안 바뀌었다"로 접으면
	//   명부가 영영 안 갱신되는 조용한 상태가 생긴다. 비용은 갱신 한 번이다.
	if headSHA != "" && headSHA == proj.ConfigFromSHA {
		return false, "명부는 " + stampSHA(headSHA) + " 에서 읽은 그대로다(파일을 다시 안 읽었다)"
	}

	raw, err := g.FileAt(ctx, ref, WorkspaceConfigFile)
	if err != nil {
		if errors.Is(err, gitreader.ErrFileNotInRef) {
			// 정상 상태다. sha 는 기록한다 — 그래야 다음 세션이 같은 커밋에서 또 안 읽는다.
			s.stampWorkspaceSHA(ctx, proj.ID, headSHA)
			return false, WorkspaceConfigFile + " 가 " + ref + " 에 없다 — 워크스페이스가 아니다"
		}
		return false, "명부를 못 읽었다: " + clip(err.Error(), 200)
	}
	ws, perr := judge.ParseWorkspace(string(raw))
	if perr != nil {
		// ★ sha 를 **안 적는다.** 적으면 다음 세션이 "이미 읽었다"고 판정해 이 오류가
		//   한 번 뜨고 영영 조용해진다. 파일이 고쳐질 때까지 매번 말하는 것이 맞다.
		return false, WorkspaceConfigFile + " 의 workspace 블록을 못 읽었다: " + clip(perr.Error(), 200)
	}
	if !ws.Declared {
		s.stampWorkspaceSHA(ctx, proj.ID, headSHA)
		return false, WorkspaceConfigFile + " 에 workspace 블록이 없다 — 워크스페이스가 아니다"
	}

	members := make([]model.WorkspaceMember, 0, len(ws.Members))
	seen := make(map[string]bool, len(ws.Members))
	for _, m := range ws.Members {
		id := judge.MemberProjectID(m)
		// ★ 루트 자신을 멤버로 등재하면 자원 정규화가 자기 참조가 되고, 발자국 귀속이
		//   루트 경로를 다시 루트로 접는다. 경로 검증(memberPathOK)이 `.` 는 막지만
		//   **이름이 겹치는 경우**는 못 막는다 — 다른 경로인데 basename 이 같을 수 있다.
		if id == proj.ID {
			return false, fmt.Sprintf("멤버 %q 가 루트 자신과 같은 프로젝트 id 다 — 명부를 안 바꿨다", clip(id, 64))
		}
		if seen[id] {
			return false, fmt.Sprintf("멤버 프로젝트 id %q 가 두 번 나온다 — 명부를 안 바꿨다", clip(id, 64))
		}
		seen[id] = true
		members = append(members, model.WorkspaceMember{Project: id, Path: m.Path})
	}

	err = s.st.Tx(ctx, func(t *store.Tx) error {
		if err := t.ReplaceWorkspaceMembers(proj.ID, members, s.now()); err != nil {
			return err
		}
		return t.SetWorkspaceSHA(proj.ID, headSHA)
	})
	if err != nil {
		return false, "명부 저장 실패: " + clip(err.Error(), 200)
	}
	s.log.InfoContext(ctx, "워크스페이스 명부를 맞췄다",
		"project", proj.ID, "members", len(members), "ref", ref, "sha", stampSHA(headSHA))
	return true, fmt.Sprintf("명부를 %s 의 %s 에서 읽었다 — 멤버 %d건", ref, stampSHA(headSHA), len(members))
}

// stampWorkspaceSHA 는 "이 커밋은 봤다"만 적는다. 실패해도 진행한다 —
// 못 적으면 다음에 한 번 더 읽을 뿐이고, 그 손실은 세션을 막을 값이 아니다.
func (s *Service) stampWorkspaceSHA(ctx context.Context, project, sha string) {
	if sha == "" {
		return
	}
	if err := s.st.SetWorkspaceSHA(ctx, project, sha); err != nil {
		s.log.WarnContext(ctx, "명부 신선도 기록 실패", "project", clip(project, 64), "error", err.Error())
	}
}

// stampSHA 는 명부 신선도를 화면에 찍을 때 쓰는 표기다.
//
// ★ shortSHA(doctor.go)를 감싼다 — 그 함수는 빈 문자열을 빈 문자열로 내는데, 이 축에서는
// 「아직 못 읽었다」와 「빈 sha 라는 값」이 화면에서 갈려야 한다. 없는 것을 있는 척하지
// 않는 규율이라, 자리표시자를 여기 한 자리에서 붙인다.
func stampSHA(sha string) string {
	if strings.TrimSpace(sha) == "" {
		return "?"
	}
	return shortSHA(sha)
}

// ResolveWorkspaceProject 는 `project` 인자를 해석한다 — **명부의 관문**이다.
//
// self 는 이 세션의 프로젝트, explicit 은 인자로 온 값이다. 비거나 자기 자신이면 지금과
// 같다(워크스페이스가 아닌 프로젝트 전건이 이 갈래로 온다). 다르면 **명부 안이어야만**
// 통과한다.
//
// ★ **왜 거절인가 — 자동 등록이 아니라.** 프로젝트는 미등록 이름이 오면 자동으로
// 생기는 축이고(service.OpenSession 의 자동 등록), 그래서 오타 하나가 조용히 유령
// 프로젝트를 만든다. 원장 실측으로 그 유령이 셋 있었고 두 달 뒤에야 경위를 알았다.
// 이 인자는 **사람이 손으로 적는 자리**라 오타 확률이 가장 높은 축이므로, 여기만은
// 「모르는 이름 = 거절」이다. fd move 가 대상 프로젝트를 검증하는 것과 같은 결이다.
//
// ★ **워크스페이스가 아닌데 남의 프로젝트를 지목하면 거절한다.** 명부가 없으면
// Knows 는 자기 자신에게도 false 라, 아래 self 단축이 그 갈래를 먼저 받는다 —
// 즉 「워크스페이스가 아니면 project 인자는 자기 이름만 받는다」가 된다. 그것이 맞다:
// 명부 없이 남의 프로젝트에 쓰게 하면 이 인자가 곧 프로젝트 경계를 지우는 문이 된다.
//
// ★ note 의 item_project 와 **다른 함수인 이유.** 그쪽은 「이 항목이 어디 있나」를
// **찾는** 축이라 못 찾으면 서버가 탐색한다(resolveItemProject). 이쪽은 「어디에 쓸까」를
// **지목하는** 축이라 탐색이 없다 — 없는 곳에 쓰면 만들어지기 때문이다. 두 축을 한
// 함수로 합치면 그 비대칭이 사라지고, 사라진 쪽은 언제나 관대한 쪽이다.
func (s *Service) ResolveWorkspaceProject(ctx context.Context, self, explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit == "" || explicit == self {
		return self, nil
	}
	r, err := s.Roster(ctx, self)
	if err != nil {
		return "", err
	}
	if !r.Knows(explicit) {
		reason := fmt.Sprintf("프로젝트 %q 는 이 워크스페이스의 것이 아니다", clip(explicit, 64))
		guidance := "project 인자는 워크스페이스 멤버(또는 루트 자신)만 받는다. " +
			"이름을 확인해라 — 없는 이름을 통과시키면 오타가 새 프로젝트를 만든다."
		if !r.Active() {
			reason = fmt.Sprintf("프로젝트 %q 를 지목했는데 %q 는 워크스페이스가 아니다",
				clip(explicit, 64), clip(self, 64))
			guidance = "project 인자는 워크스페이스 안에서만 쓴다. 루트 레포의 " +
				WorkspaceConfigFile + " 에 workspace 블록이 있어야 하고, 그 명부는 커밋돼야 읽힌다."
		} else {
			guidance += " 이 워크스페이스의 이름: " + strings.Join(append([]string{r.Root}, r.MemberIDs()...), "·")
		}
		return "", &RefusedError{What: "project", Reason: reason, Guidance: guidance}
	}
	return explicit, nil
}

// GateTargetProject 는 **이 세션이 저 프로젝트에 쓸 수 있나**를 본다.
//
// ★ **여기가 유일한 관문이다.** MCP·REST·CLI 세 표면이 전부 이 함수를 지나므로, 명부를
// 아는 목록을 표면마다 들고 있을 필요가 없다 — 그 사본은 반드시 표류하고, 표류한 관문은
// 오타를 통과시키거나 멀쩡한 이름을 막는다. 둘 다 조용하다.
//
// ★ **세션 id 로 «나»를 읽는다.** 요청이 말하는 project 는 이제 **대상**이라 그것으로는
// 요청자가 누구인지 알 수 없다. 세션 행의 project 가 그 답이고, 그 값은 세션이 처음
// 열릴 때 자기 cwd 에서 등록한 것이다 — 클라이언트가 이번 호출에 무엇을 보냈든 무관하다.
//
// ★ **세션 id 가 없으면 통과시킨다.** 세션 없이 도는 경로가 실재하고(CLI 의 일부·서버
// 내부 호출), 그 경로를 여기서 막으면 이 항목이 **새 회귀를 만든다**. 그 경로에서
// project 는 언제나 클라이언트 cwd 에서 온 자기 것이라 넘을 경계가 없다.
//
// ★ **읽기에도 건다.** 오타가 프로젝트를 만드는 것은 쓰기뿐이지만, 남의 프로젝트 보드를
// 읽는 것 자체가 경계를 넘는 일이다. 그리고 읽기만 열어 두면 「board 는 되는데 pick 은
// 안 된다」가 되어, 규칙이 도구마다 다른 것으로 읽힌다.
func (s *Service) GateTargetProject(ctx context.Context, sessionID, target string) error {
	target = strings.TrimSpace(target)
	if sessionID == "" || target == "" {
		return nil
	}
	sess, _, err := s.SessionByID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// 세션을 모르면 이 축을 판정할 근거가 없다. 막지 않는다 — 근거 없이 막는 것은
			// 지어내는 것과 같고, 여기서 막으면 세션 행이 아직 안 보이는 창에서 도구가
			// 통째로 죽는다.
			return nil
		}
		return err
	}
	if sess.Project == target {
		return nil
	}
	// ★★ **원장에 없는 이름은 여기서 안 막는다 — 하위 계층의 404 가 나가게 둔다.**
	//
	// 막았더니 회귀가 났다(시험 TestNotFoundCarriesWhatWasMissing): 없는 프로젝트를
	// 물으면 지금까지 404 「그런 프로젝트가 없다」였는데 이 관문이 400 「워크스페이스가
	// 아니다」로 덮었다. 두 문장은 사람이 할 일이 다르다 — 앞은 이름을 고치는 것이고
	// 뒤는 명부를 고치는 것이다.
	//
	// 그리고 이 관문이 실제로 지키는 것은 **존재하는 남의 프로젝트에 쓰는 것**이다.
	// 없는 이름은 이미 하위가 막는다: 항목·선점·자원은 전부 project 를 FK 로 가리키고,
	// 자동 등록을 하는 문은 세션 열기 하나뿐이다(그 문은 이 인자를 안 받는다).
	// 즉 「오타가 조용히 새 프로젝트를 만든다」는 이 인자에 대해서는 성립하지 않는다 —
	// fd move 가 대상 프로젝트의 **존재**를 검증하는 것과 같은 자리, 같은 이유다.
	if _, gerr := s.st.GetProject(ctx, target); gerr != nil {
		if errors.Is(gerr, store.ErrNotFound) {
			return nil
		}
		return gerr
	}
	_, err = s.ResolveWorkspaceProject(ctx, sess.Project, target)
	return err
}

// WorkspaceQueue 는 형제 프로젝트 하나의 큐 미리보기다.
//
// ★ 적격 판정이 **안 들어 있다**. 그 판정의 부재가 이 타입의 계약이고, 렌더가 그것을
// 문장으로 말한다 — 안 말하면 읽는 쪽이 이 줄을 추천으로 읽고, 선행이 안 풀린 항목을
// 집으러 간다. PickResult.WorkspaceQueues 의 머리말에 그 판정의 전문이 있다.
type WorkspaceQueue struct {
	Project string `json:"project"`
	Open    int    `json:"open"`
	// Oldest 는 열린 항목 중 가장 오래된 **비티클러**다. 없으면 nil이다.
	//
	// ★ 티클러를 빼는 이유는 굶김 축과 같다(설계 §8) — 기한까지 늙는 것이 정상인 항목이
	// 「가장 오래된 것」자리를 영구히 차지하면 이 줄의 판별력이 0이 된다.
	Oldest *model.Item `json:"oldest,omitempty"`
	// Ticklers 는 그 큐의 티클러 수다. 0을 안 접는다 — 「열림 7건」의 대부분이 티클러면
	// 그 레포는 붐비는 것이 아니라 **기다리는** 것이고, 그 차이가 어디로 갈지를 바꾼다.
	Ticklers int    `json:"ticklers"`
	Detail   string `json:"detail,omitempty"`
}

// workspaceQueues 는 형제 프로젝트들의 큐를 훑는다. **저장소를 안 친다.**
func (s *Service) workspaceQueues(ctx context.Context, r judge.Roster, self string) []WorkspaceQueue {
	if !r.Active() {
		return nil
	}
	names := make([]string, 0, len(r.Members)+1)
	if r.Root != self {
		names = append(names, r.Root)
	}
	for _, id := range r.MemberIDs() {
		if id != self {
			names = append(names, id)
		}
	}
	if len(names) > MaxMemberSummaries {
		names = names[:MaxMemberSummaries]
	}
	out := make([]WorkspaceQueue, 0, len(names))
	for _, id := range names {
		q := WorkspaceQueue{Project: id}
		items, err := s.st.ListOpen(ctx, id)
		if err != nil {
			q.Detail = "큐를 못 읽었다: " + clip(err.Error(), 160)
			out = append(out, q)
			continue
		}
		q.Open = len(items)
		for i := range items {
			if judge.IsTickler(items[i].Labels) {
				q.Ticklers++
				continue
			}
			// ListOpen 이 어떤 순서를 내든 여기서 **만든 시각으로** 고른다 — 순서에
			// 기대면 그 질의의 ORDER BY 가 바뀌는 날 이 줄이 조용히 다른 것을 가리킨다.
			if q.Oldest == nil || items[i].CreatedAt.Before(q.Oldest.CreatedAt) {
				q.Oldest = &items[i]
			}
		}
		out = append(out, q)
	}
	return out
}

// siblingLive 는 형제 프로젝트의 살아 있는 세션을 **이 프로젝트의 좌표로** 낸다.
//
// ★ **저장소를 안 친다.** 발자국은 원장에 있고(footprint 표), 이 함수가 하는 일은
// 그것을 좌표 변환해 옮기는 것뿐이다. 규모(Delta)를 안 채우는 근거는
// BoardView.SiblingLive 의 머리말에 있다.
//
// ★ **옮길 수 없는 경로는 버린다.** 다른 멤버의 파일은 이 프로젝트에서 원리적으로 못
// 만지므로 겹칠 수 없다 — 억지로 문자열을 맞추면 서로 무관한 두 레포의 같은 상대경로가
// 겹침으로 뜨고, 그 오탐이 쌓이면 이 축 전체가 무시된다(judge.Roster.PathAsSeenFrom).
//
// ★ **경로가 하나도 안 남은 세션은 아예 안 싣는다.** 빈 Paths 를 실으면 겹침 계산이
// 그 세션을 매번 훑고 언제나 0쌍을 낸다 — 비용만 있고 답은 없다.
func (s *Service) siblingLive(ctx context.Context, r judge.Roster, self string,
	cut time.Time, d *derive) []judge.LiveSession {

	if !r.Active() {
		return nil
	}
	names := make([]string, 0, len(r.Members)+1)
	if r.Root != self {
		names = append(names, r.Root)
	}
	for _, id := range r.MemberIDs() {
		if id != self {
			names = append(names, id)
		}
	}
	var out []judge.LiveSession
	for _, id := range names {
		views, err := s.st.ListLive(ctx, id, cut)
		if err != nil {
			d.fail("sibling-live:"+id, err)
			continue
		}
		for _, v := range views {
			paths := make([]string, 0, len(v.Paths))
			for _, p := range v.Paths {
				if moved, ok := r.PathAsSeenFrom(id, self, p); ok {
					paths = append(paths, moved)
				}
			}
			if len(paths) == 0 {
				continue
			}
			out = append(out, judge.LiveSession{
				ID: v.Session.ID, Label: v.Session.Label, Paths: paths,
				CCSessionID: v.Session.CCSessionID,
			})
		}
	}
	return out
}

// MaxMemberSummaries 는 보드가 한 번에 내는 멤버 요약의 상한이다.
//
// ★ 상한을 넘으면 **자르되 그 사실을 말한다**(BoardView.MembersOmitted). 조용히 자르면
// 20번째 레포에서 도는 세션이 화면에서 통째로 사라지고, 그 부재는 아무 표시도 안 남긴다.
// 값이 12인 근거: 이 배치의 실제 규모가 17+2 이고(context-platform v5.2), 그중 붐비는
// 레포는 한 번에 서넛이다. 상한이 전체보다 크면 상한이 아니고, 서넛보다 작으면 매번
// 잘린다 — 그 사이에서 «한 화면에 들어가는 줄 수»로 골랐다.
const MaxMemberSummaries = 12

// memberSummaries 는 멤버 프로젝트들의 한 줄 요약을 낸다.
//
// ★ **저장소를 안 친다.** MemberSummary 의 머리말이 그 판정의 전문이다.
//
// ★ **순서는 명부의 정렬 순서다**(Roster.MemberIDs). 붐비는 순으로 정렬하고 싶어지지만
// 그러면 같은 화면이 호출마다 뒤바뀌어 사람이 «어제 그 줄»을 못 찾는다. 상한에 걸려
// 잘리는 대상도 그때그때 달라진다 — 자르는 기준이 흔들리면 자른 사실을 말해도 소용없다.
func (s *Service) memberSummaries(ctx context.Context, r judge.Roster, self string,
	cut time.Time, d *derive) ([]MemberSummary, int) {

	if !r.Active() {
		// ★ nil 이 아니라 **빈 슬라이스**다. 호출부가 "물었는데 멤버가 없다"와 "안 물었다"를
		//   가르는 축이 그것이다(BoardView.Members 의 주석).
		return []MemberSummary{}, 0
	}
	ids := r.MemberIDs()
	// 루트에서 물으면 멤버 전부, 멤버에서 물으면 **자기를 뺀 형제**를 낸다 —
	// 자기 요약은 이 응답의 본문(세션 카드·큐)이 이미 낸다.
	kept := make([]string, 0, len(ids)+1)
	if r.Root != self {
		kept = append(kept, r.Root) // 멤버가 물으면 루트도 형제다
	}
	for _, id := range ids {
		if id != self {
			kept = append(kept, id)
		}
	}
	omitted := 0
	if len(kept) > MaxMemberSummaries {
		omitted = len(kept) - MaxMemberSummaries
		kept = kept[:MaxMemberSummaries]
	}

	out := make([]MemberSummary, 0, len(kept))
	for _, id := range kept {
		m := MemberSummary{Project: id}
		var notes []string
		if live, err := s.st.ListLive(ctx, id, cut); err != nil {
			notes = append(notes, "세션을 못 셌다: "+clip(err.Error(), 120))
		} else {
			m.Sessions = len(live)
			for _, v := range live {
				m.Claims += len(v.Claims)
			}
		}
		if n, err := s.st.CountOpen(ctx, id); err != nil {
			notes = append(notes, "큐를 못 셌다: "+clip(err.Error(), 120))
		} else {
			m.OpenItems = n
		}
		if held, err := s.st.ListHeld(ctx, id); err != nil {
			notes = append(notes, "자원을 못 읽었다: "+clip(err.Error(), 120))
		} else {
			for _, h := range held {
				m.Held = append(m.Held, h.Resource)
			}
		}
		if len(notes) > 0 {
			m.Detail = strings.Join(notes, " · ")
			d.note("member-summary:"+id, m.Detail)
		}
		out = append(out, m)
	}
	return out, omitted
}

// ─────────────────────────────────────────────────────────────────────────────
// 진단 — 선언한 멤버가 실제로 읽히나
// ─────────────────────────────────────────────────────────────────────────────

// workspaceAxes 는 명부가 선언한 멤버들을 **실제로 재서** 낸다.
//
// ★ 재는 것 넷, 그리고 각각이 없으면 무엇이 깨지나:
//
//	① 경로가 실재하는 저장소인가 — 아니면 그 멤버의 브랜치·sha 가 영영 「?(못 읽음)」이다.
//	   컨테이너로 띄웠으면 FD_REPOS 마운트 밖인 것이 가장 흔한 원인이고, 그때 healthz 는
//	   내내 ok 다(그 침묵이 이 축을 만든 이유다).
//	② 원장에 프로젝트로 등록됐는가 — 아직 아니면 그 레포에서 세션이 한 번도 안 열린 것이다.
//	   **결함이 아니다.** 다만 그 상태에서는 그 멤버의 큐·선점이 없다는 것을 사람이 알아야 한다.
//	③ 선언한 id 와 경로의 마지막 마디가 갈리는가 — 갈리면 그 레포에서 띄운 세션이 스스로
//	   등록하는 이름(cwd 의 basename)과 명부의 이름이 달라, **같은 레포가 프로젝트 둘**이 된다.
//	④ 같은 멤버가 두 루트에 등재됐는가 — 스키마가 안 막는다. 그때 자원 스코프가 어느
//	   워크스페이스의 것인지 서버가 임의로 고르므로(WorkspaceRootOf 는 하나만 낸다),
//	   그 임의성을 화면이 말해야 한다.
//
// ★ **읽기만 한다.** 여기서 명부를 갱신하지 않는다 — doctor 는 재는 자리이고, 재는 김에
// 고치면 "진단을 돌렸더니 상태가 바뀌었다"가 된다. 명부 갱신은 세션 열기 하나가 진다.
func (s *Service) workspaceAxes(ctx context.Context, projects []model.Project) []WorkspaceAxis {
	var out []WorkspaceAxis
	for _, root := range projects {
		members, err := s.st.WorkspaceMembers(ctx, root.ID)
		if err != nil {
			s.log.WarnContext(ctx, "진단: 멤버 명부 조회 실패", "project", clip(root.ID, 64), "error", err.Error())
			continue
		}
		for _, m := range members {
			ax := WorkspaceAxis{
				Root:   root.ID,
				Member: m.Project,
				Path:   filepath.Join(root.Path, filepath.FromSlash(m.Path)),
			}
			var notes []string

			// ① 실재하는 저장소인가
			if r, err := s.git(ax.Path).Ref(ctx, "HEAD"); err != nil {
				notes = append(notes, "HEAD 를 못 읽었다: "+clip(err.Error(), 200))
			} else {
				ax.Readable = true
				notes = append(notes, "HEAD="+shortSHA(r.SHA))
			}

			// ② 원장에 있나
			if _, err := s.st.GetProject(ctx, m.Project); err == nil {
				ax.Registered = true
			} else if errors.Is(err, store.ErrNotFound) {
				notes = append(notes, "원장에 아직 없다 — 그 레포에서 세션이 한 번도 안 열렸다(결함이 아니다)")
			} else {
				notes = append(notes, "프로젝트 조회 실패: "+clip(err.Error(), 200))
			}

			// ③ 이름이 갈리나
			if base := judge.ProjectIDFromPath(m.Path); base != m.Project {
				notes = append(notes, fmt.Sprintf(
					"명부의 id(%s)와 경로의 마지막 마디(%s)가 다르다 — 그 레포에서 띄운 세션은 %s 로 등록한다",
					clip(m.Project, 64), clip(base, 64), clip(base, 64)))
			}

			// ④ 두 루트에 걸쳐 있나
			if roots, err := s.st.WorkspaceRootsOf(ctx, m.Project); err == nil && len(roots) > 1 {
				notes = append(notes, "루트 "+strings.Join(roots, "·")+" 에 함께 등재돼 있다 — 자원 스코프는 그중 하나만 쓴다")
			}

			ax.Detail = strings.Join(notes, " · ")
			out = append(out, ax)
		}
	}
	return out
}

// refSHA 는 관측한 ref 목록에서 이름 하나의 sha 를 찾는다. 없으면 빈 문자열이다.
//
// ★ 이 함수가 있는 이유는 **호출을 안 늘리기 위해서다.** 세션 열기는 이미 Refs() 를
// 한 번 돌리므로 기본 브랜치의 sha 가 그 결과 안에 있다. 따로 Ref() 를 부르면 가장 잦은
// 경로에 프로세스가 하나 는다 — resolveProject 가 두 값을 한 번에 받는 것과 같은 논거다.
func refSHA(refs []model.RefState, name string) string {
	for _, r := range refs {
		if r.Ref == name {
			return r.SHA
		}
	}
	return ""
}
