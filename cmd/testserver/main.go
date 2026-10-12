// Full credit to claude opus 4.8
package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var (
	file   = flag.String("file", "", "file to serve")
	seed   = flag.Uint64("seed", 1, "RNG seed for reproducible runs")
	jitter = flag.Float64("jitter", 0.4, "rate jitter fraction 0..1")
	rateMB = flag.Float64("rate", 3, "per-connection rate, MB/s (base)")
	addr   = flag.String("addr", ":8080", "listen address")
)

var (
	active atomic.Int64
	mu     sync.Mutex
	rng    *rand.Rand
)

func main() {
	flag.Parse()
	if *file == "" {
		log.Fatal("need -file")
	}

	logName := fmt.Sprintf("testserver-%s.log", time.Now().Format("20060102-150405"))
	lf, err := os.Create(logName)
	if err != nil {
		log.Fatal(err)
	}
	defer lf.Close()
	log.SetOutput(lf)
	fmt.Printf("serving %s on %s - %.1f MB/s/conn ±%.0f%% · logging to %s\n", *file, *addr, *rateMB, *jitter*100, logName)

	rng = rand.New(rand.NewPCG(*seed, 0))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open(*file)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()

		fi, _ := f.Stat()

		n := active.Add(1)
		defer active.Add(-1)

		mu.Lock()
		j := 1 + (rng.Float64()*2-1)**jitter
		mu.Unlock()

		bps := *rateMB * (1 << 20) * j

		tw := &throttled{ResponseWriter: w, bps: bps, start: time.Now()}
		http.ServeContent(tw, r, fi.Name(), fi.ModTime(), f)

		elapsed := time.Since(tw.start)
		achieved := 0.0
		if elapsed > 0 {
			achieved = float64(tw.sent) / elapsed.Seconds()
		}
		log.Printf("%-26s %7.2f MiB in %7s = %4.1f MB/s (target %.1f) active=%d",
			r.Header.Get("Range"), float64(tw.sent)/(1<<20),
			elapsed.Round(time.Millisecond), achieved/(1<<20), bps/(1<<20), n)
	})

	log.Fatal(http.ListenAndServe(*addr, nil))
}

type throttled struct {
	http.ResponseWriter
	bps   float64
	start time.Time
	sent  int64
}

func (t *throttled) Write(p []byte) (int, error) {
	const chunk = 32 << 10
	written := 0
	for len(p) > 0 {
		m := min(len(p), chunk)
		n, err := t.ResponseWriter.Write(p[:m])
		written += n
		t.sent += int64(n)
		expected := time.Duration(float64(t.sent) / t.bps * float64(time.Second))
		if ahead := expected - time.Since(t.start); ahead > 0 {
			time.Sleep(ahead)
		}
		if err != nil {
			return written, err
		}
		p = p[m:]
	}
	return written, nil
}
