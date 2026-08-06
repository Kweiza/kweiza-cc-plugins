package ledger

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// IsOurOutput 은 이 디렉토리가 앞선 원장 내보내기의 산출물인지 본다.
//
// ★ 왜 필요한가. 원장은 같은 자리에 계속 쓰는 것이 정상 사용인데, legacy 의 not-empty
// 가드를 그대로 태우면 두 번째 실행부터 매번 --force 를 요구받는다. 자기 산출물이면
// 갱신으로 보는 것이 백업 도구의 관례다.
//
// ★ 판정을 느슨하게 하지 않는다. 매니페스트가 있어도 format 이 우리 것이 아니면 거짓이다 —
// 남의 manifest.json 이 있는 디렉토리를 조용히 덮으면 그 순간 이 가드는 없는 것과 같다.
func IsOurOutput(dir string) bool {
	body, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return false
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return false
	}
	return m.Format == FormatName
}
