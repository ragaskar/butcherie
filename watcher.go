package butcherie

import (
	"os"
	"time"
)

// watchFirefox monitors the Firefox process and calls os.Exit(0) if it closes
// unexpectedly. It is launched as a goroutine by Start after Firefox is ready.
func (b *Butcherie) watchFirefox() {
	for {
		time.Sleep(3 * time.Second)
		b.mu.RLock()
		wd := b.wd
		b.mu.RUnlock()
		if wd == nil {
			return
		}
		if _, err := wd.Title(); err != nil {
			os.Exit(0)
		}
	}
}
