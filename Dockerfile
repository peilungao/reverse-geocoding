FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/reverse_geocoding ./cmd/reverse_geocoding

FROM alpine:3.22

RUN addgroup -S app && adduser -S -G app app

USER app
WORKDIR /app
COPY --from=build /out/reverse_geocoding /usr/local/bin/reverse_geocoding

EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/reverse_geocoding"]
