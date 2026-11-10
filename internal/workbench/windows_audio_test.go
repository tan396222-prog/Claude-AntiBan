package workbench

import (
	"bufio"
	"bytes"
	"io"
	"testing"
)

func TestWindowsAudioReadyPreservesBufferedPCM(t *testing.T) {
	pcm := []byte{0, 255, 1, 128, 0, 0, 7, 0}
	line := []byte("{\"ready\":true,\"format\":\"s16le\",\"sample_rate\":48000,\"channels\":2}\n")
	reader := bufio.NewReader(bytes.NewReader(append(line, pcm...)))
	if err := readAudioReady(reader); err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(actual, pcm) {
		t.Fatal("audio admission consumed or changed the first samples")
	}
	for _, response := range []string{"{\"ready\":false}\n", "{\"ready\":true,\"format\":\"s16le\",\"sample_rate\":44100,\"channels\":2}\n", "invalid\n", string(bytes.Repeat([]byte{'a'}, 8192)) + "\n"} {
		if readAudioReady(bufio.NewReaderSize(bytes.NewBufferString(response), 4096)) == nil {
			t.Fatal("invalid audio admission accepted")
		}
	}
}
