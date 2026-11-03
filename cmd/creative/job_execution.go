package main

import (
	"context"
	"errors"
	"flag"
	"time"

	"github.com/sogud/Muse/internal/core"
)

// runJobLifecycle is the only CLI entry point for durable provider execution.
// Read-only status, existing-task polling and manual recovery never perform a
// second paid submission.
func runJobLifecycle(ctx context.Context, args []string) (any, error) {
	command := args[0]
	var id, providerTaskID string
	var allowPaid bool
	var interval, timeout time.Duration

	_, err := parsedFlags("job "+command, args[1:], func(fs *flag.FlagSet) {
		fs.StringVar(&id, "id", "", "durable creative job ID")
		if command == "submit" {
			fs.BoolVar(&allowPaid, "allow-paid", false, "allow exactly one paid request on a separately approved job")
		}
		if command == "attach" {
			fs.StringVar(&providerTaskID, "provider-task-id", "", "existing ID found on the provider dashboard")
		}
		if command == "wait" {
			fs.DurationVar(&interval, "interval", 5*time.Second, "interval between read-only status queries")
			fs.DurationVar(&timeout, "timeout", 10*time.Minute, "maximum time to wait in this CLI process")
		}
	})
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, fail("INVALID_ARGUMENT", "--id is required")
	}
	if command == "wait" {
		if interval < 2*time.Second || interval > time.Minute || timeout < interval || timeout > time.Hour {
			return nil, fail("INVALID_ARGUMENT", "wait requires interval 2s-1m and timeout up to 1h")
		}
	}
	if command == "attach" && providerTaskID == "" {
		return nil, fail("INVALID_ARGUMENT", "--provider-task-id is required to recover an uncertain submission")
	}

	return withStore(ctx, func(store *core.ProjectStore) (any, error) {
		runtime := core.NewJobRuntime(core.NewCreativeWorkspace(store))
		switch command {
		case "submit":
			return runtime.Submit(ctx, id, allowPaid)
		case "status":
			return runtime.Status(ctx, id)
		case "sync", "resume":
			return runtime.Sync(ctx, id)
		case "attach":
			return runtime.Attach(ctx, id, providerTaskID)
		case "wait":
			waitCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			for {
				result, err := runtime.Sync(waitCtx, id)
				if err != nil {
					return nil, err
				}
				switch result.Job.Status {
				case "succeeded", "failed", "cancelled":
					return result, nil
				}
				select {
				case <-waitCtx.Done():
					if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
						return nil, fail("TIMEOUT", "job is still running; use job status or job resume later")
					}
					return nil, waitCtx.Err()
				case <-time.After(interval):
				}
			}
		}
		return nil, fail("INVALID_ARGUMENT", "unknown job lifecycle command")
	})
}
