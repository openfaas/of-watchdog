#!/bin/bash

export USR=$DOCKER_NS
export TAG=$TRAVIS_TAG

docker manifest create $USR/of-watchdog:$TAG \
  $USR/of-watchdog:$TAG-darwin \
  $USR/of-watchdog:$TAG-x86_64 \
  $USR/of-watchdog:$TAG-armhf \
  $USR/of-watchdog:$TAG-arm64 \
  $USR/of-watchdog:$TAG-windows

docker manifest annotate $USR/of-watchdog:$TAG --arch arm $USR/of-watchdog:$TAG-darwin
docker manifest annotate $USR/of-watchdog:$TAG --arch arm $USR/of-watchdog:$TAG-armhf
docker manifest annotate $USR/of-watchdog:$TAG --arch arm64 $USR/of-watchdog:$TAG-arm64
docker manifest annotate $USR/of-watchdog:$TAG --os windows $USR/of-watchdog:$TAG-windows

docker manifest push $USR/of-watchdog:$TAG
