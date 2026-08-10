package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kweiza/flightdeck/internal/api"
	"github.com/kweiza/flightdeck/internal/ledger"
	"github.com/kweiza/flightdeck/internal/store"
)

// 판단 원장을 **주기적으로** 내보낸다 — 설계 §7 의 "매시간 판단 백업" 처방.
//
// ★ 왜 serve 안 티커인가. 셋 중에서 골랐다(호스트 cron · compose 두 번째 서비스 · 여기).
// selfwatch 가 정확히 이 모양의 선례다 — serve 가 소유하는 티커가 주기마다 실제 일을 한다.
// 호스트 cron 은 컨테이너 자족성을 깬다(이 배포는 `docker compose up -d` 하나가 전부다).
// 둘째 서비스는 잡 하나 때문에 배포 표면을 두 배로 만들고, 그 서비스도 자기 안에 주기
// 장치가 또 필요하다. 그리고 이 프로세스는 **이미 그 DB 를 쥐고 있다** — 다른 자리에서
// 돌리면 여는 쪽을 한 벌 더 만들고 잠금 모드를 또 정해야 한다.
//
// ★ 이 잡이 관측되는 유일한 자리는 **로그**다. /healthz 에 축을 안 냈다 — 그러면
// internal/api 표면이 늘고 이 항목의 경로 밖이다. 대신 회차마다 INFO(썼다/건너뛰었다)와
// 실패마다 ERROR 를 남긴다. **이것은 알고 남기는 구멍이다**: 잡이 조용히 실패하면
// `docker logs` 를 보기 전까지 아무도 모르고, 그동안 설계 문서는 "백업이 돈다"고 말한다.
// 후속 항목이 그 축을 낸다.
const ledgerBackupInterval = time.Hour

// LedgerOutDir 는 원장 산출물 자리를 정한다. 순수 함수다.
//
// ★ DB 와 **다른 볼륨**이어야 한다(설계 §7). compose.yaml 이 그 분리를 "백업 잡이 생기는
// 시점에" 로 접어 뒀고 그 시점이 여기다. 다만 정직하게 적는다 — 기본값은 DB 와 같은
// 디스크의 형제 디렉토리다. 진짜 분리는 FD_LEDGER 를 다른 매체로 겨눠야 성립한다.
// 이 함수가 사는 것은 **마운트를 가를 수 있는 자리**이지 분리 그 자체가 아니다.
func LedgerOutDir(get func(string) (string, bool), home string, dataDirExists bool) string {
	if v, ok := get("FD_LEDGER"); ok && strings.TrimSpace(v) != "" {
		return filepath.Clean(v)
	}
	if dataDirExists {
		return "/ledger"
	}
	if strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".flightdeck-ledger")
	}
	return filepath.Join(os.TempDir(), "flightdeck-ledger")
}

// ledgerDataUnchanged 는 이 회차의 데이터 파일이 자리의 것과 바이트 동일한지 본다.
//
// ★ 매니페스트는 뺀다. exported_at 이 회차마다 새로 찍혀서 **내용이 하나도 안 바뀐
// 회차도 매니페스트만은 늘 다르다.** 그것까지 세면 이 비교가 언제나 거짓이 되고,
// 안 바뀐 원장을 매시간 다시 쓰게 된다(그리고 ③ 매시간 git 커밋이 붙는 날에는
// 무의미한 커밋이 매시간 쌓인다 — 결정적 출력을 계약으로 삼은 이유가 그것인데
// 매니페스트가 그 계약 밖에 있다).
//
// 하나라도 못 읽으면 "바뀌었다"로 본다. 자리가 비었거나 반쯤 덮인 상태가 그것이고,
// 둘 다 다시 쓰는 것이 옳다.
func ledgerDataUnchanged(files map[string][]byte, dir string) bool {
	seen := 0
	for name, want := range files {
		if name == ledger.ManifestName {
			continue
		}
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, want) {
			return false
		}
		seen++
	}
	// 데이터 파일이 하나도 없는 인코딩은 이 잡의 입력이 아니다 — 같다고 말하면
	// 빈 자리를 "이미 최신"으로 오인한다.
	return seen > 0
}

