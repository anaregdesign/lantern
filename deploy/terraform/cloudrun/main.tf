# Single-instance (Tier B) Lantern on Cloud Run with snapshot durability.
#
# Lantern is in-memory, so a rolling revision update would lose the graph.
# This mounts a Cloud Storage bucket (GCS FUSE) at LANTERN_BACKUP_DIR and
# enables the periodic backup + restore-on-startup engine (#770), so a new
# revision restores the newest dump on boot. min=max=1 keeps it single-
# instance (Tier B per the replication RFC D7); GCS FUSE has no file locking,
# which the engine handles via per-instance filenames.

locals {
  # Merge the backup/runtime env with any caller-supplied extras. Extras win.
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

# Backup bucket. Uniform bucket-level access; versioning off (the engine keeps
# its own retention of per-instance dump files).
resource "google_storage_bucket" "backups" {
  name                        = var.backup_bucket_name
  project                     = var.project_id
  location                    = var.region
  uniform_bucket_level_access = true
  force_destroy               = false
}

# Dedicated service identity for the Lantern revision.
resource "google_service_account" "lantern" {
  project      = var.project_id
  account_id   = "${var.service_name}-sa"
  display_name = "Lantern Cloud Run service account"
}

# Least privilege: read+write objects in the backup bucket only.
resource "google_storage_bucket_iam_member" "backups_rw" {
  bucket = google_storage_bucket.backups.name
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${google_service_account.lantern.email}"
}

resource "google_cloud_run_v2_service" "lantern" {
  name     = var.service_name
  project  = var.project_id
  location = var.region
  ingress  = var.ingress

  # Cloud Storage FUSE volumes require the second-generation execution env.
  template {
    service_account = google_service_account.lantern.email

    scaling {
      min_instance_count = 1
      max_instance_count = 1
    }

    containers {
      image = var.image

      # Named "h2c" so Cloud Run serves HTTP/2 end-to-end (Lantern's
      # Connect / gRPC surface is h2c).
      ports {
        name           = "h2c"
        container_port = var.port
      }

      resources {
        limits = {
          cpu    = var.cpu
          memory = var.memory
        }
      }

      dynamic "env" {
        for_each = local.env
        content {
          name  = env.key
          value = env.value
        }
      }

      volume_mounts {
        name       = "backups"
        mount_path = var.backup_dir
      }
    }

    volumes {
      name = "backups"
      gcs {
        bucket    = google_storage_bucket.backups.name
        read_only = false
      }
    }
  }

  depends_on = [google_storage_bucket_iam_member.backups_rw]
}
