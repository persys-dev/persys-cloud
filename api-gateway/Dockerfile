FROM golang:1.19-alpine as build
RUN apk add build-base

WORKDIR /app

COPY . .

# RUN ls -la

RUN go mod download

RUN cd cmd && go build -o main

FROM alpine:latest as server

WORKDIR /app

COPY --from=build /app/config.toml .

COPY --from=build /app/cmd/main .

RUN chmod +x ./main

EXPOSE 8551

CMD [ "./main" ]