package judge

import (
	"strings"
	"testing"
	"time"
)

// 형제 카드(같은 대화, 다른 카드)와는 겹침 처방이 뜨면 안 된다.
//
// ★ 실측이 이유다(2026-08-05). 정체가 3중키(머신·워크트리·cc)라 한 대화가 카드 여러 장이
// 되고(cc 표류 · 워크트리 갈림), 그때 카드 id 는 다르지만 대화는 같다. overlap 발화 32건 중
// **5건이 자기 형제 카드를 가리켰다** — 세션에게 자기 자신과 조율하라고 한 것이다.
//
// 07e5df4·4de4b21 이 카드가 갈리는 원인을 고쳤지만, 그것은 **새로 갈리는 것**만 막는다.
// 플랫폼이 또 갈리면 이 처방이 다시 거짓말을 한다 — 그래서 판정 쪽에도 그물을 둔다.
func TestOverlapSkipsSiblingCardOfSameConversation(t *testing.T) {
	base := PrescribeInput{
		Now:       time.Now(),
		SessionID: "card-A",
		SelfCC:    "cc-1",
		TurnPaths: []string{"internal/service/pick.go"},
		Others: []LiveSession{{
			ID: "card-B", CCSessionID: "cc-1", // 같은 대화, 다른 카드
			Paths: []string{"internal/service/pick.go"},
		}},
	}
	if got := overlapPrescriptions(base); len(got) != 0 {
		t.Errorf("형제 카드에 겹침 처방이 떴다 — 자기 자신과 조율하라는 말이다: %+v", got)
	}

	// ── 대조: 다른 대화면 **반드시** 떠야 한다. 이 단정이 없으면 위 수정이
	// 겹침 축을 통째로 꺼도 초록이 난다.
	other := base
	other.Others = []LiveSession{{
		ID: "card-C", CCSessionID: "cc-2",
		Paths: []string{"internal/service/pick.go"},
	}}
	got := overlapPrescriptions(other)
	if len(got) != 1 {
		t.Fatalf("다른 대화인데 겹침 처방이 안 떴다: %+v", got)
	}
	if !strings.Contains(got[0].Key, "card-C") {
		t.Errorf("처방이 엉뚱한 상대를 가리킨다: %s", got[0].Key)
	}
}

// 대화 id 를 못 읽은 카드들끼리는 **같다고 보지 않는다.**
//
// 빈 값을 같다고 접으면 관측이 깨진 순간 겹침 축이 조용히 전부 꺼진다.
// 이 레포가 반복해서 겪은 실패 모양이라(못 읽음을 값으로 접기) 따로 붙든다.
func TestOverlapDoesNotFoldUnknownConversationIDs(t *testing.T) {
	in := PrescribeInput{
		Now:       time.Now(),
		SessionID: "card-A",
		SelfCC:    "", // 못 읽었다
		TurnPaths: []string{"internal/service/pick.go"},
		Others: []LiveSession{{
			ID: "card-B", CCSessionID: "", // 이쪽도 못 읽었다
			Paths: []string{"internal/service/pick.go"},
		}},
	}
	if got := overlapPrescriptions(in); len(got) != 1 {
		t.Errorf("cc 를 둘 다 못 읽었는데 같은 대화로 접었다 — 겹침이 조용히 꺼진다: %+v", got)
	}
}

// 비교 불가능한 좌표의 경로가 섞여도 "제대로 끝냈다"가 살아야 한다.
//
// ★ 관측 경로는 RelPath(카드의 워크트리, p) 가 만드는데 워크트리 **밖** 경로는 절대경로로
// 남는다. 선언 경로는 언제나 저장소 상대라, pathRelated 가 성분을 앞에서부터 맞추는 이상
// 그 둘은 원리적으로 안 맞는다. 그런 경로가 하나만 섞여도 coveredByClosed 가 무너지고,
// **방금 제대로 finish 한 세션이 "선점 0건인데 편집했다"는 잔소리를 듣는다.**
//
// 실측(2026-08-05): observed 발자국 406개 중 108개(27%)가 절대경로다. 실물 발화도 관측됐다.
// (그 모집단은 증분 005 로 0이 됐다. 존치 사유는 judge.comparablePath 주석에 있다.)
func TestClosedCoverageSurvivesUncomparablePaths(t *testing.T) {
	in := PrescribeInput{
		Now:       time.Now(),
		SessionID: "card-A",
		TurnPaths: []string{
			"internal/service/pick.go",            // 선언 안 — 비교 가능
			"/home/aaron/other-repo/plugins/x.go", // 카드 워크트리 밖 — 비교 불가능
		},
		Closed: []ClaimView{{ItemID: "it-1", Paths: []string{"internal/service"}}},
	}
	if _, ok := unclaimedPrescription(in); ok {
		t.Error("제대로 끝낸 세션이 unclaimed 처방을 맞았다 — 절대경로 하나가 가드를 무너뜨렸다")
	}

	// ── 대조 ①: 비교 가능한 경로가 정말 선언 밖이면 **떠야 한다.**
	out := in
	out.TurnPaths = []string{"cmd/fd/hook.go", "/home/aaron/other-repo/x.go"}
	if _, ok := unclaimedPrescription(out); !ok {
		t.Error("선언 밖 경로를 만졌는데 처방이 안 떴다 — 가드가 너무 넓어졌다")
	}

	// ── 대조 ②: 비교 가능한 경로가 **하나도 없으면** 덮였다고 말하지 않는다.
	// 근거 0을 통과로 접으면 이번엔 반대로 처방이 통째로 꺼진다.
	none := in
	none.TurnPaths = []string{"/home/aaron/other-repo/x.go"}
	if _, ok := unclaimedPrescription(none); !ok {
		t.Error("근거가 0인데 덮였다고 판정했다 — 처방이 통째로 꺼진다")
	}
}
