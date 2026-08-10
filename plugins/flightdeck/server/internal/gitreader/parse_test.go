package gitreader

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// 이 파일의 시험은 **판정 함수를 직접 부른다.** 상위 메서드를 통해서만 보면
// 그 시험은 결국 로직의 사본을 단정하게 되고, 그러면 변이가 조용히 새어 나간다.
//
// 표에는 실측으로 확인한 실물 문자열이 들어간다(probe 로 git 2.43 에서 찍어 온 것).
// 표 밖 케이스도 함께 넣는다 — 표 안만 보면 "내가 만든 문자열을 내가 파싱한다"가 된다.

func TestClassifyAncestry(t *testing.T) {
	cases := []struct {
		name       string
		exit       int
		stderr     string
		wantResult judge.AncestryResult
		wantOK     bool
	}{
		// ── git 2.43 실측 ──
		{"조상", 0, "", judge.AncestryYes, true},
		{"조상 아님", 1, "", judge.AncestryNo, true},
		{"그런 ref 없음(merge-base 문구)", 128, "fatal: Not a valid object name nosuchref", judge.AncestryBadRef, true},
		{"저장소가 아님", 128, "fatal: not a git repository (or any of the parent directories): .git", judge.AncestryUnknown, false},

		// ── 판 차이로 문구가 흔들리는 자리 ──
		{"log 판 문구", 128, "fatal: bad revision 'nope'", judge.AncestryBadRef, true},
		{"rev-parse 판 문구", 128, "fatal: ambiguous argument 'nope': unknown revision or path not in the working tree.", judge.AncestryBadRef, true},
		{"대소문자 무관", 128, "FATAL: NOT A VALID OBJECT NAME X", judge.AncestryBadRef, true},

		// ── 표 밖: 넷째 경우를 셋 중 하나로 접지 않는다 ──
		{"소유권 거부(128이지만 저장소 문제)", 128, "fatal: detected dubious ownership in repository at '/x'", judge.AncestryUnknown, false},
		{"128인데 아는 문구가 하나도 없다", 128, "fatal: 무언가 새로운 실패", judge.AncestryUnknown, false},
		{"규약 밖 종료코드 129(옵션 오류)", 129, "usage: git merge-base ...", judge.AncestryUnknown, false},
		{"프로세스를 못 띄웠다(-1)", -1, "", judge.AncestryUnknown, false},
		{"시그널로 죽었다(-1, stderr 있음)", -1, "killed", judge.AncestryUnknown, false},

		// ── 1 인데 stderr 가 있으면 답은 그대로, 사유에 남긴다 ──
		{"1 + 경고", 1, "warning: 무언가", judge.AncestryNo, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyAncestry(c.exit, c.stderr)
			if got.Result != c.wantResult || got.OK != c.wantOK {
				t.Errorf("classifyAncestry(%d, %q) = {%v %v}, want {%v %v}",
					c.exit, c.stderr, got.Result, got.OK, c.wantResult, c.wantOK)
			}
			// 사유가 비면 "조건 A 때문"과 "이 축을 안 본다"가 구분되지 않는다.
			if strings.TrimSpace(got.Reason) == "" {
				t.Errorf("사유가 비었다: classifyAncestry(%d, %q)", c.exit, c.stderr)
			}
			// stderr 가 있었으면 사유가 그것을 날라야 한다 — "status 128" 만 남으면
			// 무엇이 틀렸는지 영영 모른다.
			if c.stderr != "" && !strings.Contains(got.Reason, c.stderr) {
				t.Errorf("사유에 stderr 가 없다: %q (stderr=%q)", got.Reason, c.stderr)
			}
		})
	}
}

func TestClassifyAncestryFailureIsNotAVerdict(t *testing.T) {
	// 오류로 올릴 때의 Result 는 셋 중 어느 것도 아니어야 한다.
	// 이 단정이 없으면 "오류인데 값이 AncestryNo" 라는 조합이 조용히 통과하고,
	// 오류를 무시한 호출자에게 그것은 "기다리면 풀린다"로 보인다.
	for _, exit := range []int{-1, 2, 128, 255} {
		v := classifyAncestry(exit, "fatal: not a git repository")
		if v.OK {
			continue
		}
		if v.Result == judge.AncestryYes || v.Result == judge.AncestryNo || v.Result == judge.AncestryBadRef {
			t.Errorf("exit=%d: 오류인데 판정값 %v 를 냈다", exit, v.Result)
		}
	}
}

