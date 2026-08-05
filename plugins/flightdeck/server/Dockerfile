# flightdeck 조정 서버 — 멀티스테이지, CGO 0.
#
# CGO 를 끄는 것이 첫 결정이다. SQLite 드라이버가 순수 Go 구현(modernc.org/sqlite)이라
# 가능하고, 그래서 이 바이너리 자체는 libc 조차 필요 없다.
#
# ★ 그런데도 최종 이미지가 distroless 가 아니라 alpine+git 이다. 이 서버의 파생이
#   **git 을 exec 로 부르기 때문이다**(internal/gitreader — for-each-ref · log · diff ·
#   merge-base · worktree list · status). 브랜치 · sha · 미커밋 발자국 · 경로 겹침이
#   전부 그 축 위에 서 있어서, git 없는 이미지에서는 보드가 `브랜치 ?(못 읽음)` 과
#   `파생 결손` 만 낸다 — 서버는 멀쩡히 200 을 내므로 **고장이 안 보인 채로 반쪽이 된다.**
#   설계 §2 의 "공급망 표면을 바이너리 하나로"를 여기서 한 번 접는다. 접지 않으면
#   조정 계층의 절반이 없다.

FROM golang:1.25-alpine AS build
WORKDIR /src

# 의존을 먼저 받는다. 소스만 바뀌었을 때 이 계층이 캐시된다.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# -trimpath: 빌드 머신의 경로가 바이너리에 안 박힌다(재현 가능성 + 정보 노출).
# -s -w: 디버그 심볼 제거. 진단은 구조화 로그가 하지 스택 심볼이 하지 않는다.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/fd ./cmd/fd

# ── 헬스체크용 최소 도구 ──────────────────────────────────────────────────
# 셸도 wget 도 쓰지 않는다. HEALTHCHECK 를 **바이너리 자신**이 한다:
# `fd doctor` 는 서버를 치고, 미도달이면 0 이 아닌 값을 낸다.

FROM alpine:3.21
# git 하나만 넣는다. 이것이 파생의 유일한 외부 의존이다.
RUN apk add --no-cache git
WORKDIR /data
COPY --from=build /out/fd /usr/local/bin/fd

# 볼륨: DB · 판단 백업 · 로그가 전부 여기다(설계 §2).
VOLUME ["/data"]
EXPOSE 7420

ENV FD_ADDR=":7420" \
    FD_DB="/data/fd.db" \
    FD_URL="http://127.0.0.1:7420"

# 저장소는 읽기 전용으로 마운트된다. `status` 는 인덱스를 갱신하려 락을 잡는데,
# 그 자리가 못 쓰면 관측이 통째로 실패한다 — 갱신을 아예 포기시킨다(읽기만 한다).
ENV GIT_OPTIONAL_LOCKS="0"

# 컨테이너 uid 와 저장소 소유자가 어긋나면 git 이 `dubious ownership` 으로 거절한다.
# 파일에 안 쓰고 환경으로 준다 — 이미지에 설정 파일을 남기지 않기 위해서다.
ENV GIT_CONFIG_COUNT="1" \
    GIT_CONFIG_KEY_0="safe.directory" \
    GIT_CONFIG_VALUE_0="*"

# ★ 헬스체크가 /healthz 를 친다. 종료코드가 아니라 **그 응답**이 판정 근거다 —
#   서버 프로세스가 살아 있는 것과 DB 가 열려 있는 것은 다른 사실이고,
#   /healthz 의 db_ok 가 후자를 말한다.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/usr/local/bin/fd", "doctor"]

# 기본값일 뿐이다 — compose 가 `user:` 로 덮는다. 볼륨(DB)과 마운트(저장소)의 주인이
# 호스트 사용자라, 그 uid 로 돌지 않으면 DB 는 못 쓰고 git 은 소유권을 의심한다.
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/fd"]
CMD ["serve"]
