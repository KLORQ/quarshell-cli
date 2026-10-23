package cli

import (
	"os"
	"testing"
	"time"
)

func TestWatchParentLivenessTerminatesOnWriterClose(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	terminated := make(chan struct{})
	watchParentLiveness(reader, func() { close(terminated) })
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-terminated:
	case <-time.After(time.Second):
		t.Fatal("parent liveness watcher did not terminate after EOF")
	}
}
