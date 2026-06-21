variable "resource_group_name" {
  type        = string
  description = "Resource group to create for the deployment."
  default     = "lantern-rg"
}

variable "location" {
  type        = string
  description = "Azure region (e.g. eastus)."
  default     = "eastus"
}

variable "app_name" {
  type        = string
  description = "Container App name."
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
  type        = number
  description = "vCPU per replica."
  default     = 0.5
}

variable "memory" {
  type        = string
  description = "Memory per replica (must pair with cpu per ACA's allowed combos)."
  default     = "1Gi"
}

variable "storage_account_name" {
  type        = string
  description = "Storage account for Azure Files (globally unique, 3-24 lowercase alphanumeric)."
}

variable "file_share_name" {
  type        = string
  description = "Azure Files share name for snapshot backups."
  default     = "lantern-backups"
}

variable "file_share_quota_gb" {
  type        = number
  description = "Azure Files share quota in GiB."
  default     = 1
}

# --- snapshot durability (#770) ---

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
