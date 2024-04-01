#!/bin/bash
cd `dirname $0`
cd ../

export GOROOT=/usr/local/go
export PATH=${PATH}:${GOROOT}/bin

timestamp=$(date '+%Y%m%d%H%M%S')

go build -ldflags "-X main.Version=v0.1.0 -X main.Build=$timestamp" -o ./bin/
