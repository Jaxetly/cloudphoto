#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
instance=${1:-claudlab}
multipass exec "$instance" -- mkdir -p /home/ubuntu/cloudphoto-test
multipass transfer dist/tests/*.test dist/linux-amd64/cloudphoto "$instance":/home/ubuntu/cloudphoto-test/
multipass exec "$instance" -- bash --noprofile --norc -c '
    set -euo pipefail
    cd /home/ubuntu/cloudphoto-test
    for binary in ./*.test; do
        qemu-x86_64 "$binary" -test.v
    done
    python3 - <<"PY"
import os, subprocess
binary = ["qemu-x86_64", "/home/ubuntu/cloudphoto-test/cloudphoto"]
clean = {"PATH": "/usr/bin:/bin", "HOME": "/home/ubuntu"}
r = subprocess.run(binary + ["list"], env=clean, capture_output=True, timeout=20)
assert r.returncode == 1 and not r.stdout
assert r.stderr.decode() == "Не заданы переменные окружения\n"
clean.update(BUCKET="test.example", AWS_ACCESS_KEY_ID="fake-access", AWS_SECRET_ACCESS_KEY="fake-secret")
r = subprocess.run(binary + ["upload"], env=clean, capture_output=True, timeout=20)
assert r.returncode == 1 and not r.stdout
assert r.stderr.decode() == "Ошибка в аргументах\n"
print("Проверки исполняемого файла Linux x86_64 пройдены")
PY
'
