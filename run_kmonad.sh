#!/bin/sh

KMONAD_CONFIG_FILE="$1"

if ! [ -e "$KMONAD_CONFIG_FILE" ]; then
    echo "Config file must exist!" >&2
    exit 1
fi

FIFO=/run/kmonad-keylogger.sock

kmonad --log-level debug "$KMONAD_CONFIG_FILE" |
    rg --line-buffered '^Received event: Press <(.+)>$' -r '$1' --only-matching >>"$FIFO"
