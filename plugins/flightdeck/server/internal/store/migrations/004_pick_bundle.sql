-- 004 · pick_eval 이 묶음을 담는다 (schema_version 3 → 4)
--
-- ★ picked 를 안 바꾼다. 그 칸은 **선두**를 계속 담는다 —
--   선두 id 가 곧 브랜치 이름이고, 기존 행과 기존 분포 질의가 그 칸을 읽는다.
--   배열로 승격하면 옛 행(평문 id)과 새 행(JSON)이 같은 칸에서 갈리고,
--   그 순간 이 표를 읽는 모든 질의가 두 형식을 알아야 한다.
--
-- ★ NULL 을 빈 배열로 접지 않는다. NULL 은 "단독이었다"이고,
--   이 컬럼이 없던 시절의 행도 NULL 이라 둘이 같은 뜻이다 — 정확하다.
ALTER TABLE pick_eval ADD COLUMN picked_with TEXT;
