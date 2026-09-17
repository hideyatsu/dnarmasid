FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN apk add --no-cache git ca-certificates
RUN go mod download

COPY shared/ ./shared/
COPY services/storage/ ./services/storage/
COPY services/video-short-generator/ ./services/video-short-generator/

RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o /bin/video-short-generator ./services/video-short-generator

FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata ffmpeg chromium python3 py3-pip && \
    pip3 install --break-system-packages edge-tts

WORKDIR /app

COPY --from=builder /bin/video-short-generator .
COPY --from=builder /app/services/video-short-generator/templates ./templates

# Volume untuk output video
VOLUME ["/app/volumes/video"]

CMD ["./video-short-generator"]
