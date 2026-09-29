# Least-privilege roles, created only in normal accounts (create_iam = true).
# The Learner Lab forbids creating roles; there the instances use the
# existing instance_profile_name (docs/spec/iam.md §2).

data "aws_iam_policy_document" "ec2_assume" {
  statement {
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

# Controller: the policy of infra/iam/controller-policy.json (what the
# controller code calls) ...
resource "aws_iam_role" "controller" {
  count = var.create_iam ? 1 : 0

  name_prefix        = "${var.name_prefix}-controller-"
  assume_role_policy = data.aws_iam_policy_document.ec2_assume.json
}

resource "aws_iam_role_policy" "controller" {
  count = var.create_iam ? 1 : 0

  name = "controller"
  role = aws_iam_role.controller[0].id
  policy = templatefile("${path.module}/iam/controller-policy.json", {
    AWS_REGION     = var.region
    AWS_ACCOUNT_ID = data.aws_caller_identity.current.account_id
    ASG_NAME       = local.asg_name
  })
}

# ... plus a separate bootstrap statement for S3 (docs/spec/iam.md §2.1):
# binary download at boot and the evidence upload done by the AWS CLI timer.
data "aws_iam_policy_document" "controller_bootstrap" {
  statement {
    sid       = "DownloadBinaries"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.project.arn}/bin/*"]
  }

  statement {
    sid       = "UploadEvidence"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.project.arn}/logs/*"]
  }

  statement {
    sid       = "ListForSync"
    actions   = ["s3:ListBucket"]
    resources = [aws_s3_bucket.project.arn]
  }
}

resource "aws_iam_role_policy" "controller_bootstrap" {
  count = var.create_iam ? 1 : 0

  name   = "bootstrap"
  role   = aws_iam_role.controller[0].id
  policy = data.aws_iam_policy_document.controller_bootstrap.json
}

resource "aws_iam_instance_profile" "controller" {
  count = var.create_iam ? 1 : 0

  name_prefix = "${var.name_prefix}-controller-"
  role        = aws_iam_role.controller[0].name
}

# Application instances: download their binary only.
data "aws_iam_policy_document" "app_bootstrap" {
  statement {
    sid       = "DownloadBinary"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.project.arn}/bin/testapp"]
  }
}

resource "aws_iam_role" "app" {
  count = var.create_iam ? 1 : 0

  name_prefix        = "${var.name_prefix}-app-"
  assume_role_policy = data.aws_iam_policy_document.ec2_assume.json
}

resource "aws_iam_role_policy" "app" {
  count = var.create_iam ? 1 : 0

  name   = "bootstrap"
  role   = aws_iam_role.app[0].id
  policy = data.aws_iam_policy_document.app_bootstrap.json
}

resource "aws_iam_instance_profile" "app" {
  count = var.create_iam ? 1 : 0

  name_prefix = "${var.name_prefix}-app-"
  role        = aws_iam_role.app[0].name
}
