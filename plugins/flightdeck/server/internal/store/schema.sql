-- flightdeck 기본 스키마 (schema_version = 1)
--
-- ★ 이 파일은 **기반 한 판**이고 그 위의 변경은 migrations/NNN_*.sql 에 증분으로 쌓는다.
--   빈 DB 도 "이 파일 → 증분 전부"를 거쳐 올라간다 — 신규용으로 여기에 새 표를 또 적으면
--   정의가 두 벌이 되고, 그때 신규 설치와 업그레이드가 다른 모양의 DB 를 갖는다.
--   지금 버전은 store.SchemaVersion 이고 이 파일이 만드는 것은 store.BaseSchemaVersion 이다.
--
-- 계층이 곧 쓰기 권한이다. 이 파일의 제약 하나하나가 실제로 났던 사고를 막는다 —
-- 어느 사고인지 주석에 적었다. 제약을 지우려면 그 사고가 왜 재발하지 않는지부터 답해야 한다.
--
--   D 계층 — 파생.  서버(git 리더)와 러너만 쓴다. 사람용 쓰기 엔드포인트가 없다.
--   Q 계층 — 큐.    사람은 title·body·reason 만 쓴다. 나머지는 서버가 채운다.
--   J 계층 — 판단.  사람만 쓴다. 추가 전용(UPDATE·DELETE 를 트리거가 막는다).
--
-- 시각은 전부 RFC3339 UTC 문자열이다(SQLite 에 시각 타입이 없다. 정렬이 사전순과 일치한다).

PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_version (
  version    INTEGER NOT NULL,
  applied_at TEXT    NOT NULL
);

-- ═══════════════════════════════════════════════════════════════════════════
-- 설정
-- ═══════════════════════════════════════════════════════════════════════════

CREATE TABLE project (
  id              TEXT PRIMARY KEY,
  path            TEXT NOT NULL,          -- 서버 머신에서의 절대경로
  remote_url      TEXT,                   -- 없어도 Tier A 는 완전히 돈다
  default_branch  TEXT NOT NULL DEFAULT 'main',
  config          TEXT,                   -- .flightdeck.yaml 의 캐시(JSON). 정본은 레포 안의 파일
  config_from_sha TEXT,                   -- 그 캐시를 읽어 온 커밋. 어긋나면 UI 가 낡음을 표시한다
  created_at      TEXT NOT NULL
);

CREATE TABLE machine (
  id         TEXT PRIMARY KEY,            -- 클라이언트가 생성해 로컬에 보관하는 안정 id
  hostname   TEXT NOT NULL,
  first_seen TEXT NOT NULL,
  last_seen  TEXT NOT NULL
);

-- ═══════════════════════════════════════════════════════════════════════════
-- D 계층 · 파생
-- ═══════════════════════════════════════════════════════════════════════════

CREATE TABLE session (
  id            TEXT PRIMARY KEY,                 -- 서버 발급 ULID
  project       TEXT NOT NULL REFERENCES project(id),
  machine_id    TEXT NOT NULL REFERENCES machine(id),
  worktree      TEXT NOT NULL,                    -- 절대경로
  cc_session_id TEXT NOT NULL,                    -- Claude Code 세션 id (훅 입력)
  label         TEXT,                             -- 표시 전용. 어떤 필터의 축도 아니다
  state         TEXT NOT NULL DEFAULT 'active'
                CHECK (state IN ('active','paused','blocked','done')),
  blocked_why   TEXT,                             -- state='blocked' 면 필수 (아래 CHECK)
  opened_at     TEXT NOT NULL,

  -- ★ 3중키. 워크트리 경로만으로는 안 된다 — 경로는 규율상 재사용된다(지우고 다시 만든다).
  --   경로만 키로 쓰면 옛 세션 행과 합쳐지거나 유니크 위반이 난다.
  --   그리고 이 스키마 어디에도 접두 일치가 없으므로 조상 트리의 등록을 물려받는 것이
  --   원리적으로 불가능하다 — 마커를 물려받아 남의 절을 덮어쓴 사고(2026-07-28·07-29, 원문 영구 소실)가
  --   발생할 물리적 자리가 사라진다.
  UNIQUE (machine_id, worktree, cc_session_id),

  -- 공허한 단정 금지: 막혔다고만 쓰고 왜인지 안 남기는 것을 막는다
  CHECK (state <> 'blocked' OR (blocked_why IS NOT NULL AND blocked_why <> ''))
);
-- ★ ended 컬럼이 없다. 세션 종료를 신뢰성 있게 감지할 수단이 없다(SessionEnd 는 크래시·컨텍스트
--   소진을 안 알린다). 채울 수 없는 컬럼은 반드시 거짓으로 채워진다.
-- ★ pid 컬럼이 없다. 기존 도구는 네 곳에서 pid 를 쓰기만 하고 읽는 곳이 0건이었고,
--   pid 死를 근거로 살아 있는 세션을 죽었다고 판정한 사고가 실재한다.

