//go:build !windows

// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package hostagent

import "os/exec"

func hideResourceProbe(*exec.Cmd) {}
