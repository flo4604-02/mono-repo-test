FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY . .

RUN sleep 120

RUN CGO_ENABLED=0 go build -o /service ./svc/h2c

FROM alpine:3.21

RUN apk add --no-cache ca-certificates
COPY --from=builder /service /service

ENTRYPOINT ["/service"]
