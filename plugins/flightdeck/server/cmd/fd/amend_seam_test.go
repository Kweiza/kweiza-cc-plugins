package main

import (
	"context"
	"testing"
)

// `fd amend` 의 **CLI 이음매**를 지킨다.
//
// ★ 무엇이 위험한가: cmd/fd 의 amendReq 필드 이름이 internal/api 의 amendRequest 와
// 어긋나면 서버가 **조용히 0값을 받는다.** JSON 디코딩은 모르는 필드를 버리고 없는 필드를
// 0값으로 두므로, 오타 하나가 오류가 아니라 빈 값으로 나타난다. label_seam_test.go(이 파일이
// 그대로 베낀 원본)와 같은 이유로 실물 서버를 세워 **서버가 실제로 갖게 된 상태**를
// 단정한다 — 가짜 서버로는 그 어긋남을 원리적으로 못 본다.
func TestAmendCLISeamActuallyChangesStoredItem(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const item = "t-amend-seam"

	if code, out := h.run("", "add", "--id", item, "--title", "옛 제목", "--body", "옛 본문", "--path", "old/path.go"); code != 0 {
		t.Fatalf("전제 구성 실패 — add 가 %d 로 끝났다:\n%s", code, out)
	}

	// ── 대조가 성립했는지 먼저 단정한다 ─────────────────────────────────────
	pre, err := h.st.GetItem(ctx, h.project, item)
	if err != nil {
		t.Fatalf("전제가 깨졌다 — 항목 조회 실패: %v", err)
	}
	if pre.Title != "옛 제목" || pre.Body != "옛 본문" || len(pre.Paths) != 1 || pre.Paths[0] != "old/path.go" {
		t.Fatalf("전제가 깨졌다 — 시작 상태가 %+v 다", pre)
	}

	// ★ title·body·path 를 **한 호출에 함께** 준다. wire.go 의 amendReq 에서 필드이
	// 서로 뒤바뀌면(예: Title 값이 Body 자리로, Paths 가 빠지는 등) 이 시험이 잡는다 —
	// 따로따로 부르면 각 축이 단독으로는 "그대로 반영된 것처럼" 보일 수 있다
	// (label_seam_test.go 의 Add/Rm 이 뒤바뀌는 결함과 같은 결).
	code, out := h.run("", "amend", item,
		"--title", "새 제목", "--body", "새 본문", "--path", "new/path.go", "--reason", "이음매 시험")
	if code != 0 {
		t.Fatalf("amend 가 %d 로 끝났다:\n%s", code, out)
	}

	// ★ 이 단정이 이 시험의 본체다: **서버가 실제로 갖게 된 항목의 상태.**
	got, err := h.st.GetItem(ctx, h.project, item)
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if got.Title != "새 제목" {
		t.Errorf("제목이 %q다 — \"새 제목\"이어야 한다", got.Title)
	}
	if got.Body != "새 본문" {
		t.Errorf("본문이 %q다 — \"새 본문\"이어야 한다", got.Body)
	}
	if len(got.Paths) != 1 || got.Paths[0] != "new/path.go" {
		t.Errorf("경로가 %v다 — [\"new/path.go\"] 여야 한다", got.Paths)
	}

	// 화면도 mcpsrv.RenderAmend 를 통해 실제 변화분을 낸다는 규율을 함께 잠근다.
	mustContain(t, "amend 출력", out, "title·body·paths 를 고쳤다", "제목: 새 제목", "경로 1: new/path.go")
}

