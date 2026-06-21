variable "project_id" {
  type        = string
  description = "GCP project ID to deploy into."
}

variable "region" {
  type        = string
  description = "Cloud Run region (e.g. us-central1)."
  default     = "us-central1"
}

variable "service_name" {
  type        = string
  description = "Cloud Run service name."
  default     = "lantern"
}

variable "image" {
  type        = string
  description = "Lantern server container image."
  default     = "ghcr.io/anaregdesign/lantern:latest"
}

variable "port" {
  type        = number
  description = "Container port Lantern serves on (h2c)."
  default     = 6380
}

variable "cpu" {
  type        = string
  description = "CPU limit per instance."
  default     = "1"
}

variable "memory" {
  type        = string
  description = "Memory limit per instance."
  default     = "512Mi"
}

variable "ingress" {
  type        = string
  description = "Cloud Run ingress: INGRESS_TRAFFIC_ALL or INGRESS_TRAFFIC_INTERNAL_ONLY."
  default     = "INGRESS_TRAFFIC_ALL"
}

# --- snapshot durability (#770) ---

variable "backup_bucket_name" {
  type        = string
  description = "Cloud Storage bucket for snapshot backups. Created by this module."
}

variable "backup_dir" {
  type        = string
  description = "Mount path for the backup volume (LANTERN_BACKUP_DIR)."
  default     = "/data"
}

variable "backup_interval" {
  type        = string
  description = "Backup cadence (LANTERN_BACKUP_INTERVAL)."
  default     = "5m"
}

variable "backup_retain" {
  type        = number
  description = "Backups to retain per instance (LANTERN_BACKUP_RETAIN)."
  default     = 3
}

variable "default_ttl_seconds" {
  type        = number
  description = "Default vertex/edge TTL. Keep well above backup_interval so restores carry live data."
  default     = 86400
}

variable "drain_delay_seconds" {
  type        = number
  description = "Graceful drain hold on SIGTERM (#768) for clean revision cutover."
  default     = 5
}

variable "extra_env" {
  type        = map(string)
  description = "Additional LANTERN_* environment variables."
  default     = {}
}
