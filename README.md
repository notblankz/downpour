# downpour
A brutally fast, highly concurrent, multi-worker file downloader built in Go

---
### Build & Run
```bash
git clone "https://github.com/notblankz/downpour.git"
cd downpour/
go build -o downpour.exe

# Normal Download
.\downpour.exe "https://ash-speed.hetzner.com/1GB.bin"

# Enable Telemetry (Generates a CSV with telemetry data like speed/progress)
.\downpour.exe -tel "https://sin-speed.hetzner.com/1GB.bin"

# Generate HTTP Trace Logs (For deep network debugging):
.\downpour.exe -hl "https://example.com/file.zip"

# Show Help:
.\downpour.exe -h
```
---

### Current Status
- Status: ***Implemented a Health Monitor go routine that restarts slow workers***
    - Monitor smoothened worker speeds per worker for stable restart decisions
    - Calculates the trimmed mean speed across all the workers (15% trim each side) to exclude outliers
    - Restart workers slower than 0.3x trimmed mean speed in hopes of it being routed better and can perform better
    - Skip restarts in final 2% of download to avoid TCP slow-start at tail
    - 5s grace period per worker to allow TCP slow-start ramp up of the worker before evaluating it's speed
    - Non-blocking restart signal via buffered chan struct{} size 1 - this is to prevent health monitor blocking on a busy worker
- Current Throughput:  ***10GB sustained download at 22.86 MB/s average***
- Test Environment:
    - Tested on local machine *(AMD Ryzen 7 250 + 16GB RAM + RTX 5060 Laptop GPU)*
    - Test download: `"https://sin-speed.hetzner.com/10GB.bin"`
- Time Taken: ***448.55s*** *(12.8s faster than before)*

Below is the raw Global telemetry visualization from the 10GB stress test
![Downpour Global Telemetry Visualization as on 08 March 2026](docs/assets/10GB-10-March-Global.png)

Below is the telemetry for download speeds of each worker
![Downpour Worker Telemetry Visualization as on 08 March 2026](docs/assets/10GB-10-March-Worker.png)