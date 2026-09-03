-- 014 · 루트 프로젝트가 멤버 프로젝트의 명부를 갖는다 (schema_version 13 → 14)
--
-- 다중 레포 배치(루트 폴더에 하위 git 레포를 두고 **루트에서** 하네스를 띄운다)에서
-- fd 의 프로젝트는 «cwd 의 git 루트 basename» 하나로 판정되므로 루트와 하위 레포가
-- **서로 모르는 프로젝트 N개**가 된다. 이 표가 그 관계를 서버에 준다 — 교차 프로젝트
-- 인자·자원 스코프·보드 합산·발자국 귀속·Stop 처방이 전부 여기에 기댄다.
--
-- ★ **정본은 이 표가 아니다.** 루트 레포에 커밋된 `.flightdeck.yaml` 의 `workspace:`
--   블록이 정본이고 여기는 캐시다(설계 §8 의 "대상 ref 의 파일에서 읽는다" 그대로).
--   세션이 열릴 때 파일을 다시 읽어 이 표를 맞춘다 — 그래서 사람이 이 표를 손으로
--   고치면 다음 세션에 되돌아간다. 그것이 의도다: 명부가 두 자리에서 갈리면 어느
--   쪽이 참인지 판정할 축이 없다.
--
-- ★ **member_project 에 FK 를 안 건다.** 멤버 레포에서 세션이 아직 한 번도 안 열렸으면
--   `project` 행이 없기 때문이다. 명부는 **선언**이고 project 행은 **관측**이라, 선언이
--   관측을 기다리면 «등록했는데 doctor 가 아무 말도 안 한다»가 된다 — 정확히 이 항목이
--   없애려는 침묵이다. 대신 doctor 가 "명부에 있는데 아직 안 열린 프로젝트"를 말한다.
--
-- ★ **root_project 에는 건다**(ON DELETE CASCADE). 루트가 `fd project rm` 으로 원장에서
--   지워지면 그 명부는 가리킬 곳이 없다. 남겨 두면 다음에 같은 이름의 프로젝트가
--   생겼을 때 남의 명부를 물려받는다 — §3 이 없앤 «조상 트리 상속»과 같은 모양의 사고다.
--
-- ★ **path 는 루트 상대다.** 절대경로로 저장하면 편하지만(gitreader 가 바로 쓴다),
--   루트가 옮겨지거나 컨테이너의 마운트 지점이 바뀌는 순간 명부 전체가 조용히 틀린다.
--   절대경로가 필요한 자리는 루트의 `project.path` 와 합쳐서 만든다 — 합치는 자리가
--   하나뿐이라 틀리면 한 번에 틀리고, 그것은 doctor 가 잰다.
--
-- ★ **한 단계뿐이다.** 멤버가 다시 자기 멤버를 갖는 것(워크스페이스의 워크스페이스)은
--   읽지도 저장하지도 않는다. PK 가 그것을 막지는 않지만 — 멤버가 다른 워크스페이스의
--   루트로 등재되는 것은 스키마상 가능하다 — 읽는 코드가 한 단계에서 멈춘다.
--   필요가 실측되면 그때 넓힌다. 지금 넓히면 재귀 종료 조건이 없는 순회가 생긴다.
--
-- ★ 순수 가산이다 — CREATE TABLE·CREATE INDEX 뿐이라 migrate_guard_test.go 의
--   destructiveOps 여섯 축 어디에도 안 걸린다. 표를 더했으므로
--   TestDeclaredTablesMatchDesign 의 목록과 DESIGN §3 의 표 수가 함께 움직인다.

CREATE TABLE project_member (
  root_project   TEXT NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  member_project TEXT NOT NULL,
  -- 루트 상대 경로. 슬래시 구분이다(커밋된 파일에서 온 값이라 OS 와 무관하다)
  path           TEXT NOT NULL,
  -- 이 행이 명부에서 마지막으로 확인된 시각. 파일이 정본이라 «언제 읽은 캐시인가»가
  -- 곧 이 값의 뜻이다 — 낡은 명부를 최신인 척 읽지 않게 doctor 가 이것을 찍는다
  declared_at    TEXT NOT NULL,
  PRIMARY KEY (root_project, member_project)
);

-- 역방향 조회: "이 프로젝트는 어느 워크스페이스의 멤버인가".
-- 멤버 레포에서 띄운 세션이 자원 스코프를 물을 때 이 방향으로 읽는다.
CREATE INDEX project_member_by_member ON project_member(member_project);
