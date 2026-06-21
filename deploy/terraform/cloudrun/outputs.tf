output "service_uri" {
  description = "Public URL of the Lantern Cloud Run service."
  value       = google_cloud_run_v2_service.lantern.uri
}

output "service_account_email" {
  description = "Service identity the revision runs as."
  value       = google_service_account.lantern.email
}

output "backup_bucket" {
  description = "Cloud Storage bucket holding the snapshot dumps."
  value       = google_storage_bucket.backups.name
}
