FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN apk add --no-cache git
RUN go mod download

COPY shared/ ./shared/
COPY services/telegram-bot/ ./services/telegram-bot/
COPY scripts/ ./scripts/

RUN go mod tidy && \
    CGO_ENABLED=0 GOOS=linux go build -o /bin/telegram-bot ./services/telegram-bot && \
    CGO_ENABLED=0 GOOS=linux go build -o /bin/test-push ./scripts/test-push

FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /bin/telegram-bot .
COPY --from=builder /bin/test-push .

VOLUME ["/app/volumes/media"]

CMD ["./telegram-bot"]
