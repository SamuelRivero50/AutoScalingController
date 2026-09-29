# Controller configuration rendered from Terraform's own values, so a
# recreate needs no manual re-pointing (ADR-0016).
locals {
  controller_config = templatefile("${path.module}/templates/controller.json.tftpl", {
    profile                 = var.profile
    region                  = var.region
    asg_name                = aws_autoscaling_group.app.name
    target_group_arn        = aws_lb_target_group.app.arn
    load_balancer_dimension = aws_lb.app.arn_suffix
    target_group_dimension  = aws_lb_target_group.app.arn_suffix
  })
}

# The controller runs outside the application ASG (ADR-0009).
resource "aws_instance" "controller" {
  ami                    = data.aws_ssm_parameter.al2023.insecure_value
  instance_type          = var.instance_type
  subnet_id              = aws_subnet.public[0].id
  vpc_security_group_ids = [aws_security_group.controller.id]
  iam_instance_profile   = local.instance_profile
  key_name               = var.key_name != "" ? var.key_name : null

  metadata_options {
    http_tokens                 = "required"
    http_endpoint               = "enabled"
    http_put_response_hop_limit = 1
  }

  user_data = templatefile("${path.module}/templates/controller-user-data.sh.tftpl", {
    bucket            = aws_s3_bucket.project.id
    region            = var.region
    controller_config = local.controller_config
  })
  user_data_replace_on_change = true

  tags = { Name = "${var.name_prefix}-controller" }

  depends_on = [aws_s3_object.binary]
}
