#!/bin/sh
NAME=redist
IMAGE=$1
eTAG=${2:-latest}

docker create --name "$NAME" "${IMAGE}:${eTAG}" \
    && mkdir -p ./bin \
    && docker cp "$NAME":/bin .
docker rm -f "$NAME"
