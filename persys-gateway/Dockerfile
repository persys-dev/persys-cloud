FROM golang:1.24-alpine AS build

WORKDIR /app

# Copy go.mod and go.sum to download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application code
COPY . .

RUN cd cmd && go build -o main

FROM alpine:latest AS server

WORKDIR /app

COPY --from=build /app/config.toml .

COPY --from=build /app/cmd/main .

RUN chmod +x ./main

EXPOSE 8551

CMD [ "./main" ]