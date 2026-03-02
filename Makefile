PKG = github.com/figstra/usewebhook-cli
VERSION ?= dev
LDFLAGS = -ldflags "-X 'main.Version=$(VERSION)'"

build:
	go build $(LDFLAGS) -o dist/usewebhook .

run: build
	./dist/usewebhook

deps:
	go get ./...

install:
	go install $(LDFLAGS)

test: deps
	go test ./... -timeout 60s $(LDFLAGS) -v

cover: deps
	go test ./... -race -v -timeout 15s -coverprofile=coverage.out
	go tool cover -html=coverage.out

fmt:
	go fmt ./...

bench: deps
	go test $(LDFLAGS) -benchmem -bench=. -benchtime=5s ./...

race: deps
	go test ./... -v -race -timeout 15s

release: release-linux release-darwin

ensure-dist: deps
	mkdir -p dist

release-linux: ensure-dist
	GOOS=linux GOARCH=amd64 go build -o dist/usewebhook $(LDFLAGS) && cd dist && tar -czf usewebhook_linux_amd64.tar.gz usewebhook && rm usewebhook
	GOOS=linux GOARCH=arm64 go build -o dist/usewebhook $(LDFLAGS) && cd dist && tar -czf usewebhook_linux_arm64.tar.gz usewebhook && rm usewebhook

release-darwin: ensure-dist
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/usewebhook && cd dist && tar -czf usewebhook_darwin_amd64.tar.gz usewebhook && rm usewebhook
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/usewebhook && cd dist && tar -czf usewebhook_darwin_arm64.tar.gz usewebhook && rm usewebhook