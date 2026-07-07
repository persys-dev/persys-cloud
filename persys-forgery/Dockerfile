FROM golang:1.25-alpine AS builder
WORKDIR /src

# go.mod/go.sum first for layer caching. Both are needed because
# persys-forgery/go.mod does: replace github.com/persys-dev/persys-cloud/pkg => ../pkg
COPY pkg/go.mod pkg/go.sum ./pkg/
COPY persys-forgery/go.mod persys-forgery/go.sum ./persys-forgery/

WORKDIR /src/persys-forgery
RUN go mod download

WORKDIR /src
COPY pkg ./pkg
COPY persys-forgery ./persys-forgery

WORKDIR /src/persys-forgery
RUN go build -o persys-forgery ./cmd/main.go

FROM alpine:latest
WORKDIR /app

COPY --from=builder /src/persys-forgery/persys-forgery /app/persys-forgery
COPY --from=builder /src/persys-forgery/config.yaml /app/config.yaml

RUN apk add git
EXPOSE 8080
CMD ["/app/persys-forgery"]