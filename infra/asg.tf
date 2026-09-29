resource "aws_launch_template" "app" {
  name_prefix   = "${var.name_prefix}-app-"
  image_id      = data.aws_ssm_parameter.al2023.insecure_value
  instance_type = var.instance_type

  vpc_security_group_ids = [aws_security_group.app.id]

  iam_instance_profile {
    name = local.app_profile
  }

  # ADR-0014: unlimited credits so a sustained stress is not throttled.
  credit_specification {
    cpu_credits = "unlimited"
  }

  metadata_options {
    http_tokens                 = "required" # IMDSv2 only
    http_endpoint               = "enabled"
    http_put_response_hop_limit = 1
  }

  monitoring {
    enabled = var.detailed_monitoring
  }

  user_data = base64encode(templatefile("${path.module}/templates/app-user-data.sh.tftpl", {
    bucket   = aws_s3_bucket.project.id
    app_port = var.app_port
  }))

  tag_specifications {
    resource_type = "instance"
    tags          = merge(local.tags, { Name = "${var.name_prefix}-app" })
  }

  depends_on = [aws_s3_object.binary]
}

# Actuator only (ADR-0009): no scaling policies. The controller owns
# desired capacity, so Terraform ignores later changes to it.
resource "aws_autoscaling_group" "app" {
  name                = local.asg_name
  min_size            = 1
  max_size            = 5
  desired_capacity    = 1
  vpc_zone_identifier = aws_subnet.public[*].id
  target_group_arns   = [aws_lb_target_group.app.arn]

  health_check_type         = "ELB"
  health_check_grace_period = 400 # above the 360s pending timeout

  # ADR-0015: no AZ rebalancing behind the controller's back.
  suspended_processes = ["AZRebalance"]

  launch_template {
    id      = aws_launch_template.app.id
    version = aws_launch_template.app.latest_version
  }

  tag {
    key                 = "Project"
    value               = local.tags.Project
    propagate_at_launch = true
  }

  lifecycle {
    ignore_changes = [desired_capacity]
  }
}
