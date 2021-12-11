VERSION=0.0.02
CGO_ENABLED=0
export CGO_ENABLED=0

GOOS?=$(shell uname -s | tr A-Z a-z)
GOARCH?="amd64"

ARG=-v -tags netgo -ldflags '-w -extldflags "-static"'

BINARY=terrarium
SIGNER=hankhill19580@gmail.com
CONSOLEPOSTNAME=IRC

build: dep
	go build $(ARG) -tags="netgo" -o $(BINARY)-$(GOOS)-$(GOARCH) ./cmd/$(BINARY)
	make su3

clean:
	rm -f $(BINARY)-plugin plugin $(BINARY)-zip* -r
	rm -f *.su3 *.zip $(BINARY)-$(GOOS)-$(GOARCH) $(BINARY)-*

all: windows linux osx bsd

windows:
	GOOS=windows GOARCH=amd64 make build su3
	GOOS=windows GOARCH=386 make build su3

linux:
	GOOS=linux GOARCH=amd64 make build su3
	GOOS=linux GOARCH=arm64 make build su3
	GOOS=linux GOARCH=386 make build su3

osx:
	GOOS=darwin GOARCH=amd64 make build su3
	GOOS=darwin GOARCH=arm64 make build su3

bsd:
	GOOS=freebsd GOARCH=amd64 make build su3
	GOOS=openbsd GOARCH=amd64 make build su3

dep:
	cp "$(HOME)/Workspace/GIT_WORK/i2p.i2p/build/shellservice.jar" conf/lib/shellservice.jar -v

su3:
	i2p.plugin.native -name=$(BINARY)-$(GOOS)-$(GOARCH) \
		-signer=$(SIGNER) \
		-version "$(VERSION)" \
		-author=$(SIGNER) \
		-autostart=true \
		-clientname=$(BINARY)-$(GOOS)-$(GOARCH) \
		-consolename="$(BINARY) - $(CONSOLEPOSTNAME)" \
		-consoleurl="http://127.0.0.1:8084" \
		-name="$(BINARY)-$(GOOS)-$(GOARCH)" \
		-delaystart="1" \
		-desc="`cat desc`" \
		-exename=$(BINARY)-$(GOOS)-$(GOARCH) \
		-icondata=icon/icon.png \
		-command="$(BINARY)-$(GOOS)-$(GOARCH) -conf \"\$$PLUGIN/catbox-i2p.conf\"" \
		-license=MIT \
		-res=conf/
	unzip -o $(BINARY)-$(GOOS)-$(GOARCH).zip -d $(BINARY)-$(GOOS)-$(GOARCH)-zip

sum:
	sha256sum $(BINARY)-$(GOOS)-$(GOARCH).su3

version:
	@echo gothub release -u eyedeekay -r terrarium -t "$(VERSION)" -d "`cat desc`"; true

upload:
	@echo gothub upload -u eyedeekay -r terrarium -t "$(VERSION)" -f $(BINARY)-$(GOOS)-$(GOARCH).su3 -n $(BINARY)-$(GOOS)-$(GOARCH).su3 -l "`sha256sum $(BINARY)-$(GOOS)-$(GOARCH).su3`"

upload-windows:
	GOOS=windows GOARCH=amd64 make upload
	GOOS=windows GOARCH=386 make upload

upload-linux:
	GOOS=linux GOARCH=amd64 make upload
	GOOS=linux GOARCH=arm64 make upload
	GOOS=linux GOARCH=386 make upload

upload-osx:
	GOOS=darwin GOARCH=amd64 make upload
	GOOS=darwin GOARCH=arm64 make upload

upload-bsd:
	GOOS=freebsd GOARCH=amd64 make upload
	GOOS=openbsd GOARCH=amd64 make upload

upload-all: upload-windows upload-linux upload-osx upload-bsd

release: version upload-all
