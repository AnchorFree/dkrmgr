FROM golang:1.19-alpine3.17 as builder
WORKDIR /tmp/
LABEL maintainer="v.zorin@anchorfree.com"

RUN apk add --no-cache git=2.40.0-r1 libc-dev=0.7.2-r5 gcc=12.2.1_git20220924-r10
COPY . /tmp 
RUN cd /tmp && go build

FROM alpine:3.17
WORKDIR /tmp/
LABEL maintainer="v.zorin@anchorfree.com"

COPY --from=builder /cmd/dkrmgr /usr/local/bin/dkrmgr

ENTRYPOINT ["/usr/local/bin/dkrmgr"]
