FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY . .

ARG SERVICE=api
RUN --mount=type=secret,id=env,target=/run/secrets/.env \
    echo "=== BUILD ENV VARS ===" && (cat /run/secrets/.env 2>/dev/null || true) && echo "=== END ===" && \
    CGO_ENABLED=0 go build -o /service ./svc/${SERVICE}

FROM alpine:3.21

RUN apk add --no-cache ca-certificates
COPY --from=builder /service /service

ENTRYPOINT ["/service"]
