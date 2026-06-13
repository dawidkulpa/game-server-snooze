# First stage: build
FROM golang:1.23-alpine AS builder

# environment variables for a static build (for minimal deps):
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64

WORKDIR /app-src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o /app ./cmd/main.go


# Second stage: minimal runtime
FROM alpine:3.24

COPY --from=builder /app /usr/local/bin/game-server-snooze

ENTRYPOINT ["/usr/local/bin/game-server-snooze"]
