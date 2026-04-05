FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY shared/ ./shared/
COPY services/telegram-bot/ ./services/telegram-bot/

RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/telegram-bot ./services/telegram-bot

FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /bin/telegram-bot .

VOLUME ["/app/volumes/media"]

CMD ["./telegram-bot"]
