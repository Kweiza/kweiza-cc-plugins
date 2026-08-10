package mcpsrv

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 2026-08-11 사고: `finish` 를 followups 둘과 함께 불렀는데 응답이 **오류 없이 `후속 0건`** 을
// 냈고 항목은 닫혔다(원장 `item.finish` payload: `count:0, linked:0, tx:committed`).
// finish 는 한 트랜잭션이라 다시 못 부르므로 판단↔후속 링크가 그 자리에서 영영 죽었다.
//
// ★ **항목 본문의 가설 하나는 실측으로 반증됐다.** "followups 가 문자열로 도착했는데
// 거절이 아니라 무시로 접혔다"는 거짓이다 — decodeArgs 는 DisallowUnknownFields 를 쓰고
// 타입 불일치에 오류를 낸다. 실측한 아홉 모양 중 **조용히 0건으로 통과하는 것은 셋뿐**이다:
// `"followups": []` · `"followups": null` · **키 자체가 없음**. 나머지(문자열·이름 오타·
// 배열 아님·원소 문자열·없는 필드)는 전부 시끄럽게 실패한다.
//
// 그래서 이 시험이 잠그는 것은 그 셋 중 **앞의 둘**이다 — 키가 왔는데 내용이 0건인 경우.
// 키가 아예 없는 경우는 서버가 "안 보냈다"와 "보냈는데 유실됐다"를 원리적으로 못 가른다;
// 그 갈래는 문구로 막는다(TestZeroFollowupsLineSaysFinishCannotBeRerun).
func TestFollowupsKeyPresentButEmptyIsRefused(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantOK   bool
		wantWord string // 사유에 반드시 들어갈 낱말
	}{
		{
			name:     "빈 배열은 거절한다",
			raw:      `{"item_id":"x","outcome":"done","body":"b","followups":[]}`,
			wantOK:   false,
			wantWord: "followups",
		},
		{
			name:     "null 도 거절한다",
			raw:      `{"item_id":"x","outcome":"done","body":"b","followups":null}`,
			wantOK:   false,
			wantWord: "followups",
		},
		{
			// ★ 키가 아예 없는 것은 **정상**이다. 후속이 정말 없는 마무리가 흔하고,
			// 그것까지 거절하면 관문이 아니라 벽이 된다(judgeMissingFollowups 의 규율과 같다).
			name:   "키가 없으면 통과한다",
			raw:    `{"item_id":"x","outcome":"done","body":"b"}`,
			wantOK: true,
		},
		{
			name:   "내용이 있으면 통과한다",
			raw:    `{"item_id":"x","outcome":"done","body":"b","followups":[{"id":"f1","title":"t","body":"bb"}]}`,
			wantOK: true,
		},
		{
			// 인자가 아예 없는 호출(raw 가 비었다)도 정상이다 — decodeArgs 가 그렇게 다룬다.
			name:   "인자 없음도 통과한다",
			raw:    ``,
			wantOK: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var a finishArgs
			if err := decodeArgs(json.RawMessage(tc.raw), &a); err != nil {
				t.Fatalf("이 모양은 decodeArgs 를 통과해야 한다: %v", err)
			}
			ok, reason := judgeFollowupsArrived(json.RawMessage(tc.raw), len(a.Followups))
			if ok != tc.wantOK {
				t.Fatalf("ok=%v 를 기대했는데 %v 다 (사유: %q)", tc.wantOK, ok, reason)
			}
			if ok {
				return
			}
			if reason == "" {
				t.Fatal("거절인데 사유가 비었다 — 이 저장소는 불리언이 아니라 사유를 돌려준다")
			}
			if !strings.Contains(reason, tc.wantWord) {
				t.Errorf("사유에 %q 가 없다: %q", tc.wantWord, reason)
			}
			// 사유가 **되돌릴 수 없다는 사실**과 복구 경로를 말해야 한다 —
			// 그것이 이 관문의 존재 이유다.
			for _, must := range []string{"닫지 않았다", "add"} {
				if !strings.Contains(reason, must) {
					t.Errorf("사유에 %q 가 없다: %q", must, reason)
				}
			}
		})
	}
}

// 키가 아예 없는 갈래는 서버가 "안 보냈다"와 "보냈는데 유실됐다"를 못 가른다.
// 그래서 문구가 **되돌릴 수 없다는 사실**을 말해야 한다 — 지금은 "정말 없다면 그것이 맞다"로
// 안심시키기만 해서, 실제로 유실된 세션이 응답을 읽고도 못 알아챘다.
func TestZeroFollowupsLineSaysFinishCannotBeRerun(t *testing.T) {
	out := RenderFinish(service.FinishResult{
		Item:     model.Item{ID: "lead", State: model.ItemDone},
		Judgment: model.Judgment{ID: "J1", Kind: model.JudgmentHandoff, Body: "본문"},
	})
	if !strings.Contains(out, "후속 0건") {
		t.Fatalf("0건 줄이 없다:\n%s", out)
	}
	// ① 되돌릴 수 없다는 사실
	if !strings.Contains(out, "다시 못 부른다") {
		t.Errorf("finish 를 다시 못 부른다는 사실이 없다 — 그것을 모르면 지금 확인할 이유가 없다:\n%s", out)
	}
	// ② followups 를 실었는데 0건이면 유실이라는 사실
	if !strings.Contains(out, "실었는데") {
		t.Errorf("실었는데 0건이면 유실이라는 갈래가 없다:\n%s", out)
	}
	// ③ 복구 경로 둘 — add 와 판단을 거는 note
	if !strings.Contains(out, "add") || !strings.Contains(out, "handoff") {
		t.Errorf("복구 경로(add + note(kind='handoff'))가 없다:\n%s", out)
	}
}
