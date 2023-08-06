FROM golang:1.19 AS builder

COPY . /builder
WORKDIR /builder

RUN export GOPROXY=https://goproxy.cn && make build

FROM alpine:3

WORKDIR /app

EXPOSE 8080
COPY --from=builder /builder/bin .

ENTRYPOINT ["/app/azure-openai-proxy"]