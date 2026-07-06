# Multi-stage build → tiny distroless image with a static relay binary.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /relay ./cmd/relay

FROM gcr.io/distroless/static-debian12
COPY --from=build /relay /relay
# Platforms set PORT; the relay honors it (cmd/relay/main.go). 8080 is the
# default and Fly's internal_port.
EXPOSE 8080
ENTRYPOINT ["/relay"]
