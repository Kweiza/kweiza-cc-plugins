package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// project_member — 루트 프로젝트가 갖는 멤버 명부의 **캐시**다(증분 014).
//
// ★ 정본은 루트 레포에 커밋된 `.flightdeck.yaml` 이다. 이 계층은 그 파일이 말한 것을
// 그대로 적어 둘 뿐이라, 「합치기」가 없고 **통째 교체**만 있다(ReplaceWorkspaceMembers).
// 합치면 파일에서 지운 멤버가 표에 영영 남고, 그 유령은 자원 정규화와 발자국 귀속을
// 조용히 넓힌다 — 어느 화면에도 안 뜨는 채로.

// ReplaceWorkspaceMembers 는 루트의 명부를 파일이 말한 그대로 맞춘다.
//
// ★ **왜 delete-then-insert 인가.** upsert 로 더하기만 하면 파일에서 빠진 멤버를 못
// 걷는다. "빠진 것을 골라 지우기"를 하려면 지금 표의 내용을 먼저 읽어야 하는데, 그
// 읽기와 쓰기 사이가 갈리면 두 세션이 동시에 명부를 맞출 때 한쪽의 지우기가 다른 쪽의
// 넣기를 지운다. 한 트랜잭션 안의 통째 교체는 그 창이 없다.
//
// ★ **DELETE 는 이 표에만 닿는다.** project_member 는 아무도 FK 로 안 가리키므로
// (역방향만 있다 — 이 표가 project 를 가리킨다) 지워도 딸린 것이 없다.
//
// ★ **빈 목록도 정상 입력이다.** 사람이 명부를 비운 것과 파일이 없는 것은 다른 사실이고
// (judge.Workspace.Declared 가 그 축이다), 이 함수는 **선언이 있었을 때만** 불린다.
// 선언이 없으면 호출부가 아예 안 부른다 — 그래야 단일 레포 프로젝트가 안 건드려진다.
func (t *Tx) ReplaceWorkspaceMembers(root string, members []model.WorkspaceMember, at time.Time) error {
	if root == "" {
		return errors.New("루트 프로젝트 id 가 비었다")
	}
	if at.IsZero() {
		at = nowStamp()
	}
	if _, err := t.tx.ExecContext(t.ctx,
		`DELETE FROM project_member WHERE root_project = ?`, root); err != nil {
		return fmt.Errorf("멤버 명부 비우기 실패(root=%q): %w", clip(root, 64), err)
	}
	stamp := fmtTime(at)
	for _, m := range members {
		if m.Project == "" || m.Path == "" {
			// ★ 조용히 건너뛰지 않는다. 빈 값이 여기 온 것은 위 계층(judge)이 이미
			//   막았어야 하는 것이고, 통과시키면 명부에 «이름 없는 멤버»가 앉아 이후
			//   모든 조회가 그것을 프로젝트 ""로 읽는다.
			return fmt.Errorf("멤버 선언이 비었다(project=%q path=%q) — 이 값은 파싱이 막았어야 한다",
				clip(m.Project, 64), clip(m.Path, 200))
		}
		if _, err := t.tx.ExecContext(t.ctx, `
			INSERT INTO project_member(root_project, member_project, path, declared_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(root_project, member_project) DO UPDATE SET
			  path        = excluded.path,
			  declared_at = excluded.declared_at`,
			root, m.Project, m.Path, stamp); err != nil {
			return fmt.Errorf("멤버 등재 실패(root=%q member=%q): %w",
				clip(root, 64), clip(m.Project, 64), err)
		}
	}
	return nil
}

// ReplaceWorkspaceMembers 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) ReplaceWorkspaceMembers(ctx context.Context, root string,
	members []model.WorkspaceMember, at time.Time) error {
	return s.Tx(ctx, func(t *Tx) error { return t.ReplaceWorkspaceMembers(root, members, at) })
}

