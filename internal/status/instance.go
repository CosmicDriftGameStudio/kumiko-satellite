package status

import (
	"context"
	"net/http"
	"time"
)

const instanceHeader = "X-Kumiko-Satellite"

// IsSatellite reports whether a satellite already answers on addr, so a second
// start only has to open the browser.
func IsSatellite(addr string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.Header.Get(instanceHeader) == "1"
}
