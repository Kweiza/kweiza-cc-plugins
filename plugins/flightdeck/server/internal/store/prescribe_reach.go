package store

import (
	"context"
	"fmt"
	"time"
)

// AckCounts 는 **한 구간**의 세 수다. 단위는 카드가 아니라 **대화**다.
//
//	Emitted   처방이 발화된 대화 수
//	Reachable 그중 판단을 하나라도 가진 대화 = ack 이 원리적으로 닿을 수 있는 대화
//	Acked     실제로 ack 이 꽂힌 대화 수
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
// ★ **세는 단위가 대화다(machine + cc_session_id).** 카드로 세면 위 갈림이 지표에 그대로
// 들어온다 — 그리고 카드 정체는 3중키 (machine, worktree, cc) 라 **워크트리를 옮기는 대화는
// 반드시 갈린다.** 그것은 설계이고 되돌릴 수 없다(DESIGN §3: 접두 일치를 일부러 없앴다.
// coord_test 가 접히면 Fatal 을 낸다). 이 레포의 규율이 "작업은 워크트리로 연다"이므로
// **규율을 지킬수록 카드가 갈린다** — 카드로 세는 확인율은 그래서 규율을 잴 수 없다.
//
// 갈림은 분모를 부풀리기만 한 것이 아니라 **미확인을 지우고 있었다.** 처방이 A 카드에 뜨고
// 판단이 B 카드에 쌓이면 A 는 판단이 0이라 Reachable 에서 빠진다 — 그 대화의 미확인이
// 관측에서 사라진다. 실측(2026-08-08, 전 역사): 카드로 세면 kweiza-cc-plugins 가
// 59 · 13 · **13**(100%)인데 대화로 세면 40 · 15 · **13**(87%)다. 100%는 규율이 완벽해서가
// 아니었다. 대화로 접자 비로소 움직이는 지표가 됐다.
//
// ★ 접는 근거는 session 표뿐이다. event.session_id 에는 FK 가 없어 세션 행이 없는 이벤트가
// 있을 수 있고(원장 복원본), 그때는 접을 근거가 없으므로 카드 자신을 키로 폴백한다.
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
// 110%로 읽힌다). 그래서 acked 부질의는 ack 이 창 안일 것 **더하기** 그 대화의 처방도
// 창 안일 것을 함께 요구한다. 이러면 분모에 없는 대화가 분자에 못 들어온다.
//
// ★ 자르는 것은 처방이 발화된 시각(e.at)뿐이고 **판단의 나이는 안 자른다.** reachable 이
// 묻는 것은 "이 대화에 ack 이 닿을 수 있나"이고 그 답은 대화가 판단을 가졌는가이지 그
// 판단이 언제 쓰였는가가 아니다. 판단까지 자르면 어제 판단을 남긴 세션이 오늘 처방을
// 받았을 때 분모에서 빠져 확인율이 근거 없이 오른다.
//
// since 가 영값이면 두 벌이 같아진다 — 절단 없는 옛 동작이다.
//
// 실측(2026-08-08 UTC, 대화 단위): kweiza-cc-plugins 전 역사 40 · 15 · 13(87%),
// context-platform 전 역사 37 · 18 · 17(94%). 카드로 세던 같은 시점 값은 둘 다 100%였다.
// 발화 대비 도달 가능 배수는 대화로 접어도 2~5배로 남는다 — 그 남은 몫은 갈림이 아니라
// **처방을 받고 판단을 하나도 안 남긴 대화**이고, 그것이 이 지표가 원래 재려던 것이다.
//
// ★ payload 를 안 본다. 판단이 0인 대화는 애초에 prescribe_ack 이벤트를 안 남기므로,
// 분모에서 빼야 할 바로 그 대화들이 payload 에는 영영 안 나타난다.
//
// ★ 창을 id 가 아니라 at 으로 자른다 — 같은 패키지의 QueueReproduction 은 반대로 id 를
// 쓴다. 그쪽은 한 턴에 몰린 이벤트가 같은 마이크로초를 가질 때 경계에 걸친 행이 창을
// 드나드는 것이 문제였다. 24시간 창은 그 밀도가 문제되는 축이 아니고, 여기서 필요한 것은
// "배포 시각 이후" 같은 **시각으로 말해지는** 구간이라 at 이 맞다.
func (s *Store) AckReach(ctx context.Context, project string, since time.Time) (all, recent AckCounts, err error) {
	cut := fmtTime(since)
	// conv 는 카드 → **대화** 키다. 세션 행이 없는 이벤트(원장 복원본 등. event.session_id 에는
	// FK 가 없다)는 접을 근거가 없으므로 카드 자신을 키로 삼는다 — 옛 카드 단위 셈으로의 폴백이다.
	// char(31)은 단위 구분자다. 두 축을 이어 붙일 때 경로나 id 에 안 나오는 바이트여야 한다.
	row := s.db.QueryRowContext(ctx, `
		WITH conv AS (
		  SELECT id AS sid, machine_id || char(31) || cc_session_id AS k FROM session
		),
		ev AS (
		  SELECT e.kind AS kind, e.at AS at,
		         COALESCE(conv.k, 'sid:' || e.session_id) AS k
		  FROM event e LEFT JOIN conv ON conv.sid = e.session_id
		  WHERE e.project = ? AND e.kind IN ('prescribe','prescribe_ack')
		),
		judged AS (
		  SELECT DISTINCT COALESCE(conv.k, 'sid:' || j.session_id) AS k
		  FROM judgment j LEFT JOIN conv ON conv.sid = j.session_id
		)
		SELECT (SELECT count(DISTINCT k) FROM ev WHERE kind='prescribe'),
		       (SELECT count(DISTINCT k) FROM ev WHERE kind='prescribe'
		          AND k IN (SELECT k FROM judged)),
		       (SELECT count(DISTINCT k) FROM ev WHERE kind='prescribe_ack'),
		       (SELECT count(DISTINCT k) FROM ev WHERE kind='prescribe' AND at >= ?),
		       (SELECT count(DISTINCT k) FROM ev WHERE kind='prescribe' AND at >= ?
		          AND k IN (SELECT k FROM judged)),
		       (SELECT count(DISTINCT k) FROM ev WHERE kind='prescribe_ack' AND at >= ?
		          AND k IN (SELECT k FROM ev WHERE kind='prescribe' AND at >= ?))`,
		project, cut, cut, cut, cut)
	if err := row.Scan(&all.Emitted, &all.Reachable, &all.Acked,
		&recent.Emitted, &recent.Reachable, &recent.Acked); err != nil {
		return AckCounts{}, AckCounts{}, fmt.Errorf("확인율 도달성 조회 실패(project=%q since=%s): %w",
			clip(project, 64), cut, err)
	}
	return all, recent, nil
}