// WorkspaceMembers 는 루트의 멤버 명부를 낸다. 없으면 빈 슬라이스다(오류가 아니다).
//
// ★ 정렬은 member_project 다 — Roster.MemberIDs 와 같은 순서를 내려는 것이 아니라,
// 이 조회 자체가 호출마다 같은 답을 내야 하기 때문이다. 정렬 없는 목록이 화면에 그대로
// 실리면 보드 한 줄이 새로고침마다 뒤바뀐다.
func (s *Store) WorkspaceMembers(ctx context.Context, root string) ([]model.WorkspaceMember, error) {
	if root == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT member_project, path FROM project_member
		WHERE root_project = ? ORDER BY member_project`, root)
	if err != nil {
		return nil, fmt.Errorf("멤버 명부 조회 실패(root=%q): %w", clip(root, 64), err)
	}
	defer rows.Close()
	var out []model.WorkspaceMember
	for rows.Next() {
		var m model.WorkspaceMember
		if err := rows.Scan(&m.Project, &m.Path); err != nil {
			return nil, fmt.Errorf("멤버 행 읽기 실패(root=%q): %w", clip(root, 64), err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// WorkspaceRootOf 는 이 프로젝트가 **어느 워크스페이스의 멤버인가**다.
// 멤버가 아니면 ErrNotFound 다 — 그 부재가 곧 "단일 레포 프로젝트"라는 답이다.
//
// ★ **루트 자신은 여기서 안 나온다.** 루트는 명부의 행이 아니라 명부의 주인이라,
// 「나는 어느 워크스페이스에 속하나」의 답을 루트에서 물으면 빈 값이 돌아온다.
// 그래서 Roster 를 만드는 자리(service)는 **두 방향을 다 본다**: 내가 루트인가
// (WorkspaceMembers 가 비지 않았나) · 내가 멤버인가(이 함수).
//
// ★ 한 프로젝트가 두 워크스페이스의 멤버로 등재될 수 있다 — 스키마가 안 막는다(PK 는
// (root, member) 다). 그때 **가장 먼저 오는 루트 하나**를 낸다. 지어내지 않으려면
// 거절해야 할 것 같지만, 거절하면 그 프로젝트의 세션이 통째로 못 열린다 — 명부는 남의
// 레포에 커밋된 파일이라 이쪽에서 고칠 수단이 없다. 대신 **doctor 가 그 중복을 말한다**.
func (s *Store) WorkspaceRootOf(ctx context.Context, member string) (string, error) {
	if member == "" {
		return "", notFound(NFProject, "", member)
	}
	var root string
	err := s.db.QueryRowContext(ctx, `
		SELECT root_project FROM project_member
		WHERE member_project = ? ORDER BY root_project LIMIT 1`, member).Scan(&root)
	if errors.Is(err, sql.ErrNoRows) {
		return "", notFound(NFProject, "", member)
	}
	if err != nil {
		return "", fmt.Errorf("워크스페이스 역조회 실패(member=%q): %w", clip(member, 64), err)
	}
	return root, nil
}

// WorkspaceRootsOf 는 이 프로젝트를 멤버로 담은 루트 **전부**다.
//
// 위 함수가 하나만 내는 자리의 짝이다 — doctor 가 "명부가 둘에 걸쳐 있다"를 말하려면
// 셀 수 있어야 하고, 그 수를 화면이 낼 수 있어야 사람이 어느 파일을 고칠지 안다.
func (s *Store) WorkspaceRootsOf(ctx context.Context, member string) ([]string, error) {
	if member == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT root_project FROM project_member
		WHERE member_project = ? ORDER BY root_project`, member)
	if err != nil {
		return nil, fmt.Errorf("워크스페이스 역조회 실패(member=%q): %w", clip(member, 64), err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, fmt.Errorf("워크스페이스 역조회 행 읽기 실패: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetWorkspaceSHA 는 **명부를 마지막으로 읽은 커밋**을 적는다.
//
// ★ 컬럼은 project.config_from_sha 를 재사용한다. 그 칸이 원래 뜻하던 것과 같기 때문이다
// — "레포 안의 `.flightdeck.yaml` 을 어느 ref 시점에서 읽었나"(model.Project 의 주석).
// 지금까지 아무도 안 채웠고(저장·복원 경로만 있었다) 이 항목이 처음 채운다.
//
// ★ **짝인 config 칼럼은 여전히 안 채운다.** 우리가 읽는 것은 그 파일의 `workspace:`
// 블록 하나뿐이라 «파일 전체의 캐시»가 아니고, 명부의 캐시는 project_member 표가
// 정본이다. 파일 전체를 JSON 으로 접어 두면 같은 사실이 두 자리에 앉고, 두 벌은
// 반드시 표류한다. 이 칸에는 **신선도만** 산다.
//
// ★ **이 값의 용도는 「다시 읽을까」 하나다.** sha 가 그대로면 파일도 그대로이므로
// 저장소를 다시 안 친다 — 세션 열기는 훅이 프롬프트마다 무는 자리라, 여기에 무조건
// 도는 외부 프로세스를 하나 얹으면 가장 잦은 경로가 그만큼 느려진다.
// 판정 축으로는 안 쓴다.
func (t *Tx) SetWorkspaceSHA(project, sha string) error {
	if project == "" {
		return errors.New("프로젝트 id 가 비었다")
	}
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE project SET config_from_sha = ? WHERE id = ?`, nullStr(sha), project)
	if err != nil {
		return fmt.Errorf("명부 신선도 기록 실패(project=%q): %w", clip(project, 64), err)
	}
	// ★ 없는 프로젝트에 쓴 것을 조용히 넘기지 않는다. UPDATE 는 대상이 없어도 오류가
	//   아니라서, 잘못된 id 가 오면 «성공했는데 아무것도 안 바뀐» 상태가 된다 —
	//   그러면 다음 세션이 파일을 또 읽고, 그 반복은 어느 화면에도 안 뜬다.
	if n, aerr := res.RowsAffected(); aerr == nil && n == 0 {
		return notFound(NFProject, "", project)
	}
	return nil
}

// SetWorkspaceSHA 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) SetWorkspaceSHA(ctx context.Context, project, sha string) error {
	return s.Tx(ctx, func(t *Tx) error { return t.SetWorkspaceSHA(project, sha) })
}
