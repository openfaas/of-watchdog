#!/bin/sh

# ":" separated list of paths to folders that contain
# executable binaries that should be shasum'd
BINARIES=./bin

IFS=':'
for directory in $BINARIES; do
    cd "$directory" || exit 1
    for f in * ; do # Scanning files in direcotry
        if [ -x $f ]; then
            shasum -a 256 $f > $f.sha256
        fi
    done
done
