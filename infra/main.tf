data "aws_caller_identity" "current" {}

data "aws_availability_zones" "available" {
  state = "available"
}

# Amazon Linux 2023, latest, from the public SSM parameter.
data "aws_ssm_parameter" "al2023" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
}

locals {
  # Every resource carries the Project tag used by the IAM condition
  # (docs/spec/iam.md).
  tags = {
    Project = "auto-scaling-controller"
  }

  asg_name = "${var.name_prefix}-app"
  azs      = slice(data.aws_availability_zones.available.names, 0, 2)

  instance_profile = var.create_iam ? aws_iam_instance_profile.controller[0].name : var.instance_profile_name
  app_profile      = var.create_iam ? aws_iam_instance_profile.app[0].name : var.instance_profile_name
}
