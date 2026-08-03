package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kweiza/flightdeck/internal/legacy"
	"github.com/kweiza/flightdeck/internal/store"
)

// `fd import` · `fd export --to-legacy` — 이관과 공존(설계 §9).
//
// ★ 이 둘은 **REST 를 치지 않고 DB 를 직접 연다.** 다른 클라이언트 명령과 다른 이유:
// 이관은 수만 행을 한 트랜잭션에 넣는 일회성 관리 조작이고, 되돌리기가
// "DB 파일 삭제 + 재실행"이라 서버와 같은 파일을 봐야 그 되돌리기가 성립한다.
// HTTP 로 쪼개 보내면 중간에 끊겼을 때 무엇이 들어갔는지가 원본과 DB 어디에도 안 남는다.
//
// ★ **원본에는 한 바이트도 쓰지 않는다.** 두 레포에서 살아 있는 병렬 세션이 일하고 있고,
// 원본을 안 건드리는 것이 위 되돌리기가 성립하는 유일한 근거다.

// openDB 는 이관용으로 DB 를 연다. serve 와 같은 자리를 고른다.
func openDB(env func(string) (string, bool), log *slog.Logger, dbFlag string) (*store.Store, string, error) {
	path := strings.TrimSpace(dbFlag)
	if path == "" {
		home, _ := os.UserHomeDir()
		_, derr := os.Stat("/data")
		path = DefaultDBPath(env, home, derr == nil)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, path, fmt.Errorf("DB 디렉토리를 만들지 못했다(%q): %w", path, err)
	}
	st, err := store.OpenWithLogger(path, log)
	if err != nil {
		return nil, path, fmt.Errorf("DB 를 열지 못했다(%q): %w", path, err)
	}
	return st, path, nil
}

// runImport 는 `fd import` 다.
//
// **기본값이 예행이다.** `--apply` 가 있어야 실제로 쓴다 —
// 이 레포는 이미 두 번 원문을 영구 소실했고, 되돌릴 수 없는 이관은 착수 자체가 안 된다.
func (a *App) runImport(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("import")
	code := fs.String("from-code", "", "코드 레포 루트(.claude/{sessions,queue,handoffs} 를 품은 곳)")
	docs := fs.String("from-docs", "", "문서 레포 루트(slides/status.html 을 품은 곳)")
	project := fs.String("project", "", "넣을 프로젝트 id(비면 FD_PROJECT 또는 코드 레포 디렉토리 이름)")
	dbPath := fs.String("db", "", "SQLite 파일 경로(비면 자동)")
	apply := fs.Bool("apply", false, "실제로 쓴다. 없으면 예행이다")
	dryRun := fs.Bool("dry-run", false, "예행(기본값이라 붙일 필요는 없다)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *apply && *dryRun {
		fmt.Fprintln(out, "--apply 와 --dry-run 을 함께 줬다 — 어느 쪽인지 알 수 없으므로 아무것도 하지 않는다")
		return 2
	}
	if strings.TrimSpace(*code) == "" && strings.TrimSpace(*docs) == "" {
		fmt.Fprintln(out, "원본이 없다 — --from-code 또는 --from-docs 중 하나는 있어야 한다")
		return 2
	}

	proj := strings.TrimSpace(*project)
	if proj == "" {
		proj = envOr(a.env, "FD_PROJECT", "")
	}
	if proj == "" && strings.TrimSpace(*code) != "" {
		proj = filepath.Base(filepath.Clean(*code))
	}
	if proj == "" {
		fmt.Fprintln(out, "프로젝트 id 를 정하지 못했다 — --project 로 줘라")
		return 2
	}

	sc, err := legacy.ScanSource(legacy.Source{CodeRoot: *code, DocsRoot: *docs})
	if err != nil {
		a.log.Error("원본을 훑지 못했다", "error", err.Error())
		fmt.Fprintf(out, "원본을 훑지 못했다: %s\n", err)
		return 1
	}
	plan := legacy.PlanImport(sc, legacy.PlanOptions{Project: proj})

	if !*apply {
		// ★ 여기서 **DB 를 열지도 않는다.** 예행이 파일을 만들면(WAL·백업·마이그레이션)
		//   "한 바이트도 안 건드린다"가 거짓이 되고, 그 거짓은 mtime·크기로 곧바로 드러난다.
		legacy.RenderPlan(out, plan, false)
		a.log.Info("이관 예행 완료", "mode", "dry-run", "targets", len(plan.Items),
			"count", len(plan.Handoffs), "skipped", len(plan.Gone), "failed", countFatal(plan))
		return 0
	}

	st, path, err := openDB(a.env, a.log, *dbPath)
	if err != nil {
		a.log.Error("이관용 DB 를 열지 못했다", "error", err.Error())
		fmt.Fprintf(out, "%s\n", err)
		return 1
	}
	defer func() {
		if cerr := st.Close(); cerr != nil {
			a.log.Error("DB 닫기 실패", "error", cerr.Error())
		}
	}()

	projectPath := *code
	if projectPath == "" {
		projectPath = *docs
	}
	applied, err := legacy.Apply(ctx, st, plan, projectPath)
	if err != nil {
		// 한 트랜잭션이므로 실패하면 아무것도 안 들어갔다. 그 사실을 같은 줄에 적는다 —
		// 안 적으면 운영자가 반쯤 들어간 DB 를 의심하며 손으로 뒤진다.
		a.log.Error("이관 적용 실패 — 한 트랜잭션이라 아무것도 들어가지 않았다",
			"db_path", path, "error", err.Error())
		fmt.Fprintf(out, "이관 실패(한 트랜잭션이라 DB 는 그대로다): %s\n", err)
		return 1
	}
	legacy.RenderPlan(out, plan, true)
	fmt.Fprintf(out, "\n넣음: 세션 %d · 항목 %d · 판단 %d · 스냅숏 %d (DB %s)\n",
		applied.Sessions, applied.Items, applied.Judgments, applied.Snapshots, path)
	a.log.Info("이관 적용 완료", "mode", "apply", "db_path", path,
		"targets", applied.Items, "count", applied.Judgments,
		"skipped", len(plan.Gone), "failed", countFatal(plan))
	return 0
}

