// Package awsclient loads the shared AWS SDK configuration used by the real
// adapters (cloudwatch, asg). It applies the request retry policy of
// docs/spec/lifecycle-and-failures.md §3: every attempt is bounded by an
// HTTP client timeout, and failed attempts are retried with exponential
// backoff (1s, 2s, 4s ...) plus jitter, capped at a maximum backoff. The
// whole call is bounded by the caller's context (the cycle budget).
//
// It is an adapter-side helper: nothing outside internal/adapters and the
// composition root in cmd/ may depend on it.
package awsclient

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
)

// Settings configures the SDK client behavior.
type Settings struct {
	// Region is the AWS region, e.g. us-east-1. Required.
	Region string
	// AttemptTimeout bounds each HTTP attempt.
	AttemptTimeout time.Duration
	// MaxRetries is the number of retries after the first attempt.
	MaxRetries int
	// MaxBackoff caps the delay between attempts.
	MaxBackoff time.Duration
}

// Validate reports invalid settings.
func (s Settings) Validate() error {
	var errs []error
	if s.Region == "" {
		errs = append(errs, errors.New("region is required"))
	}
	if s.AttemptTimeout <= 0 {
		errs = append(errs, errors.New("attempt timeout must be positive"))
	}
	if s.MaxRetries < 0 {
		errs = append(errs, errors.New("max retries must not be negative"))
	}
	if s.MaxBackoff <= 0 {
		errs = append(errs, errors.New("max backoff must be positive"))
	}
	return errors.Join(errs...)
}

// Load returns the SDK configuration with the default credential chain
// (the EC2 instance profile on the controller host).
func Load(ctx context.Context, s Settings) (aws.Config, error) {
	if err := s.Validate(); err != nil {
		return aws.Config{}, fmt.Errorf("invalid aws settings: %w", err)
	}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(s.Region),
		config.WithHTTPClient(awshttp.NewBuildableClient().WithTimeout(s.AttemptTimeout)),
		config.WithRetryer(func() aws.Retryer { return NewRetryer(s) }),
	)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load aws config: %w", err)
	}
	return cfg, nil
}

// NewRetryer returns the standard SDK retryer with the project's attempt
// count and backoff schedule. A new retryer is returned on every call, as
// the SDK requires.
func NewRetryer(s Settings) aws.Retryer {
	return retry.NewStandard(func(o *retry.StandardOptions) {
		o.MaxAttempts = s.MaxRetries + 1
		o.MaxBackoff = s.MaxBackoff
		o.Backoff = Backoff{Max: s.MaxBackoff}
	})
}

// Backoff is the exponential schedule 1s, 2s, 4s ... capped at Max. Each
// delay is jittered down by up to 20% so concurrent retries spread out
// without ever exceeding the documented schedule.
type Backoff struct {
	Max time.Duration
	// Rand returns a value in [0, 1); nil uses math/rand/v2.
	Rand func() float64
}

// BackoffDelay implements retry.BackoffDelayer. attempt is 1 for the delay
// before the first retry.
func (b Backoff) BackoffDelay(attempt int, _ error) (time.Duration, error) {
	if attempt < 1 {
		attempt = 1
	}
	d := time.Second
	for i := 1; i < attempt && d < b.Max; i++ {
		d *= 2
	}
	d = min(d, b.Max)
	r := b.Rand
	if r == nil {
		r = rand.Float64
	}
	return d - time.Duration(0.2*r()*float64(d)), nil
}
