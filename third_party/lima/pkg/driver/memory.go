// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package driver

import "context"

// MemoryBalloon adjusts guest memory within the configured boot capacity.
// Acceptance is asynchronous; callers must observe the guest before reclaiming more.
type MemoryBalloon interface {
	SetMemoryTarget(context.Context, uint64) error
}
