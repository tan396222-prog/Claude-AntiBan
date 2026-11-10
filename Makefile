# agent-vm 0.3, the Go binary, with Lima built in from third_party/.
#
#   make              _output/bin/agent-vm, with the guest agent and templates
#   make install      build it, then link it onto the PATH (AGENT_VM_BIN_DIR
#                     to choose where; ~/.local/bin by default)
#   make limactl      Lima's own limactl from third_party/, for debugging
#   make test         the Go tests
#   make check-windows      the Windows build and tests compile and vet, from any OS
#   make test-third-party   the tests of the modules in third_party/, patched

GO ?= go
GOOS ?= $(shell $(GO) env GOOS)
GOARCH ?= $(shell $(GO) env GOARCH)
# Cage's VERSION file owns the default. Upstream release tooling can still
# supply VERSION explicitly when building a historical agent-vm tag.
VERSION ?= $(shell tr -d '\r\n' < VERSION)
LIMA_VERSION := $(shell cat internal/limaembed/lima-version)

ifeq ($(GOARCH),amd64)
GUEST_ARCH := x86_64
else ifeq ($(GOARCH),arm64)
GUEST_ARCH := aarch64
else
GUEST_ARCH := $(GOARCH)
endif
ifeq ($(GOOS),windows)
EXE := .exe
endif

ASSETS := internal/limaembed/assets
LDFLAGS := -s -w \
	-X github.com/sylvinus/agent-vm/internal/version.Version=$(VERSION) \
	-X github.com/lima-vm/lima/v2/pkg/version.Version=$(LIMA_VERSION)
# yq only edits Lima's YAML here: without its other formats (Lua, HCL, TOML,
# XML...), 10% smaller. Tests run with the same tags.
TAGS := yq_noxml,yq_nolua,yq_noini,yq_nohcl,yq_notoml,yq_noprops,yq_nocsv,yq_nouri,yq_nobase64,yq_nosh,yq_noshell
# cgo on macOS only, where vz and Lima's file watcher need it. On Linux the
# hostagent's DNS server uses Go's resolver (Lima builds it with the
# system's), and the binary cross-compiles with no C compiler.
ifeq ($(GOOS),darwin)
CGO_ENABLED ?= 1
else
CGO_ENABLED ?= 0
endif
GO_BUILD := CGO_ENABLED=$(CGO_ENABLED) $(GO) build -trimpath -tags $(TAGS) -ldflags "$(LDFLAGS)"

.PHONY: all agent-vm install limactl assets test test-go check-windows test-third-party fuzz clean

all: agent-vm

# Claude Cage ships a separate entry point with a mandatory proxy profile.
cage: cage-ui assets tailnet-assets
	$(GO_BUILD) -o _output/bin/cage$(EXE) ./cmd/cage
ifeq ($(GOOS),darwin)
	codesign -f --entitlements third_party/lima/vz.entitlements -s - _output/bin/cage
endif

cage-ui:
	npm --prefix ui ci --no-audit --no-fund
	npm run --prefix ui build

.PHONY: cage cage-ui cage-windows package-windows cage-linux package-linux check-linux
cage-linux:
	bash scripts/build-linux.sh

package-linux:
	bash scripts/package-linux.sh

check-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) vet -tags $(TAGS) . ./cmd/... ./internal/...

cage-windows:
	pwsh -NoProfile -File scripts/build-windows.ps1

package-windows:
	pwsh -NoProfile -File scripts/package-windows.ps1

agent-vm: assets
	$(GO_BUILD) -o _output/bin/agent-vm$(EXE) ./cmd/agent-vm
ifeq ($(GOOS),darwin)
	codesign -f -v --entitlements third_party/lima/vz.entitlements -s - _output/bin/agent-vm
endif

# For developers: the binary install puts on the PATH, with its own
# wording, replacement of earlier installs and next steps.
install: agent-vm
	./_output/bin/agent-vm$(EXE) install

limactl:
	$(GO_BUILD) -o _output/bin/limactl$(EXE) github.com/lima-vm/lima/v2/cmd/limactl
	mkdir -p _output/share/lima
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) $(GO) build -trimpath -ldflags "-s -w" \
		-o _output/share/lima/lima-guestagent.Linux-$(GUEST_ARCH) github.com/lima-vm/lima/v2/cmd/lima-guestagent
	rm -rf _output/share/lima/templates && cp -R third_party/lima/templates _output/share/lima/templates
