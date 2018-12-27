FROM golang:1.11.1-alpine as builder
MAINTAINER siddontang

RUN apk add --no-cache \
    make \
    git

COPY . /go/src/pd
WORKDIR /go/src/pd

RUN make

FROM alpine:3.5

COPY --from=builder /go/src/pd/bin/pd-server /pd-server

EXPOSE 2379 2380

ENTRYPOINT ["/pd-server"]
