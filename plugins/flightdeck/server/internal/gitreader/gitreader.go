// Package gitreader 는 git 저장소를 **읽기만** 해서 파생 사실을 만든다.
//
// 이 패키지가 "파생 우선" 원칙의 실행부다. 손으로 적던 값 — 브랜치·HEAD·랜딩 sha·변경 경로 —
// 이 전부 여기서 나온다. 손으로 베낀 스냅숏은 원본이 움직이는 순간 조용히 거짓이 되고,
// 거짓임을 알려 주는 자리가 없다. 그래서 **사람이 적을 자리를 없애는 것**이 이 패키지의 목적이다.
//
// 지키는 것 다섯:
//
//  1. **읽기 전용이다.** 저장소 상태를 바꾸는 git 명령을 한 번도 부르지 않는다.
//     fetch 도 안 부른다 — Tier A 는 로컬만 본다(원격 조회는 Tier B 몫이다).
//  2. **git 환경을 오염시키지 않는다.** 명령마다 `-C <repo>` 를 주고 GIT_DIR·GIT_WORK_TREE·
//     GIT_INDEX_FILE 을 지운 채 실행한다. 부모가 워크트리 안의 훅이면 그것들이 이미 걸려 있다.
//  3. **종료코드가 0이 아닌데 stderr 를 버리지 않는다.** "status 128" 만 남기면
//     무엇이 틀렸는지 영영 모른다. 절단·제어문자 제거 후 오류에 싣는다.
//  4. **판정은 시험이 부르는 순수 함수(parse.go)에 있다.** 메서드 본문에 흩어지면
//     시험이 그 로직의 사본을 단정하게 되고, 그러면 변이가 조용히 새어 나간다.
//  5. **타임아웃은 컨텍스트로.** 기본 10초.
//
// 이 패키지는 go-git 같은 라이브러리를 쓰지 않는다. os/exec 로 실제 git 을 부른다 —
// 워크트리·잠금·prunable 처럼 우리가 읽어야 하는 사실의 정본이 git 의 배관 명령 출력이기 때문이다.
package gitreader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// DefaultTimeout 은 git 명령 1회의 기본 상한이다.
// 로컬 저장소만 읽으므로 이보다 오래 걸리면 대개 무언가 잘못된 것이다.
const DefaultTimeout = 10 * time.Second

// stderrExcerptRunes 는 오류·로그에 싣는 stderr 앞부분의 길이다.
// 잘라도 첫 fatal 줄은 온전히 들어가는 값이다.
const stderrExcerptRunes = 400

// Reader 는 저장소 하나를 읽는다.
//
// 상태를 캐시하지 않는다 — 캐시하는 순간 그 값이 "언제 관측한 것인가"를 함께 날라야 하고,
// 그 축은 저장 계층(ref_state·change_set)이 이미 갖고 있다.
type Reader struct {
	repoPath string
	timeout  time.Duration
	log      *slog.Logger
	gitBin   string
}

// Option 은 Reader 의 선택 설정이다.
type Option func(*Reader)

// WithTimeout 은 git 명령 1회의 상한을 바꾼다. 0 이하는 무시한다.
func WithTimeout(d time.Duration) Option {
	return func(r *Reader) {
		if d > 0 {
			r.timeout = d
		}
	}
}

// WithLogger 는 로거를 바꾼다. nil 은 무시한다.
func WithLogger(l *slog.Logger) Option {
	return func(r *Reader) {
		if l != nil {
			r.log = l
		}
	}
}

// WithGitBinary 는 git 실행 파일 경로를 바꾼다.
// 시험에서 "git 을 못 띄우는 경우"를 만들기 위해서도 쓴다 — 그 경로가 없으면
// 종료코드가 아니라 실행 실패가 나는데, 그것이 셋 중 하나로 접히지 않는지 봐야 한다.
func WithGitBinary(p string) Option {
	return func(r *Reader) {
		if p != "" {
			r.gitBin = p
		}
	}
}

