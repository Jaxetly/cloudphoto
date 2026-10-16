"""Сквозная проверка готового Linux-приложения. Запускается в Ubuntu."""

import hashlib
from html.parser import HTMLParser
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


class Images(HTMLParser):
    def __init__(self):
        super().__init__()
        self.sources = []

    def handle_starttag(self, tag, attrs):
        if tag == "img":
            self.sources.append(dict(attrs).get("src", ""))


def main():
    settings = json.loads(sys.stdin.readline())
    root = Path("/home/ubuntu/cloudphoto-test")
    environment = {"PATH": "/usr/bin:/bin", "HOME": "/home/ubuntu", "LANG": "C.UTF-8"}
    for key in ("BUCKET", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"):
        if not settings.get(key):
            raise RuntimeError("Не заданы параметры подключения")
        environment[key] = settings[key]
    report = {"commit": settings.get("commit", ""), "passed": [], "success": False}
    base = "https://" + settings["BUCKET"] + "/"
    command = ["qemu-x86_64", str(root / "cloudphoto")]
    albums = ["Проверка-" + uuid.uuid4().hex[:12] + " & <лето>", "Пустой-" + uuid.uuid4().hex[:12]]

    def call(*args, success=True):
        result = subprocess.run(command + list(args), env=environment, capture_output=True, timeout=150)
        if result.returncode != (0 if success else 1):
            safe = {"Нет доступа к облачному хранилищу", "Ошибка обращения к облачному хранилищу",
                    "Ошибка контрольной суммы S3-запроса", "Некорректный формат S3-запроса",
                    "Хранилище не ответило вовремя", "Бакет не найден"}
            safe |= {prefix + ": " + reason for prefix in ("Не удалось настроить публичное чтение", "Не удалось настроить хостинг") for reason in list(safe)}
            detail = result.stderr.decode(errors="replace").strip()
            raise RuntimeError("Ошибка команды " + args[0] + ((": " + detail) if detail in safe else ""))
        if success and result.stderr:
            raise RuntimeError("Неожиданный stderr команды " + args[0])
        if not success and result.stdout:
            raise RuntimeError("Ошибка попала в stdout")
        return result.stdout.decode()

    def passed(name):
        report["passed"].append(name)
        print("PASS:", name, flush=True)

    def fetch(url, data=None, method=None):
        try:
            with urllib.request.urlopen(urllib.request.Request(url, data=data, method=method), timeout=20) as response:
                return response.status, response.read()
        except urllib.error.HTTPError as error:
            return error.code, error.read()

    def page(url, expected):
        for attempt in range(12):
            try:
                status, body = fetch(url)
                if status == 200 and expected in body.decode():
                    return body.decode()
            except (OSError, UnicodeError):
                pass
            time.sleep(2)
        raise RuntimeError("HTTPS-страница не прошла проверку")

    try:
        original = call("list").splitlines()
        if not original:
            call("mksite")
            page(base, "Фотоархив пустой.")
            passed("Пустой архив и публикация пустой главной страницы")
        source = root / "fixture.jpg"
        seed = source.read_bytes()
        with tempfile.TemporaryDirectory(prefix="cloudphoto-check-") as directory:
            work = Path(directory)
            upload, download, single = work / "upload", work / "download", work / "single"
            for path in (upload, download, single):
                path.mkdir()
            names = ["A.JPG", "a.jpg", 'Фото + % # ? & "<лето>".JpEg']
            for name in names:
                (upload / name).write_bytes(seed)
            (upload / "ignore.png").write_bytes(b"ignored")
            (upload / "nested").mkdir()
            (upload / "nested" / "hidden.jpg").write_bytes(seed)
            (single / "one.jpg").write_bytes(seed)
            call("upload", "--album", albums[0], "--path", str(upload))
            actual = call("list", "--album", albums[0]).splitlines()
            if actual != sorted(names, key=lambda name: name.encode()):
                raise RuntimeError("Нарушены имена или сортировка фотографий")
            passed("Загрузка, кириллица, регистр, специальные символы, отсутствие рекурсии")
            (upload / "A.JPG").write_bytes(seed + b"replacement")
            call("upload", "--album", albums[0], "--path", str(upload))
            (download / "A.JPG").write_bytes(b"old contents")
            call("download", "--album", albums[0], "--path", str(download))
            for name in names:
                if hashlib.sha256((download / name).read_bytes()).digest() != hashlib.sha256((upload / name).read_bytes()).digest():
                    raise RuntimeError("Содержимое скачанного файла не совпало")
            passed("Повторная загрузка, скачивание и замена с проверкой SHA-256")
            call("upload", "--album", albums[1], "--path", str(single))
            call("delete", "--album", albums[1], "--photo", "one.jpg")
            if call("list", "--album", albums[1]) != "":
                raise RuntimeError("Пустой альбом потерян")
            call("download", "--album", albums[1], "--path", str(download))
            passed("Удаление последней фотографии и сохранение пустого альбома")
            call("mksite")
            index = page(base, "Фотоархив")
            order = call("list").splitlines()
            number = order.index(albums[0]) + 1
            album_page = page(base + "album" + str(number) + ".html", "galleria")
            images = Images()
            images.feed(album_page)
            if len(images.sources) != len(names):
                raise RuntimeError("Не все фотографии опубликованы")
            for src in images.sources:
                status, body = fetch(urllib.parse.urljoin(base, src))
                if status != 200 or not body:
                    raise RuntimeError("Фотография недоступна по публичной ссылке")
            empty_number = order.index(albums[1]) + 1
            page(base + "album" + str(empty_number) + ".html", "Фотоальбом пустой.")
            status, body = fetch(base + "missing-" + uuid.uuid4().hex)
            if status not in (403, 404) or "Ошибка при доступе к фотоархиву" not in body.decode():
                raise RuntimeError("Не работает страница ошибки")
            passed("mksite, HTTPS, галерея, пустой альбом, ссылки на фото и error.html")
            endpoint = "https://storage.yandexcloud.net/" + urllib.parse.quote(settings["BUCKET"], safe="")
            for suffix in ("?list-type=2", "?website"):
                status, _ = fetch(endpoint + suffix)
                if status not in (401, 403):
                    raise RuntimeError("Публично доступен листинг или конфигурация бакета")
            prefix = "albums/" + albums[0].encode().hex() + "/photos/"
            probe = endpoint + "/" + prefix + "anonymous-check.jpg"
            status, _ = fetch(probe, data=b"probe", method="PUT")
            if status not in (401, 403):
                call("delete", "--album", albums[0], "--photo", "anonymous-check.jpg")
                raise RuntimeError("Публичная запись разрешена")
            status, _ = fetch(endpoint + "/" + prefix + "A.JPG", method="DELETE")
            if status not in (401, 403):
                raise RuntimeError("Публичное удаление разрешено")
            passed("Анонимные листинг, конфигурация, запись и удаление запрещены")
            call("delete", "--album", albums[0], "--photo", "missing.jpg", success=False)
            call("list", "--album", "missing-" + uuid.uuid4().hex, success=False)
            call("upload", success=False)
            passed("Ошибки аргументов и отсутствующих объектов возвращают код 1")
        report["success"] = True
    except RuntimeError as error:
        report["error"] = str(error)
    except (OSError, ValueError, subprocess.TimeoutExpired):
        report["error"] = "Клиентская проверка не завершена; значения учётных данных скрыты"
    finally:
        try:
            remaining = call("list").splitlines()
        except (RuntimeError, subprocess.TimeoutExpired):
            remaining = albums
            report["cleanup_incomplete"] = True
        for album in albums:
            if album not in remaining:
                continue
            try:
                call("delete", "--album", album)
            except (RuntimeError, subprocess.TimeoutExpired):
                report["cleanup_incomplete"] = True
        try:
            call("mksite")
        except (RuntimeError, subprocess.TimeoutExpired):
            report["cleanup_incomplete"] = True
        if report.get("cleanup_incomplete"):
            report["success"] = False
        target = root / "client-result.json"
        target.write_text(json.dumps(report, ensure_ascii=False, indent=2))
        target.chmod(0o600)
        print(json.dumps(report, ensure_ascii=False), flush=True)
    return 0 if report["success"] else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception:
        print("Не удалось начать проверку; учётные данные не выводятся", file=sys.stderr)
        sys.exit(1)
