#!/usr/bin/env bash
set -euo pipefail
VERSION=${VERSION:-0.1.0}
mkdir -p build
(cd go && CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -ldflags "-s -w -X main.pluginVersion=${VERSION}" -o ../build/omc-qbs-relay.so .)
python3 smoke.py build/omc-qbs-relay.so "${VERSION}"
cp go/assets/omc.json build/omc-qbs-relay.omc.json
python3 - <<'PYZIP'
import zipfile, hashlib, pathlib
import os
v = os.environ.get("VERSION", "0.1.0")
b = pathlib.Path("build")
z = b / f"omc-qbs-relay_{v}_linux_amd64.zip"
with zipfile.ZipFile(z, "w", zipfile.ZIP_DEFLATED) as f:
    f.write(b/"omc-qbs-relay.so", "omc-qbs-relay.so")
    f.write(b/"omc-qbs-relay.omc.json", "omc-qbs-relay.omc.json")
lines=[]
for p in (z, b/"omc-qbs-relay.omc.json"):
    lines.append(hashlib.sha256(p.read_bytes()).hexdigest()+"  "+p.name+"\n")
(b/"checksums.txt").write_text("".join(lines))
PYZIP
echo "packaged: build/omc-qbs-relay_${VERSION}_linux_amd64.zip"
