-- 016_item_revision.sql — 항목 본문의 개정 이력.
--
-- DESIGN §11 「항목 본문 수정」이 2026-09-16 에 열렸다. 본문을 제자리에서 고치는 값을
-- 치르는 대신, 옛 값이 사라지지 않는다는 보장을 이 표가 산다.
--
-- ★ 이 행이 담는 것은 **고치기 직전의 값**이지 새 값이 아니다. 현재 값은 item 한 곳에만
--   있어서 두 표가 어긋날 자리가 원리적으로 없고, rev 를 역순으로 이으면 원문까지 복원된다.
--   새 값을 쌓으면 "현재"가 두 곳에 생긴다 — judgment_link.target_project 가 없던 동안
--   링크는 들어가고 응답은 성공인데 수신자에게는 안 보였던 그 부류다.
--
-- 파괴적 조작이 없다 — CREATE 뿐이라 migrate_guard 의 destructiveExempt 등록이 필요 없다.

CREATE TABLE item_revision (
  project    TEXT NOT NULL,
  item_id    TEXT NOT NULL,
  rev        INTEGER NOT NULL,              -- 1부터. 이 항목에서 몇 번째 개정인가
  at         TEXT NOT NULL,
  session_id TEXT REFERENCES session(id),   -- 누가 고쳤나. 세션을 못 얻어도 개정은 남긴다
  title      TEXT NOT NULL,                 -- ★ 고치기 직전의 값
  body       TEXT NOT NULL,
  paths      TEXT NOT NULL,                 -- JSON 배열
  reason     TEXT NOT NULL CHECK (reason <> ''),
  PRIMARY KEY (project, item_id, rev),
  FOREIGN KEY (project, item_id) REFERENCES item(project, id)
);

-- 이력을 고칠 수 있으면 이력이 아니다. judgment 와 같은 규율이다.
CREATE TRIGGER item_revision_no_update BEFORE UPDATE ON item_revision
BEGIN SELECT RAISE(ABORT, 'item_revision 은 추가 전용이다 — 개정은 새 rev 로 쌓아라'); END;

CREATE TRIGGER item_revision_no_delete BEFORE DELETE ON item_revision
BEGIN SELECT RAISE(ABORT, 'item_revision 은 추가 전용이다 — 지우면 복구 경로가 0이 된다'); END;
