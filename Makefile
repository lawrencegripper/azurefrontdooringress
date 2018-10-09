.PHONY: dependencies test integration checks

all: dependencies checks test build

dependencies:
	dep ensure -v --vendor-only

test:
	go test -short ./...

integration:
	go test ./...

build:
	go build .

checks:
	gometalinter --vendor --disable-all --enable=errcheck --enable=vet --enable=gofmt --enable=golint --enable=deadcode --enable=varcheck --enable=structcheck --deadline=15m ./...

