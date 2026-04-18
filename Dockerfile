# syntax=docker/dockerfile:1.7

FROM golang:1.25-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags='-s -w -buildid=' -o /out/s000 ./cmd/s000

FROM alpine:3.22
RUN addgroup -S s000 && adduser -S -G s000 -u 10001 s000
WORKDIR /var/lib/s000
COPY --from=build /out/s000 /usr/local/bin/s000
RUN mkdir -p /var/lib/s000/data && chown -R s000:s000 /var/lib/s000

ENV S000_ADDR=:9000 \
    S000_DATA_DIR=/var/lib/s000/data \
    S000_METADATA_DSN=file:/var/lib/s000/data/s000-metadata.db

VOLUME ["/var/lib/s000/data"]
EXPOSE 9000
USER s000:s000
ENTRYPOINT ["/usr/local/bin/s000"]