// New 는 repoPath 를 읽는 Reader 를 만든다.
//
// repoPath 는 워크트리든 bare 든 상관없다 — `-C` 로 넘길 뿐이다.
// 경로 실재를 여기서 확인하지 않는다: 확인해도 첫 호출까지 사이에 사라질 수 있고,
// 그러면 두 자리에서 같은 실패를 다르게 보고하게 된다(비대칭은 결함의 신호다).
func New(repoPath string, opts ...Option) *Reader {
	r := &Reader{
		repoPath: repoPath,
		timeout:  DefaultTimeout,
		gitBin:   "git",
	}
	for _, o := range opts {
		o(r)
	}
	if r.log == nil {
		r.log = slog.Default()
	}
	r.log = r.log.With("component", "gitreader", "repo", repoPath)
	return r
}

// RepoPath 는 이 Reader 가 읽는 저장소 경로다.
func (r *Reader) RepoPath() string { return r.repoPath }

// CommandError 는 git 명령 1회의 실패다.
//
// **stderr 를 반드시 나른다.** 상태코드만 남기면 무엇이 틀렸는지 영영 모른다.
// ExitCode 는 조상 판정이 0/1/128 을 가르는 축이기도 하므로 errors.As 로 꺼낼 수 있어야 한다.
type CommandError struct {
	Args     []string // 실제로 넘긴 인자(맨 앞의 -C <repo> 포함)
	ExitCode int      // -1 이면 프로세스를 못 띄웠거나 시그널로 죽었다 — 종료코드 규약 밖이다
	Stderr   string   // 절단·제어문자 제거된 앞부분
	Duration time.Duration
	Err      error // 실행 실패·타임아웃의 원인. 종료코드로 끝난 실패에는 없다
}

func (e *CommandError) Error() string {
	msg := fmt.Sprintf("git %s: status %d", sanitizeExcerpt([]byte(strings.Join(e.Args, " ")), 200), e.ExitCode)
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *CommandError) Unwrap() error { return e.Err }

// run 은 git 을 한 번 부른다. dir 이 비면 Reader 의 저장소 경로를 쓴다.
//
// 종료코드가 0이 아니면 stdout 을 그대로 돌려주면서 *CommandError 를 함께 낸다 —
// 실패한 명령의 부분 출력이 진단에 필요한 경우가 있고, 무엇보다 stderr 를 버리지 않기 위해서다.
func (r *Reader) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if dir == "" {
		dir = r.repoPath
	}
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("저장소 경로가 비었다 — Reader 를 빈 경로로 만들었다")
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, r.gitBin, full...)
	cmd.Env = gitEnv(os.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)
	if err == nil {
		return stdout.Bytes(), nil
	}

	ce := &CommandError{
		Args:     full,
		ExitCode: -1,
		Stderr:   sanitizeExcerpt(stderr.Bytes(), stderrExcerptRunes),
		Duration: elapsed,
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		ce.ExitCode = ee.ExitCode() // 시그널로 죽었으면 -1 이 그대로 남는다
	} else {
		ce.Err = err // 실행 자체를 못 했다(git 이 없다 등)
	}
	if cerr := ctx.Err(); cerr != nil {
		// 타임아웃·취소는 종료코드로 구분되지 않는다. 원인을 체인에 남겨야
		// "느린 저장소"와 "고장난 저장소"가 로그에서 갈린다.
		ce.Err = errors.Join(ce.Err, cerr)
	}
	return stdout.Bytes(), ce
}

// stderrOf 는 오류 체인에서 stderr 발췌를 꺼낸다. 없으면 빈 문자열이다.
func stderrOf(err error) string {
	var ce *CommandError
	if errors.As(err, &ce) {
		return ce.Stderr
	}
	return ""
}

