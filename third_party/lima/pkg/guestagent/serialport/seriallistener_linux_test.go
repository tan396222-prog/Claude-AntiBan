package serialport

import (
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestListenerCloseInterruptsSerialRead(t *testing.T) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := Listen(fmt.Sprintf("/dev/pts/%d", number))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	// Closing a logical connection must keep the reusable device alive.
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	reading := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		close(reading)
		_, err := conn.Read(make([]byte, 1))
		finished <- err
	}()
	<-reading
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("closed port returned a successful read")
		}
	case <-time.After(time.Second):
		t.Fatal("listener close left a blocked serial read")
	}
	if _, err := listener.Accept(); err == nil {
		t.Fatal("closed listener accepted a connection")
	}
}

var _ net.Listener = (*SerialListener)(nil)
