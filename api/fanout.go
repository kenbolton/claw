// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"encoding/json"
	"sync"

	"github.com/kenbolton/claw/driver"
)

// FanOutResult holds the result of a single driver invocation.
type FanOutResult struct {
	Driver   *driver.Driver
	Messages []map[string]interface{}
	Err      error
}

// fanOut sends a request to multiple drivers concurrently and collects responses.
func fanOut(drivers []*driver.Driver, buildReq func(d *driver.Driver) map[string]interface{}) []FanOutResult {
	results := make([]FanOutResult, len(drivers))
	var wg sync.WaitGroup

	for i, d := range drivers {
		wg.Add(1)
		go func(idx int, drv *driver.Driver) {
			defer wg.Done()
			results[idx].Driver = drv

			req := buildReq(drv)
			scanner, wait, err := drv.SendRequestAndClose(req)
			if err != nil {
				results[idx].Err = err
				return
			}

			for scanner.Scan() {
				var msg map[string]interface{}
				if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
					continue
				}
				results[idx].Messages = append(results[idx].Messages, msg)
			}
			if err := scanner.Err(); err != nil {
				results[idx].Err = err
			}
			_ = wait()
		}(i, d)
	}

	wg.Wait()
	return results
}
