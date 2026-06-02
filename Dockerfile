# build stage
FROM golang:1.26-alpine AS builder
ADD . /src
RUN apk add --no-cache git
RUN cd /src && go build -o /src/bin/lantern -v ./server/cmd

# final stage
FROM alpine:3.20
ENV LANTERN_DEFAULT_TTL_SECONDS=3600
ENV LANTERN_PORT=6380

WORKDIR /app
COPY --from=builder /src/bin/lantern /tmp/lantern
ENTRYPOINT /tmp/lantern