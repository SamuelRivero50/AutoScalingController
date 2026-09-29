output "alb_dns_name" {
  description = "Public HTTP endpoint of the test application."
  value       = aws_lb.app.dns_name
}

output "asg_name" {
  description = "Application Auto Scaling group."
  value       = aws_autoscaling_group.app.name
}

output "target_group_arn" {
  description = "Application target group."
  value       = aws_lb_target_group.app.arn
}

output "bucket" {
  description = "Project bucket (binaries and uploaded decision logs)."
  value       = aws_s3_bucket.project.id
}

output "controller_instance_id" {
  description = "Controller EC2 instance."
  value       = aws_instance.controller.id
}

output "controller_public_ip" {
  description = "Controller public IP, for SSH from operator_ssh_cidr."
  value       = aws_instance.controller.public_ip
}

output "controller_config" {
  description = "Rendered controller configuration file."
  value       = local.controller_config
}