func TestHeadUnbornReason(t *testing.T) {
	const unbornErr = "fatal: bad revision 'HEAD'" // git 2.43 실측(갓 init 한 저장소)
	cases := []struct {
		name     string
		branches int
		stderr   string
		want     bool
	}{
		{"커밋 없는 새 저장소", 0, unbornErr, true},
		{"커밋 없음을 명시하는 판", 0, "fatal: your current branch 'main' does not have any commits yet", true},

		// ── 표 밖: 축 하나만 참이면 거짓이어야 한다 ──
		{"브랜치가 있는데 HEAD 가 안 읽힌다 = 진짜 고장", 3, unbornErr, false},
		{"브랜치 0건이지만 저장소가 아니다", 0, "fatal: not a git repository (or any of the parent directories): .git", false},
		{"브랜치 0건이지만 사유를 모르겠다", 0, "fatal: 무언가 새로운 실패", false},
		{"브랜치 0건 + stderr 없음(무엇도 안 말했다)", 0, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := headUnbornReason(c.branches, c.stderr)
			if got != c.want {
				t.Errorf("headUnbornReason(%d, %q) = %v, want %v (사유: %s)", c.branches, c.stderr, got, c.want, reason)
			}
			if strings.TrimSpace(reason) == "" {
				t.Errorf("사유가 비었다: headUnbornReason(%d, %q)", c.branches, c.stderr)
			}
		})
	}
}

func TestGitEnv(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"GIT_DIR=/somewhere/.git",
		"GIT_WORK_TREE=/somewhere",
		"GIT_INDEX_FILE=/somewhere/.git/index",
		"GIT_TERMINAL_PROMPT=1",  // 우리 값으로 덮여야 한다
		"GIT_WORK_TREEX=/keepme", // 표 밖: 접두가 같을 뿐인 남의 변수는 살아야 한다
		"MY_GIT_DIR=/keepme",     // 표 밖: 이름이 포함만 되는 것도 살아야 한다
		"git_dir=/keepme",        // 표 밖: 유닉스 환경변수는 대소문자를 가른다
	}
	got := gitEnv(in)

	index := map[string]string{}
	count := map[string]int{}
	for _, kv := range got {
		name, val, _ := strings.Cut(kv, "=")
		index[name] = val
		count[name]++
	}

	for _, gone := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		if _, ok := index[gone]; ok {
			t.Errorf("%s 가 안 지워졌다 — 부모의 저장소를 읽게 된다", gone)
		}
	}
	for _, kept := range []string{"PATH", "GIT_WORK_TREEX", "MY_GIT_DIR", "git_dir"} {
		if _, ok := index[kept]; !ok {
			t.Errorf("%s 가 사라졌다 — 이름이 정확히 일치할 때만 지워야 한다", kept)
		}
	}
	if index["GIT_TERMINAL_PROMPT"] != "0" {
		t.Errorf("GIT_TERMINAL_PROMPT=%q, want 0", index["GIT_TERMINAL_PROMPT"])
	}
	if index["GIT_OPTIONAL_LOCKS"] != "0" {
		t.Errorf("GIT_OPTIONAL_LOCKS=%q, want 0", index["GIT_OPTIONAL_LOCKS"])
	}
	// 같은 이름이 두 번 있으면 어느 값이 이기는지 판·플랫폼마다 다르다.
	// "우리 값이 뒤에 있으니 이긴다"에 기대지 않는다.
	for _, k := range []string{"GIT_TERMINAL_PROMPT", "GIT_OPTIONAL_LOCKS"} {
		if count[k] != 1 {
			t.Errorf("%s 가 %d번 있다 — 정확히 1번이어야 한다", k, count[k])
		}
	}
}

