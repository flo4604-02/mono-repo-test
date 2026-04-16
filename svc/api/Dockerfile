FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY . .

ARG SERVICE=api

FROM alpine:3.21

RUN apk add --no-cache ca-certificates
COPY --from=builder /service /service

ENTRYPOINT ["/service"]
