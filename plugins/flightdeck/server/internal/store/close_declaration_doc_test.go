package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// 원장에서 파생하는 축은 §5 파생 표와 §10 지표에 이름이 있어야 한다
// ─────────────────────────────────────────────────────────────────────────────
//
// ★ **이 관문은 지금 스킵이 아니라 실행이다.** CloseDeclarationsByItem 은 이 커밋
// 이전에 이미 들어와 있었다(51b4df9, "원장에 이미 있던 신호를 항목별로 접는다").
// 아래 스킵 갈래는 그래서 지금 안 밟힌다 — 남겨 둔 이유는 이 함수가 언젠가 사라지거나
// 이름이 바뀌면 이 시험이 "좌표가 틀렸다"는 애매한 실패 대신 그 사실을 그대로 말하게
// 하기 위해서다. 이 커밋을 초록으로 만드는 것은 Step 2·3 이 DESIGN.md 에 박은
// 두 앵커 문자열이고, 그것이 코드와 문서가 같은 커밋 안에서 함께 도착했다는 증거다.
//
// 하는 일은 하나다: **CloseDeclarationsByItem 이 있는데 DESIGN 에 그 축 이름이
// 없으면 커밋을 막는다.**
//
// 왜 필요한가. 설계 §8 이 지목한 네 자리는 전부 "시험이 문자열 존재만 보므로 빨간불
// 없이 표류한 산문"이었다. 문서 커밋과 코드 커밋이 갈리면 그 사이가 정확히 같은
// 모양의 구멍이고, 이 저장소는 그 구멍을 이미 여러 번 겪었다.
//
// 방향은 코드 → 문서다. 반대는 안 건다 — DESIGN 은 구현보다 앞설 수 있다(§0 머리말).
//
// 아래 두 문자열은 **앵커**다. 문서의 표현을 바꾸려면 이 시험도 같이 고쳐라 —
// 그 한 번의 수고가 이 관문의 전부이고, 그것이 없으면 관문이 조용히 눈이 먼다.

func TestLedgerDerivedAxisIsNamedInDesign(t *testing.T) {
	src, err := os.ReadFile("event.go")
	if err != nil {
		t.Fatalf("store/event.go 를 못 읽었다 — 이 시험의 좌표가 틀렸다: %v", err)
	}
	if !strings.Contains(string(src), "func (s *Store) CloseDeclarationsByItem") {
		t.Skip("CloseDeclarationsByItem 이 아직 없다 — 이 관문의 전제가 안 섰다")
	}

	p := filepath.Join("..", "..", "..", "DESIGN.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("DESIGN.md 를 못 읽었다(%s) — 이 시험의 좌표가 틀렸다: %v", p, err)
	}
	design := string(b)

	for _, want := range []string{
		"`event(kind='item.finish')` + `item.state`", // §5 파생 표의 원천 칸
		"종료 선언 축이 무엇을 몇 번 뒤로 밀었나",                    // §10 지표 줄
	} {
		if strings.Contains(design, want) {
			continue
		}
		t.Errorf("store 가 원장에서 종료 선언을 파생하는데 DESIGN 에 %q 가 없다 — "+
			"파생 표(§5)와 지표(§10) 둘 다 그 축을 이름으로 불러야 한다", want)
	}
}
