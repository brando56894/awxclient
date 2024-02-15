#!/bin/bash
CC=musl-gcc CXX=x86_64-linux-musl-g++ GOARCH=amd64 GOOS=linux CGO_ENABLED=1 go build -ldflags "-linkmode external -extldflags -static" -gcflags="all=-N -l" -o "awxclient.debug"

