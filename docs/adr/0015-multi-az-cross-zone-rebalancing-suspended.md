# ADR-0015: Multi-AZ Deployment with Cross-Zone Rebalancing Suspended

- **Status**: Accepted
- **Related**: `docs/spec/infrastructure.md`, REQ-CONSTRAINT-1

## Context

An Application Load Balancer requires its target group's Auto Scaling Group to span at least two Availability Zones. ASG's own automatic AZ-rebalancing can move or replace instances independently of the controller's decisions, which would make instance-level outcomes harder to attribute to specific controller actions during the experiment.

## Decision

Deploy the application ASG across at least two Availability Zones (required by the ALB), and suspend the `AZRebalance` process via Terraform so that instance count changes only ever result from the controller's own `SetDesiredCapacity`/termination calls.

## Alternatives considered

- **Single-AZ deployment**: not possible — the ALB requires at least two Availability Zones for the target group.
- **Leaving AZ-rebalancing enabled**: rejected — could silently terminate and relaunch an instance for balance reasons unrelated to the controller's decision, muddying the decision-log's explainability (REQ-CONSTRAINT-7) during evaluation.

## Consequences

- Every instance-count change observed in the decision log can be attributed solely to a controller-issued action.

## Sources

- AWS. *Auto Scaling groups with multiple Availability Zones* — https://docs.aws.amazon.com/autoscaling/ec2/userguide/asg-in-vpc.html