func TestValidateRev(t *testing.T) {
	cases := []struct {
		name    string
		rev     string
		wantErr bool
	}{
		{"보통 브랜치", "main", false},
		{"HEAD", "HEAD", false},
		{"상대 좌표", "HEAD~2", false},
		{"sha", "797497178513483680e6dcde0409378390bc0f74", false},
		{"세 점", "a...b", false},
		{"슬래시", "feat/x", false},

		// ── 소비 계층(git 명령줄)의 문법으로 막는다 ──
		{"빈 값", "", true},
		{"공백만", "   ", true},
		{"옵션으로 읽히는 값", "--help", true},
		{"짧은 옵션", "-n", true},
		{"가운데 공백", "a b", true},
		{"개행", "a\nb", true},
		{"NUL", "a\x00b", true},
		{"탭", "a\tb", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateRev("ref", c.rev)
			if (err != nil) != c.wantErr {
				t.Errorf("validateRev(%q) err=%v, wantErr=%v", c.rev, err, c.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "ref") {
				t.Errorf("오류에 어느 인자인지가 없다: %v", err)
			}
		})
	}
}

func TestSanitizeExcerpt(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"그대로", "fatal: bad revision 'x'", 400, "fatal: bad revision 'x'"},
		{"개행은 공백으로", "fatal: a\nb", 400, "fatal: a b"},
		{"제어문자는 걷어낸다", "fat\x07al\x1b[31m", 400, "fatal[31m"},
		{"한글이 안 깨진다", "잠금 사유: 실험 중", 400, "잠금 사유: 실험 중"},
		{"앞뒤 공백", "  x  ", 400, "x"},
		{"룬 단위로 자른다", "가나다라마", 3, "가나다…"},
		{"빈 입력", "", 400, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeExcerpt([]byte(c.in), c.max); got != c.want {
				t.Errorf("sanitizeExcerpt(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
			}
		})
	}

	// 표 밖: 깨진 UTF-8 이 그대로 로그에 실리면 그 줄이 통째로 못 읽는 것이 된다.
	if got := sanitizeExcerpt([]byte{0xff, 0xfe, 'a'}, 400); !strings.HasSuffix(got, "a") {
		t.Errorf("깨진 UTF-8 처리 결과가 이상하다: %q", got)
	}
	// 표 밖: 절단은 바이트가 아니라 룬이어야 한다(한글 1자 = 3바이트).
	if got := sanitizeExcerpt([]byte("가나다라마"), 2); got != "가나…" {
		t.Errorf("바이트로 잘렸다: %q", got)
	}
}