CREATE INDEX session_by_project ON session(project, state);

-- 한 세션이 코드 레포와 문서 레포를 함께 만지는 실무. project 단수 필드로는 못 담는다.
-- 기존 도구의 attach 를 대체한다.
CREATE TABLE session_workspace (
  session_id TEXT NOT NULL REFERENCES session(id) ON DELETE CASCADE,
  project    TEXT NOT NULL REFERENCES project(id),
  path       TEXT NOT NULL,
  is_primary INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (session_id, path)
);

-- 생존 '사실'이지 판정이 아니다. 넷을 나란히 두고 합치지 않는다 —
-- 하나만 보면 "에이전트가 긴 도구를 돌리는 중"과 "사람이 읽기만 하는 중" 둘 중 하나를 반드시 오판한다.
CREATE TABLE signal (
  session_id TEXT NOT NULL REFERENCES session(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL CHECK (kind IN ('prompt','tool','mcp','commit','push')),
  at         TEXT NOT NULL,
  PRIMARY KEY (session_id, kind)
);

-- 미커밋 발자국. 브랜치 diff 는 규율을 지킨 착수 직후 세션에 정의상 비어 있어서
-- 기존 큐의 1차 필터가 탈락을 0건 냈다. 이 표가 그 구간을 덮는다.
CREATE TABLE footprint (
  session_id TEXT NOT NULL REFERENCES session(id) ON DELETE CASCADE,
  path       TEXT NOT NULL,
  origin     TEXT NOT NULL CHECK (origin IN ('observed','declared','claimed')),
  first_at   TEXT NOT NULL,
  last_at    TEXT NOT NULL,
  -- ★ origin 을 뭉개지 않는다. 뭉개면 "선언했으나 안 건드림"과 "선언 없이 건드림"이
  --   구분되지 않고, 그러면 "겹침 없음"과 "이 축을 안 본다"도 구분되지 않는다.
  PRIMARY KEY (session_id, path, origin)
);

CREATE INDEX footprint_by_path ON footprint(path);

CREATE TABLE ref_state (
  project TEXT NOT NULL REFERENCES project(id),
  ref     TEXT NOT NULL,
  sha     TEXT NOT NULL,
  subject TEXT,
  at      TEXT NOT NULL,               -- 이 값을 관측한 시각. UI 가 신선도를 이걸로 표시한다
  PRIMARY KEY (project, ref)
);

-- ★ 변경 시점에 계산해 불변으로 보관한다. 브랜치가 지워져도 이 행은 남는다 —
--   "파생은 원본이 사라지면 계산 불가"라는 파생 우선 설계의 유일한 약점에 대한 답이다.
CREATE TABLE change_set (
  project     TEXT NOT NULL REFERENCES project(id),
  base_sha    TEXT NOT NULL,
  head_sha    TEXT NOT NULL,
  paths       TEXT NOT NULL,           -- JSON 배열
  computed_at TEXT NOT NULL,
  PRIMARY KEY (project, base_sha, head_sha)
);

-- ═══════════════════════════════════════════════════════════════════════════
-- Q 계층 · 큐
-- ═══════════════════════════════════════════════════════════════════════════

CREATE TABLE item (
  project      TEXT NOT NULL REFERENCES project(id),
  id           TEXT NOT NULL,          -- 사람이 정한다. 브랜치 이름으로도 쓰이므로 전역 유일해야 한다
  title        TEXT NOT NULL,
  body         TEXT NOT NULL,
  paths        TEXT NOT NULL DEFAULT '[]',   -- JSON 배열
  labels       TEXT NOT NULL DEFAULT '[]',   -- JSON 배열. ★ 표시 전용 — 어떤 배제 판정에도 안 쓴다
  state        TEXT NOT NULL DEFAULT 'open'
               CHECK (state IN ('open','claimed','done','dropped')),
  close_reason TEXT,
  landed_ref   TEXT,                   -- ★ 러너가 실제로 fast-forward 한 sha 만 들어간다.
                                       --   기존 도구는 "메인 트리의 지금 HEAD"를 적어 남의 커밋이
                                       --   이 항목의 랜딩 sha 로 박혔다(3회 관측, 미수정).
  created_at   TEXT NOT NULL,
  closed_at    TEXT,
  PRIMARY KEY (project, id),

  -- 사유 없는 폐기는 나중에 되짚을 수 없다
  CHECK (state <> 'dropped' OR (close_reason IS NOT NULL AND close_reason <> ''))
);

CREATE INDEX item_by_state ON item(project, state, created_at);

CREATE TABLE item_after (
  project  TEXT NOT NULL,
  item_id  TEXT NOT NULL,
  dep_item TEXT,                       -- 미랜딩 선행: 항목 id
  dep_job  TEXT,                       -- 진행 중 잡 (Tier B)
  dep_sha  TEXT,                       -- 랜딩된 것: 커밋 sha
  FOREIGN KEY (project, item_id) REFERENCES item(project, id) ON DELETE CASCADE,

  -- ★ 브랜치 이름을 담을 컬럼이 없다. 랜딩이 끝나면 규율대로 브랜치가 삭제되고,
  --   그러면 merge-base 가 해석 불가를 내 "조건이 충족되는 바로 그 순간" 판정이 깨진다.
  --   쓸 수 없게 만들어 막는다.
  CHECK ((dep_item IS NOT NULL) + (dep_job IS NOT NULL) + (dep_sha IS NOT NULL) = 1)
);

CREATE INDEX item_after_by_item ON item_after(project, item_id);

-- 삽입·삭제 시 유지되는 역인덱스. 기존 도구는 적격 항목마다 전체를 grep 해
-- 첫 명령이 51.7초 걸렸다(O(n²)).
CREATE TABLE item_dependents (
  project TEXT NOT NULL,
  item_id TEXT NOT NULL,
  n       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (project, item_id)
);

CREATE TABLE claim (
  project      TEXT NOT NULL,
  item_id      TEXT NOT NULL,
  session_id   TEXT NOT NULL REFERENCES session(id),
  at           TEXT NOT NULL,
  released_at  TEXT,
  force_reason TEXT,                   -- 남의 선점을 뺏었다면 사유 필수(아래 CHECK)
  PRIMARY KEY (project, item_id),
  FOREIGN KEY (project, item_id) REFERENCES item(project, id) ON DELETE CASCADE
);
-- ★ 만료 컬럼이 없다. 자동 반납이 없다. 자동 회수 코드 경로가 존재하지 않는다.
--   "ttl 을 넘긴 남의 락도 자동으로 뺏지 않는다" — 생존 판정의 정본이 없어 "만료 = 죽음"이
--   성립하지 않기 때문이다. 실측으로 두 번 틀렸다(죽었다 판정한 세션이 그 뒤 6커밋 랜딩,
--   419분 무갱신 세션이 실제로는 17초 전에 살아 있었음).

-- 추천 1건과 탈락 사유 전부를 남긴다. 사유가 없으면 큐는 블랙박스가 되고,
-- 블랙박스는 두 번째 세션부터 무시된다.
CREATE TABLE pick_eval (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  project    TEXT NOT NULL,
  session_id TEXT NOT NULL,
  at         TEXT NOT NULL,
  picked     TEXT,                     -- 항목 id 또는 NULL(적격 0건)
  rejected   TEXT NOT NULL             -- JSON: [{item, reason_code, detail}]
);

-- ═══════════════════════════════════════════════════════════════════════════
-- J 계층 · 판단 — 사람이 쓰는 유일한 것. 추가 전용.
-- ═══════════════════════════════════════════════════════════════════════════

CREATE TABLE judgment (
  id         TEXT PRIMARY KEY,
  project    TEXT REFERENCES project(id),
  session_id TEXT REFERENCES session(id),
  at         TEXT NOT NULL,
  kind       TEXT NOT NULL CHECK (kind IN (
               'handoff',    -- 마무리: 무엇이 랜딩됐나·어떻게 검증했나·일부러 안 한 것·후속
               'decision',   -- 되돌리기 비싼 결정과 그 근거
               'blocked',    -- 막힘(사유 필수)
               'ask',        -- 남이 건드리면 곤란한 것 — 커밋 전 의도를 나르는 유일한 축
               'now',        -- 지금 하는 것
               'rejected',   -- 검토했으나 기각한 후보
               'not-done',   -- 일부러 안 한 것과 그 이유
               'verified',   -- 확인했으나 결과가 "문제 없음"이었던 조사
               'draft')),    -- PreCompact 자동 초안
  title      TEXT,
  body       TEXT NOT NULL,
  supersedes TEXT REFERENCES judgment(id),
  CHECK (body <> '')
);

CREATE INDEX judgment_by_session ON judgment(session_id, at);
CREATE INDEX judgment_by_kind ON judgment(project, kind, at);

-- ★ 추가 전용을 트리거로 강제한다. 규율이 아니라 물리로.
--   기존 게시판은 같은 파일을 두 세션이 쓸 수 있어 앞 세션의 절이 통째로 덮였고,
--   저장소가 버전관리 밖이라 원문이 영구 소실됐다. 두 번 났다.
CREATE TRIGGER judgment_no_update BEFORE UPDATE ON judgment
BEGIN
  SELECT RAISE(ABORT, 'judgment 는 추가 전용이다 — 정정은 새 행 + supersedes 로 남겨라');
END;

CREATE TRIGGER judgment_no_delete BEFORE DELETE ON judgment
BEGIN
  SELECT RAISE(ABORT, 'judgment 는 삭제할 수 없다');
END;

-- 판단 ↔ 항목·잡·커밋. 경로 문자열 포인터가 FK 가 되면 끊긴 포인터가 원리적으로 사라진다.
-- 기존 핸드오프 85건 중 30건이 어느 큐 항목도 안 가리켜 grep 외에 도달 경로가 없었다.
CREATE TABLE judgment_link (
  judgment_id TEXT NOT NULL REFERENCES judgment(id) ON DELETE CASCADE,
  target_kind TEXT NOT NULL CHECK (target_kind IN ('item','job','commit','session')),
  target_id   TEXT NOT NULL,
  PRIMARY KEY (judgment_id, target_kind, target_id)
);

CREATE INDEX judgment_link_by_target ON judgment_link(target_kind, target_id);

-- 전문 검색. 판단이 쌓이면 grep 이 유일한 도달 경로가 되는 것을 막는다.
CREATE VIRTUAL TABLE judgment_fts USING fts5(
  title, body, content='judgment', content_rowid='rowid'
);

CREATE TRIGGER judgment_fts_ins AFTER INSERT ON judgment
BEGIN
  INSERT INTO judgment_fts(rowid, title, body) VALUES (new.rowid, new.title, new.body);
END;

-- 재계산이 불가능한 수치(예: 여러 파트를 사람이 전수 판정한 진척률).
-- ★ 손으로 넣으려면 근거를 함께 넣어야 한다 — "손으로 올리면 근거 없는 숫자가 되고,
--   그 순간 이 표를 아무도 못 믿는다"는 규율이 여기서 제약이 된다.
CREATE TABLE snapshot (
  project      TEXT NOT NULL REFERENCES project(id),
  key          TEXT NOT NULL,
  value        TEXT NOT NULL,
  method       TEXT NOT NULL CHECK (method IN ('command','manual')),
  evidence     TEXT,                   -- method='manual' 이면 필수
  input_digest TEXT,                   -- 판정 당시 입력의 해시. 현재와 다르면 UI 가 "낡음"을 붙인다
  computed_at  TEXT NOT NULL,
  PRIMARY KEY (project, key),
  CHECK (method <> 'manual' OR (evidence IS NOT NULL AND evidence <> ''))
);

-- ═══════════════════════════════════════════════════════════════════════════
-- 자원 · 계측
-- ═══════════════════════════════════════════════════════════════════════════

-- 락이 원리적으로 못 지키는 논리 카운터. 파일 접근을 직렬화해도 발번은 안 지켜진다 —
-- 실제로 같은 날 두 세션이 같은 개정 차수를 써서 뒤가 물러야 했다.
CREATE TABLE counter (
  project TEXT NOT NULL REFERENCES project(id),
  name    TEXT NOT NULL,
  value   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (project, name)
);

CREATE TABLE resource_hold (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  project      TEXT NOT NULL REFERENCES project(id),
  resource     TEXT NOT NULL,
  session_id   TEXT REFERENCES session(id),
  job_id       TEXT,
  acquired_at  TEXT NOT NULL,
  released_at  TEXT,
  force_reason TEXT,
  CHECK ((session_id IS NOT NULL) + (job_id IS NOT NULL) = 1)
);

-- ★ 배타를 애플리케이션 판정이 아니라 DB 제약으로 만든다. 판정을 앱에서 빼면 우회할 코드도 없다.
--   기존 락은 우회가 넷이었고 둘은 흔적조차 안 남겼다.
CREATE UNIQUE INDEX resource_one_holder
  ON resource_hold(project, resource) WHERE released_at IS NULL;

-- Tier B. 지금은 안 쓰지만 item.landed_ref·item_after.dep_job 이 이 표를 가리키므로 함께 둔다.
CREATE TABLE job (
  id         TEXT PRIMARY KEY,
  project    TEXT NOT NULL REFERENCES project(id),
  kind       TEXT NOT NULL CHECK (kind IN ('land','exec')),
  item_id    TEXT,
  session_id TEXT REFERENCES session(id),
  branch     TEXT,
  recipe     TEXT,
  state      TEXT NOT NULL DEFAULT 'queued'
             CHECK (state IN ('queued','running','ok','fail','stalled','bypassed')),
  -- 사유를 뭉개지 않는다 — 재시도해도 되는 것과 안 되는 것이 갈린다
  fail_kind  TEXT CHECK (fail_kind IS NULL OR fail_kind IN (
               'conflict','verify','seed','raced','infra','timeout','admission','push')),
  queued_at  TEXT NOT NULL,
  started_at TEXT,
  ended_at   TEXT,
  beat_at    TEXT,
  base_sha   TEXT,
  head_sha   TEXT,
  landed_sha TEXT,
  image_tag  TEXT,
  ack_at     TEXT,                     -- ★ NULL = 미확인. 결과의 주인은 세션이 아니라 항목이다 —
                                       --   세션이 사라져도 다음에 그 항목을 집는 세션이 먼저 받는다
  log_tail   TEXT
);

CREATE INDEX job_by_state ON job(project, state, queued_at);
CREATE INDEX job_unacked ON job(project, ack_at) WHERE ack_at IS NULL;

-- 추가 전용 감사·계측 원장. 기존 구조는 반납이 rm -rf 라 흔적이 없었고,
-- 그래서 "기다렸으나 안 적은 세션"이 원리적으로 관측 불가였다.
CREATE TABLE event (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  at         TEXT NOT NULL,
  project    TEXT,
  session_id TEXT,
  kind       TEXT NOT NULL,
  payload    TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX event_by_kind ON event(kind, at);
CREATE INDEX event_by_session ON event(session_id, at);

CREATE TRIGGER event_no_update BEFORE UPDATE ON event
BEGIN
  SELECT RAISE(ABORT, 'event 는 추가 전용이다');
END;

CREATE TRIGGER event_no_delete BEFORE DELETE ON event
BEGIN
  SELECT RAISE(ABORT, 'event 는 삭제할 수 없다');
END;
