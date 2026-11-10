package workbench

import (
	"testing"
	"time"
)

func TestWindowsAdmissionJoinsStartedTimeout(t *testing.T) {
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	timer := time.AfterFunc(0, func() { close(started); <-release; close(finished) })
	<-started
	result := make(chan bool)
	go func() { result <- cancelWindowsAdmissionDeadline(timer, finished) }()
	select {
	case <-result:
		t.Fatal("admission returned while late timeout could still kill relay")
	default:
	}
	close(release)
	if <-result {
		t.Fatal("expired connection was published")
	}
	select {
	case <-finished:
	default:
		t.Fatal("timeout callback not joined")
	}
}
