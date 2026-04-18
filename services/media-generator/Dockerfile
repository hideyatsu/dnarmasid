FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN apk add --no-cache git
RUN go mod download

COPY shared/ ./shared/
COPY services/media-generator/ ./services/media-generator/

RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o /bin/media-generator ./services/media-generator

FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata ffmpeg chromium

WORKDIR /app

COPY --from=builder /bin/media-generator .
# Salin template folder dari builder
COPY --from=builder /app/services/media-generator/templates ./templates

# Volume untuk output media
VOLUME ["/app/volumes/media"]

CMD ["./media-generator"]
