-- 003 · 랜딩 순서 큐 (schema_version 2 → 3)
--
-- ★ id 가 곧 순번이다. 별도 발번(counter)을 두지 않는 이유: 발번기는 뒤따르는 삽입이 거절되면
--   번호만 소각되는데 그 번호를 회수하는 함수가 이 레포에 없다. id 는 같은 INSERT 안에서 원자적이다.
--
-- ★ granted_at 이 없다. "내가 레인을 쥐었나"는 resource_hold 의 부분 유니크 인덱스
--   (resource_one_holder)가 정본이고 HeldBy(project,'landing') 로만 파생한다.
--   사본을 두면 갈릴 자리가 생기는데, 갈렸을 때 어느 쪽이 참인지 정하는 문장이 없다.
--
-- ★ item_id 도 없다. 세션의 선점에서 파생 가능하고, 읽는 쪽이 없는 컬럼은 session_workspace 가
--   이미 밟은 함정이다 — 쓰기만 있는 표는 나중에 "그 축은 이미 있다"의 거짓 근거가 된다.
--
-- ★ 만료도 자동 회수도 없다. resource_hold 와 같은 규율이다.

CREATE TABLE landing_queue (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,   -- 이것이 순번이다
  project     TEXT NOT NULL REFERENCES project(id),
  session_id  TEXT NOT NULL REFERENCES session(id),
  enqueued_at TEXT NOT NULL,
  left_at     TEXT,
  left_kind   TEXT,        -- ok | fail | leave | finish | force
  left_detail TEXT,

  -- 빠진 시각과 빠진 종류는 함께 있거나 함께 없다. 한쪽만 채워지면
  -- "아직 줄에 있다"와 "빠졌는데 종류를 모른다"가 같은 행 모양이 된다.
  CHECK ((left_at IS NULL) = (left_kind IS NULL)),

  -- ★ 사유 없는 회수는 나중에 되짚을 수 없다. 그 규율을 산문이 아니라 제약으로 만든다
  --   (item.state='dropped' → close_reason 이 그 본이다).
  --   ok·finish 만 면제된다 — 정상 종료라 "왜"가 종류 자체에 들어 있다.
  CHECK (left_kind IS NULL
         OR left_kind IN ('ok','finish')
         OR (left_detail IS NOT NULL AND left_detail <> '')),

  -- ★ 종류는 다섯뿐이다. Go 쪽 ValidateLandingLeave 가 1차 방어이고 이것이 최종 방어다 —
  --   판정을 애플리케이션에만 두면 우회할 코드가 언제든 생긴다(resource.go:78-81 규율).
  --   job.fail_kind 가 같은 모양으로 값을 열거한다.
  CHECK (left_kind IS NULL
         OR left_kind IN ('ok','fail','leave','finish','force'))
);

-- ★ 한 세션은 살아 있는 줄 행을 하나만 가진다. 재진입이 줄을 두 자리 차지하면 순번이 거짓이 된다.
--   배타와 같은 규율로 애플리케이션 판정이 아니라 부분 유니크 인덱스가 지킨다.
CREATE UNIQUE INDEX landing_queue_one_live_per_session
  ON landing_queue(project, session_id) WHERE left_at IS NULL;

-- 순서 집행 지점(맨 앞 조회)과 줄 전체 나열이 이 인덱스를 탄다.
CREATE INDEX landing_queue_waiting
  ON landing_queue(project, id) WHERE left_at IS NULL;
