VERSION=0.0.01

linux:
	go build -tags="netgo" -o terrarium ./cmd/terrarium

windows:
	GOOS=windows go build -tags="netgo" -o terrarium.exe ./cmd/terrarium

plugin-linux: linux
	i2p.plugin.native -name=terrarium \
		-signer=hankhill19580@gmail.com \
		-version "$(VERSION)" \
		-author=hankhill19580@gmail.com \
		-autostart=true \
		-clientname=terrarium \
		-consolename="terrarium IRC" \
		-consoleurl="http://127.0.0.1:8084" \
		-name="terrariumIRC" \
		-delaystart="1" \
		-desc="`cat desc`" \
		-exename=terrarium \
		-icondata=icon/icon.png \
		-command="terrarium -conf \"\$$PLUGIN/lib/catbox-i2p.conf\"" \
		-license=MIT \
		-res=conf
	cp -v terrariumIRC.su3 ../terrarium-linux.su3
	unzip -o terrariumIRC.zip -d terrarium-zip

plugin-windows: windows
	i2p.plugin.native -name=terrarium \
		-signer=hankhill19580@gmail.com \
		-version "$(VERSION)" \
		-author=hankhill19580@gmail.com \
		-autostart=true \
		-clientname=terrarium.exe \
		-consolename="terrarium IRC" \
		-consoleurl="http://127.0.0.1:8084" \
		-name="terrariumIRC" \
		-delaystart="1" \
		-desc="`cat desc`" \
		-exename=terrarium.exe \
		-icondata=icon/icon.png \
		-command="terrarium -conf \"\$$PLUGIN/lib/catbox-i2p.conf\"" \
		-license=MIT \
		-targetos="windows" \
		-res=conf
	cp -v terrariumIRC.su3 ../terrarium-windows.su3
	unzip -o terrariumIRC.zip -d terrarium-zip-win