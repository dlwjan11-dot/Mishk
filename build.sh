#!/bin/sh
# Builds dist/Mishk.exe. Needs Go 1.22+, Python 3 and mingw windres (x86_64-w64-mingw32-windres).
set -e
cd "$(dirname "$0")"
python3 build_ui.py
(cd res && x86_64-w64-mingw32-windres app.rc -O coff -o ../rsrc_windows_amd64.syso)
go test -count=1 .
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui" -o dist/Mishk.exe .
ls -la dist