// TestAmendCLIOmittedFlagAxisStaysUntouched 는 fs.Visit 판정이 실제로 동작하는지 본다.
//
// ★ 기본값 비교로 가르면 `--title ""`(생략)와 "빈 문자열로 바꿔라"가 안 갈린다.
// 이 시험은 그 반대 방향을 잡는다: --title 을 아예 안 준 호출이 제목을 건드리지
// 않아야 한다 — 건드리면 amendReq.Title 이 기본값 비교(빈 문자열 == 생략)로
// 판정됐다는 뜻이고, 그러면 `fd amend <id> --body …`가 매번 제목을 지운다.
func TestAmendCLIOmittedFlagAxisStaysUntouched(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const item = "t-amend-omit"

	if code, out := h.run("", "add", "--id", item, "--title", "지킬 제목", "--body", "옛 본문"); code != 0 {
		t.Fatalf("전제 구성 실패 — add 가 %d 로 끝났다:\n%s", code, out)
	}

	code, out := h.run("", "amend", item, "--body", "새 본문", "--reason", "본문만 고친다")
	if code != 0 {
		t.Fatalf("amend 가 %d 로 끝났다:\n%s", code, out)
	}

	got, err := h.st.GetItem(ctx, h.project, item)
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if got.Title != "지킬 제목" {
		t.Errorf("제목이 %q다 — --title 을 안 줬으면 안 건드려야 한다", got.Title)
	}
	if got.Body != "새 본문" {
		t.Errorf("본문이 %q다 — \"새 본문\"이어야 한다", got.Body)
	}
	mustContain(t, "amend 출력", out, "amend · "+item+" 의 body 를 고쳤다")
}

// TestAmendCLIExplicitEmptyBodyActuallyClearsIt 은 이 태스크의 머릿돌 — fs.Visit 판정 —
// 을 **생략과 빈 값을 실제로 가르는 방향**에서 잠근다.
//
// ★ 왜 필요한가(재리뷰 I-2). 위 TestAmendCLIOmittedFlagAxisStaysUntouched 는 "안 주면
// 안 건드린다"만 본다. 그런데 `given["title"]` 판정을 `*title != ""` 같은 기본값 비교로
// 바꿔도 그 시험과 이음매 시험(둘 다 값이 항상 안 비었다) 은 **똑같이 초록**이다 —
// 두 갈래(생략 vs 명시적 빈 값)를 가르는 유일한 입력이 이 시험이 서기 전까지 레포에
// 없었다. `--body ""`(빈 문자열로 **바꿔라**라는 명시적 요청)를 실제로 쳐서 저장된
// 값이 진짜 빈 문자열이 되는지 본다 — 기본값 비교로 퇴행하면 이 요청이 "생략"으로
// 잘못 접혀 옛 본문이 그대로 남는데, 그 결함은 위 두 시험 어느 쪽도 못 잡는다.
func TestAmendCLIExplicitEmptyBodyActuallyClearsIt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const item = "t-amend-explicit-empty"

	if code, out := h.run("", "add", "--id", item, "--title", "제목", "--body", "지울 본문"); code != 0 {
		t.Fatalf("전제 구성 실패 — add 가 %d 로 끝났다:\n%s", code, out)
	}
	pre, err := h.st.GetItem(ctx, h.project, item)
	if err != nil {
		t.Fatalf("전제가 깨졌다 — 항목 조회 실패: %v", err)
	}
	if pre.Body != "지울 본문" {
		t.Fatalf("전제가 깨졌다 — 시작 본문이 %q다", pre.Body)
	}

	code, out := h.run("", "amend", item, "--body", "", "--reason", "본문을 비운다")
	if code != 0 {
		t.Fatalf("amend 가 %d 로 끝났다:\n%s", code, out)
	}

	got, err := h.st.GetItem(ctx, h.project, item)
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if got.Body != "" {
		t.Errorf("본문이 %q다 — `--body \"\"`는 빈 값으로 바꾸라는 명시적 요청이라 빈 "+
			"문자열이어야 한다. 기본값 비교로 퇴행하면 이 요청이 생략으로 접혀 옛 본문(%q)이 "+
			"그대로 남는다", got.Body, pre.Body)
	}
	// 제목은 안 줬으니 그대로여야 한다 — 이 시험이 body 축 하나만 재는지 함께 확인한다.
	if got.Title != "제목" {
		t.Errorf("제목이 %q다 — --title 을 안 줬으면 안 건드려야 한다", got.Title)
	}
	mustContain(t, "amend 출력", out, "amend · "+item+" 의 body 를 고쳤다")
}

