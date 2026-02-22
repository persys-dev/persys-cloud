FROM golang:1.24-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/vault-manager ./main.go

FROM alpine:latest

RUN apk add --no-cache ca-certificates \
	&& addgroup -S app \
	&& adduser -S -G app app

COPY --from=build /out/vault-manager /usr/local/bin/vault-manager

USER app
ENTRYPOINT ["vault-manager"]
