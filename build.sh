#!/bin/sh

export arch=$(uname -m)

if [ ! "$arch" = "x86_64" ] ; then
    echo "Build not supported on $arch, use cross-build."
    exit 1
fi

if [ ! "$http_proxy" = "" ]
then
    docker build --no-cache --build-arg "https_proxy=$https_proxy" --build-arg "http_proxy=$http_proxy" -t functions/of-watchdog:build .
else
    docker build -t functions/of-watchdog:build .
fi

docker create --name buildoutput functions/of-watchdog:build echo

docker cp buildoutput:/go/src/github.com/openfaas-incubator/of-watchdog/of-watchdog ./of-watchdog
docker cp buildoutput:/go/src/github.com/openfaas-incubator/of-watchdog/of-watchdog-darwin ./of-watchdog-darwin
docker cp buildoutput:/go/src/github.com/openfaas-incubator/of-watchdog/of-watchdog-armhf ./of-watchdog-armhf
docker cp buildoutput:/go/src/github.com/openfaas-incubator/of-watchdog/of-watchdog-arm64 ./of-watchdog-arm64
docker cp buildoutput:/go/src/github.com/openfaas-incubator/of-watchdog/of-watchdog.exe ./of-watchdog.exe

docker rm buildoutput
