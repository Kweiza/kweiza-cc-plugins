package main

// REST 본문 타입 — **internal/api 의 요청 구조체와 필드 이름이 1:1 이어야 한다.**
//
// 그쪽 타입은 비공개라 여기서 다시 선언한다. 그래서 위험이 하나 생긴다:
// 이름이 갈라지면 서버가 조용히 0값을 받는다(빠진 필드는 오류가 아니다).
// 그 위험을 시험이 막는다 — wire_test.go 가 실제 서버에 붙여 왕복시킨다.
// 구조체를 눈으로 대조하는 시험은 쓰지 않는다: 사본을 단정하는 시험은 아무것도 지키지 못한다.

// afterWire 는 선행 조건이다. 셋 중 **정확히 하나**만 채운다.
// model.After 를 그대로 쓰지 않는 이유: 그 타입에는 json 태그가 없어
// 필드 이름이 "Item"·"SHA" 로 나가는데 서버는 "item"·"sha" 를 읽는다.
type afterWire struct {
	Item string `json:"item,omitempty"`
	Job  string `json:"job,omitempty"`
	SHA  string `json:"sha,omitempty"`
}

// linkWire 는 판단 링크다. 위와 같은 이유로 따로 있다.
type linkWire struct {
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id"`
}

type openReq struct {
	Project       string `json:"project"`
	ProjectPath   string `json:"project_path"`
	DefaultBranch string `json:"default_branch"`
	MachineID     string `json:"machine_id"`
	Hostname      string `json:"hostname"`
	Worktree      string `json:"worktree"`
	CCSessionID   string `json:"cc_session_id"`
	Label         string `json:"label"`
}

type beatReq struct {
	Kind  string   `json:"kind"`
	Paths []string `json:"paths,omitempty"`
}

type addReq struct {
	Project   string      `json:"project"`
	SessionID string      `json:"session_id"`
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	Body      string      `json:"body"`
	Paths     []string    `json:"paths,omitempty"`
	Labels    []string    `json:"labels,omitempty"`
	After     []afterWire `json:"after,omitempty"`
}

type claimReq struct {
	Project   string `json:"project"`
	SessionID string `json:"session_id"`
}

type followupReq struct {
	ID     string      `json:"id"`
	Title  string      `json:"title"`
	Body   string      `json:"body"`
	Paths  []string    `json:"paths,omitempty"`
	Labels []string    `json:"labels,omitempty"`
	After  []afterWire `json:"after,omitempty"`
}

type finishReq struct {
	Project     string        `json:"project"`
	SessionID   string        `json:"session_id"`
	Outcome     string        `json:"outcome"`
	Title       string        `json:"title,omitempty"`
	Body        string        `json:"body"`
	CloseReason string        `json:"close_reason,omitempty"`
	Followups   []followupReq `json:"followups,omitempty"`
	Links       []linkWire    `json:"links,omitempty"`
}

type noteReq struct {
	Project    string     `json:"project"`
	SessionID  string     `json:"session_id"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title,omitempty"`
	Body       string     `json:"body"`
	ItemID     string     `json:"item_id,omitempty"`
	Supersedes string     `json:"supersedes,omitempty"`
	Links      []linkWire `json:"links,omitempty"`
}

type counterReq struct {
	Project string `json:"project"`
}

type allocResp struct {
	Project string `json:"project"`
	Counter string `json:"counter"`
	Value   int64  `json:"value"`
}

// errBody 는 서버의 오류 응답이다(internal/api.ErrorBody 와 같은 모양).
// request_id 를 그대로 사람에게 보여준다 — 응답과 서버 로그를 잇는 유일한 열쇠다.
type errBody struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Guidance  string `json:"guidance"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}
