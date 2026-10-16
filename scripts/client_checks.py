"""Самостоятельный запуск клиентской проверки из терминала Mac."""

import argparse
import getpass
import json
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]


def connection(from_state):
    if from_state:
        from keyring import decrypt
        state = json.loads((ROOT / "terraform" / "terraform.tfstate").read_text())
        resource = next(r for r in state["resources"] if r["type"] == "yandex_iam_service_account_static_access_key" and r["name"] == "client")
        attributes = resource["instances"][0]["attributes"]
        if attributes.get("secret_key"):
            raise RuntimeError("В state обнаружена незашифрованная секретная часть ключа")
        access_key = attributes["access_key"]
        secret_key = decrypt(attributes["encrypted_secret_key"]).decode().strip()
        bucket = state["outputs"]["site_url"]["value"].split("/")[2]
    else:
        bucket = input("BUCKET: ").strip()
        access_key = getpass.getpass("AWS_ACCESS_KEY_ID (скрыто): ")
        secret_key = getpass.getpass("AWS_SECRET_ACCESS_KEY (скрыто): ")
    return {"BUCKET": bucket, "AWS_ACCESS_KEY_ID": access_key, "AWS_SECRET_ACCESS_KEY": secret_key}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--instance", default="claudlab")
    parser.add_argument("--from-state", action="store_true", help="Расшифровать S3-ключ из локального state вместо скрытого ввода")
    args = parser.parse_args()
    payload = connection(args.from_state)
    commit = subprocess.run(["git", "-C", str(ROOT), "rev-parse", "HEAD"], capture_output=True, text=True, check=True).stdout.strip()
    payload.update(commit=commit)
    print("S3-ключи будут переданы через stdin только клиентскому процессу Ubuntu. IAM-токен не передаётся.", flush=True)
    result = subprocess.run(["multipass", "exec", args.instance, "--", "python3", "/home/ubuntu/cloudphoto-test/client_checks.py"],
                            input=json.dumps(payload) + "\n", text=True, timeout=1800)
    return result.returncode


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        print("Проверка остановлена", file=sys.stderr)
        sys.exit(1)
    except Exception:
        print("Не удалось запустить проверку; учётные данные не выводятся", file=sys.stderr)
        sys.exit(1)
