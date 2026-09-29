resource "aws_lb" "app" {
  name               = "${var.name_prefix}-alb"
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = aws_subnet.public[*].id
}

resource "aws_lb_target_group" "app" {
  name                 = "${var.name_prefix}-tg"
  port                 = var.app_port
  protocol             = "HTTP"
  vpc_id               = aws_vpc.main.id
  target_type          = "instance"
  deregistration_delay = 60

  # Generous thresholds (docs/spec/infrastructure.md §1): 50s of failures
  # before a loaded instance is marked unhealthy.
  health_check {
    path                = "/health"
    port                = "traffic-port"
    protocol            = "HTTP"
    matcher             = "200"
    interval            = 10
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 5
  }
}

# HTTP only: the Learner Lab has no domain or certificate (documented
# limitation, docs/spec/infrastructure.md §1).
resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.app.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.app.arn
  }
}

# The stress endpoint is never reachable through the ALB.
resource "aws_lb_listener_rule" "deny_admin" {
  listener_arn = aws_lb_listener.http.arn
  priority     = 1

  condition {
    path_pattern {
      values = ["/admin/*", "/admin"]
    }
  }

  action {
    type = "fixed-response"

    fixed_response {
      content_type = "text/plain"
      message_body = "forbidden"
      status_code  = "403"
    }
  }
}
