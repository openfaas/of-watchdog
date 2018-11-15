#!/bin/sh

for f in of-watchdog*; do shasum -a 256 $f > $f.sha256; done