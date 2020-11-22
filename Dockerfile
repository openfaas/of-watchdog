ARG goversion=1.15

FROM teamserverless/license-check:0.3.9 as license-check

FROM --platform=${BUILDPLATFORM:-linux/amd64} golang:$goversion as builder

ARG TARGETPLATFORM
ARG BUILDPLATFORM
ARG TARGETOS
ARG TARGETARCH

ARG GIT_COMMIT="000000"
ARG VERSION="dev"

COPY --from=license-check /license-check /usr/bin/

ARG CGO_ENABLED=0
ARG GO111MODULE="on"
ENV GOFLAGS=-mod=vendor
ARG GOPROXY=""

WORKDIR /app
COPY vendor              vendor
COPY config              config
COPY executor            executor
COPY metrics             metrics
COPY version.go          .
COPY main.go             .
COPY go.mod              .
COPY go.sum              .

RUN license-check -path  /app --verbose=false "Alex Ellis" "OpenFaaS Author(s)"
RUN gofmt -l -d $(find . -type f -name '*.go' -not -path "./vendor/*")
RUN go test -v ./...

RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} CGO_ENABLED=0 \
    go build --ldflags "-s -w \
    -X github.com/openfaas/of-watchdog/main.GitCommit=${GIT_COMMIT} \
    -X github.com/openfaas/of-watchdog/main.Version=${VERSION}" \
    -a -installsuffix cgo -o fwatchdog


FROM scratch as release

LABEL org.label-schema.license="MIT" \
    org.label-schema.vcs-url="https://github.com/openfaas/of-watchdog" \
    org.label-schema.vcs-type="Git" \
    org.label-schema.name="openfaas/of-watchdog" \
    org.label-schema.vendor="openfaas" \
    org.label-schema.docker.schema-version="1.0"

COPY --from=builder /app/fwatchdog /fwatchdog

ENTRYPOINT ["/fwatchdog"]
