output "ingress_fqdn" {
  description = "Public FQDN of the Lantern Container App."
  value       = azurerm_container_app.lantern.ingress[0].fqdn
}

output "resource_group" {
  description = "Resource group the deployment lives in."
  value       = azurerm_resource_group.this.name
}

output "backup_share" {
  description = "Azure Files share holding the snapshot dumps."
  value       = azurerm_storage_share.backups.name
}
