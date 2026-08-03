# flightdeck 조정 서버 — 멀티스테이지, CGO 0, distroless.
#
# CGO 를 끄는 것이 첫 결정이다. SQLite 드라이버가 순수 Go 구현(modernc.org/sqlite)이라
# 가능하고, 그래서 최종 이미지에 libc 조차 필요 없다 — 공급망 표면이 바이너리 하나로 줄어든다.

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
# distroless 에는 셸도 wget 도 없다. 그래서 HEALTHCHECK 를 **바이너리 자신**이 한다:
# `fd status` 는 서버를 치고, 미도달이면 0 이 아닌 값을 낸다.

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /data
COPY --from=build /out/fd /usr/local/bin/fd

# 볼륨: DB · 판단 백업 · 로그가 전부 여기다(설계 §2).
VOLUME ["/data"]
EXPOSE 7420

ENV FD_ADDR=":7420" \
    FD_DB="/data/fd.db" \
    FD_URL="http://127.0.0.1:7420"

# ★ 헬스체크가 /healthz 를 친다. 종료코드가 아니라 **그 응답**이 판정 근거다 —
#   서버 프로세스가 살아 있는 것과 DB 가 열려 있는 것은 다른 사실이고,
#   /healthz 의 db_ok 가 후자를 말한다.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/usr/local/bin/fd", "doctor"]

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/fd"]
CMD ["serve"]
