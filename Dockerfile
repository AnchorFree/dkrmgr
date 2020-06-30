FROM golang:1.14-alpine3.12 as builder
LABEL maintainer="v.zorin@anchorfree.com"

RUN apk add --no-cache git libc-dev gcc 
COPY . /cmd 
RUN cd /cmd && go build

FROM alpine:3.12
LABEL maintainer="v.zorin@anchorfree.com"

COPY --from=builder /cmd/dkrmgr /usr/local/bin/dkrmgr

ENTRYPOINT ["/usr/local/bin/dkrmgr"]
