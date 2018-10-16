.PHONY: dependencies test integration checks

all: dependencies checks test build docker

dependencies:
	dep ensure -v --vendor-only

test:
	go test -short ./...

integration:
	bash -f ./scripts/clustertestsetup.sh
	go test -timeout 5m ./...

build:
	go build .

checks:
	gometalinter --vendor --disable-all --enable=errcheck --enable=vet --enable=gofmt --enable=golint --enable=deadcode --enable=varcheck --enable=structcheck --enable=misspell --deadline=15m ./...

docker:
	docker build -t lawrencegripper/azurefrontdoor-ingress .