package judge

import (
	"path"
	"strings"
)

// 워크스페이스 판정 — 명부가 있을 때 무엇이 달라지나.
//
// ★ workspace.go 와 나뉜 이유: 그쪽은 **읽기**(커밋된 파일 한 블록을 문자열에서 뜯는
// 일)이고 여기는 **판정**(그 명부가 자원 스코프·경로 귀속·인자 검증을 어떻게 바꾸나)이다.
// 한 파일에 두면 파서를 고치러 온 사람이 판정까지 읽어야 하고, 그 반대도 같다.
// 두 축의 시험 파일도 그래서 갈린다.

// Roster 는 서버가 들고 다니는 명부 한 벌이다.
//
// ★ **루트 자신이 Root 에 들어간다.** 「이 프로젝트가 워크스페이스에 속하나」와
// 「그 워크스페이스의 루트가 누구냐」는 한 질문이고, 둘을 따로 나르면 호출부마다
// 루트를 멤버 목록에 넣을지 말지를 다시 판정하게 된다 — 그 판정이 갈리는 순간
// 자원 정규화가 루트와 멤버에 서로 다른 스코프를 준다.
//
// Members 의 키는 프로젝트 id, 값은 **루트 상대** 경로다. 절대경로로 바꾸는 것은
// 루트 경로를 아는 계층(service)의 몫이다 — 이 패키지는 순수 함수만 둔다.
type Roster struct {
	Root    string            // 루트 프로젝트 id. 비면 워크스페이스가 아니다
	Members map[string]string // 멤버 프로젝트 id → 루트 상대 경로
}

// Active 는 이 명부가 무언가를 바꾸는가다.
//
// ★ 멤버가 0건이면 **아무것도 안 바꾼다**. 루트만 있는 워크스페이스는 단일 레포와
// 구별할 실익이 없고, 그 상태에서 자원 스코프를 «정규화»하면 자기 자신으로 접는
// 항등 연산이 코드 경로만 하나 늘린다.
func (r Roster) Active() bool { return r.Root != "" && len(r.Members) > 0 }

// Knows 는 프로젝트 id 가 이 워크스페이스의 것인가다(루트 자신도 참).
//
// ★ 이것이 `project` 인자의 관문이다(fd-ws-mcp-project-arg). 오타가 조용히 새
// 프로젝트를 만드는 것을 막는 유일한 자리라, **모르는 이름은 거절**이지 자동 등록이
// 아니다 — service.MoveItem 이 대상 프로젝트를 검증하는 것과 같은 결이다.
func (r Roster) Knows(project string) bool {
	if project == "" {
		return false
	}
	if project == r.Root {
		return true
	}
	_, ok := r.Members[project]
	return ok
}

// MemberIDs 는 멤버 프로젝트 id 를 정렬해서 낸다(루트는 안 낀다).
//
// 정렬하는 이유는 화면과 시험이 **같은 입력에 같은 순서**를 봐야 해서다. Go 의 map
// 순회는 의도적으로 무작위라, 정렬 없이 내면 보드 한 줄이 호출마다 뒤바뀐다.
func (r Roster) MemberIDs() []string {
	out := make([]string, 0, len(r.Members))
	for id := range r.Members {
		out = append(out, id)
	}
	sortStrings(out)
	return out
}

