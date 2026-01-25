FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates build-base sqlite-dev

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG COMMIT_HASH
ARG BUILD_TIME
ARG VERSION=0.1.0

RUN CGO_ENABLED=1 go build -tags "goolm" -o /usr/bin/mautrix-claude \
    -ldflags "-s -w \
        -X main.Tag=${VERSION} \
        -X main.Commit=${COMMIT_HASH:-$(git rev-parse HEAD 2>/dev/null || echo unknown)} \
        -X 'main.BuildTime=${BUILD_TIME:-$(date -Iseconds)}'" \
    ./cmd/mautrix-claude

FROM alpine:3.20

ENV UID=1337 \
    GID=1337

RUN apk add --no-cache su-exec ca-certificates bash jq yq curl sqlite-libs

COPY --from=builder /usr/bin/mautrix-claude /usr/bin/mautrix-claude
COPY docker-run.sh /docker-run.sh
RUN chmod +x /docker-run.sh

VOLUME /data
WORKDIR /data

CMD ["/docker-run.sh"]
