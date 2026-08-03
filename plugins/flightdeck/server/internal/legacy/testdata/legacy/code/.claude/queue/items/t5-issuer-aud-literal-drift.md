id: t5-issuer-aud-literal-drift
title: 발급기↔data-api 의 aud 리터럴 사본 2개를 묶는 시험이 없다 + e2e 가 POST /oauth2/token 을 안 부른다
repo: code
paths: services/console-api/issuer/,services/data-api/server/
track: 5
needs: none
after: 
handoff: .claude/handoffs/2026-08-03-1010-dash-judge-v2-landed.md
created: 2026-08-03T10:07:39+09:00
---
(왜 · 무엇부터 · 근거가 어느 파일에 있는지를 여기 적는다)

## 2026-08-03 정합성 감사 갱신 (audit 세션) — 전제 정정

갱신(2026-08-03, cb925b4·76cffa6): e2e 13/13 블록이 `POST /oauth2/token` 을 부르고 발급 JWT 로 data-api 를 관통하므로 「e2e 가 안 부른다」는 더 이상 참이 아니다. 남은 공백은 **단위 시험**뿐이다 — `issuer/signer.go:35` 와 `data-api/server/search.go:107` 의 두 리터럴을 잇는 시험이 0건이라, 한쪽만 바꾸면 `make test` 는 초록이고 스테이징 e2e 에서만 드러난다. 항목 범위를 그 축으로 좁혀라.
