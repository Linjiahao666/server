FROM golang:1.25-alpine AS builder

WORKDIR /src
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN GOTOOLCHAIN=local GOSUMDB=sum.golang.google.cn CGO_ENABLED=0 GOOS=linux go build -mod=readonly -o /server ./cmd/server

FROM alpine:3.20

RUN apk add --no-cache ca-certificates
WORKDIR /app

COPY --from=builder /server /app/server
COPY migrations /app/migrations

EXPOSE 8080
ENTRYPOINT ["/app/server"]
