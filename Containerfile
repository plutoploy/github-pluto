FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /plutoploy-gh-bot .

FROM alpine:3.20

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /plutoploy-gh-bot .

RUN mkdir -p /app/data

EXPOSE 8080

ENTRYPOINT ["/app/plutoploy-gh-bot"]
