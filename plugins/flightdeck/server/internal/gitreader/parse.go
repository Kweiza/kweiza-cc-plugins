package gitreader

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// 이 파일에 있는 것은 전부 **순수 함수**다 — I/O 도 상태도 시계도 없다(관측 시각은 인자로 받는다).
//
// 파싱 판정이 메서드 본문에 흩어지면 시험이 그 로직의 **사본**을 단정하게 되고,
// 그러면 변이가 조용히 새어 나간다. 이 레포의 앞선 도구가 그 자리를 두 번 틀렸고
// 두 번 다 상위 시험은 초록이었다 — 한 번은 좌표 오류, 한 번은 원격 `grep -c` 가 0건일 때
// 종료코드 1이라 `|| echo 0` 이 값을 "0\n0" 으로 만든 것.
//
// 그래서 여기서는 **종료코드가 아니라 출력의 구조로 판정한다.** 구조가 기대와 다르면
// 그 자리에서 오류를 낸다 — 조용히 0 을 내지 않는다.

// ─────────────────────────────────────────────────────────────────────────────
// 문자열 위생 — 외부에서 온 것은 자르고 제어문자를 걷어낸 뒤에만 로그·오류에 싣는다
// ─────────────────────────────────────────────────────────────────────────────

// sanitizeExcerpt 는 git 이 stderr 로 뱉은 바이트를 로그·오류에 실을 수 있게 만든다.
//
// "status 128" 만 남기면 무엇이 틀렸는지 영영 모르므로 반드시 실어야 하는데,
// 그 문자열은 파일 이름·ref 이름을 그대로 품고 있어 제어문자가 들어올 수 있다.
// 절단은 룬 단위다 — 바이트로 자르면 UTF-8 이 깨져 한글 경로가 깨진 채 로그에 남는다.
func sanitizeExcerpt(b []byte, maxRunes int) string {
	s := strings.ToValidUTF8(string(b), "?")
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\t' || r == '\r' {
			sb.WriteRune(' ')
			continue
		}
		if unicode.IsControl(r) {
			continue // 로그 주입 방지. 값의 의미를 나르지 않는 문자라 버려도 조사가 안 막힌다
		}
		sb.WriteRune(r)
	}
	out := strings.Join(strings.Fields(sb.String()), " ")
	runes := []rune(out)
	if maxRunes > 0 && len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// git 실행 환경 — 부모 프로세스의 GIT_* 를 물려받지 않는다
// ─────────────────────────────────────────────────────────────────────────────

// 부모가 워크트리 안에서 훅으로 떠 있으면 GIT_DIR·GIT_WORK_TREE·GIT_INDEX_FILE 이 이미 걸려 있다.
// 그대로 물려받으면 `-C <repo>` 를 줘도 **엉뚱한 저장소를 읽고**, 그 사실이 출력 어디에도 안 나온다.
var strippedGitEnv = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"}

// 반드시 우리 값으로 덮는 것. 프롬프트가 뜨면 타임아웃까지 매달리고,
// optional lock 은 읽기 명령이 저장소에 쓰는 유일한 경로다(이 패키지는 읽기 전용이다).
var forcedGitEnv = []string{"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0"}

// gitEnv 는 부모 환경에서 위험한 GIT_* 를 지우고 우리 값을 건 환경을 만든다.
//
// 이름이 **정확히** 일치하는 것만 지운다 — 접두 일치로 지우면 GIT_DIRECTORY 같은
// 남의 변수를 조용히 삼킨다(경로 겹침 판정에서 이미 같은 모양의 결함을 봤다).
func gitEnv(parent []string) []string {
	drop := make(map[string]bool, len(strippedGitEnv)+len(forcedGitEnv))
	for _, k := range strippedGitEnv {
		drop[k] = true
	}
	for _, kv := range forcedGitEnv {
		drop[strings.SplitN(kv, "=", 2)[0]] = true
	}
	out := make([]string, 0, len(parent)+len(forcedGitEnv))
	for _, kv := range parent {
		name, _, ok := strings.Cut(kv, "=")
		if ok && drop[name] {
			continue
		}
		out = append(out, kv)
	}
	return append(out, forcedGitEnv...)
}

// ─────────────────────────────────────────────────────────────────────────────
// 리비전 인자 검사 — 소비 계층(git 명령줄)의 문법으로 막는다
// ─────────────────────────────────────────────────────────────────────────────

