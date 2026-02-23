FROM golang:1.24.13 AS build
WORKDIR /src
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/persys-automation ./cmd/automation

FROM gcr.io/distroless/base-debian12
COPY --from=build /out/persys-automation /usr/local/bin/persys-automation
ENTRYPOINT ["/usr/local/bin/persys-automation"]
