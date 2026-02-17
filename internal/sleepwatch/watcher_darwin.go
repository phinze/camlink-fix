package sleepwatch

import (
	"context"
	"log"
	"time"

	"github.com/prashantgupta24/mac-sleep-notifier/notifier"
)

// Watch returns a channel that receives a signal each time the machine wakes
// from sleep. The watcher stops when ctx is cancelled.
func Watch(ctx context.Context) <-chan struct{} {
	ch := make(chan struct{}, 1)

	go func() {
		n := notifier.GetInstance()
		wakeCh := n.Start()

		for {
			select {
			case <-ctx.Done():
				n.Quit()
				return
			case activity, ok := <-wakeCh:
				if !ok {
					return
				}
				if activity.Type == notifier.Awake {
					log.Printf("sleepwatch: wake detected at %s", time.Now().Format(time.RFC3339))
					select {
					case ch <- struct{}{}:
					default:
					}
				}
			}
		}
	}()

	return ch
}
