package service

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// 표기 규약: 사람이 읽는 "%d번째" 는 1-based 다
// ─────────────────────────────────────────────────────────────────────────────
//
// 이 규약이 왜 시험으로 서 있나 — 이것은 **한 자리씩 고치면 더 나빠지는** 종류다.
// 같은 파일 안에 두 규약이 공존하면 읽는 사람은 어느 쪽도 못 믿는다. 실제로 그
// 상태가 실재했다: cmd/fd/outbox.go 의 readOutbox 는 `line++` 로 1-based 였고
// readQuarantine 은 range 인덱스를 그대로 실어 0-based 였다 — 한 파일, 같은
// "%d번째 줄" 문구, 다른 뜻.
//
// 그리고 이 규약은 service·store·cmd/fd 세 패키지를 걸친다. 한 패키지의 행동
// 시험으로는 못 잡으므로 소스를 전수로 본다(store/schema_table_count_test.go 가
// 같은 이유로 스키마를 전수로 보는 것과 같은 규율 — **더하는 사람**의 빨간불이
// 켜져야 그 사람이 규약을 읽는다).
//
// ★ 범위는 **비시험 소스**다. 시험 실패 메시지(id_test.go 등)에도 같은 표기가
// 있지만 그것은 개발자가 보는 문장이고, 이 축은 사용자·에이전트가 받는 사유다.
// 둘을 한 번에 묶으면 이 변경이 레포 전체로 번져 리뷰가 불가능해진다 — 넓히려면
// 별도 항목으로 세워라.

// indexNotationRe 는 사람이 읽는 순번 표기를 찾는다.
var indexNotationRe = regexp.MustCompile(`%d번째`)

// bareIndexArgRe 는 그 표기에 **벌거벗은 range 인덱스**가 실렸는지 본다.
//
// `i+1` · `line`(이미 1-based 로 세는 변수)은 안 걸리고 `, i)` · `, i,` 만 걸린다.
// 인덱스로 슬라이싱하는 `entries[i:]` 같은 표현도 안 걸린다 — 앞이 쉼표가 아니라
// 대괄호다. 잡으려는 것은 오직 **포맷 인자 자리에 앉은 맨 인덱스**다.
var bareIndexArgRe = regexp.MustCompile(`,\s*(i|j|k|n|idx)\s*[,)]`)

// serverRoot 는 이 패키지에서 본 서버 루트다(go test 는 패키지 디렉토리에서 돈다).
func serverRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("서버 루트를 못 찾았다: %v", err)
	}
	// 좌표가 밀리면 이 시험은 아무것도 안 보면서 초록이 된다. 못박아 둔다.
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("서버 루트(%s)에 go.mod 가 없다 — 이 시험의 좌표가 틀렸다: %v", root, err)
	}
	return root
}

// TestIndexNotationIsOneBased 는 사용자에게 나가는 순번이 1-based 인지 전수로 본다.
func TestIndexNotationIsOneBased(t *testing.T) {
	root := serverRoot(t)

	var offenders []string
	var scanned, sites int

	werr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		scanned++
		rel, _ := filepath.Rel(root, p)
		lines := strings.Split(string(b), "\n")
		for i, ln := range lines {
			if !indexNotationRe.MatchString(ln) {
				continue
			}
			sites++
			// 인자가 다음 줄로 넘어간 호출까지 본다 — 괄호가 안 닫혔으면 한 줄 더.
			win := ln
			if strings.Count(ln, "(") > strings.Count(ln, ")") && i+1 < len(lines) {
				win += "\n" + lines[i+1]
			}
			if m := bareIndexArgRe.FindString(win); m != "" {
				offenders = append(offenders, filepath.ToSlash(rel)+":"+strconv.Itoa(i+1)+
					"  ("+strings.TrimSpace(m)+")  "+strings.TrimSpace(ln))
			}
		}
		return nil
	})
	if werr != nil {
		t.Fatalf("소스 순회 실패: %v", werr)
	}

	// 스캔이 0이면 이 시험은 아무것도 안 지키면서 초록이다.
	if scanned == 0 || sites == 0 {
		t.Fatalf("훑은 소스 %d개, 찾은 표기 %d자리 — 둘 중 하나가 0이면 이 가드는 꺼진 것이다", scanned, sites)
	}

	if len(offenders) > 0 {
		t.Fatalf("사람이 읽는 \"%%d번째\" 에 range 인덱스가 그대로 실렸다(%d자리).\n"+
			"목록의 세 번째 것이 \"2번째\"로 나가고, 그 말을 믿은 사람은 두 번째 것을 고치러 간다.\n"+
			"`i` 대신 `i+1` 을 실어라 — 사유는 사람이 고칠 수 있어야 사유다.\n  %s",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}
