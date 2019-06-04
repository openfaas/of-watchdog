FROM openfaas/of-watchdog:build as build
FROM scratch

ARG PLATFORM

COPY --from=build /go/src/github.com/openfaas-incubator/of-watchdog/of-watchdog$PLATFORM ./fwatchdog