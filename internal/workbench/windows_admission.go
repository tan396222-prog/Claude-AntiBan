package workbench

import "time"

// A terminal or project may be published only after its timeout was cancelled.
// Stop(false) also means a callback is running; join it before reporting failure.
func cancelWindowsAdmissionDeadline(timer *time.Timer, finished <-chan struct{}) bool {
	if timer.Stop() {
		return true
	}
	<-finished
	return false
}
