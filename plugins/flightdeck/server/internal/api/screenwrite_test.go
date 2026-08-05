package api

import "testing"

// JudgeScreenOrigin 의 갈래를 전부 잠근다.
//
// cmd/fd 의 조립 서버 시험이 이 판정의 **주 경로**(같은 출처 통과 · 다른 출처 거절)를
// 이미 누른다. 여기서 더 보는 것은 조립 서버로는 만들기 번거로운 갈래들이다 —
// 특히 **한쪽 축만 맞춰 통과를 노리는 손요청**. 브라우저는 그렇게 안 보내지만,
// 공격자는 브라우저가 아니다.
func TestJudgeScreenOrigin(t *testing.T) {
	const host = "127.0.0.1:7420"

	for _, c := range []struct {
		name, origin, secFetch string
		want                   bool
	}{
		{"둘 다 같은 출처", "http://127.0.0.1:7420", "same-origin", true},
		{"Origin 만 있고 같다", "http://127.0.0.1:7420", "", true},
		{"Sec-Fetch-Site 만 있고 same-origin", "", "same-origin", true},
		{"주소창에서 직접 온 요청(none)", "", "none", true},
		{"스킴만 다르다 — 호스트가 같으므로 통과한다", "https://127.0.0.1:7420", "same-origin", true},
		{"대소문자만 다른 호스트", "http://127.0.0.1:7420", "same-origin", true},

		{"둘 다 없다", "", "", false},
		{"Origin 이 다른 호스트", "https://evil.example", "", false},
		{"포트가 다르다 — 다른 출처다", "http://127.0.0.1:9999", "same-origin", false},
		{"Sec-Fetch-Site 가 cross-site", "", "cross-site", false},
		{"Sec-Fetch-Site 가 same-site — 형제 서브도메인이다", "", "same-site", false},
		{"Origin 이 깨졌다", "://", "same-origin", false},
		{"Origin 이 호스트 없는 값", "null", "same-origin", false},

		// ★ 이 둘이 이 시험의 존재 이유다. 한 축만 맞춰서는 못 통과한다.
		{"Sec-Fetch-Site 만 맞추고 Origin 은 남의 것", "https://evil.example", "same-origin", false},
		{"Origin 만 맞추고 Sec-Fetch-Site 는 cross-site", "http://127.0.0.1:7420", "cross-site", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			v := JudgeScreenOrigin(c.origin, c.secFetch, host)
			if v.OK != c.want {
				t.Errorf("OK=%v, 기대 %v (사유: %s)", v.OK, c.want, v.Reason)
			}
			if v.Reason == "" {
				t.Error("사유가 비었다 — 판정은 항상 사유를 채운다")
			}
		})
	}
}

// JudgeScreenWrite 는 **어디에 이 검사를 걸지**를 정한다. 이 판정이 넓어지면
// REST 쓰기가 출처 검사에 걸려 CLI 가 죽고, 좁아지면 화면 쓰기가 검사를 안 탄다.
func TestJudgeScreenWrite(t *testing.T) {
	for _, c := range []struct {
		method, path string
		want         bool
	}{
		{"POST", "/actions/reclaim", true},
		{"POST", "/actions/drop", true},
		{"GET", "/actions/reclaim", false}, // 읽기는 안 본다
		{"POST", "/api/v1/items", false},   // REST 쓰기는 토큰·멱등이 이미 지킨다
		{"POST", "/", false},               // 화면 자체는 쓰기 라우트가 아니다
		{"POST", "/actionsX/drop", false},  // 접두어를 흉내 낸 다른 경로
		{"PUT", "/actions/reclaim", true},  // 쓰기 메서드가 늘어도 같은 축이다
		{"DELETE", "/actions/drop", true},
	} {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			if got := JudgeScreenWrite(c.method, c.path); got.Screen != c.want {
				t.Errorf("Screen=%v, 기대 %v (사유: %s)", got.Screen, c.want, got.Reason)
			}
		})
	}
}
