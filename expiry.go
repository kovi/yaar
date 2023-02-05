package main

import (
	"time"

	log "github.com/sirupsen/logrus"
)

func next() (bool, time.Time) {
	metalock.RLock()
	defer metalock.RUnlock()

	at := time.Time{}
	found := false
	for _, v := range metadb {
		if v.Expires == 0 {
			continue
		}

		if len(v.Locks) > 0 {
			continue
		}

		n := v.Added.Add(v.Expires)
		if !found || at.After(n) {
			at = n
			found = true
		}
	}

	return found, at
}

var timer = time.NewTimer(0)

func resetTimer() {
	wasRunning := timer.Stop()

	found, n := next()
	if !found {
		return
	}

	duration := time.Until(n)
	limit := 1 * time.Second
	if wasRunning && duration < limit {
		// wait at least 1 seconds before re-triggering
		duration = limit
	}

	log.Info("expiry: reseting timer at ", n, " (", duration, ")")
	timer.Reset(duration)
}

func StartExpiry() chan bool {

	resetTimer()

	quit := make(chan bool)
	go func() {
		for {
			select {
			case <-quit:
				return
			case <-timer.C:
				metalock.RLock()
				expired := make([]string, 0, 3)
				for k, v := range metadb {
					if len(v.Locks) > 0 {
						continue
					}

					if v.Expires == 0 {
						continue
					}

					exp := v.Added.Add(v.Expires)
					if exp.After(time.Now()) {
						continue
					}

					expired = append(expired, k)
				}
				metalock.RUnlock()

				for i := 0; i < len(expired); i++ {
					k := expired[i]
					log.Info("expired (", i, "): ", k)
					if err := Delete(k); err != nil {
						log.Info("error deleting expired key ", k, ": ", err)
					}
				}

				resetTimer()
			}
		}
	}()

	return quit
}
