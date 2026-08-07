FROM golang:1.25-alpine AS build
WORKDIR /src

# go.mod/go.sum first for layer caching. Both are needed because
# persys-gateway/go.mod does: replace github.com/persys-dev/persys-cloud/pkg => ../pkg
COPY pkg/go.mod pkg/go.sum ./pkg/
COPY persys-gateway/go.mod persys-gateway/go.sum ./persys-gateway/

WORKDIR /src/persys-gateway
RUN go mod download

WORKDIR /src
COPY pkg ./pkg
COPY persys-gateway ./persys-gateway

WORKDIR /src/persys-gateway
RUN cd cmd && go build -o persys-gateway

FROM alpine:latest AS server
WORKDIR /app

COPY --from=build /src/persys-gateway/config.yaml .
COPY --from=build /src/persys-gateway/cluster.yaml .
COPY --from=build /src/persys-gateway/catalog.yaml .
COPY --from=build /src/persys-gateway/cmd/persys-gateway .

RUN chmod +x ./persys-gateway
EXPOSE 8551
CMD [ "./persys-gateway" ]