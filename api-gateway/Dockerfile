FROM golang:1.19-alpine as build
RUN apk add build-base

WORKDIR /app

COPY go.mod .

COPY go.sum .

RUN go mod download

COPY . .

RUN ls -la

RUN cd cmd && go build -o main

FROM alpine:latest as server

WORKDIR /app

COPY --from=build /app/config.toml .

COPY --from=build /app/cmd/main .

RUN chmod +x ./main

EXPOSE 8551

CMD [ "./main" ]