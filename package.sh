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
    zi = zipfile.ZipInfo("omc-qbs-relay.so", (2020, 1, 1, 0, 0, 0))
    zi.external_attr = 0o100755 << 16
    zi.compress_type = zipfile.ZIP_DEFLATED
    f.writestr(zi, (b/"omc-qbs-relay.so").read_bytes())
lines=[]
for p in (z, b/"omc-qbs-relay.omc.json"):
    lines.append(hashlib.sha256(p.read_bytes()).hexdigest()+"  "+p.name+"\n")
(b/"checksums.txt").write_text("".join(lines))
PYZIP
echo "packaged: build/omc-qbs-relay_${VERSION}_linux_amd64.zip"
