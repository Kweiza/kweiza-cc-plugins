package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/store"
)

// 응답 조립 — 성공은 그대로, 실패는 걸러서.

const contentTypeJSON = "application/json; charset=utf-8"

// writeJSON 은 성공 응답을 낸다.
//
// service 계층의 결과 타입을 **그대로** 직렬화한다. 표면마다 DTO 를 새로 만들면
// 같은 값의 모양이 두 벌이 되고, 두 벌은 반드시 표류한다 — 그때 어느 쪽이 참인지
// 말해 주는 자리가 없다(service 패키지 주석의 규율을 그대로 따른다).
func (s *server) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		// 여기까지 왔으면 응답 타입 자체가 직렬화 불가다. 소비자에게는 내부를 말하지 않는다.
		s.logInternal(r, "응답 직렬화 실패", err)
		s.writeError(w, r, Classified{
			Status: http.StatusInternalServerError, Code: "encode_failed",
			Message: "응답을 만들 수 없다. 원인 전문은 서버 로그에 있다", Internal: true,
		})
		return
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		s.log.WarnContext(r.Context(), "응답 쓰기 실패", "error", err.Error())
	}
}

// writeError 는 분류된 오류를 낸다. request_id 를 반드시 싣는다.
func (s *server) writeError(w http.ResponseWriter, r *http.Request, c Classified) {
	id := ""
	if info := infoFrom(r.Context()); info != nil {
		id = info.requestID
	}
	body, err := json.Marshal(ErrorBody{Error: ErrorDetail{
		Code: c.Code, Message: c.Message, Guidance: c.Guidance, RequestID: id,
	}})
	if err != nil {
		// 오류 본문조차 못 만들면 상태코드만이라도 정확히 낸다.
		s.log.ErrorContext(r.Context(), "오류 본문 직렬화 실패", "request_id", id, "error", err.Error())
		w.Header().Set("Content-Type", contentTypeJSON)
		w.WriteHeader(c.Status)
		return
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(c.Status)
	if _, err := w.Write(body); err != nil {
		s.log.WarnContext(r.Context(), "오류 응답 쓰기 실패", "request_id", id, "error", err.Error())
	}
}

// fail 은 하류 오류를 분류해 응답하고, **내부 오류일 때만** 원인 전문을 로그에 남긴다.
//
// 4xx 에 로그 의무가 없는 이유는 그것이 호출자가 고칠 거리이고 응답에 이미 전부 실려서다.
// 5xx 는 응답에서 원인을 뺀 대가로 로그가 유일한 원천이 되므로 반드시 남긴다.
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	c := ClassifyError(err)
	if c.Internal {
		s.logInternal(r, "요청 처리 실패", err)
	}
	s.writeError(w, r, c)
}

func (s *server) logInternal(r *http.Request, msg string, err error) {
	id := ""
	if info := infoFrom(r.Context()); info != nil {
		id = info.requestID
	}
	s.log.ErrorContext(r.Context(), msg,
		"route", RoutePattern(r.Pattern, r.Method),
		"request_id", id,
		"session_id", infoFrom(r.Context()).session(),
		"error", err.Error(),
	)
}

// decode 는 요청 본문을 읽는다. 실패하면 응답까지 내고 false 를 돌려준다.
//
// 모르는 필드는 **거절하지 않는다** — 클라이언트(플러그인)는 자동 갱신되므로
// 서버보다 새로운 필드를 보내는 구간이 정상적으로 존재한다(설계 §7 의 버전 스큐).
func (s *server) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	body := http.MaxBytesReader(w, r.Body, s.opt.MaxBodyBytes)
	err := json.NewDecoder(body).Decode(dst)
	switch {
	case err == nil:
		return true
	case errors.Is(err, io.EOF):
		s.writeError(w, r, badRequest("empty_body", "요청 본문이 비었다", "JSON 객체 하나를 실어라."))
		return false
	default:
		// 파서의 오류 문구에는 오프셋과 Go 타입명이 섞인다. 응답에는 싣지 않는다.
		s.log.WarnContext(r.Context(), "요청 본문 해석 실패",
			"route", RoutePattern(r.Pattern, r.Method), "error", err.Error())
		s.writeError(w, r, badRequest("bad_json", "요청 본문을 JSON 으로 읽을 수 없다",
			"필드 이름과 타입을 확인해라 — 자세한 위치는 서버 로그에 있다."))
		return false
	}
}

// requireQuery 는 필수 질의 인자를 읽는다. 없으면 응답까지 내고 false 다.
func (s *server) requireQuery(w http.ResponseWriter, r *http.Request, name, why string) (string, bool) {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		s.writeError(w, r, badRequest("missing_"+name, "질의 인자 "+name+" 가 없다", why))
		return "", false
	}
	return v, true
}

// queryBool 은 기본값이 있는 불리언 인자다. 해석 실패는 기본값으로 접지 않고 거절한다 —
// 조용히 접으면 오타 하나가 "이 축을 껐다"로 둔갑한다.
func queryBool(r *http.Request, name string, def bool) (bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return def, nil
	}
	return strconv.ParseBool(raw)
}

// queryInt 는 기본값이 있는 정수 인자다.
func queryInt(r *http.Request, name string, def int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return def, nil
	}
	return strconv.Atoi(raw)
}

// queryDuration 은 기본값이 있는 구간 인자다("8h" 같은 Go 표기).
func queryDuration(r *http.Request, name string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return def, nil
	}
	return time.ParseDuration(raw)
}

// publish 는 변화 하나를 SSE 로 민다. **실패해도 요청을 죽이지 않는다** —
// 알림은 최적화이지 정본이 아니다. 다만 삼키지도 않는다(WARN 으로 사유를 남긴다).
func (s *server) publish(r *http.Request, kind, project, sessionID string, detail map[string]any) {
	err := s.hub.Publish(store.NewID(), Event{
		Kind: kind, Project: project, SessionID: sessionID, At: s.now(), Detail: detail,
	})
	if err != nil {
		s.log.WarnContext(r.Context(), "이벤트 발행 실패",
			"mode", clip(kind, 64), "error", err.Error())
	}
}
