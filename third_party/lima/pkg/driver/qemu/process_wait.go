// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import "sync"

// watchQEMU reports process exit and network setup failures on the driver's
// existing wait channel. afterExit cancels setup and releases the netstack.
func watchQEMU(wait, configure func() error, afterExit func()) chan error {
	// Each producer can publish once even when Start's caller has stopped
	// receiving. Only the coordinator closes the channel, after both finish.
	errors := make(chan error, 2)
	var producers sync.WaitGroup
	producers.Add(2)
	go func() {
		defer producers.Done()
		err := wait()
		afterExit()
		errors <- err
	}()
	go func() {
		defer producers.Done()
		if err := configure(); err != nil {
			errors <- err
		}
	}()
	go func() { producers.Wait(); close(errors) }()
	return errors
}
