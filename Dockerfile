FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/radipod ./cmd/radipod

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 radipod \
    && useradd --uid 10001 --gid radipod --no-create-home radipod \
    && mkdir -p /app/data \
    && chown radipod:radipod /app/data
COPY --from=build /out/radipod /usr/local/bin/radipod
COPY THIRD_PARTY_NOTICES.md /usr/share/doc/radipod/THIRD_PARTY_NOTICES.md
WORKDIR /app
USER 10001:10001
ENV TZ=Asia/Tokyo
EXPOSE 8080
ENTRYPOINT ["radipod"]
CMD ["run", "-config", "/app/config.json"]
