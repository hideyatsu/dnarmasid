FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy module files dari root
COPY go.mod go.sum ./
RUN apk add --no-cache git
RUN go mod download

# Copy shared & service source
COPY shared/ ./shared/
COPY services/scheduler/ ./services/scheduler/

# Build binary
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o /bin/scheduler ./services/scheduler

# ─────────────────────────────────────────
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /bin/scheduler .

CMD ["./scheduler"]
