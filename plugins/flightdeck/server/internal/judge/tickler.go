package judge

import (
	"strings"
	"time"
)

// 티클러 — 종결 조건이 미래 사건인 항목의 잘 알려진 꼬리표.
//
// 큐는 설계상 만료 조건의 보관소를 겸한다(설계 §7·§10 — "만료 조건의 보관소는 fd 큐다").
// 그 용법의 항목은 기한이 올 때까지 늙는 것이 **정상**이라, 굶김 지표(24h 임계)에 넣으면
// 시간이 갈수록 영구 "굶은 항목"으로 오탐되고 경고가 상시 점등돼 판별력이 0이 된다(§4) —
// 보드가 늑대 소년이 되는 그 형태다.
//
// labels 는 표시 전용이고 **배제 판정에는 안 쓴다**(§5) — 그 규약은 유지된다. 이 꼬리표
// 하나만 **굶김 축**(굶김 집계·★ 표식·기아 가중)에서 빠진다. 배제가 아니다: 추천·선점·
// 겹침 어디에서도 이 꼬리표를 안 보고, 집는 것을 막지 않는다 — 기한이 오면 집어야 한다.
const TicklerLabel = "tickler"

// IsTickler 는 굶김 축에서 뺄 항목인가다. 순수 함수다.
//
// 정확 일치만 본다 — 접두·대소문자 근사를 허용하면 표시용 자유 문자열이 우연히
// 판정에 걸리고, 그 순간 "표시 전용" 규약이 조용히 넓어진다.
func IsTickler(labels []string) bool {
	for _, l := range labels {
		if l == TicklerLabel {
			return true
		}
	}
	return false
}

// FiresPrefix 는 발화일 꼬리표의 접두다: `fires:2026-08-19`.
const FiresPrefix = "fires:"

// FiresDateLayout 은 발화일의 유일한 표기다. **날짜뿐이고 시각은 안 받는다** —
// 티클러의 기한은 "그 날"이지 "그 시각"이 아니고, 시각을 허용하면 표기가 갈려
// 사람이 눈으로 못 거른다.
const FiresDateLayout = "2006-01-02"

// FiresOn 은 **이 항목이 언제 열리나**다. 순수 함수다.
//
// ★ 이것은 판정이 아니라 **표시**다. 배제·추천·겹침 어디에서도 안 쓴다 — 표시-전용
// 규약(설계 §8)의 예외는 여전히 `tickler` 하나뿐이고 이 값은 그 예외에 안 낀다.
// 기한이 지나도 아무것도 안 막고 안 승격시킨다. 화면이 사람에게 알릴 뿐이다.
//
// ★ 왜 항목의 새 칸이 아니라 꼬리표인가. 스키마·add·finish 표면을 안 건드리고,
// 무엇보다 `label` 로 **나중에 고칠 수 있다** — 재측해서 기한이 밀리면 갱신해야 하는데
// 본문·제목은 못 고친다(§11). 고칠 수 없는 자리에 기한을 두면 첫 추정이 영구히 남는다.
//
// ★ 왜 이 축이 필요한가. 없으면 보드가 낼 수 있는 것은 나이뿐이고, **기한 없는 나이는
// 뜻이 없다.** 2026-08-12 에 한 항목을 두고 세 시간 반에 네 세션이 같은 재측을 돌렸다 —
// 앞 세션이 "아직 아니다"를 남겼어도 언제까지 아닌지가 화면에 없었다.
//
// 못 읽는 값은 **조용히 무시한다**(두 번째 반환값이 false). 표시 축이 잘못 적힌
// 꼬리표 하나 때문에 죽으면 안 된다. 근사도 안 한다 — IsTickler 와 같은 이유다.
// 값이 여럿이면 **가장 이른 것**이 이긴다: 기한이 둘이면 먼저 오는 것이 기한이다.
func FiresOn(labels []string) (time.Time, bool) {
	var out time.Time
	var found bool
	for _, l := range labels {
		if !strings.HasPrefix(l, FiresPrefix) {
			continue
		}
		t, err := time.ParseInLocation(FiresDateLayout, l[len(FiresPrefix):], time.UTC)
		if err != nil {
			continue
		}
		if !found || t.Before(out) {
			out, found = t, true
		}
	}
	return out, found
}
