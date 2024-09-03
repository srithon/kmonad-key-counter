BINDIR=/usr/local/bin

all: main

main: main.go
	go build .

install: main
	sudo cp -a ./kmonad-keylogger ${BINDIR}/kmonad-keylogger
	sudo cp -a ./kmonad-keylogger.service /etc/systemd/system
