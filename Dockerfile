FROM golang:1.26-alpine AS builder

RUN apk add --no-cache tzdata ca-certificates
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/main ./main.go

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

WORKDIR /app
COPY --chown=65532:65532 --from=builder /app/main /app/main

ENV TZ=Asia/Tokyo

EXPOSE 8080

USER 65532:65532

CMD ["/app/main"]
