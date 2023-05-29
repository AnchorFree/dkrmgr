FROM golang:1.19-alpine3.17 as builder
WORKDIR /cmd
LABEL maintainer="v.zorin@anchorfree.com"

RUN apk update && apk add --no-cache git libc-dev gcc
COPY . /cmd 
RUN cd /cmd && go build

FROM alpine:3.17
WORKDIR /cmd
LABEL maintainer="v.zorin@anchorfree.com"

COPY --from=builder /cmd/dkrmgr /usr/local/bin/dkrmgr

ENTRYPOINT ["/usr/local/bin/dkrmgr"]
