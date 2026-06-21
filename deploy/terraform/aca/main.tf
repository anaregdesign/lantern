# Single-instance (Tier B) Lantern on Azure Container Apps with snapshot
# durability. An Azure Files (SMB) share is mounted at LANTERN_BACKUP_DIR and
# the periodic backup + restore-on-startup engine (#770) re-seeds the graph on
# every revision rotation. min=max=1 keeps it single-instance (Tier B per the
# replication RFC D7); EmptyDir would be ephemeral, so Azure Files is required.

locals {
  env = merge({
    LANTERN_PORT                    = tostring(var.port)
    LANTERN_DEFAULT_TTL_SECONDS     = tostring(var.default_ttl_seconds)
    LANTERN_DRAIN_DELAY_SECONDS     = tostring(var.drain_delay_seconds)
    LANTERN_BACKUP_ENABLED          = "true"
    LANTERN_BACKUP_DIR              = var.backup_dir
    LANTERN_BACKUP_INTERVAL         = var.backup_interval
    LANTERN_BACKUP_RETAIN           = tostring(var.backup_retain)
    LANTERN_BACKUP_RESTORE_ON_START = "true"
  }, var.extra_env)
}

resource "azurerm_resource_group" "this" {
  name     = var.resource_group_name
  location = var.location
}

resource "azurerm_storage_account" "this" {
  name                     = var.storage_account_name
  resource_group_name      = azurerm_resource_group.this.name
  location                 = azurerm_resource_group.this.location
  account_tier             = "Standard"
  account_replication_type = "LRS"
}

resource "azurerm_storage_share" "backups" {
  name                 = var.file_share_name
  storage_account_name = azurerm_storage_account.this.name
  quota                = var.file_share_quota_gb
}

resource "azurerm_container_app_environment" "this" {
  name                = "${var.app_name}-env"
  resource_group_name = azurerm_resource_group.this.name
  location            = azurerm_resource_group.this.location
}

resource "azurerm_container_app_environment_storage" "backups" {
  name                         = "backups"
  container_app_environment_id = azurerm_container_app_environment.this.id
  account_name                 = azurerm_storage_account.this.name
  share_name                   = azurerm_storage_share.backups.name
  access_key                   = azurerm_storage_account.this.primary_access_key
  access_mode                  = "ReadWrite"
}

resource "azurerm_container_app" "lantern" {
  name                         = var.app_name
  container_app_environment_id = azurerm_container_app_environment.this.id
  resource_group_name          = azurerm_resource_group.this.name
  revision_mode                = "Single"

  template {
    min_replicas = 1
    max_replicas = 1

    container {
      name   = "lantern"
      image  = var.image
      cpu    = var.cpu
      memory = var.memory

      dynamic "env" {
        for_each = local.env
        content {
          name  = env.key
          value = env.value
        }
      }

      volume_mounts {
        name = "backups"
        path = var.backup_dir
      }
    }

    volume {
      name         = "backups"
      storage_type = "AzureFile"
      storage_name = azurerm_container_app_environment_storage.backups.name
    }
  }

  ingress {
    external_enabled = true
    target_port      = var.port
    transport        = "http2"

    traffic_weight {
      latest_revision = true
      percentage      = 100
    }
  }
}
