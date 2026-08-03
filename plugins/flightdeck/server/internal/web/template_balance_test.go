package web

import "testing"

func TestUnbalancedWrappers(t *testing.T) {
	cases := []struct {
		name string
		tpl  string
		bad  int
	}{
		{"짝이 맞는다",
			`<tbody class="fold">{{range .X}}<tr>{{if .A}}a{{end}}</tr>{{end}}</tbody>`, 0},
		// 내가 실제로 낸 결함: 안쪽 {{if}} 의 {{end}} 를 경계로 잡아 </tbody> 가 일찍 닫혔다.
		{"짝이 아닌 end 를 경계로 잡았다",
			`<tbody class="fold">{{range .X}}<tr><td>{{if .A}}a{{end}}</tbody></td></tr>{{end}}`, 1},
		{"블록이 덜 닫혔다",
			`<tbody>{{range .X}}<tr></tbody>`, 1},
		{"감싼 것이 없으면 볼 것도 없다", `<div>{{range .X}}a{{end}}</div>`, 0},
		// 표 밖: 같은 태그가 여러 번 나와도 각각 따로 본다.
		{"두 구간 중 하나만 깨졌다",
			`<tbody>{{range .A}}x{{end}}</tbody><tbody>{{range .B}}{{if .C}}y{{end}}</tbody>`, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := UnbalancedWrappers(c.tpl, "tbody")
			if len(got) != c.bad {
				t.Errorf("위반 %d건, 기대 %d건: %v", len(got), c.bad, got)
			}
		})
	}
}

// 실물 템플릿에 건다.
func TestDashboardTemplateWrappersAreBalanced(t *testing.T) {
	// 대조 전제: 감싼 구간이 실제로 있어야 이 시험이 무언가를 본다.
	if n := len(UnbalancedWrappers(mustTemplateSource(t), "tbody")); n > 0 {
		t.Errorf("템플릿의 <tbody> 감싸기가 블록을 반쯤 가른다: %v",
			UnbalancedWrappers(mustTemplateSource(t), "tbody"))
	}
	// ★ 중첩되는 태그(div·section)에는 걸지 않는다 — 이 검사기가 그 경우를 못 본다.
	//   범위를 넓히면 오탐이 쏟아지고, 오탐이 나는 검사는 곧 무시된다.
	if err := AssertNotNested(mustTemplateSource(t), "tbody"); err != nil {
		t.Fatalf("전제가 깨졌다 — %v (이 검사기의 결과를 믿을 수 없다)", err)
	}
}

// mustTemplateSource 는 embed 된 템플릿 원문이다.
func mustTemplateSource(t *testing.T) string {
	t.Helper()
	b, err := files.ReadFile("dashboard.gohtml")
	if err != nil {
		t.Fatalf("템플릿을 못 읽었다: %v", err)
	}
	return string(b)
}