func TestParseForEachRef(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	// git 2.43 실측 형태: 필드 3개가 NUL 로 끝나고 레코드마다 개행이 덧붙는다.
	out := "feat\x00" + strings.Repeat("a", 40) + "\x00두 번째\x00\n" +
		"main\x00" + strings.Repeat("b", 40) + "\x00첫 번째\x00\n"
	got, err := parseForEachRef([]byte(out), now)
	if err != nil {
		t.Fatalf("해석 실패: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("2건을 기대했는데 %d건: %+v", len(got), got)
	}
	if got[0].Ref != "feat" || got[0].Subject != "두 번째" || got[0].SHA != strings.Repeat("a", 40) {
		t.Errorf("첫 레코드가 틀렸다: %+v", got[0])
	}
	// 레코드 사이의 개행이 다음 ref 이름에 눌어붙으면 안 된다.
	if got[1].Ref != "main" {
		t.Errorf("둘째 ref 이름에 개행이 붙었다: %q", got[1].Ref)
	}
	// At 은 관측 시각이다(커밋 시각이 아니다). 화면의 신선도 표시가 이 축을 쓴다.
	if !got[0].At.Equal(now) {
		t.Errorf("At 이 관측 시각이 아니다: %v", got[0].At)
	}
	// 이 패키지는 프로젝트가 무엇인지 모른다. 저장 계층이 채운다.
	if got[0].Project != "" {
		t.Errorf("Project 를 채웠다: %q", got[0].Project)
	}

	if refs, err := parseForEachRef(nil, now); err != nil || len(refs) != 0 {
		t.Errorf("빈 출력 = 브랜치 0건이어야 한다: %v %v", refs, err)
	}

	// 표 밖: 필드가 모자란 꼬리를 조용히 버리면 포맷이 바뀐 날 목록이 짧아진 채 초록이 된다.
	bad := "main\x00" + strings.Repeat("b", 40) + "\x00주제\x00\nfeat\x00deadbee"
	if _, err := parseForEachRef([]byte(bad), now); err == nil {
		t.Error("잘린 꼬리를 오류로 안 냈다")
	}
	// 표 밖: sha 자리에 sha 가 아닌 것이 오면(포맷 오타) 그 자리에서 걸려야 한다.
	if _, err := parseForEachRef([]byte("main\x00refs/heads/main\x00주제\x00\n"), now); err == nil {
		t.Error("sha 자리의 비16진수를 안 걸렀다")
	}
	// 표 밖: 제목에 개행이 들어와도 필드 경계는 NUL 이므로 제목 안에 남아야 한다.
	nl := "main\x00" + strings.Repeat("b", 40) + "\x00두\n줄\x00\n"
	refs, err := parseForEachRef([]byte(nl), now)
	if err != nil || len(refs) != 1 || refs[0].Subject != "두\n줄" {
		t.Errorf("제목의 개행이 레코드 경계로 읽혔다: %+v %v", refs, err)
	}
}

func TestParseLogRecord(t *testing.T) {
	sha := strings.Repeat("a", 40)
	gotSHA, subj, err := parseLogRecord([]byte(sha + "\x00두 번째 커밋\n"))
	if err != nil || gotSHA != sha || subj != "두 번째 커밋" {
		t.Fatalf("= %q %q %v", gotSHA, subj, err)
	}
	// 빈 커밋 메시지는 유효하다 — 오류가 아니다.
	if _, s, err := parseLogRecord([]byte(sha + "\x00\n")); err != nil || s != "" {
		t.Errorf("빈 제목을 오류로 냈다: %q %v", s, err)
	}
	// 표 밖: 구조가 다르면 조용히 0값을 내지 않는다.
	for _, bad := range []string{"", "\n", sha, sha + "\x00a\x00b", "zzz\x00제목"} {
		if _, _, err := parseLogRecord([]byte(bad)); err == nil {
			t.Errorf("%q 를 오류로 안 냈다", bad)
		}
	}
}

func TestParseNameOnlyZ(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"보통", "a.txt\x00b/c.go\x00", []string{"a.txt", "b/c.go"}},
		{"빈 출력", "", nil},
		{"공백 든 경로", "with space.txt\x00", []string{"with space.txt"}},
		{"유니코드 경로", "문서/설계.md\x00", []string{"문서/설계.md"}},
		// 표 밖: -z 를 쓰는 이유 자체. 줄 단위로 읽으면 이 한 경로가 두 건이 된다.
		{"개행 든 경로", "new\nline.txt\x00b.txt\x00", []string{"new\nline.txt", "b.txt"}},
		{"따옴표 든 경로", "a\"b.txt\x00", []string{"a\"b.txt"}},
		{"마지막 NUL 없음", "a.txt", []string{"a.txt"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseNameOnlyZ([]byte(c.in))
			if len(got) != len(c.want) {
				t.Fatalf("= %q, want %q", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestParseAheadBehind(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantA      int
		wantB      int
		wantErr    bool
		wantErrHas string
	}{
		{name: "보통", in: "3\t1\n", wantA: 3, wantB: 1},
		{name: "둘 다 0", in: "0\t0\n", wantA: 0, wantB: 0},
		{name: "개행 없음", in: "2\t5", wantA: 2, wantB: 5},

		// ── 표 밖: 이 레포에서 실제로 났던 결함의 모양 ──
		// 원격 `grep -c` 가 0건일 때 종료코드 1이라 `|| echo 0` 이 값을 "0\n0" 으로 만들었고,
		// 필드만 세는 파서는 그것을 (0,0) 으로 통과시켰다 — 그래서 줄 수부터 단정한다.
		{name: "두 줄로 온 0", in: "0\n0\n", wantErr: true, wantErrHas: "줄"},
		{name: "빈 출력", in: "", wantErr: true},
		{name: "공백만", in: "  \n", wantErr: true},
		{name: "필드 1개", in: "3\n", wantErr: true},
		{name: "필드 3개", in: "1\t2\t3\n", wantErr: true},
		{name: "정수가 아님", in: "a\tb\n", wantErr: true},
		{name: "공백 구분(탭이 아님)", in: "3 1\n", wantErr: true},
		{name: "음수", in: "-1\t2\n", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, b, err := parseAheadBehind([]byte(c.in))
			if (err != nil) != c.wantErr {
				t.Fatalf("parseAheadBehind(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
			}
			if err != nil {
				if c.wantErrHas != "" && !strings.Contains(err.Error(), c.wantErrHas) {
					t.Errorf("오류 문구에 %q 가 없다: %v", c.wantErrHas, err)
				}
				return
			}
			if a != c.wantA || b != c.wantB {
				t.Errorf("parseAheadBehind(%q) = (%d, %d), want (%d, %d)", c.in, a, b, c.wantA, c.wantB)
			}
		})
	}
}

func TestParseStatusZ(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		// git 2.43 실측 형태
		{"수정·이름변경·미추적", " M b.txt\x00R  renamed a.txt\x00a.txt\x00?? sub/\x00?? untracked.txt\x00",
			[]string{"b.txt", "renamed a.txt", "a.txt", "sub/", "untracked.txt"}},
		{"스테이징 추가", "A  new.go\x00", []string{"new.go"}},
		{"양쪽 다 변경", "MM x.go\x00", []string{"x.go"}},
		{"빈 출력", "", nil},
		{"개행 든 경로", "?? ne\nwline.txt\x00", []string{"ne\nwline.txt"}},
		{"유니코드 경로", "?? sub/새 파일.txt\x00", []string{"sub/새 파일.txt"}},
		// 표 밖: 같은 경로가 두 번 와도 발자국은 한 번이다.
		{"중복", " M a.txt\x00 M a.txt\x00", []string{"a.txt"}},
		// 표 밖: 복사(C)도 원본 토큰이 따라온다.
		{"복사", "C  new.txt\x00old.txt\x00", []string{"new.txt", "old.txt"}},
		// 표 밖: 미스테이징 이름 변경(둘째 칸이 R)
		{"미스테이징 이름변경", " R new.txt\x00old.txt\x00", []string{"new.txt", "old.txt"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseStatusZ([]byte(c.in))
			if err != nil {
				t.Fatalf("해석 실패: %v", err)
			}
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Errorf("parseStatusZ(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}

	// 표 밖: 구조 위반은 오류다. 조용히 넘기면 그 뒤 항목 전부가 한 칸씩 밀린 채 통과한다.
	for _, bad := range []string{
		"R  new.txt\x00",     // 이름 변경인데 원본 토큰이 없다
		"R  new.txt\x00\x00", // 원본 토큰이 비었다
		"M\x00",              // 너무 짧다
		"MMx.go\x00",         // 3번째가 공백이 아니다
	} {
		if _, err := parseStatusZ([]byte(bad)); err == nil {
			t.Errorf("%q 를 오류로 안 냈다", bad)
		}
	}
}

func TestParseWorktreeList(t *testing.T) {
	// git 2.43 의 `worktree list --porcelain -z` 실측 형태.
	// -z 라 잠금 사유가 C-quote 되지 않고 한글 그대로 온다.
	sha := strings.Repeat("a", 40)
	out := "worktree /repo\x00HEAD " + sha + "\x00branch refs/heads/main\x00\x00" +
		"worktree /wt-gone\x00HEAD " + sha + "\x00branch refs/heads/gone\x00prunable gitdir file points to non-existent location\x00\x00" +
		"worktree /wt-locked\x00HEAD " + sha + "\x00branch refs/heads/feat\x00locked 실험 중\x00\x00" +
		"worktree /wt-detached\x00HEAD " + sha + "\x00detached\x00\x00"

	got, err := parseWorktreeList([]byte(out))
	if err != nil {
		t.Fatalf("해석 실패: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("4건을 기대했는데 %d건: %+v", len(got), got)
	}
	if got[0].Path != "/repo" || got[0].Branch != "refs/heads/main" || got[0].Locked || got[0].Prunable {
		t.Errorf("주 워크트리가 틀렸다: %+v", got[0])
	}
	if got[0].ShortBranch() != "main" {
		t.Errorf("축약 브랜치 = %q", got[0].ShortBranch())
	}
	if !got[1].Prunable || got[1].PrunableReason == "" {
		t.Errorf("prunable 사유가 없다: %+v", got[1])
	}
	if !got[2].Locked || got[2].LockReason != "실험 중" {
		t.Errorf("잠금 사유가 틀렸다: %+v", got[2])
	}
	if !got[3].Detached || got[3].Branch != "" {
		t.Errorf("detached 를 못 읽었다: %+v", got[3])
	}

	// 사유 없는 잠금은 유효하다(git worktree lock 은 사유가 선택이다).
	nr, err := parseWorktreeList([]byte("worktree /x\x00HEAD " + sha + "\x00locked\x00\x00"))
	if err != nil || len(nr) != 1 || !nr[0].Locked || nr[0].LockReason != "" {
		t.Errorf("사유 없는 잠금: %+v %v", nr, err)
	}

	// bare 저장소에는 HEAD 줄이 없다.
	b, err := parseWorktreeList([]byte("worktree /bare\x00bare\x00\x00"))
	if err != nil || len(b) != 1 || !b[0].Bare || b[0].HEAD != "" {
		t.Errorf("bare: %+v %v", b, err)
	}

	// 표 밖: 모르는 속성은 **버리지 않고** 남긴다. 조용히 버리면 git 이 형식을 넓힌 날
	// 우리가 무엇을 안 보는지 아무도 모르게 된다.
	e, err := parseWorktreeList([]byte("worktree /x\x00HEAD " + sha + "\x00newattr 무언가\x00\x00"))
	if err != nil {
		t.Fatalf("모르는 속성에서 죽었다: %v", err)
	}
	if len(e) != 1 || len(e[0].Extra) != 1 || e[0].Extra[0] != "newattr 무언가" {
		t.Errorf("모르는 속성을 안 남겼다: %+v", e)
	}

	// 표 밖: 구조 위반은 오류다. 여기서 안 걸면 레코드가 통째로 사라진 채 목록이 짧아진다.
	for _, bad := range []string{
		"HEAD " + sha + "\x00\x00",    // worktree 줄보다 속성이 먼저
		"worktree\x00\x00",            // 경로가 없다
		"worktree /x\x00HEAD zzz\x00", // sha 자리가 sha 가 아니다
	} {
		if _, err := parseWorktreeList([]byte(bad)); err == nil {
			t.Errorf("%q 를 오류로 안 냈다", bad)
		}
	}

	// 빈 출력에 워크트리가 0건인 것은 정상이 아니지만(주 워크트리는 항상 있다)
	// 그 판정은 호출자 몫이다 — 파서는 구조만 본다.
	if wts, err := parseWorktreeList(nil); err != nil || len(wts) != 0 {
		t.Errorf("빈 출력: %+v %v", wts, err)
	}
}

func TestParseNumstatZ(t *testing.T) {
	cases := []struct {
		name      string
		out       string
		wantPaths []string
		wantDelta map[string]model.LineDelta
		wantSkip  int
	}{
		{
			name:      "정상 레코드 셋",
			out:       "8\t0\t.gitignore\x001\t1\tplugin.json\x0012\t4\tcmds.go\x00",
			wantPaths: []string{".gitignore", "plugin.json", "cmds.go"},
			wantDelta: map[string]model.LineDelta{
				".gitignore":  {Added: 8, Removed: 0},
				"plugin.json": {Added: 1, Removed: 1},
				"cmds.go":     {Added: 12, Removed: 4},
			},
		},
		{
			// ★ 이진 파일은 **경로로는 남고 규모로는 안 남는다.** 그 파일이 바뀐 것은 사실이라
			// 겹침 축에서 빠지면 안 되고, 규모는 못 잰 것이라 0 으로 세면 안 된다.
			name:      "이진 파일은 경로만 남고 규모는 키가 없다",
			out:       "-\t-\tb.bin\x001\t0\tt.txt\x00",
			wantPaths: []string{"b.bin", "t.txt"},
			wantDelta: map[string]model.LineDelta{"t.txt": {Added: 1, Removed: 0}},
		},
		{
			name:      "빈 출력",
			out:       "",
			wantPaths: nil,
			wantDelta: map[string]model.LineDelta{},
		},
		{
			name:      "필드가 모자라면 그 레코드만 버린다",
			out:       "5\tx.go\x003\t1\ty.go\x00",
			wantPaths: []string{"y.go"},
			wantDelta: map[string]model.LineDelta{"y.go": {Added: 3, Removed: 1}},
			wantSkip:  1,
		},
		{
			name:      "수가 아닌 값이면 그 레코드만 버린다",
			out:       "a\t0\tx.go\x003\t1\ty.go\x00",
			wantPaths: []string{"y.go"},
			wantDelta: map[string]model.LineDelta{"y.go": {Added: 3, Removed: 1}},
			wantSkip:  1,
		},
		{
			// 경로에 공백·유니코드가 있어도 -z 라 구분자가 안 흔들린다.
			name:      "공백과 유니코드 경로",
			out:       "2\t0\tdocs/설계 노트.md\x00",
			wantPaths: []string{"docs/설계 노트.md"},
			wantDelta: map[string]model.LineDelta{"docs/설계 노트.md": {Added: 2, Removed: 0}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			paths, delta, skipped := parseNumstatZ([]byte(tc.out))
			if !reflect.DeepEqual(paths, tc.wantPaths) {
				t.Errorf("경로: %q 를 기대했는데 %q 다", tc.wantPaths, paths)
			}
			if len(delta) != len(tc.wantDelta) {
				t.Errorf("규모 %d건을 기대했는데 %d건이다: %v", len(tc.wantDelta), len(delta), delta)
			}
			for p, want := range tc.wantDelta {
				got, ok := delta[p]
				if !ok {
					t.Errorf("%q 의 규모 키가 없다", p)
					continue
				}
				if got != want {
					t.Errorf("%q: %+v 를 기대했는데 %+v 다", p, want, got)
				}
			}
			if len(skipped) != tc.wantSkip {
				t.Errorf("버린 레코드 %d건을 기대했는데 %d건이다: %v", tc.wantSkip, len(skipped), skipped)
			}
		})
	}
}

// ★ 이 시험이 이 계획의 논지를 지킨다. 이진 파일의 규모를 0 으로 접으면 여기가 빨개진다.
func TestParseNumstatZNeverReportsUnknownAsZero(t *testing.T) {
	_, delta, _ := parseNumstatZ([]byte("-\t-\tb.bin\x00"))
	if d, ok := delta["b.bin"]; ok {
		t.Fatalf("이진 파일의 규모 키가 생겼다(%+v) — 못 읽은 것은 키가 없어야 한다. "+
			"0 으로 접으면 화면이 '안 만졌다'라고 말하게 된다", d)
	}
}

// ★ 이 시험은 **파서가 무엇을 전제하는지**를 잠근다. 호출부에서 --no-renames 를 떼면
// numstat 이 rename 을 3필드(NUL 로 나뉜 원본·목적지)로 내는데, 그 형태가 이 파서에
// 들어오면 조용히 어긋난다. 그래서 그 형태가 오면 **버려진다는 것**을 여기 못박는다 —
// 버려지면 skipped 에 남아 WARN 이 뜨고, 조용한 손실이 아니게 된다.
func TestParseNumstatZAssumesNoRenames(t *testing.T) {
	// --no-renames 없이 나오는 형태: "3\t1\t\x00old.go\x00new.go\x00"
	paths, delta, skipped := parseNumstatZ([]byte("3\t1\t\x00old.go\x00new.go\x00"))
	if len(delta) != 0 {
		t.Errorf("rename 형태에서 규모가 나왔다: %v — 파서는 --no-renames 를 전제한다", delta)
	}
	if len(skipped) == 0 {
		t.Error("rename 형태가 조용히 버려졌다 — skipped 에 사유가 남아야 WARN 이 뜬다")
	}
	for _, p := range paths {
		if p == "" {
			t.Error("빈 경로가 경로 목록에 들어갔다")
		}
	}
}
