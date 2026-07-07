FROM golang:1.25 AS build
WORKDIR /src

# go.mod/go.sum first for layer caching. Both are needed because
# persys-automation/go.mod does: replace github.com/persys-dev/persys-cloud/pkg => ../pkg
COPY pkg/go.mod pkg/go.sum ./pkg/
COPY persys-automation/go.mod persys-automation/go.sum ./persys-automation/

WORKDIR /src/persys-automation
RUN go mod download

WORKDIR /src
COPY pkg ./pkg
COPY persys-automation ./persys-automation

WORKDIR /src/persys-automation
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/persys-automation ./cmd/automation

FROM alpine:latest
COPY --from=build /out/persys-automation /usr/local/bin/persys-automation
ENTRYPOINT ["/usr/local/bin/persys-automation"]