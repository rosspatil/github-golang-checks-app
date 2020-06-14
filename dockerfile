
FROM alpine 

ADD .  /go/src/github.com/rosspatil/github-golang-checks-app

ENV GOPATH /go
ENV BINDIR /go/bin
ENV PATH $GOPATH/bin:$PATH

RUN apk add --no-cache ca-certificates \
    git bash musl-dev openssl go  \
    && mkdir -p "$GOPATH/src" "$GOPATH/bin" \
    && chmod -R 777 "$GOPATH" 

RUN wget -O- -nv https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s v1.27.0
RUN which golangci-lint

ENV PATH=$PATH:/usr/local/go/bin
RUN go version

WORKDIR /go/src/github.com/rosspatil/github-golang-checks-app
RUN go build -o github-checks-app

EXPOSE 8080
ENTRYPOINT [ "./github-checks-app" ]
