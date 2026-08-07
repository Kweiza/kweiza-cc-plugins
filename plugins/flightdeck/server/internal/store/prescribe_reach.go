package store

import (
	"context"
	"fmt"
	"time"
)

// AckCounts 는 **한 구간**의 세 수다.
//
//	Emitted   처방이 발화된 카드 수
//	Reachable 그중 판단을 하나라도 가진 카드 수 = ack 이 원리적으로 닿을 수 있는 카드
//	Acked     실제로 ack 이 꽂힌 카드 수
//
// 세 값이 어느 구간의 것인지는 이 타입이 모른다 — AckReach 가 두 벌로 내고,
// 표면(보드 detail 꼬리)이 그 구간을 문장에 적을 책임을 진다.
type AckCounts struct {
	Emitted   int
	Reachable int
	Acked     int
}

// AckReach 는 처방 확인율의 **분모를 가른** 세 수를 **두 구간으로** 낸다 —
// 전 역사 한 벌(all)과 since 이후 한 벌(recent).
//
// ★ 왜 분모가 둘인가. 발자국은 훅이 쓰고 판단은 MCP 가 쓴다. 한 대화의 카드가 갈리면
// 처방은 발자국 카드에서 뜨고 ack 은 판단 카드에 꽂힌다 — 그 발자국 카드는 판단이 0이라
// **영영 ack 할 수 없다.** 그것을 분모에 두면 확인율이 규율이 아니라 갈림을 잰다.
//
// ★ 왜 구간이 둘인가. 전 역사만 내면 emitted 가 단조 증가한다 — 갈림의 원인을 고쳐도
// 이미 갈린 옛 카드가 분모에 영영 남아, 두 수의 차이가 회복되지 않는다. 그러면
// "지금 규율이 나아졌나"를 물을 수 없다(DESIGN §10 이 요구한 재측정이 정확히 그 물음이다).
// 최근 벌만 내면 반대로 표본이 얇아 노이즈가 된다. 그래서 **둘 다** 낸다.
//
// ★ **분자도 처방의 시각으로 묶는다.** ack 을 그 이벤트 자신의 at 으로만 자르면 안 된다 —
// 처방은 턴 끝에 나고 ack 은 그 뒤 note·finish 에 나므로(관측된 간격 최댓값 0.85시간)
// 절단선이 둘 사이에 떨어질 수 있고, 그러면 그 카드가 분자에만 들어가 `Acked ⊆ Reachable
// ⊆ Emitted` 가 깨진다. 실물 원장에서 실제로 나는 모양이다(2026-08-06T04:30Z 기준
// context-platform 이 28 · 10 · **11** 이었다 — 꼬리 문장의 "그중"이 거짓이 되고 확인율이
// 110%로 읽힌다). 그래서 acked 부질의는 ack 이 창 안일 것 **더하기** 그 세션의 처방도
// 창 안일 것을 함께 요구한다. 이러면 분모에 없는 카드가 분자에 못 들어온다.
//
// ★ 자르는 것은 처방이 발화된 시각(e.at)뿐이고 **판단의 나이는 안 자른다.** reachable 이
// 묻는 것은 "이 카드에 ack 이 닿을 수 있나"이고 그 답은 카드가 판단을 가졌는가이지 그
// 판단이 언제 쓰였는가가 아니다. 판단까지 자르면 어제 판단을 남긴 세션이 오늘 처방을
// 받았을 때 분모에서 빠져 확인율이 근거 없이 오른다.
//
// since 가 영값이면 두 벌이 같아진다 — 절단 없는 옛 동작이다.
//
// 실측(2026-08-07 UTC, kweiza-cc-plugins): 전 역사 58 · 13 · 13, 최근 24시간 7 · 2 · 2.
// 두 구간 모두 고친 분모로 100%였다 — **그 시점의 값이지 구조적 보장이 아니다**(닿을 수
// 있었던 카드가 그때 전부 ack 했다는 뜻일 뿐이다). 그런데 갈림 비율(emitted 대비
// reachable)은 전 역사 4.5배 · 최근 24시간 3.5배로 **거의 안 줄었다** — 갈림은 옛 이야기가
// 아니라 지금도 난다. 그 사실은 절단을 넣기 전에는 원리적으로 관측할 수 없었다.
//
// ★ payload 를 안 본다. 판단이 0인 카드는 애초에 prescribe_ack 이벤트를 안 남기므로,
// 분모에서 빼야 할 바로 그 카드들이 payload 에는 영영 안 나타난다.
//
// ★ 창을 id 가 아니라 at 으로 자른다 — 같은 패키지의 QueueReproduction 은 반대로 id 를
// 쓴다. 그쪽은 한 턴에 몰린 이벤트가 같은 마이크로초를 가질 때 경계에 걸친 행이 창을
// 드나드는 것이 문제였다. 24시간 창은 그 밀도가 문제되는 축이 아니고, 여기서 필요한 것은
// "배포 시각 이후" 같은 **시각으로 말해지는** 구간이라 at 이 맞다.
func (s *Store) AckReach(ctx context.Context, project string, since time.Time) (all, recent AckCounts, err error) {
	cut := fmtTime(since)
	row := s.db.QueryRowContext(ctx, `
		SELECT (SELECT count(DISTINCT e.session_id) FROM event e
		        WHERE e.kind='prescribe' AND e.project=?),
		       (SELECT count(DISTINCT e.session_id) FROM event e
		        WHERE e.kind='prescribe' AND e.project=?
		          AND EXISTS (SELECT 1 FROM judgment j WHERE j.session_id = e.session_id)),
		       (SELECT count(DISTINCT e.session_id) FROM event e
		        WHERE e.kind='prescribe_ack' AND e.project=?),
		       (SELECT count(DISTINCT e.session_id) FROM event e
		        WHERE e.kind='prescribe' AND e.project=? AND e.at >= ?),
		       (SELECT count(DISTINCT e.session_id) FROM event e
		        WHERE e.kind='prescribe' AND e.project=? AND e.at >= ?
		          AND EXISTS (SELECT 1 FROM judgment j WHERE j.session_id = e.session_id)),
		       (SELECT count(DISTINCT e.session_id) FROM event e
		        WHERE e.kind='prescribe_ack' AND e.project=? AND e.at >= ?
		          AND EXISTS (SELECT 1 FROM event p WHERE p.kind='prescribe'
		                       AND p.project = e.project AND p.session_id = e.session_id
		                       AND p.at >= ?))`,
		project, project, project,
		project, cut, project, cut, project, cut, cut)
	if err := row.Scan(&all.Emitted, &all.Reachable, &all.Acked,
		&recent.Emitted, &recent.Reachable, &recent.Acked); err != nil {
		return AckCounts{}, AckCounts{}, fmt.Errorf("확인율 도달성 조회 실패(project=%q since=%s): %w",
			clip(project, 64), cut, err)
	}
	return all, recent, nil
}
