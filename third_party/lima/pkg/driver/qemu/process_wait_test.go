// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"errors"
	"testing"
	"time"
)

func TestQEMUProcessExitDuringNetworkConfiguration(t *testing.T) {
	processError := errors.New("WHPX: Failed to enable nested virtualization, hr=80370302")
	networkError := errors.New("network setup ended after QEMU exit")
	release := make(chan struct{})
	cleaned := make(chan struct{})
	results := watchQEMU(func() error { return processError }, func() error { <-release; return networkError }, func() { close(cleaned) })
	select {
	case err := <-results:
		if !errors.Is(err, processError) {
			t.Fatalf("lost original process error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("process exit was not reported")
	}
	<-cleaned
	close(release)
	select {
	case err, ok := <-results:
		if !ok || !errors.Is(err, networkError) {
			t.Fatalf("closed before the network producer completed: %v %v", ok, err)
		}
	case <-time.After(time.Second):
		t.Fatal("late network setup failure blocked")
	}
	select {
	case _, ok := <-results:
		if ok {
			t.Fatal("unexpected extra event")
		}
	case <-time.After(time.Second):
		t.Fatal("finished producers did not close the wait channel")
	}
}

func TestQEMUNetworkFailureDoesNotBlockProcessReaping(t *testing.T) {
	processDone := make(chan struct{})
	reaped := make(chan struct{})
	results := watchQEMU(func() error { <-processDone; return errors.New("killed") }, func() error { return errors.New("network failed") }, func() { close(reaped) })
	// Stop consuming after the first startup error, just as a failed Start
	// caller can do. Both producers must still finish and reap the process.
	<-results
	close(processDone)
	select {
	case <-reaped:
	case <-time.After(time.Second):
		t.Fatal("process cleanup blocked behind an abandoned consumer")
	}
	for range results {
	}
}
