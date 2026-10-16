variable "folder_id" {
  type        = string
  description = "ID каталога Yandex Cloud"
  validation {
    condition     = length(var.folder_id) > 0
    error_message = "Укажите folder_id."
  }
}

variable "bucket_name" {
  type        = string
  description = "Имя бакета, совпадающее с BUCKET и адресом сайта"
}

variable "domain" {
  type        = string
  description = "Существующая зона: vvotNN.itiscl.ru"
}

variable "cert_id" {
  type        = string
  description = "ID выпущенного сертификата Certificate Manager"
}

variable "dns_zone_id" {
  type        = string
  description = "Полное доменное имя сайта; название переменной сохранено из условия"
}