// validateRev 는 ref·sha 인자가 git 명령줄에 실려도 되는지 본다.
//
// 이 값들은 결국 큐 항목·MCP 도구를 거쳐 들어오므로 우리가 쓴 문자열이 아니다.
// "-" 로 시작하면 git 이 **옵션으로 읽는다** — `--is-ancestor` 자리에 `--help` 가 들어가면
// 명령이 통째로 다른 것이 된다. 가드는 값을 만드는 곳이 아니라 **소비하는 계층**에 있어야 한다.
//
// 불리언이 아니라 사유를 돌려준다. 사유가 없으면 "이 값이 걸렸다"와 "이 축을 안 본다"가 구분되지 않는다.
func validateRev(kind, rev string) error {
	if strings.TrimSpace(rev) == "" {
		return fmt.Errorf("%s 가 비었다", kind)
	}
	if strings.HasPrefix(rev, "-") {
		return fmt.Errorf("%s 가 %q 로 시작한다 — git 이 옵션으로 읽는다", kind, "-")
	}
	for _, r := range rev {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("%s 에 공백·제어문자가 있다: %q", kind, sanitizeExcerpt([]byte(rev), 80))
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// merge-base --is-ancestor 의 판정
// ─────────────────────────────────────────────────────────────────────────────

// ancestryVerdict 는 조상 판정 1회의 결과와 **왜 그렇게 판정했는지**다.
//
// OK=false 는 "셋 중 어느 것도 아니다 = 오류로 올려라"는 뜻이고, 그때 Result 는
// judge.AncestryUnknown(제로값 = "조회하지 않았다")이다 — 오류를 무시하고 값만 읽은
// 호출자가 그것을 "아직 아니다"로 오인할 수 없게. 넷째 경우를 셋 중 하나로 접지 않으려면
// 값과 사유를 함께 날라야 한다.
type ancestryVerdict struct {
	Result judge.AncestryResult
	OK     bool
	Reason string
}

// 128 인데 **저장소 자체가 문제**인 경우. 이것을 UnknownRef 로 접으면
// "설정이 틀렸다"가 "그런 항목이 없다"로 둔갑해 조사가 엉뚱한 데로 간다.
var repoLevelFatals = []string{
	"not a git repository",
	"detected dubious ownership",
	"permission denied",
	"unable to read",
	"index file",
	"object file",
	"repository is corrupt",
}

// 128 인데 **리비전을 못 읽은** 경우. git 판·명령마다 문구가 달라 넷을 함께 본다
// (merge-base 는 "Not a valid object name", log 는 "bad revision",
// rev-parse 는 "unknown revision or path not in the working tree").
var revLevelFatals = []string{
	"not a valid object name",
	"invalid object name",
	"bad revision",
	"unknown revision",
	"ambiguous argument",
}

// classifyAncestry 는 종료코드와 stderr 로 조상 판정을 가른다.
//
// **종료코드만으로는 못 가른다.** 128 은 "그런 ref 없음"과 "여긴 저장소가 아님"을 함께 낸다 —
// 후자를 UnknownRef 로 접으면 설정 사고가 큐의 미충족으로 위장한다. 그래서 출력의 구조를 함께 본다.
func classifyAncestry(exit int, stderr string) ancestryVerdict {
	low := strings.ToLower(stderr)
	switch exit {
	case 0:
		return ancestryVerdict{judge.AncestryYes, true, "종료코드 0 — 조상이다"}
	case 1:
		// 1 은 git 이 내는 **답**이다(실패가 아니다). 다만 stderr 가 있으면 사유에 실어 보인다 —
		// 경고를 조용히 버리면 그 경고가 존재한 적 없는 것이 된다.
		if stderr != "" {
			return ancestryVerdict{judge.AncestryNo, true, "종료코드 1 — 조상이 아니다(stderr 있음: " + stderr + ")"}
		}
		return ancestryVerdict{judge.AncestryNo, true, "종료코드 1 — 조상이 아니다"}
	case 128:
		for _, p := range repoLevelFatals {
			if strings.Contains(low, p) {
				return ancestryVerdict{judge.AncestryUnknown, false, "종료코드 128 이지만 ref 문제가 아니라 저장소 문제다: " + stderr}
			}
		}
		for _, p := range revLevelFatals {
			if strings.Contains(low, p) {
				return ancestryVerdict{judge.AncestryBadRef, true, "종료코드 128 + 리비전 해석 실패: " + stderr}
			}
		}
		return ancestryVerdict{judge.AncestryUnknown, false, "종료코드 128 인데 stderr 가 리비전 해석 실패로 안 읽힌다: " + stderr}
	default:
		return ancestryVerdict{judge.AncestryUnknown, false,
			fmt.Sprintf("종료코드 %d — merge-base 의 규약(0/1/128) 밖이다: %s", exit, stderr)}
	}
}

// headUnbornReason 은 HEAD 조회 실패가 "커밋이 하나도 없는 저장소"인지 판정한다.
//
// 갓 만든 저장소는 브랜치도 HEAD 도 없다. 그것을 오류로 올리면 프로젝트를 등록한 첫 순간에
// 보드가 통째로 죽는다. 반대로 아무 128 이나 여기서 삼키면 **진짜 고장이 빈 목록으로 위장한다.**
// 그래서 축 둘이 동시에 참일 때만 참이다 — 브랜치가 0건이고, stderr 가 리비전 해석 실패다.
func headUnbornReason(branchCount int, stderr string) (bool, string) {
	if branchCount != 0 {
		return false, fmt.Sprintf("브랜치가 %d건 있다 — 커밋 없는 저장소가 아니다", branchCount)
	}
	low := strings.ToLower(stderr)
	for _, p := range repoLevelFatals {
		if strings.Contains(low, p) {
			return false, "저장소 수준 오류다: " + stderr
		}
	}
	for _, p := range revLevelFatals {
		if strings.Contains(low, p) {
			return true, "브랜치 0건 + HEAD 리비전 해석 실패 — 커밋이 아직 없다: " + stderr
		}
	}
	if strings.Contains(low, "does not have any commits yet") {
		return true, "브랜치 0건 + git 이 커밋 없음을 명시했다: " + stderr
	}
	return false, "브랜치는 0건이지만 stderr 가 커밋 없음으로 안 읽힌다: " + stderr
}

// ─────────────────────────────────────────────────────────────────────────────
// 출력 파서
// ─────────────────────────────────────────────────────────────────────────────

// forEachRefFormat 은 필드를 NUL 로 가르고 **마지막 필드 뒤에도** NUL 을 둔다.
// for-each-ref 는 레코드마다 개행을 덧붙이므로, 마지막 필드를 NUL 로 닫지 않으면
// 그 개행이 날짜·제목 필드에 눌어붙어 다음 레코드의 이름과 합쳐진다.
const forEachRefFormat = "%(refname:short)%00%(objectname)%00%(contents:subject)%00"

// parseForEachRef 는 for-each-ref 출력을 RefState 로 옮긴다.
//
// observedAt 은 **관측 시각**이다(커밋 시각이 아니다). model.RefState.At 의 뜻이 그것이고,
// 화면이 "(파생: git@14:31, 12초 전)" 을 그 값으로 찍는다 — 서버가 죽었을 때
// 마지막 상태가 현재 사실인 척하는 것을 막는 축이라 커밋 시각으로 바꾸면 안 된다.
func parseForEachRef(out []byte, observedAt time.Time) ([]model.RefState, error) {
	toks := strings.Split(string(out), "\x00")
	var refs []model.RefState
	i := 0
	for ; i+3 <= len(toks); i += 3 {
		// 레코드 사이의 개행은 다음 레코드 **첫 필드의 앞**에 붙어 온다.
		name := strings.TrimLeft(toks[i], "\r\n")
		sha := toks[i+1]
		subject := toks[i+2]
		if name == "" {
			return nil, fmt.Errorf("for-each-ref: %d번째 레코드의 ref 이름이 비었다", i/3+1)
		}
		if err := checkSHA(sha); err != nil {
			return nil, fmt.Errorf("for-each-ref: ref %q: %w", name, err)
		}
		refs = append(refs, model.RefState{Ref: name, SHA: sha, Subject: subject, At: observedAt})
	}
	// 3의 배수로 안 떨어지는 꼬리는 **조용히 버리지 않는다.** 여기를 버리면
	// 포맷이 바뀐 날 목록이 짧아진 채 초록으로 지나간다.
	for ; i < len(toks); i++ {
		if strings.TrimSpace(toks[i]) != "" {
			return nil, fmt.Errorf("for-each-ref: 출력 꼬리가 필드 3개로 안 떨어진다: %q", sanitizeExcerpt([]byte(toks[i]), 120))
		}
	}
	return refs, nil
}

// logRecordFormat 은 Ref() 가 쓰는 `git log -1` 포맷이다. sha 와 제목 둘뿐 —
// 커밋 시각을 안 읽는 이유는 위 parseForEachRef 주석과 같다(At 은 관측 시각이다).
const logRecordFormat = "%H%x00%s"

// parseLogRecord 는 `git log -1 --format=%H%x00%s` 한 건을 가른다.
func parseLogRecord(out []byte) (sha, subject string, err error) {
	s := strings.TrimRight(string(out), "\n")
	if s == "" {
		return "", "", fmt.Errorf("git log 출력이 비었다 — 커밋 1건을 기대했다")
	}
	toks := strings.Split(s, "\x00")
	if len(toks) != 2 {
		return "", "", fmt.Errorf("git log 출력의 필드가 2개가 아니라 %d개다: %q", len(toks), sanitizeExcerpt([]byte(s), 120))
	}
	if err := checkSHA(toks[0]); err != nil {
		return "", "", err
	}
	return toks[0], toks[1], nil
}

// checkSHA 는 sha 자리에 sha 가 왔는지 본다.
// 길이를 고정하지 않는다 — sha256 저장소는 64자다. 오탐보다 미탐이 비싸므로 문자 집합만 본다.
func checkSHA(s string) error {
	if len(s) < 7 {
		return fmt.Errorf("sha 자리의 값이 너무 짧다: %q", sanitizeExcerpt([]byte(s), 80))
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return fmt.Errorf("sha 자리에 16진수가 아닌 값이 있다: %q", sanitizeExcerpt([]byte(s), 80))
		}
	}
	return nil
}

// parseNameOnlyZ 는 `diff --name-only -z` 출력을 경로 목록으로 옮긴다.
//
// -z 를 쓰는 이유가 전부 여기 있다: 경로에 개행이나 따옴표가 들어가면
// 줄 단위 파싱이 **조용히** 어긋난다(한 경로가 두 건이 되거나 사라진다).
func parseNameOnlyZ(out []byte) []string {
	var paths []string
	for _, t := range strings.Split(string(out), "\x00") {
		if t == "" {
			continue // 마지막 NUL 뒤의 빈 토큰. 경로가 빈 문자열인 경우는 git 에 없다
		}
		paths = append(paths, t)
	}
	return paths
}

// parseAheadBehind 는 `rev-list --left-right --count A...B` 출력을 가른다.
//
// **한 줄 · 탭으로 나뉜 정수 2개**만 받는다. 필드만 세면 "0\n0" 이 (0,0) 으로 통과하는데,
// 그것이 이 레포에서 실제로 났던 결함의 모양이다(종료코드를 믿고 `|| echo 0` 이 값을 두 줄로 만든 것).
// 그래서 줄 수부터 단정한다.
func parseAheadBehind(out []byte) (ahead, behind int, err error) {
	s := strings.TrimRight(string(out), "\n")
	if strings.TrimSpace(s) == "" {
		return 0, 0, fmt.Errorf("rev-list 출력이 비었다 — 정수 2개를 기대했다")
	}
	if strings.Contains(s, "\n") {
		return 0, 0, fmt.Errorf("rev-list 출력이 %d줄이다 — 1줄을 기대했다: %q",
			strings.Count(s, "\n")+1, sanitizeExcerpt([]byte(s), 120))
	}
	fields := strings.Split(s, "\t")
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("rev-list 출력의 탭 구분 필드가 %d개다 — 2개를 기대했다: %q",
			len(fields), sanitizeExcerpt([]byte(s), 120))
	}
	ahead, err = strconv.Atoi(strings.TrimSpace(fields[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("ahead 가 정수가 아니다: %q", sanitizeExcerpt([]byte(fields[0]), 80))
	}
	behind, err = strconv.Atoi(strings.TrimSpace(fields[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("behind 가 정수가 아니다: %q", sanitizeExcerpt([]byte(fields[1]), 80))
	}
	if ahead < 0 || behind < 0 {
		return 0, 0, fmt.Errorf("커밋 수가 음수다: ahead=%d behind=%d", ahead, behind)
	}
	return ahead, behind, nil
}

// parseStatusZ 는 `status --porcelain -z` 출력을 경로 목록으로 옮긴다.
//
// 형식은 `XY <경로>\0` 이고, 이름 변경·복사만 예외로 **원본 경로가 다음 토큰에 하나 더** 온다
// (`R  new\0old\0`). 그 둘째 토큰을 안 읽으면 원본 경로가 다음 항목의 상태 코드 자리로 읽혀
// 그 뒤 전부가 한 칸씩 밀린다 — 정확히 "좌표 오류"의 모양이다.
//
// 미추적 디렉토리는 git 이 `sub/` 한 줄로 접어 준다. 그대로 둔다 —
// 경로 겹침 판정이 성분 단위라 디렉토리 토큰이 그 아래 전부를 이미 덮고,
// 펼치면(-uall) 무시 대상이 아닌 거대 디렉토리 하나가 발자국을 통째로 삼킨다.
func parseStatusZ(out []byte) ([]string, error) {
	toks := strings.Split(string(out), "\x00")
	var paths []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t == "" {
			continue // 마지막 NUL 뒤의 빈 토큰
		}
		if len(t) < 4 {
			return nil, fmt.Errorf("status 항목이 너무 짧다(`XY <경로>` 를 기대했다): %q", sanitizeExcerpt([]byte(t), 80))
		}
		if t[2] != ' ' {
			return nil, fmt.Errorf("status 항목의 3번째 문자가 공백이 아니다: %q", sanitizeExcerpt([]byte(t), 80))
		}
		x, y := t[0], t[1]
		add(t[3:])
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			i++
			if i >= len(toks) || toks[i] == "" {
				return nil, fmt.Errorf("이름 변경·복사 항목인데 원본 경로 토큰이 없다: %q", sanitizeExcerpt([]byte(t), 80))
			}
			add(toks[i])
		}
	}
	return paths, nil
}

// Worktree 는 `git worktree list --porcelain -z` 한 레코드다.
//
// Locked·Prunable 을 불리언 하나로 접지 않고 사유를 함께 나른다.
// "잠겨 있다"만 알면 그 워크트리를 어떻게 해야 하는지 아무도 모른다.
type Worktree struct {
	Path           string
	HEAD           string // sha. bare 저장소에는 없다
	Branch         string // git 이 준 그대로의 전체 ref("refs/heads/feat"). 축약은 ShortBranch()
	Detached       bool
	Bare           bool
	Locked         bool
	LockReason     string // 사유 없이 잠글 수 있으므로 Locked=true 여도 빈 문자열일 수 있다
	Prunable       bool
	PrunableReason string
	Extra          []string // 우리가 모르는 속성 줄. 버리지 않고 호출자가 로그로 낸다
}

// ShortBranch 는 표시용 축약이다. 정본은 Branch 필드(전체 ref)다 —
// 축약만 보관하면 refs/heads 밖의 ref 가 붙은 워크트리에서 좌표가 소실된다.
func (w Worktree) ShortBranch() string { return strings.TrimPrefix(w.Branch, "refs/heads/") }

// parseWorktreeList 는 `git worktree list --porcelain -z` 를 가른다.
//
// -z 를 쓰면 줄 구분이 NUL 이고 **잠금 사유가 C-quote 되지 않는다**(따옴표·개행이 든 사유가
// 그대로 온다). -z 없이 읽으면 한글 사유가 `"\354\213\244..."` 로 와서 사람이 못 읽는다.
// 레코드 경계는 빈 줄(= 연속된 NUL 둘)이다.
func parseWorktreeList(out []byte) ([]Worktree, error) {
	var (
		list []Worktree
		cur  *Worktree
	)
	flush := func() {
		if cur != nil {
			list = append(list, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(string(out), "\x00") {
		if line == "" {
			flush()
			continue
		}
		key, val, _ := strings.Cut(line, " ")
		if key == "worktree" {
			flush()
			if val == "" {
				return nil, fmt.Errorf("worktree 줄에 경로가 없다: %q", sanitizeExcerpt([]byte(line), 120))
			}
			cur = &Worktree{Path: val}
			continue
		}
		if cur == nil {
			// 속성이 worktree 줄보다 먼저 왔다 = 우리가 형식을 잘못 알고 있다.
			// 여기서 오류를 안 내면 그 레코드가 통째로 사라진 채 목록이 짧아진다.
			return nil, fmt.Errorf("worktree 레코드가 시작되기 전에 속성 줄이 왔다: %q", sanitizeExcerpt([]byte(line), 120))
		}
		switch key {
		case "HEAD":
			if err := checkSHA(val); err != nil {
				return nil, fmt.Errorf("워크트리 %q: %w", cur.Path, err)
			}
			cur.HEAD = val
		case "branch":
			cur.Branch = val
		case "detached":
			cur.Detached = true
		case "bare":
			cur.Bare = true
		case "locked":
			cur.Locked = true
			cur.LockReason = val
		case "prunable":
			cur.Prunable = true
			cur.PrunableReason = val
		default:
			cur.Extra = append(cur.Extra, line)
		}
	}
	flush()
	return list, nil
}
