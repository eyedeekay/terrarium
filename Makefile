VERSION=1.14.0
CGO_ENABLED=0
export CGO_ENABLED=0

GOOS?=$(shell uname -s | tr A-Z a-z)
GOARCH?="amd64"

ARG=-v -tags netgo -ldflags '-w -extldflags "-static"'

BINARY=terrarium
SIGNER=hankhill19580@gmail.com
CONSOLEPOSTNAME=IRC
USER_GH=eyedeekay

build: dep
	go build $(ARG) -tags="netgo" -o $(BINARY)-$(GOOS)-$(GOARCH) ./cmd/$(BINARY)
	make su3

clean:
	rm -f $(BINARY)-plugin plugin $(BINARY)-*zip -r
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
#	cp "$(HOME)/build/shellservice.jar" conf/lib/shellservice.jar -v

SIGNER_DIR=$(HOME)/i2p-go-keys/

su3:
	i2p.plugin.native -name=$(BINARY)-$(GOOS)-$(GOARCH) \
		-signer=$(SIGNER) \
		-signer-dir=$(SIGNER_DIR) \
		-version "$(VERSION)" \
		-author=$(SIGNER) \
		-autostart=true \
		-clientname=$(BINARY)-$(GOOS)-$(GOARCH) \
		-consolename="$(BINARY) - $(CONSOLEPOSTNAME)" \
		-name="$(BINARY)-$(GOOS)-$(GOARCH)" \
		-delaystart="1" \
		-desc="`cat desc`" \
		-exename=$(BINARY)-$(GOOS)-$(GOARCH) \
		-icondata=icon/icon.png \
		-updateurl="http://idk.i2p/$(BINARY)/$(BINARY)-$(GOOS)-$(GOARCH).su3" \
		-website="http://idk.i2p/$(BINARY)/" \
		-command="$(BINARY)-$(GOOS)-$(GOARCH) -conf \"\$$PLUGIN/catbox-i2p.conf\"" \
		-license=AGPL \
		-res=conf/
	unzip -o $(BINARY)-$(GOOS)-$(GOARCH).zip -d $(BINARY)-$(GOOS)-$(GOARCH)-zip

sum:
	sha256sum $(BINARY)-$(GOOS)-$(GOARCH).su3

version:
	gothub release -u eyedeekay -r $(BINARY) -t "$(VERSION)" -d "`cat desc`"; true

upload:
	gothub upload -R -u eyedeekay -r $(BINARY) -t "$(VERSION)" -f $(BINARY)-$(GOOS)-$(GOARCH).su3 -n $(BINARY)-$(GOOS)-$(GOARCH).su3 -l "`sha256sum $(BINARY)-$(GOOS)-$(GOARCH).su3`"

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

download-su3s:

release: clean all version upload-all

index:
	@echo "<!DOCTYPE html>" > index.html
	@echo "<html>" >> index.html
	@echo "<head>" >> index.html
	@echo "  <title>$(BINARY) - $(CONSOLEPOSTNAME)</title>" >> index.html
	@echo "  <link rel=\"stylesheet\" type=\"text/css\" href =\"/style.css\" />" >> index.html
	@echo "</head>" >> index.html
	@echo "<body>" >> index.html
	pandoc README.md >> index.html
	@echo "</body>" >> index.html
	@echo "</html>" >> index.html
