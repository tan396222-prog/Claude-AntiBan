package containerenv

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
)

// A compromised guest can replace its helper. Validate framing again before
// the shared netstack allocates packets or indexes the Ethernet header.
type boundedFrameConnection struct {
	net.Conn
	pending []byte
}

func (c *boundedFrameConnection) Read(output []byte) (int, error) {
	if len(output) == 0 {
		return 0, nil
	}
	if len(c.pending) == 0 {
		buffer := make([]byte, maxFrameSize+4)
		frame, err := readFrame(c.Conn, buffer[4:])
		if err != nil {
			return 0, err
		}
		mac, _ := net.ParseMAC(GuestMAC)
		if !bytes.Equal(frame[6:12], mac) {
			return 0, errors.New("unexpected container Ethernet source")
		}
		binary.BigEndian.PutUint32(buffer[:4], uint32(len(frame)))
		c.pending = buffer[:4+len(frame)]
	}
	n := copy(output, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

// A TAP read returns exactly one Ethernet frame. Jumbo frames are disabled;
// the small allowance includes Ethernet/VLAN headers above the configured MTU.
const maxFrameSize = NetworkMTU + 64

func writeFrame(writer io.Writer, frame []byte) error {
	if len(frame) < 14 || len(frame) > maxFrameSize {
		return errors.New("invalid container Ethernet frame length")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(frame)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, frame)
}

func readFrame(reader io.Reader, buffer []byte) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size < 14 || size > maxFrameSize || int(size) > len(buffer) {
		return nil, errors.New("invalid container Ethernet frame length")
	}
	frame := buffer[:int(size)]
	_, err := io.ReadFull(reader, frame)
	return frame, err
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if n < 0 || n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
