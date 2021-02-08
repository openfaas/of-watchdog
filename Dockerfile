FROM teamserverless/license-check:0.3.9 as license-check

FROM golang:1.15 as build
COPY --from=license-check /license-check /usr/bin/

ARG CGO_ENABLED=0
ARG GO111MODULE="on"
ARG GOPROXY=""

WORKDIR /go/src/github.com/openfaas/of-watchdog
COPY vendor              vendor
COPY config              config
COPY executor            executor
COPY metrics             metrics
COPY main.go             .
COPY go.mod              .
COPY go.sum              .

RUN license-check -path  /go/src/github.com/openfaas/of-watchdog --verbose=false "Alex Ellis" "OpenFaaS Author(s)"

# Run a gofmt and exclude all vendored code.
RUN test -z "$(gofmt -l $(find . -type f -name '*.go' -not -path "./vendor/*"))"

RUN go test -mod=vendor -v ./...

# Stripping via -ldflags "-s -w" 
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -a -ldflags "-s -w" -installsuffix cgo -o of-watchdog . \
    && CGO_ENABLED=0 GOOS=darwin go build -mod=vendor -a -ldflags "-s -w" -installsuffix cgo -o of-watchdog-darwin . \
    && GOARM=6 GOARCH=arm CGO_ENABLED=0 GOOS=linux go build -mod=vendor -a -ldflags "-s -w" -installsuffix cgo -o of-watchdog-armhf . \
    && GOARCH=arm64 CGO_ENABLED=0 GOOS=linux go build -mod=vendor -a -ldflags "-s -w" -installsuffix cgo -o of-watchdog-arm64 . \
    && GOOS=windows CGO_ENABLED=0 go build -mod=vendor -a -ldflags "-s -w" -installsuffix cgo -o of-watchdog.exe .
