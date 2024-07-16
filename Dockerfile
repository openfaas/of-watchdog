FROM scratch AS cache

COPY bin    .

FROM scratch AS ship

ARG TARGETPLATFORM
ARG BUILDPLATFORM
ARG TARGETOS
ARG TARGETARCH

COPY --from=cache /fwatchdog-$TARGETARCH ./fwatchdog

LABEL org.label-schema.license="MIT" \
      org.label-schema.vcs-url="https://github.com/openfaas/of-watchdog" \
      org.label-schema.vcs-type="Git" \
      org.label-schema.name="openfaas/of-watchdog" \
      org.label-schema.vendor="openfaas" \
      org.label-schema.docker.schema-version="1.0"

ENTRYPOINT ["/fwatchdog"]
