package api

import (
	"encoding/json"
	"testing"
)

// CLI 가 보내는 본문의 필드 이름이 서버가 읽는 이름과 같아야 한다.
//
// 어긋나면 서버가 조용히 0값을 받는다 — add·rm 이 둘 다 빈 채 닿으면 "하나는
// 줘라"로 거절되고, 사람은 자기가 방금 친 --add 를 다시 들여다본다. move·after cut 이
// 같은 이유로 같은 시험을 갖는다.
func TestLabelRequestFieldNamesMatchTheWire(t *testing.T) {
	// cmd/fd 의 labelReq 와 **글자 그대로** 같은 JSON 이어야 한다.
	const wire = `{"project":"p","session_id":"s","add":["tickler"],"rm":["old"]}`

	var got labelRequest
	if err := json.Unmarshal([]byte(wire), &got); err != nil {
		t.Fatalf("본문을 못 읽었다: %v", err)
	}
	if got.Project != "p" {
		t.Errorf("project 가 %q 다 — 필드 이름이 어긋났다", got.Project)
	}
	if got.SessionID != "s" {
		t.Errorf("session_id 가 %q 다 — 필드 이름이 어긋났다", got.SessionID)
	}
	if len(got.Add) != 1 || got.Add[0] != "tickler" {
		t.Errorf("add 가 %v 다 — 필드 이름이 어긋났다", got.Add)
	}
	if len(got.Rm) != 1 || got.Rm[0] != "old" {
		t.Errorf("rm 이 %v 다 — 필드 이름이 어긋났다", got.Rm)
	}
}
