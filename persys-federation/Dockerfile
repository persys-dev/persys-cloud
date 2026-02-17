FROM golang:1.24-alpine AS build

WORKDIR /app

COPY . .

RUN go mod download

RUN cd cmd && go build -o main

FROM alpine:latest AS server

WORKDIR /app

COPY --from=build /app/cmd/main .

RUN chmod +x ./main

EXPOSE 8555

CMD [ "./main" ]