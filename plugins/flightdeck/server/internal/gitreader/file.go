package gitreader

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// 커밋된 파일 읽기 — 설계 §8 의 "대상 ref 의 파일에서 읽는다(로컬 사본을 믿지 않는다)".
//
// ★ **왜 os.ReadFile 이 아닌가.** 그 함수는 **작업 트리**를 읽고, 작업 트리에는 아직
// 커밋 안 된 편집·다른 브랜치의 상태·남이 만든 임시 파일이 섞인다. 워크스페이스 명부는
// 「이 레포가 무엇을 관장한다고 **선언했나**」이고 그 선언의 정본은 커밋이다 — 트리를
// 읽으면 편집기에 열어 둔 반쯤 쓴 명부가 서버의 배타 스코프를 바꾼다.
//
// ★ 그리고 서버는 컨테이너에서 돌아 작업 트리를 **읽기 전용**으로 마운트한다. 지금 이
// 순간 사람이 워크트리를 지우고 있어도 `git show` 는 오브젝트 DB 에서 답한다.

// ErrFileNotInRef 는 그 ref 에 그 경로가 없다는 표식이다. errors.Is 로 판별한다.
//
// ★ **오류를 종류로 가르는 것이 이 파일의 요점이다.** 「파일이 없다」는 정상 상태이고
// (워크스페이스가 아닌 레포 전부가 그렇다) 「git 을 못 읽었다」는 진단 대상이다. 하나로
// 접으면 마운트가 빠진 컨테이너에서 명부가 **조용히 빈 것**이 되고, 그 침묵은 자원
// 배타가 안 서는 것으로만 나타난다.
var ErrFileNotInRef = errors.New("그 ref 에 그 경로가 없다")

// pathNotInRefMarkers 는 「경로가 없다」를 가르는 stderr 문구다.
//
// ★ git 판·명령마다 문구가 다르다. `git show <ref>:<path>` 는 경로가 없으면
// "path 'x' does not exist in 'ref'" 또는 "fatal: invalid object name" 계열을 내는데,
// 후자는 **ref 자체가 없을 때**도 난다 — 둘을 접으면 "브랜치를 못 읽었다"가 "명부가
// 없다"로 둔갑한다. 그래서 경로 부재의 문구만 여기 열거하고, 나머지는 전부 진짜 오류다.
var pathNotInRefMarkers = []string{
	"does not exist in",
	"exists on disk, but not in", // 트리에는 있는데 커밋에는 없다 — 「아직 안 커밋했다」다
	"path does not exist",
}

// FileAt 은 ref 시점의 파일 내용을 읽는다.
//
// 경로가 그 ref 에 없으면 ErrFileNotInRef 로 감싼 오류를 낸다 — 호출부는 그것을
// 「선언이 없다」로 읽고 아무것도 안 바꾼다.
//
// ★ **크기 상한을 건다.** 이 함수가 읽는 것은 사람이 손으로 쓴 설정 파일이고, 그 크기는
// 킬로바이트 단위다. 상한이 없으면 같은 이름의 거대한 파일(누가 실수로 로그를 그 이름에
// 커밋한다)이 서버 메모리에 통째로 올라오는데, 이 경로는 **세션이 열릴 때마다** 돈다.
// 넘으면 자르지 않고 **거절한다** — 잘린 YAML 은 문법이 맞을 수도 있어서, 자르면 명부의
// 뒤쪽 절반이 조용히 사라진 채로 파싱을 통과한다.
const maxFileBytes = 256 * 1024

// FileAt 은 ref 시점의 path 내용을 낸다.
//
// ★ path 는 **저장소 루트 상대**다. `:` 뒤의 값을 git 이 그렇게 읽는다(`HEAD:a/b.yaml`).
// 앞에 `./` 가 붙으면 git 이 "현재 디렉토리 기준"으로 해석해 -C 로 준 자리에 따라 답이
// 달라지므로 벗긴다.
func (r *Reader) FileAt(ctx context.Context, ref, path string) ([]byte, error) {
	if err := validateRev("ref", ref); err != nil {
		return nil, err
	}
	p := strings.TrimPrefix(strings.TrimSpace(path), "./")
	if p == "" {
		return nil, errors.New("경로가 비었다")
	}
	if strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("경로가 절대경로다(%q) — 저장소 루트 상대여야 한다", p)
	}
	// ★ 개행·제어문자를 막는다. 이 값은 항목·설정에서 오지 않고 코드 상수이지만,
	//   나중에 인자로 열릴 때 이 가드가 이미 서 있어야 한다(validateRev 와 같은 논거 —
	//   가드는 값을 만드는 곳이 아니라 **소비하는 계층**에 있다).
	if strings.ContainsAny(p, "\x00\n\r") {
		return nil, fmt.Errorf("경로에 제어문자가 있다")
	}

	out, err := r.run(ctx, "", "show", "--no-textconv", ref+":"+p)
	if err != nil {
		if pathUnseenInRef(stderrOf(err)) {
			return nil, fmt.Errorf("%s:%s: %w", ref, p, ErrFileNotInRef)
		}
		r.log.WarnContext(ctx, "커밋된 파일 읽기 실패", "ref", ref, "path", p, "error", err.Error())
		return nil, fmt.Errorf("%s:%s 읽기 실패: %w", ref, p, err)
	}
	if len(out) > maxFileBytes {
		return nil, fmt.Errorf("%s:%s 가 %d바이트다 — 상한 %d바이트를 넘어 읽지 않았다"+
			"(자르면 뒤쪽 선언이 조용히 사라진다)", ref, p, len(out), maxFileBytes)
	}
	return out, nil
}

// pathUnseenInRef 는 stderr 가 「그 ref 에 그 경로가 없다」를 말하는가다. 순수 함수다.
func pathUnseenInRef(stderr string) bool {
	low := strings.ToLower(stderr)
	for _, m := range pathNotInRefMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}