// AttributePath 는 **루트 상대** 경로가 어느 멤버의 것인지 가른다. 순수 함수다.
//
// 반환은 (멤버 프로젝트, 그 멤버 상대 경로, 멤버인가)다. 멤버 밖이면 ok=false 이고,
// 그때 호출부는 지금까지 하던 대로 루트 프로젝트에 남긴다(루트 레포 자기 파일이다).
//
// ★ **마디 경계로만 자른다.** 접두 문자열 비교(strings.HasPrefix 한 번)로 자르면
// `cp-search` 라는 멤버가 `cp-search-api/foo.go` 를 삼킨다 — 그 오귀속은 겹침 경고가
// 엉뚱한 프로젝트에 뜨는 것으로만 나타나고, 뜬 쪽은 자기 경로가 아니라서 무시한다.
//
// ★ **가장 긴 멤버가 이긴다.** 명부가 `a` 와 `a/b` 를 둘 다 담을 수 있고(막을 이유가
// 없다 — 중첩 git 은 실재한다), 그때 `a/b/x.go` 의 집은 `a/b` 다. 짧은 쪽을 고르면
// 안쪽 레포의 파일이 바깥 레포 좌표로 남아 두 좌표계가 다시 생긴다.
func (r Roster) AttributePath(rel string) (project, memberRel string, ok bool) {
	rel = strings.TrimPrefix(path.Clean(strings.ReplaceAll(strings.TrimSpace(rel), "\\", "/")), "./")
	if rel == "" || rel == "." || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "..") {
		return "", "", false
	}
	best := -1
	for id, mp := range r.Members {
		mp = strings.Trim(path.Clean(mp), "/")
		if mp == "" || mp == "." {
			continue
		}
		if rel != mp && !strings.HasPrefix(rel, mp+"/") {
			continue
		}
		if len(mp) <= best {
			continue
		}
		best = len(mp)
		project = id
		memberRel = strings.TrimPrefix(strings.TrimPrefix(rel, mp), "/")
	}
	if best < 0 {
		return "", "", false
	}
	// ★ 멤버 루트 자신을 가리키는 발자국(`cp-search-api` 딱 그것)은 상대 경로가 빈
	//   문자열이 된다. 그 값을 그대로 저장하면 «경로가 없는 발자국»이 되어 겹침 판정이
	//   전부와 맞거나 아무와도 안 맞는다 — 어느 쪽인지도 화면에 안 뜬다. 안 접는다.
	if memberRel == "" {
		return "", "", false
	}
	return project, memberRel, true
}

// PathAsSeenFrom 은 src 프로젝트 세션의 상대 경로를 dst 프로젝트의 좌표로 옮긴다.
// 순수 함수다. 옮길 수 없으면 ok=false 다.
//
// ★★ **이 함수가 없으면 겹침 경고가 원리적으로 안 난다.** 루트에서 띄운 세션이
// `member-a/server/foo.go` 를 고치고 그 레포 안에서 띄운 세션이 `server/foo.go` 를
// 고치면, 같은 파일인데 문자열이 달라 어떤 비교도 둘을 못 잇는다 — 그 침묵은
// 「겹침 없음」과 구별되지 않는다.
//
// 네 갈래다:
//
//	같은 프로젝트          → 그대로(워크스페이스가 아닌 전건이 이 갈래다)
//	멤버 → 루트            → 멤버 경로를 앞에 붙인다
//	루트 → 멤버            → 그 멤버의 접두를 벗긴다. 접두가 아니면 **겹칠 수 없다**
//	멤버 A → 멤버 B        → **언제나 겹칠 수 없다**(다른 레포의 다른 파일이다)
//
// ★ 마지막 둘에서 ok=false 를 내는 것이 판정이다. 억지로 문자열을 맞추면 서로 무관한
// 두 레포의 `server/foo.go` 가 겹침으로 뜨고, 그 오탐이 쌓이면 이 축 전체가 무시된다.
func (r Roster) PathAsSeenFrom(srcProject, dstProject, rel string) (string, bool) {
	rel = strings.TrimPrefix(strings.TrimSpace(rel), "./")
	if rel == "" {
		return "", false
	}
	if srcProject == dstProject {
		return rel, true
	}
	if !r.Active() || !r.Knows(srcProject) || !r.Knows(dstProject) {
		return "", false
	}
	switch {
	case dstProject == r.Root: // 멤버 → 루트
		mp := strings.Trim(path.Clean(r.Members[srcProject]), "/")
		if mp == "" || mp == "." {
			return "", false
		}
		return mp + "/" + rel, true
	case srcProject == r.Root: // 루트 → 멤버
		mp := strings.Trim(path.Clean(r.Members[dstProject]), "/")
		if mp == "" || mp == "." || !strings.HasPrefix(rel, mp+"/") {
			return "", false
		}
		return strings.TrimPrefix(rel, mp+"/"), true
	default: // 멤버 → 다른 멤버
		return "", false
	}
}

