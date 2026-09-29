# Project bucket: binary delivery and evidence persistence
# (docs/spec/infrastructure.md §2-§3, ADR-0018). force_destroy lets
# terraform destroy succeed while the bucket holds objects, so the evidence
# must be collected BEFORE every destroy (scripts/collect-evidence.sh).
resource "aws_s3_bucket" "project" {
  bucket_prefix = "${var.name_prefix}-project-"
  force_destroy = true
}

resource "aws_s3_bucket_public_access_block" "project" {
  bucket = aws_s3_bucket.project.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "project" {
  bucket = aws_s3_bucket.project.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_ownership_controls" "project" {
  bucket = aws_s3_bucket.project.id

  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

resource "aws_s3_object" "binary" {
  for_each = toset(["testapp", "controller", "stress"])

  bucket = aws_s3_bucket.project.id
  key    = "bin/${each.key}"
  source = "${var.binaries_dir}/${each.key}"
  etag   = filemd5("${var.binaries_dir}/${each.key}")
}