// ledgerBackupOnce 는 한 회차다. 실제로 썼으면 true 를 낸다.
//
// ★ store.Open 을 다시 안 부른다. 이 프로세스가 쥔 핸들을 그대로 쓴다 —
// ReadLedger 는 살아 있는 DB 에서 부르도록 설계돼 있고(한 트랜잭션 안에서 여섯 표),
// 여기서 OpenLedger 를 또 열면 같은 파일에 커넥션이 두 벌 생긴다.
func ledgerBackupOnce(ctx context.Context, st *store.Store, outDir, at string) (map[string][]byte, bool, error) {
	dump, err := st.ReadLedger(ctx)
	if err != nil {
		return nil, false, err
	}
	files, _, err := ledger.Encode(dump, store.SchemaVersion, at)
	if err != nil {
		return nil, false, err
	}
	if ledgerDataUnchanged(files, outDir) {
		return files, false, nil
	}
	if _, err := ledger.Write(files, outDir); err != nil {
		return nil, false, err
	}
	return files, true, nil
}

// ledgerBackupState 는 마지막 회차의 관측이다 — cmd/fd 안의 표현이다.
type ledgerBackupState struct {
	lastAt  time.Time // 제로값 = 아직 한 회차도 안 돌았다
	outcome string    // wrote | unchanged | failed
	detail  string
	route   string
	journal string // committed | unchanged | failed: <사유> · 빈 값 = 시도 안 함
}

// ledgerBackupJob 은 주기 잡이고 **자기 마지막 회차를 기억한다.**
//
// ★ 기억이 왜 필요한가. 잡을 세우면서 위험이 옮겨갔다 — "아무도 안 부른다"에서
// "도는 줄 알았는데 조용히 실패한다"로. 로그만 남기면 `docker logs` 를 뒤지기 전까지
// 아무도 모르고, 그동안 설계 §7 은 "매시간 자동 실행: 있음"이라고 말한다.
type ledgerBackupJob struct {
	log   *slog.Logger
	st    *store.Store
	route string
	every time.Duration

	mu    sync.Mutex
	state ledgerBackupState
}

func newLedgerBackupJob(log *slog.Logger, st *store.Store, route string, every time.Duration) *ledgerBackupJob {
	return &ledgerBackupJob{log: log, st: st, route: route, every: every,
		state: ledgerBackupState{route: route}}
}

// State 는 마지막 회차의 관측이다. 잠금 밖으로 값을 복사해 낸다.
func (j *ledgerBackupJob) State() ledgerBackupState {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state
}

// ledgerBackupStatusOf 는 잡의 관측을 API 표면의 모양으로 옮긴다. 순수 함수다.
//
// ★ **클로저 안에 두지 않는다.** 앞선 물결이 자동 갱신 축에서 정확히 이 자리를 실측으로
// 잃었다(2026-08-07): 조립·선 넘기기·화면은 각자 잠겨 있었는데 **그 셋을 잇는 변환만**
// 아무 시험에도 안 걸려서, 판정은 살아 있고 값만 안 도착하는 상태가 조용히 났다.
// serve.go 의 selfUpdateStatusOf 주석이 그 실측을 적어 뒀고, 이 함수는 같은 규율이다.
//
// ★ LastAt 이 유일한 비자명 갈래다 — 이쪽은 time.Time(제로 = 회차 없음), api 는
// *time.Time(nil = 회차 없음)이다. IsZero() 로 가른다. 값 그대로 주소를 넘기면
// "회차 없음"이 1970년으로 실려 나가고, omitempty 는 nil 만 보므로 그 거짓을 못 거른다.
//
// 필드를 여기서 걸러내지 않는다 — 판정은 전부 상류(회차)에 살고, 여기서 또 고르면
// 같은 판정이 두 벌이 된다.
func ledgerBackupStatusOf(s ledgerBackupState) api.LedgerBackupStatus {
	out := api.LedgerBackupStatus{
		Running: true,
		Outcome: s.outcome,
		Detail:  s.detail,
		Route:   s.route,
		Journal: s.journal,
	}
	if !s.lastAt.IsZero() {
		at := s.lastAt
		out.LastAt = &at
	}
	return out
}