// Refs 는 로컬 브랜치 전부와 HEAD 를 관측한다.
//
// 원격 추적 브랜치는 안 읽는다 — Tier A 는 로컬만 본다.
// 반환하는 RefState.Project 는 비어 있다: 이 패키지는 프로젝트가 무엇인지 모른다.
// 저장 계층이 자기 좌표를 채운다.
func (r *Reader) Refs(ctx context.Context) ([]model.RefState, error) {
	out, err := r.run(ctx, "", "for-each-ref", "--format="+forEachRefFormat, "refs/heads/")
	if err != nil {
		r.log.ErrorContext(ctx, "로컬 브랜치 열거 실패", "error", err.Error())
		return nil, fmt.Errorf("로컬 브랜치 열거 실패: %w", err)
	}
	observedAt := time.Now().UTC()
	refs, err := parseForEachRef(out, observedAt)
	if err != nil {
		r.log.ErrorContext(ctx, "for-each-ref 출력 해석 실패", "error", err.Error())
		return nil, err
	}

	head, herr := r.Ref(ctx, "HEAD")
	if herr != nil {
		unborn, reason := headUnbornReason(len(refs), stderrOf(herr))
		if !unborn {
			r.log.ErrorContext(ctx, "HEAD 관측 실패", "error", herr.Error(), "reason", reason)
			return nil, fmt.Errorf("HEAD 관측 실패: %w", herr)
		}
		// 커밋이 하나도 없는 저장소. 오류로 올리면 프로젝트 등록 첫 순간에 보드가 죽는다.
		// 조용히 넘기지도 않는다 — 왜 HEAD 가 없는지가 로그에 남아야 한다.
		r.log.WarnContext(ctx, "HEAD 없음 — 커밋이 아직 없는 저장소", "reason", reason, "error", herr.Error())
		return refs, nil
	}
	return append(refs, head), nil
}

// Ref 는 ref 하나를 관측한다. ref 는 "HEAD"·브랜치 이름·sha 무엇이든 된다.
//
// RefState.At 은 **관측 시각**이지 커밋 시각이 아니다. 화면이 신선도를 그 값으로 찍는다.
func (r *Reader) Ref(ctx context.Context, ref string) (model.RefState, error) {
	if err := validateRev("ref", ref); err != nil {
		return model.RefState{}, err
	}
	out, err := r.run(ctx, "", "log", "-1", "--format="+logRecordFormat, ref, "--")
	if err != nil {
		r.log.ErrorContext(ctx, "ref 관측 실패", "ref", ref, "error", err.Error())
		return model.RefState{}, fmt.Errorf("ref %q 관측 실패: %w", ref, err)
	}
	sha, subject, perr := parseLogRecord(out)
	if perr != nil {
		r.log.ErrorContext(ctx, "git log 출력 해석 실패", "ref", ref, "error", perr.Error())
		return model.RefState{}, fmt.Errorf("ref %q: %w", ref, perr)
	}
	return model.RefState{Ref: ref, SHA: sha, Subject: subject, At: time.Now().UTC()}, nil
}

// ChangedPaths 는 base 와 head **두 커밋을 직접 비교**한 변경 경로다(두 점 diff).
//
// 갈래 지점 기준(`base...head`)이 필요하면 호출자가 merge-base 를 넘긴다 —
// 여기서 세 점으로 바꾸면 "두 커밋 사이"라는 change_set 의 뜻이 조용히 달라진다.
//
// **--no-renames 가 붙어 있다.** 기본값(이름 변경 탐지)이면 `--name-only` 가 **목적지 경로만**
// 찍고 원본 경로는 출력에서 사라진다. 그러면 옮겨진 파일의 옛 경로가 변경집합에서 통째로 빠져,
// 그 경로를 만지는 세션이 겹침 축에 안 걸린다 — 침묵하는 손실이라 화면 어디에도 안 나온다.
// 실물로 확인한 모양: 내용이 같은 두 파일이 갈래마다 하나씩 있으면 git 이 그 둘을
// R100 한 건으로 접어 두 경로 중 하나만 낸다.
//
// ★ **`--numstat` 이다(2026-08-11 개정. `--name-only` 였다).** 겹침 줄이 상대 세션의 변경
// 규모를 함께 내야 하는데, numstat 은 경로를 **덤으로** 준다 — 즉 같은 프로세스 하나로
// 경로와 규모를 둘 다 얻는다. 따로 메서드를 두면 git 프로세스가 하나 더 늘고, 이 축을
// 꼬리 겹침에만 실은 근거(비용이 안 는다)가 거기서 무너진다.
//
// ★ **반환이 셋인 것이 요점이다.** 이진 파일은 경로로는 남고 규모로는 안 남는다 —
// 하나로 접으면 "바뀌었는데 못 쟀다"를 표현할 수 없다(parseNumstatZ 주석).
func (r *Reader) ChangedPaths(ctx context.Context, base, head string) ([]string, map[string]model.LineDelta, error) {
	if err := validateRev("base", base); err != nil {
		return nil, nil, err
	}
	if err := validateRev("head", head); err != nil {
		return nil, nil, err
	}
	out, err := r.run(ctx, "", "diff", "--numstat", "-z", "--no-renames", base, head, "--")
	if err != nil {
		r.log.ErrorContext(ctx, "변경 경로 관측 실패", "base", base, "head", head, "error", err.Error())
		return nil, nil, fmt.Errorf("변경 경로 관측 실패(%s..%s): %w", base, head, err)
	}
	paths, delta, skipped := parseNumstatZ(out)
	if len(skipped) > 0 {
		// 조용히 버리지 않는다 — 규모가 왜 비었는지 남는 유일한 자리다.
		r.log.WarnContext(ctx, "numstat 레코드를 버렸다(그 경로만 규모가 빈다)",
			"base", base, "head", head, "dropped", len(skipped), "first_reason", skipped[0])
	}
	return paths, delta, nil
}

