package keepawake

import "context"

func hold(ctx context.Context, ready func()) error {
	return holdProcess(ctx, ready, "/usr/bin/caffeinate", "-i", "/bin/sh", "-c", waitForHost)
}
