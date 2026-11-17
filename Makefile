# ternssh desktop, built with MyGo.
#
#   make dev            # run the app with live reload, on this machine's OS
#   make dev-mac        # the same, on macOS only
#   make dev-windows    # the same, on Windows only (GNU make, e.g. from Git Bash)
#   make dev ARGS="-lang en-XA"   # flags of `go tool mygo dev`
#   make windows        # amd64 + arm64: exe and installer in build/windows-*/
#   make windows-x64    # amd64 only
#   make windows-arm64  # arm64 only
#   make mac            # .app + .dmg for this Mac, with its background
#   make dmg-background # render resources/dmg-background.svg (needs rsvg-convert)
#   make test           # tests, and the Windows build check
#
# Windows builds are cross-compiled from any OS (no cgo); the installers
# need NSIS (`brew install makensis` on macOS). They are unsigned.
#
# mygo dev runs the app on the machine it runs on: it cannot cross-compile
# as mygo build does, so `make dev-windows` is for a Windows machine.

export GOTOOLCHAIN := auto

MYGO := go tool mygo

# HOST is the OS this make runs on: mac, windows or linux.
ifeq ($(OS),Windows_NT)
HOST := windows
else ifeq ($(shell uname -s),Darwin)
HOST := mac
else
HOST := linux
endif

.PHONY: dev dev-mac dev-windows dev-linux windows windows-x64 windows-arm64 mac dmg-background test vet clean

dev: dev-$(HOST)

dev-mac:
	$(if $(filter mac,$(HOST)),,$(error dev-mac runs on macOS; this is $(HOST): use make dev))
	$(MYGO) dev $(ARGS)

dev-windows:
	$(if $(filter windows,$(HOST)),,$(error dev-windows runs on Windows; this is $(HOST): use make dev, or make windows to build for Windows))
	$(MYGO) dev $(ARGS)

dev-linux:
	$(MYGO) dev $(ARGS)

windows:
	$(MYGO) build -platform windows/amd64,windows/arm64

windows-x64:
	$(MYGO) build -platform windows/amd64

windows-arm64:
	$(MYGO) build -platform windows/arm64

mac:
	$(MYGO) build
	scripts/dmg.sh build/darwin-$(shell go env GOARCH)

# The disk image's background, at 1x and 2x: see scripts/dmg.sh.
dmg-background:
	rsvg-convert resources/dmg-background.svg -o resources/dmg-background.png
	rsvg-convert -z 2 resources/dmg-background.svg -o resources/dmg-background@2x.png

vet:
	go vet ./...
	GOOS=windows go vet ./...

test: vet
	go test ./...

clean:
	rm -rf build