// MergeBase 는 두 ref 의 갈래 지점 커밋이다(`git merge-base a b`).
//
// ★ 왜 ChangedPaths 를 세 점(`a...b`)으로 바꾸지 않고 이것을 따로 두나.
// 세 점 diff 는 갈래 지점을 git 안에서 구해 주므로 프로세스가 한 번 덜 든다(실측: 세 점 4ms,
// merge-base 3ms + 두 점 4ms = 7ms). 그런데 그러면 **갈래 지점 sha 가 손에 안 남는다.**
// change_set 은 `(base_sha, head_sha)` 를 키로 "두 커밋 사이"를 불변 보관하므로, 갈래 기준
// 경로를 담으면서 base 에 기본 브랜치의 tip 을 적으면 그 행이 거짓이 된다 — 그 키로 읽는 쪽은
// 두 점을 기대하는데 내용은 갈래 기준이다. 갈래 지점을 실체화해 base 로 적으면 뜻이 정확히
// 보존된다: **갈래 기준 diff 는 merge-base 로부터의 두 점 diff 와 문자 그대로 같다.**
// 그 3ms 가 sha 를 사는 값이다.
//
// 공통 조상이 없으면(관계 없는 이력) 오류다. 호출자가 두 점으로 되돌아가면 안 된다 —
// 그것이 이 메서드가 없애려는 바로 그 오탐이다. 못 구했으면 그 축을 비우고 못 읽었다고 말해라.
func (r *Reader) MergeBase(ctx context.Context, a, b string) (string, error) {
	if err := validateRev("a", a); err != nil {
		return "", err
	}
	if err := validateRev("b", b); err != nil {
		return "", err
	}
	out, err := r.run(ctx, "", "merge-base", a, b, "--")
	if err != nil {
		r.log.ErrorContext(ctx, "갈래 지점 관측 실패", "a", a, "b", b, "error", err.Error())
		return "", fmt.Errorf("갈래 지점 관측 실패(%s, %s): %w", a, b, err)
	}
	sha := strings.TrimSpace(string(out))
	if err := checkSHA(sha); err != nil {
		r.log.ErrorContext(ctx, "갈래 지점 출력 해석 실패", "a", a, "b", b, "error", err.Error())
		return "", fmt.Errorf("갈래 지점(%s, %s): %w", a, b, err)
	}
	return sha, nil
}

