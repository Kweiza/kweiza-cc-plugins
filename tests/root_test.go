package plugintests

import (
	"os"
	"path/filepath"
	"testing"
)

// repoRoot 는 이 시험들이 훑는 저장소 루트다(`tests/` 에서 한 단계 위).
//
// 좌표가 밀리면 이 시험들은 아무것도 안 보면서 초록이 된다. 표식 둘로 못박아 둔다 —
// 마켓플레이스 루트라는 것과, 이 관문이 무는 플러그인이 거기 있다는 것.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("레포 루트를 못 찾았다: %v", err)
	}
	for _, must := range []string{
		".claude-plugin/marketplace.json",
		"plugins/grafik-bar/.claude-plugin/plugin.json",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(must))); err != nil {
			t.Fatalf("레포 루트(%s)에 %s 가 없다 — 이 시험의 좌표가 틀렸다: %v", root, must, err)
		}
	}
	return root
}

// hooksFile 은 hooks.json 의 모양이다. `readHooksFile` 이 모르는 키를 거절하며 이것으로 읽는다 —
// 여기 없는 필드를 쓰기 시작하면 그 관문이 먼저 빨개진다(그 주석 참고).
type hooksFile struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Async   bool   `json:"async"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func keysOf[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
