-- 008 · 줄 행이 자원 집합을 갖는다 (schema_version 7 → 8)
--
-- ★ landing_queue 를 ALTER 하지 않고 표를 더한다. 컬럼 하나로는 자원 집합(경로 여럿)을
--   표현할 수 없고, 기존 행 백필을 UPDATE 로 하면 파괴적 조작(opUpdateSet)으로
--   마이그레이션 가드에 걸린다 — INSERT … SELECT 는 가산이라 통과한다
--   (migrate_guard_test.go 의 판정. 실측 2026-08-12).
--
-- ★ 세션당 살아 있는 줄 행 1개(landing_queue_one_live_per_session)는 그대로다.
--   "자원 A 를 쥔 채 자원 B 를 기다린다"가 성립하지 않아 순환대기의 전제가 사라진다.
--   다만 데드락 부재의 전체 증명은 스키마가 아니라 service 의 불변식이다(lane.divergent).
--
-- ★ 자원별 left_at 이 없다. 취득이 all-or-nothing 이라 부분 이탈이 없고,
--   줄 행이 닫히면 그 행의 자원 전부가 함께 빠진다.

CREATE TABLE landing_queue_resource (
  row_id   INTEGER NOT NULL REFERENCES landing_queue(id),
  resource TEXT NOT NULL,
  PRIMARY KEY (row_id, resource),
  CHECK (resource <> '')
);

-- 자원 하나의 줄 맨 앞(FrontLandingRowFor)이 이 인덱스를 탄다.
CREATE INDEX landing_queue_resource_by_name
  ON landing_queue_resource(resource, row_id);

-- 기존 줄 행은 전부 랜딩 줄이다.
INSERT INTO landing_queue_resource(row_id, resource)
  SELECT id, 'landing' FROM landing_queue;
