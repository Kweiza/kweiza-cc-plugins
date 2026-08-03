id: t1-cover-fallback-removal
title: 옛 Raw 재처리 뒤 커버 폴백(2단) 제거 — 재처리 전에 지우면 전량 기본값이 된다
repo: code
paths: svc-beta/src/svc_beta/stages/
track: 2
needs: none
after: t2-quarantine-stale-rows
handoff: .claude/handoffs/2026-07-31-1620-track2-cover-gate-replacement-landed.md
created: 2026-07-31T14:21:58+09:00
---
(왜 · 무엇부터 · 근거가 어느 파일에 있는지를 여기 적는다)
dropped_reason: after 를 잘못 걸었다(격리 행 정리는 선행이 아니다) + id 접두가 트랙과 어긋난다 — t2-cover-fallback-removal 로 다시 넣는다
closed: 2026-07-31T14:22:24+09:00
