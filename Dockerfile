FROM golang:1.24-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o hunter-base .

FROM alpine:latest

WORKDIR /app

# Chromium and dependencies for chromedp
RUN apk add --no-cache \
    chromium \
    ca-certificates \
    tzdata

COPY --from=builder /app/hunter-base .
COPY --from=builder /app/api.yaml .

ENTRYPOINT ["/app/hunter-base"]
