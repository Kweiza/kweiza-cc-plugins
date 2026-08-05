package main

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// sqlite 드라이버는 internal/store 가 blank import 로 등록한다(cmd/fd 가 그것을 쓴다).
// 여기서 다시 import 하지 않는다.
func selfcheckDB(t *testing.T, sqls ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fd.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("열기 실패: %v", err)
	}
	defer db.Close()
	for _, q := range sqls {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("준비 실패(%q): %v", q, err)
		}
	}
	if len(sqls) == 0 {
		if _, err := db.Exec(`SELECT 1`); err != nil {
			t.Fatalf("빈 DB 실패: %v", err)
		}
	}
	return path
}

func TestSelfcheckPassesOnCleanDB(t *testing.T) {
	var out bytes.Buffer
	code := runSelfcheck([]string{"--db", selfcheckDB(t)}, &out)
	if code != 0 {
		t.Fatalf("깨끗한 DB 인데 종료코드 %d — %s", code, out.String())
	}
	if !strings.HasPrefix(out.String(), "fd selfcheck ok build=") {
		t.Fatalf("계약된 첫 줄이 아니다: %q", out.String())
	}
}

// ★ 이 갈래가 이 명령의 존재 이유다.
func TestSelfcheckFailsWhenMigrationWouldBeRejected(t *testing.T) {
	path := selfcheckDB(t, `CREATE TABLE somebody_elses (a TEXT)`)
	var out bytes.Buffer
	code := runSelfcheck([]string{"--db", path}, &out)
	if code == 0 {
		t.Fatalf("증분이 거절될 DB 인데 통과했다 — %s", out.String())
	}
	if !strings.Contains(out.String(), "schema_version") {
		t.Fatalf("사유가 원인을 안 나른다: %q", out.String())
	}
}

func TestSelfcheckFailsWhenDBMissing(t *testing.T) {
	var out bytes.Buffer
	if code := runSelfcheck([]string{"--db", filepath.Join(t.TempDir(), "nope.db")}, &out); code == 0 {
		t.Fatalf("없는 DB 인데 통과했다 — %s", out.String())
	}
}

func TestSelfcheckRequiresDBFlag(t *testing.T) {
	var out bytes.Buffer
	if code := runSelfcheck(nil, &out); code == 0 {
		t.Fatal("--db 없이 통과했다")
	}
}

// main 의 서브명령 표에 실제로 걸렸는가 — 없으면 감시기가 부를 때 usage 만 나온다.
//
// run 의 시그니처는 main.go:55 다:
//
//	func run(args []string, env func(string) (string, bool), stdin io.Reader, stdout, stderr io.Writer) int
func TestSelfcheckIsWiredIntoMain(t *testing.T) {
	var out, errBuf bytes.Buffer
	noEnv := func(string) (string, bool) { return "", false }
	code := run([]string{"selfcheck", "--db", selfcheckDB(t)},
		noEnv, strings.NewReader(""), &out, &errBuf)
	if code != 0 {
		t.Fatalf("main 경유 종료코드 %d — out=%q err=%q", code, out.String(), errBuf.String())
	}
	if !strings.Contains(out.String(), "fd selfcheck ok") {
		t.Fatalf("계약된 첫 줄이 stdout 으로 안 나왔다: %q", out.String())
	}
}
