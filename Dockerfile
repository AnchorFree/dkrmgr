FROM golang:1.10-alpine3.7 as builder
LABEL maintainer="v.zorin@anchorfree.com"

RUN apk add --no-cache git
COPY cmd /go/src/github.com/anchorfree/dkrmgr/cmd
COPY Gopkg.toml /go/src/github.com/anchorfree/dkrmgr/
COPY Gopkg.lock /go/src/github.com/anchorfree/dkrmgr/

RUN cd /go && go get -u github.com/golang/dep/cmd/dep
RUN cd /go/src/github.com/anchorfree/dkrmgr/ && dep ensure
RUN cd /go && go build github.com/anchorfree/dkrmgr/cmd/dkrmgr

FROM alpine:3.7
LABEL maintainer="v.zorin@anchorfree.com"

COPY --from=builder /go/dkrmgr /usr/local/bin/dkrmgr

ENTRYPOINT ["/usr/local/bin/dkrmgr"]
