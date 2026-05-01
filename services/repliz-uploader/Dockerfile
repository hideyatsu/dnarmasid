# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy go.mod and go.sum from root
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY shared/ ./shared/
COPY services/repliz-uploader/ ./services/repliz-uploader/

# Build
WORKDIR /app/services/repliz-uploader
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/repliz-uploader .

# Run stage
FROM alpine:latest

WORKDIR /app

# Add timezone data & ca-certificates (needed for HTTPS)
RUN apk --no-cache add tzdata ca-certificates

COPY --from=builder /app/bin/repliz-uploader .

CMD ["./repliz-uploader"]
