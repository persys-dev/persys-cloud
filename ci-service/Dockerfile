FROM golang:1.19-alpine as build
RUN apk add build-base

WORKDIR /app

COPY . .

RUN go mod download

RUN cd cmd && go build -o main

FROM alpine:latest as server
RUN apk add --update docker openrc

WORKDIR /app

COPY --from=build /app/cmd/main .

RUN chmod +x ./main

EXPOSE 8555

CMD [ "./main" ]