# ADR-0014: Instance Type t3.micro in Unlimited CPU Credit Mode

- **Status**: Accepted
- **Related**: `docs/context/constraints.md`, `docs/spec/infrastructure.md`, `docs/spec/app.md`

## Context

The project has only ~$50 in AWS Academy credits, favoring the smallest practical instance type, but the experiment specifically needs to drive one instance to 80-90% CPU via the internal stress endpoint to validate the scale-out trigger.

## Decision

Use `t3.micro` instances in **unlimited** CPU credit mode (t3's default mode).

## Alternatives considered

- **`t3.micro` in standard (throttled) credit mode**: rejected — once baseline CPU credits are exhausted, standard mode throttles the instance's CPU, which would prevent the stress endpoint from reliably reaching the 70% scale-out threshold, invalidating the experiment.
- **A larger, non-burstable instance type** (e.g., `m5.large`): rejected — unnecessarily costly against the $50 budget for a controller whose experiment only needs one instance briefly under load at a time.

## Consequences

- Unlimited mode can incur a small surplus charge ($0.05/vCPU-hour) only if the 24-hour rolling average exceeds baseline — negligible for runs under an hour, and mitigated further by the destroy-per-session policy (ADR-0016).

## Sources

- AWS. *Burstable performance instances — unlimited mode* — https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/burstable-performance-instances-unlimited-mode-concepts.html
