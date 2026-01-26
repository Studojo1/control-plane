# Build stage
FROM golang:1.25-alpine AS builder
WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod tidy && go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -o server ./cmd/server

# Runtime stage
FROM alpine:latest
WORKDIR /app

RUN apk --no-cache add ca-certificates curl && \
    addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

COPY --from=builder /app/server .
RUN chown -R appuser:appuser /app
USER appuser

EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

CMD ["./server"]
