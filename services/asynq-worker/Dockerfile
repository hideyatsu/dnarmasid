FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy module files dari root
COPY go.mod go.sum ./
RUN apk add --no-cache git
RUN go mod download

# Copy shared, internal & service source
COPY shared/ ./shared/
COPY internal/ ./internal/
COPY services/asynq-worker/ ./services/asynq-worker/

# Build binary
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o /bin/asynq-worker ./services/asynq-worker

# ─────────────────────────────────────────
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata
COPY --from=builder /bin/asynq-worker /bin/asynq-worker

CMD ["/bin/asynq-worker"]
