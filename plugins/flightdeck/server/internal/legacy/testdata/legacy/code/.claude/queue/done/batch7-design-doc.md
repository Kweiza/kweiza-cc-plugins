id: batch7-design-doc
title: batch7 설계 문서(계약이 가리키는 부채) + 핸드오프 문서 규율 편입
repo: docs
paths: docs/superpowers/specs/ CLAUDE.md
track: batch7-design
needs: none
after: 
handoff: .claude/handoffs/2026-07-30-0923-contracts-batch7-landed.md
created: 2026-07-30T09:16:09+09:00
---
출처: 계약 개정 7차(main 00f0c75)가 남긴 부채 + 사용자 지시(2026-07-30).

① **계약이 없는 파일을 가리킨다.** `contracts/openapi/data-api.v1.yaml:50`이 `docs/superpowers/specs/2026-07-30-contracts-batch7-design.md`를 가리키는데 그 파일이 없다("랜딩 직후 작성" 단서를 달아 랜딩했다 — batch4·batch6과 같은 부채 형태다). batch6 선례에서 이 문서는 기록이 아니라 **검증 장치**로도 작동했다 — 쓰면서 6차의 사실 주장을 전수 재확인해 부정확 9자리를 새로 찾아냈다.
   특히 적어야 할 것: `lifecycle.deletions`를 **의도적으로 안 고친** 판단(토픽 자체가 안 만들어져 있어 그 문장은 여전히 참이다 — 근거를 안 남기면 다음 세션이 "왜 저기만 남았지" 하고 고치러 가서 새 거짓을 심는다).

② **핸드오프 문서를 규율에 편입한다**(사용자 지시). 지금 CLAUDE.md의 핸드오프는 여섯 단계이고 문서 작성이 빠져 있는데, 관례로는 29건이 쌓여 있고 큐 항목의 `handoff:` 필드가 그것을 가리키는 자리다. **순서가 적힌 자리가 둘**이라(③ 절의 명령 블록 · 큐 절의 "핸드오프 순서" 한 줄) 한쪽만 고치면 어긋난다.
landed_sha: ca83243a039fab5d16f595e9e57df4a9bba9062c
closed: 2026-07-30T09:42:43+09:00
