package logger

import (
	"fmt"
	"log"
	"sync"
	"time"
)

var dedup = &deduplicator{
	flushDelay: 2 * time.Second,
}

type deduplicator struct {
	mu         sync.Mutex
	lastMsg    string
	count      int
	flushDelay time.Duration
	timer      *time.Timer
}

func (d *deduplicator) flush() {
	if d.count == 0 {
		return
	}
	if d.count == 1 {
		log.Print(d.lastMsg)
	} else {
		log.Printf("%s (%d)", d.lastMsg, d.count)
	}
	d.count = 0
	d.lastMsg = ""
}

func Dedup(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)

	dedup.mu.Lock()
	defer dedup.mu.Unlock()

	if msg == dedup.lastMsg {
		dedup.count++
		if dedup.timer != nil {
			dedup.timer.Stop()
		}
		dedup.timer = time.AfterFunc(dedup.flushDelay, func() {
			dedup.mu.Lock()
			defer dedup.mu.Unlock()
			dedup.flush()
		})
		return
	}

	dedup.flush()
	dedup.lastMsg = msg
	dedup.count = 1
	dedup.timer = time.AfterFunc(dedup.flushDelay, func() {
		dedup.mu.Lock()
		defer dedup.mu.Unlock()
		dedup.flush()
	})
}
