package service

import (
	"bytes"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// gofmt 관문 — 이 저장소에 이 축을 보는 눈이 0개였다
// ─────────────────────────────────────────────────────────────────────────────
//
// 실측(fd-gofmt-has-no-gate): `grep -rln 'gofmt|format.Source'` 적중 0건인 채로
// 위반 파일이 main 에 **두 번** 랜딩했다(mcpsrv/render.go → 고침 → cmd/fd/migrate_test.go).
// 두 번째가 _test.go 였다는 것이 이 시험의 자리를 정한다 — "go build 는 _test.go 를
// 건너뛰어 관문이 시험 코드에 대해 열려 있었다(그래서 go vet 으로 바꿨다)"는 앞선
// 교훈과 같은 자리에 다시 떨어진 것이다. 그래서 이 관문은 **시험 파일을 포함해**
// 모듈 전수를 본다.
//
// gofmt 바이너리를 셸로 부르지 않고 go/format 을 쓴다 — 시험 환경에 PATH 가정을
// 안 만들고, gofmt 의 정본 구현이 바로 이 패키지다.
//
// 같은 규율의 선례: store/signal_is_not_history_test.go · service/indexnotation_test.go ·
// service/containment_test.go — **더하는 사람의 빨간불이 켜져야 그 사람이 규약을 읽는다.**
func TestGofmtGateCoversTheWholeModuleIncludingTests(t *testing.T) {
	root := filepath.Join("..", "..") // internal/service → 모듈 루트(server/)
	var offenders []string
	walked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		walked++
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		want, ferr := format.Source(src)
		if ferr != nil {
			// 문법이 깨진 파일은 이 관문의 축이 아니다 — 컴파일이 먼저 죽는다.
			return nil
		}
		if !bytes.Equal(src, want) {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("모듈 순회 실패: %v", err)
	}
	// 0건 순회는 "깨끗하다"가 아니라 "아무것도 안 봤다"다 — 범위가 밀리면 여기서 죽는다
	// (containment 관문이 같은 이유로 같은 가드를 세웠다).
	if walked < 50 {
		t.Fatalf("순회한 .go 파일이 %d개뿐이다 — 루트 계산이 밀렸다(기대 수백)", walked)
	}
	if len(offenders) > 0 {
		// 고칠 때: gofmt -w 출력이 godoc 코드 블록의 산문을 가르면, 문장을 안 쪼개는
		// 다른 형식(대개 여분 공백 제거)을 골라라 — 182664a 가 값을 치르고 얻은 판단이다.
		t.Fatalf("gofmt 위반 %d개 — 랜딩 전에 gofmt -w 하라(파일 %d개를 봤다):\n%s",
			len(offenders), walked, joinLines(offenders))
	}
}

func joinLines(ss []string) string {
	out := ""
	for _, s := range ss {
		out += "  " + s + "\n"
	}
	return out
}
