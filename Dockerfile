FROM golang:1.10-alpine3.7 as builder
LABEL maintainer="v.zorin@anchorfree.com"

RUN apk add --no-cache git
COPY cmd /go/src/github.com/anchorfree/docker-manager/cmd
COPY Gopkg.toml /go/src/github.com/anchorfree/docker-manager/
COPY Gopkg.lock /go/src/github.com/anchorfree/docker-manager/

RUN cd /go && go get -u github.com/golang/dep/cmd/dep
RUN cd /go/src/github.com/anchorfree/docker-manager/ && dep ensure
RUN cd /go && go build github.com/anchorfree/docker-manager/cmd/docker-manager

FROM alpine:3.7
LABEL maintainer="v.zorin@anchorfree.com"

COPY --from=builder /go/docker-manager /usr/local/bin/docker-manager

ENTRYPOINT ["/usr/local/bin/docker-manager"]
