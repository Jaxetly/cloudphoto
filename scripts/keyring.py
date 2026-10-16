"""Локальный PGP-ключ; пароль хранится в связке ключей macOS."""

import base64
import ctypes
import hashlib
import os
from pathlib import Path
import secrets
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
KEYRING = ROOT.parent / ".private" / "cloudphoto" / "gnupg"


def passphrase():
    security = ctypes.CDLL("/System/Library/Frameworks/Security.framework/Security")
    service = b"cloudphoto-pgp"
    account = hashlib.sha256(str(KEYRING).encode()).hexdigest().encode()
    find = security.SecKeychainFindGenericPassword
    find.argtypes = [ctypes.c_void_p, ctypes.c_uint32, ctypes.c_char_p,
                     ctypes.c_uint32, ctypes.c_char_p, ctypes.POINTER(ctypes.c_uint32),
                     ctypes.POINTER(ctypes.c_void_p), ctypes.c_void_p]
    find.restype = ctypes.c_int32
    size, data = ctypes.c_uint32(), ctypes.c_void_p()
    result = find(None, len(service), service, len(account), account,
                  ctypes.byref(size), ctypes.byref(data), None)
    if result == 0:
        try:
            return ctypes.string_at(data, size.value)
        finally:
            free = security.SecKeychainItemFreeContent
            free.argtypes = [ctypes.c_void_p, ctypes.c_void_p]
            free(None, data)
    if result != -25300:
        raise RuntimeError("Связка ключей macOS недоступна")
    value = secrets.token_urlsafe(48).encode()
    add = security.SecKeychainAddGenericPassword
    add.argtypes = [ctypes.c_void_p, ctypes.c_uint32, ctypes.c_char_p,
                    ctypes.c_uint32, ctypes.c_char_p, ctypes.c_uint32,
                    ctypes.c_void_p, ctypes.c_void_p]
    add.restype = ctypes.c_int32
    if add(None, len(service), service, len(account), account, len(value), value, None):
        raise RuntimeError("Не удалось сохранить пароль локального PGP-ключа")
    return value


def gpg(args, payload=None, password=None):
    command = ["gpg", "--homedir", str(KEYRING), "--batch", "--yes", "--rfc4880"]
    descriptor = None
    if password is not None:
        reader, writer = os.pipe()
        os.write(writer, password + b"\n")
        os.close(writer)
        descriptor = reader
        command += ["--pinentry-mode", "loopback", "--passphrase-fd", str(reader)]
    try:
        result = subprocess.run(command + args, input=payload, capture_output=True,
                                timeout=90, pass_fds=(() if descriptor is None else (descriptor,)))
    finally:
        if descriptor is not None:
            os.close(descriptor)
    if result.returncode:
        raise RuntimeError("Операция с локальным PGP-ключом не выполнена")
    return result.stdout


def prepare():
    os.umask(0o077)
    KEYRING.mkdir(parents=True, exist_ok=True, mode=0o700)
    KEYRING.chmod(0o700)
    password = passphrase()
    listing = gpg(["--with-colons", "--list-secret-keys"])
    if b"sec:" not in listing:
        gpg(["--quick-generate-key", "cloudphoto local encryption", "rsa2048", "encr", "1y"],
            password=password)
    listing = gpg(["--with-colons", "--list-secret-keys"]).decode()
    fingerprint = next(line.split(":")[9] for line in listing.splitlines() if line.startswith("fpr:"))
    sample = b"local-encryption-check"
    encrypted = gpg(["--trust-model", "always", "--recipient", fingerprint, "--encrypt"], sample)
    if gpg(["--decrypt"], encrypted, password) != sample:
        raise RuntimeError("Проверка шифрования не пройдена")
    target = ROOT / "terraform" / ".local" / "public-key.b64"
    target.parent.mkdir(mode=0o700, exist_ok=True)
    target.write_bytes(base64.b64encode(gpg(["--export", fingerprint])))
    target.chmod(0o600)
    return target


def decrypt(ciphertext):
    return gpg(["--decrypt"], base64.b64decode(ciphertext, validate=True), passphrase())


if __name__ == "__main__":
    try:
        prepare()
        print("Локальное шифрование подготовлено и проверено")
    except (RuntimeError, OSError, subprocess.TimeoutExpired, StopIteration):
        print("Не удалось подготовить локальное шифрование", file=sys.stderr)
        sys.exit(1)