// Ancestry 는 sha 가 tip 의 조상인지 판정한다. **값이 셋이다**(judge.AncestryResult).
//
// git 은 0(조상)·1(아님)·128(그런 ref 없음)을 내는데, 128 을 1 로 접으면
// 오타와 "아직"이 뭉개져 그 항목이 영구히 굶는다 — 화면에는 "아직 안 됐다"로만 보인 채로.
// 그리고 128 은 "저장소가 아니다"도 함께 내므로 **종료코드만으로는 못 가른다.**
// 그 판정은 classifyAncestry(순수 함수)에 있고 시험이 그것을 직접 부른다.
//
// 셋 중 어느 것도 아닌 실패는 오류다. 그때 반환값은 judge.AncestryUnknown(제로값,
// "조회하지 않았다")이라 오류를 무시한 호출자가 그것을 "아직 아니다"로 오인할 수 없다.
func (r *Reader) Ancestry(ctx context.Context, sha, tip string) (judge.AncestryResult, error) {
	if err := validateRev("sha", sha); err != nil {
		return judge.AncestryUnknown, err
	}
	if err := validateRev("tip", tip); err != nil {
		return judge.AncestryUnknown, err
	}
	_, err := r.run(ctx, "", "merge-base", "--is-ancestor", sha, tip)
	if err == nil {
		return judge.AncestryYes, nil
	}
	var ce *CommandError
	if !errors.As(err, &ce) {
		r.log.ErrorContext(ctx, "조상 판정 실패", "sha", sha, "tip", tip, "error", err.Error())
		return judge.AncestryUnknown, fmt.Errorf("조상 판정 실패(%s → %s): %w", sha, tip, err)
	}
	v := classifyAncestry(ce.ExitCode, ce.Stderr)
	if !v.OK {
		r.log.ErrorContext(ctx, "조상 판정 실패", "sha", sha, "tip", tip,
			"exit_code", ce.ExitCode, "reason", v.Reason, "error", err.Error())
		return judge.AncestryUnknown, fmt.Errorf("조상 판정 실패(%s → %s): %s: %w", sha, tip, v.Reason, err)
	}
	if v.Result == judge.AncestryBadRef {
		// 오류는 아니지만 대개 오타다. 알리지 않으면 그 항목이 왜 안 도는지 아무도 모른다.
		r.log.WarnContext(ctx, "조상 판정: 그런 ref 가 없다", "sha", sha, "tip", tip,
			"exit_code", ce.ExitCode, "reason", v.Reason)
	}
	return v.Result, nil
}

// Worktrees 는 이 저장소에 딸린 워크트리 전부를 낸다(주 워크트리 포함).
//
// 잠김·prunable 을 함께 낸다 — 세션이 남긴 워크트리가 살아 있는지 판단하는 축이고,
// 사유 없이 불리언만 내면 그 워크트리를 어떻게 해야 하는지 아무도 모른다.
func (r *Reader) Worktrees(ctx context.Context) ([]Worktree, error) {
	out, err := r.run(ctx, "", "worktree", "list", "--porcelain", "-z")
	if err != nil {
		r.log.ErrorContext(ctx, "워크트리 열거 실패", "error", err.Error())
		return nil, fmt.Errorf("워크트리 열거 실패: %w", err)
	}
	wts, perr := parseWorktreeList(out)
	if perr != nil {
		r.log.ErrorContext(ctx, "worktree list 출력 해석 실패", "error", perr.Error())
		return nil, perr
	}
	for _, w := range wts {
		if len(w.Extra) > 0 {
			// 모르는 속성을 조용히 버리면 git 이 형식을 넓힌 날 우리가 무엇을 안 보는지 모르게 된다.
			r.log.WarnContext(ctx, "worktree list 에 모르는 속성이 있다",
				"worktree", w.Path, "count", len(w.Extra),
				"reason", sanitizeExcerpt([]byte(strings.Join(w.Extra, " | ")), 200))
		}
	}
	return wts, nil
}

// AheadBehind 는 ref 가 base 보다 앞선 커밋 수와 뒤진 커밋 수다.
//
// `rev-list --left-right --count <ref>...<base>` 의 왼쪽이 ahead, 오른쪽이 behind 다.
// 인자 순서를 뒤집으면 두 값이 통째로 바뀌는데 **둘 다 정수라 아무 데서도 안 걸린다** —
// 그래서 시험이 ahead≠behind 인 저장소로 방향을 단정한다.
func (r *Reader) AheadBehind(ctx context.Context, ref, base string) (ahead, behind int, err error) {
	if err := validateRev("ref", ref); err != nil {
		return 0, 0, err
	}
	if err := validateRev("base", base); err != nil {
		return 0, 0, err
	}
	out, rerr := r.run(ctx, "", "rev-list", "--left-right", "--count", ref+"..."+base, "--")
	if rerr != nil {
		r.log.ErrorContext(ctx, "ahead/behind 관측 실패", "ref", ref, "base", base, "error", rerr.Error())
		return 0, 0, fmt.Errorf("ahead/behind 관측 실패(%s vs %s): %w", ref, base, rerr)
	}
	ahead, behind, perr := parseAheadBehind(out)
	if perr != nil {
		r.log.ErrorContext(ctx, "rev-list 출력 해석 실패", "ref", ref, "base", base, "error", perr.Error())
		return 0, 0, fmt.Errorf("ahead/behind 해석 실패(%s vs %s): %w", ref, base, perr)
	}
	return ahead, behind, nil
}

