package downloader

type ChunkDownloadMode uint8

const (
	HedgeMode  ChunkDownloadMode = 0
	NormalMode ChunkDownloadMode = 1
)

func isUsableTask(ct *ChunkTask) bool {
	if ct == nil {
		return false
	}

	if ct.Done.Load() {
		return false
	}

	if ct.Ctx != nil {
		select {
		case <-ct.Ctx.Done():
			return false
		default:
		}
	}
	return true
}

func (rdi *RangeDownloadInfo) pickTaskForWorker(worker *WorkerInfo) (task *ChunkTask, mode ChunkDownloadMode, ok bool) {
	switch rdi.CurHedgePhase {
	case HedgePhaseEarly:
		// Try normal queue first
		select {
		case ct := <-rdi.NormalQueue:
			if isUsableTask(ct) {
				return ct, NormalMode, true
			}
		case ticket := <-rdi.HedgeTicketQueue:
			if ticket != nil && isUsableTask(ticket.Task) && ticket.ChunkVersionSnapshot == ticket.Task.ChunkVersion.Load() {
				ticket.Task.Hedged.Store(true)
				return ticket.Task, HedgeMode, true
			} else {
				if rdi.StatusFlags.EnableTrace {
					rdi.Logger.Hedging.Printf("[Worker %2d::Chunk %4d] SKIP HEDGE | stale ticket version %d != current %d",
						worker.ID,
						ticket.Task.Index,
						ticket.ChunkVersionSnapshot,
						ticket.Task.ChunkVersion.Load())
				}
			}
		}
	case HedgePhaseLate:
		// Try hedge ticket queue first
		select {
		case ticket := <-rdi.HedgeTicketQueue:
			if ticket != nil && isUsableTask(ticket.Task) && ticket.ChunkVersionSnapshot == ticket.Task.ChunkVersion.Load() {
				ticket.Task.Hedged.Store(true)
				if rdi.StatusFlags.EnableTrace {
					rdi.Logger.Hedging.Printf("[Worker %2d::Chunk %4d] HEDGE ASSIGNED | Range: %d-%d | ReservationHead: %d",
						worker.ID,
						ticket.Task.Index,
						ticket.Task.Start,
						ticket.Task.End-1,
						ticket.Task.ReservationHead.Load())
				}
				return ticket.Task, HedgeMode, true
			} else {
				if rdi.StatusFlags.EnableTrace {
					rdi.Logger.Hedging.Printf("[Worker %2d::Chunk %4d] SKIP HEDGE | stale ticket version %d != current %d",
						worker.ID,
						ticket.Task.Index,
						ticket.ChunkVersionSnapshot,
						ticket.Task.ChunkVersion.Load())
				}
			}
		case t := <-rdi.NormalQueue:
			if isUsableTask(t) {
				return t, NormalMode, true
			}
		}
	}

	return nil, NormalMode, false
}
