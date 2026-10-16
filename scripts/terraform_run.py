"""Запуск Terraform без вывода учётных данных и сырых ответов API."""

import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
TF = ROOT / "terraform"


def run(args, timeout=180):
    result = subprocess.run(["terraform", "-chdir=" + str(TF), *args], capture_output=True,
                            text=True, timeout=timeout)
    if result.returncode:
        message = (result.stdout + result.stderr).lower()
        labels = [("permissiondenied", "Недостаточно прав"), ("permission denied", "Недостаточно прав"),
                  ("deadline", "Сервис не ответил вовремя"), ("unavailable", "Сервис недоступен"),
                  ("unauthenticated", "Ошибка авторизации"), ("unsupported", "Несовместимая схема провайдера"),
                  ("invalid", "Некорректная конфигурация или параметры"), ("no space", "Недостаточно места")]
        reason = next((v for k, v in labels if k in message), "Операция не выполнена")
        raise RuntimeError(reason)
    return result.stdout


def main():
    os.umask(0o077)
    command = sys.argv[1] if len(sys.argv) == 2 else ""
    if command not in {"init", "validate", "plan", "apply"}:
        raise RuntimeError("Используйте init, validate, plan или apply")
    if command == "init":
        run(["init", "-input=false", "-no-color"])
        print("Terraform инициализирован")
    elif command == "validate":
        data = json.loads(run(["validate", "-json"]))
        if not data["valid"]:
            raise RuntimeError("Конфигурация не прошла проверку")
        print("Конфигурация корректна")
    elif command == "plan":
        target = TF / ".local" / "deploy.tfplan"
        run(["plan", "-input=false", "-no-color", "-out=" + str(target)])
        data = json.loads(run(["show", "-json", str(target)]))
        for change in data.get("resource_changes", []):
            actions = change["change"]["actions"]
            if actions != ["no-op"]:
                print(change["address"], ":", ", ".join(actions))
        print("План подготовлен; значения учётных данных скрыты")
    else:
        target = TF / ".local" / "deploy.tfplan"
        if not target.is_file():
            raise RuntimeError("Сначала подготовьте plan")
        run(["apply", "-input=false", "-no-color", str(target)], timeout=300)
        print("Ресурсы применены")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, subprocess.TimeoutExpired, OSError, ValueError) as error:
        print(str(error) if isinstance(error, RuntimeError) else "Операция Terraform не завершена", file=sys.stderr)
        sys.exit(1)
