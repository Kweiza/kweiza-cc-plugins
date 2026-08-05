package store

import (
	"context"
	"fmt"
)

// AckReach 는 처방 확인율의 **분모를 가른** 세 수다.
//
//	emitted   처방이 발화된 카드 수
//	reachable 그중 판단을 하나라도 가진 카드 수 = ack 이 원리적으로 닿을 수 있는 카드
//	acked     실제로 ack 이 꽂힌 카드 수
//
// ★ 왜 분모가 둘인가. 발자국은 훅이 쓰고 판단은 MCP 가 쓴다. 한 대화의 카드가 갈리면
// 처방은 발자국 카드에서 뜨고 ack 은 판단 카드에 꽂힌다 — 그 발자국 카드는 판단이 0이라
// **영영 ack 할 수 없다.** 그것을 분모에 두면 확인율이 규율이 아니라 갈림을 잰다.
//
// 실측(2026-08-05): emitted 26 · reachable 4 · acked 4.
// 옛 분모로 15%, 고친 분모로 100%였다 — 닿을 수 있었던 카드는 전부 ack 했다.
//
// ★ payload 를 안 본다. 판단이 0인 카드는 애초에 prescribe_ack 이벤트를 안 남기므로,
// 분모에서 빼야 할 바로 그 카드들이 payload 에는 영영 안 나타난다.
func (s *Store) AckReach(ctx context.Context, project string) (emitted, reachable, acked int, err error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT (SELECT count(DISTINCT e.session_id) FROM event e
		        WHERE e.kind='prescribe' AND e.project=?),
		       (SELECT count(DISTINCT e.session_id) FROM event e
		        WHERE e.kind='prescribe' AND e.project=?
		          AND EXISTS (SELECT 1 FROM judgment j WHERE j.session_id = e.session_id)),
		       (SELECT count(DISTINCT e.session_id) FROM event e
		        WHERE e.kind='prescribe_ack' AND e.project=?)`,
		project, project, project)
	if err := row.Scan(&emitted, &reachable, &acked); err != nil {
		return 0, 0, 0, fmt.Errorf("확인율 도달성 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	return emitted, reachable, acked, nil
}
