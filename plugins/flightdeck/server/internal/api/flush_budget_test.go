package api

import (
	"testing"

	"github.com/kweiza/flightdeck/internal/store"
)

// TestDeferredFlushBudgetFitsInShutdownGrace 는 **교차 패키지 불변식**을 붙든다.
//
// store.DeferredFlushBudget 는 예약 계측 이벤트를 흘리는 상한이고, 그 ctx 는 요청의
// 취소에서 떼어져 있다 — 즉 드레인이 그것을 못 끊는다. 예산이 ShutdownGrace 보다 크면
// 유예를 넘긴 흘리기가 남고, 그 시점의 인플라이트는 끊긴 뒤이며 곧 store 도 닫힌다.
// 그러면 남는 시간에 하는 일이 "닫힌 DB 에 매달리기"뿐이라 예산이 사는 것이 없다.
//
// 두 값이 다른 패키지에 있으니 주석으로는 못 막는다 — 부등식의 아래쪽(busy_timeout 보다
// 크다)은 반대로 store 안에 있다(event_flush_detached_ctx_test.go). import 방향이
// api → store 라 한 자리에 못 모은다.
func TestDeferredFlushBudgetFitsInShutdownGrace(t *testing.T) {
	if store.DeferredFlushBudget >= ShutdownGrace {
		t.Fatalf("예약 이벤트 흘리기 예산 %s 가 종료 유예 %s 보다 작지 않다 — "+
			"유예를 넘긴 흘리기는 닫힌 DB 를 만난다", store.DeferredFlushBudget, ShutdownGrace)
	}
}
