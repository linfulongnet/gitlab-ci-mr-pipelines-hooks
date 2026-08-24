# 构建阶段
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY main.go ./
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/gateway .

# 运行阶段
FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
USER app
COPY --from=build /out/gateway /usr/local/bin/gateway
COPY config.example.yaml /etc/gateway/config.yaml
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/gateway", "-config", "/etc/gateway/config.yaml"]