// TestAmendCLIEmptyPathClearsListInsteadOfStoringBlankEntry 는 `--path ""` 가
// **경로 목록을 비운다**는 뜻이 되는지 본다 — `[""]`(빈 문자열 한 칸)이 그대로
// 저장되면 안 된다(재리뷰 I-3).
//
// ★ 왜 필요한가. `stringList.Set`(:29)은 공백을 안 거르므로 `--path ""` 는 `paths`
// 슬라이스에 빈 문자열 하나를 그대로 쌓는다. `nonBlankPositionals` 로 거르지 않으면
// 그 값이 그대로 서버에 실려 store 가 항목 paths 에 빈 칸 하나를 저장한다 — 서버는
// `paths: []`(경로 없음)을 이미 일급으로 다루는데(겹침 계산을 아예 안 돈다, 커밋
// 4b796b7) CLI 로는 그 상태에 못 가고 대신 쓰레기 값 하나가 남는 셈이다. 그리고
// `--path` 플래그 설명("한 번이라도 주면 목록 전체를 이것으로 바꾼다")을 읽은
// 사람은 정확히 `--path ""` 를 쳐서 "경로 축을 비워라"를 표현하려 할 것이다.
func TestAmendCLIEmptyPathClearsListInsteadOfStoringBlankEntry(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const item = "t-amend-empty-path"

	if code, out := h.run("", "add", "--id", item, "--title", "제목", "--body", "본문", "--path", "old/path.go"); code != 0 {
		t.Fatalf("전제 구성 실패 — add 가 %d 로 끝났다:\n%s", code, out)
	}
	pre, err := h.st.GetItem(ctx, h.project, item)
	if err != nil {
		t.Fatalf("전제가 깨졌다 — 항목 조회 실패: %v", err)
	}
	if len(pre.Paths) != 1 || pre.Paths[0] != "old/path.go" {
		t.Fatalf("전제가 깨졌다 — 시작 경로가 %v 다", pre.Paths)
	}

	code, out := h.run("", "amend", item, "--path", "", "--reason", "경로를 비운다")
	if code != 0 {
		t.Fatalf("amend 가 %d 로 끝났다:\n%s", code, out)
	}

	got, err := h.st.GetItem(ctx, h.project, item)
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if len(got.Paths) != 0 {
		t.Errorf("경로가 %v다 — `--path \"\"`는 목록을 비우라는 요청이라 빈 슬라이스여야 "+
			"한다. 빈 문자열 한 칸이 그대로 저장됐다면 nonBlankPositionals 를 안 거친 것이다",
			got.Paths)
	}
	mustContain(t, "amend 출력", out, "경로가 없으면 이 항목은 겹침 축에 안 잡힌다")
}

// TestAmendCLIRejectsMissingArgsOffline 은 runAmend 의 사전 거절 세 갈래(id 없음·축
// 없음·사유 없음)가 종료코드 2 로 끝나는지 잠근다(재리뷰 M-7).
//
// ★ 왜 필요한가. 이 셋을 서버까지 안 보내고 클라이언트에서 막는 이유는 cmds.go 의
// 주석이 적은 그대로다 — 서버도 거절하지만 그 왕복은 오프라인에서 "서버 미도달"
// 오류가 되어 사용자에게 실제 원인(인자)이 아니라 "서버에 못 닿았다"로 보인다.
// 회귀 방지가 없으면 이 사전 거절 셋이 조용히 없어져도(예: 서버 왕복으로 되돌아가도)
// 아무 시험도 안 잡는다 — 세 갈래 다 항목이 실재하지 않아도 서버에 안 닿고 끝나야
// 하므로, 존재하지 않는 id 로도 이 시험이 성립한다.
func TestAmendCLIRejectsMissingArgsOffline(t *testing.T) {
	h := newHarness(t)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"항목 id 없음", []string{"amend", "--reason", "사유"}, "고칠 항목 id 를 줘라"},
		{"축 없음", []string{"amend", "t-amend-missing", "--reason", "사유"}, "고칠 축을 하나는 줘라"},
		{"사유 없음", []string{"amend", "t-amend-missing", "--title", "새 제목"}, "고친 사유를 줘라"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out := h.run("", c.args...)
			if code != 2 {
				t.Fatalf("종료코드가 %d 다(2 여야 한다) — 서버 왕복이 됐다면 오프라인에서 "+
					"'서버 미도달'로 오진된다:\n%s", code, out)
			}
			mustContain(t, "amend 거절 출력", out, c.want)
		})
	}
}
