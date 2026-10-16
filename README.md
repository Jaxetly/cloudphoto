# Фотоархив

`cloudphoto` — консольная программа для управления фотоальбомами в Yandex Object Storage и публикации [сайта фотоархива](https://photos.vvot05.itiscl.ru/).

## Сборка

Для сборки нужны Go 1.24 или новее и `make`.

```bash
make build
```

Результат сборки — `dist/linux-amd64/cloudphoto`, статический исполняемый файл для Linux x86_64.

## Настройка программы

| Переменная | Назначение |
|---|---|
| `BUCKET` | Имя бакета фотоархива, совпадающее с `bucket_name` Terraform |
| `AWS_ACCESS_KEY_ID` | Идентификатор статического S3-ключа сервисного аккаунта |
| `AWS_SECRET_ACCESS_KEY` | Секретная часть этого ключа |

Регион `ru-central1` и адрес Object Storage `https://storage.yandexcloud.net` заданы в коде. Для скрытого ввода ключей в Bash подойдут следующие команды.

```bash
export BUCKET=photos.vvot05.itiscl.ru
read -r -s -p 'AWS_ACCESS_KEY_ID: ' AWS_ACCESS_KEY_ID; printf '\n'
read -r -s -p 'AWS_SECRET_ACCESS_KEY: ' AWS_SECRET_ACCESS_KEY; printf '\n'
export AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY
```

## Команды

В примерах `cloudphoto` доступен в `PATH`.

```bash
cloudphoto list
cloudphoto list --album 'Природа'
cloudphoto upload --album 'Природа' --path ./photo/nature
cloudphoto download --album 'Природа' --path ./download
cloudphoto delete --album 'Природа' --photo 'Лес.JPG'
cloudphoto delete --album 'Природа'
cloudphoto mksite
```

Без `--path` используется текущий каталог. `upload` берёт из него обычные файлы `.jpg` и `.jpeg` без учёта регистра расширения и без обхода подкаталогов. `upload` и `download` заменяют файлы с совпадающим именем. Чтобы удалить только одну фотографию, укажите `--photo`. После загрузки или удаления запустите `mksite`, чтобы обновить сайт.

## Организация хранилища

```text
albums/<имя альбома в hex-кодировке UTF-8>/.album
albums/<имя альбома в hex-кодировке UTF-8>/photos/<исходное имя файла>
index.html
error.html
album1.html ... albumN.html
```

Например, альбом `A` кодируется как `41`, а его фотография `Лес.JPG` имеет ключ `albums/41/photos/Лес.JPG`. Hex-кодирование обратимо, сохраняет регистр и порядок байтов, исключает конфликт имени альбома с разделителями ключа. Исходные имена фотографий не изменяются.

Пустой объект `.album` позволяет сохранить альбом после удаления последней фотографии. Одинаковые имена файлов в разных альбомах имеют разные ключи. HTML лежит отдельно от `albums/` и не попадает в списки фотографий.

## Публикация сайта

`mksite` включает статический хостинг с `index.html` и `error.html`. Страницы `album1.html`, `album2.html` и далее соответствуют порядку имён альбомов по байтам UTF-8. Публичное чтение открывается для фотографий и страниц сайта.

## Инфраструктура

Конфигурация в [`terraform/`](terraform/) создаёт бакет, DNS-запись, HTTPS и сервисный аккаунт с ключом. Она использует существующие DNS-зону и сертификат, а для авторизации берёт `YC_TOKEN` из окружения.

В [примере параметров](terraform/terraform.tfvars.example) `dns_zone_id` содержит полное имя сайта, как указано в задании. Terraform находит ID существующей DNS-зоны по имени `vvotNN`.