// tick 은 한 회차를 돌고 그 결과를 기억한다.
func (j *ledgerBackupJob) tick(ctx context.Context, at time.Time) {
	files, wrote, err := ledgerBackupOnce(ctx, j.st, j.route, at.UTC().Truncate(time.Microsecond).Format(stampLayout))
	next := ledgerBackupState{lastAt: at, route: j.route}
	switch {
	case err != nil:
		next.outcome, next.detail = "failed", err.Error()
		j.log.Error("판단 원장 백업 실패 — 다음 회차에 다시 시도한다",
			"route", clip(j.route, 200), "error", err.Error())
	case wrote:
		next.outcome = "wrote"
		j.log.Info("판단 원장 백업", "route", clip(j.route, 200), "outcome", "wrote")
	default:
		next.outcome = "unchanged"
		j.log.Info("판단 원장 백업", "route", clip(j.route, 200), "outcome", "unchanged")
	}

	// ★ 파일을 안 썼어도 저널은 **항상** 시도한다. 트리 비교가 실제 변경만 쌓게 하고,
	//   그래야 "파일은 이미 있는데 저널만 비어 있는" 판(이 기능이 붙은 첫 회차가 정확히
	//   그 모양이다)이 스스로 낫는다. 여기서 wrote 로 가르면 그 판은 영영 안 쌓인다.
	//
	// ★ 저널 실패가 백업 자체를 실패로 만들지 않는다. JSONL 은 이미 착지했고 그것이
	//   복원의 정본이다 — 역사는 그 위에 얹는 것이라 없다고 원장이 무효가 되지 않는다.
	//   대신 그 사실을 축에 남긴다(안 남기면 이 항목이 고친 침묵이 한 칸 옆에서 재발한다).
	if files != nil {
		names := make([]string, 0, len(files))
		for n := range files {
			names = append(names, n)
		}
		committed, jerr := commitLedgerGeneration(ctx,
			filepath.Join(j.route, journalRepoName), j.route, names, at)
		switch {
		case jerr != nil:
			next.journal = "failed: " + jerr.Error()
			j.log.Error("판단 원장 저널 커밋 실패 — JSONL 은 착지했다",
				"route", clip(j.route, 200), "error", jerr.Error())
		case committed:
			next.journal = "committed"
		default:
			next.journal = "unchanged"
		}
	}

	j.mu.Lock()
	j.state = next
	j.mu.Unlock()
}

