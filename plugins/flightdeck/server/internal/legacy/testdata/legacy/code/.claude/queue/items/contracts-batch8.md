id: contracts-batch8
title: 계약 묶음 batch8 — cursor 설명 400 층위 · occurred_at 근사 · README 예문
repo: code
paths: contracts/
track: contracts
needs: contracts
after: 
handoff: .claude/handoffs/2026-07-30-0923-contracts-batch7-landed.md
created: 2026-07-30T09:02:02+09:00
---
출처: 계약 개정 7차(main 3ab19d2)의 문안 확정 게이트가 "이번 범위 밖, 다음 개정 후보"로 분류한 3건. 전부 서술 축이고 스키마 제약은 안 건드린다 — 7차와 같은 patch 개정이 된다.

① `contracts/openapi/svc-alpha.v1.yaml` cursor 파라미터 설명(현행 :384-386 부근) — "400으로 걸리는 것은 형식 파손·질의 묶임 불일치·음수 offset뿐"이 같은 설명 위쪽의 **랭킹 모드 전환 400**과 층위가 섞여 읽힌다. 7차가 400 설명 쪽을 고쳐 두 자리의 진술은 이제 정합하지만, "뿐"이라는 전칭이 ②(랭킹 모드)를 배제하는 것으로 오독될 수 있다. 두 400을 어느 층위에서 세는지 문장으로 갈라야 한다.

② `contracts/events/context-updated.v1.schema.json` occurred_at — "아웃박스 행 생성(=Canonical 커밋) 시각"이 근사다. 실제로는 `svc-beta/src/svc_beta/outbox.py:61`의 **조립 시각**이고 커밋은 그 직후다(`stages/index.py:113-121`의 같은 트랜잭션). 소비자가 이 값으로 커밋 순서를 재구성하면 어긋날 수 있는지 판단이 선행한다 — 어긋나지 않으면 근사임을 단서로 달고, 어긋나면 서술을 바꾼다.

③ `contracts/README.md:36` — 서술 원칙 2조의 인용 예문이 "(트랙 2가 아웃박스를 랜딩하면 …)"이다. 거짓 진술은 아니고(예문이다) 소재가 낡았을 뿐이지만, 실제로 랜딩된 지금은 예문으로서 힘이 없다.

**전용 세션에서만**(D-0018). 착수 순서: 세 자리 전부 코드로 먼저 재확인 — 7차에서 넘겨받은 목록이 두 군데 틀려 있었고(하나는 "고치면 안 되는 것", 하나는 "이미 고쳐진 것") 그 확인이 목록 밖 결함 셋을 새로 냈다.


**차수 정정 (2026-07-30 21:35, 계약 세션)**: 이 배치는 **9차가 된다.** 항목 id 의 `batch8` 은
차수가 아니라 이름이다 — `svc-delta` 스펙 신설이 **8차를 먼저 썼다**(코드 main `2ca0d6f`,
`CHANGELOG.md` 8차 절, 설계 문서 `2026-07-30-contracts-svc-delta-design.md`). 착수할 때
`info.description` 의 이력 항목과 CHANGELOG 제목을 9차로 적을 것.

**차수 재정정 (2026-07-31 01:5x, 계약 세션 `contracts-feedback`)**: **10차가 된다.** 위 정정
뒤에 신고 접수 표면(`POST /v1/feedback/flags`)이 **9차를 먼저 썼다** — `CHANGELOG.md` 9차 절,
설계 문서 `2026-07-31-contracts-feedback-surface-design.md`, `svc-alpha.v1.yaml` 1.5.0-alpha.
차수를 항목 본문에 적어 두는 방식이 **두 번 연속 어긋났다** — 이 항목보다 급한 표면이 계속
먼저 랜딩되기 때문이다. 착수할 때 차수를 이 본문에서 읽지 말고 **`CHANGELOG.md` 맨 위 절의
차수 + 1** 로 정하라(거기가 유일하게 자동으로 최신인 자리다).

**이 항목 ①(cursor 설명)의 좌표가 밀렸다** — 9차가 같은 파일에 178줄을 넣어 아래 "현행
:384-386" 이 더는 맞지 않는다. 줄 번호가 아니라 `cursor:` 키로 찾을 것.

그 신설이 함께 바꾼 것 하나가 이 항목에 직접 걸린다: **공통 불변식 8종이
`contracts/tests/test_openapi_common.py` 로 옮겨져 `openapi/*.yaml` 전수 순회로 돈다.**
`svc-alpha.v1.yaml` 을 고칠 때 `test_openapi.py` 만 보면 안 되고 그쪽도 함께 본다 —
특히 선언된 오류 응답이 `problem+json` + `Problem` 단일 `$ref` 라는 단정과 전 응답의
`X-Request-Id` 반향 단정이 그쪽에 있다.

## 2026-08-03 정합성 감사 (audit 세션) — 좌표·부수 정정

CHANGELOG.md 맨 위 절은 이제 **15차**(2026-08-03, 신고 접수 서빙 발효)라 '10차'는 다섯 차수 낡았다 — 본문이 스스로 '차수를 이 본문에서 읽지 말고 CHANGELOG 맨 위 절 +1 로 정하라'고 적어 두어 실무 위험은 막혀 있으나 문면은 거짓이다. occurred_at 조립은 `outbox.py:84`(`"occurred_at": _now_rfc3339()`)이고 `:61` 은 dataclass 필드 선언이다. ③ `contracts/README.md:36` 예문과 `stages/index.py:113-122` 같은 트랜잭션, `contracts/tests/test_openapi_common.py` 실재는 전부 그대로 참이다.
