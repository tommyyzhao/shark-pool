# shark-pool — multi-exit Surfshark VPN proxy
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/shark-pool ./cmd/shark-pool

FROM alpine:3.20
RUN apk add --no-cache openvpn iptables iproute2 curl ca-certificates
COPY --from=build /out/shark-pool /usr/local/bin/shark-pool
COPY docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8000 8888 1080 8100
ENTRYPOINT ["/entrypoint.sh"]
