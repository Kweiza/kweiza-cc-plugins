package service

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// 항목이 선언한 경로가 실제로 그 프로젝트에 있는지 관측한다.
//
// **git 을 쓰지 않는다.** 답하려는 질문은 "이 경로가 이 레포에 실재하는가"이고
// 그 질문의 정본은 파일시스템이다. `git ls-files` 는 미추적 파일을 못 보고(방금 만든 파일을
// 가리키는 항목이 오탐이 된다), `git cat-file -e HEAD:<path>` 는 워크트리 상태를 못 본다.
// 그리고 프로세스 스폰이 stat 보다 서너 자릿수 비싸다.
//
// **2단이다.** 자기 프로젝트를 먼저 보고, 전부 없을 때만 남을 본다.
// 실측: 항목 하나당 stat 27회 · 0.048ms(보드 한 장의 git 파생이 이미 8~60ms 다).
//
// **derive(d.ok/d.fail)를 안 쓴다.** FreshnessOf 가 reads==0 → Source:"db",
// failures>0 → Stale 로 정의돼 있어서, stat 을 거기 세면 git 을 한 번도 안 읽은 응답이
// Source:"git" 이 되거나 git 이 멀쩡한 응답이 Stale 이 된다. 이 축은 자기 상태를
// 자기 안에서 말한다 — Unreadable 과 KindUnknown 이 그 자리다.

// checkItemPaths 는 항목 하나의 경로가 어느 프로젝트에 실재하는지 관측하고 판정한다.
//
// **절대 nil 을 돌려주지 않는다.** nil 은 "이 응답은 그 축을 안 읽었다"를 뜻하는데,
// 이 함수가 돌았다는 것 자체가 읽었다는 뜻이다. 못 읽은 것은 KindUnknown 으로 말한다.
func (s *Service) checkItemPaths(ctx context.Context, proj model.Project, paths []string) *judge.ItemPathVerdict {
	in := judge.ItemPathInput{Project: proj.ID, Paths: paths}
	if len(paths) == 0 {
		v := judge.ClassifyItemPaths(in)
		return &v
	}

	// ★ 사유는 **이 프로젝트 축에서만** 모은다. 남의 프로젝트를 못 읽은 사실은
	// in.Unreadable 이 이미 나르고, KindUnknown 문장은 Here 만 근거로 만들어진다.
	in.UnknownReason = map[string]string{}
	in.Here = observeIn(proj.Path, paths, in.UnknownReason)

	// 1단에서 하나라도 봤으면(있다) 남을 볼 필요가 없다. 못 읽은 것이 섞여 있어도
	// 판정은 unknown 으로 갈 것이므로 역시 남을 볼 필요가 없다.
	if !allAbsent(paths, in.Here) {
		v := judge.ClassifyItemPaths(in)
		return &v
	}

	others, err := s.st.ListProjects(ctx)
	if err != nil {
		// 목록을 못 읽으면 "다른 데 있다/없다"를 말할 수 없다. 그 사실을 숨기지 않는다.
		//
		// ★ in.Unreadable 에 넣지 않는다. Unreadable 은 **프로젝트 id 목록**이고 REST 로
		// 그대로 나간다(judge.ItemPathVerdict.Unreadable). 목록 조회 실패는 id 가 아니라
		// **상태**다 — in.Elsewhere 를 nil 로 둔 채 놔두면 judge.ClassifyItemPaths 가
		// 그 nil 을 읽고 이미 KindUnknown 을 낸다(classifyAllAbsent). id 자리에 유사 id
		// 문자열을 채우면 그 문자열이 REST 의 unreadable 배열을 오염시킨다.
		s.log.WarnContext(ctx, "프로젝트 목록 조회 실패 — 경로 실재 축의 지목을 못 한다",
			"project", proj.ID, "error", err.Error())
		v := judge.ClassifyItemPaths(in)
		return &v
	}

	in.Elsewhere = map[string]map[string]judge.PathPresence{}
	for _, o := range others {
		if o.ID == proj.ID {
			continue
		}
		if !rootUsable(o.Path) {
			in.Unreadable = append(in.Unreadable, o.ID)
			continue
		}
		in.Elsewhere[o.ID] = observeIn(o.Path, paths, nil)
	}

	v := judge.ClassifyItemPaths(in)
	return &v
}

// allAbsent 는 관측된 것이 **전부 Absent** 인지 본다.
// Present 가 하나라도 있으면 false, Unknown 이 하나라도 있어도 false 다 —
// 둘 다 "남의 프로젝트를 뒤질 근거가 없다"는 뜻이기 때문이다.
func allAbsent(paths []string, here map[string]judge.PathPresence) bool {
	for _, p := range paths {
		if here[p] != judge.PathAbsent {
			return false
		}
	}
	return true
}

