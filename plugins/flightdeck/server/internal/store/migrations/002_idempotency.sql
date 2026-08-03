-- 002 · 멱등 기록을 DB 로 옮긴다 (schema_version 1 → 2)
--
-- ★ 왜: 앞 판의 멱등 표는 프로세스 메모리에만 있었고, 그래서 **서버를 재기동하면
--   멱등 기억이 통째로 사라졌다.** 그런데 그 조합이 나는 상황이 정확히 설계 §7 이
--   겨냥한 시나리오다 — 서버가 죽어 아웃박스가 쌓이고, 살아나서 재생이 돈다.
--   그때 서버는 방금 재기동해 기억이 비어 있다. 그리고 **판단은 추가 전용이라
--   중복이 안 지워진다**(judgment_no_delete 트리거). 그 순간 한 판단이 두 행이 되고
--   되돌릴 방법이 없다.
--
-- ★ 무엇만 남기나: **중복이 영구히 남는 쓰기**만이다(판단·항목·종료·발번).
--   어느 라우트가 그런지는 api.JudgePersistIdempotency 가 사유와 함께 판정한다.
--   전부 남기면 신호(beat)처럼 초당 오는 쓰기가 이 표를 채우고, 그 쓰기들은
--   애초에 upsert 라 재생이 무해하다 — 이득 없이 비용만 는다.
--
-- ★ 5xx 를 저장하지 않는 정책은 그대로다. 일시 장애를 영구 응답으로 굳히면
--   하류가 복구된 뒤에도 같은 실패만 돌려주게 된다. 그래서 status 컬럼에 CHECK 를 건다 —
--   규율을 산문이 아니라 제약으로 만든다.

CREATE TABLE idempotency (
  key         TEXT PRIMARY KEY,        -- Idempotency-Key 원문(<session>:<seq>)
  fingerprint TEXT NOT NULL,           -- method+path+body 의 해시. 키 재사용 탐지의 유일한 축
  status      INTEGER NOT NULL,
  ctype       TEXT NOT NULL,
  body        BLOB NOT NULL,           -- 응답 본문 원문. 재생은 바이트가 같아야 한다
  at          TEXT NOT NULL,           -- 저장 시각. TTL 청소의 축이다

  -- 5xx 는 저장하지 않는다. 산문이 아니라 제약으로 둔다 —
  -- 이 규율이 깨지면 하류 복구 뒤에도 장애 응답이 영구히 재생된다.
  CHECK (status >= 100 AND status < 500)
);

-- TTL 청소가 이 인덱스를 탄다. 없으면 청소가 매번 전수 주사가 된다.
CREATE INDEX idempotency_by_at ON idempotency(at);
