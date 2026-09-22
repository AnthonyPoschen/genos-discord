# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/genos-discord ./cmd/genos-discord

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && addgroup -g 65532 -S nonroot \
    && adduser -u 65532 -S -G nonroot -H -h /nonexistent nonroot

USER 65532:65532

COPY --from=build /out/genos-discord /usr/local/bin/genos-discord

ENTRYPOINT ["/usr/local/bin/genos-discord"]