// UncommittedPaths 는 워크트리의 미커밋 발자국이다 — 스테이징·미스테이징·미추적 전부.
//
// worktree 가 비면 Reader 의 저장소 경로를 본다.
// **커밋 전 의도를 나르는 유일한 축**이라 조용히 짧아지면 안 된다: 이름 변경 항목의
// 원본 경로까지 낸다(그 자리를 놓치면 그 뒤 항목 전부가 한 칸씩 밀린다).
func (r *Reader) UncommittedPaths(ctx context.Context, worktree string) ([]string, error) {
	out, err := r.run(ctx, worktree, "status", "--porcelain", "-z")
	if err != nil {
		r.log.ErrorContext(ctx, "미커밋 경로 관측 실패", "worktree", worktree, "error", err.Error())
		return nil, fmt.Errorf("미커밋 경로 관측 실패(%s): %w", worktree, err)
	}
	paths, perr := parseStatusZ(out)
	if perr != nil {
		r.log.ErrorContext(ctx, "status 출력 해석 실패", "worktree", worktree, "error", perr.Error())
		return nil, fmt.Errorf("미커밋 경로 해석 실패(%s): %w", worktree, perr)
	}
	return paths, nil
}

// UncommittedDelta 는 워크트리의 미커밋 증감이다 — 스테이징 + 미스테이징(추적 파일).
//
// ★ **UncommittedPaths 를 대체하지 않는다. 그 옆에 선다.** 이유 둘이고 둘 다 실물 관측이다:
//
//	① `status --porcelain` 만이 **미추적 파일과 이름 변경 원본 경로**를 낸다. numstat 은
//	   미추적 파일을 아예 안 본다 — 그것들은 경로로는 남고 규모로는 키가 없어야 맞다.
//	② **별개 호출이라 이것이 실패해도 경로 축이 산다.** UncommittedPaths 의 독스트링이
//	   "커밋 전 의도를 나르는 유일한 축이라 조용히 짧아지면 안 된다"고 못박은 그 축이다.
//	   합쳤다면 규모를 못 읽은 순간 경로도 같이 사라진다.
//
// ★ **이것이 이 축에서 유일하게 새로 드는 git 호출이다**(세션당 4→5). 커밋 구간은
// ChangedPaths 가 numstat 으로 바뀌면서 공짜가 됐다. 이 하나를 내는 이유는 이 축을 낳은
// 손 앵커들이 대부분 **랜딩 전 구간**이기 때문이다 — 안 재면 조율이 가장 필요한 창이 빈다.
//
// 커밋이 하나도 없는 저장소에서는 `HEAD` 가 없어 실패한다. **특례를 안 만든다** —
// 호출부(service.sessionCardsAndRoots)의 바로 이웃 줄인 Ref(기본 브랜치)가 같은 저장소에서
// 이미 같은 모양으로 실패를 낸다. 특례를 만들면 붙어 있는 두 줄의 관용이 갈린다.
func (r *Reader) UncommittedDelta(ctx context.Context, worktree string) (map[string]model.LineDelta, error) {
	out, err := r.run(ctx, worktree, "diff", "--numstat", "-z", "--no-renames", "HEAD", "--")
	if err != nil {
		r.log.ErrorContext(ctx, "미커밋 규모 관측 실패", "worktree", worktree, "error", err.Error())
		return nil, fmt.Errorf("미커밋 규모 관측 실패(%s): %w", worktree, err)
	}
	_, delta, skipped := parseNumstatZ(out)
	if len(skipped) > 0 {
		r.log.WarnContext(ctx, "numstat 레코드를 버렸다(그 경로만 규모가 빈다)",
			"worktree", worktree, "dropped", len(skipped), "first_reason", skipped[0])
	}
	return delta, nil
}
