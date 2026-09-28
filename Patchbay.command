#!/bin/sh
# Open the packaged local demo. Closing its terminal stops only its owned daemon.
cd -- "$(dirname -- "$0")" || exit 1
exec ./bin/deckctl demo