// observeIn 은 저장소 하나에서 경로들을 stat 한다.
//
// ★ **루트를 먼저 잰다.** 루트가 통째로 없으면 그 아래 모든 경로의 stat 도 ErrNotExist 를
// 내는데, 그것을 Absent 로 접으면 죽은 프로젝트의 항목이 nowhere 나 misregistered 로
// **고발당한다.** "프로젝트를 못 열었으면 Unknown"과 "ErrNotExist 면 Absent"라는 두 규칙이
// 정확히 이 지점에서 충돌하고, 루트 stat 이 그 충돌을 없애는 유일한 단계다.
// ★ why 는 PathUnknown 이 난 경로 → 사유다. nil 을 줘도 된다(남의 프로젝트를 볼 때가 그렇다) —
// 사유가 문장에 실리는 것은 **이 프로젝트(Here)** 축뿐이라 그쪽만 채우면 된다.
// errno 를 여기서 안 넘기면 그 자리에서 영영 버려진다: 원인 셋이 판정 계층에서
// 바이트 단위로 같은 문장이 된다(judge.ItemPathInput.UnknownReason).
func observeIn(root string, paths []string, why map[string]string) map[string]judge.PathPresence {
	out := make(map[string]judge.PathPresence, len(paths))
	if !rootUsable(root) {
		// ★ 원인이 **경로가 아니라 레포**인 유일한 갈래다. 경로마다 같은 사유를 넣어 두면
		// judge 가 "전부 같은 이유다"로 접어 레포를 지목한다. 그 접기가 없으면 화면이
		// 경로 목록만 내고 운영자는 옮겨진 것이 레포라는 것을 못 읽는다.
		for _, p := range paths {
			out[p] = judge.PathUnknown // 0값이지만 명시한다 — 키 부재와 값을 가른다
			if why != nil {
				why[p] = "프로젝트 루트를 열 수 없다: " + clip(root, 120)
			}
		}
		return out
	}
	for _, p := range paths {
		pres, reason := observeOne(root, p)
		out[p] = pres
		if why != nil && reason != "" {
			why[p] = reason
		}
	}
	return out
}

// rootUsable 은 저장소 루트가 실제로 열리는 디렉토리인지 본다.
func rootUsable(root string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	st, err := os.Stat(root)
	return err == nil && st.IsDir()
}

// observeOne 은 경로 하나를 관측한다.
//
// ★ 루트 **밖으로 나가는 토큰은 stat 하지 않는다.** judge.components 가 ".." 를 일부러
// 안 걷어내는 것과 같은 규율이다 — 조용히 정규화하면 그 입력 오류가 안 보인다.
// 그리고 filepath.Join(root, "../../etc") 에 그대로 stat 하면 프로젝트 밖을 관측하게 되어
// 판정이 남의 디렉토리에 기댄다.
//
// 밖인지는 문자열 접두가 아니라 filepath.Rel 로 성분 단위 계산한다 — 접두로 하면
// root="/a/b" 일 때 "/a/bc/d" 가 안이라고 나온다(같은 모양의 결함이 이 레포에 실재했다).
// ★ 둘째 반환값은 PathUnknown 일 때의 **사유**다. 나머지 둘(Absent·Present)은 빈 문자열이다 —
// 사유는 "왜 판정을 못 했나"이지 판정 결과의 설명이 아니다.
func observeOne(root, p string) (judge.PathPresence, string) {
	if strings.TrimSpace(p) == "" {
		return judge.PathUnknown, "경로가 비었다"
	}
	joined := filepath.Join(root, p)
	rel, err := filepath.Rel(filepath.Clean(root), joined)
	if err != nil {
		return judge.PathUnknown, "루트 기준 상대경로를 못 구했다: " + clip(err.Error(), 120)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		// ★ 이 갈래는 **항목 등록이 잘못된 것**이라 고칠 사람이 있다. "못 읽었다"로만
		// 말하면 그것이 입력 오류인 줄 모른다 — 그래서 사유가 특히 중요한 자리다.
		return judge.PathUnknown, "'..' 로 프로젝트 루트 밖을 가리킨다 — 관측하지 않았다"
	}
	if _, err := os.Stat(joined); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return judge.PathAbsent, ""
		}
		// 권한·I/O — **절대 Absent 가 아니다.** errno 문장을 그대로 나른다(EACCES 등).
		return judge.PathUnknown, clip(errText(err), 120)
	}
	return judge.PathPresent, ""
}

// errText 는 stat 오류에서 사람이 읽을 부분만 낸다.
//
// *fs.PathError 의 Error() 는 "stat /긴/절대/경로: permission denied" 라 경로가 두 번
// (문장 앞의 선언 경로 + 여기) 나오고 루트 절대경로까지 샌다. 원인만 남긴다.
func errText(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) && pe.Err != nil {
		return pe.Err.Error()
	}
	return err.Error()
}
