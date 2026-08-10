-- 007 · 프로젝트에 표시 축을 준다 — 핀과 보관 (schema_version 6 → 7)
--
-- ★ 무엇을 위한 컬럼인가: 헤더의 프로젝트 줄이 ListProjects 전부를 그대로 내는데,
--   실측 11건 중 일이 도는 것은 2건이고 4건은 워크트리·프로브 경로가 프로젝트로 잘못
--   등록된 잔해다. 사람이 고른 것만 줄에 남기고 나머지를 접기 위한 자리다.
--
-- ★ 불리언이 아니라 시각인 이유는 created_at 과 같다 — 언제 접었는지가 없으면 되짚을 수
--   없다. 보관 목록이 "언제 보관했는지"와 "그 뒤에 세션이 열렸는지"를 견주려면 시각이 있어야
--   한다. NULL 이 "아님"이다.
--
-- ★ 이 축은 표시 계층이다. 항목·판단·선점·랜딩 어디에도 안 닿고, 접힌 프로젝트도
--   ?project= 로 그대로 열린다(그 단정은 web/project_nav_test.go 의
--   TestArchivedProjectStillOpens 다). 그래서 화면의 이 폼은 "파생물에 쓰는 폼" 상한
--   넷에서 빠진다 — 그 근거는 web/render_test.go 의
--   TestWriteFormsAreAtMostFourAndAllRequireReason 이 이름으로 적어 뒀다.
--
-- ★ 순수 가산이다 — ALTER TABLE ADD COLUMN 뿐이라 migrate_guard_test.go 의
--   destructiveOps 여섯 축(DROP TABLE·DROP COLUMN·RENAME·DELETE FROM·UPDATE…SET·
--   INSERT…SELECT) 어느 것에도 안 걸린다. 예외 등재가 필요 없다.
--
-- ★ 멱등이 아니다(ALTER 는 두 번 돌면 "duplicate column name" 으로 죽는다). 그것으로 족하다 —
--   증분은 schema_version 으로 정확히 한 번만 돈다.

ALTER TABLE project ADD COLUMN pinned_at   TEXT;
ALTER TABLE project ADD COLUMN archived_at TEXT;