func countFatal(p legacy.ImportPlan) int {
	n := 0
	for _, r := range p.Rejects {
		if r.Fatal {
			n++
		}
	}
	return n
}

// runExport 는 `fd export --to-legacy` 다.
func (a *App) runExport(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("export")
	toLegacy := fs.Bool("to-legacy", false, "옛 형식(.claude/{sessions,queue,handoffs})으로 되쓴다")
	outDir := fs.String("out", "", "되쓸 디렉토리(반드시 준다 — 원본 위에 쓰지 않기 위해서다)")
	project := fs.String("project", "", "프로젝트 id(비면 FD_PROJECT)")
	dbPath := fs.String("db", "", "SQLite 파일 경로(비면 자동)")
	force := fs.Bool("force", false, "비어 있지 않은 자리에도 되쓴다(git 작업 트리는 이것으로도 안 된다)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*toLegacy {
		fmt.Fprintln(out, "지금 있는 되쓰기 형식은 --to-legacy 하나다")
		return 2
	}
	if strings.TrimSpace(*outDir) == "" {
		// ★ 기본값을 원본 경로로 두지 않는다. 기본값이 원본이면 언젠가 원본 위에 쓴다.
		fmt.Fprintln(out, "--out 이 없다 — 되쓸 디렉토리를 반드시 줘라(원본 위에 쓰지 않기 위해서다)")
		return 2
	}
	// ★ 되쓸 자리를 판정한다. --out 을 필수로 만든 위 가드는 "기본값이 원본이 되는 것"만 막고
	//   **인자로 원본을 주는 것**은 하나도 안 막았다. 오타 한 번이면 살아 있는 세션들의
	//   .claude/ 위에 수백 개 파일이 덮이고, 그 디렉토리는 gitignore 라 되돌릴 수도 없다.
	outExists, inGit, hasLegacy, outEntries, ierr := legacy.InspectOutTarget(*outDir)
	if ierr != nil {
		fmt.Fprintf(out, "%s\n", ierr)
		return 2
	}
	if v := legacy.JudgeOutTarget(outExists, inGit, hasLegacy, outEntries); !v.OK {
		if !*force || !legacy.ForceAllows(v.Code) {
			fmt.Fprintf(out, "되쓰기 거절 [%s]: %s\n", v.Code, v.Reason)
			return 2
		}
		// --force 로 뒤집었다는 사실을 **로그에 남긴다.** 조용히 덮으면 나중에 무엇이 사라졌는지 못 찾는다.
		a.log.Warn("되쓰기 자리가 비어 있지 않은데 --force 로 진행한다",
			"route", clip(*outDir, 200), "reason", v.Code)
	}

	proj := strings.TrimSpace(*project)
	if proj == "" {
		proj = envOr(a.env, "FD_PROJECT", "")
	}
	if proj == "" {
		fmt.Fprintln(out, "프로젝트 id 를 정하지 못했다 — --project 로 줘라")
		return 2
	}

	st, path, err := openDB(a.env, a.log, *dbPath)
	if err != nil {
		a.log.Error("되쓰기용 DB 를 열지 못했다", "error", err.Error())
		fmt.Fprintf(out, "%s\n", err)
		return 1
	}
	defer func() {
		if cerr := st.Close(); cerr != nil {
			a.log.Error("DB 닫기 실패", "error", cerr.Error())
		}
	}()

	res, err := legacy.ExportLegacy(ctx, st, proj, *outDir)
	if err != nil {
		a.log.Error("되쓰기 실패", "db_path", path, "error", err.Error())
		fmt.Fprintf(out, "되쓰기 실패: %s\n", err)
		return 1
	}
	fmt.Fprintf(out, "fd export --to-legacy · 프로젝트 %s → %s\n\n", proj, *outDir)
	fmt.Fprintf(out, "  세션 카드 %d · 큐 항목 %d · 핸드오프 %d (파일 %d)\n",
		res.Sessions, res.Items, res.Handoffs, len(res.Files))
	fmt.Fprintf(out, "\n── 왕복에서 돌아오지 않는 것 (%d건)\n", len(res.Losses))
	for _, l := range res.Losses {
		fmt.Fprintf(out, "  - %s\n", l)
	}
	a.log.Info("되쓰기 완료", "db_path", path, "mode", "to-legacy",
		"targets", res.Items, "count", len(res.Files))
	return 0
}
