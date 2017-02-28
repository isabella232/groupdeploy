TOP := $(shell dirname $(realpath $(lastword $(MAKEFILE_LIST))))

GOPATH=$(shell pwd)/.gopath
GOFMT=gofmt -w

.PHONY: gopath build fmt get

default: all

all: get build

get:
	mkdir -p .gopath/src/github.com/liquidm
	ln -nfs ../../../../ .gopath/src/github.com/liquidm/groupdeploy
	go get golang.org/x/net/context
	go get golang.org/x/oauth2/google
	go get google.golang.org/api/compute/v1

build:
	go build
