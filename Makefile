# FleetWarden build. `make` builds everything into dist/.
VERSION  ?= $(shell cat VERSION)
CHANNEL  ?= test
COMMIT   ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG      := github.com/rhisijmuiden/fleetwarden/internal/version
LDFLAGS  := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Channel=$(CHANNEL) -X $(PKG).BuildDate=$(DATE)
GOBUILD  := CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)"

.PHONY: all test web server agents deb clean

all: web server agents

test:
	go vet ./...
	go test -race ./...
	cd web && npm run test:i18n && npm run typecheck

web:
	cd web && npm ci --no-audit --no-fund && npm run build

server:
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o dist/linux-amd64/fleetwarden-server ./cmd/fleetwarden-server

agents:
	GOOS=linux   GOARCH=amd64 $(GOBUILD) -o dist/linux-amd64/fleetwarden-agent ./cmd/fleetwarden-agent
	GOOS=linux   GOARCH=arm64 $(GOBUILD) -o dist/linux-arm64/fleetwarden-agent ./cmd/fleetwarden-agent
	GOOS=windows GOARCH=amd64 $(GOBUILD) -o dist/windows-amd64/fleetwarden-agent.exe ./cmd/fleetwarden-agent

deb: web server
	VERSION=$(VERSION) nfpm package -f packaging/nfpm-server.yaml -p deb -t dist/

clean:
	rm -rf dist web/dist
