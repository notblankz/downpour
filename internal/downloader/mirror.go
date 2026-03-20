package downloader

import (
	"net/url"
	"sync/atomic"
)

type MirrorInfo struct {
	URL           string
	Speed         float64
	ActiveWorkers atomic.Int32
	Failures      atomic.Int32
	Dead          atomic.Bool
}

func (rdi *RangeDownloadInfo) rebalanceMirrors(mirrorWorkerMap map[*MirrorInfo][]*WorkerInfo) {
	// find fastest live mirror
	var fastest *MirrorInfo
	for _, mi := range rdi.Mirrors {
		if mi.Dead.Load() {
			continue
		}
		if fastest == nil || mi.Speed > fastest.Speed {
			fastest = mi
		}
	}

	if fastest == nil {
		return
	}

	for _, mi := range rdi.Mirrors {
		if mi.Dead.Load() || mi == fastest {
			continue
		}

		if mi.Speed >= 0.5*fastest.Speed {
			continue
		}

		workers := mirrorWorkerMap[mi]
		if len(workers) <= 1 {
			continue
		}

		// find one idle worker to reassign
		for _, wi := range workers {
			if wi.Status == WorkerStatusIdle {
				fromHost := mirrorHost(mi.URL)
				toHost := mirrorHost(fastest.URL)
				wi.Mirror = fastest
				rdi.Logger.Mirrors.Printf("[INFO] [Mirror] REBALANCE | worker=%d | from=%s | to=%s", wi.ID, fromHost, toHost)
				mi.ActiveWorkers.Add(-1)
				fastest.ActiveWorkers.Add(1)
				break
			}
		}
	}
}

func mirrorHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	host := parsed.Hostname()
	if host == "" {
		return rawURL
	}
	return host
}
