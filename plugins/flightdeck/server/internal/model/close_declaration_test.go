package model

import "testing"

// 이 패키지의 첫 시험 파일이다. model 은 지금까지 순수 데이터뿐이라 시험할 행동이 없었고,
// CloseDeclaration 이 **메서드를 가진 첫 타입**이다.
//
// Count 가 사유 문구의 "종료 선언 N건"을 만든다. 한쪽 mode 를 빠뜨려도 컴파일은 되고
// 시험도 안 죽는데 화면만 조용히 작은 수를 말한다 — 그래서 여기 빨간불을 세운다.
func TestCloseDeclarationCountSumsBothModes(t *testing.T) {
	cases := []struct {
		name string
		in   CloseDeclaration
		want int
		why  string
	}{
		{
			name: "done 만", in: CloseDeclaration{Done: 2}, want: 2,
			why: "실측 384건 중 308건이 이쪽이다",
		},
		{
			name: "dropped 만", in: CloseDeclaration{Dropped: 3}, want: 3,
			why: "dropped 를 안 세면 실측 20%(384건 중 76건)가 통째로 침묵한다",
		},
		{
			name: "둘 다", in: CloseDeclaration{Done: 1, Dropped: 2}, want: 3,
			why: "둘은 처방이 갈려 따로 담지만 '몇 번 선언됐나'는 합이다",
		},
		{
			name: "빈 값은 0", in: CloseDeclaration{}, want: 0,
			why: "zero 값이 '선언 없음'이다 — '이 축을 안 읽었다'와 가르는 것은 호출부의 bool 이지 이 수가 아니다",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.in.Count(); got != c.want {
				t.Errorf("Count() = %d, 기대 %d\n%s", got, c.want, c.why)
			}
		})
	}
}
