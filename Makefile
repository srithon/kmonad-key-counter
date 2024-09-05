BINDIR=/usr/local/bin

all: main

main: main.go
	go build .

install: main
	sudo cp -a ./kmonad-key-counter ${BINDIR}/kmonad-key-counter
	sudo cp -a ./kmonad-key-counter.service /etc/systemd/system
