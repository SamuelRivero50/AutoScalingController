variable "region" {
  description = "AWS region."
  type        = string
  default     = "us-east-1"
}

variable "name_prefix" {
  description = "Prefix for resource names. The ASG name is <prefix>-app."
  type        = string
  default     = "asc"

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,20}$", var.name_prefix))
    error_message = "name_prefix must be 2-21 lowercase letters, digits or hyphens, starting with a letter."
  }
}

variable "operator_ssh_cidr" {
  description = "CIDR allowed to SSH into the controller instance (the operator's IP, e.g. 203.0.113.10/32)."
  type        = string

  validation {
    condition     = can(cidrhost(var.operator_ssh_cidr, 0)) && var.operator_ssh_cidr != "0.0.0.0/0"
    error_message = "operator_ssh_cidr must be a valid CIDR and must not be 0.0.0.0/0."
  }
}

variable "key_name" {
  description = "Existing EC2 key pair name for SSH to the controller (Learner Lab: vockey). Empty disables SSH keys."
  type        = string
  default     = ""
}

variable "create_iam" {
  description = "Create least-privilege roles (normal accounts). false uses instance_profile_name (Learner Lab)."
  type        = bool
  default     = false
}

variable "instance_profile_name" {
  description = "Existing instance profile used when create_iam is false."
  type        = string
  default     = "LabInstanceProfile"
}

variable "detailed_monitoring" {
  description = "Enable EC2 detailed monitoring (1-minute CPU) on the application instances."
  type        = bool
  default     = true
}

variable "instance_type" {
  description = "Instance type for the application and the controller (ADR-0014)."
  type        = string
  default     = "t3.micro"
}

variable "app_port" {
  description = "Port the test application listens on."
  type        = number
  default     = 8080
}

variable "profile" {
  description = "Controller configuration profile. The real deployment uses realistic."
  type        = string
  default     = "realistic"

  validation {
    condition     = contains(["realistic", "demo"], var.profile)
    error_message = "profile must be realistic or demo."
  }
}

variable "binaries_dir" {
  description = "Directory holding the linux/amd64 testapp, controller and stress binaries (make build-linux)."
  type        = string
  default     = "../bin"
}
