# build stage
FROM golang:1.20.4-alpine3.17 AS builder
ADD . /src
RUN apk add git
RUN cd /src && go build -o /src/bin/lantern -v /src/server/cmd/

# final stage
FROM alpine:3.17
ENV LANTERN_DEFAULT_TTL_SECONDS=3600
ENV LANTERN_PORT=6380

WORKDIR /app
COPY --from=builder /src/bin/lantern /tmp/lantern
ENTRYPOINT /tmp/lantern