// Run 은 주기 잡이다. ctx 가 끝나면 돌아온다.
//
// ★ 기동 직후에 한 번 돈다. 그래야 서버가 오래 안 떠 있던 판에서도 최신 원장이 곧바로
// 생기고, 안 바뀐 회차는 비교가 걸러 준다(읽기와 인코딩만 돌고 쓰기는 없다).
//
// ★ 실패해도 서버를 안 죽인다. 백업이 실패했다고 판단을 못 받는 것이 더 나쁘다 —
// 그 실패는 ERROR 와 /healthz 의 outcome=failed 로 남고 다음 회차가 다시 시도한다.
func (j *ledgerBackupJob) Run(ctx context.Context) {
	j.tick(ctx, time.Now())
	t := time.NewTicker(j.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			j.tick(ctx, time.Now())
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 세대를 bare 레포에 쌓는다 — 지금 자리는 **마지막 한 장**만 산다
// ─────────────────────────────────────────────────────────────────────────────
//
// ★ 왜 bare 인가. `legacy.JudgeOutTarget` 은 **git 작업 트리**에 쓰는 것을 금지하고
// `ForceAllows` 도 그 코드를 안 뚫는다. 그 가드가 막는 것은 판단 본문 몇 MB 가 남의 레포
// 작업본에 쌓여 누군가의 `git add .` 에 딸려 들어가는 사고다. **bare 레포에는 작업 트리가
// 없다** — 그 관심사 자체가 성립하지 않으므로 가드를 한 글자도 안 건드리고 지나간다.
// 예외를 두어 가드를 약화시키는 길(후보 (b))을 안 고른 이유가 이것이다.
//
// ★ `internal/gitreader` 의 "읽기 전용" 규율과도 안 부딪힌다. 그 규율의 대상은 이 서버가
// **관측하는** 저장소다(설계 §4 — 파생은 관측이지 조작이 아니다). 이 레포는 이 잡이
// 만들고 소유하는 산출물이라 관측 대상이 아니다.
//
// ★ 디렉토리 이름으로 세대를 남기는 길(후보 (c))을 안 고른 이유: 회차마다 몇 MB 가 통째로
// 복제되고 보관 정책이라는 결정이 새로 생긴다. git 은 내용 주소라 안 바뀐 파일을 공짜로
// 공유하고, 우리는 이미 "안 바뀌면 안 쓴다"를 쓰고 있어 커밋도 실제 변경에만 쌓인다.
const journalRepoName = "journal.git"

// gitBinName 은 부를 git 이다. 이미지에 alpine+git 이 들어 있고(Dockerfile), 이 서버의
// 파생이 이미 그것에 의존한다 — 새 외부 의존이 아니다.
const gitBinName = "git"

// runGit 은 bare 레포에 대해 git 하나를 돌린다. stdin 을 주면 그것을 먹인다.
func runGit(ctx context.Context, repoDir string, stdin []byte, env []string, args ...string) (string, error) {
	full := append([]string{"--git-dir=" + repoDir}, args...)
	cmd := exec.CommandContext(ctx, gitBinName, full...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	// ★ 환경을 통째로 비운다. 호스트의 GIT_* 가 새면 커밋의 저자·시각이 회차마다 흔들리고,
	//   그러면 "안 바뀌면 커밋이 없다"는 성질을 사람이 못 믿게 된다.
	cmd.Env = append([]string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null"}, env...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err,
			clip(strings.TrimSpace(errb.String()), 300))
	}
	return strings.TrimSpace(string(out)), nil
}

// commitLedgerGeneration 은 이 회차의 파일들을 bare 레포에 한 커밋으로 쌓는다.
// 실제로 커밋을 만들었으면 true 를 낸다.
//
// ★ **트리가 부모와 같으면 커밋을 안 만든다.** 상류가 이미 "데이터가 안 바뀌면 안 쓴다"로
// 거르지만, 이 비교는 그것과 독립이어야 한다 — 손으로 파일을 되돌린 판이나 상류 판정이
// 틀어진 판에서도 빈 커밋이 안 쌓여야 한다.
//
// ★ 작업 트리를 안 만든다. hash-object → mktree → commit-tree → update-ref 는 전부
// 인덱스도 체크아웃도 없이 도는 배관이다.
//
// ★ **디스크에 실제로 실린 바이트를 쌓는다** — 갓 인코딩한 것이 아니다. 매니페스트의
// exported_at 은 회차마다 새로 찍히므로, 인코딩 결과를 쌓으면 내용이 하나도 안 바뀐
// 회차도 트리가 달라져 **매시간 무의미한 커밋이 쌓인다**(원 설계가 걱정한 그 모양이다).
// 자리에 안 쓴 회차는 자리가 그대로이므로 트리도 그대로다 — 그것이 옳다. 저널은
// "이 도구가 무엇을 인코딩했나"가 아니라 **"자리에 무엇이 실려 있었나"**의 역사다.
func commitLedgerGeneration(ctx context.Context, repoDir, srcDir string, names []string, at time.Time) (bool, error) {
	if _, err := os.Stat(filepath.Join(repoDir, "HEAD")); err != nil {
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			return false, fmt.Errorf("저널 레포 디렉토리 생성 실패(%q): %w", clip(repoDir, 200), err)
		}
		if _, err := runGit(ctx, repoDir, nil, nil, "init", "--bare", "-q", "--initial-branch=main"); err != nil {
			return false, err
		}
	}

	sorted := append([]string(nil), names...)
	sort.Strings(sorted) // mktree 입력은 정렬돼야 하고, 결정적이어야 한다

	var tree bytes.Buffer
	for _, n := range sorted {
		body, rerr := os.ReadFile(filepath.Join(srcDir, n))
		if rerr != nil {
			return false, fmt.Errorf("저널에 실을 파일을 못 읽었다(%q): %w", clip(n, 120), rerr)
		}
		blob, err := runGit(ctx, repoDir, body, nil, "hash-object", "-w", "--stdin")
		if err != nil {
			return false, err
		}
		fmt.Fprintf(&tree, "100644 blob %s\t%s\n", blob, n)
	}
	treeSHA, err := runGit(ctx, repoDir, tree.Bytes(), nil, "mktree")
	if err != nil {
		return false, err
	}

	// 부모가 있으면 그 트리와 견준다. 없으면(첫 커밋) rev-parse 가 실패하는 것이 정상이다.
	parent, _ := runGit(ctx, repoDir, nil, nil, "rev-parse", "--verify", "-q", "refs/heads/main^{commit}")
	if parent != "" {
		if prev, perr := runGit(ctx, repoDir, nil, nil, "rev-parse", "--verify", "-q", parent+"^{tree}"); perr == nil && prev == treeSHA {
			return false, nil
		}
	}

	stamp := at.UTC().Format(time.RFC3339)
	env := []string{
		"GIT_AUTHOR_NAME=flightdeck", "GIT_AUTHOR_EMAIL=flightdeck@localhost",
		"GIT_COMMITTER_NAME=flightdeck", "GIT_COMMITTER_EMAIL=flightdeck@localhost",
		"GIT_AUTHOR_DATE=" + stamp, "GIT_COMMITTER_DATE=" + stamp,
	}
	args := []string{"commit-tree", treeSHA, "-m", "판단 원장 " + stamp}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	commit, err := runGit(ctx, repoDir, nil, env, args...)
	if err != nil {
		return false, err
	}
	if _, err := runGit(ctx, repoDir, nil, nil, "update-ref", "refs/heads/main", commit); err != nil {
		return false, err
	}
	return true, nil
}
