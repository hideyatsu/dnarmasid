FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN apk add --no-cache git
RUN go mod download

COPY shared/ ./shared/
COPY services/ai-generator/ ./services/ai-generator/

RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o /bin/ai-generator ./services/ai-generator

FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /bin/ai-generator .

CMD ["./ai-generator"]
