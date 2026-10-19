FROM golang:1.27.2 AS build

WORKDIR /src
COPY . ./
RUN CGO_ENABLED=0 go test -mod=vendor ./... \
    && mkdir -p /out \
    && CGO_ENABLED=0 go build -mod=vendor -trimpath -o /out/main ./cmd

FROM golang:1.27.2 AS runtime

LABEL org.opencontainers.image.source=https://github.com/supercakecrumb/otvali-xray-bot
LABEL org.opencontainers.image.description="Otvali Xray telegram bot"

WORKDIR /app

COPY --from=build --chown=65532:65532 /out/main /app/main

USER 65532:65532
HEALTHCHECK --interval=30s --timeout=5s --start-period=90s --retries=3 CMD ["/app/main", "--healthcheck"]

CMD ["/app/main"]
