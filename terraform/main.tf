terraform {
  required_version = ">= 1.6.0"
  required_providers {
    yandex = {
      source  = "yandex-cloud/yandex"
      version = "0.238.0"
    }
  }
}

provider "yandex" {
  folder_id = var.folder_id
}

locals {
  zone_domain = trimsuffix(var.domain, ".")
  site_domain = trimsuffix(var.dns_zone_id, ".")
}

data "yandex_dns_zone" "existing" {
  name      = split(".", local.zone_domain)[0]
  folder_id = var.folder_id
}

data "yandex_cm_certificate" "existing" {
  certificate_id = var.cert_id
}

resource "yandex_storage_bucket" "photos" {
  folder_id     = var.folder_id
  bucket        = var.bucket_name
  max_size      = 1073741824
  force_destroy = false

  # Начальное правило сохраняется mksite вместе с публичным чтением фотографий.
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "InfrastructureAdministrator"
      Effect    = "Allow"
      Principal = { CanonicalUser = jsondecode(file("${path.module}/.local/administrator.json")).id }
      Action    = "s3:*"
      Resource  = ["arn:aws:s3:::${var.bucket_name}", "arn:aws:s3:::${var.bucket_name}/*"]
      }, {
      Sid       = "ClientAccount"
      Effect    = "Allow"
      Principal = { CanonicalUser = yandex_iam_service_account.client.id }
      Action    = "s3:*"
      Resource  = ["arn:aws:s3:::${var.bucket_name}", "arn:aws:s3:::${var.bucket_name}/*"]
    }]
  })

  https {
    certificate_id = data.yandex_cm_certificate.existing.id
  }

  lifecycle {
    # Эти два параметра настраивает команда cloudphoto mksite.
    ignore_changes = [policy, website]
    precondition {
      condition     = data.yandex_dns_zone.existing.zone == "${local.zone_domain}." && data.yandex_dns_zone.existing.public
      error_message = "DNS-зона должна быть публичной и совпадать с domain."
    }
    precondition {
      condition     = local.site_domain == var.bucket_name && local.site_domain == "photos.${local.zone_domain}"
      error_message = "bucket_name и dns_zone_id должны задавать photos.<domain>."
    }
    precondition {
      condition     = data.yandex_cm_certificate.existing.status == "ISSUED"
      error_message = "Сертификат ещё не выпущен."
    }
    precondition {
      condition = (
        contains(data.yandex_cm_certificate.existing.domains, local.site_domain) ||
        contains(data.yandex_cm_certificate.existing.domains, "*.${local.zone_domain}")
      ) && timecmp(data.yandex_cm_certificate.existing.not_after, plantimestamp()) > 0
      error_message = "Сертификат должен покрывать адрес сайта и быть действующим."
    }
  }
}

resource "yandex_dns_recordset" "photos" {
  zone_id = data.yandex_dns_zone.existing.id
  name    = "${local.site_domain}."
  type    = "CNAME"
  ttl     = 60
  data    = ["${var.bucket_name}.website.yandexcloud.net."]
}

resource "yandex_iam_service_account" "client" {
  folder_id   = var.folder_id
  name        = "cloudphoto-client"
  description = "Управление фотоархивом"
}

resource "yandex_storage_bucket_iam_binding" "client" {
  bucket  = yandex_storage_bucket.photos.bucket
  role    = "storage.admin"
  members = ["serviceAccount:${yandex_iam_service_account.client.id}"]
}

resource "yandex_iam_service_account_static_access_key" "client" {
  service_account_id = yandex_iam_service_account.client.id
  description        = "Ключ программы cloudphoto"
  pgp_key            = trimspace(file("${path.module}/.local/public-key.b64"))
  depends_on         = [yandex_storage_bucket_iam_binding.client]
}

output "site_url" {
  value = "https://${local.site_domain}/"
}