// LandingResource 는 랜딩 레인의 예약된 자원 이름이다.
//
// ★ 이 이름은 store·service 의 랜딩 축이 이미 쓰고 있고, 여기서는 **정규화에서 빼기
// 위해** 다시 부른다. 상수를 이 패키지에 둔 이유는 ScopeResource 가 순수 함수여야
// 해서다(§12 — 판정은 judge 에 있고 시험이 그것을 부른다).
const LandingResource = "landing"

// ScopeResource 는 자원 하나가 설 줄이 **어느 프로젝트 스코프**인가다. 순수 함수다.
//
// 배타는 부분 유니크 인덱스 `resource_one_holder(project, resource)` 가 잡는다. 그래서
// 스코프를 넓히는 유일한 방법은 그 키의 `project` 자리에 **루트 id** 를 넣는 것이다 —
// 자원 행이 «누가 잡았나»(세션)를 잃지 않으면서 배타만 워크스페이스로 넓어진다.
//
// ★ **`landing` 은 예외다.** 랜딩 레인이 지키는 명제는 「병합 시점에 origin/main 이 내
// HEAD 의 조상」이고 그것은 **레포마다 별개**다. 멤버 17개가 동시에 자기 레포로 랜딩하는
// 것은 정상이고, 여기서 접으면 그 정상 동작이 17배 직렬화된다.
//
// ★ **`path:` 도 예외다.** 이 축이 가리키는 것은 파일이고, 파일은 멤버 레포 안에서 이미
// 유일하다 — 두 멤버의 `path:server/foo.go` 는 **다른 파일**이라 같은 줄에 세우면 서로
// 무관한 두 세션이 기다린다. (루트 상대로 풀어 같은 파일인지 보는 축은 이 함수 밖이다:
// 그 값을 알려면 멤버 경로가 필요하고 그것은 Roster 를 아는 자리의 일이다. **오늘은 안
// 한다** — 원장의 `path:` 자원 사용이 0건이라 정규화할 대상이 없고, 없는 수요에 맞춰
// 지은 축은 첫 사용자가 나타날 때 이미 틀려 있다.)
func ScopeResource(resource, project, root string) string {
	if root == "" || project == "" {
		return project
	}
	r := strings.TrimSpace(resource)
	if r == LandingResource || strings.HasPrefix(r, "path:") {
		return project
	}
	return root
}

// ScopeSet 은 자원 **집합 하나**가 설 스코프다. 순수 함수다.
//
// 줄 행(landing_queue)은 자원 집합 하나에 행 하나라, 그 행이 어느 프로젝트의 큐에
// 들어가는지는 **집합 전체**의 질문이다. 그런데 ScopeResource 는 자원마다 답이 달라질 수
// 있다 — `landing` 은 레포별이고 `env:dell` 은 워크스페이스 하나다.
//
// ★ **섞이면 안 접는다. 이름을 대고 거절할 재료를 낸다.** 접는 방법이 둘 다 틀리기
// 때문이다: 루트로 접으면 멤버 17개의 랜딩이 한 줄로 직렬화되고(랜딩 명제는 레포마다
// 별개다), 멤버로 접으면 스테이징 배타가 워크스페이스 밖으로 새어 두 레포가 같은
// 클러스터에 동시에 반입한다. 둘 다 조용하다.
//
// 반환은 (스코프, 갈린 자원 이름들)이다. mixed 가 비면 scope 가 답이다.
func ScopeSet(resources []string, project, root string) (scope string, mixed []string) {
	if len(resources) == 0 {
		return project, nil
	}
	scope = ScopeResource(resources[0], project, root)
	for _, r := range resources[1:] {
		if s := ScopeResource(r, project, root); s != scope {
			mixed = append(mixed, r)
		}
	}
	if len(mixed) > 0 {
		// 갈린 축을 낼 때는 **첫 자원도 함께** 낸다 — 이름 하나만 대면 읽는 쪽이
		// "그 하나만 빼면 되나"로 읽는데, 실제로는 두 부류가 한 줄에 있는 것이 문제다.
		mixed = append([]string{resources[0]}, mixed...)
	}
	return scope, mixed
}

// sortStrings 는 문자열을 제자리 정렬한다.
//
// 명부는 20건 규모라 삽입 정렬로 족하고, 그 크기에서는 `sort` 호출보다 읽기 쉽다.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