ifeq ($(GOOS),darwin)
	codesign -f -v --entitlements third_party/lima/vz.entitlements -s - _output/bin/limactl
endif

# Build each guest helper for the OS in which it will run.
assets:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) $(GO) build -trimpath \
		-ldflags "-s -w -X github.com/lima-vm/lima/v2/pkg/version.Version=$(LIMA_VERSION)" \
		-o $(ASSETS)/lima-guestagent.Linux-$(GUEST_ARCH) github.com/lima-vm/lima/v2/cmd/lima-guestagent
	gzip -9 -n -f $(ASSETS)/lima-guestagent.Linux-$(GUEST_ARCH)
ifeq ($(GOOS)/$(GOARCH),darwin/arm64)
	mkdir -p $(ASSETS)/Darwin
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -tags $(TAGS) \
		-ldflags "-s -w -X github.com/lima-vm/lima/v2/pkg/version.Version=$(LIMA_VERSION)" \
		-o $(ASSETS)/Darwin/lima-guestagent.Darwin-aarch64 github.com/lima-vm/lima/v2/cmd/lima-guestagent
	gzip -9 -n -f $(ASSETS)/Darwin/lima-guestagent.Darwin-aarch64
endif
	rm -rf $(ASSETS)/templates && cp -R third_party/lima/templates $(ASSETS)/templates

test: test-go

test-go:
	$(GO) test -tags $(TAGS) . ./cmd/... ./internal/...
	cd tools/tailnet && GOFLAGS= $(GO) test ./...

# The Windows build and its tests compile and vet, from any OS. The Windows
# paths' logic is tested everywhere (paths.Windows); the tests themselves
# run on Windows in CI (windows-latest).
check-windows:
	GOOS=windows GOARCH=amd64 $(GO) vet -tags $(TAGS) . ./cmd/... ./internal/...
	GOOS=windows GOARCH=arm64 $(GO) vet -tags $(TAGS) . ./cmd/... ./internal/...

# From this module, so that each library is built with the others patched.
# Lima's MCP packages and sshocker's command are not part of agent-vm (-e:
# listing them needs sums nothing built here uses; grep keeps them out of
# the tests).
test-third-party:
	$(GO) test -vet=off -race github.com/pkg/sftp/...
	$(GO) test -race github.com/lima-vm/sshocker/pkg/...
	$(GO) test $$($(GO) list -e github.com/lima-vm/lima/v2/pkg/... | grep -v /mcp)
	$(GO) test github.com/containers/gvisor-tap-vsock/pkg/...

# Each fuzzer for FUZZTIME (their seeds run with the tests). The vendored
# sshocker's through tests/fuzz/go.work, which makes it fuzzable.
FUZZTIME ?= 1m
FUZZ = github.com/lima-vm/sshocker/pkg/reversesshfs github.com/pkg/sftp ./internal/env ./internal/gitguard \
	./internal/mounts ./internal/paths ./internal/runscript ./internal/ui ./internal/vmname
fuzz:
	@set -e; for pkg in $(FUZZ); do \
	  case $$pkg in ./*) work=off ;; *) work=$(CURDIR)/tests/fuzz/go.work ;; esac; \
	  fs=$$(GOWORK=$$work $(GO) test -vet=off -list '^Fuzz' $$pkg) || { echo "$$fs"; exit 1; }; \
	  for f in $$(echo "$$fs" | grep '^Fuzz'); do \
	    echo "== $$pkg $$f"; \
	    GOWORK=$$work $(GO) test -vet=off -run '^$$' -fuzz "^$$f$$" -fuzztime $(FUZZTIME) $$pkg; \
	  done; \
	done

clean:
	rm -rf _output
	find $(ASSETS) -mindepth 1 ! -name README -exec rm -rf {} +

.PHONY: tailnet-assets
tailnet-assets:
	bash scripts/build-tailnet.sh linux amd64 "$(CURDIR)/_output/tailnet/cage-tailnet-linux-amd64"
	bash scripts/build-tailnet.sh linux arm64 "$(CURDIR)/_output/tailnet/cage-tailnet-linux-arm64"
	gzip -n -c _output/tailnet/cage-tailnet-linux-amd64 > internal/tailnetassets/assets/cage-tailnet-linux-amd64.gz
	gzip -n -c _output/tailnet/cage-tailnet-linux-arm64 > internal/tailnetassets/assets/cage-tailnet-linux-arm64.gz
